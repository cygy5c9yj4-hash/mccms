package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mccms/mccms-go/internal/mc"
)

// nhentai 站点常量。按产品要求本客户端只收录 gay（yaoi）内容。
const (
	nhHost      = "https://nhentai.net"
	nhImageHost = "https://i.nhentai.net"
	nhThumbHost = "https://t.nhentai.net"

	nhYaoiTag = "yaoi"
	nhOneShot = "全一话"
)

// nhBlockedTags 命中即整本丢弃（fail-closed，宁可少收不静默降级）。
var nhBlockedTags = []string{"lolicon", "shotacon", "toddlercon", "lolita", "loli", "shota"}

// nhBaseQuery 基础检索式：只取 yaoi，并显式排除禁止内容。
// 列表接口不返回标签，排除只能依赖检索式；详情接口会再做一次严格校验。
var nhBaseQuery = func() string {
	parts := []string{fmt.Sprintf("tag:%q", nhYaoiTag)}
	for _, b := range nhBlockedTags {
		parts = append(parts, fmt.Sprintf("-tag:%q", b))
	}
	return strings.Join(parts, " ")
}()

// NhentaiClient nhentai 客户端。
type NhentaiClient struct {
	Base

	mu    sync.Mutex
	cache map[string]*nhGallery
}

// NewNhentai 构造 nhentai 客户端。
func NewNhentai(base Base) *NhentaiClient {
	return &NhentaiClient{Base: base, cache: map[string]*nhGallery{}}
}

// Site 站点 key。
func (c *NhentaiClient) Site() string { return mc.SiteNhentai }

// Postman 暴露底层会话。
func (c *NhentaiClient) Postman() *mc.Postman { return c.HTTP }

// ---------- nhentai API 数据模型 ----------

