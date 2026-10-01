package mc

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DirRule 路径规则 DSL。
//
// 规则由 `_` 或 `/` 分隔，支持三类片段：
//
//  1. Bd               —— base_dir
//  2. Cxxx / Chxxx     —— 漫画 / 章节实体的字段
//  3. {Cid}_{Chindex:03} —— Go 模板风格
type DirRule struct {
	BaseDir     string `yaml:"base_dir"`
	Rule        string `yaml:"rule"`
	NormalizeZH string `yaml:"normalize_zh,omitempty"`
}

const (
	prefixComic   = "C"
	prefixChapter = "Ch"
	ruleBaseDir   = "Bd"
)

// NewDirRule 构造并校验规则。
func NewDirRule(rule, baseDir, normalizeZH string) (*DirRule, error) {
	if strings.TrimSpace(rule) == "" {
		return nil, Errorf("dir_rule.rule 不能为空")
	}
	if baseDir == "" {
		baseDir = mustGetwd()
	}
	abs, err := filepath.Abs(ExpandEnv(baseDir))
	if err != nil {
		abs = baseDir
	}
	return &DirRule{BaseDir: abs, Rule: rule, NormalizeZH: normalizeZH}, nil
}

// SplitDSL 切分规则片段（`/` 优先，其次 `_`），并自动补 Bd。
func (d *DirRule) SplitDSL() []string {
	sep := "_"
	if strings.Contains(d.Rule, "/") {
		sep = "/"
	}
	parts := strings.Split(d.Rule, sep)
	out := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 || out[0] != ruleBaseDir {
		out = append([]string{ruleBaseDir}, out...)
	}
	return out
}

// IsChapterOnly 判断片段是否只依赖章节字段。
func IsChapterOnly(rule string) bool {
	if strings.HasPrefix(rule, prefixChapter) {
		return true
	}
	if strings.Contains(rule, "{") {
		keys := reTemplateKey.FindAllStringSubmatch(rule, -1)
		if len(keys) == 0 {
			return false
		}
		for _, k := range keys {
			if !strings.HasPrefix(k[1], prefixChapter) {
				return false
			}
		}
		return true
	}
	return false
}

var reTemplateKey = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)[^}]*\}`)

// ApplyPath 生成路径。onlyComic 为 true 时跳过纯章节片段（用于求漫画根目录）。
func (d *DirRule) ApplyPath(comic *Comic, chapter *Chapter, onlyComic bool) (string, error) {
	segs := []string{}
	for _, rule := range d.SplitDSL() {
		if onlyComic && IsChapterOnly(rule) {
			continue
		}

		seg, err := d.resolveSeg(comic, chapter, rule)
		if err != nil {
			return "", err
		}
		if rule != ruleBaseDir {
			seg = FixWinDirName(seg)
		}
		segs = append(segs, seg)
	}
	return strings.Join(segs, "/"), nil
}

func (d *DirRule) resolveSeg(comic *Comic, chapter *Chapter, rule string) (string, error) {
	if rule == ruleBaseDir {
		return d.BaseDir, nil
	}

	if strings.Contains(rule, "{") {
		return renderTemplate(rule, FieldMap(comic, chapter))
	}

	for _, prefix := range []string{prefixChapter, prefixComic} {
		if !strings.HasPrefix(rule, prefix) {
			continue
		}
		field := rule[len(prefix):]
		if prefix == prefixChapter {
			if chapter == nil {
				return "", Errorf("dir_rule 片段 [%s] 需要章节上下文", rule)
			}
			v, ok := ChapterField(chapter, field)
			if !ok {
				return "", Errorf("章节不存在字段 [%s]", field)
			}
			return v, nil
		}
		if comic == nil {
			return "", Errorf("dir_rule 片段 [%s] 缺少漫画上下文。\n"+
				"单独下载章节时请提供 comic_id，或改用只依赖章节的规则（例如 Bd_Chindextitle）", rule)
		}
		v, ok := ComicField(comic, field)
		if !ok {
			return "", Errorf("漫画不存在字段 [%s]", field)
		}
		return v, nil
	}

	// 普通字符串片段
	return renderTemplate(rule, FieldMap(comic, chapter))
}

// ImageDir 章节图片目录。
func (d *DirRule) ImageDir(comic *Comic, chapter *Chapter) (string, error) {
	p, err := d.ApplyPath(comic, chapter, false)
	if err != nil {
		return "", err
	}
	return p, EnsureDir(p)
}

// ComicRoot 漫画根目录（只用 Bd 与 C* 片段）。
func (d *DirRule) ComicRoot(comic *Comic) (string, error) {
	return d.ApplyPath(comic, nil, true)
}

// ApplyToFilename 只生成单段文件名（供打包类功能使用）。
func (d *DirRule) ApplyToFilename(comic *Comic, chapter *Chapter, rule string) (string, error) {
	if comic == nil && chapter != nil {
		comic = chapter.FromComic
	}
	if rule == ruleBaseDir {
		return "", Errorf("文件名规则不支持 Bd")
	}
	if strings.Contains(rule, "{") {
		out, err := renderTemplate(rule, FieldMap(comic, chapter))
		if err != nil {
			return "", err
		}
		return FixWinDirName(out), nil
	}
	out, err := d.resolveSeg(comic, chapter, rule)
	if err != nil {
		return "", err
	}
	return FixWinDirName(out), nil
}

// ---- 字段表 -----------------------------------------------------------------

// ComicField 取漫画字段。
func ComicField(c *Comic, field string) (string, bool) {
	if c == nil {
		return "", false
	}
	switch field {
	case "id", "comic_id":
		return c.ComicID, true
	case "name", "title":
		return c.Name, true
	case "author":
		return c.Author(), true
	case "authors":
		return strings.Join(c.Authors, ","), true
	case "serialize":
		return c.Serialize, true
	case "tags":
		return strings.Join(c.Tags, ","), true
	case "description":
		return c.Description, true
	case "cover":
		return c.Cover, true
	case "update_date":
		return c.UpdateDate, true
	case "views":
		return itoa(c.Views), true
	case "score":
		return trimFloat(c.Score), true
	case "site":
		return c.Site, true
	case "slug":
		return c.Slug, true
	case "url":
		return c.URL, true
	case "chapter_count":
		return itoa(len(c.Chapters)), true
	case "oname":
		return OrigName(c.Name), true
	case "authoroname":
		return "【" + c.Author() + "】" + OrigName(c.Name), true
	case "idoname":
		return "[" + c.ComicID + "] " + OrigName(c.Name), true
	}
	return "", false
}

// ChapterField 取章节字段。
func ChapterField(ch *Chapter, field string) (string, bool) {
	if ch == nil {
		return "", false
	}
	switch field {
	case "id", "chapter_id":
		return ch.ChapterID, true
	case "name", "title":
		return ch.Name, true
	case "index":
		return itoa(ch.Index), true
	case "indextitle":
		return ch.IndexTitle(), true
	case "count":
		return itoa(ch.Count), true
	case "comic_id":
		return ch.ComicID, true
	case "comic_name":
		return ch.ComicName(), true
	case "author":
		return ch.Author(), true
	case "tags":
		return strings.Join(ch.Tags(), ","), true
	case "access":
		return ch.Access.Desc(), true
	case "is_free":
		return boolStr(ch.Access.Free()), true
	case "update_date":
		return ch.UpdateDate, true
	case "url":
		return ch.URL, true
	case "oname":
		return OrigName(ch.Name), true
	}
	return "", false
}

// FieldMap 汇总漫画与章节字段，供模板渲染使用。
func FieldMap(comic *Comic, chapter *Chapter) map[string]string {
	out := map[string]string{}
	for _, prefix := range []string{"C", "Ch"} {
		if prefix == "C" {
			if comic == nil {
				continue
			}
			for _, f := range comicFields {
				if v, ok := ComicField(comic, f); ok {
					out[prefix+f] = v
				}
			}
		} else {
			if chapter == nil {
				continue
			}
			for _, f := range chapterFields {
				if v, ok := ChapterField(chapter, f); ok {
					out[prefix+f] = v
				}
			}
		}
	}
	return out
}

var comicFields = []string{
	"id", "name", "author", "authors", "serialize", "tags", "description", "cover",
	"update_date", "views", "score", "site", "slug", "url", "chapter_count",
	"oname", "authoroname", "idoname",
}

var chapterFields = []string{
	"id", "name", "index", "indextitle", "count", "comic_id", "comic_name",
	"author", "tags", "access", "is_free", "update_date", "url", "oname",
}

var reTemplateToken = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(:([^}]*))?\}`)

