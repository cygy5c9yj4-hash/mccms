package client

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/mccms/mccms-go/internal/mc"
)

// E-Hentai 站点客户端。
//
// 数据模型映射
//------------
// E-Hentai 的「画廊」没有章节层级：一个画廊就是一部作品，画廊里的每张图就是一章的每一页。
// 因此这里把**画廊当作漫画**，并给它一个**唯一的章节**（章节 id 与画廊相同），
// 章节的图片就是画廊的全部页面。
//
// 只收 yaoi
//----------
// 站点的搜索**不能**可靠地按标签过滤：实测 `f_search=male:yaoi`（含加引号的精确写法）
// 返回的第一条仍然是一个没有 yaoi 标签的置顶条目，前 6 条里只有 3 条真的带 yaoi 标签。
// 所以这里一律**在本地按标签严格复核**：只有标签里存在 `yaoi`（含 `male:yaoi` 这类
// 带命名空间的写法）才保留，其余全部丢弃。
//
// 同时硬性排除涉及未成年人的标签（lolicon / shotacon / toddlercon / lolita 等）——
// 这类标签在同类站点上常与 yaoi 同时出现，必须在入口处挡掉。
//
// 图片直链是延迟解析的
//------------------
// 列表页给的是**图片页**地址（`/s/{hash}/{gid}-{n}`），真正的图片直链要再访问一次
// 图片页、从 `#img` 里取。一个画廊可能有上百张图，逐张预解析代价太高，
// 因此本客户端实现 ImageResolver，由下载器与图片代理在真正需要某一张时再解析（带缓存）。

const (
	ehBase = "https://e-hentai.org"

	// ehYaoiTag 目标标签。站点的 yaoi 标签带 male 命名空间。
	ehYaoiTag = "male:yaoi"

	// ehAPIURL 元信息接口。一次最多查 25 个画廊，返回**完整**标签列表。
	ehAPIURL = "https://api.e-hentai.org/api.php"

	// ehAPIBatch 单次 API 请求的最大画廊数（站点限制）。
	ehAPIBatch = 25
)

// ehBlockedTagParts 涉及未成年人的标签片段，命中即整条丢弃。
var ehBlockedTagParts = []string{
	"lolicon", "shotacon", "toddlercon", "lolita", "loli", "shota",
}

var (
	reEHShowing  = regexp.MustCompile(`Showing\s+([\d,]+)\s*-\s*([\d,]+)\s+of\s+([\d,]+)`)
	reEHPages    = regexp.MustCompile(`(\d+)\s+pages`)
	reEHPageHref = regexp.MustCompile(`/s/[0-9a-f]+/(\d+)-(\d+)`)
	reEHGallery  = regexp.MustCompile(`/g/(\d+)/([0-9a-f]+)`)
	reEHFound    = regexp.MustCompile(`Found\s+about\s+([\d,]+)\s+results`)
)

// EhentaiClient E-Hentai 客户端。
type EhentaiClient struct {
	Base

	mu       sync.Mutex
	chapters map[string]*ehChapter // 章节缓存：章节 id -> 页面列表与已解析直链
}

type ehPage struct {
	No      int    // 页码，从 1 开始
	Name    string // 图片标识，形如 4223931-1
	PageURL string // 图片页地址
	Token   string // 页面 token（图片页 URL 里那段十六进制）
}

type ehChapter struct {
	ComicID  string
	Title    string
	GID      string
	Token    string
	Pages    []ehPage
	Resolved map[string]string // 图片标识 -> 图片直链
}

// NewEhentai 创建客户端。
func NewEhentai(base Base) *EhentaiClient {
	return &EhentaiClient{
		Base:     base,
		chapters: map[string]*ehChapter{},
	}
}

// ---- 只收 yaoi 的过滤 -------------------------------------------------------

