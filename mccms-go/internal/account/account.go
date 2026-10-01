// Package account 实现 mccms 自有的账号体系。
//
// 与 internal/mc 里的「站点账号」不同，这里的账号完全由本服务自己持有：
// 注册、登录、会话、收藏夹、收藏、阅读历史、阅读笔记，以及管理后台需要的
// 用户管理与审计日志。它不依赖任何上游站点，后续的 VIP / 权益功能
// 直接在本包之上扩展（见 Tier 与 User.EffectiveTier）。
//
// 存储通过 Store 接口抽象，默认实现是单个 JSON 文件（见 store_json.go）。
// 业务层（service.go）只依赖 Store，因此换成 SQLite 等后端时无需改动
// 业务逻辑与 HTTP 层。
package account

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ---- 角色 / 状态 / 权益 ------------------------------------------------------

// Role 账号角色。
type Role string

const (
	// RoleUser 普通用户。
	RoleUser Role = "user"
	// RoleAdmin 管理员，可访问 /api/admin/*。
	RoleAdmin Role = "admin"
)

// Valid 报告 r 是否为已知角色。
func (r Role) Valid() bool { return r == RoleUser || r == RoleAdmin }

// Status 账号状态。
type Status string

const (
	// StatusActive 正常可用。
	StatusActive Status = "active"
	// StatusDisabled 已被管理员禁用，无法登录，已有会话在下一次请求时失效。
	StatusDisabled Status = "disabled"
)

// Valid 报告 s 是否为已知状态。
func (s Status) Valid() bool { return s == StatusActive || s == StatusDisabled }

// Tier 权益等级。当前只有免费档；vip 是为后续付费/特权功能预留的落点。
type Tier string

const (
	// TierFree 免费用户。
	TierFree Tier = "free"
	// TierVIP 特权用户。
	TierVIP Tier = "vip"
)

// Valid 报告 t 是否为已知等级。
func (t Tier) Valid() bool { return t == TierFree || t == TierVIP }

// ---- 实体 --------------------------------------------------------------------

// User 一个自有账号。
//
// PasswordHash 永远不会出现在任何 API 响应里；对外统一走 Public()。
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	// AfdianUserID 是已绑定的爱发电账号 ID（订单中的 user_id）。
	// 一旦绑定，该爱发电账号之后的每一笔订单都会自动归户，无需再填备注。
	AfdianUserID  string     `json:"afdian_user_id,omitempty"`
	AfdianBoundAt *time.Time `json:"afdian_bound_at,omitempty"`
	PasswordHash  string     `json:"password_hash"`
	Role          Role       `json:"role"`
	Status        Status     `json:"status"`
	Tier          Tier       `json:"tier"`
	TierExpiresAt *time.Time `json:"tier_expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
}

// IsAdmin 报告该账号是否为管理员。
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// IsActive 报告该账号是否处于可用状态。
func (u *User) IsActive() bool { return u != nil && u.Status == StatusActive }

// EffectiveTier 返回在 now 时刻实际生效的权益等级。
//
// 到期时间已过的 VIP 自动回落为免费档，这样调用方不必各自判断过期。
func (u *User) EffectiveTier(now time.Time) Tier {
	if u == nil {
		return TierFree
	}
	if u.Tier != TierVIP {
		return TierFree
	}
	if u.TierExpiresAt == nil {
		// 未设置到期时间 = 永久
		return TierVIP
	}
	if now.Before(*u.TierExpiresAt) {
		return TierVIP
	}
	return TierFree
}

// HasTier 报告在 now 时刻是否至少具备 tier 等级。
func (u *User) HasTier(now time.Time, tier Tier) bool {
	if tier == TierFree {
		return u != nil
	}
	return u.EffectiveTier(now) == tier
}

// PublicUser 是账号对外的安全视图：不含密码散列。
type PublicUser struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name,omitempty"`
	Email       string     `json:"email,omitempty"`
	Role        Role       `json:"role"`
	Status      Status     `json:"status"`
	Tier        Tier       `json:"tier"`
	IsAdmin     bool       `json:"is_admin"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// Public 返回可安全序列化给客户端的视图。
func (u *User) Public(now time.Time) PublicUser {
	return PublicUser{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Role:        u.Role,
		Status:      u.Status,
		Tier:        u.EffectiveTier(now),
		IsAdmin:     u.Role == RoleAdmin,
		CreatedAt:   u.CreatedAt,
		LastLoginAt: u.LastLoginAt,
	}
}

func (u *User) clone() *User {
	if u == nil {
		return nil
	}
	c := *u
	c.TierExpiresAt = cloneTime(u.TierExpiresAt)
	c.LastLoginAt = cloneTime(u.LastLoginAt)
	return &c
}

// Session 一次登录产生的会话。服务端只保存 token 的 SHA-256，
// 因此即使数据文件泄露也无法直接冒用会话。
type Session struct {
	TokenHash string    `json:"token_hash"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UserAgent string    `json:"user_agent,omitempty"`
	IP        string    `json:"ip,omitempty"`
}

