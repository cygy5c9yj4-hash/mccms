// Package web 提供 HTTP 服务：JM-Aura 兼容 API + 内嵌前端。
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mccms/mccms-go/internal/account"
	"github.com/mccms/mccms-go/internal/client"
	"github.com/mccms/mccms-go/internal/download"
	"github.com/mccms/mccms-go/internal/mc"
)

// 业务状态码（对齐 JM-Aura 前端的约定）。
const (
	stOK       = 1001 // 成功
	stError    = 1002 // 通用失败
	stNotLogin = 1014 // 需要登录
)

// Config 服务配置。
type Config struct {
	Addr         string
	Site         string
	DownloadDir  string
	Resolve      map[string]string
	Proxies      string
	Cookies      map[string]string
	Username     string
	Password     string
	ImageThreads int
	ChapterThr   int

	// AccountsPath 账号库（JSON）路径。为空则使用 DownloadDir 同级的 accounts.json。
	AccountsPath string
	// DisableAccounts 关闭自有账号体系（仅保留站点透传能力）。
	DisableAccounts bool
	// SessionTTLDays 会话有效期（天）。<=0 时用 account.DefaultSessionTTL。
	SessionTTLDays int
}

// Server HTTP 服务。
type Server struct {
	cfg     Config
	manager *download.Manager

	// accounts 自有账号体系；可为 nil（未启用时相关端点返回明确提示）。
	accounts *account.Service

	mu           sync.Mutex
	clients      map[string]client.Client // site -> client
	options      map[string]download.Options
	chapterCache *chapterImageCache

	// online 在内存里统计在线人数（心跳式，进程重启后清零）。
	online *onlineTracker
}

// New 创建服务，并初始化自有账号体系。
//
// 返回错误的情形只有一种：账号库无法打开（路径不可写、文件损坏等）。
// 这类问题会让「收藏 / 历史 / 笔记 / 管理后台」全部不可用，因此不静默降级，
// 而是让调用方决定如何处理（main 会据此终止启动）。
func New(cfg Config) (*Server, error) {
	if cfg.Site == "" {
		cfg.Site = mc.SiteTibiu
	}
	if cfg.DownloadDir == "" {
		wd, _ := os.Getwd()
		cfg.DownloadDir = filepath.Join(wd, "downloads")
	}
	if cfg.ImageThreads <= 0 {
		cfg.ImageThreads = 16
	}
	if cfg.ChapterThr <= 0 {
		cfg.ChapterThr = 4
	}
	if cfg.AccountsPath == "" {
		cfg.AccountsPath = filepath.Join(filepath.Dir(cfg.DownloadDir), "accounts.json")
	}

	srv := &Server{
		cfg:          cfg,
		manager:      download.NewManager(),
		clients:      map[string]client.Client{},
		options:      map[string]download.Options{},
		chapterCache: newChapterImageCache(),
		online:       newOnlineTracker(),
	}

	if !cfg.DisableAccounts {
		store, err := account.NewJSONStore(cfg.AccountsPath)
		if err != nil {
			return nil, err
		}
		svc, err := account.New(account.Options{
			Store:      store,
			SessionTTL: time.Duration(cfg.SessionTTLDays) * 24 * time.Hour,
		})
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		if err := svc.Bootstrap(); err != nil {
			_ = store.Close()
			return nil, err
		}
		srv.accounts = svc
	}

	return srv, nil
}

// Close 释放服务持有的资源（主要是账号库落盘）。
func (s *Server) Close() error {
	if s.accounts == nil {
		return nil
	}
	return s.accounts.Store().Close()
}

// Accounts 暴露账号服务，供测试与外部集成使用；未启用时为 nil。
func (s *Server) Accounts() *account.Service { return s.accounts }

// Manager 暴露任务管理器。
func (s *Server) Manager() *download.Manager { return s.manager }

