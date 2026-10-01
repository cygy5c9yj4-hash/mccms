package account

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const vipFileVersion = 1

// RedeemCode 是一张一次性 VIP 卡密。卡密兑换后立即消费，不可重复使用。
type RedeemCode struct {
	ID         string     `json:"id"`
	Code       string     `json:"code"`
	Note       string     `json:"note,omitempty"`
	Days       int        `json:"days"`
	Batch      string     `json:"batch,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	CreatedBy  string     `json:"created_by,omitempty"`
	UsedBy     string     `json:"used_by,omitempty"`
	UsedByName string     `json:"used_by_name,omitempty"`
	UsedAt     *time.Time `json:"used_at,omitempty"`
	Revoked    bool       `json:"revoked,omitempty"`
}

// Usable 报告卡密当前是否可以被兑换。
func (c *RedeemCode) Usable() bool {
	return c != nil && !c.Revoked && c.UsedAt == nil
}

// AfdianOrder 记录一笔已处理的爱发电订单，用于幂等与审计。
type AfdianOrder struct {
	OrderID      string `json:"order_id"`
	OutTradeNo   string `json:"out_trade_no,omitempty"`
	AfdianUserID string `json:"afdian_user_id,omitempty"`
	// UserPrivateID 是爱发电的跨主体用户标识（等价于微信 unionid），
	// 优先于 user_id 用于识别同一捐赠者。
	UserPrivateID string `json:"user_private_id,omitempty"`
	// CustomOrderID 是订单的「自定义信息」，售卖方案可设为必填，
	// 比自由留言可靠，是首次识别账号的首选来源。
	CustomOrderID string `json:"custom_order_id,omitempty"`
	// RedeemID 是订单使用的兑换码 ID。
	RedeemID  string `json:"redeem_id,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
	PlanTitle string `json:"plan_title,omitempty"`
	Months    int    `json:"months,omitempty"`
	Days      int    `json:"days,omitempty"`
	// DaysNote 记录天数是怎么算出来的（按金额折算 / 按方案月数 / 金额不足未发放），
	// 便于后台核对某笔订单为什么发或不发权益。
	DaysNote     string     `json:"days_note,omitempty"`
	Amount       string     `json:"amount,omitempty"`
	Remark       string     `json:"remark,omitempty"`
	Status       int        `json:"status"`
	LinkedUserID string     `json:"linked_user_id,omitempty"`
	LinkedName   string     `json:"linked_name,omitempty"`
	LinkedAt     *time.Time `json:"linked_at,omitempty"`
	CreditedAt   *time.Time `json:"credited_at,omitempty"`
	ReceivedAt   time.Time  `json:"received_at"`
}

type vipFile struct {
	Version int            `json:"version"`
	Codes   []*RedeemCode  `json:"codes"`
	Orders  []*AfdianOrder `json:"orders"`
}

// VipManager 管理卡密与爱发电订单。数据保存在独立 JSON 文件中，
// 通过 Store 接口更新用户 VIP 权益。
type VipManager struct {
	mu     sync.Mutex
	store  Store
	path   string
	now    func() time.Time
	codes  []*RedeemCode
	orders []*AfdianOrder
}

// NewVipManager 打开（或创建）卡密与订单存储。
func NewVipManager(store Store, path string) (*VipManager, error) {
	if store == nil {
		return nil, fmt.Errorf("account: 卡密存储缺少账号后端")
	}
	m := &VipManager{store: store, path: path, now: time.Now}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *VipManager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path == "" {
		return nil
	}
	raw, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var f vipFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("account: 卡密存储损坏: %w", err)
	}
	m.codes = f.Codes
	m.orders = f.Orders
	return nil
}

func (m *VipManager) saveLocked() error {
	if m.path == "" {
		return nil
	}
	if dir := filepath.Dir(m.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	f := vipFile{Version: vipFileVersion, Codes: m.codes, Orders: m.orders}
	raw, err := json.MarshalIndent(&f, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomGroup(n int) (string, error) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[idx.Int64()])
	}
	return b.String(), nil
}

