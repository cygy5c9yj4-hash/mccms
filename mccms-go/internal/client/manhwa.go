package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/mccms/mccms-go/internal/mc"
)

// 漫蛙（www.manhwa.wang）站点客户端。
//
// 该站是原生 Mccms PC 模板，接口是 JSON + HTML 混合的：
//
//	章节列表  GET /index.php/api/comic/chapter?mid={漫画数字 id}        JSON
//	热门列表  GET /index.php/api/comic/hot                             JSON
//	章节取图  GET /index.php/api/comic/isbuy?id={章节 id}               JSON（权限见下）
//	漫画详情  GET /index.php/comic/{数字 id 或 slug}                    HTML
//	阅读页    GET /index.php/chapter/{章节 id}                          HTML
//	搜索      GET /index.php/search/{关键字}/{页码}                      HTML（路径式分页）
//	分类      GET /index.php/category/order/{order}[/...]/list/{页码}    HTML
//
// 权限语义（实测，本实现只做映射，不做任何绕过访问控制的尝试）：
//
//	isbuy → {"code":1,"pic":[{id,img}]}  当前会话确有权限，正常返回图片
//	isbuy → {"code":2,"msg":"登录超时"}    未登录，映射为「需要登录」
//	isbuy → {"code":3,"type":"vip"}      已登录但缺会员权益，映射为「需要会员」
//	isbuy → {"code":3,"type":"cion"}     已登录但缺金币，映射为「需要会员」
//
// 取图顺序固定为：先读阅读页 HTML 里的内联图片（免费章节直接直出），只有为空才问 isbuy。
type ManhwaClient struct {
	Base

	// readerHTML 缓存已经抓过的阅读页 HTML。
	//
	// 为什么要有它：GetChapterDetail 要先抓阅读页解析元信息，紧接着取图又要用同一份
	// HTML（内联图片就在里面）；不缓存就会对同一话发两次请求。
	// 章节对象上没有可放 HTML 的字段（mc.Chapter 是共享实体），所以在客户端侧按章节 id 缓存。
	mu         sync.Mutex
	readerHTML map[string]string
}

// 站点路径模板。
const (
	manhwaPathChapterList = "/index.php/api/comic/chapter"
	manhwaPathIsBuy       = "/index.php/api/comic/isbuy"
	manhwaPathHot         = "/index.php/api/comic/hot"
	manhwaPathSearch      = "/index.php/search/%s/%d"
	manhwaPathCategory    = "/index.php/category/order/%s"
	manhwaPathComic       = "/index.php/comic/%s"
	manhwaPathReader      = "/index.php/chapter/%s"
	// 登录走 Mccms 通用接口（与站点无关的那一套）
	manhwaPathLogin = "/index.php/api/user/login"
)

// 分类排序与分页常量。
const (
	manhwaOrderHits    = "hits"    // 人气
	manhwaOrderAddtime = "addtime" // 更新
)

// readerHTMLCacheMax 阅读页缓存上限。
//
// 超过就整体丢弃：这只是为了避免同一次「详情 → 取图」重复请求，
// 不做长期缓存，免得批量下载时把整本书的 HTML 都留在内存里。
const manhwaReaderCacheMax = 32

// NewManhwa 构造漫蛙客户端。
func NewManhwa(base Base) *ManhwaClient {
	return &ManhwaClient{Base: base, readerHTML: map[string]string{}}
}

// 编译期断言：漫蛙必须满足 Client 与 HotLister。
var (
	_ Client    = (*ManhwaClient)(nil)
	_ HotLister = (*ManhwaClient)(nil)
)

var (
	// readPic(mid,cid,vip,cion)：漫画数字 id 与本章权限标记只在这段内联 JS 里出现
	reManhwaReadPic = regexp.MustCompile(`readPic\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)`)
	// 搜索页的「搜索结果（3）」总数，注意是全角括号
	reManhwaSearchTotal = regexp.MustCompile(`（(\d+)）`)
	// 分页控件里只有页码，没有总数
	reManhwaDigits = regexp.MustCompile(`\d+`)
)

// ============================================================== 搜索 / 列表

