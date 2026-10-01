// 香香腐宅（boylove.cc）站点客户端。
//
// 只用网页端公开接口（/home/api/* 与 /home/book/*）。站点另有一套与 18comic(JM)
// 同构的 App 端接口（/setting、/latest、/api/album 等），那套要求客户端签名
// （token = md5(ts + 客户端密钥)），本文件既不复用也不推导任何签名或凭证：
// 网页端接口已经覆盖搜索/列表/详情/章节/图片的全部需求，没有理由伪造官方客户端身份。
//
// 参考实现为 mccms/src/mccms/boylove_client_impl.py（已实测验证），
// 本文件逐条对齐其行为，并把每个坑的「为什么」写在对应代码旁。
package client

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mccms/mccms-go/internal/mc"
	"golang.org/x/net/html"
)

// ---- 站点常量 ---------------------------------------------------------------

const (
	// boyloveImageBase 图片 CDN。og:image 与列表项的 image/cover 都是站内相对
	// 路径（如 /bookimages/...），必须补上这个前缀才是可下载地址。
	boyloveImageBase = "https://img.boylove.cc"

	boylovePathSearch      = "/home/api/searchk"
	boylovePathChapterList = "/home/api/chapter_list/tp/%s"
	boylovePathRank        = "/home/api/rank/type/%s"
	boylovePathCate        = "/home/api/cate/tp/%s"
	boylovePathComic       = "/home/book/index/id/%s"
	// boylovePathReader 阅读页路径就是 capter 这个拼写（站点自己的笔误），
	// 写成 chapter 会 404。
	boylovePathReader = "/home/book/capter/id/%s"

	// boyloveSearchType 搜索 type：0/1 = 漫画，2 = 小说。
	// Go 侧 Search 接口没有 type 参数，固定搜漫画。
	boyloveSearchType = 0
)

// 榜单分组键：/home/api/rank/type/{n} 一次返回四组榜单。
const (
	boyloveRankClicks    = "most_clicks"
	boyloveRankConsumes  = "most_consumes"
	boyloveRankFavorites = "most_favorites"
	boyloveRankSearch    = "most_search"
)

// boyloveRankGroups 榜单导航（顺序即展示顺序）。
var boyloveRankGroups = []struct{ Type, Name string }{
	{boyloveRankClicks, "人气榜"},
	{boyloveRankConsumes, "消费榜"},
	{boyloveRankFavorites, "收藏榜"},
	{boyloveRankSearch, "搜索榜"},
}

// ---- 正则 -------------------------------------------------------------------
//
// 阅读页里有几处信息只存在于内联 JS / <title> 里，DOM 结构反而更绕，
// 所以这几项直接用正则从原文抠。
var (
	// <title>[第01话] 无根树/无根之树 - 香香腐宅BoyLove│…</title>
	reBoyloveTitleBracket = regexp.MustCompile(`(?s)<title>\s*\[([^\]]+)\]`)
	// 退化路径：<title> 里没有方括号时，取到第一个短横线为止
	reBoyloveTitlePlain = regexp.MustCompile(`(?s)<title>\s*([^<\-–]+)`)

	// var imageData = [{"id":0,"src":"…","width":649,"height":993}, …];
	reBoyloveImageDataAt = regexp.MustCompile(`imageData\s*=\s*`)
	// var randomClass = 11;   竖带数量，逐章不同
	reBoyloveRandomClass = regexp.MustCompile(`randomClass\s*=\s*(\d+)`)
	// 阅读页的 VIP 角标（缺失时不要紧，章节列表的 isvip 才是权威）
	reBoyloveVIPMark = regexp.MustCompile(`(?i)class="[^"]*vip[^"]*"[^>]*>\s*VIP`)
)

// ---- 客户端 -----------------------------------------------------------------

// BoyloveClient 香香腐宅站点客户端。
type BoyloveClient struct {
	Base
}

// 编译期断言：必须同时满足 Client 与 Ranker。
var (
	_ Client = (*BoyloveClient)(nil)
	_ Ranker = (*BoyloveClient)(nil)
)

// NewBoylove 构造客户端。
//
// 这里刻意不实现 Login：该站网页端公开接口无需任何凭证，
// 而需要凭证的 App 端接口本库一律不碰，所以账号密码不会生效——
// 与其做一个假的登录，不如让「未实现」这件事保持显式。
func NewBoylove(base Base) *BoyloveClient {
	if base.SiteName == "" {
		base.SiteName = mc.SiteBoylove
	}
	if base.HTTP == nil {
		// 兜底构造，避免调用方漏传 Postman 时 panic（无论如何都不 panic）
		base.HTTP = mc.NewPostman(mc.PostmanConfig{Site: base.SiteName})
	}
	return &BoyloveClient{Base: base}
}

// ---- 请求与信封 -------------------------------------------------------------

