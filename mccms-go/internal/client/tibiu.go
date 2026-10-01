package client

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/mccms/mccms-go/internal/mc"
)

// TIBIU（cache.tibiu.net）客户端。
//
// 接口清单（均为实测确认）：
//
//	搜索      GET /api/data/search?key=&page=
//	漫画详情  GET /api/data/comicinfo?cid=
//	章节列表  GET /api/data/chapter?mid=
//	章节图片  GET /api/data/pic?cid=      （按 id 升序重排！）
//	分类浏览  GET /index.php/api/data/category_api
//	最近更新  GET /index.php/api/data/update_api
//	筛选项    GET /index.php/api/data/filter_options_api
//	榜单      GET /index.php/api/rankdata/{nav,lists}
//
// 两处与网页端对齐的顺序修正：
//  1. /api/data/pic 返回的是 id 降序，网页端渲染前做了升序排序，客户端必须同样升序；
//  2. /api/data/chapter 返回的是最新在前，按 xid 升序整理后才是正常的阅读顺序。
type TibiuClient struct {
	Base

	// 榜单导航是静态数据，缓存一次即可。
	rankNavCache []mc.RankNav
}

// TIBIU 的接口路径。
const (
	tibiuPathSearch        = "/api/data/search"
	tibiuPathComicInfo     = "/api/data/comicinfo"
	tibiuPathChapterList   = "/api/data/chapter"
	tibiuPathPictureList   = "/api/data/pic"
	tibiuPathCategory      = "/index.php/api/data/category_api"
	tibiuPathUpdate        = "/index.php/api/data/update_api"
	tibiuPathFilterOptions = "/index.php/api/data/filter_options_api"
	tibiuPathRankNav       = "/index.php/api/rankdata/nav"
	tibiuPathRankLists     = "/index.php/api/rankdata/lists"
)

// 搜索接口不返回总数，按页大小估算下界；与 Python 版 PAGE_SIZE_SEARCH 一致。
const tibiuSearchPageSize = 10

// 榜单接口单次最多返回的条目数（上限由站点侧决定）。
const tibiuRankMaxItems = 500

// NewTibiu 构造 TIBIU 客户端。
func NewTibiu(base Base) *TibiuClient {
	if base.SiteName == "" {
		base.SiteName = mc.SiteTibiu
	}
	return &TibiuClient{Base: base}
}

// ---- 信封 --------------------------------------------------------------------

