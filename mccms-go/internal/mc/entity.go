package mc

import (
	"os"
	"path/filepath"
	"strings"
)

// Access 章节的权益标记。
type Access struct {
	VIP   int `json:"vip"`
	Cion  int `json:"cion"`
	Price int `json:"price"`
	Pnum  int `json:"pnum"`
}

// Free 是否免费可读。
func (a Access) Free() bool { return a.VIP == 0 && a.Cion == 0 && a.Price == 0 }

// Desc 权限的中文描述。
func (a Access) Desc() string {
	if a.Free() {
		return "免费"
	}
	parts := []string{}
	if a.VIP > 0 {
		parts = append(parts, "VIP")
	}
	if a.Cion > 0 {
		parts = append(parts, "金币")
	}
	if a.Price > 0 {
		parts = append(parts, "付费")
	}
	if len(parts) == 0 {
		return "受限"
	}
	return strings.Join(parts, "/")
}

// Image 单张图片。
type Image struct {
	ChapterID string `json:"chapter_id"`
	URL       string `json:"url"`
	FileName  string `json:"file_name"` // 不含后缀
	Suffix    string `json:"suffix"`    // 含点
	Index     int    `json:"index"`     // 从 1 开始
	ImgID     string `json:"img_id,omitempty"`
	ScrambleN int    `json:"scramble_n"` // >1 表示需要竖带倒序还原

	FromChapter *Chapter `json:"-"`
	SavePath    string   `json:"-"`
	Exists      bool     `json:"-"`
	Skip        bool     `json:"-"`
}

// Filename 含后缀的文件名。
func (i *Image) Filename() string { return i.FileName + i.Suffix }

// IsScrambled 是否需要还原。
func (i *Image) IsScrambled() bool { return i.ScrambleN > 1 }

// NewImage 由 URL 构造图片实体。
func NewImage(chapterID, rawURL string, index int, scrambleN int) (*Image, error) {
	url := strings.TrimSpace(rawURL)
	if url == "" {
		return nil, Errorf("图片地址为空: chapter=%s index=%d", chapterID, index)
	}

	path := url
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	slash := strings.LastIndex(path, "/")
	dot := strings.LastIndex(path, ".")
	name, suffix := "", ".jpg"
	if dot > slash && slash >= 0 {
		name, suffix = path[slash+1:dot], path[dot:]
	} else if slash >= 0 {
		name = path[slash+1:]
	}
	if name == "" {
		name = padIndex(index)
	}

	return &Image{
		ChapterID: chapterID,
		URL:       url,
		FileName:  name,
		Suffix:    suffix,
		Index:     index,
		ScrambleN: scrambleN,
	}, nil
}