// tagIsYaoi 判断单个标签是否为 yaoi（兼容 `yaoi` 与 `male:yaoi` 两种写法）。
func tagIsYaoi(tag string) bool {
	t := strings.ToLower(strings.TrimSpace(tag))
	return t == "yaoi" || strings.HasSuffix(t, ":yaoi")
}

// tagIsBlocked 判断标签是否命中未成年人相关内容黑名单。
func tagIsBlocked(tag string) bool {
	t := strings.ToLower(strings.TrimSpace(tag))
	for _, bad := range ehBlockedTagParts {
		if strings.Contains(t, bad) {
			return true
		}
	}
	return false
}

// keepGallery 决定一个画廊是否收录：必须带 yaoi 标签，且不含被排除的标签。
func keepGallery(tags []string) bool {
	hasYaoi := false
	for _, t := range tags {
		if tagIsBlocked(t) {
			return false
		}
		if tagIsYaoi(t) {
			hasYaoi = true
		}
	}
	return hasYaoi
}

// filterSummaries 对列表结果做统一的 yaoi 过滤。
func filterSummaries(items []mc.ComicSummary) []mc.ComicSummary {
	out := make([]mc.ComicSummary, 0, len(items))
	for _, it := range items {
		if keepGallery(it.Tags) {
			out = append(out, it)
		}
	}
	return out
}

// ---- 列表解析 ---------------------------------------------------------------

// parseEHListPage 解析搜索结果 / 首页列表中的条目。
//
// 结构（实测）：
//
//	<tr>
//	  <td class="gl1c glcat"><div class="cn ct1">Doujinshi</div></td>
//	  <td class="gl2c"><div class="glthumb"><img src="封面"><div>35 pages</div></div></td>
//	  <td class="gl3c glname">
//	    <a href="https://e-hentai.org/g/4223931/37d6418440/">
//	      <div class="glink">标题</div>
//	      <div class="gt" title="male:yaoi">m:yaoi</div> ...
//	    </a>
//	  </td>
//	</tr>
func parseEHListPage(htmlText string) ([]mc.ComicSummary, error) {
	doc, err := ParseHTML(htmlText)
	if err != nil {
		return nil, err
	}

	var out []mc.ComicSummary
	for _, tr := range ByTag(doc, "tr") {
		nameCell := FirstByTagClass(tr, "td", "glname")
		if nameCell == nil {
			continue
		}

		anchor := FirstByTag(nameCell, "a")
		href := Attr(anchor, "href")
		m := reEHGallery.FindStringSubmatch(href)
		if m == nil {
			continue
		}
		gid, token := m[1], m[2]

		title := Text(FirstByClass(nameCell, "glink"))
		if title == "" {
			title = mc.Unescape(Attr(FirstByTag(tr, "img"), "alt"))
		}

		// 标签：搜索页写成 .gt[title="命名空间:标签"]
		var tags []string
		for _, gt := range ByClass(nameCell, "gt") {
			if t := Attr(gt, "title"); t != "" {
				tags = append(tags, t)
			}
		}
		if len(tags) == 0 {
			continue // 拿不到标签就无法保证「只收 yaoi」，宁可丢掉
		}

		cover := Attr(FirstByTag(tr, "img"), "src")
		category := ""
		if cn := FirstByClass(tr, "cn"); cn != nil {
			category = Text(cn)
		}

		pages := 0
		if pm := reEHPages.FindStringSubmatch(Text(tr)); pm != nil {
			pages, _ = strconv.Atoi(pm[1])
		}

		out = append(out, mc.ComicSummary{
			ID:    gid + "-" + token,
			Name:  mc.Unescape(title),
			Cover: cover,
			Tags:  tags,
			Site:  mc.SiteEhentai,
			URL:   href,
			Slug:  category,
			Text:  category,
			Views: pages,
		})
	}
	return out, nil
}

// parseEHFound 取搜索页的「Found about 141,279 results」。
//
// 注意：这是**站点侧**的匹配总数，未经过本地的 yaoi 复核，
// 因此它比实际返回的条目数大，只作参考。
func parseEHFound(htmlText string) int {
	m := reEHFound.FindStringSubmatch(htmlText)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	return n
}