type nhPage struct {
	T    string `json:"t"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	Path string `json:"path"`
}

type nhTag struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type nhThumb struct {
	Path string `json:"path"`
}

// nhItem 同时覆盖「列表形态」与「详情形态」。
// 列表（/api/v2/search、/api/v2/galleries/popular）只有 english_title/thumbnail/tag_ids；
// 详情（/api/v2/galleries/{id}）才有 title{}/images/tags/upload_date。
type nhItem struct {
	ID      int    `json:"id"`
	MediaID string `json:"media_id"`

	// 列表形态
	EnglishTitle  string `json:"english_title"`
	JapaneseTitle string `json:"japanese_title"`
	Thumbnail     string `json:"thumbnail"`
	Blacklisted   bool   `json:"blacklisted"`

	// 详情形态
	Title struct {
		English  string `json:"english"`
		Japanese string `json:"japanese"`
		Pretty   string `json:"pretty"`
	} `json:"title"`
	Images struct {
		Pages     []nhPage `json:"pages"`
		Cover     nhThumb  `json:"cover"`
		Thumbnail nhThumb  `json:"thumbnail"`
	} `json:"images"`
	Tags         []nhTag `json:"tags"`
	UploadDate   int64   `json:"upload_date"`
	NumPages     int     `json:"num_pages"`
	NumFavorites int     `json:"num_favorites"`
	Scanlator    string  `json:"scanlator"`
}

type nhSearchResp struct {
	Result   []nhItem `json:"result"`
	NumPages int      `json:"num_pages"`
	PerPage  int      `json:"per_page"`
	Total    int      `json:"total"`
}

// 详情接口复用同一结构（字段完全兼容）。
type nhGallery = nhItem

// ---------- 解析与过滤 ----------

func nhItemTitle(it *nhItem) string {
	for _, s := range []string{it.Title.Pretty, it.Title.English, it.EnglishTitle, it.Title.Japanese, it.JapaneseTitle} {
		if v := strings.TrimSpace(s); v != "" {
			return v
		}
	}
	return fmt.Sprintf("nhentai %d", it.ID)
}

func nhHasTag(tags []nhTag, name string) bool {
	for _, t := range tags {
		if strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

// nhKeepDetail 详情形态可用标签做严格校验（fail-closed）。
func nhKeepDetail(tags []nhTag) bool {
	for _, t := range tags {
		for _, b := range nhBlockedTags {
			if strings.EqualFold(t.Name, b) {
				return false
			}
		}
	}
	return nhHasTag(tags, nhYaoiTag)
}

// nhKeepItem 列表形态没有标签，只能依赖检索式（已强制 yaoi 且排除禁止标签），
// 再叠加 nhentai 自身的 blacklisted 标记。
func nhKeepItem(it *nhItem) bool {
	if it.Blacklisted {
		return false
	}
	if len(it.Tags) > 0 {
		return nhKeepDetail(it.Tags)
	}
	return true
}

func nhTagNames(tags []nhTag) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		n := strings.TrimSpace(t.Name)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func nhAuthors(tags []nhTag) []string {
	out := []string{}
	for _, t := range tags {
		if t.Type == "artist" || t.Type == "group" {
			if n := strings.TrimSpace(t.Name); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// nhCoverURL 封面：优先详情里的 cover，其次列表里的 thumbnail。
func nhCoverURL(it *nhItem) string {
	if p := strings.TrimSpace(it.Images.Cover.Path); p != "" {
		return nhThumbHost + "/" + strings.TrimPrefix(p, "/")
	}
	if p := strings.TrimSpace(it.Thumbnail); p != "" {
		return nhThumbHost + "/" + strings.TrimPrefix(p, "/")
	}
	if p := strings.TrimSpace(it.Images.Thumbnail.Path); p != "" {
		return nhThumbHost + "/" + strings.TrimPrefix(p, "/")
	}
	if it.MediaID != "" {
		return fmt.Sprintf("%s/galleries/%s/thumb.jpg", nhThumbHost, it.MediaID)
	}
	return ""
}

// nhImageURLs 返回图片直链与图片 ID（只有详情接口能给）。
func nhImageURLs(it *nhItem) ([]string, []string) {
	urls := make([]string, 0, len(it.Images.Pages))
	ids := make([]string, 0, len(it.Images.Pages))
	for i, p := range it.Images.Pages {
		path := strings.TrimPrefix(strings.TrimSpace(p.Path), "/")
		if path == "" {
			path = fmt.Sprintf("galleries/%s/%d%s", it.MediaID, i+1, nhExt(p.T))
		}
		urls = append(urls, nhImageHost+"/"+path)
		ids = append(ids, fmt.Sprintf("%s-%d", it.MediaID, i+1))
	}
	return urls, ids
}

func nhExt(t string) string {
	switch strings.ToLower(t) {
	case "p":
		return ".png"
	case "g":
		return ".gif"
	case "w":
		return ".webp"
	default:
		return ".jpg"
	}
}

func nhUnixDate(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02")
}

// ---------- HTTP ----------

func (c *NhentaiClient) gallery(id string) (*nhGallery, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, mc.Errorf("nhentai: 缺少作品 ID")
	}
	c.mu.Lock()
	if g, ok := c.cache[id]; ok {
		c.mu.Unlock()
		return g, nil
	}
	c.mu.Unlock()

	resp, err := c.HTTP.Get("/api/v2/galleries/" + url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	var g nhGallery
	if err := json.Unmarshal(resp.Body, &g); err != nil {
		return nil, mc.Errorf("nhentai: 解析作品详情失败: %v", err)
	}
	if g.ID == 0 {
		return nil, mc.Errorf("nhentai: 作品 %s 不存在", id)
	}
	c.mu.Lock()
	c.cache[strconv.Itoa(g.ID)] = &g
	c.cache[id] = &g
	c.mu.Unlock()
	return &g, nil
}

// searchRaw 调 v2 搜索接口。query 为 nhentai 检索式。
func (c *NhentaiClient) searchRaw(query string, page int) ([]nhItem, int, error) {
	if page <= 0 {
		page = 1
	}
	resp, err := c.HTTP.GetWith("/api/v2/search", map[string]string{
		"query": query,
		"page":  strconv.Itoa(page),
	})
	if err != nil {
		return nil, 0, err
	}
	var out nhSearchResp
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, 0, mc.Errorf("nhentai: 解析搜索结果失败: %v", err)
	}
	total := out.Total
	if total <= 0 {
		total = out.NumPages
	}
	return out.Result, total, nil
}

// popularRaw 调热门接口（返回数组）。
func (c *NhentaiClient) popularRaw(page int) ([]nhItem, error) {
	if page <= 0 {
		page = 1
	}
	resp, err := c.HTTP.GetWith("/api/v2/galleries/popular", map[string]string{"page": strconv.Itoa(page)})
	if err != nil {
		return nil, err
	}
	var list []nhItem
	if err := json.Unmarshal(resp.Body, &list); err == nil && len(list) > 0 {
		return list, nil
	}
	var wrap struct {
		Result []nhItem `json:"result"`
	}
	if err := json.Unmarshal(resp.Body, &wrap); err == nil && len(wrap.Result) > 0 {
		return wrap.Result, nil
	}
	return nil, mc.Errorf("nhentai: 解析热门列表失败")
}

// pageFrom 把一批条目收敛成 SearchPage（再本地过滤一次，fail-closed）。
func (c *NhentaiClient) pageFrom(list []nhItem, page, total int) *mc.SearchPage {
	items := make([]mc.ComicSummary, 0, len(list))
	for i := range list {
		it := &list[i]
		if it.ID == 0 || !nhKeepItem(it) {
			continue
		}
		items = append(items, c.toSummary(it))
	}
	return &mc.SearchPage{Items: items, Total: total, Page: page, Site: mc.SiteNhentai}
}

func (c *NhentaiClient) toSummary(it *nhItem) mc.ComicSummary {
	return mc.ComicSummary{
		ID:            strconv.Itoa(it.ID),
		Name:          nhItemTitle(it),
		Author:        strings.Join(nhAuthors(it.Tags), "/"),
		Cover:         nhCoverURL(it),
		UpdateDate:    nhUnixDate(it.UploadDate),
		Views:         it.NumFavorites,
		Tags:          nhTagNames(it.Tags),
		Site:          mc.SiteNhentai,
		URL:           fmt.Sprintf("%s/g/%d/", nhHost, it.ID),
		Slug:          it.MediaID,
		LatestChapter: nhOneShot,
	}
}

// ---------- Client 接口实现 ----------

// Search 关键词搜索，始终叠加 yaoi 检索式。
func (c *NhentaiClient) Search(keyword string, page int) (*mc.SearchPage, error) {
	q := strings.TrimSpace(keyword)
	query := nhBaseQuery
	if q != "" {
		query = q + " " + nhBaseQuery
	}
	list, total, err := c.searchRaw(query, page)
	if err != nil {
		return nil, err
	}
	return c.pageFrom(list, page, total), nil
}

// CategoriesFilter 分类浏览（nhentai 的“分类”实为标签）。
func (c *NhentaiClient) CategoriesFilter(page int, options map[string]string) (*mc.SearchPage, error) {
	if kw := strings.TrimSpace(options["keyword"]); kw != "" {
		return c.Search(kw, page)
	}
	cat := strings.TrimSpace(options["category"])
	if cat == "" || cat == "0" {
		cat = strings.TrimSpace(options["tag"])
	}
	query := nhBaseQuery
	if cat != "" && cat != "0" && !strings.EqualFold(cat, nhYaoiTag) {
		query = fmt.Sprintf("tag:%q %s", cat, nhBaseQuery)
	}
	list, total, err := c.searchRaw(query, page)
	if err != nil {
		return nil, err
	}
	return c.pageFrom(list, page, total), nil
}

// UpdateList 最近更新（yaoi 结果的第一页）。
func (c *NhentaiClient) UpdateList(page int) (*mc.SearchPage, error) {
	list, total, err := c.searchRaw(nhBaseQuery, page)
	if err != nil {
		return nil, err
	}
	return c.pageFrom(list, page, total), nil
}

// HotList 热门（首页用）。
func (c *NhentaiClient) HotList() (*mc.SearchPage, error) {
	list, err := c.popularRaw(1)
	if err != nil {
		return nil, err
	}
	return c.pageFrom(list, 1, len(list)), nil
}

// Ranking 排行榜（nhentai 无独立榜，回退最近更新）。
func (c *NhentaiClient) Ranking(rankType string, page int) (*mc.SearchPage, error) {
	return c.UpdateList(page)
}

// GetComicDetail 作品详情。nhentai 一本即一话。
func (c *NhentaiClient) GetComicDetail(comicID string, fetchChapters bool) (*mc.Comic, error) {
	g, err := c.gallery(comicID)
	if err != nil {
		return nil, err
	}
	if !nhKeepDetail(g.Tags) {
		return nil, mc.Errorf("nhentai: 作品不在收录范围（仅 yaoi）")
	}
	comic := &mc.Comic{
		ComicID:    strconv.Itoa(g.ID),
		Name:       nhItemTitle(g),
		Authors:    nhAuthors(g.Tags),
		Tags:       nhTagNames(g.Tags),
		Cover:      nhCoverURL(g),
		UpdateDate: nhUnixDate(g.UploadDate),
		Views:      g.NumFavorites,
		Site:       mc.SiteNhentai,
		URL:        fmt.Sprintf("%s/g/%d/", nhHost, g.ID),
		Slug:       g.MediaID,
	}
	if fetchChapters {
		comic.Chapters = []mc.ChapterMeta{{ChapterID: strconv.Itoa(g.ID), Index: 0, Name: nhOneShot}}
		comic.ChapterAccess = map[string]mc.Access{strconv.Itoa(g.ID): {}}
	}
	return comic, nil
}

// GetChapterDetail 章节详情（整本图片）。
func (c *NhentaiClient) GetChapterDetail(chapterID, comicID string, fetchImages bool) (*mc.Chapter, error) {
	if strings.TrimSpace(comicID) == "" {
		comicID = chapterID
	}
	g, err := c.gallery(chapterID)
	if err != nil {
		return nil, err
	}
	if !nhKeepDetail(g.Tags) {
		return nil, mc.Errorf("nhentai: 作品不在收录范围（仅 yaoi）")
	}
	ch := &mc.Chapter{
		ChapterID:  strconv.Itoa(g.ID),
		ComicID:    comicID,
		Name:       nhOneShot,
		Index:      0,
		Count:      len(g.Images.Pages),
		UpdateDate: nhUnixDate(g.UploadDate),
		URL:        fmt.Sprintf("%s/g/%d/", nhHost, g.ID),
	}
	if fetchImages {
		ch.ImageURLs, ch.ImageIDs = nhImageURLs(g)
	}
	return ch, nil
}

// FetchImageURLs 补齐章节图片直链。
func (c *NhentaiClient) FetchImageURLs(ch *mc.Chapter) ([]string, error) {
	if len(ch.ImageURLs) > 0 {
		return ch.ImageURLs, nil
	}
	g, err := c.gallery(ch.ChapterID)
	if err != nil {
		return nil, err
	}
	urls, ids := nhImageURLs(g)
	ch.ImageURLs, ch.ImageIDs = urls, ids
	ch.Count = len(urls)
	return urls, nil
}

// GetComicIDFromURL 从 URL 解析作品 ID。
func (c *NhentaiClient) GetComicIDFromURL(raw string) string { return nhIDFromURL(raw) }

// GetChapterIDFromURL 从 URL 解析章节（=作品）ID。
func (c *NhentaiClient) GetChapterIDFromURL(raw string) string { return nhIDFromURL(raw) }

// nhIDFromURL 解析 /g/{id}/ 形式。
func nhIDFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "g" {
			if _, err := strconv.Atoi(parts[i+1]); err == nil {
				return parts[i+1]
			}
		}
	}
	return ""
}