func padIndex(i int) string {
	s := "000"
	if i > 0 {
		s = ""
		for n := i; n > 0; n /= 10 {
			s = string(rune('0'+n%10)) + s
		}
	}
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

// ChapterMeta 漫画下的章节元信息。
type ChapterMeta struct {
	ChapterID string
	Index     int
	Name      string
}

// Chapter 章节实体。
type Chapter struct {
	ChapterID  string
	Name       string
	ComicID    string
	Index      int
	Count      int
	Access     Access
	UpdateDate string
	URL        string

	ImageURLs []string
	ImageIDs  []string
	ScrambleN int

	FromComic *Comic

	SavePath string
	Skip     bool
}

// Title 章节标题（章节名已带「第N话」时不再重复加前缀）。
func (c *Chapter) Title() string { return c.Name }

// IndexTitle 「第N话 标题」。
func (c *Chapter) IndexTitle() string {
	if chapterNameHasIndex(c.Name) {
		return c.Name
	}
	return "第" + itoa(c.Index) + "话 " + c.Name
}

// ComicName 所属漫画名。
func (c *Chapter) ComicName() string {
	if c.FromComic != nil {
		return c.FromComic.Name
	}
	return ""
}

// Author 作者（优先取所属漫画）。
func (c *Chapter) Author() string {
	if c.FromComic != nil {
		return c.FromComic.Author()
	}
	return DefaultAuthor
}

// Tags 标签（优先取所属漫画）。
func (c *Chapter) Tags() []string {
	if c.FromComic != nil {
		return c.FromComic.Tags
	}
	return nil
}

// SetImages 写入图片地址列表。
func (c *Chapter) SetImages(urls, ids []string) {
	c.ImageURLs = append([]string(nil), urls...)
	if len(ids) == 0 {
		c.ImageIDs = make([]string, len(c.ImageURLs))
	} else {
		c.ImageIDs = append([]string(nil), ids...)
	}
	c.Count = len(c.ImageURLs)
}

// ImageAt 取第 index 张（从 0 开始）。
func (c *Chapter) ImageAt(index int) (*Image, error) {
	if index < 0 || index >= len(c.ImageURLs) {
		return nil, Errorf("图片下标越界: chapter=%s index=%d 共%d张", c.ChapterID, index, len(c.ImageURLs))
	}
	img, err := NewImage(c.ChapterID, c.ImageURLs[index], index+1, c.ScrambleN)
	if err != nil {
		return nil, err
	}
	if index < len(c.ImageIDs) {
		img.ImgID = c.ImageIDs[index]
	}
	img.FromChapter = c
	return img, nil
}

// Comic 漫画实体。
type Comic struct {
	ComicID     string
	Name        string
	Authors     []string
	Tags        []string
	Description string
	Cover       string
	Serialize   string
	UpdateDate  string
	Views       int
	Score       float64
	Site        string
	URL         string
	Slug        string

	Chapters      []ChapterMeta
	ChapterAccess map[string]Access

	SavePath string
	Skip     bool
}

// Author 主作者。
func (c *Comic) Author() string {
	if len(c.Authors) > 0 && c.Authors[0] != "" {
		return c.Authors[0]
	}
	return DefaultAuthor
}

// ChapterCount 章节数。
func (c *Comic) ChapterCount() int { return len(c.Chapters) }

// IsFinished 是否已完结。
func (c *Comic) IsFinished() bool {
	return c.Serialize == "完结" || c.Serialize == "已完结" || c.Serialize == "finished"
}

// LatestChapterName 最新一话的名称。
func (c *Comic) LatestChapterName() string {
	if len(c.Chapters) == 0 {
		return ""
	}
	return c.Chapters[len(c.Chapters)-1].Name
}

// BuildChapter 由元信息构造章节对象。
func (c *Comic) BuildChapter(i int) (*Chapter, error) {
	if i < 0 || i >= len(c.Chapters) {
		return nil, Errorf("章节下标越界: comic=%s index=%d 共%d话", c.ComicID, i, len(c.Chapters))
	}
	meta := c.Chapters[i]
	access := Access{}
	if c.ChapterAccess != nil {
		access = c.ChapterAccess[meta.ChapterID]
	}
	return &Chapter{
		ChapterID: meta.ChapterID,
		Name:      meta.Name,
		ComicID:   c.ComicID,
		Index:     meta.Index,
		Count:     access.Pnum,
		Access:    access,
		FromComic: c,
	}, nil
}

// DistinctChapters 按序号排序、按 id 去重，并把序号重排为连续值。
func DistinctChapters(list []ChapterMeta) []ChapterMeta {
	seen := map[string]bool{}
	out := make([]ChapterMeta, 0, len(list))
	for _, m := range list {
		if m.ChapterID == "" || seen[m.ChapterID] {
			continue
		}
		seen[m.ChapterID] = true
		out = append(out, m)
	}
	for i := range out {
		out[i].Index = i + 1
	}
	return out
}

// ComicSummary 列表页里的漫画摘要。
type ComicSummary struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Author        string   `json:"author"`
	Cover         string   `json:"cover"`
	Text          string   `json:"text"`
	Serialize     string   `json:"serialize"`
	UpdateDate    string   `json:"update_date"`
	LatestChapter string   `json:"latest_chapter"`
	Views         int      `json:"views"`
	Score         float64  `json:"score"`
	Tags          []string `json:"tags"`
	Site          string   `json:"site"`
	URL           string   `json:"url"`
	Slug          string   `json:"slug"`
}

// SearchPage 分页结果。
type SearchPage struct {
	Items []ComicSummary `json:"items"`
	Total int            `json:"total"`
	Page  int            `json:"page"`
	Site  string         `json:"site"`
}

// RankNav 榜单导航项。
type RankNav struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// chapterNameHasIndex 判断章节名是否已经是「第N话/章/集」形式。
// 是的话就不再叠加序号前缀，避免出现「第1话 第01话」这种重复。
func chapterNameHasIndex(name string) bool {
	s := strings.TrimSpace(name)
	if !strings.HasPrefix(s, "第") {
		return false
	}
	rest := strings.TrimPrefix(s, "第")
	// 允许「第 3 话」这种带空格的写法
	for len(rest) > 0 && rest[0] == ' ' {
		rest = rest[1:]
	}
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	tail := strings.TrimLeft(rest[i:], " ")
	for _, suffix := range []string{"话", "話", "章", "集", "回", "节"} {
		if strings.HasPrefix(tail, suffix) {
			return true
		}
	}
	return false
}

// EnsureDir 创建目录。
func EnsureDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// JoinPath 统一用正斜杠拼接。
func JoinPath(parts ...string) string {
	return filepath.ToSlash(filepath.Join(parts...))
}
