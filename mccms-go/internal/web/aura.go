package web

import (
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/mccms/mccms-go/internal/client"
	"github.com/mccms/mccms-go/internal/decode"
	"github.com/mccms/mccms-go/internal/download"
	"github.com/mccms/mccms-go/internal/mc"
)

// ---- 与前端 types.ts 对齐的响应结构 -----------------------------------------

type comicSummaryDTO struct {
	Source      string   `json:"source"`
	SourceLabel string   `json:"source_label,omitempty"`
	ComicID     string   `json:"comic_id"`
	Title       string   `json:"title"`
	Author      *string  `json:"author"`
	CoverURL    *string  `json:"cover_url"`
	Tags        []string `json:"tags"`
	Category    *string  `json:"category"`
}

type chapterSummaryDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Order int    `json:"order"`
}

type comicDetailDTO struct {
	comicSummaryDTO
	Description *string             `json:"description"`
	IsFavorite  *bool               `json:"is_favorite"`
	Chapters    []chapterSummaryDTO `json:"chapters"`
}

type chapterPageDTO struct {
	Name *string `json:"name"`
	URL  *string `json:"url"`
}

type chapterRawDTO struct {
	PhotoID            string   `json:"photo_id"`
	AlbumID            string   `json:"album_id"`
	ScrambleID         string   `json:"scramble_id"`
	DataOriginalDomain *string  `json:"data_original_domain"`
	Images             []string `json:"images"`
	Title              string   `json:"title"`
	Index              int      `json:"index"`
}

type chapterDetailDTO struct {
	Source    string           `json:"source"`
	ChapterID string           `json:"chapter_id"`
	Title     *string          `json:"title"`
	Images    []chapterPageDTO `json:"images"`
	Raw       chapterRawDTO    `json:"raw"`
}

type categoryDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toSummaryDTO(s mc.ComicSummary) comicSummaryDTO {
	return comicSummaryDTO{
		Source:      anonSourceKey(s.Site),
		SourceLabel: anonSourceLabel(anonSourceKey(s.Site)),
		ComicID:     s.ID,
		Title:       s.Name,
		Author:      strPtr(s.Author),
		CoverURL:    strPtr(s.Cover),
		Tags:        nonNilTags(s.Tags),
	}
}

func nonNilTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

func comicToDetail(c *mc.Comic) comicDetailDTO {
	chapters := make([]chapterSummaryDTO, 0, len(c.Chapters))
	for _, ch := range c.Chapters {
		chapters = append(chapters, chapterSummaryDTO{ID: ch.ChapterID, Title: ch.Name, Order: ch.Index})
	}
	fav := false
	return comicDetailDTO{
		comicSummaryDTO: comicSummaryDTO{
			Source:      anonSourceKey(c.Site),
			SourceLabel: anonSourceLabel(anonSourceKey(c.Site)),
			ComicID:     c.ComicID,
			Title:       c.Name,
			Author:      strPtr(c.Author()),
			CoverURL:    strPtr(c.Cover),
			Tags:        nonNilTags(c.Tags),
		},
		Description: strPtr(c.Description),
		IsFavorite:  &fav,
		Chapters:    chapters,
	}
}

// chapterToDetail 把章节转成前端期望的结构。
//
// 关键点：`raw.scramble_id` 固定为 "0"。
// 前端 DscImage 以 `scrambleId !== '0'` 作为「需要按 JM 算法在浏览器里解码」的开关；
// 本站点的乱序由后端在下载/代理时还原，若这里给出非 0 值，
// 前端会再叠加一次 JM 的横向切片解码，把图片弄坏。
func chapterToDetail(ch *mc.Chapter, site string) chapterDetailDTO {
	pages := make([]chapterPageDTO, 0, len(ch.ImageURLs))
	names := make([]string, 0, len(ch.ImageURLs))

	for i, u := range ch.ImageURLs {
		name := fileNameOf(u)
		if i < len(ch.ImageIDs) && ch.ImageIDs[i] != "" {
			name = fileNameOf(u)
		}
		names = append(names, name)
		pages = append(pages, chapterPageDTO{Name: &names[len(names)-1], URL: strPtr(u)})
	}

	return chapterDetailDTO{
		Source:    anonSourceKey(site),
		ChapterID: ch.ChapterID,
		Title:     strPtr(ch.Name),
		Images:    pages,
		Raw: chapterRawDTO{
			PhotoID:    ch.ChapterID,
			AlbumID:    ch.ComicID,
			ScrambleID: "0",
			Images:     names,
			Title:      ch.Name,
			Index:      ch.Index,
		},
	}
}