// GenerateCode 生成一张形如 MCCMS-XXXX-XXXX-XXXX 的随机卡密。
func GenerateCode() (string, error) {
	parts := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		g, err := randomGroup(4)
		if err != nil {
			return "", err
		}
		parts = append(parts, g)
	}
	return "MCCMS-" + strings.Join(parts, "-"), nil
}

// NormalizeCode 归一化卡密输入：去掉空白、前缀与大小写差异。
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CreateCodes 批量生成卡密。actor 为操作者（用于记录）。
func (m *VipManager) CreateCodes(actor *User, count, days int, note string) ([]*RedeemCode, error) {
	if count <= 0 || count > 500 {
		return nil, fmt.Errorf("%w: 生成数量需在 1-500 之间", ErrInvalid)
	}
	if days <= 0 || days > 3650 {
		return nil, fmt.Errorf("%w: 有效天数需在 1-3650 之间", ErrInvalid)
	}
	batch := time.Now().Format("20060102-150405")
	out := make([]*RedeemCode, 0, count)
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]bool, len(m.codes))
	for _, c := range m.codes {
		seen[NormalizeCode(c.Code)] = true
	}
	for i := 0; i < count; i++ {
		var code string
		for attempt := 0; attempt < 8; attempt++ {
			g, err := GenerateCode()
			if err != nil {
				return nil, err
			}
			if !seen[NormalizeCode(g)] {
				code = g
				break
			}
		}
		if code == "" {
			return nil, fmt.Errorf("account: 卡密生成冲突，请重试")
		}
		seen[NormalizeCode(code)] = true
		item := &RedeemCode{
			ID:        NewID("code"),
			Code:      code,
			Note:      strings.TrimSpace(note),
			Days:      days,
			Batch:     batch,
			CreatedAt: m.now(),
		}
		if actor != nil {
			item.CreatedBy = actor.Username
		}
		m.codes = append(m.codes, item)
		out = append(out, item)
	}
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListCodes 返回全部卡密，最新在前。
func (m *VipManager) ListCodes() []*RedeemCode {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*RedeemCode, len(m.codes))
	copy(out, m.codes)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// RevokeCode 作废一张未使用的卡密。
func (m *VipManager) RevokeCode(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.codes {
		if c.ID == id {
			if c.UsedAt != nil {
				return fmt.Errorf("%w: 已使用的卡密无法作废", ErrConflict)
			}
			c.Revoked = true
			return m.saveLocked()
		}
	}
	return fmt.Errorf("%w: 卡密不存在", ErrNotFound)
}

// DeleteCode 删除一张卡密记录。
func (m *VipManager) DeleteCode(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range m.codes {
		if c.ID == id {
			m.codes = append(m.codes[:i], m.codes[i+1:]...)
			return m.saveLocked()
		}
	}
	return fmt.Errorf("%w: 卡密不存在", ErrNotFound)
}