// boyloveEnvelope 网页端 JSON API 的信封。
//
// 成功码不统一：搜索返回 code=0，列表/章节/榜单返回 code=1，两者都算成功；
// 失败形如 {"code":500,"data":[],"errorMsg":"Server Error"}。
type boyloveEnvelope struct {
	Code     int             `json:"code"`
	Result   json.RawMessage `json:"result"`
	Data     json.RawMessage `json:"data"`
	Msg      string          `json:"msg"`
	ErrorMsg string          `json:"errorMsg"`
}

// get 发起 GET，并把站点与 URL 上下文补进错误里。
func (c *BoyloveClient) get(path string, query map[string]string) (*mc.Response, error) {
	var (
		resp *mc.Response
		err  error
	)
	if len(query) > 0 {
		resp, err = c.HTTP.GetWith(path, query)
	} else {
		resp, err = c.HTTP.Get(path)
	}
	if err != nil {
		return nil, mc.Errorf("请求失败: [%s]：%v", c.HTTP.BuildURL(path), err)
	}
	if resp == nil {
		return nil, mc.Errorf("请求失败: [%s]：响应为空", c.HTTP.BuildURL(path))
	}
	return resp, nil
}

// getHTML 取 HTML 页面正文。
func (c *BoyloveClient) getHTML(path string) (string, error) {
	resp, err := c.get(path, nil)
	if err != nil {
		return "", err
	}
	return resp.Text(), nil
}

// api 调用网页端 JSON API 并拆信封，返回 result（其次 data）的原始 JSON。
func (c *BoyloveClient) api(path string, query map[string]string, ctx map[string]any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = map[string]any{}
	}
	ctx["site"] = c.SiteName

	resp, err := c.get(path, query)
	if err != nil {
		return nil, err
	}

	var env boyloveEnvelope
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		ctx["url"] = resp.URL
		return nil, mc.NewError(fmt.Sprintf("接口返回非 JSON: [%s]：%v", resp.URL, err), ctx)
	}

	if env.Code == 0 || env.Code == 1 {
		// 两种成功信封都见过，result 优先、data 兜底
		if raw := boyloveNotNullJSON(env.Result); raw != nil {
			return raw, nil
		}
		if raw := boyloveNotNullJSON(env.Data); raw != nil {
			return raw, nil
		}
		return json.RawMessage("{}"), nil
	}

	// 失败信息优先 errorMsg，其次 msg（两者的实际用法见逆向文档 §4.3）
	msg := strings.TrimSpace(env.ErrorMsg)
	if msg == "" {
		msg = strings.TrimSpace(env.Msg)
	}
	if msg == "" {
		msg = fmt.Sprintf("接口返回异常: code=%d", env.Code)
	}

	ctx["url"] = resp.URL
	ctx["code"] = env.Code

	if env.Code == 401 {
		// 401 = 缺少登录态 / 客户端凭证（App 端接口的签名校验就是这么拒绝的）。
		// 本库如实映射成「需要登录」，不做也不建议任何绕过尝试。
		return nil, mc.RaiseAccessError(msg, ctx, 2, "")
	}
	return nil, mc.NewError(msg, ctx)
}

// ---- JSON 结构 --------------------------------------------------------------

// boyloveListResult 搜索 / 分类接口的 result。
type boyloveListResult struct {
	List []map[string]any `json:"list"`
	// LastPage 声明成 any：站点有时给 true，有时给字符串或 0/1
	LastPage  any `json:"lastPage"`
	TotalRow  int `json:"totalRow"`
	TotalPage int `json:"totalPage"`
}

// boyloveRankResult 榜单接口的 result（一次返回四组）。
type boyloveRankResult struct {
	MostClicks    []map[string]any `json:"most_clicks"`
	MostConsumes  []map[string]any `json:"most_consumes"`
	MostFavorites []map[string]any `json:"most_favorites"`
	MostSearch    []map[string]any `json:"most_search"`
}

// pick 取指定分组的列表；未知分组退回主榜单（人气榜）。
func (r boyloveRankResult) pick(group string) []map[string]any {
	switch group {
	case boyloveRankConsumes:
		return r.MostConsumes
	case boyloveRankFavorites:
		return r.MostFavorites
	case boyloveRankSearch:
		return r.MostSearch
	default:
		return r.MostClicks
	}
}

// has 该分组在响应里是否存在（用于按实际响应裁剪导航）。
func (r boyloveRankResult) has(group string) bool { return len(r.pick(group)) > 0 }

// boyloveChapterResult 章节列表接口的 result。
//
// 注意 pageSize 字段是幌子：接口实际一次返回全部章节。
type boyloveChapterResult struct {
	List []struct {
		ID    any    `json:"id"`
		IsVip any    `json:"isvip"`
		Title string `json:"title"`
		// Score / CreateTime 目前不用，但保留字段说明响应确实带它们
		Score      any    `json:"score"`
		CreateTime string `json:"create_time"`
	} `json:"list"`
	TotalRow  int `json:"totalRow"`
	TotalPage int `json:"totalPage"`
}

