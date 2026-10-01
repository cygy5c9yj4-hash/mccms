package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 默认参数。
const (
	// DefaultSessionTTL 会话有效期。
	DefaultSessionTTL = 30 * 24 * time.Hour

	// DefaultPageSize / MaxPageSize 列表分页的默认与上限。
	DefaultPageSize = 20
	MaxPageSize     = 100

	// MinUsernameLen / MaxUsernameLen 用户名长度限制。
	MinUsernameLen = 3
	MaxUsernameLen = 32

	// auditLimit 内存中不必限制；这里只用于默认分页。
	auditDefaultLimit = 50

	// firstUserAdmin 决定「首个注册用户自动成为管理员」这一自助引导行为。
	firstUserAdmin = true
)

// SettingRegistrationOpen 是「是否开放注册」的设置键。
const SettingRegistrationOpen = "registration_open"

// usernameRe 限定用户名可用字符。使用 ASCII 字符集是有意为之：
// 避免不同 Unicode 归一化形式造成「看起来一样、实际不同」的账号。
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Options 构造 Service 的参数。
type Options struct {
	// Store 必填：持久化实现。
	Store Store
	// SessionTTL 会话有效期，默认 DefaultSessionTTL。
	SessionTTL time.Duration
	// Now 注入时钟，便于测试；默认 time.Now。
	Now func() time.Time
}

// Service 账号业务逻辑。HTTP 层只应该通过它与存储交互。
type Service struct {
	store      Store
	sessionTTL time.Duration
	now        func() time.Time

	// mu 保护「首个用户成为管理员」这类需要跨多次存储调用的复合操作。
	// 注意：sync.Mutex 不可重入，持有 mu 期间不得调用会再次加锁的方法。
	mu sync.Mutex
	// purgeMu 单独保护 lastPurge，避免与 mu 互相等待（见 maybePurge）。
	purgeMu   sync.Mutex
	lastPurge time.Time
}

// New 创建账号服务。
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("%w: 必须提供 Store", ErrInvalid)
	}
	ttl := opts.SessionTTL
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: opts.Store, sessionTTL: ttl, now: now}, nil
}

// Store 暴露底层存储（管理后台统计等只读用途）。
func (s *Service) Store() Store { return s.store }

// SessionTTL 返回当前会话有效期。
func (s *Service) SessionTTL() time.Duration { return s.sessionTTL }

// Bootstrap 初始化默认设置并清理过期会话。启动时调用一次即可。
func (s *Service) Bootstrap() error {
	if err := s.ensureSetting(SettingRegistrationOpen, "1"); err != nil {
		return err
	}
	_, err := s.store.PurgeExpiredSessions(s.now())
	return err
}

func (s *Service) ensureSetting(key, def string) error {
	if _, err := s.store.GetSetting(key); err == nil {
		return nil
	}
	return s.store.SetSetting(key, def)
}

// ---- 会话 --------------------------------------------------------------------

// newSessionToken 生成明文 token 与其存储用的散列值。
//
// 存储里只保留散列，因此账号库文件泄露也无法直接冒用他人会话。
func newSessionToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("生成会话 token 失败: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, hashToken(token), nil
}

// hashToken 计算 token 的 SHA-256 十六进制表示。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueSession 为用户签发一个新会话，返回明文 token。
func (s *Service) IssueSession(u *User, ua, ip string) (string, error) {
	token, hash, err := newSessionToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	sess := &Session{
		TokenHash: hash,
		UserID:    u.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(s.sessionTTL),
		UserAgent: truncate(ua, 256),
		IP:        truncate(ip, 64),
	}
	if err := s.store.CreateSession(sess); err != nil {
		return "", err
	}
	s.maybePurge()
	return token, nil
}

// maybePurge 偶尔清理过期会话，避免长期运行后存储无限增长。
// 每小时最多一次，失败不影响主流程。
func (s *Service) maybePurge() {
	// 这里刻意使用独立的 purgeMu：IssueSession 可能在持有 mu 的情况下被调用
	// （如 Register），若共用一把锁会因为 sync.Mutex 不可重入而自死锁。
	s.purgeMu.Lock()
	due := s.now().Sub(s.lastPurge) > time.Hour
	if due {
		s.lastPurge = s.now()
	}
	s.purgeMu.Unlock()

	if due {
		_, _ = s.store.PurgeExpiredSessions(s.now())
	}
}

