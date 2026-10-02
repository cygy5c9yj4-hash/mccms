package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mccms/mccms-go/internal/account"
	"github.com/mccms/mccms-go/internal/mc"
)

// newVipTestServer 构造启用了卡密/VIP 的测试服务。
// 卡密库指向独立临时文件，避免污染工作区。
func newVipTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("MCCMS_VIP_STORE", filepath.Join(t.TempDir(), "vip.json"))
	s, err := New(Config{
		Site:         mc.SiteTibiu,
		AccountsPath: filepath.Join(t.TempDir(), "accounts.json"),
		DownloadDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// postJSON 发送带会话 cookie 的 JSON 请求，返回解码后的信封。
func postJSON(t *testing.T, s *Server, path string, body any, cookie *http.Cookie) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s 响应不是 JSON: %s", path, rec.Body.String())
	}
	return out
}

// registerAndCookie 注册一个用户并返回其会话 cookie。
func registerAndCookie(t *testing.T, s *Server, username string) *http.Cookie {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"username": username, "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("注册 %s 返回 %d: %s", username, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatalf("注册 %s 未收到会话 cookie", username)
	return nil
}

func seedOrder(t *testing.T, s *Server, orderID string, status int, days int) {
	t.Helper()
	m, err := s.vipManager()
	if err != nil || m == nil {
		t.Fatalf("vipManager: %v", err)
	}
	recv := time.Now().UTC()
	if _, err := m.RecordOrder(&account.AfdianOrder{
		OrderID:    orderID,
		Status:     status,
		Days:       days,
		Amount:     "7.00",
		ReceivedAt: recv,
	}); err != nil {
		t.Fatalf("RecordOrder: %v", err)
	}
}

// 付款时漏填留言的用户，可以凭订单号自助归户并立即开通。
func TestVipClaimSucceeds(t *testing.T) {
	s := newVipTestServer(t)
	cookie := registerAndCookie(t, s, "claimer")
	seedOrder(t, s, "2026100200000001", 2, 31)

	got := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "2026100200000001"}, cookie)
	if got["st"] != float64(stOK) {
		t.Fatalf("认领失败: %v", got)
	}
	data, _ := got["data"].(map[string]any)
	if data["tier"] != string(account.TierVIP) {
		t.Errorf("认领后 tier = %v, want vip", data["tier"])
	}
	if data["claimed"] != true {
		t.Errorf("claimed = %v, want true", data["claimed"])
	}

	// 订单应被标记为已归户到本人。
	m, _ := s.vipManager()
	ord, found := m.GetOrder("2026100200000001")
	if !found || ord.CreditedAt == nil {
		t.Fatalf("订单未被标记为已发放: %+v", ord)
	}
}

// 重复认领同一订单应当幂等，不重复发放。
func TestVipClaimIdempotent(t *testing.T) {
	s := newVipTestServer(t)
	cookie := registerAndCookie(t, s, "repeater")
	seedOrder(t, s, "2026100200000002", 2, 31)

	first := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "2026100200000002"}, cookie)
	if first["st"] != float64(stOK) {
		t.Fatalf("首次认领失败: %v", first)
	}
	m, _ := s.vipManager()
	ord, _ := m.GetOrder("2026100200000002")
	expAfterFirst := ord.CreditedAt

	second := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "2026100200000002"}, cookie)
	if second["st"] != float64(stOK) {
		t.Fatalf("重复认领应幂等成功: %v", second)
	}
	if data, _ := second["data"].(map[string]any); data["claimed"] != false {
		t.Errorf("重复认领 claimed = %v, want false", data["claimed"])
	}
	m2, _ := s.vipManager()
	ord2, _ := m2.GetOrder("2026100200000002")
	if ord2.CreditedAt == nil || !ord2.CreditedAt.Equal(*expAfterFirst) {
		t.Errorf("重复认领改变了发放时间: %v -> %v", expAfterFirst, ord2.CreditedAt)
	}
}

// 不存在的订单号必须被拒绝。
func TestVipClaimRejectsUnknownOrder(t *testing.T) {
	s := newVipTestServer(t)
	cookie := registerAndCookie(t, s, "unknown")

	got := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "does-not-exist"}, cookie)
	if got["st"] != float64(stError) {
		t.Fatalf("未知订单应失败, got %v", got)
	}
}

// 未支付成功的订单不能被认领。
func TestVipClaimRejectsUnpaidOrder(t *testing.T) {
	s := newVipTestServer(t)
	cookie := registerAndCookie(t, s, "unpaid")
	seedOrder(t, s, "2026100200000003", 1, 31)

	got := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "2026100200000003"}, cookie)
	if got["st"] != float64(stError) {
		t.Fatalf("未支付订单应失败, got %v", got)
	}
}

// 已绑定他人的订单不能被抢走。
func TestVipClaimRejectsAlreadyLinkedToOther(t *testing.T) {
	s := newVipTestServer(t)
	owner := registerAndCookie(t, s, "owner")
	_ = owner
	thief := registerAndCookie(t, s, "thief")
	seedOrder(t, s, "2026100200000004", 2, 31)

	// 先把订单归给 owner。
	m, _ := s.vipManager()
	ownerUser, err := s.accountService().Store().UserByUsername("owner")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}
	if _, _, err := m.LinkOrder("2026100200000004", ownerUser.ID); err != nil {
		t.Fatalf("LinkOrder: %v", err)
	}

	got := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "2026100200000004"}, thief)
	if got["st"] != float64(stError) {
		t.Fatalf("抢单应失败, got %v", got)
	}
}

// 未登录用户不能认领。
func TestVipClaimRequiresLogin(t *testing.T) {
	s := newVipTestServer(t)
	seedOrder(t, s, "2026100200000005", 2, 31)

	got := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "2026100200000005"}, nil)
	if got["st"] != float64(stNotLogin) {
		t.Fatalf("未登录应返回 1014, got %v", got)
	}
}

// 金额折算不足一个月的订单（days<=0）不能被认领。
func TestVipClaimRejectsZeroDays(t *testing.T) {
	s := newVipTestServer(t)
	cookie := registerAndCookie(t, s, "toosmall")
	seedOrder(t, s, "2026100200000006", 2, 0)

	got := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": "2026100200000006"}, cookie)
	if got["st"] != float64(stError) {
		t.Fatalf("零天数订单应失败, got %v", got)
	}
}

// 订单号里夹带的空格/换行应被规范化后仍可匹配。
func TestVipClaimNormalizesWhitespace(t *testing.T) {
	s := newVipTestServer(t)
	cookie := registerAndCookie(t, s, "messy")
	seedOrder(t, s, "2026100200000007", 2, 31)

	got := postJSON(t, s, "/api/vip/claim", map[string]any{"order_id": " 2026100200000007\n"}, cookie)
	if got["st"] != float64(stOK) {
		t.Fatalf("含空白的订单号应能认领, got %v", got)
	}
}
