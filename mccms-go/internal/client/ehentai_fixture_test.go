package client

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mccms/mccms-go/internal/mc"
)

// 回归测试：为什么「只收 yaoi」必须走 gdata API，而不能只看搜索结果页。
//
// 站点的搜索结果页只展示**部分**标签。实测这份抓下来的真实响应里，
// 25 个画廊在 API 侧**全部**带 male:yaoi，但搜索页展示的标签里有 **9 个**
// 没把 yaoi 列出来（每个画廊真实有 19~33 个标签，页面只展示 12 个）。
//
// 如果只用搜索页标签判断：
//   - 误杀：这 9 个真 yaoi 画廊会被丢掉（召回率只剩 64%）
//   - 漏放：带 shotacon 等被排除标签的画廊，如果它没被展示出来，就会被错误收录
//
// 所以实现改为查 gdata API 拿完整标签，且查不到就不收录（fail-closed）。

func loadEHF(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读 fixture 失败: %v", err)
	}
	return b
}

// ehFixtureClient 造一个把 API 请求固定回放到 fixture 的客户端。
func ehFixtureClient(t *testing.T) *EhentaiClient {
	t.Helper()
	gdata := loadEHF(t, "ehentai_gdata.json")

	postman := mc.NewPostman(mc.PostmanConfig{Site: mc.SiteEhentai})
	postman.Client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if !strings.Contains(r.URL.Host, "api.e-hentai.org") {
				t.Errorf("只应请求元信息接口，实际请求了 %s", r.URL)
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(string(gdata))),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    r,
			}, nil
		}),
	}

	return NewEhentai(Base{SiteName: mc.SiteEhentai, HTTP: postman})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// 搜索页确实只能看到部分标签——这是本回归测试的前提。
func TestEHSearchPageTagsAreIncomplete(t *testing.T) {
	items, err := parseEHListPage(string(loadEHF(t, "ehentai_search.html")))
	if err != nil {
		t.Fatalf("解析搜索页失败: %v", err)
	}
	if len(items) != 25 {
		t.Fatalf("应当解析出 25 条，实得 %d", len(items))
	}

	seenYaoi := 0
	for _, it := range items {
		if keepGallery(it.Tags) {
			seenYaoi++
		}
	}
	if seenYaoi >= len(items) {
		t.Fatalf("前提不成立：搜索页标签竟然完整（%d/%d）", seenYaoi, len(items))
	}
	t.Logf("只看搜索页标签：%d/%d 条能判定为 yaoi（会误杀 %d 条）",
		seenYaoi, len(items), len(items)-seenYaoi)
}

// 走 API 之后能正确判定：25 条里有 24 条是真 yaoi 且不含被排除标签，
// 剩下 1 条虽然也是 yaoi，但带 male:shotacon，必须被挡掉。
//
// 这一条正是「必须走 API」的最好证明：它在搜索页展示的 12 个标签里
// **既没有 yaoi 也没有 shotacon**，只看搜索页会把它当成无关条目放过去。
func TestEHAPIEnrichmentIsAccurate(t *testing.T) {
	c := ehFixtureClient(t)

	items, err := parseEHListPage(string(loadEHF(t, "ehentai_search.html")))
	if err != nil {
		t.Fatalf("解析搜索页失败: %v", err)
	}

	naive := len(filterSummaries(items))
	enriched := c.enrichViaAPI(items)

	if len(enriched) != 24 {
		t.Fatalf("应当收录 24 条（25 条里 1 条带 shotacon 被排除），实得 %d", len(enriched))
	}
	if len(enriched) <= naive {
		t.Fatalf("走 API 应当明显多于只看搜索页标签（%d vs %d）", len(enriched), naive)
	}

	kept := map[string]bool{}
	for _, it := range enriched {
		kept[it.ID] = true

		if !keepGallery(it.Tags) {
			t.Errorf("混入非 yaoi 或含被排除标签的条目: %s | tags=%v", it.Name, it.Tags)
		}
		for _, tag := range it.Tags {
			if tagIsBlocked(tag) {
				t.Errorf("混入被排除标签 %q: %s", tag, it.Name)
			}
		}
		// 元信息也应当被 API 补全
		if it.Name == "" || it.Cover == "" {
			t.Errorf("元信息缺失: name=%q cover=%q", it.Name, it.Cover)
		}
		if strings.HasPrefix(it.Cover, "data:") {
			t.Errorf("封面仍是懒加载占位图: %s", it.Name)
		}
	}

	// 那条带 shotacon 的必须被挡掉——而它的搜索页标签里看不出任何异常
	if kept["4223476-31e3a8e793"] {
		t.Error("带 male:shotacon 的画廊被错误收录")
	}

	t.Logf("只看搜索页标签 %d 条 -> 走 API 后 %d 条（多恢复 %d 条真 yaoi，并正确挡掉 1 条带 shotacon 的）",
		naive, len(enriched), len(enriched)-naive)
}

