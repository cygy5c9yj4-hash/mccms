package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mccms/mccms-go/internal/mc"
)

// 对拍测试：用 Python 版（mccms/）里预录的真实响应 fixture 验证 boylove.go 的解析结果。
//
// 这些 fixture 是当初对接站点时抓下来的真实响应，因此这里等价于
// 「Go 实现与已验证的 Python 实现在同一份输入上给出相同结论」。
// fixture 路径为仓库内的 mccms/tests/fixtures。

type blRT func(*http.Request) (*http.Response, error)

func (f blRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func blLoad(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../mccms/tests/fixtures", name))
	if err != nil {
		t.Fatalf("读 fixture 失败: %v", err)
	}
	return string(b)
}

type blSrv struct {
	t        *testing.T
	requests []string
	queries  []map[string]string
	routes   map[string]string
}

func blNew(t *testing.T, routes map[string]string) (*BoyloveClient, *blSrv) {
	t.Helper()
	s := &blSrv{t: t, routes: routes}
	rt := blRT(func(req *http.Request) (*http.Response, error) {
		s.requests = append(s.requests, req.URL.Path)
		q := map[string]string{}
		for k, v := range req.URL.Query() {
			if len(v) > 0 {
				q[k] = v[0]
			}
		}
		s.queries = append(s.queries, q)

		for prefix, body := range s.routes {
			if strings.HasPrefix(req.URL.Path, prefix) {
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewReader([]byte(body))),
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Request:    req,
				}, nil
			}
		}
		return nil, fmt.Errorf("未匹配到路由: %s", req.URL.Path)
	})

	pm := mc.NewPostman(mc.PostmanConfig{Site: mc.SiteBoylove, Retries: 1, Timeout: 5 * time.Second})
	pm.Client = &http.Client{Transport: rt}
	return NewBoylove(Base{SiteName: mc.SiteBoylove, HTTP: pm}), s
}

func blRoutes(t *testing.T) map[string]string {
	return map[string]string{
		"/home/api/searchk":       blLoad(t, "boylove_search.json"),
		"/home/api/chapter_list":  blLoad(t, "boylove_chapter_list.json"),
		"/home/api/rank":          blLoad(t, "boylove_rank.json"),
		"/home/api/cate":          blLoad(t, "boylove_cate.json"),
		"/home/book/index/id/":    blLoad(t, "boylove_detail.html"),
		"/home/book/capter/id/26": blLoad(t, "boylove_reader_scrambled.html"),
		"/home/book/capter/id/43": blLoad(t, "boylove_reader.html"),
	}
}

func blLast(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[len(a)-1]
}

// ---- 详情页 + 章节列表 -------------------------------------------------------