// Authenticate 校验明文 token 并返回对应用户。
//
// 会话过期、用户被删除或被禁用都会返回错误，从而让已有登录态立即失效。
func (s *Service) Authenticate(token string) (*User, *Session, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil, ErrUnauthenticated
	}
	hash := hashToken(token)

	sess, err := s.store.SessionByTokenHash(hash)
	if err != nil {
		if err == ErrNotFound {
			return nil, nil, ErrUnauthenticated
		}
		return nil, nil, err
	}
	now := s.now()
	if !sess.ExpiresAt.After(now) {
		// 顺手清掉这条失效记录。
		_ = s.store.DeleteSession(hash)
		return nil, nil, ErrUnauthenticated
	}

	u, err := s.store.UserByID(sess.UserID)
	if err != nil {
		if err == ErrNotFound {
			return nil, nil, ErrUnauthenticated
		}
		return nil, nil, err
	}
	if u.Status == StatusDisabled {
		// 被禁用的账号：立刻吊销全部会话。
		_ = s.store.DeleteUserSessions(u.ID)
		return nil, nil, ErrDisabled
	}
	return u, sess, nil
}

// Logout 吊销指定 token 对应的会话。
func (s *Service) Logout(token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	err := s.store.DeleteSession(hashToken(token))
	if err == ErrNotFound {
		return nil // 已经失效，视为登出成功
	}
	return err
}

// ---- 注册 / 登录 --------------------------------------------------------------

// ValidateUsername 校验用户名格式。
func ValidateUsername(name string) error {
	name = strings.TrimSpace(name)
	if len(name) < MinUsernameLen || len(name) > MaxUsernameLen {
		return fmt.Errorf("%w: 用户名长度需在 %d-%d 个字符之间", ErrInvalid, MinUsernameLen, MaxUsernameLen)
	}
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("%w: 用户名只能包含字母、数字、下划线、点与连字符", ErrInvalid)
	}
	return nil
}

// ValidateEmail 做最基本的邮箱格式校验；留空表示不填。
func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}
	if len(email) > 254 || strings.Count(email, "@") != 1 {
		return fmt.Errorf("%w: 邮箱格式不正确", ErrInvalid)
	}
	parts := strings.SplitN(email, "@", 2)
	if parts[0] == "" || parts[1] == "" || !strings.Contains(parts[1], ".") {
		return fmt.Errorf("%w: 邮箱格式不正确", ErrInvalid)
	}
	return nil
}

// RegistrationOpen 报告当前是否开放注册。
func (s *Service) RegistrationOpen() bool {
	v, err := s.store.GetSetting(SettingRegistrationOpen)
	if err != nil {
		return true // 读取失败时按默认开放处理
	}
	return v != "0"
}

// SetRegistrationOpen 设置注册开关（仅管理员）。
func (s *Service) SetRegistrationOpen(open bool, actor *User, ip string) error {
	v := "0"
	if open {
		v = "1"
	}
	if err := s.store.SetSetting(SettingRegistrationOpen, v); err != nil {
		return err
	}
	detail := "关闭注册"
	if open {
		detail = "开放注册"
	}
	return s.audit(actor, "settings.update", "", "registration", detail, ip)
}

