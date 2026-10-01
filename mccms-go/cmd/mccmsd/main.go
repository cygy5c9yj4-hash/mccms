// Command mccmsd 是 mccms 的 Go 后端：单二进制提供 API 与前端。
//
// 用法：
//
//	mccmsd                         # 默认 127.0.0.1:8765
//	mccmsd -addr 0.0.0.0:8000      # 局域网访问
//	mccmsd -site boylove           # 默认站点
//	mccmsd -cookie "a=1;b=2"       # 带上你自己的会话
//	mccmsd -proxy http://127.0.0.1:7890
//	mccmsd -resolve boylove.cc=1.2.3.4
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/mccms/mccms-go/internal/mc"
	"github.com/mccms/mccms-go/internal/web"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	var (
		addr        = flag.String("addr", "127.0.0.1:8765", "监听地址")
		site        = flag.String("site", mc.SiteTibiu, "默认站点: tibiu / manhwa / boylove")
		downloadDir = flag.String("download-dir", "", "下载目录（默认 ./downloads）")
		proxy       = flag.String("proxy", "", "HTTP 代理；留空则用环境变量或系统代理")
		cookie      = flag.String("cookie", "", `会话 cookie，形如 "name=value; name2=value2"`)
		username    = flag.String("username", "", "站点账号（可选；仅用于访问该账号本身有权访问的内容）")
		password    = flag.String("password", "", "站点密码（可选）")
		imgThreads  = flag.Int("image-threads", 16, "图片并发数")
		chThreads   = flag.Int("chapter-threads", 4, "章节并发数")

		accountsPath = flag.String("accounts", "", "账号库路径（默认 <下载目录同级>/accounts.json）")
		noAccounts   = flag.Bool("no-accounts", false, "关闭自有账号体系（收藏/历史/笔记/管理后台不可用）")
		sessionDays  = flag.Int("session-days", 30, "登录会话有效期（天）")

		version = flag.Bool("version", false, "打印版本后退出")
	)
	var resolves stringList
	flag.Var(&resolves, "resolve", "host=ip 解析覆盖，可重复（等价 curl --resolve）")
	flag.Parse()

	if *version {
		fmt.Println("mccmsd 1.0.0")
		return
	}

	if !mc.KnownSite(*site) {
		log.Fatalf("未知站点 %q，可用: %v", *site, mc.AllSites)
	}

	resolveMap := map[string]string{}
	for _, item := range resolves {
		if i := strings.Index(item, "="); i > 0 {
			resolveMap[strings.ToLower(strings.TrimSpace(item[:i]))] = strings.TrimSpace(item[i+1:])
		}
	}

	server, err := web.New(web.Config{
		Addr:         *addr,
		Site:         *site,
		DownloadDir:  *downloadDir,
		Resolve:      resolveMap,
		Proxies:      *proxy,
		Cookies:      parseCookies(*cookie),
		Username:     *username,
		Password:     *password,
		ImageThreads: *imgThreads,
		ChapterThr:   *chThreads,

		AccountsPath:    *accountsPath,
		DisableAccounts: *noAccounts,
		SessionTTLDays:  *sessionDays,
	})
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}
	defer func() { _ = server.Close() }()

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server.Handler(),
	}

	log.Printf("mccms 后端已启动: http://%s/", *addr)
	log.Printf("默认站点: %s (%s)", *site, mc.SiteName(*site))
	if p := mc.DetectSystemProxy(); p != "" {
		log.Printf("检测到系统代理，将用于出站请求: %s", p)
	}

	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("服务退出: %v", err)
	}
	_ = os.Stdout
}

// parseCookies 解析 "a=1; b=2" 形式的 cookie 串。
func parseCookies(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.Index(part, "="); i > 0 {
			out[strings.TrimSpace(part[:i])] = strings.TrimSpace(part[i+1:])
		}
	}
	return out
}