// parseEHShowing 取「Showing 1 - 20 of 118」。
func parseEHShowing(htmlText string) (from, to, total int, ok bool) {
	m := reEHShowing.FindStringSubmatch(htmlText)
	if m == nil {
		return 0, 0, 0, false
	}
	atoi := func(s string) int {
		n, _ := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
		return n
	}
	return atoi(m[1]), atoi(m[2]), atoi(m[3]), true
}

// ---- 元信息补全（判定 yaoi 的唯一可靠途径）---------------------------------
//
// 站点搜索结果只展示**部分**标签：实测某画廊真实有 19~33 个标签，
// 搜索页只列其中 12 个。仅凭搜索页标签判断会出现两种错误：
//   - 误杀：真 yaoi 画廊因为展示的 12 个标签里没有 yaoi 而被丢掉（实测丢掉 40%）
//   - 漏放：带 shotacon 等被排除标签的画廊因为没展示出来而被收进来
// 所以这里统一再查一次 gdata API 拿完整标签。
//
// **判不出来就不收录**（fail-closed）：API 没返回的画廊一律丢弃，
// 宁可少收，也不能破坏「只收 yaoi」这个约束。

// 站点的元信息接口字段类型不稳定：实测同一份响应里 `filesize` 是数字，
// 而 `posted` / `filecount` 是字符串。这里统一用容错类型接。
type ehFlexInt int

func (f *ehFlexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = ehFlexInt(v)
	}
	return nil
}

type ehFlexFloat float64

func (f *ehFlexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = ehFlexFloat(v)
	}
	return nil
}

type ehFlexBool bool

func (f *ehFlexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	*f = ehFlexBool(s == "true" || s == "1")
	return nil
}

type ehAPIMeta struct {
	GID       int         `json:"gid"`
	Token     string      `json:"token"`
	Title     string      `json:"title"`
	TitleJPN  string      `json:"title_jpn"`
	Category  string      `json:"category"`
	Thumb     string      `json:"thumb"`
	Uploader  string      `json:"uploader"`
	Posted    ehFlexInt   `json:"posted"`
	FileCount ehFlexInt   `json:"filecount"`
	Rating    ehFlexFloat `json:"rating"`
	Expunged  ehFlexBool  `json:"expunged"`
	Tags      []string    `json:"tags"`
}

// enrichViaAPI 查询完整元信息，并据此做 yaoi 复核。
func (c *EhentaiClient) enrichViaAPI(items []mc.ComicSummary) []mc.ComicSummary {
	if len(items) == 0 {
		return nil
	}

	// 收集 gid/token
	type key struct {
		gid   int
		token string
	}
	gidlist := make([][]any, 0, len(items))
	keys := make([]key, 0, len(items))
	for _, it := range items {
		gid, token, err := parseEHComicID(it.ID)
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(gid)
		gidlist = append(gidlist, []any{n, token})
		keys = append(keys, key{gid: n, token: token})
	}
	if len(gidlist) == 0 {
		return nil
	}

	meta := map[string]ehAPIMeta{}
	for start := 0; start < len(gidlist); start += ehAPIBatch {
		end := start + ehAPIBatch
		if end > len(gidlist) {
			end = len(gidlist)
		}

		resp, err := c.HTTP.PostJSON(ehAPIURL, map[string]any{
			"method":    "gdata",
			"gidlist":   gidlist[start:end],
			"namespace": 1,
		})
		if err != nil || !resp.OK() {
			// 拿不到完整标签时不做任何猜测，直接放弃这一批
			return nil
		}

		var parsed struct {
			Metadata []ehAPIMeta `json:"gmetadata"`
		}
		if err := json.Unmarshal(resp.Body, &parsed); err != nil {
			return nil
		}
		for _, m := range parsed.Metadata {
			meta[fmt.Sprintf("%d-%s", m.GID, m.Token)] = m
		}
	}

	out := make([]mc.ComicSummary, 0, len(items))
	for _, it := range items {
		m, okk := meta[it.ID]
		if !okk {
			continue // 查不到 -> 不收录
		}
		if bool(m.Expunged) {
			continue // 已被站点移除
		}
		if !keepGallery(m.Tags) {
			continue // 不是 yaoi，或命中排除名单
		}

		summary := it
		if m.Title != "" {
			summary.Name = mc.Unescape(m.Title)
		}
		if m.Thumb != "" {
			summary.Cover = m.Thumb
		}
		if m.Uploader != "" {
			summary.Author = mc.Unescape(m.Uploader)
		}
		summary.Tags = m.Tags
		summary.Views = int(m.FileCount)
		summary.Score = float64(m.Rating)
		summary.Text = m.Category
		summary.Slug = m.Category
		out = append(out, summary)
	}
	_ = keys
	return out
}

