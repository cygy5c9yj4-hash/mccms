package web

import (
	"bytes"
	"crypto"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mccms/mccms-go/internal/account"
)

// 内容门禁相关设置键。
const (
	settingVipFreeComics = "vip_free_comics" // 免费本子白名单，逗号分隔
	settingVipMonthPrice = "vip_month_price" // 会员月费（元），用于按实付金额折算会员天数；空或 0 表示不启用
	stNeedVIP            = 1402              // 需要 VIP 权限
)

var (
	vipMu     sync.Mutex
	vipBySrv  = map[*Server]*account.VipManager{}
	vipErrSrv = map[*Server]error{}
)

// vipManager 惰性构建卡密/订单管理器。存储路径来自 MCCMS_VIP_STORE，
// 未设置时落在账号数据目录（MCCMS_DATA_DIR）或当前目录。
func (s *Server) vipManager() (*account.VipManager, error) {
	vipMu.Lock()
	defer vipMu.Unlock()
	if m, ok := vipBySrv[s]; ok {
		return m, vipErrSrv[s]
	}
	svc := s.accountService()
	if svc == nil {
		err := account.ErrUnauthenticated
		vipBySrv[s], vipErrSrv[s] = nil, err
		return nil, err
	}
	path := strings.TrimSpace(os.Getenv("MCCMS_VIP_STORE"))
	if path == "" {
		if d := strings.TrimSpace(os.Getenv("MCCMS_DATA_DIR")); d != "" {
			path = filepath.Join(d, "vip.json")
		} else {
			path = "mccms-vip.json"
		}
	}
	m, err := account.NewVipManager(svc.Store(), path)
	vipBySrv[s], vipErrSrv[s] = m, err
	return m, err
}

func (s *Server) freeComicSet() map[string]bool {
	out := map[string]bool{}
	svc := s.accountService()
	if svc == nil {
		return out
	}
	raw, err := svc.Store().GetSetting(settingVipFreeComics)
	if err != nil {
		return out
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if p := strings.TrimSpace(part); p != "" {
			out[p] = true
		}
	}
	return out
}

func (s *Server) isFreeComic(id string) bool {
	if id == "" {
		return false
	}
	return s.freeComicSet()[id]
}

// isFreeComicFor 判断某站点下的作品是否在免费白名单内。
// 白名单条目支持 "site:id" 与纯 "id" 两种写法（跨源白名单用前者）。
func (s *Server) isFreeComicFor(site, id string) bool {
	if id == "" {
		return false
	}
	set := s.freeComicSet()
	if set[id] {
		return true
	}
	return site != "" && set[site+":"+id]
}

func firstQuery(r *http.Request, keys ...string) string {
	q := r.URL.Query()
	for _, k := range keys {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

// extractComicID 从漫画/章节路径中取出漫画 ID。
func extractComicID(path string) string {
	p := path
	for _, prefix := range []string{"/api/v2/jm/comic/", "/api/v2/jm/chapter/"} {
		if strings.HasPrefix(p, prefix) {
			p = strings.TrimPrefix(p, prefix)
			break
		}
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return strings.TrimSpace(p)
}

// vipGate 保护内容端点：VIP 或免费白名单内的内容可访问，否则返回需要开通 VIP。
func (s *Server) vipGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, err := s.currentUser(r); err == nil && u != nil {
			if u.EffectiveTier(time.Now()) == account.TierVIP {
				next(w, r)
				return
			}
		}
		id := extractComicID(r.URL.Path)
		if id == "" || id == "chapter" {
			id = firstQuery(r, "comic_id", "comicId", "cid", "comic")
		}
		if id != "" && s.isFreeComicFor(firstQuery(r, "site"), id) {
			next(w, r)
			return
		}
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"st":  stNeedVIP,
			"msg": "该内容需要 VIP 权限。可通过爱发电支持后获取卡密，或在「我的」中兑换卡密开通。",
			"data": map[string]any{
				"need_vip": true,
				"comic_id": id,
			},
		})
	}
}