// Search 按关键字搜索。
//
// 分页只认路径式写法：`/index.php/search/{key}/{page}`。
// 实测 `?key=x&page=2`、`?key=x&p=2`、`/search/{key}/page/2` 都翻不了页，
// 所以这里永远拼路径，不拼 query。
func (c *ManhwaClient) Search(keyword string, page int) (*mc.SearchPage, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, mc.Errorf("搜索关键字不能为空")
	}
	if page < 1 {
		page = 1
	}

	resp, err := c.Get(fmt.Sprintf(manhwaPathSearch, manhwaQuote(keyword), page))
	if err != nil {
		return nil, err
	}
	root, err := ParseHTML(resp.Text())
	if err != nil {
		return nil, mc.Errorf("解析搜索页失败: keyword=%s page=%d err=%v", keyword, page, err)
	}

	items := manhwaParseCards(root, c.Site())
	total := manhwaSearchTotal(root)
	if total == 0 {
		// 页面偶尔没渲染总数：按站点每页 30 条估算（页满则认为是完整页）
		if len(items) >= mc.PageSizeDefault {
			total = page * mc.PageSizeDefault
		} else {
			total = (page-1)*mc.PageSizeDefault + len(items)
		}
	}

	return &mc.SearchPage{Items: items, Total: total, Page: page, Site: c.Site()}, nil
}

// CategoriesFilter 分类浏览。
//
// opts 支持：order（默认 hits 人气，可传 addtime 更新）、finish（1 完结 / 2 连载）、
// pay（1 免费 / 2 付费）、tags / quality / copyright（各维度 id）。
// 片段顺序必须与站点路由一致，站点是按固定顺序解析这些可选段的。
func (c *ManhwaClient) CategoriesFilter(page int, opts map[string]string) (*mc.SearchPage, error) {
	if page < 1 {
		page = 1
	}

	order := strings.TrimSpace(opts["order"])
	if order == "" {
		order = manhwaOrderHits
	}

	path := fmt.Sprintf(manhwaPathCategory, manhwaQuote(order))
	for _, key := range []string{"finish", "pay", "tags", "quality", "copyright"} {
		// 值为空视为「不筛选」：不能拼出 /finish/ 这种空段，否则路由解析会错位
		if v := strings.TrimSpace(opts[key]); v != "" {
			path += "/" + key + "/" + manhwaQuote(v)
		}
	}
	path += "/list/" + mc.IntToStr(page)

	resp, err := c.Get(path)
	if err != nil {
		return nil, err
	}
	root, err := ParseHTML(resp.Text())
	if err != nil {
		return nil, mc.Errorf("解析分类页失败: path=%s err=%v", path, err)
	}

	return &mc.SearchPage{
		Items: manhwaParseCards(root, c.Site()),
		Total: manhwaCategoryTotal(root),
		Page:  page,
		Site:  c.Site(),
	}, nil
}

// UpdateList 最近更新（按 addtime 排序）。
func (c *ManhwaClient) UpdateList(page int) (*mc.SearchPage, error) {
	return c.CategoriesFilter(page, map[string]string{"order": manhwaOrderAddtime})
}

// HotList 热门漫画（首页推荐位的 JSON 接口）。
func (c *ManhwaClient) HotList() (*mc.SearchPage, error) {
	resp, err := c.Get(manhwaPathHot)
	if err != nil {
		return nil, err
	}
	env, err := manhwaParseEnvelope(resp.Body, map[string]any{"site": c.Site(), "url": resp.URL})
	if err != nil {
		return nil, err
	}

	var rawList []struct {
		ID     any    `json:"id"`
		Pic    string `json:"pic"`
		Name   string `json:"name"`
		Author string `json:"author"`
		Text   string `json:"text"`
		URL    string `json:"url"`
	}
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &rawList); err != nil {
			return nil, mc.Errorf("解析热门列表失败: %v", err)
		}
	}

	// 该接口给的是**数字** id（卡片页给的是 slug），url 里带 slug，两者都收下
	items := make([]mc.ComicSummary, 0, len(rawList))
	for _, raw := range rawList {
		items = append(items, BuildSummary(mc.ToString(raw.ID), map[string]any{
			"name":   raw.Name,
			"author": raw.Author,
			"pic":    raw.Pic,
			"text":   raw.Text,
			"url":    raw.URL,
			"slug":   mc.ParseSlugFromURL(raw.URL),
		}, c.Site()))
	}

	return &mc.SearchPage{Items: items, Total: len(items), Page: 1, Site: c.Site()}, nil
}

