package mc

// 站点 key（与 Python 版保持一致）。
const (
	SiteTibiu   = "tibiu"
	SiteManhwa  = "manhwa"
	SiteBoylove = "boylove"
	SiteEhentai = "ehentai"
)

// AllSites 全部站点。
var AllSites = []string{SiteTibiu, SiteManhwa, SiteBoylove, SiteEhentai}

// SiteNames 站点展示名。
var SiteNames = map[string]string{
	SiteTibiu:   "TIBIU",
	SiteManhwa:  "漫蛙",
	SiteBoylove: "香香腐宅",
	SiteEhentai: "E-Hentai",
}

// SiteDomains 站点默认域名（可被 option 覆盖，也支持追加镜像）。
var SiteDomains = map[string][]string{
	SiteTibiu:   {"cache.tibiu.net"},
	SiteManhwa:  {"www.manhwa.wang"},
	SiteBoylove: {"boylove.cc"},
	SiteEhentai: {"e-hentai.org"},
}

// 运行时常量。
const (
	DefaultAuthor   = "default_author"
	DefaultTimeout  = 20 // 秒
	DefaultRetries  = 3
	PageSizeSearch  = 10
	PageSizeDefault = 30

	// UserAgent 默认 UA。
	UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	// VeryTallImageHeight 站点前端对 height >= 4000 的图不做竖带还原，本库保持一致。
	VeryTallImageHeight = 4000
)

// DefaultHeaders HTML 请求头模板。
func DefaultHeaders() map[string]string {
	return map[string]string{
		"User-Agent":      UserAgent,
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Connection":      "keep-alive",
	}
}

// JSONHeaders JSON 请求头模板。
func JSONHeaders() map[string]string {
	h := DefaultHeaders()
	h["Accept"] = "application/json, text/javascript, */*; q=0.01"
	h["X-Requested-With"] = "XMLHttpRequest"
	return h
}

// KnownSite 判断站点 key 是否受支持。
func KnownSite(site string) bool {
	_, ok := SiteDomains[site]
	return ok
}

// SiteName 取站点展示名。
func SiteName(site string) string {
	if n, ok := SiteNames[site]; ok {
		return n
	}
	return site
}
