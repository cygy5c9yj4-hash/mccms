package client

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mccms/mccms-go/internal/mc"
)

// Options 构造客户端时的配置。
type Options struct {
	Site     string
	Domains  []string
	Retries  int
	Timeout  int // 秒
	Headers  map[string]string
	Resolve  map[string]string
	Proxies  string
	Cookies  map[string]string
	Username string
	Password string
}

// Client 是所有站点客户端的契约。
type Client interface {
	// Site 站点 key。
	Site() string

	// Search 按关键字搜索。
	Search(keyword string, page int) (*mc.SearchPage, error)

	// GetComicDetail 漫画详情；fetchChapters 为 false 时跳过章节列表。
	GetComicDetail(comicID string, fetchChapters bool) (*mc.Comic, error)

	// GetChapterDetail 章节详情；fetchImages 为 false 时跳过图片列表。
	GetChapterDetail(chapterID, comicID string, fetchImages bool) (*mc.Chapter, error)

	// FetchImageURLs 取章节图片地址（会写入 chapter）。
	FetchImageURLs(ch *mc.Chapter) ([]string, error)

	// CategoriesFilter 分类浏览。
	CategoriesFilter(page int, opts map[string]string) (*mc.SearchPage, error)

	// UpdateList 最近更新。
	UpdateList(page int) (*mc.SearchPage, error)

	// GetComicIDFromURL / GetChapterIDFromURL 从 URL 解析 id。
	GetComicIDFromURL(u string) string
	GetChapterIDFromURL(u string) string

	// Postman 暴露底层会话，供图片代理等复用。
	Postman() *mc.Postman
}

// Ranker 支持排行榜的客户端。
type Ranker interface {
	Ranking(rankType string, page int) (*mc.SearchPage, error)
	RankingNav() ([]mc.RankNav, error)
}

// HotLister 支持热门列表的客户端。
type HotLister interface {
	HotList() (*mc.SearchPage, error)
}

// ImageResolver 让客户端按需把「图片标识」解析成真实图片直链。
//
// 有些站点（例如 E-Hentai）列表页只给图片页地址，直链必须再访问一次图片页才能拿到。
// 对这类站点，逐个解析的成本很高，所以不强制在拉列表时就解析完全部图片，
// 而是由下载器和图片代理在真正需要某一张时调用本接口。
type ImageResolver interface {
	ResolveImage(chapterID, name string) (string, error)
}

// Base 提供各站点共用的能力。
type Base struct {
	SiteName string
	HTTP     *mc.Postman
}

func (b *Base) Site() string         { return b.SiteName }
func (b *Base) Postman() *mc.Postman { return b.HTTP }

// Get 发起 GET。
func (b *Base) Get(path string) (*mc.Response, error) { return b.HTTP.Get(path) }

// GetWith 带 query 的 GET。
func (b *Base) GetWith(path string, q map[string]string) (*mc.Response, error) {
	return b.HTTP.GetWith(path, q)
}

// UserInfo 取用户态（Mccms 站点通用）。站点未实现时返回空 map。
func (b *Base) UserInfo() (map[string]any, error) {
	resp, err := b.Get("/index.php/api/user/info")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return nil, err
	}
	if payload.Data == nil {
		payload.Data = map[string]any{}
	}
	return payload.Data, nil
}

// New 按站点构造客户端。
func New(opts Options) (Client, error) {
	if !mc.KnownSite(opts.Site) {
		return nil, mc.Errorf("未知站点: [%s]，可用: %v", opts.Site, mc.AllSites)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = mc.DefaultTimeout
	}

	postman := mc.NewPostman(mc.PostmanConfig{
		Site:    opts.Site,
		Domains: opts.Domains,
		Retries: opts.Retries,
		Timeout: time.Duration(timeout) * time.Second,
		Headers: opts.Headers,
		Resolve: opts.Resolve,
		Proxies: opts.Proxies,
	})
	if len(opts.Cookies) > 0 {
		postman.SetCookies(opts.Cookies)
	}

	base := Base{SiteName: opts.Site, HTTP: postman}

	var c Client
	switch opts.Site {
	case mc.SiteTibiu:
		c = NewTibiu(base)
	case mc.SiteManhwa:
		c = NewManhwa(base)
	case mc.SiteBoylove:
		c = NewBoylove(base)
	case mc.SiteNhentai:
		c = NewNhentai(base)
	case mc.SiteEhentai:
		c = NewEhentai(base)
	}

	// 配置了账号则尝试登录（仅用于访问该账号本身有权访问的内容）
	if opts.Username != "" && opts.Password != "" {
		if lg, ok := c.(interface{ Login(string, string) error }); ok {
			_ = lg.Login(opts.Username, opts.Password)
		}
	}

	return c, nil
}

// ---- 共用小工具 -------------------------------------------------------------

// BuildSummary 把站点返回的原始 map 归一化成 ComicSummary。
func BuildSummary(id string, raw map[string]any, site string) mc.ComicSummary {
	return mc.ComicSummary{
		ID:            id,
		Name:          mc.Unescape(mc.ToString(raw["name"])),
		Author:        mc.Unescape(mc.ToString(firstNonNil(raw["author"], raw["auther"]))),
		Cover:         mc.ToString(firstNonNil(raw["pic"], raw["cover"], raw["image"])),
		Text:          mc.Unescape(mc.ToString(firstNonNil(raw["text"], raw["desc"], raw["description"]))),
		Serialize:     mc.Unescape(mc.ToString(raw["serialize"])),
		UpdateDate:    mc.ToString(firstNonNil(raw["addtime"], raw["update_time"], raw["update_date"])),
		LatestChapter: mc.Unescape(mc.ToString(raw["last_chapter_title"])),
		Views:         mc.ParseHumanNumber(firstNonNil(raw["hits"], raw["views"], raw["watchtimes"], raw["view"])),
		Score:         toFloat(raw["score"]),
		Tags:          mc.ParseTags(raw["tags"]),
		Site:          site,
		URL:           mc.ToString(raw["url"]),
		Slug:          mc.ToString(firstNonNil(raw["slug"], raw["yname"])),
	}
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		return v
	}
	return nil
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		var f float64
		_, _ = fmt.Sscanf(strings.TrimSpace(t), "%f", &f)
		return f
	}
	return 0
}
