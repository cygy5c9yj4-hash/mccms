package client

import (
	"os"
	"strings"
	"testing"

	"github.com/mccms/mccms-go/internal/mc"
)

// 实网测试：默认跳过，设置 MCCMS_LIVE=1 才跑。
//
//	MCCMS_LIVE=1 go test ./internal/client/ -run Live -v
func liveEhentai(t *testing.T) *EhentaiClient {
	t.Helper()
	if os.Getenv("MCCMS_LIVE") == "" {
		t.Skip("实网测试，设置 MCCMS_LIVE=1 运行")
	}
	return NewEhentai(Base{
		SiteName: mc.SiteEhentai,
		HTTP:     mc.NewPostman(mc.PostmanConfig{Site: mc.SiteEhentai}),
	})
}

// 核心不变量：无论站点怎么返回，收录进来的每一条都必须真的带 yaoi 标签。
func TestLiveEhentaiSearchOnlyYaoi(t *testing.T) {
	c := liveEhentai(t)

	page, err := c.Search("", 1)
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("没有搜到任何 yaoi 条目")
	}

	for _, it := range page.Items {
		if !keepGallery(it.Tags) {
			t.Errorf("混入了非 yaoi 条目: %s | tags=%v", it.Name, it.Tags)
		}
		if it.ID == "" || it.Name == "" {
			t.Errorf("条目字段缺失: %+v", it)
		}
	}
	t.Logf("搜索到 %d 条 yaoi（站点报告总数 %d）", len(page.Items), page.Total)
}

// 未成年人相关内容必须被挡掉。
func TestLiveEhentaiBlocksMinorTags(t *testing.T) {
	c := liveEhentai(t)

	if keepGallery([]string{"male:yaoi", "male:shotacon"}) {
		t.Error("带 shotacon 的条目不应被收录")
	}
	if keepGallery([]string{"male:yaoi", "female:lolicon"}) {
		t.Error("带 lolicon 的条目不应被收录")
	}
	if keepGallery([]string{"male:yaoi", "other:toddlercon"}) {
		t.Error("带 toddlercon 的条目不应被收录")
	}
	if !keepGallery([]string{"male:yaoi", "language:chinese"}) {
		t.Error("正常 yaoi 条目应被收录")
	}

	// 实网翻几页，确认没有被漏网的
	for p := 1; p <= 3; p++ {
		page, err := c.Search("", p)
		if err != nil {
			t.Fatalf("第 %d 页搜索失败: %v", p, err)
		}
		for _, it := range page.Items {
			for _, tag := range it.Tags {
				if tagIsBlocked(tag) {
					t.Errorf("第 %d 页混入被排除标签: %s | %s", p, it.Name, tag)
				}
			}
		}
	}
}

func TestLiveEhentaiComicAndChapter(t *testing.T) {
	c := liveEhentai(t)

	page, err := c.Search("", 1)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("搜索失败: %v", err)
	}
	target := page.Items[0]
	t.Logf("目标画廊: %s (%s)", target.Name, target.ID)

	comic, err := c.GetComicDetail(target.ID, true)
	if err != nil {
		t.Fatalf("详情失败: %v", err)
	}
	if comic.Name == "" {
		t.Error("详情没有标题")
	}
	if len(comic.Chapters) != 1 {
		t.Errorf("画廊应当只有 1 个章节，实得 %d", len(comic.Chapters))
	}
	t.Logf("详情: %s | 作者=%v | 标签=%v", comic.Name, comic.Authors, comic.Tags)

	chapter, err := c.GetChapterDetail(target.ID, target.ID, true)
	if err != nil {
		t.Fatalf("章节失败: %v", err)
	}
	if len(chapter.ImageURLs) == 0 {
		t.Fatal("章节没有图片")
	}
	t.Logf("章节: %s | 共 %d 页", chapter.Name, len(chapter.ImageURLs))

	// 解析第一张的直链
	first := chapter.ImageURLs[0]
	name := chapter.ImageIDs[0]
	direct, err := c.ResolveImage(chapter.ChapterID, name)
	if err != nil {
		t.Fatalf("解析直链失败: %v", err)
	}
	if !strings.HasPrefix(direct, "http") {
		t.Errorf("直链格式不对: %s", direct)
	}
	if strings.Contains(direct, "509.gif") {
		t.Error("命中了配额占位图")
	}
	t.Logf("图片页 %s -> 直链 %s", first, direct[:min(70, len(direct))])

	// 再解析一次应当命中缓存（这里只验证结果一致）
	again, err := c.ResolveImage(chapter.ChapterID, name)
	if err != nil || again != direct {
		t.Errorf("二次解析结果不一致: %v / %s", err, again)
	}
}

func TestLiveEhentaiUpdateListAndCategories(t *testing.T) {
	c := liveEhentai(t)

	up, err := c.UpdateList(1)
	if err != nil {
		t.Fatalf("最近更新失败: %v", err)
	}
	for _, it := range up.Items {
		if !keepGallery(it.Tags) {
			t.Errorf("最近更新混入非 yaoi: %s", it.Name)
		}
	}
	t.Logf("最近更新 %d 条", len(up.Items))

	cat, err := c.CategoriesFilter(1, map[string]string{"category": "doujinshi"})
	if err != nil {
		t.Fatalf("分类浏览失败: %v", err)
	}
	for _, it := range cat.Items {
		if !keepGallery(it.Tags) {
			t.Errorf("分类浏览混入非 yaoi: %s", it.Name)
		}
	}
	t.Logf("分类（同人志）%d 条", len(cat.Items))
}

func TestEhentaiIDParsing(t *testing.T) {
	cases := map[string]string{
		"4223931-37d6418440":                         "4223931-37d6418440",
		"https://e-hentai.org/g/4223931/37d6418440/": "4223931-37d6418440",
		"https://exhentai.org/g/1234567/abcdef0123/": "1234567-abcdef0123",
	}
	c := NewEhentai(Base{})
	for in, want := range cases {
		if got := c.GetComicIDFromURL(in); got != want {
			t.Errorf("GetComicIDFromURL(%q) = %q, want %q", in, got, want)
		}
	}

	if _, _, err := parseEHComicID("坏的id"); err == nil {
		t.Error("非法 id 应当报错")
	}
}

func TestEhentaiTagFiltering(t *testing.T) {
	cases := []struct {
		tags []string
		want bool
	}{
		{[]string{"male:yaoi"}, true},
		{[]string{"yaoi"}, true},
		{[]string{"male:yaoi", "language:chinese"}, true},
		{[]string{"male:yaoi", "male:shotacon"}, false},
		{[]string{"male:yaoi", "female:lolicon"}, false},
		{[]string{"male:yaoi", "other:toddlercon"}, false},
		{[]string{"female:sole female"}, false},
		{[]string{}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := keepGallery(c.tags); got != c.want {
			t.Errorf("keepGallery(%v) = %v, want %v", c.tags, got, c.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