// ============================================================== 漫画详情

// GetComicDetail 取漫画详情。
//
// comicID 可以是数字 id，也可以是 slug（站点两种 URL 都接受）；
// 但章节列表接口只认数字 id，所以详情页解析里必须把数字 id 找出来。
func (c *ManhwaClient) GetComicDetail(comicID string, fetchChapters bool) (*mc.Comic, error) {
	comicID = strings.TrimSpace(comicID)
	if comicID == "" {
		return nil, mc.Errorf("漫画 id 不能为空")
	}

	resp, err := c.Get(fmt.Sprintf(manhwaPathComic, comicID))
	if err != nil {
		return nil, err
	}
	root, err := ParseHTML(resp.Text())
	if err != nil {
		return nil, mc.Errorf("解析漫画详情页失败: comic=%s err=%v", comicID, err)
	}

	comic := manhwaParseDetail(root, comicID, c.Site())

	if fetchChapters {
		list, accessMap, err := c.fetchChapterList(comic.ComicID)
		if err != nil {
			return nil, err
		}
		// 站点列表可能跳号/重话，统一去重并把序号重排为连续值
		comic.Chapters = mc.DistinctChapters(list)
		comic.ChapterAccess = accessMap
	}

	return comic, nil
}

// manhwaParseDetail 解析详情页 HTML。
func manhwaParseDetail(root *html.Node, givenID, site string) *mc.Comic {
	// 详情区块单独定位：页面别处也有 .comic-title（阅读页的 h1、推荐位卡片），
	// 不做作用域限定容易取错标题。
	box := FirstByClass(root, "de-info__box")
	if box == nil {
		box = root
	}

	// 标题
	name := Text(FirstByClass(box, "comic-title"))

	// 封面：列表用 data-original、详情用 src，两个都试
	cover := ""
	if img := FirstByTag(FirstByClass(box, "de-info__cover"), "img"); img != nil {
		cover = strings.TrimSpace(Attr(img, "src"))
		if cover == "" {
			cover = strings.TrimSpace(Attr(img, "data-original"))
		}
	}

	// 作者：站点可能是「A/B」或「A，B」两种写法
	var authors []string
	authorScope := FirstByClass(box, "comic-author")
	if authorScope == nil {
		authorScope = box
	}
	if a := FirstByTag(FirstByClass(authorScope, "name"), "a"); a != nil {
		authorText := strings.ReplaceAll(Text(a), "，", "/")
		for _, part := range strings.Split(authorText, "/") {
			if part = strings.TrimSpace(part); part != "" {
				authors = append(authors, part)
			}
		}
	}

	// 简介：优先「展开后」的完整文本
	introScope := FirstByClass(box, "comic-intro")
	if introScope == nil {
		introScope = root
	}
	intro := FirstByClass(introScope, "intro-total")
	if intro == nil {
		intro = FirstByClass(introScope, "intro")
	}
	description := Text(intro)

	// 数字 id 藏在收藏按钮的 data-id 上——URL 里给的是 slug，接口却要数字 id
	numericID := ""
	if collect := FirstByClass(root, "j-user-collect"); collect != nil {
		numericID = strings.TrimSpace(Attr(collect, "data-id"))
	}
	if numericID == "" {
		// 兜底：数字 id 直接用；传的是 URL 时从中提取（纯 slug 提不出数字则原样保留）
		numericID = mc.ParseToID(givenID)
		if numericID == "" {
			numericID = givenID
		}
	}

	// 连载状态：.de-chapter__title 里第一个非「更新时间」的 span
	serialize := ""
	if titleBox := FirstByClass(root, "de-chapter__title"); titleBox != nil {
		serialize = manhwaFirstSpanText(titleBox, true)
		if serialize == "" {
			// 模板把 span 包进了别的容器时，退化为在整块里找
			serialize = manhwaFirstSpanText(titleBox, false)
		}
	}

	// 人气：.comic-status 里的「人气: 14 万」
	views := 0
	if statusBox := FirstByClass(root, "comic-status"); statusBox != nil {
		for _, node := range ByClass(statusBox, "text") {
			text := Text(node)
			if !strings.Contains(text, "人气") {
				continue
			}
			part := text
			if i := strings.LastIndexAny(part, "：:"); i >= 0 {
				part = part[i+1:]
			}
			views = mc.ParseHumanNumber(strings.TrimSpace(part))
		}
	}

	// 标签：所有指向 /category/tags/ 的链接
	var tags []string
	seen := map[string]bool{}
	for _, a := range ByTag(root, "a") {
		if !strings.Contains(Attr(a, "href"), "/category/tags/") {
			continue
		}
		text := strings.TrimSpace(Text(a))
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		tags = append(tags, text)
	}

	// URL 里给的是 slug 时才算 slug；数字 id 不当 slug
	slug := ""
	if !manhwaAllDigits(givenID) {
		slug = givenID
	}

	return &mc.Comic{
		ComicID:     numericID,
		Name:        name,
		Authors:     authors,
		Tags:        tags,
		Description: description,
		Cover:       cover,
		Serialize:   serialize,
		Views:       views,
		Site:        site,
		URL:         fmt.Sprintf(manhwaPathComic, givenID),
		Slug:        slug,
	}
}