func fileNameOf(u string) string {
	s := u
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// ---- 基础接口 ---------------------------------------------------------------

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	type siteInfo struct {
		Key          string          `json:"key"`
		Name         string          `json:"name"`
		Domains      []string        `json:"domains"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	out := make([]siteInfo, 0, len(mc.AllSites))
	for _, site := range mc.AllSites {
		caps := map[string]bool{}
		if c, err := s.clientOf(site); err == nil {
			_, caps["ranking"] = c.(client.Ranker)
			_, caps["hot"] = c.(client.HotLister)
			caps["categories"] = true
			caps["update"] = true
		}
		key := anonSourceKey(site)
		out = append(out, siteInfo{
			Key:          key,
			Name:         anonSourceLabel(key),
			Domains:      nil,
			Capabilities: caps,
		})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "sites": out})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	sites := make([]string, 0, len(mc.AllSites))
	names := map[string]string{}
	for _, site := range mc.AllSites {
		key := anonSourceKey(site)
		sites = append(sites, key)
		names[key] = anonSourceLabel(key)
	}
	ok(w, map[string]any{
		"site":         anonSourceKey(s.siteOf(r)),
		"sites":        sites,
		"site_names":   names,
		"version":      "mccms-go 1.0.0",
		"download_dir": s.cfg.DownloadDir,
	})
}

func (s *Server) handleSiteMe(w http.ResponseWriter, r *http.Request) {
	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}

	username := ""
	if base, okk := c.(interface {
		UserInfo() (map[string]any, error)
	}); okk {
		if info, ierr := base.UserInfo(); ierr == nil {
			username = mc.ToString(info["nichen"])
			if mc.Atoi(info["log"]) == 0 {
				username = ""
			}
		}
	}
	ok(w, map[string]any{"username": username, "is_admin": false, "site": anonSourceKey(site)})
}

// handleSiteLogin 本后端不使用自有账号体系；站点登录请用 cookies 配置。
func (s *Server) handleSiteLogin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"st":  stError,
		"msg": "本后端不使用自建账号体系。如需访问需要登录/会员的内容，请在启动参数里配置你自己的 cookies。",
	})
}

func (s *Server) handleSiteLogout(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"logged_out": true})
}

func (s *Server) handleAnnouncement(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"enabled": false, "title": "", "content": "", "items": []any{}})
}

func (s *Server) handleAfdian(w http.ResponseWriter, r *http.Request) {
	ok(w, []any{})
}

// handleUnsupported 对未实现的功能返回空数据，避免前端整页报错。
func (s *Server) handleUnsupported(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.Contains(path, "favorite"):
		ok(w, []any{})
	case strings.Contains(path, "history"):
		ok(w, []any{})
	case strings.Contains(path, "comment"):
		ok(w, []any{})
	case strings.Contains(path, "recommend"):
		ok(w, map[string]any{"items": []any{}, "total": 0, "page": 1, "page_size": 20, "has_more": false, "is_moderator": false})
	case strings.Contains(path, "novel"):
		ok(w, []any{})
	case strings.Contains(path, "cache"):
		ok(w, map[string]any{"cleaned": 0})
	}
	ok(w, map[string]any{})
}

// ---- 内容接口 ---------------------------------------------------------------

// categoriesOf 返回该站点支持的分类项，供前端下拉框使用。
func categoriesOf(site string) []categoryDTO {
	base := []categoryDTO{{ID: "0", Title: "全部"}}
	switch site {
	case mc.SiteTibiu:
		return append(base,
			categoryDTO{ID: "hits", Title: "人气"},
			categoryDTO{ID: "addtime", Title: "更新"},
			categoryDTO{ID: "score", Title: "评分"},
		)
	case mc.SiteManhwa:
		return append(base,
			categoryDTO{ID: "hits", Title: "人气"},
			categoryDTO{ID: "addtime", Title: "更新"},
		)
	case mc.SiteBoylove:
		return append(base,
			categoryDTO{ID: "free", Title: "免费"},
			categoryDTO{ID: "vip", Title: "会员"},
			categoryDTO{ID: "finish", Title: "已完结"},
			categoryDTO{ID: "serial", Title: "连载中"},
		)
	case mc.SiteEhentai:
		// key 与 client 包里的 ehCategories 对应
		return append(base,
			categoryDTO{ID: "doujinshi", Title: "同人志"},
			categoryDTO{ID: "manga", Title: "漫画"},
			categoryDTO{ID: "artistcg", Title: "画师CG"},
			categoryDTO{ID: "gamecg", Title: "游戏CG"},
			categoryDTO{ID: "imageset", Title: "图集"},
			categoryDTO{ID: "western", Title: "西方"},
		)
	case mc.SiteNhentai:
		// 本站只收 yaoi 内容，这里的 key 是 nhentai 标签名，直接透传给客户端
		return append(base,
			categoryDTO{ID: "yaoi", Title: "男同"},
			categoryDTO{ID: "bara", Title: "壮汉"},
			categoryDTO{ID: "crossdressing", Title: "伪娘"},
			categoryDTO{ID: "bdsm", Title: "调教"},
			categoryDTO{ID: "full color", Title: "全彩"},
		)
	}
	return base
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	ok(w, categoriesOf(s.siteOf(r)))
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	keyword := strings.TrimSpace(r.URL.Query().Get("q"))
	if keyword == "" {
		keyword = strings.TrimSpace(r.URL.Query().Get("keyword"))
	}
	page := queryInt(r, "page", 1)
	if keyword == "" {
		ok(w, []comicSummaryDTO{})
		return
	}

	// 跨源搜索：并行拉取后按来源轮转交错，任何单一来源都不会霸占列表；
	// 每项只带匿名来源标签（来源1/来源2…），用户感知不到背后有几个站。
	buckets := make([][]mc.ComicSummary, len(mc.AllSites))
	var wg sync.WaitGroup
	for i, site := range mc.AllSites {
		wg.Add(1)
		go func(idx int, site string) {
			defer wg.Done()
			c, err := s.clientOf(site)
			if err != nil {
				return
			}
			res, err := c.Search(keyword, page)
			if err != nil || res == nil {
				return
			}
			buckets[idx] = res.Items
		}(i, site)
	}
	wg.Wait()

	out := make([]comicSummaryDTO, 0, 48)
	idx := make([]int, len(buckets))
	for {
		progressed := false
		for i := range buckets {
			if idx[i] < len(buckets[i]) {
				out = append(out, toSummaryDTO(buckets[i][idx[i]]))
				idx[i]++
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	ok(w, out)
}

// categoryOpts 把前端下拉框的值翻译成各站点的筛选参数。
func categoryOpts(site, category string) map[string]string {
	opts := map[string]string{}
	switch site {
	case mc.SiteTibiu:
		if category != "" && category != "0" {
			opts["order"] = category
		}
	case mc.SiteManhwa:
		if category != "" && category != "0" {
			opts["order"] = category
		}
	case mc.SiteBoylove:
		switch category {
		case "free":
			opts["vip"] = "0"
		case "vip":
			opts["vip"] = "1"
		case "finish":
			opts["done"] = "1"
		case "serial":
			opts["done"] = "0"
		}
	case mc.SiteEhentai:
		// E-Hentai 用分类位掩码筛选，key 直接透传给客户端解释
		if category != "" && category != "0" {
			opts["category"] = category
		}
	case mc.SiteNhentai:
		// nhentai 的“分类”实为标签，直接透传
		if category != "" && category != "0" {
			opts["category"] = category
		}
	}
	return opts
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}

	page := queryInt(r, "page", 1)
	category := r.URL.Query().Get("category")
	sort := r.URL.Query().Get("sort")

	ranker, isRanker := c.(client.Ranker)

	var res *mc.SearchPage
	if isRanker {
		rankType := sort
		if rankType == "" {
			rankType = "top"
		}
		// boylove 的榜单按数字 type 索引，前端传的是 mr/tf 之类的 JM 排序键，这里做一次适配
		if site == mc.SiteBoylove {
			switch sort {
			case "tf", "top", "mr", "":
				rankType = "1"
			case "mv":
				rankType = "2"
			}
		}
		res, err = ranker.Ranking(rankType, page)
	} else {
		// 没有榜单能力的站点退回分类列表
		res, err = c.CategoriesFilter(page, categoryOpts(site, category))
	}
	if err != nil {
		fail(w, err)
		return
	}

	out := make([]comicSummaryDTO, 0, len(res.Items))
	for _, item := range res.Items {
		out = append(out, toSummaryDTO(item))
	}
	ok(w, out)
}

// handleLatest 返回最近更新。前端会自己把原始列表映射成卡片，
// 因此这里必须给出**绝对图片地址**（相对路径会被前端当作 JM CDN 路径拼错域名）。
func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}

	res, err := c.UpdateList(queryInt(r, "page", 1))
	if err != nil {
		fail(w, err)
		return
	}

	out := make([]map[string]any, 0, len(res.Items))
	for _, item := range res.Items {
		out = append(out, map[string]any{
			"id":     item.ID,
			"name":   item.Name,
			"title":  item.Name,
			"author": item.Author,
			"image":  item.Cover,
			"tags":   nonNilTags(item.Tags),
		})
	}
	ok(w, out)
}

func (s *Server) handleRandom(w http.ResponseWriter, r *http.Request) {
	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}

	// 站点没有随机接口：从「最近更新」里随机取一页，再随机取一条
	page := rand.Intn(5) + 1
	res, err := c.UpdateList(page)
	if err != nil || len(res.Items) == 0 {
		if res2, err2 := c.UpdateList(1); err2 == nil && len(res2.Items) > 0 {
			res = res2
		} else {
			if err == nil {
				err = mc.Errorf("没有可用的随机内容")
			}
			fail(w, err)
			return
		}
	}

	item := res.Items[rand.Intn(len(res.Items))]
	ok(w, toSummaryDTO(item))
}

// handlePromote 提供首页分区数据（对齐前端 /api/promote 的分区结构）。
func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}

	type section struct {
		ID      string           `json:"id"`
		Title   string           `json:"title"`
		Slug    string           `json:"slug"`
		Type    string           `json:"type"`
		Content []map[string]any `json:"content"`
	}

	toContent := func(items []mc.ComicSummary) []map[string]any {
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, map[string]any{
				"id":     it.ID,
				"name":   it.Name,
				"title":  it.Name,
				"author": it.Author,
				"image":  it.Cover, // 绝对地址：前端会直接走图片代理
				"tags":   nonNilTags(it.Tags),
			})
		}
		return out
	}

	sections := make([]section, 0, 3)

	if ranker, isRanker := c.(client.Ranker); isRanker {
		rankType := "top"
		if site == mc.SiteBoylove {
			rankType = "1"
		}
		if res, rerr := ranker.Ranking(rankType, 1); rerr == nil && len(res.Items) > 0 {
			sections = append(sections, section{
				ID: "ranking", Title: "排行榜", Slug: "ranking", Type: "comic",
				Content: toContent(res.Items),
			})
		}
	}

	if res, uerr := c.UpdateList(1); uerr == nil && len(res.Items) > 0 {
		sections = append(sections, section{
			ID: "latest", Title: "最近更新", Slug: "latest", Type: "comic",
			Content: toContent(res.Items),
		})
	}

	if hot, isHot := c.(client.HotLister); isHot {
		if res, herr := hot.HotList(); herr == nil && len(res.Items) > 0 {
			sections = append(sections, section{
				ID: "hot", Title: "热门推荐", Slug: "hot", Type: "comic",
				Content: toContent(res.Items),
			})
		}
	}

	ok(w, sections)
}

// ---- 详情与章节 -------------------------------------------------------------

func (s *Server) handleComicPath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v2/jm/comic/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		fail(w, mc.Errorf("缺少漫画 id"))
		return
	}
	comicID, _ := url.PathUnescape(parts[0])

	// /favorite、/comments 等 JM 专有子资源：返回空结果，前端不会报错
	if len(parts) > 1 {
		ok(w, map[string]any{"ok": false, "message": "本后端未实现该功能"})
		return
	}

	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}

	comic, err := c.GetComicDetail(comicID, true)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, comicToDetail(comic))
}

func (s *Server) handleChapterPath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v2/jm/chapter/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		fail(w, mc.Errorf("缺少章节 id"))
		return
	}
	chapterID, _ := url.PathUnescape(parts[0])

	if len(parts) > 1 {
		ok(w, map[string]any{"ok": false, "message": "本后端未实现该功能"})
		return
	}

	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}

	comicID := r.URL.Query().Get("album_id")
	chapter, err := c.GetChapterDetail(chapterID, comicID, true)
	if err != nil {
		fail(w, err)
		return
	}

	s.rememberChapterImages(chapter)
	ok(w, chapterToDetail(chapter, site))
}

// ---- 图片 -------------------------------------------------------------------

func (s *Server) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	if raw == "" {
		http.Error(w, "缺少 url", http.StatusBadRequest)
		return
	}
	target, err := url.QueryUnescape(raw)
	if err != nil {
		target = raw
	}
	if okTarget, reason := isSafeProxyTarget(target); !okTarget {
		http.Error(w, "图片地址非法: "+reason, http.StatusForbidden)
		return
	}

	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := c.Postman().Get(target)
	if err != nil || !resp.OK() {
		http.Error(w, "图片请求失败", http.StatusBadGateway)
		return
	}

	// 单张图片若带乱序标记（来自 /api/v2/jm/chapter 的 scramble_n），在这里还原
	n := mc.Atoi(r.URL.Query().Get("scramble_n"))
	body := resp.Body
	contentType := sniffImageType(body)
	if n > 1 {
		if decoded, _, derr := decode.DecodeImageBytes(body, n); derr == nil {
			body = decoded
			contentType = sniffImageType(body)
		}
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// handleChapterImage 按「章节 id + 图片文件名」取图。
//
// 前端的 DscImage 用的是 /api/chapter_image/{photoId}/{name} 这种形式，
// 所以这里需要把 name 反查回真实 URL（章节图列表会缓存）。
func (s *Server) handleChapterImage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/chapter_image/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 {
		http.Error(w, "参数不足", http.StatusBadRequest)
		return
	}
	chapterID, _ := url.PathUnescape(parts[0])
	name, _ := url.PathUnescape(strings.Join(parts[1:], "/"))

	site := s.siteOf(r)
	c, err := s.clientOf(site)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	target, scrambleN := s.lookupChapterImage(c, site, chapterID, name)
	if target == "" {
		http.Error(w, "找不到该图片", http.StatusNotFound)
		return
	}

	resp, err := c.Postman().Get(target)
	if err != nil || !resp.OK() {
		http.Error(w, "图片请求失败", http.StatusBadGateway)
		return
	}

	body := resp.Body
	contentType := sniffImageType(body)
	if scrambleN > 1 {
		if decoded, _, derr := decode.DecodeImageBytes(body, scrambleN); derr == nil {
			body = decoded
			contentType = sniffImageType(body)
		}
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// sniffImageType 按魔数判断图片类型（站点的 Content-Type 并不可信：
// 实测有 .webp 后缀实际是 JPEG 的情况）。
func sniffImageType(data []byte) string {
	switch {
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) > 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) > 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	}
	return "application/octet-stream"
}

// ---- 下载任务 ---------------------------------------------------------------

func (s *Server) handleDownloadTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ok(w, s.manager.List())
	case http.MethodPost:
		s.createDownloadTask(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDownloadTaskByID(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v2/jm/download/tasks/"), "/")
	if id == "" {
		ok(w, s.manager.List())
		return
	}

	switch r.Method {
	case http.MethodDelete:
		ok(w, map[string]any{"deleted": id})
	default:
		task, found := s.manager.Get(id)
		if !found {
			writeJSON(w, 200, map[string]any{"st": stError, "msg": "任务不存在"})
			return
		}
		ok(w, task)
	}
}

func (s *Server) createDownloadTask(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	site := s.siteOf(r)
	if real := realSourceKey(mc.ToString(body["site"])); real != "" {
		site = real
	}

	c, err := s.clientOf(site)
	if err != nil {
		fail(w, err)
		return
	}
	opts := s.optionsOf(site)

	chapterID := mc.ToString(body["chapter_id"])
	comicID := mc.ToString(body["album_id"])
	if comicID == "" {
		comicID = mc.ToString(body["comic_id"])
	}

	if chapterID == "" && comicID == "" {
		fail(w, mc.Errorf("缺少 chapter_id 或 album_id"))
		return
	}

	// 章节模式
	if chapterID != "" {
		// 先取一次章节信息，让任务卡片立刻有标题
		title, chapterName := comicID, ""
		if ch, cerr := c.GetChapterDetail(chapterID, comicID, false); cerr == nil {
			chapterName = ch.Name
			if ch.ComicName() != "" {
				title = ch.ComicName()
			}
		}

		task := s.manager.Submit(site, comicID, title, chapterID, chapterName,
			func(t *download.Task) error {
				_, derr := download.DownloadChapter(c, chapterID, comicID, opts, t)
				return derr
			})
		ok(w, task.Snapshot())
		return
	}

	// 整本模式（下载全部章节，可能很久，但任务在后台跑）
	title := comicID
	if comic, cerr := c.GetComicDetail(comicID, false); cerr == nil {
		title = comic.Name
	}

	task := s.manager.Submit(site, comicID, title, "", "",
		func(t *download.Task) error {
			_, derr := download.DownloadComic(c, comicID, opts, t)
			return derr
		})
	ok(w, task.Snapshot())
}

var _ = io.Discard
