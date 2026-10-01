package mc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Postman 是站点级别的 HTTP 会话：
// 保持 cookie、支持域名轮换重试、可配置代理与 host->ip 解析覆盖。
type Postman struct {
	Site    string
	Domains []string
	Retries int
	Timeout time.Duration
	Headers map[string]string
	Resolve map[string]string // host -> ip，等价 curl --resolve
	Proxies string            // 形如 http://127.0.0.1:7890；为空则用环境变量/系统代理
	Client  *http.Client

	mu        sync.Mutex
	domainIdx int
	cookies   map[string]string
	failed    map[string]int
}

// PostmanConfig 构造参数。
type PostmanConfig struct {
	Site    string
	Domains []string
	Retries int
	Timeout time.Duration
	Headers map[string]string
	Resolve map[string]string
	Proxies string
}

// NewPostman 创建会话。
func NewPostman(cfg PostmanConfig) *Postman {
	domains := cfg.Domains
	if len(domains) == 0 {
		domains = SiteDomains[cfg.Site]
	}
	retries := cfg.Retries
	if retries <= 0 {
		retries = DefaultRetries
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout * time.Second
	}
	headers := cfg.Headers
	if headers == nil {
		headers = DefaultHeaders()
	}

	proxy := cfg.Proxies
	if proxy == "" {
		proxy = DetectSystemProxy()
	}

	p := &Postman{
		Site:    cfg.Site,
		Domains: domains,
		Retries: retries,
		Timeout: timeout,
		Headers: headers,
		Resolve: cfg.Resolve,
		Proxies: proxy,
		cookies: map[string]string{},
		failed:  map[string]int{},
	}
	p.Client = p.buildClient()
	return p
}

func (p *Postman) buildClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}

	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err == nil && p.Resolve != nil {
			if ip, ok := p.Resolve[strings.ToLower(host)]; ok && ip != "" {
				addr = net.JoinHostPort(ip, port)
			}
		}
		return dialer.DialContext(ctx, network, addr)
	}

	transport := &http.Transport{
		DialContext:           dialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		ForceAttemptHTTP2:     true,
	}

	if p.Proxies != "" {
		// 配置了代理时，DialContext 由代理接管；解析覆盖仍然生效
		transport.Proxy = func(*http.Request) (*url.URL, error) {
			return url.Parse(p.Proxies)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	return &http.Client{
		Transport: transport,
		Timeout:   p.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			return nil
		},
	}
}

// Close 释放连接。
func (p *Postman) Close() {
	if p.Client != nil {
		p.Client.CloseIdleConnections()
	}
}

// CurrentDomain 当前使用的域名。
func (p *Postman) CurrentDomain() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Domains) == 0 {
		return ""
	}
	return p.Domains[p.domainIdx%len(p.Domains)]
}

func (p *Postman) switchDomain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Domains) > 1 {
		p.domainIdx = (p.domainIdx + 1) % len(p.Domains)
	}
}

// SetCookies 写入会话 cookie。
func (p *Postman) SetCookies(cookies map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range cookies {
		p.cookies[k] = v
	}
}

// Cookies 当前会话 cookie。
func (p *Postman) Cookies() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]string, len(p.cookies))
	for k, v := range p.cookies {
		out[k] = v
	}
	return out
}

// BuildURL 拼接完整 URL。
func (p *Postman) BuildURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "https://" + p.CurrentDomain() + path
}

// Response 是对响应的封装。
type Response struct {
	StatusCode int
	Body       []byte
	Header     http.Header
	URL        string
}

// Text 返回文本。
func (r *Response) Text() string { return string(r.Body) }

// OK 是否 2xx/3xx。
func (r *Response) OK() bool { return r.StatusCode >= 200 && r.StatusCode < 400 }

// Get 发起 GET（带域名轮换重试）。
func (p *Postman) Get(path string) (*Response, error) {
	return p.Do(http.MethodGet, path, nil, nil)
}

// GetWith 带 query 的 GET。
func (p *Postman) GetWith(path string, query map[string]string) (*Response, error) {
	return p.Do(http.MethodGet, path, query, nil)
}