// manhwaFirstSpanText 取块内第一个 span 的文本。
//
// directOnly=true 只看直接子元素（站点的「连载/完结」就是第一个直接子 span，
// 而排序按钮的 span 可能排在它前面，所以直接子元素优先）；
// 为 false 时在整棵子树里找，作为模板变化时的兜底。
func manhwaFirstSpanText(box *html.Node, directOnly bool) string {
	scan := func(nodes []*html.Node) string {
		for _, span := range nodes {
			if span.Data != "span" || HasClass(span, "update-time") {
				continue
			}
			if text := strings.TrimSpace(Text(span)); text != "" {
				return text
			}
		}
		return ""
	}
	if directOnly {
		return scan(Children(box))
	}
	return scan(ByTag(box, "span"))
}

// fetchChapterList 取章节列表（JSON 接口）。
//
// 返回章节元信息与章节权益表；mid 必须是**数字**漫画 id。
func (c *ManhwaClient) fetchChapterList(comicID string) ([]mc.ChapterMeta, map[string]mc.Access, error) {
	if comicID == "" {
		return nil, nil, mc.Errorf("章节列表需要漫画 id")
	}

	resp, err := c.GetWith(manhwaPathChapterList, map[string]string{"mid": comicID})
	if err != nil {
		return nil, nil, err
	}
	env, err := manhwaParseEnvelope(resp.Body, map[string]any{
		"site":     c.Site(),
		"url":      resp.URL,
		"comic_id": comicID,
	})
	if err != nil {
		return nil, nil, err
	}

	var rawList []struct {
		ID    any    `json:"id"`
		Name  string `json:"name"`
		Link  string `json:"link"`
		Pnum  any    `json:"pnum"`
		Price any    `json:"price"`
		VIP   any    `json:"vip"`
		Cion  any    `json:"cion"`
	}
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &rawList); err != nil {
			return nil, nil, mc.Errorf("解析章节列表失败: comic=%s err=%v", comicID, err)
		}
	}

	list := make([]mc.ChapterMeta, 0, len(rawList))
	accessMap := make(map[string]mc.Access, len(rawList))
	for _, raw := range rawList {
		chapterID := mc.ToString(raw.ID)
		if chapterID == "" {
			continue // 没有 id 的条目无法访问，跳过（序号用当前长度续排，保持连续）
		}
		list = append(list, mc.ChapterMeta{
			ChapterID: chapterID,
			Index:     len(list) + 1,
			Name:      mc.Unescape(raw.Name),
		})
		accessMap[chapterID] = mc.Access{
			VIP:   mc.Atoi(raw.VIP),
			Cion:  mc.Atoi(raw.Cion),
			Price: mc.Atoi(raw.Price),
			Pnum:  mc.Atoi(raw.Pnum),
		}
	}

	return list, accessMap, nil
}

// ============================================================== 章节