// Register 注册新账号并直接签发会话。
//
// 引导行为：当库中还没有任何用户时，第一个注册者自动成为管理员，
// 这样自托管场景下无需额外的命令行参数即可完成初始化。
func (s *Service) Register(username, password, displayName, email, ua, ip string) (*User, string, error) {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return nil, "", err
	}
	if err := ValidateEmail(email); err != nil {
		return nil, "", err
	}

	// 注册是「读计数 -> 写用户」的复合操作，需要串行化，
	// 否则并发注册时可能产生两个管理员。
	s.mu.Lock()
	defer s.mu.Unlock()

	count, err := s.store.CountUsers()
	if err != nil {
		return nil, "", err
	}
	if count > 0 && !s.RegistrationOpen() {
		return nil, "", fmt.Errorf("%w: 管理员已关闭注册", ErrForbidden)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, "", err
	}

	now := s.now()
	role := RoleUser
	if count == 0 && firstUserAdmin {
		role = RoleAdmin
	}

	u := &User{
		ID:           NewID("usr_"),
		Username:     username,
		DisplayName:  strings.TrimSpace(displayName),
		Email:        strings.TrimSpace(email),
		PasswordHash: hash,
		Role:         role,
		Status:       StatusActive,
		Tier:         TierFree,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateUser(u); err != nil {
		return nil, "", err
	}

	detail := "自助注册"
	if role == RoleAdmin {
		detail = "首个用户，自动成为管理员"
	}
	if err := s.audit(u, "user.register", u.ID, u.Username, detail, ip); err != nil {
		return nil, "", err
	}

	token, err := s.IssueSession(u, ua, ip)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// Login 校验口令并签发会话。
func (s *Service) Login(username, password, ua, ip string) (*User, string, error) {
	u, err := s.store.UserByUsername(username)
	if err != nil {
		if err == ErrNotFound {
			// 不区分「用户不存在」与「口令错误」，避免账号枚举。
			return nil, "", ErrUnauthenticated
		}
		return nil, "", err
	}

	ok, err := VerifyPassword(u.PasswordHash, password)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		_ = s.audit(nil, "user.login_failed", u.ID, u.Username, "口令错误", ip)
		return nil, "", ErrUnauthenticated
	}
	if u.Status == StatusDisabled {
		return nil, "", ErrDisabled
	}

	now := s.now()
	u.LastLoginAt = &now
	u.UpdatedAt = now
	if err := s.store.UpdateUser(u); err != nil {
		return nil, "", err
	}

	token, err := s.IssueSession(u, ua, ip)
	if err != nil {
		return nil, "", err
	}
	if err := s.audit(u, "user.login", u.ID, u.Username, "", ip); err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// ChangePassword 修改自己的口令。返回新的会话 token（旧会话全部作废）。
func (s *Service) ChangePassword(user *User, oldPassword, newPassword, ua, ip string) (string, error) {
	if user == nil {
		return "", ErrUnauthenticated
	}
	ok, err := VerifyPassword(user.PasswordHash, oldPassword)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: 原密码不正确", ErrUnauthenticated)
	}
	if oldPassword == newPassword {
		return "", fmt.Errorf("%w: 新密码不能与原密码相同", ErrInvalid)
	}
	return s.setPassword(user, newPassword, "user.password_change", "用户自行修改密码", ua, ip)
}

// setPassword 落库新口令并重置该用户的全部会话，然后签发一个新会话。
// 这样改密后其它设备上的登录态会立刻失效。
func (s *Service) setPassword(u *User, newPassword, action, detail, ua, ip string) (string, error) {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return "", err
	}
	u.PasswordHash = hash
	u.UpdatedAt = s.now()
	if err := s.store.UpdateUser(u); err != nil {
		return "", err
	}
	if err := s.store.DeleteUserSessions(u.ID); err != nil {
		return "", err
	}
	token, err := s.IssueSession(u, ua, ip)
	if err != nil {
		return "", err
	}
	if err := s.audit(u, action, u.ID, u.Username, detail, ip); err != nil {
		return "", err
	}
	return token, nil
}

// ---- 收藏夹 ------------------------------------------------------------------

// CreateFolder 新建收藏夹。
func (s *Service) CreateFolder(userID, name string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: 收藏夹名称不能为空", ErrInvalid)
	}
	if len([]rune(name)) > 40 {
		return nil, fmt.Errorf("%w: 收藏夹名称过长", ErrInvalid)
	}
	existing, err := s.store.ListFolders(userID)
	if err != nil {
		return nil, err
	}
	for _, f := range existing {
		if strings.EqualFold(f.Name, name) {
			return nil, fmt.Errorf("%w: 同名收藏夹已存在", ErrConflict)
		}
	}

	f := &Folder{ID: NewID("fld_"), UserID: userID, Name: name, CreatedAt: s.now()}
	if err := s.store.CreateFolder(f); err != nil {
		return nil, err
	}
	return f, nil
}

// RenameFolder 重命名收藏夹。
func (s *Service) RenameFolder(userID, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: 收藏夹名称不能为空", ErrInvalid)
	}
	if len([]rune(name)) > 40 {
		return fmt.Errorf("%w: 收藏夹名称过长", ErrInvalid)
	}
	return s.store.RenameFolder(userID, id, name)
}

// DeleteFolder 删除收藏夹；夹内收藏会落回「未分组」。
func (s *Service) DeleteFolder(userID, id string) error {
	if id == "" || id == DefaultFolderID || id == UngroupedFolderID {
		return fmt.Errorf("%w: 默认收藏夹不可删除", ErrInvalid)
	}
	return s.store.DeleteFolder(userID, id)
}