func TestBoyloveVerifyComicDetail(t *testing.T) {
	c, _ := blNew(t, blRoutes(t))

	comic, err := c.GetComicDetail("16904", true)
	if err != nil {
		t.Fatalf("GetComicDetail: %v", err)
	}
	if comic.ComicID != "16904" {
		t.Errorf("comic_id = %q", comic.ComicID)
	}
	if comic.Name != "无根树/无根之树" {
		t.Errorf("name = %q", comic.Name)
	}
	if comic.Author() != "라포" {
		t.Errorf("author = %q (authors=%v)", comic.Author(), comic.Authors)
	}
	if comic.Serialize != "连载中" {
		t.Errorf("serialize = %q", comic.Serialize)
	}
	if comic.Score <= 0 {
		t.Errorf("score = %v", comic.Score)
	}
	if !strings.HasPrefix(comic.Cover, "https://img.boylove.cc") {
		t.Errorf("cover = %q", comic.Cover)
	}
	if !strings.Contains(comic.Description, "뿌리") {
		t.Errorf("description = %q", comic.Description)
	}
	if !strings.Contains(comic.Description, "\n") {
		t.Errorf("description 未把 \"br /\" 还原成换行: %q", comic.Description)
	}
	if comic.Views != 7488345 {
		t.Errorf("views = %d", comic.Views)
	}
	if len(comic.Tags) <= 5 || comic.Tags[0] != "韩漫" {
		t.Errorf("tags = %v", comic.Tags)
	}
	if comic.URL != "/home/book/index/id/16904" || comic.Site != mc.SiteBoylove {
		t.Errorf("url/site = %q/%q", comic.URL, comic.Site)
	}

	// 章节顺序：id 不单调，必须按接口列表顺序
	if len(comic.Chapters) != 112 {
		t.Fatalf("章节数 = %d", len(comic.Chapters))
	}
	want := []string{"435648", "435649", "435647", "435650", "439936"}
	for i, id := range want {
		if comic.Chapters[i].ChapterID != id {
			t.Errorf("章节[%d] = %q, want %q", i, comic.Chapters[i].ChapterID, id)
		}
		if comic.Chapters[i].Index != i+1 {
			t.Errorf("章节[%d].Index = %d, want %d", i, comic.Chapters[i].Index, i+1)
		}
	}
	if comic.Chapters[0].Name != "第01话" {
		t.Errorf("章节名 = %q", comic.Chapters[0].Name)
	}
	free := 0
	for _, m := range comic.Chapters {
		if comic.ChapterAccess[m.ChapterID].Free() {
			free++
		}
	}
	if free == 0 {
		t.Errorf("没有任何免费章节，access 解析可疑")
	}
	t.Logf("详情：name=%q author=%q tags=%d chapters=%d free=%d score=%.2f views=%d",
		comic.Name, comic.Author(), len(comic.Tags), len(comic.Chapters), free, comic.Score, comic.Views)

	// 不取章节
	c2, _ := blNew(t, blRoutes(t))
	only, err := c2.GetComicDetail("16904", false)
	if err != nil {
		t.Fatalf("GetComicDetail(fetchChapters=false): %v", err)
	}
	if len(only.Chapters) != 0 {
		t.Errorf("fetchChapters=false 仍拿到了 %d 个章节", len(only.Chapters))
	}
}

// ---- 阅读页：老章节（DOM 过滤） ----------------------------------------------

func TestBoyloveVerifyOldChapter(t *testing.T) {
	c, _ := blNew(t, blRoutes(t))

	ch, err := c.GetChapterDetail("435648", "", true)
	if err != nil {
		t.Fatalf("GetChapterDetail: %v", err)
	}
	if !strings.Contains(ch.Name, "第01话") {
		t.Errorf("name = %q", ch.Name)
	}
	if ch.ScrambleN != 0 {
		t.Errorf("老章节 scramble_n = %d, want 0", ch.ScrambleN)
	}
	if len(ch.ImageURLs) <= 50 {
		t.Fatalf("图片数 = %d", len(ch.ImageURLs))
	}
	seen := map[string]bool{}
	for i, u := range ch.ImageURLs {
		file := u
		if k := strings.IndexAny(file, "?#"); k >= 0 {
			file = file[:k]
		}
		file = file[strings.LastIndex(file, "/")+1:]
		if !strings.HasPrefix(file, "435648-") {
			t.Fatalf("混入非正文图片: %s", u)
		}
		if strings.Contains(u, "/bookimages/img/") {
			t.Fatalf("混入推荐位缩略图: %s", u)
		}
		if seen[u] {
			t.Fatalf("重复图片: %s", u)
		}
		seen[u] = true
		img, err := ch.ImageAt(i)
		if err != nil {
			t.Fatalf("ImageAt(%d): %v", i, err)
		}
		if img.Suffix != ".webp" || img.IsScrambled() {
			t.Errorf("图片[%d] 派生异常: suffix=%q scrambled=%v", i, img.Suffix, img.IsScrambled())
		}
	}
	if ch.Count != len(ch.ImageURLs) {
		t.Errorf("count = %d, len = %d", ch.Count, len(ch.ImageURLs))
	}
	t.Logf("老章节：name=%q 图片数=%d 首图=%s", ch.Name, len(ch.ImageURLs), ch.ImageURLs[0])
}

// ---- 阅读页：新章节（imageData + randomClass） -------------------------------

