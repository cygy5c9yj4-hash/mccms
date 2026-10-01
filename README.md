# mccms

多站点漫画抓取 / 阅读 / 下载工具。

后端是 **Go 单二进制**（内嵌 React 前端），前端基于 [JM-Aura](https://codeberg.org/Tom6814/JM-Aura)
的界面改造而来：**主题改为玫红**、**适配本后端的多站点 API**、**重写首页**。

支持四个源：

| 源 | 说明 |
|---|---|
| TIBIU | `cache.tibiu.net`，自研 JSON API |
| 漫蛙 | `www.manhwa.wang`，Mccms JSON + HTML |
| 香香腐宅 | `boylove.cc`，网页端 JSON API |
| E-Hentai | `e-hentai.org`，**只收录 yaoi** |

---

## 快速开始

```bash
# 1) 构建前端（产物会输出到 mccms-go/internal/web/dist，由 go:embed 打进二进制）
cd web-frontend
npm install
npm run build

# 2) 构建并启动后端
cd ../mccms-go
go build -o mccmsd ./cmd/mccmsd
./mccmsd                      # http://127.0.0.1:8765
```

启动参数：

```bash
./mccmsd -addr 0.0.0.0:8000            # 局域网访问（手机也能看）
./mccmsd -site boylove                 # 默认站点
./mccmsd -cookie "PHPSESSID=xxx"       # 带上你自己的会话
./mccmsd -proxy http://127.0.0.1:7890  # 显式指定代理
./mccmsd -resolve boylove.cc=1.2.3.4   # 解析覆盖，可重复（等价 curl --resolve）
```

完整参数与架构说明见 [`mccms-go/README.md`](mccms-go/README.md)。

---

## 目录结构

```
.
├── mccms-go/                Go 后端（线上使用）
│   ├── cmd/mccmsd/          单二进制入口
│   └── internal/
│       ├── mc/              实体、配置、dir_rule DSL、HTTP 会话、错误体系
│       ├── client/          四站点客户端
│       ├── decode/          图片竖带倒序还原
│       ├── download/        下载编排 + 后台任务
│       └── web/             HTTP API + go:embed 前端
│
├── web-frontend/            React 前端（JM-Aura 改造版）
├── Dockerfile               多阶段构建（前端 → 单二进制）
└── start-server.sh          部署脚本（本机专用，未入库）
```

---

## 几个实现上的坑（都有测试固化）

- **图片乱序**：香香腐宅的新章节把整页图切成 N 条**竖带**倒序下发（不是 JM 的横向切片）。
  N 逐章不同，从阅读页的 `randomClass` 读取，**不写死**。还原算法在 `internal/decode`，
  下载与图片代理两处都会自动还原。判定方法是竖带接缝的 z 检验：还原前 ≈ +5.6，还原后 ≈ 0.0。
- **E-Hentai 只收 yaoi**：站点搜索结果页**只展示 12 个标签**（实际有 19~33 个），
  只看搜索页会误杀约 37.5% 的真 yaoi，还可能放行本该排除的标签。
  所以判定一律以 `api.e-hentai.org` 的完整标签为准，且**查不到就不收录**。
- **图片代理的 SSRF 防护**：拒绝解析到内网 / 回环 / 链路本地地址的目标
  （含云元数据 `169.254.169.254`），否则用 `-addr 0.0.0.0` 暴露出去就是跳板。

---

## 访问权限

本项目只访问**公开内容**与**当前会话本身有权访问的内容**。

需要登录或会员的章节会返回明确的权限错误，**不做任何绕过访问控制的尝试**。
要访问你自己的权益，请通过 `-cookie` 或 `-username` / `-password` 提供你自己的账号。

E-Hentai 源**只收录 yaoi**，并硬性排除涉及未成年人的标签
（`lolicon` / `shotacon` / `toddlercon` / `lolita` 等），过滤在入口处强制执行。

---

## 测试

```bash
cd mccms-go
go test ./...                              # 离线用例（含对拍测试）

MCCMS_LIVE=1 go test ./internal/client/ -run Live -v   # 实网用例（默认跳过）
```

---

## 致谢与许可

前端界面基于 [JM-Aura](https://codeberg.org/Tom6814/JM-Aura)（MIT）改造，保留其许可与署名。