func (s *Session) clone() *Session {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

// Folder 用户自建的收藏夹分组。每个用户还有一个隐式的「默认收藏夹」，
// 其 id 为 DefaultFolderID，不需要在存储里真实存在。
type Folder struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func (f *Folder) clone() *Folder {
	if f == nil {
		return nil
	}
	c := *f
	return &c
}

// DefaultFolderID 是默认收藏夹的 id：folder_id 为空或等于它时归入默认收藏夹。
const DefaultFolderID = "0"

// Favorite 一条收藏。(UserID, Source, ComicID) 唯一。
type Favorite struct {
	UserID    string    `json:"user_id"`
	FolderID  string    `json:"folder_id"`
	Source    string    `json:"source"`
	ComicID   string    `json:"comic_id"`
	Title     string    `json:"title,omitempty"`
	Author    string    `json:"author,omitempty"`
	CoverURL  string    `json:"cover_url,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Kind      string    `json:"kind,omitempty"` // comic / novel
	CreatedAt time.Time `json:"created_at"`
}

func (f *Favorite) clone() *Favorite {
	if f == nil {
		return nil
	}
	c := *f
	if f.Tags != nil {
		c.Tags = append([]string(nil), f.Tags...)
	}
	return &c
}

// History 一条阅读历史。(UserID, Source, ComicID) 唯一，重复阅读会原地更新
// 章节与进度并刷新时间。
type History struct {
	UserID       string    `json:"user_id"`
	Source       string    `json:"source"`
	ComicID      string    `json:"comic_id"`
	Title        string    `json:"title,omitempty"`
	CoverURL     string    `json:"cover_url,omitempty"`
	Kind         string    `json:"kind,omitempty"` // comic / novel
	ChapterID    string    `json:"chapter_id,omitempty"`
	ChapterTitle string    `json:"chapter_title,omitempty"`
	Page         int       `json:"page"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (h *History) clone() *History {
	if h == nil {
		return nil
	}
	c := *h
	return &c
}

// Note 一条阅读笔记（对应前端的「阅读笔记 / 推荐」）。
type Note struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Source     string    `json:"source"`
	ComicID    string    `json:"comic_id"`
	ComicTitle string    `json:"comic_title,omitempty"`
	Kind       string    `json:"kind,omitempty"` // comic / novel
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (n *Note) clone() *Note {
	if n == nil {
		return nil
	}
	c := *n
	return &c
}

// Audit 一条审计记录，记录「谁在什么时候做了什么」。
type Audit struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	ActorID   string    `json:"actor_id,omitempty"`
	ActorName string    `json:"actor_name,omitempty"`
	Action    string    `json:"action"`
	TargetID  string    `json:"target_id,omitempty"`
	Target    string    `json:"target,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	IP        string    `json:"ip,omitempty"`
}

func (a *Audit) clone() *Audit {
	if a == nil {
		return nil
	}
	c := *a
	return &c
}

// Counts 各类数据的数量，供管理后台总览使用。
type Counts struct {
	Users         int `json:"users"`
	Admins        int `json:"admins"`
	DisabledUsers int `json:"disabled_users"`
	Sessions      int `json:"sessions"`
	Folders       int `json:"folders"`
	Favorites     int `json:"favorites"`
	History       int `json:"history"`
	Notes         int `json:"notes"`
}

// Data 是存储层的完整快照。JSON 实现直接序列化它。
type Data struct {
	Version   int               `json:"version"`
	Users     []*User           `json:"users"`
	Sessions  []*Session        `json:"sessions"`
	Folders   []*Folder         `json:"folders"`
	Favorites []*Favorite       `json:"favorites"`
	History   []*History        `json:"history"`
	Notes     []*Note           `json:"notes"`
	Audit     []*Audit          `json:"audit"`
	Settings  map[string]string `json:"settings,omitempty"`
}

// ---- 存储接口 ----------------------------------------------------------------

// Store 是账号数据的持久化接口。
//
// 业务层只依赖这个接口：把 JSON 文件换成 SQLite 或远端数据库时，
// 只需提供新的实现，service.go 与 HTTP 层不需要改动。
//
// 约定：
//   - 未找到的记录返回 ErrNotFound（而不是 nil, nil）。
//   - 所有返回的实体都是副本，调用方可以自由修改，不会影响存储内部状态。
//   - DeleteUser 会级联删除该用户的会话、收藏夹、收藏、历史与笔记。
type Store interface {
	// ---- 用户 ----
	CreateUser(u *User) error
	UpdateUser(u *User) error
	DeleteUser(id string) error
	UserByID(id string) (*User, error)
	UserByUsername(username string) (*User, error)
	ListUsers() ([]*User, error)
	CountUsers() (int, error)

	// ---- 会话 ----
	CreateSession(s *Session) error
	SessionByTokenHash(hash string) (*Session, error)
	DeleteSession(tokenHash string) error
	DeleteUserSessions(userID string) error
	PurgeExpiredSessions(now time.Time) (int, error)

	// ---- 收藏夹 ----
	CreateFolder(f *Folder) error
	RenameFolder(userID, id, name string) error
	DeleteFolder(userID, id string) error
	ListFolders(userID string) ([]*Folder, error)

	// ---- 收藏 ----
	PutFavorite(f *Favorite) error
	DeleteFavorite(userID, source, comicID string) error
	FavoriteOf(userID, source, comicID string) (*Favorite, error)
	ListFavorites(userID, folderID string, offset, limit int) ([]*Favorite, int, error)

	// ---- 阅读历史 ----
	PutHistory(h *History) error
	DeleteHistory(userID, source, comicID string) error
	ClearHistory(userID string) error
	ListHistory(userID string, offset, limit int) ([]*History, int, error)

	// ---- 阅读笔记 ----
	CreateNote(n *Note) error
	UpdateNote(n *Note) error
	DeleteNote(userID, id string) error
	ListNotes(userID, source, comicID string, offset, limit int) ([]*Note, int, error)

	// ---- 审计 ----
	AppendAudit(a *Audit) error
	ListAudit(offset, limit int) ([]*Audit, int, error)

	// ---- 设置（键值对，跨重启保留） ----
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
	AllSettings() (map[string]string, error)

	// ---- 统计 ----
	Counts(now time.Time) (Counts, error)

	// Close 释放底层资源。
	Close() error
}

// ---- 错误 --------------------------------------------------------------------

var (
	// ErrNotFound 目标记录不存在。
	ErrNotFound = errors.New("记录不存在")
	// ErrConflict 唯一性冲突（用户名已占用等）。
	ErrConflict = errors.New("记录已存在")
	// ErrUnauthenticated 未登录或会话已失效。
	ErrUnauthenticated = errors.New("未登录或会话已失效")
	// ErrForbidden 已登录但权限不足。
	ErrForbidden = errors.New("权限不足")
	// ErrDisabled 账号已被禁用。
	ErrDisabled = errors.New("账号已被禁用")
	// ErrInvalid 入参不合法。
	ErrInvalid = errors.New("参数不合法")
)

// ---- 工具 --------------------------------------------------------------------

// NewID 生成带前缀的随机 id（前缀 + 8 字节十六进制）。
func NewID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败在正常系统上不可能发生；退回时间戳以保证可用性。
		return prefix + hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return prefix + hex.EncodeToString(b[:])
}

// NormalizeUsername 归一化用户名，用于大小写不敏感的唯一性比较。
func NormalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}