// ---- 小工具 -----------------------------------------------------------------

// boyloveNotNullJSON 过滤掉 JSON null / 空值。
func boyloveNotNullJSON(raw json.RawMessage) json.RawMessage {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil
	}
	return raw
}

// boyloveTruthy 宽松判断 JSON 里的布尔字段。
func boyloveTruthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "y":
			return true
		}
	case float64:
		return t != 0
	}
	return false
}

// boyloveAbsImage 把站内相对图片路径补全成 CDN 绝对地址。
func boyloveAbsImage(u string) string {
	u = strings.TrimSpace(u)
	if u == "" || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return boyloveImageBase + u
}

// boyloveOptInt 从 opts 里取整数选项：支持别名，空值与非法值都退回默认值。
//
// 之所以容忍非法值：别的站点用 order='hits' 这类字符串，调用方若把同一份
// 参数透传给本客户端，这里宁可忽略也不能拼出一个会让接口返回 Server Error 的路径。
func boyloveOptInt(opts map[string]string, def int, keys ...string) int {
	for _, k := range keys {
		v, ok := opts[k]
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		return n
	}
	return def
}

// boyloveGuessTotal 估算总数：接口不返回总数时，用「是否满页」推出下界。
func boyloveGuessTotal(count, page int) int {
	if count == 0 {
		return 0
	}
	if count < mc.PageSizeDefault {
		return (page-1)*mc.PageSizeDefault + count
	}
	return page*mc.PageSizeDefault + 1
}

// boylovePageTotal 列表接口带 lastPage 时的总数：末页 = 前面整页 + 本页。
func boylovePageTotal(count, page int, lastPage bool) int {
	if !lastPage {
		return boyloveGuessTotal(count, page)
	}
	return (page-1)*mc.PageSizeDefault + count
}

// boyloveDecodeError 统一的「响应结构异常」错误。
func boyloveDecodeError(what string, err error) error {
	return mc.Errorf("接口响应结构异常: [%s]：%v", what, err)
}

// ---- 列表项归一化 -----------------------------------------------------------

// boyloveSerialize 列表项的 mhstatus → 中文状态（1 = 完结）。
func boyloveSerialize(raw map[string]any) string {
	if mc.Atoi(raw["mhstatus"]) == 1 {
		return "完结"
	}
	return "连载"
}

// buildSummary 把列表项归一化成 ComicSummary。
//
// 搜索/榜单/分类返回的是同一套对象，且信息量很大（作者、标签、简介、评分、
// 状态都在列表里），所以不必为了这些字段再进详情页。
// 站点字段名与 mc 通用命名不一致（title/auther/desc/keyword/pingfen/mhstatus），
// 这里先补齐映射再交给共享的 BuildSummary，避免每个接口各写一遍。
func (c *BoyloveClient) buildSummary(raw map[string]any) mc.ComicSummary {
	id := mc.ToString(raw["id"])

	norm := make(map[string]any, len(raw)+6)
	for k, v := range raw {
		norm[k] = v
	}
	norm["name"] = firstNonNil(raw["title"], raw["name"])
	norm["author"] = firstNonNil(raw["auther"], raw["author"])
	norm["text"] = firstNonNil(raw["desc"], raw["text"])
	norm["score"] = firstNonNil(raw["pingfen"], raw["score"])
	norm["tags"] = firstNonNil(raw["keyword"], raw["tags"])
	norm["serialize"] = boyloveSerialize(raw)

	out := BuildSummary(id, norm, c.SiteName)

	// 列表项里的 image/cover 是站内相对路径，BuildSummary 只取值不补全，这里覆盖
	out.Cover = boyloveAbsImage(mc.ToString(firstNonNil(raw["image"], raw["cover"])))
	if id != "" {
		out.URL = fmt.Sprintf(boylovePathComic, id)
	}
	return out
}

// buildSummaries 批量归一化，保持接口返回顺序。
func (c *BoyloveClient) buildSummaries(list []map[string]any) []mc.ComicSummary {
	items := make([]mc.ComicSummary, 0, len(list))
	for _, raw := range list {
		if raw == nil {
			continue
		}
		items = append(items, c.buildSummary(raw))
	}
	return items
}

// ---- 搜索 -------------------------------------------------------------------