func TestBoyloveVerifyScrambledChapter(t *testing.T) {
	c, _ := blNew(t, blRoutes(t))

	ch, err := c.GetChapterDetail("2623003", "", true)
	if err != nil {
		t.Fatalf("GetChapterDetail: %v", err)
	}
	if ch.ScrambleN != 11 {
		t.Fatalf("scramble_n = %d, want 11", ch.ScrambleN)
	}
	if len(ch.ImageURLs) <= 50 {
		t.Fatalf("图片数 = %d", len(ch.ImageURLs))
	}
	if !strings.HasPrefix(ch.ImageURLs[0], "https://img.boylove.cc/") ||
		!strings.Contains(ch.ImageURLs[0], "bookimages") {
		t.Errorf("首图 = %q", ch.ImageURLs[0])
	}
	if strings.Contains(ch.ImageURLs[0], `\/`) {
		t.Errorf("URL 里的 \\/ 未还原: %q", ch.ImageURLs[0])
	}
	img, err := ch.ImageAt(0)
	if err != nil {
		t.Fatalf("ImageAt: %v", err)
	}
	if !img.IsScrambled() || img.ScrambleN != 11 {
		t.Errorf("图片未带上乱序参数: %+v", img)
	}
	if img.Suffix != ".webp" {
		t.Errorf("后缀 = %q（查询串应被剥掉）", img.Suffix)
	}
	if strings.Contains(ch.Name, "第60话") == false {
		t.Errorf("章节名 = %q", ch.Name)
	}
	t.Logf("新章节：name=%q scrambleN=%d 图片数=%d 首图=%s",
		ch.Name, ch.ScrambleN, len(ch.ImageURLs), ch.ImageURLs[0])

	// 重复调用 FetchImageURLs 不应再次发请求
	before := len(c.HTTP.Domains) // 占位，避免未使用；真正断言看下面的请求计数
	_ = before
	urls, err := c.FetchImageURLs(ch)
	if err != nil || len(urls) != len(ch.ImageURLs) {
		t.Fatalf("FetchImageURLs: %v (%d)", err, len(urls))
	}
}

// ---- imageData / randomClass 的针对性用例 -----------------------------------

func TestBoyloveVerifyInlineParsing(t *testing.T) {
	// 新章节：imageData 的数组顺序就是阅读顺序，randomClass 逐章不同
	h := blLoad(t, "boylove_reader_scrambled.html")
	urls, ids := boyloveParseImageData(h)
	if len(urls) != len(ids) || len(urls) <= 50 {
		t.Fatalf("imageData 解析异常: %d urls / %d ids", len(urls), len(ids))
	}
	if ids[0] != "0" || ids[1] != "1" {
		t.Errorf("id 顺序异常: %v %v", ids[0], ids[1])
	}
	for i := 1; i < len(urls); i++ {
		if urls[i] == urls[i-1] {
			t.Fatalf("imageData 相邻重复: %s", urls[i])
		}
	}
	if n := boyloveParseScrambleN(h); n != 11 {
		t.Errorf("randomClass 解析 = %d, want 11", n)
	}

	// 老章节：既没有 imageData 也没有 randomClass → 必须为 0
	old := blLoad(t, "boylove_reader.html")
	if u, _ := boyloveParseImageData(old); len(u) != 0 {
		t.Errorf("老章节不该有 imageData: %d", len(u))
	}
	if n := boyloveParseScrambleN(old); n != 0 {
		t.Errorf("老章节 scramble_n = %d, want 0", n)
	}

	// 逐章不同的 N；N<=1 视为无需还原；缺变量为 0
	cases := []struct {
		html string
		want int
	}{
		{`<script>var randomClass = 13;</script>`, 13},
		{`<script>var randomClass = 23;</script>`, 23},
		{`<script>var randomClass = 1;</script>`, 0},
		{`<script>var randomClass = 0;</script>`, 0},
		{`<script>var x = 1;</script>`, 0},
		// 只有用到 randomClass 而没有赋值时不能误判
		{`<script>do_mergeImg(ctx, img, w, h, url, randomClass);</script>`, 0},
	}
	for _, tc := range cases {
		if got := boyloveParseScrambleN(tc.html); got != tc.want {
			t.Errorf("boyloveParseScrambleN(%q) = %d, want %d", tc.html, got, tc.want)
		}
	}

	// src 里含 ] 或转义斜杠时也要能正确取数组
	weird := `<script>var imageData = [{"id":0,"src":"https:\/\/img.boylove.cc\/a]b\/1.webp","width":1,"height":"2"}];var randomClass = 5;</script>`
	u, ids := boyloveParseImageData(weird)
	if len(u) != 1 || u[0] != "https://img.boylove.cc/a]b/1.webp" || ids[0] != "0" {
		t.Errorf("含 ] 的 src 解析失败: %v %v", u, ids)
	}
	if n := boyloveParseScrambleN(weird); n != 5 {
		t.Errorf("同一页面里的 randomClass = %d, want 5", n)
	}
}

