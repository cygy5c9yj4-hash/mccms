package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mccms/mccms-go/internal/account"
	"github.com/mccms/mccms-go/internal/mc"
)

// 账号体系相关的业务状态码。
//
// stNotLogin(1014) 沿用前端既有约定：前端收到它会派发「未登录」事件并清理本地状态。
const (
	stForbidden = 1003 // 已登录但权限不足
)

// sessionCookie 是会话 cookie 名。
const sessionCookie = "mccms_session"

// ---- 会话 cookie -------------------------------------------------------------

// setSessionCookie 下发会话 cookie。
//
// 属性说明：
//   - HttpOnly：JS 无法读取，降低 XSS 窃取会话的风险。
//   - SameSite=Lax：阻止跨站 POST 携带 cookie（CSRF 第一道防线）。
//   - Secure：仅在 HTTPS 下设置，避免本地 http 调试时 cookie 被丢弃。
//   - Path=/：整个站点共享。
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

// clearSessionCookie 让浏览器丢弃会话 cookie。
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// sessionToken 从 cookie 中取出会话 token。
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// clientIP 尽力还原客户端地址，仅用于审计与展示。
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.Index(v, ","); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- 认证辅助 ----------------------------------------------------------------

// accountService 返回账号服务；未启用账号体系时返回 nil。
func (s *Server) accountService() *account.Service { return s.accounts }

// currentUser 解析当前请求的登录用户。未登录返回 (nil, nil)。
func (s *Server) currentUser(r *http.Request) (*account.User, error) {
	if s.accounts == nil {
		return nil, nil
	}
	token := sessionToken(r)
	if token == "" {
		return nil, nil
	}
	u, _, err := s.accounts.Authenticate(token)
	if err != nil {
		if errors.Is(err, account.ErrUnauthenticated) || errors.Is(err, account.ErrDisabled) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// requireUser 要求已登录；未登录时写入 1014 响应并返回 false。
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*account.User, bool) {
	if s.accounts == nil {
		writeJSON(w, 200, map[string]any{"st": stError, "msg": "本服务未启用账号体系"})
		return nil, false
	}
	u, err := s.currentUser(r)
	if err != nil {
		fail(w, err)
		return nil, false
	}
	if u == nil {
		writeJSON(w, 200, map[string]any{"st": stNotLogin, "msg": "请先登录"})
		return nil, false
	}
	return u, true
}

// requireAdmin 要求管理员身份。
//
// 未登录 -> 200 + stNotLogin（前端据此跳登录）；
// 已登录但非管理员 -> 403 + stForbidden。
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (*account.User, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return nil, false
	}
	if !u.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]any{"st": stForbidden, "msg": "需要管理员权限"})
		return nil, false
	}
	return u, true
}

// failAccount 把账号包的语义错误映射为响应。
func failAccount(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		ok(w, map[string]any{})
	case errors.Is(err, account.ErrUnauthenticated):
		writeJSON(w, 200, map[string]any{"st": stNotLogin, "msg": "请先登录"})
	case errors.Is(err, account.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]any{"st": stForbidden, "msg": err.Error()})
	case errors.Is(err, account.ErrDisabled):
		writeJSON(w, 200, map[string]any{"st": stNotLogin, "msg": "账号已被禁用"})
	case errors.Is(err, account.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"st": stError, "msg": "记录不存在"})
	case errors.Is(err, account.ErrConflict), errors.Is(err, account.ErrInvalid),
		errors.Is(err, account.ErrBadPassword):
		writeJSON(w, 200, map[string]any{"st": stError, "msg": err.Error()})
	default:
		fail(w, err)
	}
}

// decodeBody 把请求体解析到 dst。空体视为零值，便于无参数端点复用。
func decodeBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%w: 请求体不是合法 JSON", account.ErrInvalid)
	}
	return nil
}

// ---- 认证端点 ----------------------------------------------------------------