// ---- 用户侧接口 ----

func (s *Server) handleVipStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": 1, "msg": "方法不允许"})
		return
	}
	svc := s.accountService()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"st": 1, "msg": "账号体系未启用"})
		return
	}
	now := time.Now()
	resp := map[string]any{
		"free_comics": sortedKeys(s.freeComicSet()),
		"tier":        string(account.TierFree),
		"active":      false,
	}
	if u, err := s.currentUser(r); err == nil && u != nil {
		tier := u.EffectiveTier(now)
		resp["tier"] = string(tier)
		resp["active"] = tier == account.TierVIP
		resp["username"] = u.Username
		if u.TierExpiresAt != nil {
			resp["expires_at"] = u.TierExpiresAt
			if tier == account.TierVIP {
				resp["days_left"] = int(time.Until(*u.TierExpiresAt).Hours()/24) + 1
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": resp})
}

func (s *Server) handleVipRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": 1, "msg": "方法不允许"})
		return
	}
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"st": 1, "msg": "卡密功能未启用"})
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": "请求格式不正确"})
		return
	}
	code, exp, err := m.Redeem(u, body.Code)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
		return
	}
	_ = s.accountService().LogAudit(u, "vip.redeem", u.ID, u.Username, "兑换卡密 "+code.Code+"，+ "+strconv.Itoa(code.Days)+" 天", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{
		"tier":       string(account.TierVIP),
		"days":       code.Days,
		"expires_at": exp,
	}})
}

// ---- 管理端接口 ----

func (s *Server) handleAdminVipCodes(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"st": 1, "msg": "卡密功能未启用"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		list := m.ListCodes()
		writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{"list": list, "total": len(list)}})
	case http.MethodPost:
		var body struct {
			Count int    `json:"count"`
			Days  int    `json:"days"`
			Note  string `json:"note"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": "请求格式不正确"})
			return
		}
		created, err := m.CreateCodes(actor, body.Count, body.Days, body.Note)
		if err != nil {
			writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
			return
		}
		_ = s.accountService().LogAudit(actor, "vip.codes.create", "", "", "生成 "+strconv.Itoa(len(created))+" 张卡密（"+strconv.Itoa(body.Days)+" 天）", clientIP(r))
		writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{"list": created, "count": len(created)}})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": 1, "msg": "方法不允许"})
	}
}

func (s *Server) handleAdminVipCodeAction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"st": 1, "msg": "卡密功能未启用"})
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/vip/codes/"), "/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": "缺少卡密 ID"})
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := m.DeleteCode(id); err != nil {
			writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
			return
		}
		_ = s.accountService().LogAudit(actor, "vip.codes.delete", id, "", "删除卡密", clientIP(r))
	case http.MethodPost:
		// 作废
		if err := m.RevokeCode(id); err != nil {
			writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
			return
		}
		_ = s.accountService().LogAudit(actor, "vip.codes.revoke", id, "", "作废卡密", clientIP(r))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": 1, "msg": "方法不允许"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK})
}

func (s *Server) handleAdminVipOrders(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"st": 1, "msg": "卡密功能未启用"})
		return
	}
	list := m.ListOrders()
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{"list": list, "total": len(list)}})
}

func (s *Server) handleAdminVipOrderAction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": 1, "msg": "方法不允许"})
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"st": 1, "msg": "卡密功能未启用"})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/vip/orders/"), "/")
	parts := strings.Split(rest, "/")
	orderID := parts[0]
	if orderID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": "缺少订单号"})
		return
	}
	if len(parts) < 2 || parts[1] != "bind" {
		writeJSON(w, http.StatusNotFound, map[string]any{"st": 1, "msg": "未知操作"})
		return
	}
	var body struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": "请求格式不正确"})
		return
	}
	target, err := s.resolveUser(body.UserID, body.Username)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
		return
	}
	ord, exp, err := m.LinkOrder(orderID, target.ID)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
		return
	}
	_ = s.accountService().LogAudit(actor, "vip.order.bind", target.ID, target.Username,
		"绑定订单 "+orderID+"（+"+strconv.Itoa(ord.Days)+" 天）", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{
		"order":      ord,
		"user_id":    target.ID,
		"username":   target.Username,
		"expires_at": exp,
	}})
}

func (s *Server) handleAdminVipGrant(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": 1, "msg": "方法不允许"})
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"st": 1, "msg": "卡密功能未启用"})
		return
	}
	var body struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		Days     int    `json:"days"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": "请求格式不正确"})
		return
	}
	target, err := s.resolveUser(body.UserID, body.Username)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
		return
	}
	u, exp, err := m.Grant(target.ID, body.Days)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]any{"st": 1, "msg": err.Error()})
		return
	}
	_ = s.accountService().LogAudit(actor, "vip.grant", u.ID, u.Username,
		"手动发放 VIP "+strconv.Itoa(body.Days)+" 天", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{
		"user_id":    u.ID,
		"username":   u.Username,
		"tier":       string(account.TierVIP),
		"expires_at": exp,
	}})
}