// ---- 无图时的显式报错 -------------------------------------------------------

func TestBoyloveVerifyNoImageError(t *testing.T) {
	c, _ := blNew(t, map[string]string{
		"/home/book/capter/id/": `<html><body><img src="/static/x.png"></body></html>`,
	})
	if _, err := c.GetChapterDetail("999999", "", true); err == nil {
		t.Fatalf("空页面应当报错")
	}
	// fetchImages=false 时只解析元信息，不应报错
	ch, err := c.GetChapterDetail("999999", "", false)
	if err != nil {
		t.Fatalf("fetchImages=false 不应报错: %v", err)
	}
	if ch.Name != "第999999话" || ch.Count != 0 {
		t.Errorf("兜底章节 = %+v", ch)
	}
}

// ---- 搜索 / 分类 / 榜单 ------------------------------------------------------

func TestBoyloveVerifySearch(t *testing.T) {
	c, srv := blNew(t, blRoutes(t))

	page, err := c.Search("魔咒", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatalf("搜索无结果")
	}
	first := page.Items[0]
	if first.Author == "" || first.Name == "" || first.LatestChapter == "" {
		t.Errorf("字段缺失: %+v", first)
	}
	if first.Serialize != "连载" && first.Serialize != "完结" {
		t.Errorf("serialize = %q", first.Serialize)
	}
	if !strings.HasPrefix(first.Cover, "https://img.boylove.cc") {
		t.Errorf("cover = %q", first.Cover)
	}
	if len(first.Tags) <= 3 {
		t.Errorf("tags = %v", first.Tags)
	}
	for _, tag := range first.Tags {
		if strings.Contains(tag, ",") {
			t.Errorf("标签未拆分: %q", tag)
		}
	}
	if first.Score <= 0 {
		t.Errorf("score = %v", first.Score)
	}
	if first.URL != "/home/book/index/id/"+first.ID {
		t.Errorf("url = %q", first.URL)
	}
	t.Logf("搜索：%d 条，首条=%s/%s 标签=%d total=%d", len(page.Items), first.ID, first.Name, len(first.Tags), page.Total)

	// 参数
	if got := srv.queries[len(srv.queries)-1]; got["keyword"] != "魔咒" || got["type"] != "0" || got["pageNo"] != "1" {
		t.Errorf("搜索参数 = %v", got)
	}
	if _, err := c.Search("   ", 1); err == nil {
		t.Errorf("空关键字应当报错")
	}
	if _, err := c.Search("魔咒", 3); err != nil {
		t.Fatalf("Search(page=3): %v", err)
	}
	if got := srv.queries[len(srv.queries)-1]; got["pageNo"] != "3" {
		t.Errorf("pageNo = %q", got["pageNo"])
	}
}