// renderTemplate 渲染 {Cid} / {Chindex:03} 这类模板。
func renderTemplate(tpl string, fields map[string]string) (string, error) {
	var firstErr error
	out := reTemplateToken.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := reTemplateToken.FindStringSubmatch(m)
		key, spec := sub[1], sub[3]
		val, ok := fields[key]
		if !ok {
			if firstErr == nil {
				firstErr = Errorf("规则 [%s] 引用了不存在的字段 {%s}；漫画字段以 C 开头、章节字段以 Ch 开头", tpl, key)
			}
			return m
		}
		return applyFormatSpec(val, spec)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// applyFormatSpec 支持零填充（对齐 Python 的 {Chindex:03}）。
//
// 规则与 Python 的 format spec 一致：前导 0 表示用 0 填充，其余数字是宽度。
func applyFormatSpec(val, spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return val
	}

	pad := byte(' ')
	if strings.HasPrefix(spec, "0") {
		pad = '0'
		spec = spec[1:]
	}
	width, err := strconv.Atoi(spec)
	if err != nil || width <= 0 || len(val) >= width {
		return val
	}
	return strings.Repeat(string(pad), width-len(val)) + val
}

// ---- 名称工具 ---------------------------------------------------------------

var (
	reBracket = regexp.MustCompile(`^[\(\[【（《].*[\)\]】）》]$`)
	reSplit   = regexp.MustCompile(`(\([^)]*\)|\[[^\]]*\]|【[^】]*】|（[^）]*）|《[^》]*》)`)
)

// OrigName 去掉 [汉化组]、【社团】 这类后缀，取原始名称。
func OrigName(title string) string {
	for _, tok := range reSplit.Split(title, -1) {
		tok = strings.TrimSpace(tok)
		if tok != "" && !reBracket.MatchString(tok) {
			return tok
		}
	}
	return strings.TrimSpace(title)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func trimFloat(f float64) string {
	s := strings.TrimRight(strings.TrimRight(formatFloat(f), "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// ---- 环境变量展开 -----------------------------------------------------------

var reEnv = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnv 展开 ${VAR}；未设置时保留原串（
// 这里选择宽松处理，避免读一个示例配置就 panic）。
func ExpandEnv(s string) string {
	return reEnv.ReplaceAllStringFunc(s, func(m string) string {
		name := reEnv.FindStringSubmatch(m)[1]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return m
	})
}

// LoadYAML 读取 yaml 文件。
func LoadYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}