// GetChapterDetail 取章节详情。
//
// comicID 可以为空：此时漫画数字 id 从阅读页的 readPic(...) 里拿；
// 拿到之后再去章节列表接口定位本话的序号与真实章节名，接口失败就退化（序号 1、用页面标题），
// 不影响阅读。
func (c *ManhwaClient) GetChapterDetail(chapterID, comicID string, fetchImages bool) (*mc.Chapter, error) {
	chapterID = mc.ParseToID(chapterID)
	if chapterID == "" {
		return nil, mc.Errorf("章节 id 不能为空")
	}

	pageHTML, err := c.readerPage(chapterID)
	if err != nil {
		return nil, err
	}
	meta, err := c.parseReaderPage(pageHTML, chapterID)
	if err != nil {
		return nil, err
	}

	// 阅读页里的 mid 是权威值；只有调用方明确给了不同的 key 才改用它。
	// （传进来的可能是 slug，解析不出数字时仍回落到页面里的数字 id。）
	resolvedComicID := meta.ComicID
	if comicID != "" && comicID != meta.ComicID {
		resolvedComicID = mc.ParseToID(comicID)
	}
	if resolvedComicID == "" {
		resolvedComicID = meta.ComicID
	}

	name := meta.Name
	index := 1
	access := mc.Access{VIP: meta.VIP, Cion: meta.Cion, Pnum: meta.Count}
	count := meta.Count

	if resolvedComicID != "" {
		list, accessMap, err := c.fetchChapterList(resolvedComicID)
		if err == nil {
			for _, m := range list {
				if m.ChapterID != chapterID {
					continue
				}
				index = m.Index
				if name == "" {
					name = m.Name // 阅读页没渲染标题时，用章节列表里的真实章节名兜底
				}
				break
			}
			if entry, ok := accessMap[chapterID]; ok {
				// 接口的权益标记比页面内联 JS 更权威（含 price / pnum）
				access = entry
			}
		}
		// 失败不报错：序号只是给目录命名用的，退化即可
	}

	if name == "" {
		name = "第" + chapterID + "话"
	}

	ch := &mc.Chapter{
		ChapterID: chapterID,
		Name:      name,
		ComicID:   resolvedComicID,
		Index:     index,
		Count:     count,
		Access:    access,
		URL:       fmt.Sprintf(manhwaPathReader, chapterID),
	}

	if fetchImages {
		if _, err := c.FetchImageURLs(ch); err != nil {
			return nil, err
		}
	}

	return ch, nil
}

// manhwaReaderMeta 阅读页解析结果。
type manhwaReaderMeta struct {
	ComicID   string
	ComicName string
	Slug      string
	Name      string
	Count     int
	VIP       int
	Cion      int
	ImageURLs []string
	ImageIDs  []string
}

// parseReaderPage 解析阅读页的元信息与内联图片。
func (c *ManhwaClient) parseReaderPage(pageHTML, chapterID string) (*manhwaReaderMeta, error) {
	root, err := ParseHTML(pageHTML)
	if err != nil {
		return nil, mc.Errorf("解析阅读页失败: chapter=%s err=%v", chapterID, err)
	}

	meta := &manhwaReaderMeta{}

	crumb := FirstByClass(root, "read__crumb")

	// 漫画名与 slug：面包屑里的 .crumb__title 指回 /index.php/comic/{slug}
	if link := FirstByTagClass(crumb, "a", "crumb__title"); link != nil {
		meta.ComicName = Text(link)
		meta.Slug = mc.ParseSlugFromURL(Attr(link, "href"))
	}

	// 章节名：.read__crumb h1.comic-title a
	h1 := FirstByTagClass(crumb, "h1", "comic-title")
	if h1 == nil {
		h1 = FirstByTagClass(root, "h1", "comic-title")
	}
	if link := FirstByTag(h1, "a"); link != nil {
		meta.Name = strings.TrimSpace(Text(link))
	} else if h1 != nil {
		meta.Name = strings.TrimSpace(Text(h1))
	}

	// 图片总数
	if btn := FirstByClass(root, "page-index__btn"); btn != nil {
		meta.Count = mc.Atoi(Text(FirstByClass(btn, "count")))
	}

	// 内联 JS readPic(mid,cid,vip,cion)：漫画数字 id 与权限标记只有这里能拿到。
	// 在原始 HTML 上做正则（等价于 Python 版），DOM 里 script 是文本节点，取值一样。
	if m := reManhwaReadPic.FindStringSubmatch(pageHTML); m != nil {
		meta.ComicID = m[1]
		meta.VIP = mc.Atoi(m[3])
		meta.Cion = mc.Atoi(m[4])
	}

	meta.ImageURLs, meta.ImageIDs = manhwaInlineImages(root)
	return meta, nil
}

