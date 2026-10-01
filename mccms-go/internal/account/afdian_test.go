package account

import "testing"

// 覆盖爱发电绑定与解绑的生命周期，以及「一个爱发电账号只能绑定一个本站账号」的约束。
func TestBindAfdianLifecycle(t *testing.T) {
	svc, js := newTestService(t)
	alice := mkUser(t, js, "alice")
	bob := mkUser(t, js, "bob")

	// 首次绑定后可以按爱发电 ID 反查到账号。
	if err := svc.BindAfdian(alice.ID, "adf_alice"); err != nil {
		t.Fatalf("BindAfdian: %v", err)
	}
	if got := svc.UserByAfdianUID("adf_alice"); got == nil || got.ID != alice.ID {
		t.Fatalf("UserByAfdianUID 未返回 alice：%+v", got)
	}
	if ua, _ := js.UserByID(alice.ID); ua.AfdianUserID != "adf_alice" || ua.AfdianBoundAt == nil {
		t.Fatalf("alice 绑定未落库：%+v", ua)
	}

	// 空 ID 不应匹配任何账号（否则会把订单发给随机用户）。
	if got := svc.UserByAfdianUID(""); got != nil {
		t.Fatalf("空爱发电 ID 不应匹配，实际返回 %+v", got)
	}
	if got := svc.UserByAfdianUID("   "); got != nil {
		t.Fatalf("空白爱发电 ID 不应匹配，实际返回 %+v", got)
	}

	// 同一爱发电账号改绑到 bob 时 alice 必须自动解绑，避免一笔捐赠记到两个账号。
	if err := svc.BindAfdian(bob.ID, "adf_alice"); err != nil {
		t.Fatalf("改绑: %v", err)
	}
	if got := svc.UserByAfdianUID("adf_alice"); got == nil || got.ID != bob.ID {
		t.Fatalf("改绑后应归属 bob：%+v", got)
	}
	if ua, _ := js.UserByID(alice.ID); ua.AfdianUserID != "" || ua.AfdianBoundAt != nil {
		t.Fatalf("alice 应已被解绑：%+v", ua)
	}

	// 解绑后不再反查到账号，但账号本身仍在。
	if err := svc.UnbindAfdian(bob.ID); err != nil {
		t.Fatalf("UnbindAfdian: %v", err)
	}
	if got := svc.UserByAfdianUID("adf_alice"); got != nil {
		t.Fatalf("解绑后不应匹配，实际 %+v", got)
	}
	if ub, _ := js.UserByID(bob.ID); ub == nil {
		t.Fatal("解绑不应删除账号")
	}
}

// 空参数必须被拒绝，而不是静默绑定到某个账号上。
func TestBindAfdianRejectsEmpty(t *testing.T) {
	svc, js := newTestService(t)
	alice := mkUser(t, js, "alice")

	if err := svc.BindAfdian(alice.ID, ""); err == nil {
		t.Fatal("空爱发电 ID 应当报错")
	}
	if err := svc.BindAfdian("", "adf_alice"); err == nil {
		t.Fatal("空用户 ID 应当报错")
	}
	if err := svc.UnbindAfdian(""); err == nil {
		t.Fatal("空用户 ID 解绑应当报错")
	}
}
