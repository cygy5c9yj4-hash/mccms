package account

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newVipFixture(t *testing.T) (*VipManager, Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewJSONStore(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	vm, err := NewVipManager(store, filepath.Join(dir, "vip.json"))
	if err != nil {
		t.Fatalf("NewVipManager: %v", err)
	}
	return vm, store
}

func mkUser(t *testing.T, store Store, name string) *User {
	t.Helper()
	now := time.Now()
	u := &User{
		ID:        NewID("u_"),
		Username:  name,
		Role:      RoleUser,
		Status:    StatusActive,
		Tier:      TierFree,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateUser(u); err != nil {
		t.Fatalf("CreateUser(%s): %v", name, err)
	}
	return u
}

func TestNormalizeCodeStripsNoise(t *testing.T) {
	got := NormalizeCode("  mccms-abcd-efgh-ijkm ")
	want := "MCCMSABCDEFGHIJKM"
	if got != want {
		t.Fatalf("NormalizeCode = %q, want %q", got, want)
	}
}

func TestCreateAndRedeemCode(t *testing.T) {
	vm, store := newVipFixture(t)
	u := mkUser(t, store, "alice")

	codes, err := vm.CreateCodes(u, 2, 31, "首次活动")
	if err != nil {
		t.Fatalf("CreateCodes: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("CreateCodes 返回 %d 张，期望 2", len(codes))
	}
	for _, c := range codes {
		if !strings.HasPrefix(c.Code, "MCCMS-") {
			t.Fatalf("卡密格式不符: %q", c.Code)
		}
	}

	code, exp, err := vm.Redeem(u, codes[0].Code)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if code.Days != 31 {
		t.Fatalf("兑换天数 = %d，期望 31", code.Days)
	}
	if time.Until(exp) < 29*24*time.Hour {
		t.Fatalf("到期时间过短: %v", exp)
	}
	updated, err := store.UserByID(u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if updated.EffectiveTier(time.Now()) != TierVIP {
		t.Fatalf("兑换后等级 = %s，期望 vip", updated.Tier)
	}
	if updated.TierExpiresAt == nil {
		t.Fatal("兑换后缺少到期时间")
	}
}

func TestRedeemIsSingleUse(t *testing.T) {
	vm, store := newVipFixture(t)
	alice := mkUser(t, store, "alice")
	bob := mkUser(t, store, "bob")

	codes, err := vm.CreateCodes(alice, 1, 31, "")
	if err != nil {
		t.Fatalf("CreateCodes: %v", err)
	}
	if _, _, err := vm.Redeem(alice, codes[0].Code); err != nil {
		t.Fatalf("首次兑换失败: %v", err)
	}
	if _, _, err := vm.Redeem(bob, codes[0].Code); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复兑换错误 = %v，期望 ErrConflict", err)
	}
}

func TestRedeemExtendsExistingExpiry(t *testing.T) {
	vm, store := newVipFixture(t)
	u := mkUser(t, store, "alice")

	if _, first, err := vm.Grant(u.ID, 10); err != nil {
		t.Fatalf("Grant: %v", err)
	} else if time.Until(first) < 9*24*time.Hour {
		t.Fatalf("首次发放到期时间异常: %v", first)
	}

	codes, err := vm.CreateCodes(u, 1, 30, "")
	if err != nil {
		t.Fatalf("CreateCodes: %v", err)
	}
	_, second, err := vm.Redeem(u, codes[0].Code)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if time.Until(second) < 39*24*time.Hour {
		t.Fatalf("续期未顺延，到期时间 = %v", second)
	}
}

func TestRedeemUnknownCode(t *testing.T) {
	vm, store := newVipFixture(t)
	u := mkUser(t, store, "alice")
	if _, _, err := vm.Redeem(u, "MCCMS-XXXX-XXXX-XXXX"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知卡密错误 = %v，期望 ErrNotFound", err)
	}
}

func TestGrantRejectsNonPositiveDays(t *testing.T) {
	vm, store := newVipFixture(t)
	u := mkUser(t, store, "alice")
	if _, _, err := vm.Grant(u.ID, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("零天数错误 = %v，期望 ErrInvalid", err)
	}
}

func TestCreateCodesRejectsBadInput(t *testing.T) {
	vm, store := newVipFixture(t)
	u := mkUser(t, store, "alice")
	if _, err := vm.CreateCodes(u, 0, 31, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("零数量错误 = %v，期望 ErrInvalid", err)
	}
	if _, err := vm.CreateCodes(u, 1, 0, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("零天数错误 = %v，期望 ErrInvalid", err)
	}
}

func TestOrderIsIdempotent(t *testing.T) {
	vm, _ := newVipFixture(t)
	ord := &AfdianOrder{OrderID: "ord-1", Days: 31, Status: 2}
	created, err := vm.RecordOrder(ord)
	if err != nil || !created {
		t.Fatalf("首次记录订单: created=%v err=%v", created, err)
	}
	again := &AfdianOrder{OrderID: "ord-1", Days: 31, Status: 2}
	created, err = vm.RecordOrder(again)
	if err != nil {
		t.Fatalf("重复记录返回错误: %v", err)
	}
	if created {
		t.Fatal("重复订单不应再次写入")
	}
	if len(vm.ListOrders()) != 1 {
		t.Fatalf("订单数 = %d，期望 1", len(vm.ListOrders()))
	}
}

func TestLinkOrderCreditsOnce(t *testing.T) {
	vm, store := newVipFixture(t)
	u := mkUser(t, store, "alice")
	if _, err := vm.RecordOrder(&AfdianOrder{OrderID: "ord-2", Days: 31, Status: 2}); err != nil {
		t.Fatalf("RecordOrder: %v", err)
	}
	if _, _, err := vm.LinkOrder("ord-2", u.ID); err != nil {
		t.Fatalf("LinkOrder: %v", err)
	}
	if _, _, err := vm.LinkOrder("ord-2", u.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复发放错误 = %v，期望 ErrConflict", err)
	}
}

func TestVipManagerPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONStore(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	u := mkUser(t, store, "alice")
	vm, err := NewVipManager(store, filepath.Join(dir, "vip.json"))
	if err != nil {
		t.Fatalf("NewVipManager: %v", err)
	}
	if _, err := vm.CreateCodes(u, 3, 31, "持久化"); err != nil {
		t.Fatalf("CreateCodes: %v", err)
	}
	reloaded, err := NewVipManager(store, filepath.Join(dir, "vip.json"))
	if err != nil {
		t.Fatalf("重新加载: %v", err)
	}
	if got := len(reloaded.ListCodes()); got != 3 {
		t.Fatalf("重载后卡密数 = %d，期望 3", got)
	}
}

// 启用「按月费折算」后，实付不足一个月的订单 Days 为 0。
// 这类订单必须被拒绝发放：以前 LinkOrder 会把 days<=0 兜底成 31 天，等于白送。
func TestLinkOrderRejectsZeroDays(t *testing.T) {
	vm, store := newVipFixture(t)
	u := mkUser(t, store, "alice")
	if _, err := vm.RecordOrder(&AfdianOrder{OrderID: "ord-low", Days: 0, Status: 2}); err != nil {
		t.Fatalf("RecordOrder: %v", err)
	}
	if _, _, err := vm.LinkOrder("ord-low", u.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("LinkOrder 零天数订单错误 = %v，期望 ErrInvalid", err)
	}
	orders := vm.ListOrders()
	if len(orders) != 1 {
		t.Fatalf("订单数 = %d，期望 1", len(orders))
	}
	if orders[0].LinkedUserID != "" {
		t.Fatalf("订单不应被标记为已发放，LinkedUserID = %q", orders[0].LinkedUserID)
	}
	if got := u.EffectiveTier(time.Now()); got != TierFree {
		t.Fatalf("用户等级 = %q，期望 free", got)
	}
}