// 把两种做法的差异量化固化：朴素过滤会**误杀**真 yaoi 画廊。
//
// 精确结论（基于这份真实抓取）：
//   - 只看搜索页标签：保留 15 条，丢掉 10 条
//     其中 1 条是真该丢的（带 male:shotacon），另外 9 条是货真价实的 yaoi
//   - 走 API：保留 24 条，丢掉 1 条（就是那条 shotacon）
//
// 也就是说朴素做法的召回率只有 15/24，漏掉 37.5% 的目标内容。
func TestEHNaiveFilterLosesRealYaoi(t *testing.T) {
	c := ehFixtureClient(t)

	items, _ := parseEHListPage(string(loadEHF(t, "ehentai_search.html")))
	naiveKept := map[string]bool{}
	for _, it := range filterSummaries(items) {
		naiveKept[it.ID] = true
	}
	apiKept := map[string]bool{}
	for _, it := range c.enrichViaAPI(items) {
		apiKept[it.ID] = true
	}

	// 真该丢的：带 shotacon，两种做法都应挡掉
	if naiveKept["4223476-31e3a8e793"] || apiKept["4223476-31e3a8e793"] {
		t.Error("带 male:shotacon 的画廊不应被收录")
	}

	// 被朴素做法误杀的（API 证明它们确实是 yaoi）
	type lost struct {
		id    string
		title string
	}
	var falseNegatives []lost
	for _, it := range items {
		if !naiveKept[it.ID] && apiKept[it.ID] {
			falseNegatives = append(falseNegatives, lost{it.ID, it.Name})
		}
	}
	if len(falseNegatives) != 9 {
		t.Fatalf("预期朴素做法误杀 9 条，实得 %d", len(falseNegatives))
	}
	for _, f := range falseNegatives[:3] {
		t.Logf("被误杀: %s (%s)", f.title, f.id)
	}
	t.Logf("朴素做法保留 %d 条；走 API 保留 %d 条（漏掉 %.1f%% 的目标内容）",
		len(naiveKept), len(apiKept), float64(len(falseNegatives))/float64(len(apiKept))*100)
}

// 封面必须取到真实地址：搜索页的 img src 是懒加载的 base64 占位图。
func TestEHCoverNotPlaceholder(t *testing.T) {
	c := ehFixtureClient(t)

	items, _ := parseEHListPage(string(loadEHF(t, "ehentai_search.html")))
	enriched := c.enrichViaAPI(items)
	if len(enriched) == 0 {
		t.Fatal("没有条目")
	}

	for _, it := range enriched {
		if !strings.HasPrefix(it.Cover, "http") {
			t.Errorf("封面不是绝对地址: %q", it.Cover)
		}
	}
	t.Logf("封面示例: %s", enriched[0].Cover)
}

// API 拿不到数据时必须整体放弃，而不是退回到「按搜索页标签猜」。
func TestEHAPIFailureIsFailClosed(t *testing.T) {
	postman := mc.NewPostman(mc.PostmanConfig{Site: mc.SiteEhentai})
	postman.Client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader("boom")),
				Header:     http.Header{},
				Request:    r,
			}, nil
		}),
	}
	c := NewEhentai(Base{SiteName: mc.SiteEhentai, HTTP: postman})

	items, _ := parseEHListPage(string(loadEHF(t, "ehentai_search.html")))
	if got := c.enrichViaAPI(items); len(got) != 0 {
		t.Fatalf("API 失败时应当一条都不收录，实得 %d 条", len(got))
	}
}

// 元信息接口的字段类型不稳定（posted/filecount 是字符串，filesize 是数字），
// 这里确认容错解析没漏字段。
func TestEHAPIMetaFlexibleTypes(t *testing.T) {
	c := ehFixtureClient(t)
	items, _ := parseEHListPage(string(loadEHF(t, "ehentai_search.html")))
	enriched := c.enrichViaAPI(items)

	withPages := 0
	for _, it := range enriched {
		if it.Views > 0 {
			withPages++
		}
	}
	if withPages == 0 {
		t.Fatal("没有一条解析出页数，说明 filecount 的字符串类型没被容错处理")
	}
	t.Logf("%d/%d 条解析出页数", withPages, len(enriched))
}