// handleAuthRegister 注册新账号。首个注册者自动成为管理员。
func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if s.accounts == nil {
		writeJSON(w, 200, map[string]any{"st": stError, "msg": "本服务未启用账号体系"})
		return
	}

	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil {
		failAccount(w, err)
		return
	}

	u, token, err := s.accounts.Register(
		req.Username, req.Password, req.DisplayName, req.Email,
		r.UserAgent(), clientIP(r),
	)
	if err != nil {
		failAccount(w, err)
		return
	}
	setSessionCookie(w, r, token, s.accounts.SessionTTL())
	ok(w, map[string]any{"user": u.Public(time.Now().UTC())})
}

// handleAuthLogin 账号口令登录。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if s.accounts == nil {
		writeJSON(w, 200, map[string]any{"st": stError, "msg": "本服务未启用账号体系"})
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		failAccount(w, err)
		return
	}

	u, token, err := s.accounts.Login(req.Username, req.Password, r.UserAgent(), clientIP(r))
	if err != nil {
		if errors.Is(err, account.ErrUnauthenticated) {
			writeJSON(w, 200, map[string]any{"st": stError, "msg": "用户名或密码错误"})
			return
		}
		failAccount(w, err)
		return
	}
	setSessionCookie(w, r, token, s.accounts.SessionTTL())
	ok(w, map[string]any{"user": u.Public(time.Now().UTC())})
}

// handleAuthLogout 退出登录并吊销当前会话。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if s.accounts == nil {
		clearSessionCookie(w, r)
		ok(w, map[string]any{"logged_out": true})
		return
	}
	token := sessionToken(r)
	if u, _ := s.currentUser(r); u != nil {
		_ = s.accounts.LogAudit(u, "user.logout", u.ID, u.Username, "", clientIP(r))
	}
	if err := s.accounts.Logout(token); err != nil {
		failAccount(w, err)
		return
	}
	clearSessionCookie(w, r)
	ok(w, map[string]any{"logged_out": true})
}

// handleAuthMe 返回当前登录态，以及注册开关等前端需要的引导信息。
//
// 未登录时同样返回 200 + stNotLogin，让前端可以在不报错的条件下渲染登录页。
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		writeJSON(w, 200, map[string]any{"st": stError, "msg": "本服务未启用账号体系"})
		return
	}
	// 是否还有用户：用于前端判断「首个注册者将成为管理员」。
	userCount, _ := s.accounts.Store().CountUsers()

	u, err := s.currentUser(r)
	if err != nil {
		fail(w, err)
		return
	}
	if u == nil {
		writeJSON(w, 200, map[string]any{
			"st":  stNotLogin,
			"msg": "未登录",
			"data": map[string]any{
				"user":              nil,
				"registration_open": s.accounts.RegistrationOpen(),
				"bootstrap":         userCount == 0,
			},
		})
		return
	}
	ok(w, map[string]any{
		"user":              u.Public(time.Now().UTC()),
		"registration_open": s.accounts.RegistrationOpen(),
		"bootstrap":         false,
	})
}

// handleAuthPassword 修改自己的口令。成功后旧会话全部作废并换发新 cookie。
func (s *Server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeBody(r, &req); err != nil {
		failAccount(w, err)
		return
	}

	token, err := s.accounts.ChangePassword(u, req.OldPassword, req.NewPassword, r.UserAgent(), clientIP(r))
	if err != nil {
		failAccount(w, err)
		return
	}
	setSessionCookie(w, r, token, s.accounts.SessionTTL())
	ok(w, map[string]any{"changed": true})
}

// ---- 「我的」端点：收藏 / 历史 / 笔记 -----------------------------------------

// handleMeFavorites 收藏列表与收藏动作。
//
//	GET    /api/me/favorites?folder_id=&page=&page_size=
//	POST   /api/me/favorites            新增/更新收藏
//	DELETE /api/me/favorites?source=&comic_id=
func (s *Server) handleMeFavorites(w http.ResponseWriter, r *http.Request) {
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listFavorites(w, r, u)
	case http.MethodPost:
		s.addFavorite(w, r, u)
	case http.MethodDelete:
		q := r.URL.Query()
		if err := s.accounts.RemoveFavorite(u.ID, q.Get("source"), q.Get("comic_id")); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"is_favorite": false})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost, http.MethodDelete)
	}
}

