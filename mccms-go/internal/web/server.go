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
}

// Server HTTP 服务。
type Server struct {
	cfg     Config
	manager *download.Manager

	mu           sync.Mutex
	clients      map[string]client.Client // site -> client
	options      map[string]download.Options
	chapterCache *chapterImageCache
}

// New 创建服务。
func New(cfg Config) *Server {
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
	return &Server{
		cfg:          cfg,
		manager:      download.NewManager(),
		clients:      map[string]client.Client{},
		options:      map[string]download.Options{},
		chapterCache: newChapterImageCache(),
	}
}

// Manager 暴露任务管理器。
func (s *Server) Manager() *download.Manager { return s.manager }

// Handler 组装路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// ---- 站点信息（本库自有）----
	mux.HandleFunc("/api/sites", s.handleSites)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "sites": mc.AllSites})
	})

	// ---- JM-Aura 兼容层 ----
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/site/me", s.handleSiteMe)
	mux.HandleFunc("/api/site/login", s.handleSiteLogin)
	mux.HandleFunc("/api/site/logout", s.handleSiteLogout)
	mux.HandleFunc("/api/announcement", s.handleAnnouncement)
	mux.HandleFunc("/api/promote", s.handlePromote)
	mux.HandleFunc("/api/latest", s.handleLatest)
	mux.HandleFunc("/api/random", s.handleRandom)
	mux.HandleFunc("/api/afdian/sponsors", s.handleAfdian)

	mux.HandleFunc("/api/image-proxy", s.handleImageProxy)
	mux.HandleFunc("/api/chapter_image/", s.handleChapterImage)

	mux.HandleFunc("/api/v2/jm/search", s.handleSearch)
	mux.HandleFunc("/api/v2/jm/categories", s.handleCategories)
	mux.HandleFunc("/api/v2/jm/leaderboard", s.handleLeaderboard)
	mux.HandleFunc("/api/v2/jm/random", s.handleRandom)
	mux.HandleFunc("/api/v2/jm/comic/", s.handleComicPath)
	mux.HandleFunc("/api/v2/jm/chapter/", s.handleChapterPath)
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

	return logMiddleware(mux)
}

// ---- 客户端缓存 -------------------------------------------------------------

func (s *Server) siteOf(r *http.Request) string {
	site := r.URL.Query().Get("site")
	if site == "" {
		site = r.Header.Get("X-Mccms-Site")
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