// Search 按关键字搜索。
func (c *BoyloveClient) Search(keyword string, page int) (*mc.SearchPage, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, mc.Errorf("搜索关键字不能为空")
	}
	if page < 1 {
		page = 1
	}

	raw, err := c.api(boylovePathSearch, map[string]string{
		"keyword": keyword,
		"type":    strconv.Itoa(boyloveSearchType),
		"pageNo":  strconv.Itoa(page),
	}, map[string]any{"keyword": keyword, "page": page})
	if err != nil {
		return nil, err
	}

	var result boyloveListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, boyloveDecodeError(boylovePathSearch, err)
	}

	items := c.buildSummaries(result.List)
	return &mc.SearchPage{
		Items: items,
		Total: boyloveGuessTotal(len(items), page),
		Page:  page,
		Site:  c.SiteName,
	}, nil
}

// ---- 详情 -------------------------------------------------------------------

// GetComicDetail 漫画详情；fetchChapters 为 false 时跳过章节列表。
func (c *BoyloveClient) GetComicDetail(comicID string, fetchChapters bool) (*mc.Comic, error) {
	comicID = mc.ParseToID(comicID)
	if comicID == "" {
		return nil, mc.Errorf("漫画 id 不能为空")
	}

	pageHTML, err := c.getHTML(fmt.Sprintf(boylovePathComic, comicID))
	if err != nil {
		return nil, err
	}
	root, err := ParseHTML(pageHTML)
	if err != nil {
		return nil, mc.Errorf("详情页解析失败: comic=%s：%v", comicID, err)
	}

	comic := c.parseComicDetail(root, comicID)

	if fetchChapters {
		metas, access, err := c.fetchChapterList(comicID)
		if err != nil {
			return nil, err
		}
		// 章节 id 不单调（435648, 435649, 435647, 435650…），
		// 阅读顺序由接口的列表顺序决定；DistinctChapters 只做去重与重排序号、
		// 不排序，因此这里可以安全地用它。
		comic.Chapters = mc.DistinctChapters(metas)
		comic.ChapterAccess = access
	}
	return comic, nil
}

// parseComicDetail 解析详情页 HTML。
func (c *BoyloveClient) parseComicDetail(root *html.Node, comicID string) *mc.Comic {
	comic := &mc.Comic{
		ComicID: comicID,
		Site:    c.SiteName,
		URL:     fmt.Sprintf(boylovePathComic, comicID),
	}

	// 名称：og:title 比 <title> 干净（后者带站点后缀），但仍要切掉「 - 香香腐宅…」
	name := MetaContent(root, "og:title")
	if strings.TrimSpace(name) == "" {
		name = Text(FirstByTag(root, "h1"))
	}
	if i := strings.Index(name, " - 香香腐宅"); i >= 0 {
		name = name[:i]
	}
	comic.Name = mc.Unescape(name)

	// 简介：站点的换行是字面量 "br /"（不是 <br>），必须还原成换行
	comic.Description = strings.TrimSpace(
		strings.ReplaceAll(MetaContent(root, "og:description"), `"br /"`, "\n"))

	// 封面：og:image 是站内相对路径
	comic.Cover = boyloveAbsImage(MetaContent(root, "og:image"))

	// 标签：meta keywords（逗号分隔）
	comic.Tags = mc.ParseTags(MetaContent(root, "keywords"))

	// 详情区：<p class="data"> 用 <i>字段名：</i> 区分字段，
	// 第一个没有 <i> 的才是连载状态文本。
	var (
		authors []string
		seen    = map[string]bool{}
	)
	for _, p := range ByTagClass(root, "p", "data") {
		labelNode := FirstByTag(p, "i")
		value := Text(p)

		if labelNode == nil {
			if comic.Serialize == "" {
				comic.Serialize = value
			}
			continue
		}

		label := boyloveCleanLabel(Text(labelNode))
		switch {
		case strings.Contains(label, "作者"):
			for _, a := range ByTag(p, "a") {
				if n := strings.TrimSpace(Text(a)); n != "" && !seen[n] {
					seen[n] = true
					authors = append(authors, n)
				}
			}
		case strings.Contains(label, "觀看") || strings.Contains(label, "观看"):
			// 标签文字里不含数字，去掉后再解析「1,234」「14万」这类写法
			comic.Views = mc.ParseHumanNumber(strings.Replace(value, label, "", 1))
		}
	}
	comic.Authors = authors

	// 评分：.rating 的 data-avg。
	// 必须按完整 class token 找（页面里还有 .rating-star / .rating-score，它们没有 data-avg）
	if rating := FirstByClass(root, "rating"); rating != nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(Attr(rating, "data-avg")), 64); err == nil {
			comic.Score = f
		}
	}

	return comic
}

// boyloveCleanLabel 取 <i>作者：</i> 里的纯标签文字（去掉中英文冒号）。
func boyloveCleanLabel(s string) string {
	s = strings.NewReplacer("：", "", ":", "").Replace(s)
	return strings.TrimSpace(s)
}

// ---- 章节列表 ---------------------------------------------------------------