// Handler 组装路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// ---- 自有账号体系（收藏 / 历史 / 笔记 / 管理后台）----
	mux.HandleFunc("/api/auth/register", s.handleAuthRegister)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/auth/me", s.handleAuthMe)
	mux.HandleFunc("/api/auth/password", s.handleAuthPassword)

	mux.HandleFunc("/api/me/favorites", s.handleMeFavorites)
	mux.HandleFunc("/api/me/favorite-check", s.handleMeFavoriteCheck)
	mux.HandleFunc("/api/me/favorite-folders", s.handleMeFavoriteFolders)
	mux.HandleFunc("/api/me/history", s.handleMeHistory)
	mux.HandleFunc("/api/me/notes", s.handleMeNotes)
	mux.HandleFunc("/api/me/afdian", s.handleMeAfdian)
	mux.HandleFunc("/api/me/afdian/unbind", s.handleMeAfdianUnbind)

	mux.HandleFunc("/api/admin/overview", s.handleAdminOverview)
	mux.HandleFunc("/api/admin/users", s.handleAdminUsers)
	mux.HandleFunc("/api/admin/users/", s.handleAdminUserAction)
	mux.HandleFunc("/api/admin/audit", s.handleAdminAudit)
	mux.HandleFunc("/api/admin/settings", s.handleAdminSettings)
	mux.HandleFunc("/api/admin/vip/codes", s.handleAdminVipCodes)
	mux.HandleFunc("/api/admin/vip/codes/", s.handleAdminVipCodeAction)
	mux.HandleFunc("/api/admin/vip/orders", s.handleAdminVipOrders)
	mux.HandleFunc("/api/admin/vip/orders/", s.handleAdminVipOrderAction)
	mux.HandleFunc("/api/admin/vip/grant", s.handleAdminVipGrant)
	mux.HandleFunc("/api/admin/vip/settings", s.handleAdminVipSettings)
	mux.HandleFunc("/api/admin/vip/sync", s.handleAdminVipSync)
	mux.HandleFunc("/api/admin/vip/ping", s.handleAdminVipPing)
	mux.HandleFunc("/api/admin/site", s.handleAdminSite)
	mux.HandleFunc("/api/admin/vip/free-comics/generate", s.handleAdminVipGenerateFree)

	// ---- 站点信息（本库自有）----
	mux.HandleFunc("/api/sites", s.handleSites)
	mux.HandleFunc("/api/site", s.handlePublicSite)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		anon := make([]string, 0, len(mc.AllSites))
		for _, site := range mc.AllSites {
			anon = append(anon, anonSourceKey(site))
		}
		writeJSON(w, 200, map[string]any{"ok": true, "sites": anon})
	})

	// ---- JM-Aura 兼容层 ----
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/site/me", s.handleSiteMe)
	mux.HandleFunc("/api/site/login", s.handleSiteLogin)
	mux.HandleFunc("/api/site/logout", s.handleSiteLogout)
	mux.HandleFunc("/api/announcement", s.handlePublicAnnouncement)
	mux.HandleFunc("/api/online", s.handleOnline)
	mux.HandleFunc("/api/home", s.handleHome)
	mux.HandleFunc("/api/promote", s.handlePromote)
	mux.HandleFunc("/api/latest", s.handleLatest)
	mux.HandleFunc("/api/random", s.handleRandom)
	mux.HandleFunc("/api/afdian/sponsors", s.handleAfdian)
	mux.HandleFunc("/api/vip/status", s.handleVipStatus)
	mux.HandleFunc("/api/vip/redeem", s.handleVipRedeem)
	mux.HandleFunc("/api/vip/claim", s.handleVipClaim)
	mux.HandleFunc("/api/webhook/afdian", s.handleAfdianWebhook)

	mux.HandleFunc("/api/image-proxy", s.handleImageProxy)
	mux.HandleFunc("/api/chapter_image/", s.handleChapterImage)

	mux.HandleFunc("/api/v2/jm/search", s.handleSearch)
	mux.HandleFunc("/api/v2/jm/categories", s.handleCategories)
	mux.HandleFunc("/api/v2/jm/leaderboard", s.handleLeaderboard)
	mux.HandleFunc("/api/v2/jm/random", s.handleRandom)
	mux.HandleFunc("/api/v2/jm/comic/", s.vipGate(s.handleComicPath))
	mux.HandleFunc("/api/v2/jm/chapter/", s.vipGate(s.handleChapterPath))
	mux.HandleFunc("/api/v2/jm/download/tasks", s.handleDownloadTasks)
	mux.HandleFunc("/api/v2/jm/download/tasks/", s.handleDownloadTaskByID)

	// 未实现的功能给出明确的空数据，而不是 404/500，避免前端整页报错
	for _, p := range []string{
		"/api/v2/jm/novels", "/api/v2/jm/novel/search", "/api/v2/jm/novel_favorites",
		"/api/v2/jm/novel/export/tasks", "/api/v2/jm/comments", "/api/v2/jm/favorites",
		"/api/favorites", "/api/favorite_folder", "/api/credentials",
		"/api/recommend", "/api/aura/library/history", "/api/session/relogin",
		"/api/v2/cache/cleanup",
	} {
		mux.HandleFunc(p, s.handleUnsupported)
	}
	for _, p := range []string{"/api/v2/jm/novel/", "/api/v2/jm/novel_chapter/", "/api/v2/jm/comment/"} {
		mux.HandleFunc(p, s.handleUnsupported)
	}

	// ---- 前端静态资源（SPA）----
	mux.HandleFunc("/", s.handleStatic)

	return logMiddleware(adminNoStore(mux))
}