// ListFolders 列出用户的自建收藏夹。
func (s *Service) ListFolders(userID string) ([]*Folder, error) {
	return s.store.ListFolders(userID)
}

// ---- 收藏 --------------------------------------------------------------------

// AddFavorite 收藏一部作品（重复调用等于更新元信息 / 移动分组）。
func (s *Service) AddFavorite(f *Favorite) error {
	if f == nil {
		return fmt.Errorf("%w: 收藏参数不合法", ErrInvalid)
	}
	if strings.TrimSpace(f.Source) == "" {
		f.Source = "unknown"
	}
	if strings.TrimSpace(f.ComicID) == "" {
		return fmt.Errorf("%w: 作品 id 不能为空", ErrInvalid)
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = s.now()
	}
	return s.store.PutFavorite(f)
}

// RemoveFavorite 取消收藏。
func (s *Service) RemoveFavorite(userID, source, comicID string) error {
	err := s.store.DeleteFavorite(userID, source, comicID)
	if err == ErrNotFound {
		return nil // 幂等：本来就没收藏
	}
	return err
}

// IsFavorite 报告某作品是否已被该用户收藏。
func (s *Service) IsFavorite(userID, source, comicID string) bool {
	_, err := s.store.FavoriteOf(userID, source, comicID)
	return err == nil
}

// ListFavorites 分页列出收藏。folderID 语义见 Store.ListFavorites 的说明。
func (s *Service) ListFavorites(userID, folderID string, pageNo, pageSize int) ([]*Favorite, int, error) {
	pageNo, pageSize = normalizePaging(pageNo, pageSize)
	items, total, err := s.store.ListFavorites(userID, folderID, (pageNo-1)*pageSize, pageSize)
	return items, total, err
}

// FavoriteCountByFolder 返回每个收藏夹的收藏数，供前端显示角标。
func (s *Service) FavoriteCountByFolder(userID string) (map[string]int, error) {
	out := map[string]int{}
	// 收藏总量在自托管规模下很小，直接全量统计更简单可靠。
	items, _, err := s.store.ListFavorites(userID, "", 0, 0)
	if err != nil {
		return nil, err
	}
	out[DefaultFolderID] = len(items)
	for _, f := range items {
		key := f.FolderID
		if key == "" {
			key = UngroupedFolderID
		}
		out[key]++
	}
	return out, nil
}

// ---- 阅读历史 ----------------------------------------------------------------

// RecordHistory 记录 / 更新阅读进度。
func (s *Service) RecordHistory(h *History) error {
	if h == nil {
		return fmt.Errorf("%w: 历史参数不合法", ErrInvalid)
	}
	if strings.TrimSpace(h.ComicID) == "" {
		return fmt.Errorf("%w: 作品 id 不能为空", ErrInvalid)
	}
	if strings.TrimSpace(h.Source) == "" {
		h.Source = "unknown"
	}
	if h.Page < 0 {
		h.Page = 0
	}
	h.UpdatedAt = s.now()
	return s.store.PutHistory(h)
}

// ListHistory 分页列出阅读历史（最近阅读在前）。
func (s *Service) ListHistory(userID string, pageNo, pageSize int) ([]*History, int, error) {
	pageNo, pageSize = normalizePaging(pageNo, pageSize)
	return s.store.ListHistory(userID, (pageNo-1)*pageSize, pageSize)
}

// DeleteHistory 删除单条历史。
func (s *Service) DeleteHistory(userID, source, comicID string) error {
	err := s.store.DeleteHistory(userID, source, comicID)
	if err == ErrNotFound {
		return nil
	}
	return err
}

// ClearHistory 清空该用户的全部历史。
func (s *Service) ClearHistory(userID string) error {
	return s.store.ClearHistory(userID)
}

// ---- 阅读笔记 ----------------------------------------------------------------