// fetchChapterList 取章节列表。
//
// 返回的切片严格保持接口顺序，这是阅读顺序的唯一依据（id 不可靠）。
func (c *BoyloveClient) fetchChapterList(comicID string) ([]mc.ChapterMeta, map[string]mc.Access, error) {
	path := fmt.Sprintf(boylovePathChapterList, comicID)
	raw, err := c.api(path, nil, map[string]any{"comic_id": comicID})
	if err != nil {
		return nil, nil, err
	}

	var result boyloveChapterResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, nil, boyloveDecodeError(path, err)
	}

	metas := make([]mc.ChapterMeta, 0, len(result.List))
	access := make(map[string]mc.Access, len(result.List))
	for i, item := range result.List {
		chapterID := mc.ToString(item.ID)
		if chapterID == "" {
			continue
		}
		metas = append(metas, mc.ChapterMeta{
			ChapterID: chapterID,
			// 序号用列表下标 +1：id 有 435648, 435649, 435647 这种回跳
			Index: i + 1,
			Name:  mc.Unescape(item.Title),
		})
		// 该接口不返回图片数，pnum 留 0，取图后再补
		access[chapterID] = mc.Access{VIP: mc.Atoi(item.IsVip)}
	}
	return metas, access, nil
}

// ---- 章节详情 ---------------------------------------------------------------

// GetChapterDetail 章节详情；fetchImages 为 false 时不要求解析出图片。
func (c *BoyloveClient) GetChapterDetail(chapterID, comicID string, fetchImages bool) (*mc.Chapter, error) {
	chapterID = mc.ParseToID(chapterID)
	if chapterID == "" {
		return nil, mc.Errorf("章节 id 不能为空")
	}
	comicID = mc.ParseToID(comicID)

	pageHTML, err := c.getHTML(fmt.Sprintf(boylovePathReader, chapterID))
	if err != nil {
		return nil, err
	}
	page, err := c.parseReaderPage(pageHTML, chapterID)
	if err != nil {
		return nil, err
	}

	ch := &mc.Chapter{
		ChapterID: chapterID,
		ComicID:   comicID,
		Name:      page.Name,
		Index:     1,
		URL:       fmt.Sprintf(boylovePathReader, chapterID),
		Access:    mc.Access{VIP: page.IsVIP, Pnum: len(page.ImageURLs)},
		ScrambleN: page.ScrambleN,
	}
	if ch.Name == "" {
		ch.Name = "第" + chapterID + "话"
	}
	ch.SetImages(page.ImageURLs, page.ImageIDs)
	ch.Count = len(page.ImageURLs)

	// 有 comic_id 时顺带取该话在漫画里的序号与权益标记。
	// 这是锦上添花：取不到不应让整个请求失败（与参考实现一致）。
	if comicID != "" {
		metas, access, err := c.fetchChapterList(comicID)
		if err == nil {
			for _, m := range metas {
				if m.ChapterID != chapterID {
					continue
				}
				ch.Index = m.Index
				if m.Name != "" {
					ch.Name = m.Name
				}
				break
			}
			if a, ok := access[chapterID]; ok {
				// 章节列表的 isvip 是权威值，阅读页的 VIP 角标只作补充
				if a.Pnum == 0 {
					a.Pnum = len(page.ImageURLs)
				}
				ch.Access = a
			}
		}
	}

	if fetchImages && len(page.ImageURLs) == 0 {
		return nil, boyloveNoImageError(chapterID, comicID, page.IsVIP)
	}
	return ch, nil
}

// FetchImageURLs 取章节图片地址（会写入 chapter）。
func (c *BoyloveClient) FetchImageURLs(ch *mc.Chapter) ([]string, error) {
	if ch == nil {
		return nil, mc.Errorf("章节不能为空")
	}
	// 详情阶段已经解析过同一页，直接复用，避免为一个章节发两次请求
	if len(ch.ImageURLs) > 0 {
		return ch.ImageURLs, nil
	}

	chapterID := mc.ParseToID(ch.ChapterID)
	if chapterID == "" {
		return nil, mc.Errorf("章节 id 不能为空")
	}

	pageHTML, err := c.getHTML(fmt.Sprintf(boylovePathReader, chapterID))
	if err != nil {
		return nil, err
	}
	page, err := c.parseReaderPage(pageHTML, chapterID)
	if err != nil {
		return nil, err
	}
	if len(page.ImageURLs) == 0 {
		return nil, boyloveNoImageError(chapterID, ch.ComicID, page.IsVIP)
	}

	ch.SetImages(page.ImageURLs, page.ImageIDs)
	if page.ScrambleN > 0 {
		// 与参考实现一致：新值有效才覆盖，避免把已知的 N 抹成 0
		ch.ScrambleN = page.ScrambleN
	}
	if page.IsVIP > 0 {
		ch.Access.VIP = 1
	}
	if ch.Access.Pnum == 0 {
		ch.Access.Pnum = len(page.ImageURLs)
	}
	if ch.Name == "" {
		ch.Name = page.Name
	}
	if ch.ComicID == "" {
		ch.ComicID = mc.ParseToID(ch.ComicID)
	}
	if ch.URL == "" {
		ch.URL = fmt.Sprintf(boylovePathReader, chapterID)
	}
	return ch.ImageURLs, nil
}