// PostJSON 发送 JSON 请求体。
func (p *Postman) PostJSON(path string, payload any) (*Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{}
	for k, v := range p.Headers {
		headers[k] = v
	}
	headers["Content-Type"] = "application/json"

	// 借用一次请求，把 Content-Type 单独带上
	saved := p.Headers
	p.mu.Lock()
	p.Headers = headers
	p.mu.Unlock()

	resp, err := p.Do(http.MethodPost, path, nil, bytes.NewReader(body))

	p.mu.Lock()
	p.Headers = saved
	p.mu.Unlock()

	return resp, err
}

// Do 发起请求。
func (p *Postman) Do(method, path string, query map[string]string, body io.Reader) (*Response, error) {
	var lastErr error
	attempts := p.Retries
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		req, err := p.newRequest(method, path, query, body)
		if err != nil {
			return nil, err
		}

		resp, err := p.Client.Do(req)
		if err != nil {
			lastErr = err
			p.onFailure()
			continue
		}

		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			p.onFailure()
			continue
		}

		p.captureCookies(resp)

		out := &Response{StatusCode: resp.StatusCode, Body: data, Header: resp.Header, URL: req.URL.String()}
		if out.OK() {
			// 注意：这里持锁期间不能调用 CurrentDomain()——它内部也会加锁，
			// sync.Mutex 不可重入，会直接自死锁。改为直接读同包字段。
			p.mu.Lock()
			if len(p.Domains) > 0 {
				delete(p.failed, p.Domains[p.domainIdx%len(p.Domains)])
			}
			p.mu.Unlock()
			return out, nil
		}

		lastErr = fmt.Errorf("http %d", resp.StatusCode)
		if attempt < attempts-1 {
			p.onFailure()
			time.Sleep(time.Duration(150*(attempt+1)) * time.Millisecond)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("请求失败")
	}
	return nil, Errorf("请求失败: [%s] 已重试 %d 次: %v", p.BuildURL(path), attempts, lastErr)
}

func (p *Postman) newRequest(method, path string, query map[string]string, body io.Reader) (*http.Request, error) {
	full := p.BuildURL(path)

	if len(query) > 0 {
		u, err := url.Parse(full)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		full = u.String()
	}

	req, err := http.NewRequest(method, full, body)
	if err != nil {
		return nil, err
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	p.mu.Lock()
	var cookieHeader string
	if len(p.cookies) > 0 {
		parts := make([]string, 0, len(p.cookies))
		for k, v := range p.cookies {
			parts = append(parts, k+"="+v)
		}
		cookieHeader = strings.Join(parts, "; ")
	}
	p.mu.Unlock()
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	return req, nil
}

func (p *Postman) captureCookies(resp *http.Response) {
	if len(resp.Cookies()) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range resp.Cookies() {
		p.cookies[c.Name] = c.Value
	}
}

func (p *Postman) onFailure() {
	p.mu.Lock()
	defer p.mu.Unlock()
	domain := ""
	if len(p.Domains) > 0 {
		domain = p.Domains[p.domainIdx%len(p.Domains)]
	}
	p.failed[domain]++
	if p.failed[domain] >= 2 && len(p.Domains) > 1 {
		p.domainIdx = (p.domainIdx + 1) % len(p.Domains)
		delete(p.failed, domain)
	}
}

// ---- 系统代理 ---------------------------------------------------------------

var (
	sysProxyOnce sync.Once
	sysProxy     string
)

// DetectSystemProxy 读取操作系统级代理设置。
//
// Go 的 http.ProxyFromEnvironment 只看环境变量，不读 macOS 的系统代理配置；
// 而本机访问部分站点恰恰依赖系统代理，所以这里显式探测一次。
func DetectSystemProxy() string {
	sysProxyOnce.Do(func() {
		if runtime.GOOS != "darwin" {
			return
		}
		out, err := exec.Command("scutil", "--proxy").Output()
		if err != nil {
			return
		}
		text := string(out)
		if !strings.Contains(text, "HTTPEnable : 1") && !strings.Contains(text, "HTTPSEnable : 1") {
			return
		}
		host := scutilValue(text, "HTTPSProxy")
		port := scutilValue(text, "HTTPSPort")
		if host == "" {
			host = scutilValue(text, "HTTPProxy")
			port = scutilValue(text, "HTTPPort")
		}
		if host == "" || port == "" {
			return
		}
		sysProxy = "http://" + host + ":" + port
	})
	return sysProxy
}

func scutilValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+" :") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+" :"))
		}
	}
	return ""
}

// IntToStr 便捷转换。
func IntToStr(i int) string { return strconv.Itoa(i) }
