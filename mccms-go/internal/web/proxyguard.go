package web

import (
	"net"
	"net/url"
	"strings"
)

// 图片代理会按调用方给的 url 去取内容，如果不加约束，任何能访问到本服务的人
// 都可以拿它去探测内网（SSRF）。默认只监听 127.0.0.1 时风险有限，
// 但一旦用 -addr 0.0.0.0 暴露出去就是实打实的跳板，所以这里统一挡掉。

// isSafeProxyTarget 判断代理目标是否允许访问。
//
// 规则：只允许 http/https；主机必须解析到公网地址。
// 解析到回环 / 私有 / 链路本地地址的一律拒绝（含「公私都有」的解析结果，
// 以覆盖 DNS rebinding 的情况）。
func isSafeProxyTarget(raw string) (bool, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, "缺少 url"
	}

	u, err := url.Parse(raw)
	if err != nil {
		return false, "url 解析失败"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false, "只允许 http/https"
	}

	host := u.Hostname()
	if host == "" {
		return false, "缺少主机名"
	}

	// 字面量 IP 直接判断，省掉一次解析
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return false, "目标地址不在公网范围"
		}
		return true, ""
	}

	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false, "域名解析失败"
	}
	for _, ip := range ips {
		if isFakeProxyIP(ip) {
			// 本机代理（Clash 等）处于 fake-ip 模式时，所有被代理的域名都会被解析到
			// 198.18.0.0/15 这个占位段，它并不反映真实地址。
			// 这里跳过它——真实地址由代理解析，本地无从判定；
			// 而字面量写内网 IP 的情况在上面已经被挡住了。
			continue
		}
		if !isPublicIP(ip) {
			return false, "目标地址不在公网范围"
		}
	}
	return true, ""
}

// isFakeProxyIP 判断是否为代理工具的 fake-ip 占位地址（198.18.0.0/15）。
func isFakeProxyIP(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 198 && (v4[1] == 18 || v4[1] == 19)
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// 100.64.0.0/10（运营商级 NAT）与 192.0.0.0/24 等特殊段
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return false
		}
		if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
			return false
		}
	}
	return true
}