// boyloveNoImageError 统一的「一张图都没解析到」错误。
func boyloveNoImageError(chapterID, comicID string, isVIP int) error {
	ctx := map[string]any{"chapter_id": chapterID}
	if comicID != "" {
		ctx["comic_id"] = comicID
	}
	if isVIP > 0 {
		ctx["vip"] = isVIP
	}
	return mc.NewError(fmt.Sprintf(
		"章节 [%s] 未解析到任何图片；该章节可能受权益保护（需要登录/会员权益），或页面结构已变化",
		chapterID), ctx)
}

// ---- 阅读页解析 -------------------------------------------------------------

// boyloveReaderPage 阅读页解析结果。
type boyloveReaderPage struct {
	Name      string
	ImageURLs []string
	ImageIDs  []string
	ScrambleN int
	IsVIP     int
}

// parseReaderPage 解析阅读页（章节名 + 图片 + 乱序参数 + 权益标记）。
func (c *BoyloveClient) parseReaderPage(pageHTML, chapterID string) (boyloveReaderPage, error) {
	var page boyloveReaderPage

	root, err := ParseHTML(pageHTML)
	if err != nil {
		return page, mc.Errorf("阅读页解析失败: chapter=%s：%v", chapterID, err)
	}

	page.Name = boyloveReaderName(pageHTML, root)

	// 图片有两个来源，优先用新管线的内联 imageData：
	// 它的顺序就是阅读顺序，而 DOM 里的 <img> 在新章节里是隐藏的（仅用于 canvas 还原）。
	if urls, ids := boyloveParseImageData(pageHTML); len(urls) > 0 {
		page.ImageURLs, page.ImageIDs = urls, ids
	} else {
		page.ImageURLs, page.ImageIDs = boyloveParseReaderImages(root, chapterID)
	}

	// 乱序参数只跟页面有关，与走哪个图片来源无关
	page.ScrambleN = boyloveParseScrambleN(pageHTML)

	if reBoyloveVIPMark.MatchString(pageHTML) {
		page.IsVIP = 1
	}
	return page, nil
}

// boyloveReaderName 取章节名。
//
// 阅读页的章节名在 <title> 的方括号里：
//
//	<title>[第01话] 无根树/无根之树 - 香香腐宅BoyLove│耽美漫畫…</title>
func boyloveReaderName(pageHTML string, root *html.Node) string {
	if m := reBoyloveTitleBracket.FindStringSubmatch(pageHTML); m != nil {
		if name := strings.TrimSpace(m[1]); name != "" {
			return name
		}
	}
	if m := reBoyloveTitlePlain.FindStringSubmatch(pageHTML); m != nil {
		if name := strings.TrimSpace(m[1]); name != "" {
			return name
		}
	}
	// 最后退化为页内标题节点
	for _, class := range []string{"read__title", "chapter-title"} {
		if n := FirstClassContains(root, class); n != nil {
			if name := strings.TrimSpace(Text(n)); name != "" {
				return name
			}
		}
	}
	return ""
}

// boyloveParseImageData 解析阅读页内联的图片清单：
//
//	var imageData = [{"id":0,"src":"https:\/\/img.boylove.cc\/bookimages\/…","width":649,"height":993}, …];
//
// 只声明真正需要的 id/src 两个字段：height 在实测数据里有时是数字、有时是字符串，
// 少声明字段就不会因为个别字段类型不一致导致整段解析失败。
// 另外 \/ 是合法 JSON 转义，encoding/json 会自行还原成 /，无需手工替换；
// 用 Decoder 而不是正则截数组，是为了避免 src 里出现 ] 时被截断。
func boyloveParseImageData(pageHTML string) ([]string, []string) {
	loc := reBoyloveImageDataAt.FindStringIndex(pageHTML)
	if loc == nil {
		return nil, nil
	}

	var raw []struct {
		ID  any    `json:"id"`
		Src string `json:"src"`
	}
	if err := json.NewDecoder(strings.NewReader(pageHTML[loc[1]:])).Decode(&raw); err != nil {
		return nil, nil
	}

	urls := make([]string, 0, len(raw))
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		src := strings.TrimSpace(item.Src)
		if src == "" {
			continue
		}
		urls = append(urls, boyloveAbsImage(src))
		ids = append(ids, mc.ToString(item.ID))
	}
	return urls, ids
}