func (s *Server) handleAdminVipSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	svc := s.accountService()
	switch r.Method {
	case http.MethodGet:
		free, _ := svc.Store().GetSetting(settingVipFreeComics)
		price, _ := svc.Store().GetSetting(settingVipMonthPrice)
		writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{
			"free_comics": free,
			"month_price": price,
		}})
	case http.MethodPost:
		var body struct {
			// 用指针区分「没传」和「传了空值」，这样才能只更新其中一个字段，
			// 让「保存白名单」和「保存月费」互不覆盖。
			FreeComics *string `json:"free_comics"`
			MonthPrice *string `json:"month_price"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": "请求格式不正确"})
			return
		}
		data := map[string]any{}
		if body.MonthPrice != nil {
			clean, err := normalizeMonthPrice(*body.MonthPrice)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"st": 1, "msg": err.Error()})
				return
			}
			if err := svc.Store().SetSetting(settingVipMonthPrice, clean); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"st": 1, "msg": err.Error()})
				return
			}
			data["month_price"] = clean
			_ = svc.LogAudit(actor, "vip.settings", "", "", "更新会员月费: "+clean+" 元", clientIP(r))
		}
		if body.FreeComics != nil {
			clean := strings.Join(strings.FieldsFunc(*body.FreeComics, func(r rune) bool {
				return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
			}), ",")
			if err := svc.Store().SetSetting(settingVipFreeComics, clean); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"st": 1, "msg": err.Error()})
				return
			}
			data["free_comics"] = clean
			_ = svc.LogAudit(actor, "vip.settings", "", "", "更新免费本子白名单: "+clean, clientIP(r))
		}
		writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": data})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": 1, "msg": "方法不允许"})
	}
}

// resolveUser 允许管理员用 user_id 或用户名指定目标账号。
func (s *Server) resolveUser(userID, username string) (*account.User, error) {
	svc := s.accountService()
	store := svc.Store()
	if strings.TrimSpace(userID) != "" {
		return store.UserByID(strings.TrimSpace(userID))
	}
	name := account.NormalizeUsername(username)
	if name == "" {
		return nil, account.ErrInvalid
	}
	users, err := store.ListUsers()
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if account.NormalizeUsername(u.Username) == name {
			return u, nil
		}
	}
	return nil, account.ErrNotFound
}

// ---- 爱发电回调 ----

// afdianPublicKeyPEM 是爱发电 Webhook 签名校验使用的平台公钥（2025-07 起启用）。
// 可用 MCCMS_AFDIAN_PUBKEY 覆盖（PEM 内容或文件路径）。
const afdianPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwwdaCg1Bt+UKZKs0R54y
lYnuANma49IpgoOwNmk3a0rhg/PQuhUJ0EOZSowIC44l0K3+fqGns3Ygi4AfmEfS
4EKbdk1ahSxu7Zkp2rHMt+R9GarQFQkwSS/5x1dYiHNVMiR8oIXDgjmvxuNes2Cr
8fw9dEF0xNBKdkKgG2qAawcN1nZrdyaKWtPVT9m2Hl0ddOO9thZmVLFOb9NVzgYf
jEgI+KWX6aY19Ka/ghv/L4t1IXmz9pctablN5S0CRWpJW3Cn0k6zSXgjVdKm4uN7
jRlgSRaf/Ind46vMCm3N2sgwxu/g3bnooW+db0iLo13zzuvyn727Q3UDQ0MmZcEW
MQIDAQAB
-----END PUBLIC KEY-----`

func (s *Server) handleAfdianWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ec": 405, "em": "method not allowed"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ec": 400, "em": "读取请求失败"})
		return
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ec": 400, "em": "JSON 解析失败"})
		return
	}
	data, _ := root["data"].(map[string]any)
	if data == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ec": 400, "em": "缺少 data"})
		return
	}
	if typ, _ := data["type"].(string); typ != "" && typ != "order" {
		writeJSON(w, http.StatusOK, map[string]any{"ec": 200, "em": ""})
		return
	}
	items := afdianWebhookItems(data)
	if len(items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ec": 400, "em": "缺少订单信息"})
		return
	}
	if !s.verifyAfdianWebhook(data, items) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ec": 403, "em": "签名校验失败"})
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ec": 500, "em": "卡密功能未启用"})
		return
	}
	daysPerMonth := afdianDaysPerMonth()
	monthPrice := s.vipMonthPriceCents()
	for _, item := range items {
		ord := afdianOrderFromItem(item, daysPerMonth, monthPrice)
		if ord == nil {
			continue
		}
		created, err := m.RecordOrder(ord)
		if err != nil || !created {
			continue
		}
		s.creditOrder(m, ord, nil, "vip.order.auto", r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ec": 200, "em": ""})
}

// afdianWebhookItems 从回调 data 中取出订单对象。
// Webhook 推送为 data.order（单个对象）；OpenAPI query-order 返回 data.list[]。
func afdianWebhookItems(data map[string]any) []map[string]any {
	if order, ok := data["order"].(map[string]any); ok && len(order) > 0 {
		return []map[string]any{order}
	}
	raw, _ := data["list"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		if m, ok := v.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}

// afdianOrderFromItem 把一条爱发电订单解析成内部订单。
//
// 天数规则（monthPriceCents 为后台设置的月费，单位「分」；0 表示未启用）：
//   - 未启用月费：按爱发电给出的方案月数 × daysPerMonth（旧行为）；
//   - 已启用且实付为正：只给整月，月数 = 实付 ÷ 月费 向下取整，不足一个月不发放；
//   - 已启用但实付为 0：兑换码 / 赠送订单（爱发电文档：使用兑换码时 total_amount 为 0.00），
//     按方案月数发放，否则「兑换链接」送出的免费月份会被吞掉。
func afdianOrderFromItem(item map[string]any, daysPerMonth int, monthPriceCents int64) *account.AfdianOrder {
	orderID := strVal(item, "order_id")
	if orderID == "" {
		orderID = strVal(item, "out_trade_no")
	}
	if orderID == "" {
		return nil
	}
	// status = 2 表示交易成功。非成功订单一律忽略，避免白送 VIP。
	status := intVal(item, "status")
	if status != 2 {
		return nil
	}
	months := intVal(item, "month")
	if months <= 0 {
		months = 1
	}
	amountRaw := strVal(item, "total_amount")
	days, daysNote := afdianOrderDays(amountRaw, months, daysPerMonth, monthPriceCents)
	return &account.AfdianOrder{
		OrderID:      orderID,
		OutTradeNo:   strVal(item, "out_trade_no"),
		AfdianUserID: strVal(item, "user_id"),
		// user_private_id 是跨主体用户标识，与 user_id 一起用于识别捐赠者。
		UserPrivateID: strVal(item, "user_private_id"),
		// custom_order_id 是「自定义信息」，结构化且可设为必填，首次识别优先用它。
		CustomOrderID: strVal(item, "custom_order_id"),
		RedeemID:      strVal(item, "redeem_id"),
		PlanID:        strVal(item, "plan_id"),
		PlanTitle:     strVal(item, "plan_title"),
		Months:        months,
		Days:          days,
		DaysNote:      daysNote,
		Amount:        amountRaw,
		Remark:        strVal(item, "remark"),
		Status:        status,
		ReceivedAt:    time.Now(),
	}
}

// afdianOrderDays 依据后台设置的月费把订单金额折算成会员天数，并返回一句可读的说明。
func afdianOrderDays(amountRaw string, months, daysPerMonth int, monthPriceCents int64) (int, string) {
	if monthPriceCents <= 0 {
		return months * daysPerMonth, fmt.Sprintf("未设置月费，按方案月数 %d 个月", months)
	}
	amountCents := parseYuanToCents(amountRaw)
	if amountCents <= 0 {
		// 实付为 0：兑换码 / 赠送订单，按爱发电给出的方案月数发放。
		return months * daysPerMonth, fmt.Sprintf("兑换/赠送订单（实付 0 元），按方案月数 %d 个月", months)
	}
	n := int(amountCents / monthPriceCents)
	if n <= 0 {
		return 0, fmt.Sprintf("实付 %s 元不足月费 %s 元，未发放", strings.TrimSpace(amountRaw), yuanOfCents(monthPriceCents))
	}
	return n * daysPerMonth, fmt.Sprintf("实付 %s 元 ÷ 月费 %s 元 = %d 个月", strings.TrimSpace(amountRaw), yuanOfCents(monthPriceCents), n)
}

// parseYuanToCents 把「元」字符串转成「分」。空值或无法解析时返回 0，表示没有金额。
func parseYuanToCents(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int64(math.Round(f * 100))
}

// yuanOfCents 把「分」格式化成两位小数的「元」字符串，仅用于展示。
func yuanOfCents(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

// normalizeMonthPrice 校验并规范化月费设置。空串或 0 表示停用按月费折算。
func normalizeMonthPrice(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f < 0 {
		return "", fmt.Errorf("月费必须是不小于 0 的数字")
	}
	if f < 0.01 {
		return "", nil
	}
	return yuanOfCents(int64(math.Round(f * 100))), nil
}

// vipMonthPriceCents 读取后台设置的会员月费（单位：分）。0 表示未启用金额折算。
func (s *Server) vipMonthPriceCents() int64 {
	svc := s.accountService()
	if svc == nil {
		return 0
	}
	raw, _ := svc.Store().GetSetting(settingVipMonthPrice)
	return parseYuanToCents(raw)
}

// maskAfdianUID 只保留爱发电 ID 的首尾各 4 位，用于前端展示。
func maskAfdianUID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 10 {
		return id[:1] + "****"
	}
	return id[:4] + "****" + id[len(id)-4:]
}

// resolveOrderUser 按「已绑定账号 → 自定义信息 → 备注」的顺序确定订单归属的本站用户。
//
// 首次识别优先读 custom_order_id（结构化、可设为必填），其次才是自由留言；
// 识别成功后会写下 AfdianUserID，之后该爱发电账号的订单直接命中第一级。
func (s *Server) resolveOrderUser(ord *account.AfdianOrder) *account.User {
	svc := s.accountService()
	if svc == nil {
		return nil
	}
	// 1) 已绑定的爱发电账号：一次绑定后永久自动归户。
	if u := svc.UserByAfdianUID(ord.AfdianUserID); u != nil {
		return u
	}
	if u := svc.UserByAfdianUID(ord.UserPrivateID); u != nil {
		return u
	}
	// 2) 自定义信息（custom_order_id）。
	if u := s.matchOrderUser(ord.CustomOrderID); u != nil {
		return u
	}
	// 3) 备注兜底。
	return s.matchOrderUser(ord.Remark)
}

// creditOrder 记录订单、（幂等地）发放权益，并把爱发电账号绑定到目标用户。
// 返回 true 表示本次调用确实发放了权益。actor 可为 nil（平台回调没有操作者）。
func (s *Server) creditOrder(m *account.VipManager, ord *account.AfdianOrder, actor *account.User, action string, r *http.Request) bool {
	if m == nil {
		return false
	}
	target := s.resolveOrderUser(ord)
	if target == nil {
		return false
	}
	if _, _, err := m.LinkOrder(ord.OrderID, target.ID); err != nil {
		return false
	}
	svc := s.accountService()
	if svc != nil {
		uid := ord.AfdianUserID
		if uid == "" {
			uid = ord.UserPrivateID
		}
		// 写出绑定：之后该爱发电账号的订单不再需要备注。
		if uid != "" && target.AfdianUserID != uid {
			_ = svc.BindAfdian(target.ID, uid)
		}
		_ = svc.LogAudit(actor, action, target.ID, target.Username,
			"爱发电订单 "+ord.OrderID+"（"+ord.Amount+" 元，+"+strconv.Itoa(ord.Days)+" 天）", clientIP(r))
	}
	return true
}

// matchOrderUser 在订单备注中查找本站用户名，用于自动发放。
func (s *Server) matchOrderUser(remark string) *account.User {
	remark = strings.TrimSpace(remark)
	if remark == "" {
		return nil
	}
	svc := s.accountService()
	if svc == nil {
		return nil
	}
	users, err := svc.Store().ListUsers()
	if err != nil {
		return nil
	}
	lower := strings.ToLower(remark)
	var best *account.User
	for _, u := range users {
		name := strings.ToLower(strings.TrimSpace(u.Username))
		if len(name) < 3 {
			continue
		}
		if strings.Contains(lower, name) {
			if best == nil || len(name) > len(best.Username) {
				best = u
			}
		}
	}
	return best
}

func afdianDaysPerMonth() int {
	if v := strings.TrimSpace(os.Getenv("MCCMS_AFDIAN_DAYS_PER_MONTH")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 31
}

// verifyAfdianWebhook 校验爱发电 Webhook 的 RSA-SHA256 签名。
// 待签串为 out_trade_no + user_id + plan_id + total_amount，签名字段是 data.sign（Base64）。
func (s *Server) verifyAfdianWebhook(data map[string]any, items []map[string]any) bool {
	if strings.TrimSpace(os.Getenv("MCCMS_AFDIAN_INSECURE")) == "1" {
		return true
	}
	signB64 := strings.TrimSpace(strVal(data, "sign"))
	if signB64 == "" {
		return false
	}
	key, err := afdianPublicKey()
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(signB64)
	if err != nil {
		return false
	}
	for _, item := range items {
		msg := strVal(item, "out_trade_no") + strVal(item, "user_id") + strVal(item, "plan_id") + strVal(item, "total_amount")
		sum := sha256.Sum256([]byte(msg))
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) == nil {
			return true
		}
	}
	return false
}

func afdianPublicKey() (*rsa.PublicKey, error) {
	pemStr := strings.TrimSpace(os.Getenv("MCCMS_AFDIAN_PUBKEY"))
	if pemStr == "" {
		pemStr = afdianPublicKeyPEM
	} else if !strings.Contains(pemStr, "BEGIN PUBLIC KEY") {
		if b, err := os.ReadFile(pemStr); err == nil {
			pemStr = string(b)
		}
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("afdian: 公钥解析失败")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("afdian: 公钥类型不是 RSA")
	}
	return rsaPub, nil
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ---- 爱发电 OpenAPI（主动对账）----

// afdianAPICall 用 user_id + token 的 md5 签名规则调用爱发电开放接口。
// 签名：sign = md5(token + "params" + params + "ts" + ts + "user_id" + user_id)
func afdianAPICall(endpoint, paramsJSON string) (map[string]any, error) {
	token := strings.TrimSpace(os.Getenv("MCCMS_AFDIAN_TOKEN"))
	userID := strings.TrimSpace(os.Getenv("MCCMS_AFDIAN_USER_ID"))
	if token == "" || userID == "" {
		return nil, errors.New("未配置 MCCMS_AFDIAN_TOKEN / MCCMS_AFDIAN_USER_ID")
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sign := md5Hex(token + "params" + paramsJSON + "ts" + ts + "user_id" + userID)
	form := url.Values{}
	form.Set("user_id", userID)
	form.Set("params", paramsJSON)
	form.Set("ts", ts)
	form.Set("sign", sign)
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// handleAdminVipSync 通过爱发电 OpenAPI 拉取历史订单并对账发放。
func (s *Server) handleAdminVipSync(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	m, err := s.vipManager()
	if err != nil || m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "卡密功能未启用"})
		return
	}
	pages := 1
	if v := strings.TrimSpace(r.URL.Query().Get("pages")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			pages = n
		}
	}
	daysPerMonth := afdianDaysPerMonth()
	monthPrice := s.vipMonthPriceCents()
	fetched, created := 0, 0
	for page := 1; page <= pages; page++ {
		out, err := afdianAPICall("https://afdian.com/api/open/query-order", fmt.Sprintf("{\"page\":%d}", page))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if ec := intVal(out, "ec"); ec != 200 {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "ec": ec, "error": strVal(out, "em")})
			return
		}
		data, _ := out["data"].(map[string]any)
		list, _ := data["list"].([]any)
		for _, raw := range list {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			ord := afdianOrderFromItem(item, daysPerMonth, monthPrice)
			if ord == nil {
				continue
			}
			fetched++
			isNew, err := m.RecordOrder(ord)
			if err != nil || !isNew {
				continue
			}
			created++
			s.creditOrder(m, ord, actor, "vip.order.sync", r)
		}
		if tp := intVal(data, "total_page"); tp > 0 && page >= tp {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fetched": fetched, "created": created})
}

// handleAdminVipPing 校验爱发电 token/签名配置是否正确。
func (s *Server) handleAdminVipPing(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	// 爱发电没有独立的 ping 端点，用 query-order 第 1 页做连通性与签名校验。
	out, err := afdianAPICall("https://afdian.com/api/open/query-order", "{\"page\":1}")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ec := intVal(out, "ec")
	if ec != 200 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "ec": ec, "em": strVal(out, "em")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ec": ec, "em": strVal(out, "em")})
}

func marshalNoEscape(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case json.Number:
			return t.String()
		}
	}
	return ""
}

func intVal(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
		case json.Number:
			if n, err := t.Int64(); err == nil {
				return int(n)
			}
		}
	}
	return 0
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func statusForError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, account.ErrNotFound.Error()), strings.Contains(msg, "卡密不存在"):
		return http.StatusNotFound
	case strings.Contains(msg, "已被使用"), strings.Contains(msg, "已发放"), strings.Contains(msg, "已使用"):
		return http.StatusConflict
	case strings.Contains(msg, account.ErrInvalid.Error()):
		return http.StatusBadRequest
	case strings.Contains(msg, account.ErrUnauthenticated.Error()):
		return http.StatusUnauthorized
	case strings.Contains(msg, account.ErrForbidden.Error()):
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}
