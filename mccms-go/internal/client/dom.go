// Package client 实现各站点的抓取逻辑。
package client

import (
	"strings"

	"golang.org/x/net/html"
)

// ---- 极简 DOM 工具 ----------------------------------------------------------
//
// 站点页面需要的选择器很有限（按 class / id / 属性找元素、取文本与属性），
// 这里直接用 x/net/html 遍历实现，避免引入 CSS 选择器依赖。

// ParseHTML 解析 HTML。
func ParseHTML(s string) (*html.Node, error) {
	return html.Parse(strings.NewReader(s))
}

// Walk 遍历节点树（含自身）。
func Walk(n *html.Node, fn func(*html.Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		Walk(c, fn)
	}
}

// FindAll 返回所有满足条件的节点。
func FindAll(root *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	Walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && pred(n) {
			out = append(out, n)
		}
		return true
	})
	return out
}

// Find 返回第一个满足条件的节点。
func Find(root *html.Node, pred func(*html.Node) bool) *html.Node {
	var found *html.Node
	Walk(root, func(n *html.Node) bool {
		if found != nil {
			return false
		}
		if n.Type == html.ElementNode && pred(n) {
			found = n
			return false
		}
		return true
	})
	return found
}

// Attr 取属性值。
func Attr(n *html.Node, name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// HasClass 是否含指定 class。
func HasClass(n *html.Node, class string) bool {
	if n == nil {
		return false
	}
	for _, c := range strings.Fields(Attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// ByTag 按标签名查找（在 root 子树内，含自身）。
func ByTag(root *html.Node, tag string) []*html.Node {
	return FindAll(root, func(n *html.Node) bool { return n.Data == tag })
}

// FirstByTag 第一个指定标签。
func FirstByTag(root *html.Node, tag string) *html.Node {
	return Find(root, func(n *html.Node) bool { return n.Data == tag })
}

// ByClass 按 class 查找。
func ByClass(root *html.Node, class string) []*html.Node {
	return FindAll(root, func(n *html.Node) bool { return HasClass(n, class) })
}

// FirstByClass 第一个含指定 class 的节点。
func FirstByClass(root *html.Node, class string) *html.Node {
	return Find(root, func(n *html.Node) bool { return HasClass(n, class) })
}

// ByAttr 按属性名（可选值）查找。
func ByAttr(root *html.Node, name, value string) []*html.Node {
	return FindAll(root, func(n *html.Node) bool {
		v, ok := attrOK(n, name)
		if !ok {
			return false
		}
		return value == "" || v == value
	})
}

// FirstByAttr 第一个匹配属性的节点。
func FirstByAttr(root *html.Node, name, value string) *html.Node {
	return Find(root, func(n *html.Node) bool {
		v, ok := attrOK(n, name)
		if !ok {
			return false
		}
		return value == "" || v == value
	})
}

// FirstByTagClass 第一个同时满足标签与 class 的节点。
func FirstByTagClass(root *html.Node, tag, class string) *html.Node {
	return Find(root, func(n *html.Node) bool {
		return n.Data == tag && HasClass(n, class)
	})
}

// ByTagClass 所有同时满足标签与 class 的节点。
func ByTagClass(root *html.Node, tag, class string) []*html.Node {
	return FindAll(root, func(n *html.Node) bool {
		return n.Data == tag && HasClass(n, class)
	})
}

// ClassContains 按 class 子串查找（用于 `de-info__box` 这类 BEM 命名）。
func ClassContains(root *html.Node, sub string) []*html.Node {
	return FindAll(root, func(n *html.Node) bool {
		return strings.Contains(Attr(n, "class"), sub)
	})
}

// FirstClassContains 第一个 class 含子串的节点。
func FirstClassContains(root *html.Node, sub string) *html.Node {
	return Find(root, func(n *html.Node) bool {
		return strings.Contains(Attr(n, "class"), sub)
	})
}

// Text 取节点及其后代的可见文本（跳过 script/style），压缩空白。
func Text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style") {
			return
		}
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
			sb.WriteString(" ")
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

// OwnText 只取直接文本子节点。
func OwnText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
			sb.WriteString(" ")
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

// Children 直接子元素。
func Children(n *html.Node) []*html.Node {
	var out []*html.Node
	if n == nil {
		return out
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// MetaContent 取 <meta name=... 或 property=...> 的 content。
func MetaContent(root *html.Node, key string) string {
	n := Find(root, func(n *html.Node) bool {
		if n.Data != "meta" {
			return false
		}
		return Attr(n, "name") == key || Attr(n, "property") == key
	})
	return Attr(n, "content")
}

// TitleText 取 <title>。
func TitleText(root *html.Node) string {
	return Text(FirstByTag(root, "title"))
}

func attrOK(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}