// CreateNote 新建笔记。
func (s *Service) CreateNote(n *Note) (*Note, error) {
	if n == nil {
		return nil, fmt.Errorf("%w: 笔记参数不合法", ErrInvalid)
	}
	body := strings.TrimSpace(n.Body)
	if body == "" {
		return nil, fmt.Errorf("%w: 笔记内容不能为空", ErrInvalid)
	}
	if len([]rune(body)) > 4000 {
		return nil, fmt.Errorf("%w: 笔记内容过长（上限 4000 字）", ErrInvalid)
	}
	if strings.TrimSpace(n.ComicID) == "" {
		return nil, fmt.Errorf("%w: 作品 id 不能为空", ErrInvalid)
	}
	if strings.TrimSpace(n.Source) == "" {
		n.Source = "unknown"
	}

	now := s.now()
	out := &Note{
		ID:         NewID("note_"),
		UserID:     n.UserID,
		Source:     n.Source,
		ComicID:    n.ComicID,
		ComicTitle: strings.TrimSpace(n.ComicTitle),
		Kind:       n.Kind,
		Body:       body,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.store.CreateNote(out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateNote 编辑自己的笔记。
func (s *Service) UpdateNote(userID, id, body string) (*Note, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("%w: 笔记内容不能为空", ErrInvalid)
	}
	if len([]rune(body)) > 4000 {
		return nil, fmt.Errorf("%w: 笔记内容过长（上限 4000 字）", ErrInvalid)
	}
	// 先按 id 取回：Store 通过 ListNotes 过滤成本高，这里用全量反查保证归属校验。
	notes, _, err := s.store.ListNotes(userID, "", "", 0, 0)
	if err != nil {
		return nil, err
	}
	var target *Note
	for _, n := range notes {
		if n.ID == id {
			target = n
			break
		}
	}
	if target == nil {
		return nil, ErrNotFound
	}
	target.Body = body
	target.UpdatedAt = s.now()
	if err := s.store.UpdateNote(target); err != nil {
		return nil, err
	}
	return target, nil
}

// DeleteNote 删除自己的笔记。
func (s *Service) DeleteNote(userID, id string) error {
	err := s.store.DeleteNote(userID, id)
	if err == ErrNotFound {
		return nil
	}
	return err
}

// ListNotes 分页列出笔记。
func (s *Service) ListNotes(userID, source, comicID string, pageNo, pageSize int) ([]*Note, int, error) {
	pageNo, pageSize = normalizePaging(pageNo, pageSize)
	return s.store.ListNotes(userID, source, comicID, (pageNo-1)*pageSize, pageSize)
}

// ---- 管理后台 ----------------------------------------------------------------

// ListUsers 列出全部用户（管理员视图，含状态与角色）。
func (s *Service) ListUsers() ([]PublicUser, error) {
	users, err := s.store.ListUsers()
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]PublicUser, 0, len(users))
	for _, u := range users {
		out = append(out, u.Public(now))
	}
	return out, nil
}

// UserByID 取单个用户的安全视图。
func (s *Service) UserByID(id string) (*PublicUser, error) {
	u, err := s.store.UserByID(id)
	if err != nil {
		return nil, err
	}
	p := u.Public(s.now())
	return &p, nil
}

// AdminSetRole 调整用户角色。
//
// 自我保护：管理员不能把自己降级，否则可能把系统锁死在没有管理员的局面。
func (s *Service) AdminSetRole(actor *User, targetID string, role Role, ip string) error {
	if !role.Valid() {
		return fmt.Errorf("%w: 未知角色 %q", ErrInvalid, role)
	}
	if actor != nil && actor.ID == targetID && role != RoleAdmin {
		return fmt.Errorf("%w: 不能取消自己的管理员身份", ErrForbidden)
	}

	target, err := s.store.UserByID(targetID)
	if err != nil {
		return err
	}
	target.Role = role
	target.UpdatedAt = s.now()
	if err := s.store.UpdateUser(target); err != nil {
		return err
	}
	// 降级为普通用户时，已有的管理会话不必保留。
	if role != RoleAdmin {
		if err := s.store.DeleteUserSessions(target.ID); err != nil {
			return err
		}
	}
	return s.audit(actor, "admin.set_role", target.ID, target.Username, string(role), ip)
}

// AdminSetStatus 启用 / 禁用账号。禁用会立刻吊销该用户全部会话。
func (s *Service) AdminSetStatus(actor *User, targetID string, status Status, ip string) error {
	if !status.Valid() {
		return fmt.Errorf("%w: 未知状态 %q", ErrInvalid, status)
	}
	if actor != nil && actor.ID == targetID && status != StatusActive {
		return fmt.Errorf("%w: 不能禁用自己的账号", ErrForbidden)
	}

	target, err := s.store.UserByID(targetID)
	if err != nil {
		return err
	}
	target.Status = status
	target.UpdatedAt = s.now()
	if err := s.store.UpdateUser(target); err != nil {
		return err
	}
	if status == StatusDisabled {
		if err := s.store.DeleteUserSessions(target.ID); err != nil {
			return err
		}
	}
	return s.audit(actor, "admin.set_status", target.ID, target.Username, string(status), ip)
}