func (s *Server) listFavorites(w http.ResponseWriter, r *http.Request, u *account.User) {
	q := r.URL.Query()
	pageNo := queryInt(r, "page", 1)
	pageSize := queryInt(r, "page_size", account.DefaultPageSize)
	folderID := q.Get("folder_id")

	items, total, err := s.accounts.ListFavorites(u.ID, folderID, pageNo, pageSize)
	if err != nil {
		failAccount(w, err)
		return
	}
	folders, err := s.accounts.ListFolders(u.ID)
	if err != nil {
		failAccount(w, err)
		return
	}
	counts, err := s.accounts.FavoriteCountByFolder(u.ID)
	if err != nil {
		failAccount(w, err)
		return
	}

	ok(w, map[string]any{
		"list":      items,
		"total":     total,
		"page":      pageNo,
		"page_size": pageSize,
		"folders":   folders,
		"counts":    counts,
	})
}

func (s *Server) addFavorite(w http.ResponseWriter, r *http.Request, u *account.User) {
	var req struct {
		Source   string   `json:"source"`
		ComicID  string   `json:"comic_id"`
		Title    string   `json:"title"`
		Author   string   `json:"author"`
		CoverURL string   `json:"cover_url"`
		FolderID string   `json:"folder_id"`
		Kind     string   `json:"kind"`
		Tags     []string `json:"tags"`
	}
	if err := decodeBody(r, &req); err != nil {
		failAccount(w, err)
		return
	}
	// 站点未显式提供时，回落到请求上下文里的当前站点。
	if strings.TrimSpace(req.Source) == "" {
		req.Source = s.siteOf(r)
	}

	fav := &account.Favorite{
		UserID:   u.ID,
		FolderID: req.FolderID,
		Source:   req.Source,
		ComicID:  req.ComicID,
		Title:    req.Title,
		Author:   req.Author,
		CoverURL: req.CoverURL,
		Kind:     req.Kind,
		Tags:     req.Tags,
	}
	if err := s.accounts.AddFavorite(fav); err != nil {
		failAccount(w, err)
		return
	}
	ok(w, map[string]any{"is_favorite": true})
}

// handleMeFavoriteCheck 查询单部作品是否已收藏。
func (s *Server) handleMeFavoriteCheck(w http.ResponseWriter, r *http.Request) {
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}
	q := r.URL.Query()
	ok(w, map[string]any{
		"is_favorite": s.accounts.IsFavorite(u.ID, q.Get("source"), q.Get("comic_id")),
	})
}

// handleMeFavoriteFolders 收藏夹分组的增删改查。
//
//	GET    /api/me/favorite-folders
//	POST   /api/me/favorite-folders            {name}
//	PUT    /api/me/favorite-folders            {id, name}
//	DELETE /api/me/favorite-folders?id=
func (s *Server) handleMeFavoriteFolders(w http.ResponseWriter, r *http.Request) {
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}

	switch r.Method {
	case http.MethodGet:
		folders, err := s.accounts.ListFolders(u.ID)
		if err != nil {
			failAccount(w, err)
			return
		}
		counts, err := s.accounts.FavoriteCountByFolder(u.ID)
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"folders": folders, "counts": counts})

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		f, err := s.accounts.CreateFolder(u.ID, req.Name)
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"folder": f})

	case http.MethodPut:
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if err := s.accounts.RenameFolder(u.ID, req.ID, req.Name); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"renamed": true})

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if err := s.accounts.DeleteFolder(u.ID, id); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"deleted": true})

	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete)
	}
}

