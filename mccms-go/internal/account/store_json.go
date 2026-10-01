package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// storeVersion 是数据文件的结构版本，用于将来做迁移。
const storeVersion = 1

// jsonStore 是 Store 的默认实现：把所有账号数据放进内存索引，
// 每次写操作后原子落盘到一个 JSON 文件。
//
// 选择 JSON 而非数据库，是为了与项目「零外部依赖、单二进制」的取向保持一致；
// 由于业务层只依赖 Store 接口，将来换成 SQLite 不需要改动上层。
//
// 并发：所有公开方法都持锁，Store 可被多个 HTTP 请求并发调用。
// 写放大：每次变更都会重写整个文件。对自托管规模（数千条记录）足够；
// 若数据量增长，替换为 SQLite 实现即可。
type jsonStore struct {
	path string

	mu        sync.RWMutex
	users     map[string]*User     // id -> user
	usernames map[string]string    // 归一化用户名 -> id
	sessions  map[string]*Session  // token 散列 -> session
	folders   map[string]*Folder   // id -> folder
	favorites map[string]*Favorite // favKey -> favorite
	history   map[string]*History  // histKey -> history
	notes     map[string]*Note     // id -> note
	audit     []*Audit             // 追加序（旧 -> 新）
	settings  map[string]string    // 键值设置（注册开关等）
}

// NewJSONStore 打开（或创建）指定路径上的 JSON 账号库。
//
// 目录不存在时会自动创建；文件权限为 0600，因为其中包含口令散列。
func NewJSONStore(path string) (Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: 账号库路径不能为空", ErrInvalid)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("创建账号库目录失败: %w", err)
		}
	}

	s := &jsonStore{
		path:      path,
		users:     map[string]*User{},
		usernames: map[string]string{},
		sessions:  map[string]*Session{},
		folders:   map[string]*Folder{},
		favorites: map[string]*Favorite{},
		history:   map[string]*History{},
		notes:     map[string]*Note{},
		audit:     nil,
		settings:  map[string]string{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *jsonStore) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 首次启动：空库
		}
		return fmt.Errorf("读取账号库失败: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}

	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("解析账号库失败: %w", err)
	}
	if d.Version > storeVersion {
		return fmt.Errorf("账号库版本 %d 高于本程序支持的 %d，请升级程序", d.Version, storeVersion)
	}

	for _, u := range d.Users {
		if u == nil || u.ID == "" {
			continue
		}
		s.users[u.ID] = u
		s.usernames[NormalizeUsername(u.Username)] = u.ID
	}
	for _, sess := range d.Sessions {
		if sess == nil || sess.TokenHash == "" {
			continue
		}
		s.sessions[sess.TokenHash] = sess
	}
	for _, f := range d.Folders {
		if f == nil || f.ID == "" {
			continue
		}
		s.folders[f.ID] = f
	}
	for _, f := range d.Favorites {
		if f == nil {
			continue
		}
		s.favorites[favKey(f.UserID, f.Source, f.ComicID)] = f
	}
	for _, h := range d.History {
		if h == nil {
			continue
		}
		s.history[histKey(h.UserID, h.Source, h.ComicID)] = h
	}
	for _, n := range d.Notes {
		if n == nil || n.ID == "" {
			continue
		}
		s.notes[n.ID] = n
	}
	s.audit = append(s.audit, d.Audit...)
	for k, v := range d.Settings {
		s.settings[k] = v
	}

	// 载入时清理一次过期会话，避免失效记录长期堆积。
	now := time.Now().UTC()
	removed := 0
	for hash, sess := range s.sessions {
		if !sess.ExpiresAt.After(now) {
			delete(s.sessions, hash)
			removed++
		}
	}
	if removed > 0 {
		return s.persistLocked()
	}
	return nil
}