// boyloveParseScrambleN 解析竖带数量 N。
//
// 服务端把正文图切成 N 条竖带后倒序下发（N 逐章不同，实测同一本漫画里出现过
// 11/13/18/23，绝不能写死）。还原逻辑由 internal/decode 在下载时按 ScrambleN
// 自动完成，这里只负责把 N 如实交给上层。
// 老章节没有 randomClass → 0，表示无需还原。
func boyloveParseScrambleN(pageHTML string) int {
	m := reBoyloveRandomClass.FindStringSubmatch(pageHTML)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 1 {
		// N<=1 等于「没切带」，同样归零。
		// 参考实现里还有一句 "'canvas' not in html and N<=1 → 0"，
		// 在此条件下（N 仅为 0 或 1）与上面等价，故省略。
		return 0
	}
	return n
}

// boyloveParseReaderImages 从 DOM 里取老章节的正文图。
//
// 阅读页混着推荐位缩略图（路径形如 /bookimages/img/20260910/xxx.jpg，
// 注意中间那个 img/ 目录），只按 img[data-original] 抓会把它们一起收进来，
// 所以正文图必须用「文件名以章节 id 开头」精确过滤（如 435648-xxxx.webp）。
//
// 取图分三层，前一层拿到就不再往下走：
//  1. 文件名以 {chapter_id}- 开头（最可靠，与参考实现一致）；
//  2. .rd-article__pic 容器内的图（老模板的正文区，容器不存在时自然为空）；
//  3. id 以 webp 开头的懒加载图（新管线没有 imageData 时的兜底）。
func boyloveParseReaderImages(root *html.Node, chapterID string) ([]string, []string) {
	var (
		preciseURLs, preciseIDs []string
		articleURLs, articleIDs []string
		fallbackURLs            []string
	)
	seen := map[string]bool{}
	add := func(urls *[]string, ids *[]string, url, id string) {
		if url == "" || seen[url] {
			return
		}
		seen[url] = true
		*urls = append(*urls, url)
		*ids = append(*ids, id)
	}

	for _, img := range ByTag(root, "img") {
		url := strings.TrimSpace(Attr(img, "data-original"))
		if url == "" {
			url = strings.TrimSpace(Attr(img, "src"))
		}
		if !strings.HasPrefix(url, "http") {
			continue
		}
		// 占位图/静态资源（load.png、lazyload_img、/static/…）不是正文
		if strings.Contains(url, "lazyload_img") || strings.Contains(url, "/static/") {
			continue
		}

		nodeID := Attr(img, "id")
		if strings.HasPrefix(nodeID, "webp") {
			fallbackURLs = append(fallbackURLs, url)
		}

		fileName := url
		if i := strings.IndexAny(fileName, "?#"); i >= 0 {
			fileName = fileName[:i]
		}
		if i := strings.LastIndex(fileName, "/"); i >= 0 {
			fileName = fileName[i+1:]
		}

		if chapterID != "" && strings.HasPrefix(fileName, chapterID+"-") {
			add(&preciseURLs, &preciseIDs, url, nodeID)
			continue
		}
		if boyloveInArticlePic(img) {
			add(&articleURLs, &articleIDs, url, nodeID)
		}
	}

	switch {
	case len(preciseURLs) > 0:
		return preciseURLs, preciseIDs
	case len(articleURLs) > 0:
		return articleURLs, articleIDs
	default:
		// 兜底层拿不到 id，长度对齐由 SetImages 负责
		urls := make([]string, 0, len(fallbackURLs))
		for _, u := range fallbackURLs {
			if seen[u] {
				continue
			}
			seen[u] = true
			urls = append(urls, u)
		}
		return urls, nil
	}
}

// boyloveInArticlePic 判断 img 是否位于阅读区容器 .rd-article__pic 内。
func boyloveInArticlePic(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if HasClass(p, "rd-article__pic") {
			return true
		}
	}
	return false
}

// ---- 浏览 -------------------------------------------------------------------

// CategoriesFilter 分类浏览。
//
// opts 支持的键（都取整数值）：cate 栏目、tag 标签、done 进度（0 连载/1 完结/2 全部）、
// order 排序（1 更新/2 人气…）、is18 是否 18+、vip 权益（0 免费/1 VIP/2 全部）；
// 同名别名 category/lanmu、tags、finish、pay 也认。
func (c *BoyloveClient) CategoriesFilter(page int, opts map[string]string) (*mc.SearchPage, error) {
	if page < 1 {
		page = 1
	}

	cate := boyloveOptInt(opts, 1, "cate", "category", "lanmu")
	tag := boyloveOptInt(opts, 0, "tag", "tags")
	done := boyloveOptInt(opts, 2, "done", "finish")
	order := boyloveOptInt(opts, 1, "order")
	is18 := boyloveOptInt(opts, 0, "is18")
	vip := boyloveOptInt(opts, 2, "vip", "pay")

	// 这个接口的要求很硬：路径必须拼满 8 段
	// {cate}-{tag}-{done}-{order}-{page}-{is18}-1-{vip}，少一段就 Server Error；
	// 第 7 段固定 1（站点旧版参数，删掉会报错），并且必须带 query mt=0。
	spec := fmt.Sprintf("%d-%d-%d-%d-%d-%d-1-%d", cate, tag, done, order, page, is18, vip)
	path := fmt.Sprintf(boylovePathCate, spec)

	raw, err := c.api(path, map[string]string{"mt": "0"}, map[string]any{
		"page": page,
		"spec": spec,
	})
	if err != nil {
		return nil, err
	}

	var result boyloveListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, boyloveDecodeError(path, err)
	}

	items := c.buildSummaries(result.List)
	return &mc.SearchPage{
		Items: items,
		Total: boylovePageTotal(len(items), page, boyloveTruthy(result.LastPage)),
		Page:  page,
		Site:  c.SiteName,
	}, nil
}

