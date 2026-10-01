package mc

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ---- 名称净化 ---------------------------------------------------------------

var winForbid = strings.NewReplacer(
	"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
	`"`, "_", "<", "_", ">", "_", "|", "_",
	"\n", "_", "\t", "_", "\r", "_",
)

// FixWinDirName 把文件名/目录名里各平台的非法字符替换掉。
func FixWinDirName(name string) string {
	out := strings.TrimSpace(winForbid.Replace(name))
	out = strings.TrimRight(out, ".")
	return out
}

// FixSuffix 保证后缀以点开头。
func FixSuffix(suffix string) string {
	if suffix == "" {
		return ""
	}
	if strings.HasPrefix(suffix, ".") {
		return suffix
	}
	return "." + suffix
}

// Unescape 解码站点 JSON 里残留的 HTML 实体。
func Unescape(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}

// ---- id 解析 ----------------------------------------------------------------

var (
	reChapterScoped = regexp.MustCompile(`/chapter/\d+/(\d+)`) // TIBIU: /chapter/{comic}/{chapter}
	reChapter       = regexp.MustCompile(`/chapter/(\d+)`)     // manhwa: /index.php/chapter/{id}
	reComic         = regexp.MustCompile(`/comic/(\d+)`)
	reComicSlug     = regexp.MustCompile(`/comic/([A-Za-z0-9_\-]+)`)
	reQueryID       = regexp.MustCompile(`[?&](?:id|mid|cid)=(\d+)`)
	reDigits        = regexp.MustCompile(`(\d{2,})`)
	reBoyloveComic  = regexp.MustCompile(`/home/book/index/id/(\d+)`)
	reBoyloveChap   = regexp.MustCompile(`/home/book/capter/id/(\d+)`)
)

// ParseToID 从 id / URL / 混合文本中提取数字资源 id。
func ParseToID(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if isAllDigits(text) {
		return text
	}
	for _, re := range []*regexp.Regexp{
		reChapterScoped, reBoyloveChap, reChapter, reBoyloveComic,
		reComic, reComicSlug, reQueryID, reDigits,
	} {
		if m := re.FindStringSubmatch(text); m != nil {
			return m[1]
		}
	}
	return ""
}

// ParseChapterIDFromURL 提取章节 id。
func ParseChapterIDFromURL(u string) string {
	if m := reBoyloveChap.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	if m := reChapterScoped.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	if m := reChapter.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// ParseComicIDFromURL 提取漫画数字 id。
func ParseComicIDFromURL(u string) string {
	if m := reBoyloveComic.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	if m := reComic.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// ParseSlugFromURL 提取 slug 形式的漫画标识（如 manhwa 的 mozhouweixianzaoyu）。
func ParseSlugFromURL(u string) string {
	m := reComicSlug.FindStringSubmatch(u)
	if m == nil || isAllDigits(m[1]) {
		return ""
	}
	return m[1]
}

// ParseDomain 从 URL 取主机名。
func ParseDomain(u string) string {
	if m := regexp.MustCompile(`https?://([^/]+)`).FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return strings.Trim(strings.TrimSpace(u), "/")
}

// ParseHumanNumber 解析「14 万」「1.2亿」「1,234」这类中文计数。
func ParseHumanNumber(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		s := strings.ReplaceAll(strings.ReplaceAll(t, ",", ""), " ", "")
		if s == "" {
			return 0
		}
		unit := 1.0
		switch {
		case strings.HasSuffix(s, "万"):
			unit, s = 10000, strings.TrimSuffix(s, "万")
		case strings.HasSuffix(s, "亿"):
			unit, s = 100000000, strings.TrimSuffix(s, "亿")
		case strings.HasSuffix(s, "千"):
			unit, s = 1000, strings.TrimSuffix(s, "千")
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f * unit)
		}
		digits := regexp.MustCompile(`\D`).ReplaceAllString(s, "")
		if digits == "" {
			return 0
		}
		n, _ := strconv.Atoi(digits)
		return n
	}
	return 0
}

// Atoi 宽松整数解析。
func Atoi(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f)
		}
	}
	return 0
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// ---- 标签 -------------------------------------------------------------------

// ParseTags 把标签字段拆成字符串切片。
func ParseTags(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := strings.TrimSpace(toString(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		s := strings.NewReplacer("，", ",", "|", ",").Replace(t)
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		return strings.TrimSpace(strings.Trim(strings.TrimSpace(fmt.Sprint(v)), `"`))
	}
}

// ToString 导出给其它包使用。
func ToString(v any) string { return toString(v) }