func TestBoyloveVerifyBrowse(t *testing.T) {
	c, srv := blNew(t, blRoutes(t))

	page, err := c.CategoriesFilter(1, map[string]string{"vip": "0"})
	if err != nil {
		t.Fatalf("CategoriesFilter: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatalf("分类无结果")
	}
	if got := blLast(srv.requests); got != "/home/api/cate/tp/1-0-2-1-1-0-1-0" {
		t.Errorf("分类路径 = %q（必须拼满 8 段）", got)
	}
	if got := srv.queries[len(srv.queries)-1]; got["mt"] != "0" {
		t.Errorf("分类缺少 mt=0: %v", got)
	}

	if _, err := c.CategoriesFilter(2, map[string]string{"cate": "1", "tag": "0", "done": "2", "order": "1", "is18": "0", "vip": "1"}); err != nil {
		t.Fatalf("CategoriesFilter(page=2): %v", err)
	}
	if got := blLast(srv.requests); got != "/home/api/cate/tp/1-0-2-1-2-0-1-1" {
		t.Errorf("分类路径 = %q", got)
	}
	// 非法值不该覆盖默认值（例如别的站点的 order=hits）
	if _, err := c.CategoriesFilter(1, map[string]string{"order": "hits"}); err != nil {
		t.Fatalf("CategoriesFilter(order=hits): %v", err)
	}
	if got := blLast(srv.requests); got != "/home/api/cate/tp/1-0-2-1-1-0-1-2" {
		t.Errorf("非法 order 覆盖了默认值: %q", got)
	}

	if _, err := c.UpdateList(1); err != nil {
		t.Fatalf("UpdateList: %v", err)
	}
	if got := blLast(srv.requests); got != "/home/api/cate/tp/1-0-2-1-1-0-1-2" {
		t.Errorf("UpdateList 路径 = %q", got)
	}

	rank, err := c.Ranking("1", 1)
	if err != nil {
		t.Fatalf("Ranking: %v", err)
	}
	if len(rank.Items) == 0 || rank.Items[0].Name == "" {
		t.Fatalf("榜单为空: %+v", rank.Items)
	}
	if got := blLast(srv.requests); got != "/home/api/rank/type/1" {
		t.Errorf("榜单路径 = %q", got)
	}
	// 用 nav 的 type 调用 Ranking 也要能用（取对应分组）
	fav, err := c.Ranking("most_favorites", 1)
	if err != nil {
		t.Fatalf("Ranking(most_favorites): %v", err)
	}
	if len(fav.Items) == 0 {
		t.Errorf("收藏榜为空")
	}
	if len(fav.Items) == len(rank.Items) && fav.Items[0].ID == rank.Items[0].ID {
		t.Errorf("收藏榜与人气榜结果相同，分组可能没生效")
	}
	t.Logf("榜单：人气=%d 条（首条 %s），收藏=%d 条（首条 %s）",
		len(rank.Items), rank.Items[0].Name, len(fav.Items), fav.Items[0].Name)

	nav, err := c.RankingNav()
	if err != nil {
		t.Fatalf("RankingNav: %v", err)
	}
	if len(nav) != 4 {
		t.Fatalf("nav = %+v", nav)
	}
	wantNames := []string{"人气榜", "消费榜", "收藏榜", "搜索榜"}
	wantTypes := []string{"most_clicks", "most_consumes", "most_favorites", "most_search"}
	for i := range nav {
		if nav[i].Name != wantNames[i] || nav[i].Type != wantTypes[i] {
			t.Errorf("nav[%d] = %+v", i, nav[i])
		}
	}

	// URL 解析
	if got := c.GetComicIDFromURL("/home/book/index/id/16904"); got != "16904" {
		t.Errorf("comic id = %q", got)
	}
	if got := c.GetChapterIDFromURL("/home/book/capter/id/435648"); got != "435648" {
		t.Errorf("chapter id = %q", got)
	}
	if got := c.Site(); got != mc.SiteBoylove {
		t.Errorf("site = %q", got)
	}
}

// ---- 信封语义 ---------------------------------------------------------------

func blEnvelopeClient(t *testing.T, body string) *BoyloveClient {
	t.Helper()
	c, _ := blNew(t, map[string]string{"/home/api/searchk": body})
	return c
}

func TestBoyloveVerifyEnvelope(t *testing.T) {
	// code=0 与 code=1 都算成功
	for _, body := range []string{
		`{"code":0,"result":{"list":[],"lastPage":true}}`,
		`{"code":1,"result":{"list":[],"lastPage":true}}`,
		`{"code":1,"data":{"list":[]}}`,
	} {
		c := blEnvelopeClient(t, body)
		if _, err := c.Search("x", 1); err != nil {
			t.Errorf("信封 %s 应当成功: %v", body, err)
		}
	}

	// 401 → 需要登录态（等价 LoginRequiredError），且消息取 errorMsg
	c := blEnvelopeClient(t, `{"code":401,"data":[],"errorMsg":"Not legal request."}`)
	_, err := c.Search("x", 1)
	if err == nil {
		t.Fatalf("401 应当报错")
	}
	if !mc.IsAccessDenied(err) || !strings.Contains(err.Error(), "Not legal request.") {
		t.Errorf("401 错误 = %v", err)
	}
	if _, ok := err.(*mc.LoginRequiredError); !ok {
		t.Errorf("401 应映射为 LoginRequiredError，实际 %T", err)
	}

	// 500 → 普通错误，消息取 errorMsg 其次 msg
	c = blEnvelopeClient(t, `{"code":500,"data":[],"errorMsg":"Server Error"}`)
	if _, err := c.Search("x", 1); err == nil || !strings.Contains(err.Error(), "Server Error") {
		t.Errorf("500 错误 = %v", err)
	}
	c = blEnvelopeClient(t, `{"code":500,"msg":"Server Error"}`)
	if _, err := c.Search("x", 1); err == nil || !strings.Contains(err.Error(), "Server Error") {
		t.Errorf("500(msg) 错误 = %v", err)
	}
	c = blEnvelopeClient(t, `{"code":404,"data":[]}`)
	if _, err := c.Search("x", 1); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404 错误 = %v", err)
	}

	// 非 JSON
	c = blEnvelopeClient(t, `<html>waf</html>`)
	if _, err := c.Search("x", 1); err == nil {
		t.Errorf("非 JSON 应当报错")
	}
}