// handleMeHistory 阅读历史。
//
//	GET    /api/me/history?page=&page_size=
//	POST   /api/me/history                 上报阅读进度
//	DELETE /api/me/history?source=&comic_id=   删除单条
//	DELETE /api/me/history?all=1               清空
func (s *Server) handleMeHistory(w http.ResponseWriter, r *http.Request) {
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, total, err := s.accounts.ListHistory(u.ID, queryInt(r, "page", 1), queryInt(r, "page_size", account.DefaultPageSize))
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"list": items, "total": total})

	case http.MethodPost:
		var req struct {
			Source       string `json:"source"`
			ComicID      string `json:"comic_id"`
			Title        string `json:"title"`
			CoverURL     string `json:"cover_url"`
			Kind         string `json:"kind"`
			ChapterID    string `json:"chapter_id"`
			ChapterTitle string `json:"chapter_title"`
			Page         int    `json:"page"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if strings.TrimSpace(req.Source) == "" {
			req.Source = s.siteOf(r)
		}
		err := s.accounts.RecordHistory(&account.History{
			UserID:       u.ID,
			Source:       req.Source,
			ComicID:      req.ComicID,
			Title:        req.Title,
			CoverURL:     req.CoverURL,
			Kind:         req.Kind,
			ChapterID:    req.ChapterID,
			ChapterTitle: req.ChapterTitle,
			Page:         req.Page,
		})
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"recorded": true})

	case http.MethodDelete:
		q := r.URL.Query()
		if q.Get("all") != "" {
			if err := s.accounts.ClearHistory(u.ID); err != nil {
				failAccount(w, err)
				return
			}
			ok(w, map[string]any{"cleared": true})
			return
		}
		if err := s.accounts.DeleteHistory(u.ID, q.Get("source"), q.Get("comic_id")); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"deleted": true})

	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost, http.MethodDelete)
	}
}

// handleMeNotes 阅读笔记。
//
//	GET    /api/me/notes?source=&comic_id=&page=
//	POST   /api/me/notes                 新建
//	PUT    /api/me/notes                 编辑 {id, body}
//	DELETE /api/me/notes?id=             删除
func (s *Server) handleMeNotes(w http.ResponseWriter, r *http.Request) {
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}

	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		items, total, err := s.accounts.ListNotes(
			u.ID, q.Get("source"), q.Get("comic_id"),
			queryInt(r, "page", 1), queryInt(r, "page_size", account.DefaultPageSize),
		)
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"list": items, "total": total})

	case http.MethodPost:
		var req struct {
			Source     string `json:"source"`
			ComicID    string `json:"comic_id"`
			ComicTitle string `json:"comic_title"`
			Kind       string `json:"kind"`
			Body       string `json:"body"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if strings.TrimSpace(req.Source) == "" {
			req.Source = s.siteOf(r)
		}
		n, err := s.accounts.CreateNote(&account.Note{
			UserID:     u.ID,
			Source:     req.Source,
			ComicID:    req.ComicID,
			ComicTitle: req.ComicTitle,
			Kind:       req.Kind,
			Body:       req.Body,
		})
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"note": n})

	case http.MethodPut:
		var req struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		n, err := s.accounts.UpdateNote(u.ID, req.ID, req.Body)
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"note": n})

	case http.MethodDelete:
		if err := s.accounts.DeleteNote(u.ID, r.URL.Query().Get("id")); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"deleted": true})

	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete)
	}
}

// ---- 管理后台端点 ------------------------------------------------------------

// handleAdminOverview 仪表盘总览：数据统计 + 服务运行信息。
func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	if _, okk := s.requireAdmin(w, r); !okk {
		return
	}
	counts, err := s.accounts.Counts()
	if err != nil {
		failAccount(w, err)
		return
	}
	settings, err := s.accounts.Settings()
	if err != nil {
		failAccount(w, err)
		return
	}
	ok(w, map[string]any{
		"counts":   counts,
		"settings": settings,
		"runtime": map[string]any{
			"version":          "mccms-go 1.0.0",
			"default_site":     s.cfg.Site,
			"site_names":       mc.SiteNames,
			"download_dir":     s.cfg.DownloadDir,
			"accounts_path":    s.cfg.AccountsPath,
			"image_threads":    s.cfg.ImageThreads,
			"chapter_threads":  s.cfg.ChapterThr,
			"session_ttl_days": int(s.accounts.SessionTTL().Hours() / 24),
		},
	})
}

// handleAdminUsers 用户列表。
//
//	GET    /api/admin/users
//	DELETE /api/admin/users?user_id=
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	actor, okk := s.requireAdmin(w, r)
	if !okk {
		return
	}

	switch r.Method {
	case http.MethodGet:
		list, err := s.accounts.ListUsers()
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"list": list, "total": len(list)})

	case http.MethodDelete:
		target := r.URL.Query().Get("user_id")
		if err := s.accounts.AdminDeleteUser(actor, target, clientIP(r)); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"deleted": true})

	default:
		methodNotAllowed(w, http.MethodGet, http.MethodDelete)
	}
}