// FetchImageURLs 取章节图片地址（会写入 chapter）。
//
// 顺序（不可颠倒）：先读阅读页 HTML 的内联图片——免费章节的图直接直出在页面里，
// 此时完全不需要登录态，也不该去调 isbuy；只有内联图片为空（受权限控制的章节）
// 才向 isbuy 申请，由服务端裁决后按返回码抛出对应的权限错误。
func (c *ManhwaClient) FetchImageURLs(ch *mc.Chapter) ([]string, error) {
	if ch == nil {
		return nil, mc.Errorf("章节为空，无法取图")
	}
	if ch.ChapterID == "" {
		return nil, mc.Errorf("章节 id 为空，无法取图")
	}

	pageHTML, err := c.readerPage(ch.ChapterID)
	if err != nil {
		return nil, err
	}
	root, err := ParseHTML(pageHTML)
	if err != nil {
		return nil, mc.Errorf("解析阅读页失败: chapter=%s err=%v", ch.ChapterID, err)
	}

	if urls, ids := manhwaInlineImages(root); len(urls) > 0 {
		ch.SetImages(urls, ids)
		return ch.ImageURLs, nil
	}

	urls, ids, err := c.fetchImagesViaAPI(ch)
	if err != nil {
		return nil, err
	}
	ch.SetImages(urls, ids)
	return ch.ImageURLs, nil
}

// fetchImagesViaAPI 调 isbuy 取图。
//
// 该接口的返回码语义由服务端决定，本方法只做映射，不尝试任何绕过：
// code=1 有权限返回 pic；code=2 未登录；code=3 缺 vip/cion 权益。
func (c *ManhwaClient) fetchImagesViaAPI(ch *mc.Chapter) ([]string, []string, error) {
	resp, err := c.GetWith(manhwaPathIsBuy, map[string]string{"id": ch.ChapterID})
	if err != nil {
		return nil, nil, err
	}

	env, err := manhwaParseEnvelope(resp.Body, map[string]any{
		"site":       c.Site(),
		"url":        resp.URL,
		"chapter_id": ch.ChapterID,
		"comic_id":   ch.ComicID,
	})
	if err != nil {
		return nil, nil, err
	}

	var pics []struct {
		ID  any    `json:"id"`
		Img string `json:"img"`
	}
	if len(env.Pic) > 0 {
		if err := json.Unmarshal(env.Pic, &pics); err != nil {
			return nil, nil, mc.Errorf("解析章节图片失败: chapter=%s err=%v", ch.ChapterID, err)
		}
	}

	urls := make([]string, 0, len(pics))
	ids := make([]string, 0, len(pics))
	for _, item := range pics {
		img := strings.TrimSpace(item.Img)
		if img == "" {
			continue
		}
		urls = append(urls, img)
		ids = append(ids, mc.ToString(item.ID))
	}

	if len(urls) == 0 {
		// 有权限但服务端没给图：明确报错，不要静默返回空列表
		return nil, nil, mc.NewError(
			fmt.Sprintf("章节 [%s] 未返回任何图片（该章节可能需要会员权益）", ch.ChapterID),
			map[string]any{"site": c.Site(), "chapter_id": ch.ChapterID, "comic_id": ch.ComicID},
		)
	}

	return urls, ids, nil
}

// ============================================================== url

// GetComicIDFromURL 从 URL 解析漫画 id：数字 id 优先，其次是 slug。
func (c *ManhwaClient) GetComicIDFromURL(u string) string {
	if numeric := mc.ParseComicIDFromURL(u); numeric != "" {
		return numeric
	}
	return mc.ParseSlugFromURL(u)
}

// GetChapterIDFromURL 从 URL 解析章节 id。
func (c *ManhwaClient) GetChapterIDFromURL(u string) string {
	return mc.ParseChapterIDFromURL(u)
}

// ============================================================== 登录

