package web

import (
	"strconv"
	"strings"

	"github.com/mccms/mccms-go/internal/mc"
)

// 站点匿名化：对前端只暴露 s1..sN 与「来源N」，真实站点 key 和域名不下发，
// 用户因此感知不到背后有多个站点。顺序取自 mc.AllSites，跨进程稳定。

func anonSourceKey(real string) string {
	for i, site := range mc.AllSites {
		if site == real {
			return "s" + strconv.Itoa(i+1)
		}
	}
	return real
}

func realSourceKey(anon string) string {
	if len(anon) < 2 || anon[0] != 's' {
		return ""
	}
	n, err := strconv.Atoi(anon[1:])
	if err != nil || n < 1 || n > len(mc.AllSites) {
		return ""
	}
	return mc.AllSites[n-1]
}

// anonSourceLabel 是给用户看的显示名：来源1、来源2……
func anonSourceLabel(anon string) string {
	if n := strings.TrimPrefix(anon, "s"); n != anon && n != "" {
		return "来源" + n
	}
	return "来源"
}