// ---- 接口实现 ---------------------------------------------------------------

// Search 搜索。站点搜索本身不可靠，结果一律再做一次本地 yaoi 复核。
func (c *EhentaiClient) Search(keyword string, page int) (*mc.SearchPage, error) {
	query := ehYaoiTag
	if kw := strings.TrimSpace(keyword); kw != "" {
		query = ehYaoiTag + " " + kw
	}
	return c.searchWith(query, page, nil)
}

// HotList 「热门」：5 星以上的 yaoi。
//
// 阈值用 5 而不是 4：实测 4 星以上（11 万条）的首屏与「最近更新」高度重叠，
// 5 星以上（8 千多条）首屏与无筛选结果**零重叠**，才是真正不同的内容。
func (c *EhentaiClient) HotList() (*mc.SearchPage, error) {
	return c.searchWith(ehYaoiTag, 1, map[string]string{"f_srdd": "5"})
}

// UpdateList 最近更新：按标签检索，站点默认按收录时间倒序。
func (c *EhentaiClient) UpdateList(page int) (*mc.SearchPage, error) {
	return c.searchWith(ehYaoiTag, page, nil)
}

// CategoriesFilter 按分类浏览。
//
// opts 支持 category（分类 key，见 ehCategories）；站点用 f_cats 位掩码表示
// **要排除**的分类，因此这里取全集的补集。
func (c *EhentaiClient) CategoriesFilter(page int, opts map[string]string) (*mc.SearchPage, error) {
	extra := map[string]string{}
	if key := opts["category"]; key != "" && key != "0" {
		if bit, okk := ehCategories[key]; okk {
			extra["f_cats"] = strconv.Itoa(ehAllCats &^ bit)
		}
	}
	return c.searchWith(ehYaoiTag, page, extra)
}

// ehCategories 分类 key -> E-Hentai 分类位。
var ehCategories = map[string]int{
	"doujinshi": 2,
	"manga":     4,
	"artistcg":  8,
	"gamecg":    16,
	"imageset":  32,
	"cosplay":   64,
	"western":   512,
	"nonh":      256,
	"asianporn": 128,
	"misc":      1,
}

// ehAllCats 全部分类位之和。
const ehAllCats = 1 | 2 | 4 | 8 | 16 | 32 | 64 | 128 | 256 | 512

func (c *EhentaiClient) searchWith(query string, page int, extra map[string]string) (*mc.SearchPage, error) {
	if page < 1 {
		page = 1
	}

	q := map[string]string{
		"f_search": query,
	}
	// 站点的 page 参数从 0 开始
	if page > 1 {
		q["page"] = strconv.Itoa(page - 1)
	}
	for k, v := range extra {
		q[k] = v
	}

	resp, err := c.GetWith("/", q)
	if err != nil {
		return nil, err
	}

	items, err := parseEHListPage(resp.Text())
	if err != nil {
		return nil, err
	}

	total := parseEHFound(resp.Text())
	if total == 0 {
		_, _, total, _ = parseEHShowing(resp.Text())
	}
	kept := c.enrichViaAPI(items)

	return &mc.SearchPage{
		Items: kept,
		Total: total,
		Page:  page,
		Site:  mc.SiteEhentai,
	}, nil
}