// tibiuEnvelope 覆盖 Mccms 的两套信封：
// 详情类接口用 {"code":1,...}，列表类接口（category_api / update_api）的 code 恒为 -1，
// 成功与否只能看 "status":"success"。
type tibiuEnvelope struct {
	Code    int    `json:"code"`
	Status  string `json:"status"`
	Msg     string `json:"msg"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Type    string `json:"type"`
	Data    any    `json:"data"`
}

// parseTibiuResp 校验 HTTP 状态并解开 Mccms 信封。
//
// context 会并入错误上下文（站点报文里的 msg/type 不足以定位是哪一章出的问题）。
func (t *TibiuClient) parseTibiuResp(resp *mc.Response, context map[string]any) (map[string]any, error) {
	if !resp.OK() {
		return nil, tibiuErrorf(mergeCtx(context, map[string]any{
			"site": t.SiteName, "url": resp.URL, "http_code": resp.StatusCode,
		}), "请求失败: [%s]，http_code: [%d]", resp.URL, resp.StatusCode)
	}

	var env tibiuEnvelope
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, tibiuErrorf(mergeCtx(context, map[string]any{
			"site": t.SiteName, "url": resp.URL,
		}), "接口返回的不是合法 JSON: [%s]: %v", resp.URL, err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		payload = map[string]any{}
	}

	// 列表类接口：code 恒为 -1，status=success 才是成功
	if strings.EqualFold(strings.TrimSpace(env.Status), "success") {
		return payload, nil
	}

	// 详情类接口未带 code 时按成功处理（与 Python 版 safe_int(..., 1) 的默认值一致）
	code := 1
	if _, ok := payload["code"]; ok {
		code = mc.Atoi(payload["code"])
	}
	if code == 1 {
		return payload, nil
	}

	msg := strings.TrimSpace(env.Msg)
	if msg == "" {
		msg = strings.TrimSpace(env.Message)
	}
	if msg == "" {
		msg = strings.TrimSpace(env.Error)
	}
	if msg == "" {
		msg = mc.Errorf("接口返回异常: code=%d", code).Msg
	}

	ctx := mergeCtx(context, map[string]any{
		"site":        t.SiteName,
		"url":         resp.URL,
		"code":        code,
		"access_type": env.Type,
	})
	switch code {
	case 2: // 未登录
		return nil, mc.RaiseAccessError(msg, ctx, 2, "")
	case 3: // 已登录但缺少权益
		return nil, mc.RaiseAccessError(msg, ctx, 3, env.Type)
	}
	return nil, mc.NewError(msg, ctx)
}

func mergeCtx(dst, extra map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(extra))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// tibiuErrorf 带上下文的业务错误。
func tibiuErrorf(ctx map[string]any, format string, args ...any) error {
	return mc.NewError(mc.Errorf(format, args...).Msg, ctx)
}

// tibiuGetJSON 发起 GET 并解开信封。
func (t *TibiuClient) tibiuGetJSON(path string, query, context map[string]any) (map[string]any, error) {
	q := map[string]string{}
	for k, v := range query {
		q[k] = mc.ToString(v)
	}
	resp, err := t.GetWith(path, q)
	if err != nil {
		return nil, err
	}
	return t.parseTibiuResp(resp, context)
}

// tibiuDataList 取 data 数组；缺字段 / 类型不符时退化为空列表。
func tibiuDataList(payload map[string]any) []map[string]any {
	raw, _ := payload["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// tibiuDataMap 取 data 对象。
func tibiuDataMap(payload map[string]any) map[string]any {
	if m, ok := payload["data"].(map[string]any); ok {
		return m
	}
	return nil
}

// tibiuSortAscBy 按数字字段升序重排（站点返回的顺序与网页端展示顺序不一致）。
func tibiuSortAscBy(list []map[string]any, field string) {
	sort.SliceStable(list, func(i, j int) bool {
		return mc.Atoi(list[i][field]) < mc.Atoi(list[j][field])
	})
}

// ---- 搜索 --------------------------------------------------------------------

// Search 按关键字搜索。
func (t *TibiuClient) Search(keyword string, page int) (*mc.SearchPage, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, mc.Errorf("搜索关键字不能为空")
	}
	if page < 1 {
		page = 1
	}

	payload, err := t.tibiuGetJSON(tibiuPathSearch,
		map[string]any{"key": keyword, "page": page},
		map[string]any{"keyword": keyword, "page": page})
	if err != nil {
		return nil, err
	}

	list := tibiuDataList(payload)
	out := &mc.SearchPage{
		Items: make([]mc.ComicSummary, 0, len(list)),
		Page:  page,
		Site:  t.SiteName,
	}
	for _, raw := range list {
		id := mc.ToString(raw["id"])
		summary := BuildSummary(id, raw, t.SiteName)
		summary.URL = "/comic/" + id
		out.Items = append(out.Items, summary)
	}
	out.Total = tibiuGuessTotal(len(out.Items), page)
	return out, nil
}

// tibiuGuessTotal 搜索接口不返回总数，用「满页与否」估算下界。
func tibiuGuessTotal(count, page int) int {
	switch {
	case count == 0:
		return 0
	case count < tibiuSearchPageSize:
		return (page-1)*tibiuSearchPageSize + count
	default:
		return page*tibiuSearchPageSize + 1
	}
}

// ---- 详情 --------------------------------------------------------------------

// GetComicDetail 漫画详情；fetchChapters 为 false 时只请求 comicinfo。
func (t *TibiuClient) GetComicDetail(comicID string, fetchChapters bool) (*mc.Comic, error) {
	comicID = mc.ParseToID(comicID)
	if comicID == "" {
		return nil, mc.Errorf("漫画 id 不能为空")
	}

	payload, err := t.tibiuGetJSON(tibiuPathComicInfo,
		map[string]any{"cid": comicID},
		map[string]any{"comic_id": comicID})
	if err != nil {
		return nil, err
	}

	raw := tibiuDataMap(payload)
	if len(raw) == 0 {
		return nil, tibiuErrorf(
			map[string]any{"site": t.SiteName, "comic_id": comicID},
			"漫画不存在: [%s]", comicID)
	}

	comic := tibiuBuildComic(raw, comicID, t.SiteName)

	if fetchChapters {
		chapters, access, err := t.tibiuFetchChapterList(comicID)
		if err != nil {
			return nil, err
		}
		comic.Chapters = mc.DistinctChapters(chapters)
		comic.ChapterAccess = access
	}
	return comic, nil
}

// tibiuBuildComic 把 comicinfo 的原始对象归一化成 Comic。
func tibiuBuildComic(raw map[string]any, comicID, site string) *mc.Comic {
	authorText := mc.Unescape(mc.ToString(raw["author"]))
	authors := make([]string, 0, 2)
	for _, part := range strings.Split(strings.ReplaceAll(authorText, "，", "/"), "/") {
		if part = strings.TrimSpace(part); part != "" {
			authors = append(authors, part)
		}
	}

	// 长简介优先（列表项只有短简介 text）
	description := mc.ToString(raw["content"])
	if strings.TrimSpace(description) == "" {
		description = mc.ToString(raw["text"])
	}

	comic := &mc.Comic{
		ComicID:     comicID,
		Name:        mc.Unescape(mc.ToString(raw["name"])),
		Authors:     authors,
		Description: mc.Unescape(description),
		Cover:       mc.ToString(raw["pic"]),
		Serialize:   mc.Unescape(mc.ToString(raw["serialize"])),
		UpdateDate:  mc.ToString(raw["addtime"]),
		Views:       mc.ParseHumanNumber(raw["hits"]),
		Score:       toFloat(raw["score"]),
		Site:        site,
		URL:         "/comic/" + comicID,
		Slug:        mc.ToString(raw["yname"]),
	}
	return comic
}

// tibiuFetchChapterList 返回按 xid 升序整理后的章节元信息与权益表。
func (t *TibiuClient) tibiuFetchChapterList(comicID string) ([]mc.ChapterMeta, map[string]mc.Access, error) {
	payload, err := t.tibiuGetJSON(tibiuPathChapterList,
		map[string]any{"mid": comicID},
		map[string]any{"comic_id": comicID})
	if err != nil {
		return nil, nil, err
	}

	list := tibiuDataList(payload)
	if len(list) == 0 {
		return nil, nil, nil
	}

	// 接口返回的是倒序（最新在前），按 xid（漫画内序号）升序整理
	tibiuSortAscBy(list, "xid")

	metas := make([]mc.ChapterMeta, 0, len(list))
	access := make(map[string]mc.Access, len(list))
	for i, raw := range list {
		chapterID := mc.ToString(raw["id"])
		if chapterID == "" {
			continue
		}
		metas = append(metas, mc.ChapterMeta{
			ChapterID: chapterID,
			Index:     i + 1,
			Name:      mc.Unescape(mc.ToString(raw["name"])),
		})
		access[chapterID] = mc.Access{
			VIP:   mc.Atoi(raw["vip"]),
			Cion:  mc.Atoi(raw["cion"]),
			Price: mc.Atoi(raw["pay"]),
			Pnum:  mc.Atoi(raw["pnum"]),
		}
	}
	return metas, access, nil
}

// ---- 章节 --------------------------------------------------------------------

// GetChapterDetail 章节详情。
//
// comicID 为空时无法反查所属漫画（站点没有对应接口），章节名退化为「第{chapterID}话」。
func (t *TibiuClient) GetChapterDetail(chapterID, comicID string, fetchImages bool) (*mc.Chapter, error) {
	chapterID = mc.ParseToID(chapterID)
	if chapterID == "" {
		return nil, mc.Errorf("章节 id 不能为空")
	}
	if comicID != "" {
		comicID = mc.ParseToID(comicID)
	}

	chapter := &mc.Chapter{
		ChapterID: chapterID,
		ComicID:   comicID,
		Index:     1,
	}

	if comicID != "" {
		metas, access, err := t.tibiuFetchChapterList(comicID)
		if err != nil {
			return nil, err
		}
		if a, ok := access[chapterID]; ok {
			chapter.Access = a
			chapter.Count = a.Pnum
		}
		// 章节名与序号以列表里的位置为准
		for _, m := range metas {
			if m.ChapterID == chapterID {
				chapter.Name = m.Name
				chapter.Index = m.Index
				break
			}
		}
	}

	if strings.TrimSpace(chapter.Name) == "" {
		chapter.Name = "第" + chapterID + "话"
	}
	if comicID != "" {
		chapter.URL = "/chapter/" + comicID + "/" + chapterID
	} else {
		chapter.URL = "/chapter/-/" + chapterID
	}

	if fetchImages {
		if _, err := t.FetchImageURLs(chapter); err != nil {
			return nil, err
		}
	}
	return chapter, nil
}

// FetchImageURLs 取章节图片并写入 chapter。
//
// 站点返回的 data 是按 id 降序的，这里按 id 升序重排以匹配网页端的阅读顺序。
func (t *TibiuClient) FetchImageURLs(ch *mc.Chapter) ([]string, error) {
	if ch == nil {
		return nil, mc.Errorf("章节对象不能为空")
	}

	payload, err := t.tibiuGetJSON(tibiuPathPictureList,
		map[string]any{"cid": ch.ChapterID},
		map[string]any{"chapter_id": ch.ChapterID, "comic_id": ch.ComicID})
	if err != nil {
		return nil, err
	}

	list := tibiuDataList(payload)
	if len(list) == 0 {
		return nil, tibiuErrorf(
			map[string]any{"site": t.SiteName, "chapter_id": ch.ChapterID, "comic_id": ch.ComicID},
			"章节 [%s] 没有返回任何图片", ch.ChapterID)
	}

	tibiuSortAscBy(list, "id")

	urls := make([]string, 0, len(list))
	ids := make([]string, 0, len(list))
	for _, raw := range list {
		url := strings.TrimSpace(mc.ToString(raw["img"]))
		if url == "" {
			continue
		}
		urls = append(urls, url)
		ids = append(ids, mc.ToString(raw["id"]))
	}

	ch.SetImages(urls, ids) // 顺带更新 Count
	return urls, nil
}

// ---- 浏览 --------------------------------------------------------------------

// CategoriesFilter 分类浏览；opts 支持 order / tags / tids。
func (t *TibiuClient) CategoriesFilter(page int, opts map[string]string) (*mc.SearchPage, error) {
	if page < 1 {
		page = 1
	}
	query := map[string]any{"page": page}
	for _, key := range []string{"order", "tags", "tids"} {
		if v := strings.TrimSpace(opts[key]); v != "" {
			query[key] = v
		}
	}
	return t.tibiuListPage(tibiuPathCategory, query, page)
}

// UpdateList 最近更新。
func (t *TibiuClient) UpdateList(page int) (*mc.SearchPage, error) {
	if page < 1 {
		page = 1
	}
	return t.tibiuListPage(tibiuPathUpdate, map[string]any{"page": page}, page)
}

// FilterOptions 筛选项（分类 / 标签的 id -> 名称）。
func (t *TibiuClient) FilterOptions(mode string) (map[string]any, error) {
	if strings.TrimSpace(mode) == "" {
		mode = "update"
	}
	payload, err := t.tibiuGetJSON(tibiuPathFilterOptions, map[string]any{"mode": mode}, nil)
	if err != nil {
		return nil, err
	}
	if data, ok := payload["data"].(map[string]any); ok {
		return data, nil
	}
	return map[string]any{}, nil
}

// tibiuListPage 解析列表类信封的公共逻辑。
func (t *TibiuClient) tibiuListPage(path string, query map[string]any, page int) (*mc.SearchPage, error) {
	payload, err := t.tibiuGetJSON(path, query, map[string]any{"page": page})
	if err != nil {
		return nil, err
	}

	list := tibiuDataList(payload)
	out := &mc.SearchPage{
		Items: make([]mc.ComicSummary, 0, len(list)),
		Page:  page,
		Site:  t.SiteName,
	}
	for _, raw := range list {
		id := mc.ToString(raw["id"])
		summary := BuildSummary(id, raw, t.SiteName)
		summary.URL = "/comic/" + id
		summary.Slug = mc.ToString(raw["yname"])
		out.Items = append(out.Items, summary)
	}

	out.Total = mc.Atoi(payload["total_items"])
	if out.Total == 0 {
		out.Total = tibiuGuessTotal(len(out.Items), page)
	}
	return out, nil
}

// ---- 榜单 --------------------------------------------------------------------

// RankingNav 榜单分类导航（人气榜 / 月票榜 / 收藏榜 / 日榜 / 周榜 / 月榜 / 飙升榜）。
// 导航是静态数据，首次取回后缓存。
func (t *TibiuClient) RankingNav() ([]mc.RankNav, error) {
	if t.rankNavCache != nil {
		return t.rankNavCache, nil
	}

	payload, err := t.tibiuGetJSON(tibiuPathRankNav, nil, nil)
	if err != nil {
		return nil, err
	}

	list := tibiuDataList(payload)
	out := make([]mc.RankNav, 0, len(list))
	for _, raw := range list {
		typ := mc.ToString(raw["type"])
		if typ == "" {
			continue
		}
		out = append(out, mc.RankNav{Type: typ, Name: mc.Unescape(mc.ToString(raw["name"]))})
	}
	t.rankNavCache = out
	return out, nil
}

// Ranking 榜单列表；rankType 取 RankingNav 的 type 字段（top / ticket / fav / day / week / month）。
func (t *TibiuClient) Ranking(rankType string, page int) (*mc.SearchPage, error) {
	if strings.TrimSpace(rankType) == "" {
		rankType = "top"
	}
	if page < 1 {
		page = 1
	}
	return t.tibiuListPage(tibiuPathRankLists, map[string]any{
		"type":      rankType,
		"page":      page,
		"max_items": tibiuRankMaxItems,
	}, page)
}

// ---- URL ---------------------------------------------------------------------

// GetComicIDFromURL 从 URL / 纯 id 里解析漫画 id。
func (t *TibiuClient) GetComicIDFromURL(u string) string { return mc.ParseToID(u) }

// GetChapterIDFromURL 从 URL 里解析章节 id（/chapter/{comic}/{chapter} 取后者）。
func (t *TibiuClient) GetChapterIDFromURL(u string) string {
	if id := mc.ParseChapterIDFromURL(u); id != "" {
		return id
	}
	return mc.ParseToID(u)
}

// ParseChapterURL 解析 TIBIU 的章节 URL，返回 (comicID, chapterID)。
// 站点章节路径形如 /chapter/{comicID}/{chapterID}，一次能拿到两个 id。
func (t *TibiuClient) ParseChapterURL(u string) (string, string) {
	parts := strings.Split(u, "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] != "chapter" {
			continue
		}
		comicID := tibiuDigits(parts[i+1])
		chapterID := tibiuDigits(parts[i+2])
		if comicID != "" && chapterID != "" {
			return comicID, chapterID
		}
	}
	return t.GetComicIDFromURL(u), t.GetChapterIDFromURL(u)
}

func tibiuDigits(s string) string {
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err != nil {
		return ""
	}
	return s
}