// Login 使用账号密码登录（Mccms 通用接口），成功后 cookie 留在会话里。
//
// 为什么需要：isbuy 要求登录态，未登录只能读免费章节的内联图片。
// 这里只做普通登录，不涉及任何绕过访问控制的逻辑。
func (c *ManhwaClient) Login(username, password string) error {
	if strings.TrimSpace(username) == "" || password == "" {
		return mc.Errorf("登录需要提供用户名与密码")
	}

	resp, err := c.GetWith(manhwaPathLogin, map[string]string{
		"name":  username,
		"pass":  password,
		"islog": "1",
		"pcode": "",
	})
	if err != nil {
		return err
	}

	if _, err := manhwaParseEnvelope(resp.Body, map[string]any{
		"site": c.Site(),
		"url":  resp.URL,
	}); err != nil {
		return err
	}
	return nil
}

// ============================================================== 内部工具

// manhwaEnvelope Mccms 的 JSON 信封。
//
// data / pic 用 RawMessage 承接：这样即使服务端在失败分支里不回这两个字段
// （例如 code=3 只有 msg + type），也不会因为反序列化形状不符而丢掉真正的错误码。
type manhwaEnvelope struct {
	Code    any             `json:"code"`
	Msg     string          `json:"msg"`
	Message string          `json:"message"`
	Status  string          `json:"status"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Pic     json.RawMessage `json:"pic"`
}

// manhwaParseEnvelope 校验 HTTP 结果并解析 Mccms JSON 信封。
//
// 返回码语义（与站点实测一致，也与 Python 版 parse_resp 对齐）：
//
//	code=1            成功
//	status=success    另一套列表信封（code 恒为 -1），以 status 为准
//	code=2            需要登录  → mc.RaiseAccessError(code=2)
//	code=3 + type     vip/cion  → mc.RaiseAccessError(code=3, type)
//	其它              普通错误
func manhwaParseEnvelope(body []byte, ctx map[string]any) (*manhwaEnvelope, error) {
	env := &manhwaEnvelope{}
	if err := json.Unmarshal(body, env); err != nil {
		return nil, mc.Errorf("解析接口响应失败: %v", err)
	}

	if strings.EqualFold(env.Status, "success") {
		return env, nil
	}

	// 缺省 code 视为成功（与站点「不带 code 即成功」的约定一致）
	code := 1
	if env.Code != nil {
		code = mc.Atoi(env.Code)
	}
	if code == 1 {
		return env, nil
	}

	msg := env.Msg
	if msg == "" {
		msg = env.Message
	}
	if msg == "" {
		msg = fmt.Sprintf("接口返回异常: code=%d", code)
	}

	if code == 2 || code == 3 {
		// 统一工厂：code=2 或 type=login → 需要登录；
		// type=vip/cion/ticket/pay → 需要会员权益；其余 → 章节不可访问
		return nil, mc.RaiseAccessError(msg, ctx, code, env.Type)
	}
	return nil, mc.Errorf("接口返回异常: code=%d msg=%s", code, msg)
}

// manhwaParseCards 解析 .common-comic-item 卡片列表（搜索页 / 分类页通用）。
//
// 卡片里没有数字漫画 id（href 给的是 slug），也没有作者字段，
// 所以 id 用 slug、author 留空；要看作者必须进详情页。
func manhwaParseCards(root *html.Node, site string) []mc.ComicSummary {
	items := []mc.ComicSummary{}
	for _, item := range ByClass(root, "common-comic-item") {
		link := FirstByTag(FirstByClass(item, "comic__title"), "a")
		if link == nil {
			link = FirstByTagClass(item, "a", "cover") // 兜底：只有封面可点
		}
		if link == nil {
			continue
		}

		href := Attr(link, "href")
		slug := mc.ParseSlugFromURL(href)
		comicID := slug
		if comicID == "" {
			comicID = mc.ParseToID(href)
		}
		if comicID == "" {
			continue // 既没有 slug 也没有数字 id，这条无法访问
		}

		cover, name := "", ""
		if img := FirstByTag(item, "img"); img != nil {
			cover = strings.TrimSpace(Attr(img, "data-original"))
			if cover == "" {
				cover = strings.TrimSpace(Attr(img, "src"))
			}
			name = Attr(img, "alt")
		}
		if name == "" {
			name = Text(link)
		}

		latestChapter := ""
		if update := FirstByClass(item, "comic-update"); update != nil {
			if hl := FirstByTagClass(update, "a", "hl"); hl != nil {
				latestChapter = Text(hl)
			}
		}

		views := 0
		if counter := FirstByClass(item, "comic-count"); counter != nil {
			// 「人气：14 万」交给 ParseHumanNumber 处理中文单位
			views = mc.ParseHumanNumber(strings.NewReplacer("人气：", "", "人气:", "").Replace(Text(counter)))
		}

		feature := ""
		if node := FirstByClass(item, "comic-feature"); node != nil {
			feature = Text(node)
		}

		items = append(items, BuildSummary(comicID, map[string]any{
			"name":               name,
			"cover":              cover,
			"text":               feature,
			"hits":               views,
			"url":                href,
			"slug":               slug,
			"last_chapter_title": latestChapter,
		}, site))
	}
	return items
}

// manhwaInlineImages 取免费章节直出在 HTML 里的图片。
//
// 每个 .rd-article__pic 一张图；data-original 为空时用 src，
// 但 src 为 lazyload_img.png 的占位图不算真图（受权限控制的章节整页都是占位图）。
func manhwaInlineImages(root *html.Node) ([]string, []string) {
	var urls, ids []string
	for _, pic := range ByClass(root, "rd-article__pic") {
		img := FirstByTag(pic, "img")
		if img == nil {
			continue
		}
		src := strings.TrimSpace(Attr(img, "data-original"))
		if src == "" {
			src = strings.TrimSpace(Attr(img, "src"))
		}
		if src == "" || strings.Contains(src, "lazyload_img.png") {
			continue
		}
		urls = append(urls, src)
		// id 只跟着真正取到的图收，保证 ImageIDs 与 ImageURLs 一一对应（占位图会错位）
		ids = append(ids, Attr(pic, "data-pid"))
	}
	return urls, ids
}

// manhwaSearchTotal 解析搜索页的「搜索结果（N）」总数。
func manhwaSearchTotal(root *html.Node) int {
	head := FirstByClass(root, "search_head")
	if head == nil {
		return 0
	}
	m := reManhwaSearchTotal.FindStringSubmatch(Text(head))
	if m == nil {
		return 0
	}
	return mc.Atoi(m[1])
}

// manhwaCategoryTotal 估算分类页总数。
//
// 站点分页控件只渲染页码、不给总数，这里按「最大页码 × 每页 30 条」估算，
// 与 Python 版保持一致（宁可偏大，也不要截断列表）。
func manhwaCategoryTotal(root *html.Node) int {
	node := FirstByClass(root, "pagination")
	if node == nil {
		node = FirstClassContains(root, "page")
	}
	if node == nil {
		return 0
	}

	maxPage := 0
	for _, d := range reManhwaDigits.FindAllString(Text(node), -1) {
		if n := mc.Atoi(d); n > maxPage {
			maxPage = n
		}
	}
	return maxPage * mc.PageSizeDefault
}

// readerPage 取阅读页 HTML（优先用缓存）。
func (c *ManhwaClient) readerPage(chapterID string) (string, error) {
	if cached, ok := c.cachedReaderHTML(chapterID); ok {
		return cached, nil
	}

	resp, err := c.Get(fmt.Sprintf(manhwaPathReader, chapterID))
	if err != nil {
		return "", err
	}
	pageHTML := resp.Text()
	c.cacheReaderHTML(chapterID, pageHTML)
	return pageHTML, nil
}

func (c *ManhwaClient) cachedReaderHTML(chapterID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pageHTML, ok := c.readerHTML[chapterID]
	return pageHTML, ok
}

func (c *ManhwaClient) cacheReaderHTML(chapterID, pageHTML string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readerHTML == nil {
		c.readerHTML = map[string]string{}
	}
	if len(c.readerHTML) >= manhwaReaderCacheMax {
		c.readerHTML = map[string]string{}
	}
	c.readerHTML[chapterID] = pageHTML
}

// manhwaQuote 编码路径片段（搜索关键字、分类筛选项）。
//
// 对齐 Python 的 urllib.parse.quote(safe='/')：QueryEscape 会把空格编成 '+'，
// 在路径里必须还原成 %20；斜杠保留，避免关键字里的 / 把路径切碎。
func manhwaQuote(s string) string {
	q := url.QueryEscape(s)
	q = strings.ReplaceAll(q, "+", "%20")
	return strings.ReplaceAll(q, "%2F", "/")
}

// manhwaAllDigits 判断字符串是否全为数字。
func manhwaAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
