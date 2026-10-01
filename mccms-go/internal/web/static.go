package web

import "embed"

// distFS 内嵌前端构建产物。
//
// 构建前端：cd web-frontend && npm install && npm run build
// 产物会输出到 internal/web/dist。
//
//go:embed all:dist
var distFS embed.FS