// persistLocked 把当前状态原子写入磁盘。调用方必须已持有写锁。
func (s *jsonStore) persistLocked() error {
	d := Data{Version: storeVersion}
	for _, u := range s.users {
		d.Users = append(d.Users, u)
	}
	for _, sess := range s.sessions {
		d.Sessions = append(d.Sessions, sess)
	}
	for _, f := range s.folders {
		d.Folders = append(d.Folders, f)
	}
	for _, f := range s.favorites {
		d.Favorites = append(d.Favorites, f)
	}
	for _, h := range s.history {
		d.History = append(d.History, h)
	}
	for _, n := range s.notes {
		d.Notes = append(d.Notes, n)
	}
	d.Audit = s.audit
	if len(s.settings) > 0 {
		d.Settings = make(map[string]string, len(s.settings))
		for k, v := range s.settings {
			d.Settings[k] = v
		}
	}

	// 排序让文件内容稳定、便于 diff 与人工排查。
	sort.Slice(d.Users, func(i, j int) bool { return d.Users[i].ID < d.Users[j].ID })
	sort.Slice(d.Sessions, func(i, j int) bool { return d.Sessions[i].TokenHash < d.Sessions[j].TokenHash })
	sort.Slice(d.Folders, func(i, j int) bool { return d.Folders[i].ID < d.Folders[j].ID })
	sort.Slice(d.Favorites, func(i, j int) bool {
		return favKey(d.Favorites[i].UserID, d.Favorites[i].Source, d.Favorites[i].ComicID) <
			favKey(d.Favorites[j].UserID, d.Favorites[j].Source, d.Favorites[j].ComicID)
	})
	sort.Slice(d.History, func(i, j int) bool {
		return histKey(d.History[i].UserID, d.History[i].Source, d.History[i].ComicID) <
			histKey(d.History[j].UserID, d.History[j].Source, d.History[j].ComicID)
	})
	sort.Slice(d.Notes, func(i, j int) bool { return d.Notes[i].ID < d.Notes[j].ID })

	buf, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化账号库失败: %w", err)
	}
	buf = append(buf, '\n')

	// 先写临时文件再 rename，保证读者永远看不到半截文件。
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".accounts-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// 成功路径下 rename 已经带走了文件，这里忽略失败。
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置文件权限失败: %w", err)
	}
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入账号库失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步账号库失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("替换账号库失败: %w", err)
	}
	return nil
}