// GetComicDetail 画廊详情。章节列表固定只有一项：画廊本身。
func (c *EhentaiClient) GetComicDetail(comicID string, fetchChapters bool) (*mc.Comic, error) {
	gid, token, err := parseEHComicID(comicID)
	if err != nil {
		return nil, err
	}

	resp, err := c.Get(fmt.Sprintf("/g/%s/%s/", gid, token))
	if err != nil {
		return nil, err
	}
	htmlText := resp.Text()

	doc, err := ParseHTML(htmlText)
	if err != nil {
		return nil, err
	}

	title := mc.Unescape(Text(FirstByAttr(doc, "id", "gn")))
	if title == "" {
		title = mc.Unescape(strings.TrimSuffix(TitleText(doc), " - E-Hentai Galleries"))
	}

	tags := parseEHTags(doc)

	// 只收 yaoi：详情页也要复核，避免通过直链访问到非目标内容
	if !keepGallery(tags) {
		return nil, mc.RaiseAccessError(
			fmt.Sprintf("画廊 [%s] 不属于本源的收录范围（仅收录 yaoi）", title), nil, 3, "vip")
	}

	comic := &mc.Comic{
		ComicID:   gid + "-" + token,
		Name:      title,
		Tags:      tags,
		Site:      mc.SiteEhentai,
		URL:       fmt.Sprintf("%s/g/%s/%s/", ehBase, gid, token),
		Serialize: "完结",
	}

	if up := FirstByAttr(doc, "id", "gdn"); up != nil {
		if a := FirstByTag(up, "a"); a != nil {
			comic.Authors = []string{mc.Unescape(Text(a))}
		}
	}
	if comic.Authors == nil {
		comic.Authors = []string{mc.DefaultAuthor}
	}

	if cover := FirstByAttr(doc, "id", "gd1"); cover != nil {
		if img := FirstByTag(cover, "img"); img != nil {
			comic.Cover = Attr(img, "src")
		}
	}

	// 页数决定章节的图片数量
	_, _, total, _ := parseEHShowing(htmlText)
	if total == 0 {
		if pm := reEHPages.FindStringSubmatch(htmlText); pm != nil {
			total, _ = strconv.Atoi(pm[1])
		}
	}

	if fetchChapters {
		chapter := mc.ChapterMeta{
			ChapterID: gid + "-" + token,
			Index:     1,
			Name:      fmt.Sprintf("全一册（%d 页）", total),
		}
		comic.Chapters = []mc.ChapterMeta{chapter}
		comic.ChapterAccess = map[string]mc.Access{
			chapter.ChapterID: {},
		}
	}

	return comic, nil
}

