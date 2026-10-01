package mc

import (
	"strings"
	"testing"
)

func TestParseToID(t *testing.T) {
	cases := map[string]string{
		"17001":                               "17001",
		"/comic/17001":                        "17001",
		"https://cache.tibiu.net/comic/17001": "17001",
		"/chapter/17001/291931":               "291931",
		"https://www.manhwa.wang/index.php/chapter/936939": "936939",
		"https://boylove.cc/home/book/capter/id/435648":    "435648",
	}
	for in, want := range cases {
		if got := ParseToID(in); got != want {
			t.Errorf("ParseToID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseIDsFromURL(t *testing.T) {
	if got := ParseChapterIDFromURL("/chapter/17001/291931"); got != "291931" {
		t.Errorf("TIBIU 章节 id = %q", got)
	}
	if got := ParseComicIDFromURL("/comic/17001"); got != "17001" {
		t.Errorf("漫画 id = %q", got)
	}
	if got := ParseSlugFromURL("/index.php/comic/mozhouweixianzaoyu"); got != "mozhouweixianzaoyu" {
		t.Errorf("slug = %q", got)
	}
	if got := ParseSlugFromURL("/index.php/comic/27032"); got != "" {
		t.Errorf("数字 id 不应被当作 slug，得到 %q", got)
	}
}

func TestParseHumanNumber(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{"1487", 1487},
		{"14 万", 140000},
		{"1.2亿", 120000000},
		{"", 0},
		{nil, 0},
		{"1,234", 1234},
		{42, 42},
	}
	for _, c := range cases {
		if got := ParseHumanNumber(c.in); got != c.want {
			t.Errorf("ParseHumanNumber(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFixWinDirName(t *testing.T) {
	if got := FixWinDirName("a/b:c*d?e"); got != "a_b_c_d_e" {
		t.Errorf("got %q", got)
	}
	if got := FixWinDirName("社内恋爱/内部爱情"); got != "社内恋爱_内部爱情" {
		t.Errorf("got %q", got)
	}
	if got := FixWinDirName("trailing..."); strings.HasSuffix(got, ".") {
		t.Errorf("尾部点未清理: %q", got)
	}
}

func TestOrigName(t *testing.T) {
	if got := OrigName("喂我吃吧 老師! [漢化組] [DL版]"); got != "喂我吃吧 老師!" {
		t.Errorf("got %q", got)
	}
}

func TestIndexOfChapterName(t *testing.T) {
	c := &Chapter{Name: "暴君", Index: 3}
	if got := c.IndexTitle(); got != "第3话 暴君" {
		t.Errorf("got %q", got)
	}
	// 章节名本身已带序号时不重复加前缀
	c2 := &Chapter{Name: "第3章：暴君", Index: 3}
	if got := c2.IndexTitle(); got != "第3章：暴君" {
		t.Errorf("got %q", got)
	}
}

func TestDirRule(t *testing.T) {
	comic := &Comic{
		ComicID:   "17001",
		Name:      "社内恋爱/内部爱情",
		Authors:   []string{"이로비"},
		Serialize: "完结",
		Chapters: []ChapterMeta{
			{ChapterID: "291931", Index: 1, Name: "第01话"},
			{ChapterID: "292046", Index: 2, Name: "第54话"},
		},
	}
	chapter, err := comic.BuildChapter(0)
	if err != nil {
		t.Fatal(err)
	}

	d, err := NewDirRule("Bd_Cname_Chindextitle", "/tmp/base", "")
	if err != nil {
		t.Fatal(err)
	}

	root, err := d.ComicRoot(comic)
	if err != nil {
		t.Fatal(err)
	}
	if root != "/tmp/base/社内恋爱_内部爱情" {
		t.Errorf("漫画根目录 = %q", root)
	}

	path, err := d.ApplyPath(comic, chapter, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/base/社内恋爱_内部爱情/第01话" {
		t.Errorf("章节路径 = %q", path)
	}
}

func TestDirRuleTemplateAndPadding(t *testing.T) {
	comic := &Comic{ComicID: "17001", Name: "n", Authors: []string{"a"}}
	chapter := &Chapter{ChapterID: "9", Name: "c", Index: 2, ComicID: "17001"}

	d, _ := NewDirRule("Bd/{Cid}/{Chindex:03}", "/tmp/base", "")
	got, err := d.ApplyPath(comic, chapter, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/base/17001/002" {
		t.Errorf("模板路径 = %q", got)
	}
}

// 含下划线的字段必须用 / 分隔（用 _ 会被当成两个片段）。
func TestDirRuleUnderscoreFieldNeedsSlash(t *testing.T) {
	comic := &Comic{ComicID: "1", Name: "n"}
	chapter := &Chapter{ChapterID: "9", Name: "c", Index: 1, UpdateDate: "2024-03-04"}

	bad, _ := NewDirRule("Bd_Chupdate_date", "/tmp/base", "")
	if _, err := bad.ApplyPath(comic, chapter, false); err == nil {
		t.Error("用 _ 分隔含下划线字段应当报错")
	}

	good, _ := NewDirRule("Bd/Chupdate_date", "/tmp/base", "")
	got, err := good.ApplyPath(comic, chapter, false)
	if err != nil {
		t.Fatalf("用 / 分隔应当成功: %v", err)
	}
	if got != "/tmp/base/2024-03-04" {
		t.Errorf("got %q", got)
	}
}

// 求漫画根目录时必须跳过纯章节片段。
func TestComicRootSkipsChapterSegments(t *testing.T) {
	comic := &Comic{ComicID: "1", Name: "n", Authors: []string{"a"}}
	d, _ := NewDirRule("Bd_Cname_Chindextitle", "/tmp/base", "")
	got, err := d.ComicRoot(comic)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/base/n" {
		t.Errorf("got %q", got)
	}
}

func TestAccessDesc(t *testing.T) {
	if got := (Access{}).Desc(); got != "免费" {
		t.Errorf("got %q", got)
	}
	if got := (Access{VIP: 1}).Desc(); got != "VIP" {
		t.Errorf("got %q", got)
	}
	if got := (Access{Cion: 5}).Desc(); got != "金币" {
		t.Errorf("got %q", got)
	}
	if !(Access{}).Free() {
		t.Error("空权益应为免费")
	}
}

func TestRaiseAccessError(t *testing.T) {
	err := RaiseAccessError("登录超时", nil, 2, "")
	if !IsAccessDenied(err) {
		t.Error("code=2 应当映射为权限类错误")
	}
	_, isLogin := err.(*LoginRequiredError)
	if !isLogin {
		t.Errorf("code=2 应当映射为 LoginRequiredError，实际 %T", err)
	}

	err2 := RaiseAccessError("需要VIP", nil, 3, "vip")
	if _, isVip := err2.(*VipRequiredError); !isVip {
		t.Errorf("type=vip 应当映射为 VipRequiredError，实际 %T", err2)
	}
	if !strings.Contains(err2.Error(), "会员") {
		t.Errorf("错误信息应包含可读提示: %q", err2.Error())
	}
}

func TestKnownSite(t *testing.T) {
	for _, s := range AllSites {
		if !KnownSite(s) {
			t.Errorf("%s 应当是已知站点", s)
		}
	}
	if KnownSite("nope") {
		t.Error("未知站点不应通过校验")
	}
}
