package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mccms/mccms-go/internal/mc"
)

func TestSniffImageType(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}, "image/jpeg"},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00"), "image/png"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"gif", []byte("GIF89a....."), "image/gif"},
		{"unknown", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}, "application/octet-stream"},
	}
	for _, c := range cases {
		if got := sniffImageType(c.data); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 站点给的 Content-Type 并不可信（实测有 .webp 后缀实际是 JPEG），必须按魔数判断。
func TestSniffImageTypeBeatsLyingExtension(t *testing.T) {
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	if got := sniffImageType(jpegBytes); got != "image/jpeg" {
		t.Errorf("声称 .webp 的 JPEG 应识别为 image/jpeg，得到 %q", got)
	}
}

func TestCategoriesOf(t *testing.T) {
	for _, site := range mc.AllSites {
		cats := categoriesOf(site)
		if len(cats) < 2 {
			t.Errorf("%s 至少应有「全部」加一个选项，实得 %d", site, len(cats))
		}
		if cats[0].ID != "0" {
			t.Errorf("%s 第一项应当是「全部」", site)
		}
		for _, c := range cats {
			if c.ID == "" || c.Title == "" {
				t.Errorf("%s 出现空分类项: %+v", site, c)
			}
		}
	}
}

func TestCategoryOpts(t *testing.T) {
	// boylove 的免费/会员映射到 vip 参数
	if got := categoryOpts(mc.SiteBoylove, "free"); got["vip"] != "0" {
		t.Errorf("boylove free -> %v", got)
	}
	if got := categoryOpts(mc.SiteBoylove, "vip"); got["vip"] != "1" {
		t.Errorf("boylove vip -> %v", got)
	}
	// tibiu/manhwa 的排序项映射到 order
	if got := categoryOpts(mc.SiteTibiu, "hits"); got["order"] != "hits" {
		t.Errorf("tibiu hits -> %v", got)
	}
	// 「全部」不带任何筛选
	if got := categoryOpts(mc.SiteManhwa, "0"); len(got) != 0 {
		t.Errorf("全部不应带筛选参数，得到 %v", got)
	}
	// E-Hentai 的分类 key 直接透传给客户端（由它换算成分类位掩码）
	if got := categoryOpts(mc.SiteEhentai, "doujinshi"); got["category"] != "doujinshi" {
		t.Errorf("ehentai doujinshi -> %v", got)
	}
	if got := categoryOpts(mc.SiteEhentai, "0"); len(got) != 0 {
		t.Errorf("ehentai 全部不应带筛选参数，得到 %v", got)
	}
}

// 前端下拉框里的分类项，必须和客户端认识的 key 对得上。
func TestEhentaiCategoryKeysMatchClient(t *testing.T) {
	known := map[string]bool{
		"0": true, "doujinshi": true, "manga": true, "artistcg": true,
		"gamecg": true, "imageset": true, "cosplay": true, "western": true,
		"nonh": true, "asianporn": true, "misc": true,
	}
	for _, c := range categoriesOf(mc.SiteEhentai) {
		if !known[c.ID] {
			t.Errorf("前端给出了客户端不认识的分类 key: %q", c.ID)
		}
	}
}

func TestEnvelopeOK(t *testing.T) {
	rec := httptest.NewRecorder()
	ok(rec, map[string]any{"hello": "world"})

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["st"] != float64(stOK) {
		t.Errorf("st = %v, want %d", body["st"], stOK)
	}
	data, _ := body["data"].(map[string]any)
	if data["hello"] != "world" {
		t.Errorf("data 未透传: %v", body["data"])
	}
}

// 权限类错误必须映射成 1014，前端才会弹登录提示而不是当普通报错。
func TestEnvelopeAccessDenied(t *testing.T) {
	rec := httptest.NewRecorder()
	fail(rec, mc.RaiseAccessError("登录超时", nil, 2, ""))

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["st"] != float64(stNotLogin) {
		t.Errorf("权限错误 st = %v, want %d", body["st"], stNotLogin)
	}
	if msg, _ := body["msg"].(string); msg == "" {
		t.Error("权限错误应带可读提示")
	}
}

func TestEnvelopeGenericError(t *testing.T) {
	rec := httptest.NewRecorder()
	fail(rec, mc.Errorf("接口崩了"))

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["st"] != float64(stError) {
		t.Errorf("普通错误 st = %v, want %d", body["st"], stError)
	}
}

// newTestServer 构造测试用服务。
//
// 账号库与下载目录都指向 t.TempDir()，避免测试写入工作区。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{
		Site:         mc.SiteTibiu,
		AccountsPath: filepath.Join(t.TempDir(), "accounts.json"),
		DownloadDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSiteOf(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sites?site=boylove", nil)
	if got := s.siteOf(req); got != mc.SiteBoylove {
		t.Errorf("query 参数未生效: %q", got)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	req2.Header.Set("X-Mccms-Site", mc.SiteManhwa)
	if got := s.siteOf(req2); got != mc.SiteManhwa {
		t.Errorf("请求头未生效: %q", got)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/api/sites?site=不存在", nil)
	if got := s.siteOf(req3); got != mc.SiteTibiu {
		t.Errorf("未知站点应回落到默认站点，得到 %q", got)
	}
}

// SPA 深链接必须回退到 index.html，否则刷新页面会 404。
func TestStaticSPAFallback(t *testing.T) {
	s := newTestServer(t)
	handler := s.Handler()

	for _, path := range []string{"/", "/search", "/comic/17001", "/reader/291931"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s 返回 %d，期望 200", path, rec.Code)
		}
	}
}

func TestDirectoryTraversalBlocked(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../go.mod", nil))
	if rec.Code == http.StatusOK && len(rec.Body.String()) > 0 &&
		contains(rec.Body.String(), "module github.com/mccms") {
		t.Fatal("目录穿越未被拦截")
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