// AdminSetTier 设置用户权益等级。expiresAt 为 nil 表示永久。
//
// 这是 VIP 功能的基础设施：当前只有 free / vip 两档，
// 将来接入付费或兑换码时，只需在此基础上扩展，不必改动账号模型。
func (s *Service) AdminSetTier(actor *User, targetID string, tier Tier, expiresAt *time.Time, ip string) error {
	if !tier.Valid() {
		return fmt.Errorf("%w: 未知权益等级 %q", ErrInvalid, tier)
	}
	target, err := s.store.UserByID(targetID)
	if err != nil {
		return err
	}
	target.Tier = tier
	if tier == TierFree {
		target.TierExpiresAt = nil
	} else {
		target.TierExpiresAt = expiresAt
	}
	target.UpdatedAt = s.now()
	if err := s.store.UpdateUser(target); err != nil {
		return err
	}

	detail := string(tier)
	if expiresAt != nil {
		detail += " 至 " + expiresAt.Format(time.RFC3339)
	}
	return s.audit(actor, "admin.set_tier", target.ID, target.Username, detail, ip)
}

// AdminResetPassword 由管理员重置某用户的口令，并作废其全部会话。
func (s *Service) AdminResetPassword(actor *User, targetID, newPassword, ip string) error {
	target, err := s.store.UserByID(targetID)
	if err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	target.PasswordHash = hash
	target.UpdatedAt = s.now()
	if err := s.store.UpdateUser(target); err != nil {
		return err
	}
	if err := s.store.DeleteUserSessions(target.ID); err != nil {
		return err
	}
	return s.audit(actor, "admin.reset_password", target.ID, target.Username, "管理员重置口令", ip)
}

// AdminDeleteUser 删除账号，级联清理其全部自有数据。
func (s *Service) AdminDeleteUser(actor *User, targetID, ip string) error {
	if actor != nil && actor.ID == targetID {
		return fmt.Errorf("%w: 不能删除自己的账号", ErrForbidden)
	}
	target, err := s.store.UserByID(targetID)
	if err != nil {
		return err
	}
	name := target.Username
	if err := s.store.DeleteUser(targetID); err != nil {
		return err
	}
	return s.audit(actor, "admin.delete_user", targetID, name, "", ip)
}

// AdminRevokeSessions 强制某用户下线。
func (s *Service) AdminRevokeSessions(actor *User, targetID, ip string) error {
	target, err := s.store.UserByID(targetID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteUserSessions(targetID); err != nil {
		return err
	}
	return s.audit(actor, "admin.revoke_sessions", target.ID, target.Username, "强制下线", ip)
}

// Counts 返回数据量统计，供仪表盘使用。
func (s *Service) Counts() (Counts, error) {
	return s.store.Counts(s.now())
}

// Audit 分页返回审计日志（最新在前）。
func (s *Service) Audit(pageNo, pageSize int) ([]*Audit, int, error) {
	pageNo, pageSize = normalizePaging(pageNo, pageSize)
	return s.store.ListAudit((pageNo-1)*pageSize, pageSize)
}

// Settings 返回可公开给管理后台的设置项。
func (s *Service) Settings() (map[string]any, error) {
	all, err := s.store.AllSettings()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		SettingRegistrationOpen: all[SettingRegistrationOpen] != "0",
	}, nil
}

// ---- 内部工具 ----------------------------------------------------------------

// audit 追加一条审计记录。审计失败不应让业务操作失败，因此调用方
// 可以选择忽略返回的错误；需要强一致的场景（如管理员操作）则向上传播。
func (s *Service) audit(actor *User, action, targetID, target, detail, ip string) error {
	a := &Audit{
		ID:       NewID("aud_"),
		At:       s.now(),
		Action:   action,
		TargetID: targetID,
		Target:   target,
		Detail:   detail,
		IP:       truncate(ip, 64),
	}
	if actor != nil {
		a.ActorID = actor.ID
		a.ActorName = actor.Username
	}
	return s.store.AppendAudit(a)
}

// LogAudit 供 HTTP 层记录只读或轻量操作。
func (s *Service) LogAudit(actor *User, action, targetID, target, detail, ip string) error {
	return s.audit(actor, action, targetID, target, detail, ip)
}

func normalizePaging(pageNo, pageSize int) (int, int) {
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return pageNo, pageSize
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var _ = auditDefaultLimit