func (s *jsonStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

// ---- 键 ----------------------------------------------------------------------

// favKey / histKey 用 NUL 连接，避免 id 中可能出现的分隔符造成歧义。
func favKey(userID, source, comicID string) string {
	return userID + "\x00" + source + "\x00" + comicID
}

func histKey(userID, source, comicID string) string {
	return userID + "\x00" + source + "\x00" + comicID
}

// ---- 用户 --------------------------------------------------------------------

func (s *jsonStore) CreateUser(u *User) error {
	if u == nil || u.ID == "" {
		return fmt.Errorf("%w: 用户 id 不能为空", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := NormalizeUsername(u.Username)
	if existing, ok := s.usernames[key]; ok && existing != u.ID {
		return fmt.Errorf("%w: 用户名已被占用", ErrConflict)
	}
	s.users[u.ID] = u.clone()
	s.usernames[key] = u.ID
	return s.persistLocked()
}

func (s *jsonStore) UpdateUser(u *User) error {
	if u == nil || u.ID == "" {
		return fmt.Errorf("%w: 用户 id 不能为空", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	old, ok := s.users[u.ID]
	if !ok {
		return ErrNotFound
	}
	// 用户名可能被改名：先释放旧索引再建立新索引。
	delete(s.usernames, NormalizeUsername(old.Username))
	if existing, ok := s.usernames[NormalizeUsername(u.Username)]; ok && existing != u.ID {
		// 冲突时回滚索引，避免把旧键删掉后留下不一致状态。
		s.usernames[NormalizeUsername(old.Username)] = u.ID
		return fmt.Errorf("%w: 用户名已被占用", ErrConflict)
	}
	s.users[u.ID] = u.clone()
	s.usernames[NormalizeUsername(u.Username)] = u.ID
	return s.persistLocked()
}

func (s *jsonStore) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.usernames, NormalizeUsername(u.Username))
	delete(s.users, id)

	// 级联清理：会话、收藏夹、收藏、历史、笔记。审计日志保留，
	// 因为它记录的是「发生过什么」，删号不应抹掉管理员的操作痕迹。
	for hash, sess := range s.sessions {
		if sess.UserID == id {
			delete(s.sessions, hash)
		}
	}
	for fid, f := range s.folders {
		if f.UserID == id {
			delete(s.folders, fid)
		}
	}
	for k, f := range s.favorites {
		if f.UserID == id {
			delete(s.favorites, k)
		}
	}
	for k, h := range s.history {
		if h.UserID == id {
			delete(s.history, k)
		}
	}
	for nid, n := range s.notes {
		if n.UserID == id {
			delete(s.notes, nid)
		}
	}
	return s.persistLocked()
}

func (s *jsonStore) UserByID(id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return u.clone(), nil
}

func (s *jsonStore) UserByUsername(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.usernames[NormalizeUsername(username)]
	if !ok {
		return nil, ErrNotFound
	}
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return u.clone(), nil
}

func (s *jsonStore) ListUsers() ([]*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *jsonStore) CountUsers() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users), nil
}

// ---- 会话 --------------------------------------------------------------------

func (s *jsonStore) CreateSession(sess *Session) error {
	if sess == nil || sess.TokenHash == "" {
		return fmt.Errorf("%w: 会话 token 不能为空", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.TokenHash] = sess.clone()
	return s.persistLocked()
}

func (s *jsonStore) SessionByTokenHash(hash string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[hash]
	if !ok {
		return nil, ErrNotFound
	}
	return sess.clone(), nil
}

func (s *jsonStore) DeleteSession(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[tokenHash]; !ok {
		return ErrNotFound
	}
	delete(s.sessions, tokenHash)
	return s.persistLocked()
}

func (s *jsonStore) DeleteUserSessions(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for hash, sess := range s.sessions {
		if sess.UserID == userID {
			delete(s.sessions, hash)
			n++
		}
	}
	if n == 0 {
		return nil // 无变化就不必落盘
	}
	return s.persistLocked()
}

func (s *jsonStore) PurgeExpiredSessions(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for hash, sess := range s.sessions {
		if !sess.ExpiresAt.After(now) {
			delete(s.sessions, hash)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.persistLocked()
}

// ---- 收藏夹 ------------------------------------------------------------------

func (s *jsonStore) CreateFolder(f *Folder) error {
	if f == nil || f.ID == "" || f.UserID == "" {
		return fmt.Errorf("%w: 收藏夹参数不完整", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.folders[f.ID] = f.clone()
	return s.persistLocked()
}

func (s *jsonStore) RenameFolder(userID, id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.folders[id]
	if !ok || f.UserID != userID {
		return ErrNotFound
	}
	f.Name = name
	return s.persistLocked()
}

func (s *jsonStore) DeleteFolder(userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.folders[id]
	if !ok || f.UserID != userID {
		return ErrNotFound
	}
	delete(s.folders, id)
	// 夹子里的收藏落回「未分组」，而不是跟着一起消失。
	for _, fav := range s.favorites {
		if fav.UserID == userID && fav.FolderID == id {
			fav.FolderID = ""
		}
	}
	return s.persistLocked()
}

func (s *jsonStore) ListFolders(userID string) ([]*Folder, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*Folder{}
	for _, f := range s.folders {
		if f.UserID == userID {
			out = append(out, f.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].Name < out[j].Name
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// ---- 收藏 --------------------------------------------------------------------

func (s *jsonStore) PutFavorite(f *Favorite) error {
	if f == nil || f.UserID == "" || f.ComicID == "" {
		return fmt.Errorf("%w: 收藏参数不完整", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := favKey(f.UserID, f.Source, f.ComicID)
	if old, ok := s.favorites[key]; ok {
		// 重复收藏视为更新：保留首次收藏时间，只刷新元信息与分组。
		c := f.clone()
		c.CreatedAt = old.CreatedAt
		s.favorites[key] = c
		return s.persistLocked()
	}
	s.favorites[key] = f.clone()
	return s.persistLocked()
}

func (s *jsonStore) DeleteFavorite(userID, source, comicID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := favKey(userID, source, comicID)
	if _, ok := s.favorites[key]; !ok {
		return ErrNotFound
	}
	delete(s.favorites, key)
	return s.persistLocked()
}

func (s *jsonStore) FavoriteOf(userID, source, comicID string) (*Favorite, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.favorites[favKey(userID, source, comicID)]
	if !ok {
		return nil, ErrNotFound
	}
	return f.clone(), nil
}

// ListFavorites 按收藏时间倒序列出。
//
// folderID 语义：
//   - "" 或 "0"（DefaultFolderID）：全部收藏
//   - "ungrouped"：未归入任何自建收藏夹的收藏
//   - 其它：该收藏夹 id
func (s *jsonStore) ListFavorites(userID, folderID string, offset, limit int) ([]*Favorite, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := []*Favorite{}
	for _, f := range s.favorites {
		if f.UserID != userID || !folderMatches(f.FolderID, folderID) {
			continue
		}
		matched = append(matched, f.clone())
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ComicID < matched[j].ComicID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})
	return page(matched, offset, limit), len(matched), nil
}

// UngroupedFolderID 供上层引用「未分组」这个虚拟收藏夹。
const UngroupedFolderID = "ungrouped"

func folderMatches(actual, want string) bool {
	switch want {
	case "", DefaultFolderID:
		return true
	case UngroupedFolderID:
		return actual == "" || actual == UngroupedFolderID
	default:
		return actual == want
	}
}

// ---- 阅读历史 ----------------------------------------------------------------

func (s *jsonStore) PutHistory(h *History) error {
	if h == nil || h.UserID == "" || h.ComicID == "" {
		return fmt.Errorf("%w: 历史参数不完整", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[histKey(h.UserID, h.Source, h.ComicID)] = h.clone()
	return s.persistLocked()
}

func (s *jsonStore) DeleteHistory(userID, source, comicID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := histKey(userID, source, comicID)
	if _, ok := s.history[key]; !ok {
		return ErrNotFound
	}
	delete(s.history, key)
	return s.persistLocked()
}

func (s *jsonStore) ClearHistory(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, h := range s.history {
		if h.UserID == userID {
			delete(s.history, k)
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return s.persistLocked()
}

func (s *jsonStore) ListHistory(userID string, offset, limit int) ([]*History, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := []*History{}
	for _, h := range s.history {
		if h.UserID == userID {
			matched = append(matched, h.clone())
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].UpdatedAt.Equal(matched[j].UpdatedAt) {
			return matched[i].ComicID < matched[j].ComicID
		}
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	return page(matched, offset, limit), len(matched), nil
}

// ---- 阅读笔记 ----------------------------------------------------------------

func (s *jsonStore) CreateNote(n *Note) error {
	if n == nil || n.ID == "" || n.UserID == "" {
		return fmt.Errorf("%w: 笔记参数不完整", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes[n.ID] = n.clone()
	return s.persistLocked()
}

func (s *jsonStore) UpdateNote(n *Note) error {
	if n == nil || n.ID == "" {
		return fmt.Errorf("%w: 笔记 id 不能为空", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.notes[n.ID]
	if !ok || old.UserID != n.UserID {
		return ErrNotFound
	}
	s.notes[n.ID] = n.clone()
	return s.persistLocked()
}

func (s *jsonStore) DeleteNote(userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.notes[id]
	if !ok || n.UserID != userID {
		return ErrNotFound
	}
	delete(s.notes, id)
	return s.persistLocked()
}

// ListNotes 按更新时间倒序列出。source / comicID 非空时按作品过滤。
func (s *jsonStore) ListNotes(userID, source, comicID string, offset, limit int) ([]*Note, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := []*Note{}
	for _, n := range s.notes {
		if n.UserID != userID {
			continue
		}
		if source != "" && n.Source != source {
			continue
		}
		if comicID != "" && n.ComicID != comicID {
			continue
		}
		matched = append(matched, n.clone())
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].UpdatedAt.Equal(matched[j].UpdatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	return page(matched, offset, limit), len(matched), nil
}

// ---- 审计 --------------------------------------------------------------------

func (s *jsonStore) AppendAudit(a *Audit) error {
	if a == nil || a.ID == "" {
		return fmt.Errorf("%w: 审计记录不完整", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, a.clone())
	return s.persistLocked()
}

// ListAudit 按时间倒序返回审计记录（最新在前）。
func (s *jsonStore) ListAudit(offset, limit int) ([]*Audit, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rev := make([]*Audit, 0, len(s.audit))
	for i := len(s.audit) - 1; i >= 0; i-- {
		rev = append(rev, s.audit[i].clone())
	}
	return page(rev, offset, limit), len(rev), nil
}

// ---- 设置 --------------------------------------------------------------------

func (s *jsonStore) GetSetting(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.settings[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *jsonStore) SetSetting(key, value string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("%w: 设置键不能为空", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings[key] == value {
		return nil // 无变化不必落盘
	}
	s.settings[key] = value
	return s.persistLocked()
}

func (s *jsonStore) AllSettings() (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.settings))
	for k, v := range s.settings {
		out[k] = v
	}
	return out, nil
}

// ---- 统计 --------------------------------------------------------------------

func (s *jsonStore) Counts(now time.Time) (Counts, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var c Counts
	c.Users = len(s.users)
	for _, u := range s.users {
		if u.Role == RoleAdmin {
			c.Admins++
		}
		if u.Status == StatusDisabled {
			c.DisabledUsers++
		}
	}
	for _, sess := range s.sessions {
		if sess.ExpiresAt.After(now) {
			c.Sessions++
		}
	}
	c.Folders = len(s.folders)
	c.Favorites = len(s.favorites)
	c.History = len(s.history)
	c.Notes = len(s.notes)
	return c, nil
}

// ---- 分页工具 ----------------------------------------------------------------

// page 对已排序切片做边界安全的分页。
func page[T any](items []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}
	}
	end := len(items)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return items[offset:end]
}
