# mccms-go

mccms 的 **Go 后端**：单二进制同时提供 HTTP API 与前端页面。

前端沿用 [JM-Aura](https://codeberg.org/Tom6814/JM-Aura) 的 React 19 + MUI 7 界面，
按本项目做了三处改造：**主题改为玫红色**、**适配本后端的多站点 API**、**新增首页**。

---

## 快速开始

```bash
# 1) 构建前端（产物会输出到 internal/web/dist，由 go:embed 打进二进制）
cd web-frontend
npm install
npm run build

# 2) 构建并启动后端
cd ../mccms-go
go build -o mccmsd ./cmd/mccmsd
./mccmsd                      # http://127.0.0.1:8765
```

启动参数：

| 参数 | 说明 |
|---|---|
| `-addr` | 监听地址，默认 `127.0.0.1:8765`；局域网访问用 `0.0.0.0:8000` |
| `-site` | 默认站点：`tibiu` / `manhwa` / `boylove` / `ehentai` |
| `-download-dir` | 下载目录，默认 `./downloads` |
| `-proxy` | HTTP 代理；留空则用环境变量或系统代理（macOS 会自动读 `scutil --proxy`） |
| `-cookie` | 会话 cookie，形如 `"a=1; b=2"` |
| `-username` / `-password` | 站点账号（可选，仅用于访问该账号本身有权访问的内容） |
| `-image-threads` / `-chapter-threads` | 并发数，默认 16 / 4 |
| `-resolve host=ip` | 解析覆盖，可重复（等价 `curl --resolve`） |

---

## 架构

```
mccms-go/
├── cmd/mccmsd/          单二进制入口
└── internal/
    ├── mc/              核心：实体、配置、dir_rule DSL、HTTP 会话、错误体系
    ├── client/          站点客户端：接口 + tibiu / manhwa / boylove / ehentai 四个实现
    ├── decode/          图片竖带倒序还原
    ├── download/        下载编排 + 后台任务管理
    └── web/             HTTP 服务：JM-Aura 兼容 API + go:embed 前端
        └── dist/        前端构建产物（go:embed）
```

依赖只有三个：`golang.org/x/net/html`（页面解析）、`golang.org/x/image/webp`（WebP 解码）、
`gopkg.in/yaml.v3`（配置）。**单二进制约 12MB，部署只需要一个文件。**

---

## 前端改造点

### 1. 主题：玫红色

`web-frontend/src/theme.ts` 是一套完整的 Material Design 3 令牌体系（30 个 color role × 明暗两套）。
主色由原来的暖橙改为玫红：

| role | dark | light |
|---|---|---|
| `primary` | `#FFB1C8` | `#A83B63` |
| `onPrimary` | `#5E1130` | `#FFFFFF` |
| `primaryContainer` | `#7D2948` | `#FFD9E2` |
| `surface` | `#1C1417` | `#FFF7F8` |

40 组前景/背景配对的 WCAG 对比度**全部 ≥ 4.5:1**，五级 surface 容器的明度阶梯保持单调。

### 2. 适配本后端

- **站点切换**：新增 `src/site.tsx`，把当前站点存进 `localStorage`，并在 `api.ts` 里
  给每个请求自动注入 `site=` 参数；顶栏加了站点选择器，切换后整页刷新。
- **导航裁剪**：小说 / 阅读笔记 / 收藏夹 / 阅读历史依赖上游 JM 的账号体系，本后端没有实现，
  已从导航移除，只保留本后端真正支持的能力。
- **图片解码归后端**：`/api/v2/jm/chapter` 返回的 `raw.scramble_id` 固定为 `"0"`，
  这样前端的 `DscImage` 不会再去套用 JM 的横向切片算法（本项目的乱序是竖带倒序，由后端还原）。
- **`rawListToSummaries` 不再拼 JM 的 CDN 域名**（那是错误域名），改为使用后端给的绝对地址。

### 3. 首页

`src/pages/Home.tsx` 重写为：站点信息 + 搜索框 + 站点切换的 Hero，下面按
`/api/promote` 返回的分区（排行榜 / 最近更新 / 热门）横向铺开封面。

---

## API

前端用到的接口全部实现；响应统一是 `{st, msg, data}` 信封，`st=1001` 成功、`st=1014` 需要登录。

### 前端接口

| 接口 | 说明 |
|---|---|
| `GET /api/sites` | 站点列表与能力矩阵（按客户端实际实现的方法探测） |
| `GET /api/config` | 运行配置 |
| `GET /api/site/me` | 当前站点登录态 |
| `GET /api/promote` | 首页分区（排行榜 / 最近更新 / 热门） |
| `GET /api/latest?page=` | 最近更新 |
| `GET /api/v2/jm/search?q=&page=` | 搜索（E-Hentai 源结果恒为 yaoi） |
| `GET /api/v2/jm/categories` | 分类选项 |
| `GET /api/v2/jm/leaderboard?sort=&category=&page=` | 排行榜 |
| `GET /api/v2/jm/random` | 随机一本 |
| `GET /api/v2/jm/comic/{id}` | 漫画详情（含章节列表） |
| `GET /api/v2/jm/chapter/{id}?album_id=` | 章节详情（含图片地址） |
| `GET /api/chapter_image/{chapterId}/{name}` | 按章节取图（自动还原乱序） |
| `GET /api/image-proxy?url=` | 图片代理（自动还原乱序） |
| `POST /api/v2/jm/download/tasks` | 提交下载任务 |
| `GET /api/v2/jm/download/tasks[/{id}]` | 任务列表 / 单个任务 |

以上接口都接受 `?site=` 指定站点（或 `X-Mccms-Site` 请求头），默认取启动参数里的站点。

### 未实现的部分

小说、评论、收藏夹、阅读历史、阅读笔记、自建账号体系——这些依赖上游 JM 的能力，
本后端返回**空数据而不是报错**，避免前端整页崩掉。

---

## E-Hentai 源（只收 yaoi）

`-site ehentai` 或在前端把站点切成 E-Hentai。**该源只收录 yaoi**，其余一律不要。

### 为什么必须查元信息接口

站点的搜索结果页只展示**部分**标签。实测抓下来的一份真实响应里：

| 判定方式 | 收录数 | 说明 |
|---|---|---|
| 只看搜索页标签 | 15 / 25 | **误杀 9 条真 yaoi**（召回率 62.5%） |
| 查 gdata API 拿完整标签 | 24 / 25 | 正确挡掉 1 条带 `male:shotacon` 的 |

25 个画廊在 API 侧**全部**带 `male:yaoi`，但搜索页每个只展示 12 个标签（实际有 19~33 个），
其中 9 个恰好没把 yaoi 列出来。只看搜索页既会误杀真内容，也可能放行本该排除的标签。

所以实现是：**搜索页只用来拿候选列表，判定一律以
[`api.e-hentai.org/api.php`](https://api.e-hentai.org/api.php) 的 `gdata` 完整标签为准**
（一次请求最多 25 条）。**查不到就不收录**（fail-closed）——宁可少收，也不能破坏约束。

### 硬性排除

除要求 `male:yaoi` 外，命中以下标签片段的画廊会被直接丢弃：
`lolicon` / `shotacon` / `toddlercon` / `lolita` / `loli` / `shota`。
这类标签在同类站点上常与 yaoi 同时出现，必须在入口挡住。

回归测试 `internal/client/ehentai_fixture_test.go` 用真实抓取的响应把上述数字固化了下来。

### 数据模型与图片直链

- **一个画廊 = 一部作品**：E-Hentai 没有章节层级，所以画廊映射为漫画，并给它**唯一的章节**
  （章节 id 与画廊 id 相同，形如 `4223931-37d6418440`）。下载目录用 `Bd_Cname`，不再套「第 N 话」。
- **图片直链是延迟解析的**：列表给的是图片页地址（`/s/{hash}/{gid}-{n}`），
  真正的直链要再访问一次图片页、从 `#img` 取。一个画廊可能上百页，逐张预解析不现实，
  因此客户端实现 `ImageResolver`，由下载器与图片代理在需要某一张时才解析（带缓存）。

## 图片乱序还原

香香腐宅的新章节会把整页图切成 N 条**竖带**倒序下发，前端原本用 canvas 还原。
本后端在下载与图片代理两处都做了还原（`internal/decode`），对使用者透明。

- N 逐章不同（实测有 11 / 13 / 18 / 23），从阅读页的 `randomClass` 读取，**不写死**。
- 老章节没有该标记，图片按字节原样落盘，不做重新编码。
- `height >= 4000` 的超高图站点前端是原样拷贝，本库保持一致，避免弄坏正常的长条漫。

---

## 测试

```bash
go test ./...      # 38 个用例
go vet ./...
```

- `internal/decode`：还原算法的自逆性、余数条带映射、超高图跳过等。
- `internal/mc`：id 解析、dir_rule DSL（含 `{Chindex:03}` 零填充、含下划线字段必须用 `/` 分隔）、
  权限错误映射。
- `internal/client`：**对拍测试**——用对接站点时预录的真实响应 fixture（`testdata/` 下），
  验证解析结论。
- `internal/web`：信封语义、权限错误映射到 1014、SPA 深链接回退、目录穿越拦截、
  `Content-Type` 魔数嗅探、图片代理的 SSRF 防护。
- `internal/client/ehentai_fixture_test.go`：用真实抓取的搜索页 + 元信息响应，
  固化「只收 yaoi」的判定链路与召回率数字。

实网测试默认跳过：

```bash
MCCMS_LIVE=1 go test ./internal/client/ -run Live -v
```

---

## 图片代理的安全约束

`/api/image-proxy` 会按调用方给的 url 去取内容，因此加了两道约束：

- 只允许 `http` / `https`；
- 目标必须解析到公网地址，字面量写的内网 / 回环 / 链路本地地址（含云元数据
  `169.254.169.254`）一律 403——否则用 `-addr 0.0.0.0` 暴露出去就成了 SSRF 跳板。

例外：本机代理处于 **fake-ip** 模式时（Clash 默认把被代理域名解析到 `198.18.0.0/15`），
该占位段不反映真实地址，会被跳过——真实地址由代理解析，本地无从判定。

## 访问权限

本后端只访问**公开内容**与**当前会话本身有权访问的内容**。

需要登录或会员的章节会返回明确的权限错误（前端显示为登录提示），
不会做任何绕过访问控制的尝试。要访问你自己的权益，请通过 `-cookie`
或 `-username` / `-password` 提供你自己的账号。

`-resolve` 是标准的 `curl --resolve` 等价能力，用于镜像站点或本机 DNS 解析异常的场景。

---