// ---- 客户端缓存 -------------------------------------------------------------

func (s *Server) siteOf(r *http.Request) string {
	site := r.URL.Query().Get("site")
	if site == "" {
		site = r.Header.Get("X-Mccms-Site")
	}
	// 前端只会传匿名来源键（s1..sN），这里解析回真实站点。
	if real := realSourceKey(site); real != "" {
		site = real
	}
	if site == "" {
		site = s.cfg.Site
	}
	if !mc.KnownSite(site) {
		return s.cfg.Site
	}
	return site
}

func (s *Server) clientOf(site string) (client.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.clients[site]; ok {
		return c, nil
	}

	c, err := client.New(client.Options{
		Site:     site,
		Resolve:  s.cfg.Resolve,
		Proxies:  s.cfg.Proxies,
		Cookies:  s.cfg.Cookies,
		Username: s.cfg.Username,
		Password: s.cfg.Password,
	})
	if err != nil {
		return nil, err
	}
	s.clients[site] = c
	return c, nil
}

func (s *Server) optionsOf(site string) download.Options {
	s.mu.Lock()
	defer s.mu.Unlock()

	if o, ok := s.options[site]; ok {
		return o
	}
	dir := filepath.Join(s.cfg.DownloadDir, site)

	// E-Hentai 的一个画廊就是一部作品（只有一个章节），
	// 再套一层「第N话」目录没有意义，直接按作品名放。
	rule := "Bd_Cname_Chindextitle"
	if site == mc.SiteEhentai {
		rule = "Bd_Cname"
	}

	o := download.DefaultOptions()
	o.ImageThreads = s.cfg.ImageThreads
	o.ChapterThreads = s.cfg.ChapterThr
	o.DirRule, _ = mc.NewDirRule(rule, dir, "")
	s.options[site] = o
	return o
}

// ---- 响应工具 ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"st":1002,"msg":"序列化失败"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// ok 返回成功信封。
func ok(w http.ResponseWriter, data any) {
	writeJSON(w, 200, map[string]any{"st": stOK, "msg": "", "data": data})
}

// fail 返回失败信封。
func fail(w http.ResponseWriter, err error) {
	st := stError
	msg := mc.ErrorMessage(err)

	// 权限类错误统一用 1014，前端会弹登录提示而不是报错
	if mc.IsAccessDenied(err) {
		st = stNotLogin
	}
	writeJSON(w, 200, map[string]any{"st": st, "msg": msg})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

func readBody(r *http.Request) map[string]any {
	out := map[string]any{}
	if r.Body == nil {
		return out
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n := mc.Atoi(v)
	if n <= 0 {
		return def
	}
	return n
}

// ---- 静态资源 ---------------------------------------------------------------

// handleStatic 提供内嵌的 React 构建产物，并对未知路径回退到 index.html（SPA 路由）。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	// 目录穿越防护
	if strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}

	if data, err := distFS.ReadFile("dist/" + path); err == nil {
		w.Header().Set("Content-Type", contentTypeOf(path))
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		_, _ = w.Write(data)
		return
	}

	// SPA 回退
	data, err := distFS.ReadFile("dist/index.html")
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"st":  stOK,
			"msg": "前端尚未构建：请在 web-frontend 目录执行 npm install && npm run build",
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func contentTypeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	}
	return "application/octet-stream"
}

var _ = fmt.Sprintf

// adminNoStore 让管理后台相关的响应不被任何中间层或浏览器缓存。
// 管理面板不随站点一起缓存到用户端：/api/admin/* 与 /admin 一律 no-store。
func adminNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/admin/") || p == "/admin" || strings.HasPrefix(p, "/admin/") {
			h := w.Header()
			h.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
			h.Set("Pragma", "no-cache")
			h.Set("Expires", "0")
			h.Set("X-Robots-Tag", "noindex, nofollow")
		}
		next.ServeHTTP(w, r)
	})
}