// parseEHTags 解析详情页标签清单。
//
// 结构（实测）：
//
//	<div id="td_male:yaoi" class="gt"><a id="ta_male:yaoi" href="...">yaoi</a></div>
//
// 展示文本只有 `yaoi`，完整的「命名空间:标签」在 id 属性里（去掉 ta_ 前缀）。
func parseEHTags(doc *html.Node) []string {
	var tags []string
	for _, a := range ByTag(doc, "a") {
		id := Attr(a, "id")
		if !strings.HasPrefix(id, "ta_") {
			continue
		}
		if tag := strings.TrimPrefix(id, "ta_"); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// GetChapterDetail 章节详情。章节 id 与画廊 id 相同。
func (c *EhentaiClient) GetChapterDetail(chapterID, comicID string, fetchImages bool) (*mc.Chapter, error) {
	gid, token, err := parseEHComicID(chapterID)
	if err != nil {
		return nil, err
	}

	cached, err := c.loadPages(gid, token)
	if err != nil {
		return nil, err
	}

	chapter := &mc.Chapter{
		ChapterID: gid + "-" + token,
		ComicID:   gid + "-" + token,
		Name:      fmt.Sprintf("全一册（%d 页）", len(cached.Pages)),
		Index:     1,
		Count:     len(cached.Pages),
		URL:       fmt.Sprintf("%s/g/%s/%s/", ehBase, gid, token),
	}

	if comicID != "" && comicID != chapterID {
		if comic, cerr := c.GetComicDetail(comicID, false); cerr == nil {
			chapter.FromComic = comic
		}
	}

	if fetchImages {
		urls := make([]string, 0, len(cached.Pages))
		ids := make([]string, 0, len(cached.Pages))
		for _, p := range cached.Pages {
			urls = append(urls, p.PageURL)
			ids = append(ids, p.Name)
		}
		chapter.SetImages(urls, ids)
	}

	return chapter, nil
}

// FetchImageURLs 返回图片**页**地址。
//
// 这里刻意不解析真实直链：一个画廊可能上百张图，逐张访问图片页代价太高。
// 真正的直链由 ResolveImage 在需要时解析（下载器与图片代理都会调用）。
func (c *EhentaiClient) FetchImageURLs(ch *mc.Chapter) ([]string, error) {
	gid, token, err := parseEHComicID(ch.ChapterID)
	if err != nil {
		return nil, err
	}

	cached, err := c.loadPages(gid, token)
	if err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(cached.Pages))
	ids := make([]string, 0, len(cached.Pages))
	for _, p := range cached.Pages {
		urls = append(urls, p.PageURL)
		ids = append(ids, p.Name)
	}
	ch.SetImages(urls, ids)
	return urls, nil
}

// ResolveImage 把图片标识解析成真实直链（实现 ImageResolver）。
func (c *EhentaiClient) ResolveImage(chapterID, name string) (string, error) {
	gid, token, err := parseEHComicID(chapterID)
	if err != nil {
		return "", err
	}

	// 下载器传进来的 name 可能带了推断出来的后缀，这里统一剥掉
	key := stripExt(name)

	cached, err := c.loadPages(gid, token)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	if url, okk := cached.Resolved[key]; okk {
		c.mu.Unlock()
		return url, nil
	}
	c.mu.Unlock()

	var target *ehPage
	for i := range cached.Pages {
		if cached.Pages[i].Name == key {
			target = &cached.Pages[i]
			break
		}
	}
	if target == nil {
		return "", mc.Errorf("画廊 [%s] 中找不到图片 [%s]", chapterID, name)
	}

	resp, err := c.Get(target.PageURL)
	if err != nil {
		return "", err
	}

	direct, err := parseEHImagePage(resp.Text())
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	cached.Resolved[key] = direct
	c.mu.Unlock()

	return direct, nil
}

// ---- 内部工具 ---------------------------------------------------------------

// loadPages 拉取画廊的全部缩略图页，得到完整页面列表（带缓存）。
func (c *EhentaiClient) loadPages(gid, token string) (*ehChapter, error) {
	key := gid + "-" + token

	c.mu.Lock()
	if ch, okk := c.chapters[key]; okk {
		c.mu.Unlock()
		return ch, nil
	}
	c.mu.Unlock()

	ch := &ehChapter{
		ComicID:  key,
		GID:      gid,
		Token:    token,
		Resolved: map[string]string{},
	}

	const perPage = 20 // 站点默认每页缩略图数

	for page := 0; page < 500; page++ { // 上限兜底，避免异常情况下死循环
		q := map[string]string{}
		if page > 0 {
			q["p"] = strconv.Itoa(page)
		}

		resp, err := c.GetWith(fmt.Sprintf("/g/%s/%s/", gid, token), q)
		if err != nil {
			return nil, err
		}
		htmlText := resp.Text()

		doc, err := ParseHTML(htmlText)
		if err != nil {
			return nil, err
		}

		// 章节标题（后续用于展示）
		if ch.Title == "" {
			ch.Title = mc.Unescape(Text(FirstByAttr(doc, "id", "gn")))
		}

		gdt := FirstByAttr(doc, "id", "gdt")
		if gdt == nil {
			if page == 0 {
				return nil, mc.Errorf("画廊 [%s] 没有可用页面（可能已被删除或需要登录）", key)
			}
			break
		}

		for _, a := range ByTag(gdt, "a") {
			href := Attr(a, "href")
			m := reEHPageHref.FindStringSubmatch(href)
			if m == nil {
				continue
			}
			no, _ := strconv.Atoi(m[2])

			name := fmt.Sprintf("%s-%d", m[1], no)
			ch.Pages = append(ch.Pages, ehPage{
				No:      no,
				Name:    name,
				PageURL: href,
				Token:   m[1],
			})
		}

		from, to, total, okk := parseEHShowing(htmlText)
		_ = from
		if !okk || to >= total || len(ch.Pages) == 0 {
			break
		}
		if len(ch.Pages) < page*perPage {
			break // 没有新增，说明已经到底
		}
	}

	if len(ch.Pages) == 0 {
		return nil, mc.Errorf("画廊 [%s] 没有解析到任何页面", key)
	}

	sort.SliceStable(ch.Pages, func(i, j int) bool { return ch.Pages[i].No < ch.Pages[j].No })

	c.mu.Lock()
	c.chapters[key] = ch
	c.mu.Unlock()

	return ch, nil
}

// parseEHImagePage 从图片页取真实直链。
func parseEHImagePage(htmlText string) (string, error) {
	doc, err := ParseHTML(htmlText)
	if err != nil {
		return "", err
	}

	img := FirstByAttr(doc, "id", "img")
	if img == nil {
		low := strings.ToLower(htmlText)
		switch {
		case strings.Contains(low, "image limit"), strings.Contains(low, "exceeded your image"):
			return "", mc.Errorf("E-Hentai 图片配额已用尽（匿名访问有配额限制），请稍后再试或配置 -cookie")
		default:
			return "", mc.Errorf("图片页解析失败：没有找到 #img")
		}
	}

	src := Attr(img, "src")
	if src == "" {
		return "", mc.Errorf("图片页没有给出图片地址")
	}
	// 配额耗尽时站点会返回 509 占位图
	if strings.Contains(src, "509.gif") {
		return "", mc.Errorf("E-Hentai 图片配额已用尽（返回 509 占位图）")
	}
	return src, nil
}

// parseEHComicID 解析 `{gid}-{token}` 或完整 URL。
func parseEHComicID(s string) (gid, token string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", mc.Errorf("画廊 id 不能为空")
	}

	if m := reEHGallery.FindStringSubmatch(s); m != nil {
		return m[1], m[2], nil
	}

	if i := strings.Index(s, "-"); i > 0 {
		gid, token = s[:i], s[i+1:]
		if gid != "" && token != "" {
			return gid, token, nil
		}
	}

	return "", "", mc.Errorf("画廊 id 格式应为 {gid}-{token}，实际为 [%s]", s)
}

func stripExt(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		if ext := name[i:]; len(ext) <= 6 {
			return name[:i]
		}
	}
	return name
}

// GetComicIDFromURL 从 URL / 文本中解析画廊 id。
//
// E-Hentai 的画廊 id 需要 gid 与 token 两部分，缺一不可，
// 所以这里返回 `{gid}-{token}` 而不是单纯的数字。
func (c *EhentaiClient) GetComicIDFromURL(u string) string {
	if m := reEHGallery.FindStringSubmatch(u); m != nil {
		return m[1] + "-" + m[2]
	}
	// 也接受已经是 `{gid}-{token}` 形式的输入
	if gid, token, err := parseEHComicID(u); err == nil {
		return gid + "-" + token
	}
	return ""
}

// GetChapterIDFromURL 章节 id 与画廊 id 相同。
func (c *EhentaiClient) GetChapterIDFromURL(u string) string {
	return c.GetComicIDFromURL(u)
}
