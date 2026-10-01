# recon —— 侦察资料

本目录保存编写 `mccms` 时用到的原始侦察材料，不是运行时代码。

| 路径 | 内容 |
|---|---|
| `jmcomic-engineering-spec.md` | JMComic-Crawler-Python 2.7.7 的架构与公共 API 复刻规格（1201 行），本库刻意对齐它 |
| `samples/` | 两个站点的原始响应样本（HTML 页面、JSON 接口响应），用于核对解析逻辑 |

> `jmcomic` 的源码只作为**阅读参考**，没有随本仓库分发；
> 规格文档是对其公开 API 的独立描述，实现代码全部为本项目自行编写。

接口层面的结论整理见 [`../docs/API_REVERSE_ENGINEERING.md`](../docs/API_REVERSE_ENGINEERING.md)；
单元测试使用的固定夹具在 [`../tests/fixtures/`](../tests/fixtures)。