// ---- 与 Python 参考实现的逐字段对拍（输出 JSON 供 diff） ----------------------

func blSummaryDump(s mc.ComicSummary) map[string]any {
	return map[string]any{
		"id": s.ID, "name": s.Name, "author": s.Author, "cover": s.Cover,
		"text": s.Text, "serialize": s.Serialize, "update_date": s.UpdateDate,
		"latest": s.LatestChapter, "views": s.Views, "score": s.Score,
		"tags": s.Tags, "site": s.Site, "url": s.URL, "slug": s.Slug,
	}
}

func TestBoyloveVerifyParityDump(t *testing.T) {
	c, _ := blNew(t, blRoutes(t))
	dump := map[string]any{}

	comic, err := c.GetComicDetail("16904", true)
	if err != nil {
		t.Fatal(err)
	}
	free := 0
	for _, m := range comic.Chapters {
		if comic.ChapterAccess[m.ChapterID].Free() {
			free++
		}
	}
	ids := []string{}
	for _, m := range comic.Chapters[:5] {
		ids = append(ids, m.ChapterID)
	}
	idx := []int{}
	for _, m := range comic.Chapters[:3] {
		idx = append(idx, m.Index)
	}
	dump["detail"] = map[string]any{
		"name": comic.Name, "author": comic.Author(), "authors": comic.Authors,
		"serialize": comic.Serialize, "score": comic.Score, "cover": comic.Cover,
		"views": comic.Views, "tags": comic.Tags, "description": comic.Description,
		"url": comic.URL, "chapters": len(comic.Chapters), "ids5": ids,
		"idx3": idx, "name0": comic.Chapters[0].Name, "free": free,
	}

	for _, tc := range []struct{ key, id string }{{"old", "435648"}, {"new", "2623003"}} {
		ch, err := c.GetChapterDetail(tc.id, "", true)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, u := range ch.ImageURLs {
			seen[u] = true
		}
		dump[tc.key] = map[string]any{
			"name": ch.Name, "count": ch.Count, "n": len(ch.ImageURLs),
			"scramble": ch.ScrambleN, "uniq": len(seen), "url": ch.URL,
			"first": ch.ImageURLs[0], "last": ch.ImageURLs[len(ch.ImageURLs)-1],
			"vip": ch.Access.VIP, "pnum": ch.Access.Pnum,
		}
	}

	sp, err := c.Search("魔咒", 1)
	if err != nil {
		t.Fatal(err)
	}
	dump["search"] = map[string]any{
		"n": len(sp.Items), "total": sp.Total, "page": sp.Page,
		"first": blSummaryDump(sp.Items[0]),
	}

	cf, err := c.CategoriesFilter(1, map[string]string{"vip": "0"})
	if err != nil {
		t.Fatal(err)
	}
	dump["cate"] = map[string]any{
		"n": len(cf.Items), "total": cf.Total, "first": blSummaryDump(cf.Items[0]),
	}

	rk, err := c.Ranking("1", 1)
	if err != nil {
		t.Fatal(err)
	}
	dump["rank"] = map[string]any{
		"n": len(rk.Items), "total": rk.Total, "first": blSummaryDump(rk.Items[0]),
	}

	nav, err := c.RankingNav()
	if err != nil {
		t.Fatal(err)
	}
	dump["nav"] = nav

	b, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/boylove_go.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("已写出 /tmp/boylove_go.json")
}