// UpdateList 最近更新：等价于「按更新时间排序」的分类列表（order=1）。
func (c *BoyloveClient) UpdateList(page int) (*mc.SearchPage, error) {
	return c.CategoriesFilter(page, map[string]string{"order": "1"})
}

// ---- 榜单 -------------------------------------------------------------------

// Ranking 排行榜。
//
// 接口一次返回四组榜单（most_clicks / most_consumes / most_favorites / most_search），
// 路径里的 {n} 是统计周期。rankType 因此支持两种写法：
//   - RankingNav 返回的分组键或中文名 → 用默认周期，取对应分组；
//   - 纯数字（1/2…，对齐参考实现的 ranking(rank_type)）→ 作为路径段，取主榜单（人气榜）。
func (c *BoyloveClient) Ranking(rankType string, page int) (*mc.SearchPage, error) {
	if page < 1 {
		page = 1
	}

	pathType, group := boyloveResolveRank(rankType)
	path := fmt.Sprintf(boylovePathRank, pathType)

	raw, err := c.api(path, nil, map[string]any{"rank_type": rankType, "group": group})
	if err != nil {
		return nil, err
	}

	var result boyloveRankResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, boyloveDecodeError(path, err)
	}

	// 榜单是一次性返回的定长列表，没有分页参数，page 只用于回填结果
	items := c.buildSummaries(result.pick(group))
	return &mc.SearchPage{
		Items: items,
		Total: len(items),
		Page:  page,
		Site:  c.SiteName,
	}, nil
}

// boyloveResolveRank 把 rankType 解析成「路径段 + 榜单分组」。
func boyloveResolveRank(rankType string) (string, string) {
	rt := strings.TrimSpace(rankType)
	if rt == "" {
		return "1", boyloveRankClicks
	}

	switch rt {
	case boyloveRankClicks, "人气榜":
		return "1", boyloveRankClicks
	case boyloveRankConsumes, "消费榜":
		return "1", boyloveRankConsumes
	case boyloveRankFavorites, "收藏榜":
		return "1", boyloveRankFavorites
	case boyloveRankSearch, "搜索榜":
		return "1", boyloveRankSearch
	}

	if _, err := strconv.Atoi(rt); err == nil {
		return rt, boyloveRankClicks
	}
	// 未知取值原样透传给路径：站点若新增统计周期，调用方无需改本文件
	return rt, boyloveRankClicks
}

// RankingNav 榜单导航。
//
// 分组是站点固定的四组，所以即使接口暂时不可用也照常返回，
// 避免整个榜单入口因为一次网络抖动而消失。
func (c *BoyloveClient) RankingNav() ([]mc.RankNav, error) {
	path := fmt.Sprintf(boylovePathRank, "1")

	nav := make([]mc.RankNav, 0, len(boyloveRankGroups))
	if raw, err := c.api(path, nil, nil); err == nil {
		var result boyloveRankResult
		if json.Unmarshal(raw, &result) == nil {
			for _, g := range boyloveRankGroups {
				if result.has(g.Type) {
					nav = append(nav, mc.RankNav{Type: g.Type, Name: g.Name})
				}
			}
		}
	}
	if len(nav) == 0 {
		for _, g := range boyloveRankGroups {
			nav = append(nav, mc.RankNav{Type: g.Type, Name: g.Name})
		}
	}
	return nav, nil
}

// ---- URL 解析 ---------------------------------------------------------------

// GetComicIDFromURL 从 URL 解析漫画 id（/home/book/index/id/{id}）。
func (c *BoyloveClient) GetComicIDFromURL(u string) string {
	return mc.ParseComicIDFromURL(u)
}

// GetChapterIDFromURL 从 URL 解析章节 id（/home/book/capter/id/{id}）。
func (c *BoyloveClient) GetChapterIDFromURL(u string) string {
	return mc.ParseChapterIDFromURL(u)
}
