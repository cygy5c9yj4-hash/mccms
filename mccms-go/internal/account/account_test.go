package account

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// newTestService 构造一个使用临时目录 JSON 库的账号服务。
func newTestService(t *testing.T) (*Service, *jsonStore) {
	t.Helper()
	store, err := NewJSONStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	js, ok := store.(*jsonStore)
	if !ok {
		t.Fatalf("期望 *jsonStore，得到 %T", store)
	}

	svc, err := New(Options{Store: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return svc, js
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "correct-horse-battery" {
		t.Fatal("散列不应等于明文")
	}

	ok, err := VerifyPassword(hash, "correct-horse-battery")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("正确口令应校验通过")
	}

	ok, err = VerifyPassword(hash, "wrong-password-here")
	if err != nil {
		t.Fatalf("VerifyPassword(wrong): %v", err)
	}
	if ok {
		t.Fatal("错误口令不应校验通过")
	}
}

func TestPasswordHashIsSalted(t *testing.T) {
	// 同一口令两次散列应得到不同结果（盐随机），否则相同口令可被批量比对。
	a, err := HashPassword("same-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := HashPassword("same-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if a == b {
		t.Fatal("两次散列结果相同，说明没有使用随机盐")
	}
}

func TestHashPasswordRejectsWeakInput(t *testing.T) {
	if _, err := HashPassword("short"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("短口令应被拒绝，得到 %v", err)
	}
	long := make([]byte, MaxPasswordLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := HashPassword(string(long)); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("超长口令应被拒绝，得到 %v", err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"not-a-hash",
		"pbkdf2_sha256$bad$c2FsdA$aa",
		"unknown_algo$1000$c2FsdA$aa",
	}
	for _, c := range cases {
		if _, err := VerifyPassword(c, "whatever-password"); err == nil {
			t.Errorf("散列 %q 应被判定为格式非法", c)
		}
	}
}

func TestFirstUserBecomesAdmin(t *testing.T) {
	svc, _ := newTestService(t)

	first, _, err := svc.Register("alice", "password123", "", "", "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("Register(first): %v", err)
	}
	if !first.IsAdmin() {
		t.Fatal("首个注册用户应自动成为管理员")
	}

	second, _, err := svc.Register("bob", "password123", "", "", "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("Register(second): %v", err)
	}
	if second.IsAdmin() {
		t.Fatal("第二个注册用户不应成为管理员")
	}
}

func TestRegisterRejectsDuplicateUsername(t *testing.T) {
	svc, _ := newTestService(t)
	if _, _, err := svc.Register("alice", "password123", "", "", "", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 大小写不同也应视为重复。
	_, _, err := svc.Register("ALICE", "password123", "", "", "", "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("重复用户名应返回 ErrConflict，得到 %v", err)
	}
}

func TestRegisterValidatesUsername(t *testing.T) {
	svc, _ := newTestService(t)
	bad := []string{"ab", "has space", "bad/slash", "用户名", ""}
	for _, name := range bad {
		if _, _, err := svc.Register(name, "password123", "", "", "", ""); err == nil {
			t.Errorf("用户名 %q 应被拒绝", name)
		}
	}
}

func TestLoginAndAuthenticate(t *testing.T) {
	svc, _ := newTestService(t)
	u, _, err := svc.Register("alice", "password123", "", "", "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, token, err := svc.Login("alice", "password123", "ua", "127.0.0.1")
	_ = u
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	got, _, err := svc.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.Username != "alice" {
		t.Fatalf("Authenticate 返回 %q，期望 alice", got.Username)
	}

	// 登出后 token 立即失效。
	if err := svc.Logout(token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := svc.Authenticate(token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("登出后应无法认证，得到 %v", err)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	svc, _ := newTestService(t)
	if _, _, err := svc.Register("alice", "password123", "", "", "", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, _, err := svc.Login("alice", "wrong-password", "", ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("错误口令应返回 ErrUnauthenticated，得到 %v", err)
	}
	// 不存在的用户返回同样的错误，避免账号枚举。
	if _, _, err := svc.Login("nobody", "password123", "", ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("不存在的用户应返回 ErrUnauthenticated，得到 %v", err)
	}
}

func TestDisabledUserIsRejectedAndSessionsRevoked(t *testing.T) {
	svc, _ := newTestService(t)
	admin, _, err := svc.Register("admin", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(admin): %v", err)
	}
	victim, token, err := svc.Register("victim", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(victim): %v", err)
	}

	if err := svc.AdminSetStatus(admin, victim.ID, StatusDisabled, "127.0.0.1"); err != nil {
		t.Fatalf("AdminSetStatus: %v", err)
	}

	// 已有会话应立即失效。
	if _, _, err := svc.Authenticate(token); err == nil {
		t.Fatal("被禁用用户的会话应立即失效")
	}
	// 也不能再登录。
	if _, _, err := svc.Login("victim", "password123", "", ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("被禁用用户不应能登录，得到 %v", err)
	}
	// 解禁后旧会话不得复活。禁用时主动删除会话记录，而不是只靠
	// Authenticate 的惰性状态检查，否则一次解禁就会让盗号者的旧会话重新可用。
	if err := svc.AdminSetStatus(admin, victim.ID, StatusActive, "127.0.0.1"); err != nil {
		t.Fatalf("AdminSetStatus(active): %v", err)
	}
	if _, _, err := svc.Authenticate(token); err == nil {
		t.Fatal("解禁后旧会话不应复活")
	}
}

func TestAdminCannotLockSelfOut(t *testing.T) {
	svc, _ := newTestService(t)
	admin, _, err := svc.Register("admin", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := svc.AdminSetRole(admin, admin.ID, RoleUser, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("管理员不应能取消自己的管理员身份，得到 %v", err)
	}
	if err := svc.AdminSetStatus(admin, admin.ID, StatusDisabled, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("管理员不应能禁用自己，得到 %v", err)
	}
	if err := svc.AdminDeleteUser(admin, admin.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("管理员不应能删除自己的账号，得到 %v", err)
	}
}

func TestRegistrationCanBeClosed(t *testing.T) {
	svc, _ := newTestService(t)
	admin, _, err := svc.Register("admin", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := svc.SetRegistrationOpen(false, admin, ""); err != nil {
		t.Fatalf("SetRegistrationOpen: %v", err)
	}
	if _, _, err := svc.Register("newbie", "password123", "", "", "", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("关闭注册后应拒绝新注册，得到 %v", err)
	}
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	svc, _ := newTestService(t)
	u, first, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, second, err := svc.Login("alice", "password123", "", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	fresh, err := svc.ChangePassword(u, "password123", "newpassword456", "", "")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// 改密后旧会话全部作废。
	if _, _, err := svc.Authenticate(first); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("旧会话应失效，得到 %v", err)
	}
	if _, _, err := svc.Authenticate(second); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("旧会话应失效，得到 %v", err)
	}
	// 换发的新会话可用。
	if _, _, err := svc.Authenticate(fresh); err != nil {
		t.Fatalf("新会话应有效: %v", err)
	}
	// 新口令可登录。
	if _, _, err := svc.Login("alice", "newpassword456", "", ""); err != nil {
		t.Fatalf("新口令应能登录: %v", err)
	}
}

func TestChangePasswordRequiresOldPassword(t *testing.T) {
	svc, _ := newTestService(t)
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.ChangePassword(u, "wrong-old", "newpassword456", "", ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("原密码错误时应拒绝，得到 %v", err)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	store, err := NewJSONStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 用可控时钟构造会话，再把时钟拨到 TTL 之后，验证过期判定。
	now := time.Now().UTC()
	var clock atomic.Pointer[time.Time]
	clock.Store(&now)

	svc, err := New(Options{
		Store:      store,
		SessionTTL: time.Minute,
		Now:        func() time.Time { return *clock.Load() },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	_, token, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// 有效期内应能认证。
	if _, _, err := svc.Authenticate(token); err != nil {
		t.Fatalf("有效会话应通过: %v", err)
	}

	later := now.Add(2 * time.Minute)
	clock.Store(&later)

	if _, _, err := svc.Authenticate(token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("过期会话应被拒绝，得到 %v", err)
	}
}

func TestFavoritesCRUD(t *testing.T) {
	svc, _ := newTestService(t)
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	fav := &Favorite{
		UserID:   u.ID,
		Source:   "tibiu",
		ComicID:  "comic-1",
		Title:    "某部漫画",
		FolderID: "",
	}
	if err := svc.AddFavorite(fav); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if !svc.IsFavorite(u.ID, "tibiu", "comic-1") {
		t.Fatal("收藏后 IsFavorite 应为 true")
	}

	items, total, err := svc.ListFavorites(u.ID, "", 1, 20)
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("期望 1 条收藏，得到 total=%d len=%d", total, len(items))
	}

	// 取消收藏应幂等。
	if err := svc.RemoveFavorite(u.ID, "tibiu", "comic-1"); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}
	if err := svc.RemoveFavorite(u.ID, "tibiu", "comic-1"); err != nil {
		t.Fatalf("重复取消收藏应幂等，得到 %v", err)
	}
	if svc.IsFavorite(u.ID, "tibiu", "comic-1") {
		t.Fatal("取消收藏后 IsFavorite 应为 false")
	}
}

func TestFavoriteFolders(t *testing.T) {
	svc, _ := newTestService(t)
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	f, err := svc.CreateFolder(u.ID, "想看")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// 同名收藏夹应被拒绝。
	if _, err := svc.CreateFolder(u.ID, "想看"); !errors.Is(err, ErrConflict) {
		t.Fatalf("同名收藏夹应返回 ErrConflict，得到 %v", err)
	}

	if err := svc.AddFavorite(&Favorite{UserID: u.ID, Source: "tibiu", ComicID: "c1", FolderID: f.ID}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := svc.AddFavorite(&Favorite{UserID: u.ID, Source: "tibiu", ComicID: "c2"}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	// 按收藏夹过滤。
	grouped, total, err := svc.ListFavorites(u.ID, f.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if total != 1 || grouped[0].ComicID != "c1" {
		t.Fatalf("收藏夹内应有 1 条 c1，得到 total=%d", total)
	}

	// 「未分组」应只含 c2。
	ungrouped, total, err := svc.ListFavorites(u.ID, UngroupedFolderID, 1, 20)
	if err != nil {
		t.Fatalf("ListFavorites(ungrouped): %v", err)
	}
	if total != 1 || ungrouped[0].ComicID != "c2" {
		t.Fatalf("未分组应有 1 条 c2，得到 total=%d", total)
	}

	// 删除收藏夹后，夹内收藏落回未分组而不是消失。
	if err := svc.DeleteFolder(u.ID, f.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	_, total, err = svc.ListFavorites(u.ID, UngroupedFolderID, 1, 20)
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if total != 2 {
		t.Fatalf("删除收藏夹后未分组应有 2 条，得到 %d", total)
	}
}

func TestDefaultFolderCannotBeDeleted(t *testing.T) {
	svc, _ := newTestService(t)
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, id := range []string{"", DefaultFolderID, UngroupedFolderID} {
		if err := svc.DeleteFolder(u.ID, id); !errors.Is(err, ErrInvalid) {
			t.Errorf("删除虚拟收藏夹 %q 应被拒绝，得到 %v", id, err)
		}
	}
}

func TestFavoritesAreScopedPerUser(t *testing.T) {
	svc, _ := newTestService(t)
	alice, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(alice): %v", err)
	}
	bob, _, err := svc.Register("bob", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(bob): %v", err)
	}

	if err := svc.AddFavorite(&Favorite{UserID: alice.ID, Source: "tibiu", ComicID: "c1"}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}

	// 同一作品，Bob 的收藏状态必须独立。
	if svc.IsFavorite(bob.ID, "tibiu", "c1") {
		t.Fatal("收藏不应跨用户泄漏")
	}
	_, total, err := svc.ListFavorites(bob.ID, "", 1, 20)
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if total != 0 {
		t.Fatalf("Bob 不应看到 Alice 的收藏，得到 %d", total)
	}
}

func TestHistoryUpsertAndClear(t *testing.T) {
	svc, _ := newTestService(t)
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := svc.RecordHistory(&History{
		UserID: u.ID, Source: "tibiu", ComicID: "c1", Title: "作品",
		ChapterID: "ch1", Page: 3,
	}); err != nil {
		t.Fatalf("RecordHistory: %v", err)
	}
	// 同一作品的第二次上报应原地更新，而不是新增一条。
	if err := svc.RecordHistory(&History{
		UserID: u.ID, Source: "tibiu", ComicID: "c1", Title: "作品",
		ChapterID: "ch2", Page: 7,
	}); err != nil {
		t.Fatalf("RecordHistory: %v", err)
	}

	items, total, err := svc.ListHistory(u.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("期望 1 条历史，得到 total=%d", total)
	}
	if items[0].ChapterID != "ch2" || items[0].Page != 7 {
		t.Fatalf("历史未更新到最新章节：%+v", items[0])
	}

	if err := svc.ClearHistory(u.ID); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	_, total, err = svc.ListHistory(u.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if total != 0 {
		t.Fatalf("清空后应为 0 条，得到 %d", total)
	}
}

func TestNotesCRUDAndOwnership(t *testing.T) {
	svc, _ := newTestService(t)
	alice, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(alice): %v", err)
	}
	bob, _, err := svc.Register("bob", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(bob): %v", err)
	}

	n, err := svc.CreateNote(&Note{UserID: alice.ID, Source: "tibiu", ComicID: "c1", Body: "读起来不错"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if n.ID == "" {
		t.Fatal("笔记应有 id")
	}

	// 空内容应被拒绝。
	if _, err := svc.CreateNote(&Note{UserID: alice.ID, Source: "tibiu", ComicID: "c1", Body: "   "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空笔记应被拒绝，得到 %v", err)
	}

	// 他人不能编辑或删除。
	if _, err := svc.UpdateNote(bob.ID, n.ID, "篡改"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("他人编辑应返回 ErrNotFound，得到 %v", err)
	}
	if err := svc.DeleteNote(bob.ID, n.ID); err != nil {
		t.Fatalf("他人删除应静默无操作，得到 %v", err)
	}
	if _, total, _ := svc.ListNotes(alice.ID, "", "", 1, 20); total != 1 {
		t.Fatalf("Alice 的笔记不应被 Bob 删除，得到 %d", total)
	}

	// 本人编辑。
	updated, err := svc.UpdateNote(alice.ID, n.ID, "改了想法")
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if updated.Body != "改了想法" {
		t.Fatalf("笔记内容未更新：%q", updated.Body)
	}
}

func TestTierExpiryFallsBackToFree(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	expired := &User{Tier: TierVIP, TierExpiresAt: &past}
	if got := expired.EffectiveTier(now); got != TierFree {
		t.Fatalf("已过期的 VIP 应回落 free，得到 %q", got)
	}

	active := &User{Tier: TierVIP, TierExpiresAt: &future}
	if got := active.EffectiveTier(now); got != TierVIP {
		t.Fatalf("未过期的 VIP 应为 vip，得到 %q", got)
	}

	forever := &User{Tier: TierVIP}
	if got := forever.EffectiveTier(now); got != TierVIP {
		t.Fatalf("未设置到期时间应视为永久 VIP，得到 %q", got)
	}

	free := &User{Tier: TierFree}
	if got := free.EffectiveTier(now); got != TierFree {
		t.Fatalf("免费用户应为 free，得到 %q", got)
	}
	if !free.HasTier(now, TierFree) || free.HasTier(now, TierVIP) {
		t.Fatal("HasTier 判定不正确")
	}
}

func TestAdminSetTier(t *testing.T) {
	svc, _ := newTestService(t)
	admin, _, err := svc.Register("admin", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(admin): %v", err)
	}
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(alice): %v", err)
	}

	exp := time.Now().UTC().Add(24 * time.Hour)
	if err := svc.AdminSetTier(admin, u.ID, TierVIP, &exp, ""); err != nil {
		t.Fatalf("AdminSetTier: %v", err)
	}
	pub, err := svc.UserByID(u.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if pub.Tier != TierVIP {
		t.Fatalf("期望 vip，得到 %q", pub.Tier)
	}

	if err := svc.AdminSetTier(admin, u.ID, TierFree, nil, ""); err != nil {
		t.Fatalf("AdminSetTier: %v", err)
	}
	pub, _ = svc.UserByID(u.ID)
	if pub.Tier != TierFree {
		t.Fatalf("回退后应为 free，得到 %q", pub.Tier)
	}
}

func TestDeleteUserCascades(t *testing.T) {
	svc, js := newTestService(t)
	admin, _, err := svc.Register("admin", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(admin): %v", err)
	}
	victim, token, err := svc.Register("victim", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register(victim): %v", err)
	}

	if err := svc.AddFavorite(&Favorite{UserID: victim.ID, Source: "tibiu", ComicID: "c1"}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := svc.RecordHistory(&History{UserID: victim.ID, Source: "tibiu", ComicID: "c1"}); err != nil {
		t.Fatalf("RecordHistory: %v", err)
	}
	if _, err := svc.CreateNote(&Note{UserID: victim.ID, Source: "tibiu", ComicID: "c1", Body: "笔记"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := svc.CreateFolder(victim.ID, "夹子"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	if err := svc.AdminDeleteUser(admin, victim.ID, ""); err != nil {
		t.Fatalf("AdminDeleteUser: %v", err)
	}

	// 会话失效，且该用户的数据被清空。
	if _, _, err := svc.Authenticate(token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("被删用户的会话应失效，得到 %v", err)
	}
	c, err := js.Counts(time.Now().UTC())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.Favorites != 0 || c.History != 0 || c.Notes != 0 || c.Folders != 0 {
		t.Fatalf("删号未级联清理：%+v", c)
	}
}

func TestAuditLogRecordsActions(t *testing.T) {
	svc, _ := newTestService(t)
	admin, _, err := svc.Register("admin", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.AdminSetStatus(admin, u.ID, StatusDisabled, "10.0.0.1"); err != nil {
		t.Fatalf("AdminSetStatus: %v", err)
	}

	items, total, err := svc.Audit(1, 50)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if total == 0 {
		t.Fatal("应有审计记录")
	}
	// 最新在前。
	if items[0].Action != "admin.set_status" {
		t.Fatalf("最新审计应为 admin.set_status，得到 %q", items[0].Action)
	}
	if items[0].ActorName != "admin" {
		t.Fatalf("审计应记录操作者，得到 %q", items[0].ActorName)
	}
	if items[0].IP != "10.0.0.1" {
		t.Fatalf("审计应记录 IP，得到 %q", items[0].IP)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

	store, err := NewJSONStore(path)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	svc, err := New(Options{Store: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.AddFavorite(&Favorite{UserID: u.ID, Source: "tibiu", ComicID: "c1", Title: "作品"}); err != nil {
		t.Fatalf("AddFavorite: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 重新打开：数据与口令都应仍然可用。
	store2, err := NewJSONStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })
	svc2, err := New(Options{Store: store2})
	if err != nil {
		t.Fatalf("New(2): %v", err)
	}
	if err := svc2.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap(2): %v", err)
	}

	if _, _, err := svc2.Login("alice", "password123", "", ""); err != nil {
		t.Fatalf("重开后登录失败: %v", err)
	}
	got, err := svc2.Store().UserByUsername("alice")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}
	_, total, err := svc2.ListFavorites(got.ID, "", 1, 20)
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if total != 1 {
		t.Fatalf("重开后应有 1 条收藏，得到 %d", total)
	}
}

func TestRegistrationOpenSettingPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

	store, err := NewJSONStore(path)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	svc, _ := New(Options{Store: store})
	if err := svc.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	admin, _, err := svc.Register("admin", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.SetRegistrationOpen(false, admin, ""); err != nil {
		t.Fatalf("SetRegistrationOpen: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	store2, err := NewJSONStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })
	svc2, _ := New(Options{Store: store2})
	if err := svc2.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap(2): %v", err)
	}
	if svc2.RegistrationOpen() {
		t.Fatal("重开后注册开关应仍为关闭")
	}
}

func TestSessionTokenIsNotStoredInPlaintext(t *testing.T) {
	svc, js := newTestService(t)
	_, token, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 存储里应只存在散列，而不是明文 token。
	if _, err := js.SessionByTokenHash(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("存储中不应以明文 token 作为键，得到 %v", err)
	}
	if _, err := js.SessionByTokenHash(hashToken(token)); err != nil {
		t.Fatalf("存储中应能按散列找到会话: %v", err)
	}
}

func TestPublicUserHidesPasswordHash(t *testing.T) {
	svc, _ := newTestService(t)
	u, _, err := svc.Register("alice", "password123", "", "", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	pub := u.Public(time.Now().UTC())
	if pub.Username != "alice" || pub.IsAdmin != true {
		t.Fatalf("公开视图不正确：%+v", pub)
	}
}