// handleAdminUserAction 对单个用户执行管理操作（角色 / 状态 / 权益 / 口令 / 强制下线）。
func (s *Server) handleAdminUserAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	actor, okk := s.requireAdmin(w, r)
	if !okk {
		return
	}

	action := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	ip := clientIP(r)

	switch action {
	case "role":
		var req struct {
			UserID string       `json:"user_id"`
			Role   account.Role `json:"role"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if err := s.accounts.AdminSetRole(actor, req.UserID, req.Role, ip); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"updated": true})

	case "status":
		var req struct {
			UserID string         `json:"user_id"`
			Status account.Status `json:"status"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if err := s.accounts.AdminSetStatus(actor, req.UserID, req.Status, ip); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"updated": true})

	case "tier":
		var req struct {
			UserID    string       `json:"user_id"`
			Tier      account.Tier `json:"tier"`
			ExpiresAt *time.Time   `json:"expires_at"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if err := s.accounts.AdminSetTier(actor, req.UserID, req.Tier, req.ExpiresAt, ip); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"updated": true})

	case "password":
		var req struct {
			UserID   string `json:"user_id"`
			Password string `json:"password"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if err := s.accounts.AdminResetPassword(actor, req.UserID, req.Password, ip); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"updated": true})

	case "revoke-sessions":
		var req struct {
			UserID string `json:"user_id"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if err := s.accounts.AdminRevokeSessions(actor, req.UserID, ip); err != nil {
			failAccount(w, err)
			return
		}
		ok(w, map[string]any{"revoked": true})

	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"st": stError, "msg": "未知的管理操作"})
	}
}

// handleAdminAudit 审计日志。
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if _, okk := s.requireAdmin(w, r); !okk {
		return
	}
	items, total, err := s.accounts.Audit(queryInt(r, "page", 1), queryInt(r, "page_size", account.DefaultPageSize))
	if err != nil {
		failAccount(w, err)
		return
	}
	ok(w, map[string]any{"list": items, "total": total})
}

// handleAdminSettings 读取 / 更新全局设置（当前只有注册开关）。
func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	actor, okk := s.requireAdmin(w, r)
	if !okk {
		return
	}

	switch r.Method {
	case http.MethodGet:
		settings, err := s.accounts.Settings()
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, settings)

	case http.MethodPost:
		var req struct {
			RegistrationOpen *bool `json:"registration_open"`
		}
		if err := decodeBody(r, &req); err != nil {
			failAccount(w, err)
			return
		}
		if req.RegistrationOpen != nil {
			if err := s.accounts.SetRegistrationOpen(*req.RegistrationOpen, actor, clientIP(r)); err != nil {
				failAccount(w, err)
				return
			}
		}
		settings, err := s.accounts.Settings()
		if err != nil {
			failAccount(w, err)
			return
		}
		ok(w, settings)

	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

// ---- 方法校验 -----------------------------------------------------------------

// methodNotAllowed 统一 405 响应。
func methodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": stError, "msg": "请求方法不被支持"})
}

// handleMeAfdian 返回当前账号的爱发电绑定状态。
func (s *Server) handleMeAfdian(w http.ResponseWriter, r *http.Request) {
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}
	ok(w, map[string]any{
		"bound":     u.AfdianUserID != "",
		"masked_id": maskAfdianUID(u.AfdianUserID),
		"bound_at":  u.AfdianBoundAt,
	})
}

// handleMeAfdianUnbind 解除当前账号的爱发电绑定。
// 解绑后该爱发电账号的新订单不再自动归户，已发放的权益不受影响。
func (s *Server) handleMeAfdianUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"st": stError, "msg": "方法不支持"})
		return
	}
	u, okk := s.requireUser(w, r)
	if !okk {
		return
	}
	if err := s.accounts.UnbindAfdian(u.ID); err != nil {
		failAccount(w, err)
		return
	}
	_ = s.accounts.LogAudit(u, "afdian.unbind", u.ID, u.Username, "解除爱发电绑定", clientIP(r))
	ok(w, map[string]any{"bound": false})
}