// Redeem 兑换卡密：核销后为用户续期 VIP。整个流程持锁，避免并发重复兑换。
func (m *VipManager) Redeem(user *User, code string) (*RedeemCode, time.Time, error) {
	if user == nil {
		return nil, time.Time{}, ErrUnauthenticated
	}
	want := NormalizeCode(code)
	if want == "" {
		return nil, time.Time{}, fmt.Errorf("%w: 卡密不能为空", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *RedeemCode
	for _, c := range m.codes {
		if NormalizeCode(c.Code) == want {
			found = c
			break
		}
	}
	if found == nil {
		return nil, time.Time{}, fmt.Errorf("%w: 卡密不存在", ErrNotFound)
	}
	if !found.Usable() {
		return nil, time.Time{}, fmt.Errorf("%w: 卡密已被使用或已作废", ErrConflict)
	}
	u, exp, err := m.grantLocked(user.ID, found.Days)
	if err != nil {
		return nil, time.Time{}, err
	}
	now := m.now()
	found.UsedBy = u.ID
	found.UsedByName = u.Username
	found.UsedAt = &now
	if err := m.saveLocked(); err != nil {
		return nil, time.Time{}, err
	}
	return found, exp, nil
}

// Grant 由管理员或订单直接为用户续期。
func (m *VipManager) Grant(userID string, days int) (*User, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.grantLocked(userID, days)
}

// grantLocked 为用户增加 VIP 天数；已有未过期权益则在其后顺延。调用方需持有 m.mu。
func (m *VipManager) grantLocked(userID string, days int) (*User, time.Time, error) {
	if days <= 0 {
		return nil, time.Time{}, fmt.Errorf("%w: 有效天数必须为正", ErrInvalid)
	}
	if m.store == nil {
		return nil, time.Time{}, fmt.Errorf("account: 账号后端不可用")
	}
	u, err := m.store.UserByID(userID)
	if err != nil {
		return nil, time.Time{}, err
	}
	now := m.now()
	base := now
	if u.TierExpiresAt != nil && u.TierExpiresAt.After(now) {
		base = *u.TierExpiresAt
	}
	exp := base.AddDate(0, 0, days)
	u.Tier = TierVIP
	u.TierExpiresAt = &exp
	if err := m.store.UpdateUser(u); err != nil {
		return nil, time.Time{}, err
	}
	return u, exp, nil
}

// RecordOrder 幂等地记录一笔爱发电订单。created=false 表示已处理过。
func (m *VipManager) RecordOrder(ord *AfdianOrder) (bool, error) {
	if ord == nil || ord.OrderID == "" {
		return false, fmt.Errorf("%w: 缺少订单号", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.orders {
		if o.OrderID == ord.OrderID {
			return false, nil
		}
	}
	if ord.ReceivedAt.IsZero() {
		ord.ReceivedAt = m.now()
	}
	m.orders = append(m.orders, ord)
	if err := m.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// ListOrders 返回全部订单，最新在前。
func (m *VipManager) ListOrders() []*AfdianOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*AfdianOrder, len(m.orders))
	copy(out, m.orders)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ReceivedAt.After(out[j].ReceivedAt) })
	return out
}

// GetOrder 按订单号查询。
func (m *VipManager) GetOrder(orderID string) (*AfdianOrder, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.orders {
		if o.OrderID == orderID {
			return o, true
		}
	}
	return nil, false
}

// LinkOrder 将订单绑定到指定用户并发放权益。已绑定的订单不可重复发放。
func (m *VipManager) LinkOrder(orderID, userID string) (*AfdianOrder, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var target *AfdianOrder
	for _, o := range m.orders {
		if o.OrderID == orderID {
			target = o
			break
		}
	}
	if target == nil {
		return nil, time.Time{}, fmt.Errorf("%w: 订单不存在", ErrNotFound)
	}
	if target.CreditedAt != nil {
		return nil, time.Time{}, fmt.Errorf("%w: 该订单已发放权益", ErrConflict)
	}
	days := target.Days
	if days <= 0 {
		// 之前这里兜底成 31 天。启用「按月费折算」后，days = 0 表示实付金额不足一个月，
		// 继续送 31 天就等于白送，所以改为拒绝发放，由管理员决定是否手动发放。
		return nil, time.Time{}, fmt.Errorf("%w: 该订单折算后不足一个月，未发放权益", ErrInvalid)
	}
	u, exp, err := m.grantLocked(userID, days)
	if err != nil {
		return nil, time.Time{}, err
	}
	now := m.now()
	target.LinkedUserID = u.ID
	target.LinkedName = u.Username
	target.LinkedAt = &now
	target.CreditedAt = &now
	if err := m.saveLocked(); err != nil {
		return nil, time.Time{}, err
	}
	return target, exp, nil
}
