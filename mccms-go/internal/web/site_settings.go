package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/mccms/mccms-go/internal/mc"
)

// 站点自定义设置的键（存于账号库的 settings 表）。
const (
	settingSiteName         = "site.name"
	settingSiteAbout        = "site.about"
	settingSiteAnnouncement = "site.announcement"

	defaultSiteName = "mccms"
)

// defaultAbout 关于页默认内容（管理员可在后台覆盖）。
const defaultAbout = `这是一个自托管的多站点漫画阅读器。

内容来自多个公开站点，本项目只做聚合与阅读，不存储任何作品文件。

如需支持本站运营，可在「会员」页通过爱发电赞助，赞助后可解锁完整内容。`

// SiteAnnouncement 站点公告。
type SiteAnnouncement struct {
	Enabled bool   `json:"enabled"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// writeErr 统一的失败响应（沿用 {st, msg} 信封；成功码为 stOK）。
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"st": 1, "msg": msg})
}

// getSetting 读取设置，缺省或空值时返回 def。
func (s *Server) getSetting(key, def string) string {
	svc := s.accountService()
	if svc == nil {
		return def
	}
	v, err := svc.Store().GetSetting(key)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// putSetting 写入设置。
func (s *Server) putSetting(key, value string) error {
	svc := s.accountService()
	if svc == nil {
		return fmt.Errorf("账号体系未启用")
	}
	return svc.Store().SetSetting(key, value)
}

// siteAnnouncement 解析公告设置。
func (s *Server) siteAnnouncement() SiteAnnouncement {
	var a SiteAnnouncement
	raw := s.getSetting(settingSiteAnnouncement, "")
	if raw == "" {
		return a
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return SiteAnnouncement{}
	}
	return a
}

// siteInfo 当前站点信息（名称/关于/公告）。
func (s *Server) siteInfo() map[string]any {
	return map[string]any{
		"name":         s.getSetting(settingSiteName, defaultSiteName),
		"about":        s.getSetting(settingSiteAbout, defaultAbout),
		"announcement": s.siteAnnouncement(),
	}
}

// handlePublicSite 公开的站点信息，供前端全站展示（名称/关于/公告）。
func (s *Server) handlePublicSite(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": s.siteInfo()})
}

// handlePublicAnnouncement 公告（兼容旧端点 /api/announcement）。
func (s *Server) handlePublicAnnouncement(w http.ResponseWriter, r *http.Request) {
	a := s.siteAnnouncement()
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{
		"enabled": a.Enabled,
		"title":   a.Title,
		"content": a.Content,
	}})
}

// handleAdminSite 站点设置读写（仅管理员）。
func (s *Server) handleAdminSite(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": s.siteInfo()})
	case http.MethodPost, http.MethodPut:
		var body struct {
			Name         *string           `json:"name"`
			About        *string           `json:"about"`
			Announcement *SiteAnnouncement `json:"announcement"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不正确")
			return
		}
		if body.Name != nil {
			name := strings.TrimSpace(*body.Name)
			if name == "" {
				writeErr(w, http.StatusBadRequest, "站点名称不能为空")
				return
			}
			if len([]rune(name)) > 40 {
				writeErr(w, http.StatusBadRequest, "站点名称过长（至多 40 字）")
				return
			}
			if err := s.putSetting(settingSiteName, name); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if body.About != nil {
			about := strings.TrimSpace(*body.About)
			if len([]rune(about)) > 20000 {
				writeErr(w, http.StatusBadRequest, "关于内容过长")
				return
			}
			if err := s.putSetting(settingSiteAbout, about); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if body.Announcement != nil {
			a := *body.Announcement
			a.Title = strings.TrimSpace(a.Title)
			a.Content = strings.TrimSpace(a.Content)
			if len([]rune(a.Title)) > 120 || len([]rune(a.Content)) > 5000 {
				writeErr(w, http.StatusBadRequest, "公告内容过长")
				return
			}
			raw, _ := json.Marshal(a)
			if err := s.putSetting(settingSiteAnnouncement, string(raw)); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": s.siteInfo()})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

// freeComicItem 一键生成结果条目。
type freeComicItem struct {
	Site  string `json:"site"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Cover string `json:"cover,omitempty"`
}

// handleAdminVipGenerateFree 一键生成免费观看白名单。
// 支持跨多个源挑选，但白名单总数固定为 count（整体替换，不做追加）。
func (s *Server) handleAdminVipGenerateFree(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	var body struct {
		Count int      `json:"count"`
		Sites []string `json:"sites"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	if body.Count <= 0 {
		body.Count = 10
	}
	if body.Count > 300 {
		body.Count = 300
	}
	sites := body.Sites
	if len(sites) == 0 {
		sites = mc.AllSites
	}

	pools := map[string][]freeComicItem{}
	order := []string{}
	for _, site := range sites {
		site = strings.TrimSpace(site)
		if site == "" || !mc.KnownSite(site) {
			continue
		}
		cl, err := s.clientOf(site)
		if err != nil || cl == nil {
			continue
		}
		page, err := cl.UpdateList(1)
		if err != nil || page == nil || len(page.Items) == 0 {
			continue
		}
		items := make([]freeComicItem, 0, len(page.Items))
		for _, it := range page.Items {
			if strings.TrimSpace(it.ID) == "" {
				continue
			}
			items = append(items, freeComicItem{Site: site, ID: it.ID, Name: it.Name, Cover: it.Cover})
		}
		if len(items) > 0 {
			pools[site] = items
			order = append(order, site)
		}
	}
	if len(order) == 0 {
		writeErr(w, http.StatusBadGateway, "未能从任何站点获取内容，请稍后重试")
		return
	}

	// 跨源轮询挑选，保证总量恰好等于 count 且尽量分散到多个源。
	picked := make([]freeComicItem, 0, body.Count)
	seen := map[string]bool{}
	for len(picked) < body.Count {
		progressed := false
		for _, site := range order {
			if len(picked) >= body.Count {
				break
			}
			pool := pools[site]
			for len(pool) > 0 {
				it := pool[0]
				pool = pool[1:]
				key := it.Site + ":" + it.ID
				if seen[key] {
					continue
				}
				seen[key] = true
				picked = append(picked, it)
				progressed = true
				break
			}
			pools[site] = pool
		}
		if !progressed {
			break
		}
	}
	if len(picked) == 0 {
		writeErr(w, http.StatusBadGateway, "未能挑选出可用内容")
		return
	}

	ids := make([]string, 0, len(picked))
	for _, it := range picked {
		ids = append(ids, it.Site+":"+it.ID)
	}
	sort.Strings(ids)
	if err := s.putSetting(settingVipFreeComics, strings.Join(ids, ",")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"st": stOK, "data": map[string]any{
		"count":       len(ids),
		"items":       picked,
		"free_comics": ids,
	}})
}
