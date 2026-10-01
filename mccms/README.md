# mccms

**一句话简介**：`mccms` 是一个面向 Mccms 站点（**TIBIU** `cache.tibiu.net` 与 **漫蛙** `www.manhwa.wang`）的 Python API + 下载器，架构刻意对齐 [JMComic-Crawler-Python](https://github.com/hect0x7/JMComic-Crawler-Python)（下称 jmcomic），因此 jmcomic 用户几乎没有学习成本。

```python
import mccms

mccms.download_comic('17001')                       # 下载整本（默认 TIBIU）
mccms.download_chapter('291931', comic_id='17001')  # 只下载一话
```

> **访问权限**：本库只访问**公开内容**与**当前会话本身有权访问的内容**；需要登录/会员的章节会抛出 `LoginRequiredException` / `VipRequiredException`。**本库不包含任何绕过访问控制的实现。** 详见 [访问权限说明](#访问权限说明重要)。

---

## 目录

- [1. 与 JMComic-Crawler-Python 的 API 对应](#1-与-jmcomic-crawler-python-的-api-对应)
- [2. 安装](#2-安装)
- [3. 快速开始](#3-快速开始)（3.8 图片乱序自动还原 / 3.9 Web UI）
- [4. 三个站点的差异与能力矩阵](#4-三个站点的差异与能力矩阵)
- [5. option 配置详解](#5-option-配置详解)
- [6. dir_rule 路径规则 DSL](#6-dir_rule-路径规则-dsl)
- [7. 内置插件（10 个）](#7-内置插件10-个)
- [8. 命令行用法](#8-命令行用法)
- [9. 扩展：自定义插件 / 下载器 / 客户端](#9-扩展自定义插件--下载器--客户端)
- [10. 访问权限说明（重要）](#访问权限说明重要)
- [11. 常见问题（FAQ）](#11-常见问题faq)
- [12. 示例文件索引](#12-示例文件索引)

---

## 1. 与 JMComic-Crawler-Python 的 API 对应

| jmcomic | mccms | 说明 |
|---|---|---|
| `JmOption` | `McOption` | 配置对象，从 yml / dict 构造 |
| `JmDownloader` | `McDownloader` | 下载器（含 `DoNotDownloadImage` / `JustDownloadSpecificCountImage`） |
| `JmOptionPlugin` | `McOptionPlugin` | 插件基类（`plugin_key` + `invoke`） |
| `JmAlbumDetail` | `McComicDetail` | 漫画（≈ jmcomic 的 album / 本子） |
| `JmPhotoDetail` | `McChapterDetail` | 章节 |
| `JmImageDetail` | `McImageDetail` | 图片 |
| `JmSearchPage` | `McSearchPage` | 搜索 / 列表分页（`McCategoryPage` 是它的别名） |
| `JmModuleConfig` | `McModuleConfig` | 全局配置与注册表 |
| `JmMagicConstants` | `McMagicConstants` | 协议层常量 |
| `JmApiClient` / `JmHtmlClient` | `TibiuClient` / `ManhwaClient` | 站点实现（`client_key` = `tibiu` / `manhwa`） |
| `download_album()` | `download_comic()` | 下载整本 |
| `download_photo()` | `download_chapter()` | 下载单章 |
| `download_album_async()` | `download_comic_async()` | 异步门面（见 [3.6](#36-批量下载与异步门面)） |
| `create_option_by_file/str/env` | 同名函数 | option 构造入口 |
| `dir_rule` 的 `A*`（album）/ `P*`（photo）字段 | `C*`（comic）/ `Ch*`（chapter）字段 | 见 [第 6 节](#6-dir_rule-路径规则-dsl) |
| `plugins.after_album` / `after_photo` | `plugins.after_comic` / `after_chapter` | 旧事件名会被自动迁移，见 [5.5](#55-旧配置迁移) |

`mccms/__init__.py` 还导出了一批**纯别名**，方便逐步迁移：`JmOption = McOption`、`JmDownloader = McDownloader`、`download_album = download_comic`、`download_photo = download_chapter` 等。**但参数名一律以本库源码为准**，不要照抄 jmcomic 的调用签名。

---

## 2. 安装

```bash
git clone <this-repo> && cd mccms
pip install -e .            # 可编辑安装（推荐，示例脚本直接可跑）
```

核心依赖只有两个：`requests`、`pyyaml`。

### 可选依赖

| 能力 | 安装 | 未安装时的行为 |
|---|---|---|
| 浏览器 TLS 指纹伪装（`client.postman.meta_data.impersonate`） | `pip install curl-cffi` | 回退到 `requests`，并记一条日志 |
| 目录名繁简体归一（`dir_rule.normalize_zh`） | `pip install zhconv` | 原样返回，不转换 |
| `zip` 插件加密打包 | `pip install pyzipper` | 退化为**不加密**的 zip，并记一条 warning |
| `img2pdf` 插件 | `pip install img2pdf`（加密还需 `pikepdf`） | 插件执行失败（异常被 `call_all_plugin` 捕获并记日志） |
| `long_img` 插件 | `pip install pillow` | 同上 |
| 一次性装齐插件依赖 | `pip install -e ".[plugins,zh,impersonate]"` | — |

### Python 版本

项目元数据声明 `requires-python >= 3.8`，但 **`download_comic_async()` / `download_chapter_async()` 使用了 `asyncio.to_thread`，需要 Python ≥ 3.9**；其余功能在 3.8 上可用（实测环境为 CPython 3.9.6）。

### 命令行入口

`pip install -e .` 会注册控制台脚本 `mccms`，同时也支持 `python -m mccms`（两者等价）：

```bash
mccms --help
python -m mccms --help
```

---

## 3. 快速开始

### 3.1 下载整本漫画

```python
import mccms

# 最简：默认 TIBIU 站点，默认目录规则 Bd_Cname_Chindextitle，base_dir = 当前工作目录
mccms.download_comic('17001')

# 带自己的配置
option = mccms.McOption.construct({
    'dir_rule': {'rule': 'Bd_Cname_Chindextitle', 'base_dir': './downloads'},
    'download': {'threading': {'image': 8, 'chapter': 2}},
    'client': {'impl': 'tibiu'},
})
result = mccms.download_comic('17001', option)

print(result.detail.name)                              # McComicDetail
print(len(result.manifest.image_filepath_list))        # 实际落盘的图片
print(result.duration)                                 # 秒（可能为 None）
```

`download_comic()` 的返回值是 `DownloadResult`（`NamedTuple`：`detail` + `downloader`），`result.manifest` 是 `DownloadManifest`，包含：

- `image_filepath_list: List[str]`：本次下载的图片路径（按 章节序号、图片序号 排序）
- `export_filepath_dict: Dict[str, List[str]]`：插件导出的文件（`'zip'` / `'pdf'` / `'png'` / `'jpg'`…），用 `manifest.get_export_filepath_list('zip')` 取值
- `duration: Optional[float]`

也可以直接用 `McOption` 上的语法糖（内部就是调用同名 api 函数）：

```python
option.download_comic('17001')
option.download_chapter('291931', comic_id='17001')
```

### 3.2 下载单个章节

```python
import mccms

# 只下这一话；comic_id 建议提供，这样 dir_rule 里的 Cxxx（漫画字段）才可用
mccms.download_chapter('291931', comic_id='17001')

# TIBIU 的章节 URL 也能直接传（路径 /chapter/{comic_id}/{chapter_id} 会同时解析出两个 id）
mccms.download_chapter('https://cache.tibiu.net/chapter/17001/291931')
```

> 只传 `chapter_id` 且目录规则里含 `Cxxx` 字段时，库会尝试用章节自带的 `comic_id` 反查漫画详情补上下文（`McDownloader.attach_comic_context`）；查不到就会报 `无法解析的 dir_rule 片段: [Cname]`。

### 3.3 只用 API，不下载

`McOption.build_client()` 之后的调用不会写任何文件（除你自己保存）：

```python
import mccms

option = mccms.McOption.construct({'log': False, 'client': {'impl': 'tibiu'}})
client = option.build_client()

comic = client.get_comic_detail('17001')          # 含章节列表
print(comic.comic_id, comic.name, comic.author, comic.chapter_count, comic.serialize)

chapter = comic[0]                                # McComicDetail 是 Sequence，支持切片
print(chapter.chapter_id, chapter.indextitle, chapter.access_desc, chapter.count)

# 注意：comic[i] 造出来的章节默认不带图片列表（len(chapter) == 0），要显式取一次
if len(chapter) == 0:
    chapter = client.get_chapter_detail(chapter.chapter_id, comic_id=comic.comic_id)

for image in chapter[:3]:                         # 只看前 3 张
    print(image.index, image.filename, image.download_url)

client.postman.close()                            # 关闭底层 requests.Session
```

实体的层级关系与常用成员：

| 实体 | 关键字段 | 关键方法 |
|---|---|---|
| `McComicDetail` | `comic_id` / `id`、`name` / `title`、`author`、`authors`、`tags`、`description`、`cover`、`serialize`、`site`、`url`、`slug`、`episode_list`、`chapter_access`、`chapter_count`、`page_count`、`is_finished`、`latest_chapter_name`、`oname`、`authoroname`、`idoname`、`save_path` | `comic[i]`、`comic[a:b]`、`chapter_at(n)`、`get_properties_dict()` |
| `McChapterDetail` | `chapter_id` / `id`、`name` / `title`、`indextitle`、`index`、`comic_id`、`comic_name`、`count`、`access`、`vip` / `cion` / `price`、`is_free`、`access_desc`、`image_url_list`、`from_comic`、`url`、`save_path` | `chapter[i]`、`chapter[a:b]`、`set_image_url_list(urls, ids)`、`len(chapter)` |
| `McImageDetail` | `chapter_id`、`img_url` / `download_url`、`img_file_name`、`img_file_suffix`、`filename`、`filename_without_suffix`、`index`（从 1 开始）、`is_gif`、`from_chapter`、`save_path` | `McImageDetail.of(chapter_id, url, from_chapter=..., index=...)` |
| `McSearchPage` | `content`（`[(comic_id, info), ...]`）、`total`、`page_number`、`page_size`、`page_count` | `iter_id_title()`、`iter_id_title_author()`、`iter_id_title_tag()`、`iter_id()`、`to_comic_list()`、`wrap_single_comic(comic)` |

### 3.4 搜索

```python
page = client.search('魔咒', page=1)

print(len(page), page.total, page.page_number)      # 本页条数 / 总数（部分站点是估算值）
for comic_id, name in page.iter_id_title():
    print(comic_id, name)

# 连续翻页：搜索分页生成器（满页才继续；也可用 end_page 限死页数）
for sub_page in client.search_gen('魔咒', start_page=1, end_page=2):
    print(sub_page.page_number, len(sub_page))
```

`info` 字典里通常有 `name` / `author` / `cover` / `text` / `views` / `update_date` / `url` / `slug` / `raw`（漫蛙的搜索结果还会带 `latest_chapter`、`latest_chapter_url`）。

### 3.5 分类 / 榜单

```python
from mccms import McMagicConstants as C

# TIBIU：JSON 接口
page = client.categories_filter(page=1, order=C.ORDER_HITS)     # ORDER_LIST = ('addtime','hits','score','nums')
page = client.categories_filter(page=1, tags=[12, 15], tids=3)  # tags/tids 可传 id 或 id 列表
page = client.update_list(page=1)                               # 最近更新
options = client.filter_options(mode='update')                  # 分类/标签的 id -> 名称
nav = client.ranking_nav()                                      # 榜单导航
page = client.ranking(rank_type='top', page=1)                  # 榜单列表

# 漫蛙：HTML 页面解析
page = client.categories_filter(page=1, order='hits', finish=1, pay=2)  # finish: 1完结/2连载, pay: 1免费/2付费
page = client.update_list(page=1)
page = client.hot_list()                                        # 漫蛙独有的热门接口
```

分类分页也有生成器：`client.categories_filter_gen(start_page=1, end_page=2, order='hits')`。

### 3.6 批量下载与异步门面

```python
# 传可迭代对象 -> 自动走 download_batch：一个 id 一个线程，失败项不会静默丢失
result = mccms.download_comic(['17001', '17002', '17003'])
print(result, result.total, result.all_succeeded)
for comic_id, error in result.failed.items():
    print('失败:', comic_id, error)

# 也可以显式调用
result = mccms.download_batch(mccms.download_comic, ['17001', '17002'])
```

```python
import asyncio, mccms

async def main():
    # 注意：本库下载流程本身是「同步 + 多线程」，这两个 async 函数只是
    # asyncio.to_thread 的门面，便于用 gather 并发多本；它们不是原生异步实现。
    await asyncio.gather(
        mccms.download_comic_async('17001'),
        mccms.download_comic_async('17002'),
    )

asyncio.run(main())        # 需要 Python >= 3.9
```

### 3.7 异常处理

所有异常都继承 `McException`：

```
McException
├── MissingComicChapterException      漫画/章节不存在
├── RegularNotMatchException          响应结构不匹配
├── ResponseUnexpectedException       响应不符合预期（如 code != 1）
├── PartialDownloadFailedException    部分图片/章节下载失败（失败明细在 e.context['downloader'] 上）
├── PluginValidationException         插件参数校验失败（带 plugin 属性）
└── AccessDeniedException             「无权访问」的基类
    ├── LoginRequiredException        需要登录
    ├── VipRequiredException          需要会员/金币权益
    └── ChapterNotAccessibleException 章节不可访问（下架/审核/未发布）
```

```python
try:
    mccms.download_chapter('291931', comic_id='17001')
except mccms.LoginRequiredException as e:
    print('需要登录:', e.msg)
except mccms.VipRequiredException as e:
    print('需要会员权益:', e.msg)
except mccms.PartialDownloadFailedException as e:
    downloader = e.context['downloader']         # 可拿到失败明细
    print(len(downloader.download_failed_image), len(downloader.download_failed_chapter))
```

也可以注册全局监听器（对所有通过 `ExceptionTool.raises` 抛出的异常生效）：

```python
# 两种写法等价
mccms.register_exception_listener(mccms.LoginRequiredException,
                                  lambda e: print('需要登录:', e.msg))
mccms.McModuleConfig.register_exception_listener(mccms.VipRequiredException,
                                                 lambda e: print('需要会员:', e.msg))
```

### 3.8 图片乱序自动还原

部分站点（目前是香香腐宅的新章节）会把整页图**切成 N 条竖带后倒序**再下发，
前端用 canvas 还原。直接用 `<img>` 打开会看到明显错位的画面。

本库在下载时自动还原，**对使用者透明**：

```python
option = mccms.McOption.construct({
    'client': {'impl': 'boylove'},
    # download.image.decode 默认就是 True，无需显式配置
})
mccms.download_chapter('2623003', option, comic_id='16904')   # 落盘即为正常图
```

要点：

- **N 逐章不同**（同一本漫画里实测有 11 / 13 / 18 / 23），由阅读页的 `randomClass` 下发，
  逐章读取，不写死。
- **只对乱序章节生效**。老章节没有这个标记（`chapter.scramble_n == 0`），
  图片按字节原样落盘，不做任何重新编码。
- **超高图不解码**：站点前端对 `height >= 4000` 的图是原样拷贝，本库保持一致，
  避免把本来就正常的超长条漫弄坏。
- 想保留原始错位图时：`download: {image: {decode: false}}`。

还原算法在 [`mc_decode.py`](src/mccms/mc_decode.py)，是站点 `do_mergeImg` 的等价实现：

```python
from mccms.mc_decode import reverse_vertical_strips, decode_scrambled_image_file

decode_scrambled_image_file('错位图.webp', n=11)      # 就地还原，保持原后缀格式
```

判定是否乱序的 `McChapterDetail.scramble_n` / `McImageDetail.is_scrambled` 也可以直接读：

```python
comic  = client.get_comic_detail('16904')
chapter = client.get_chapter_detail('2623003', comic_id='16904')
print(chapter.scramble_n, chapter[0].is_scrambled)    # 11 True
```

> 本地 Web 前端同样会还原：图片代理接受 `scramble_n` 参数，
> 页面里看到的也是还原后的图（章节标题旁会标出「已还原竖带 N」）。

### 3.9 可视化验证前端（Web UI）

想先用眼睛确认「搜索 → 详情 → 章节 → 看图 → 下载」这条链路通不通，可以直接起本地前端：

```bash
python -m mccms.web            # 默认 http://127.0.0.1:8765
python -m mccms.web --port 9000 --open
mccms-web --open               # 安装后等价的 console script
```

零额外依赖（只用 stdlib `http.server`），只监听 `127.0.0.1`。页面上可以：

- 切换站点（TIBIU / 漫蛙），搜索、看榜单 / 最近更新 / 热门
- 点开漫画看详情与章节列表（章节带 `免费` / `VIP` 角标，可只看免费、按名过滤）
- 点开章节直接在页面里翻图
- 「下载本章」支持只下前 N 张，页面实时显示进度、落盘路径与耗时
- 右上角显示当前会话的登录态

它背后的 JSON 接口（也可直接当 API 用）：

| 接口 | 说明 |
|---|---|
| `GET /api/sites` | 站点列表与能力矩阵 |
| `GET /api/search?site=&keyword=&page=` | 搜索 |
| `GET /api/comic?site=&id=` | 漫画详情（含章节列表） |
| `GET /api/chapter?site=&id=&comic_id=` | 章节详情（含图片地址） |
| `GET /api/ranking?site=&type=` / `GET /api/ranknav?site=` | 榜单（TIBIU） |
| `GET /api/update?site=&page=` | 最近更新 |
| `GET /api/hot?site=` | 热门（漫蛙） |
| `GET /api/user?site=` | 当前会话登录态 |
| `GET /api/image?site=&url=` | 图片代理 |
| `POST /api/download` + `GET /api/job?id=` | 提交下载任务 / 轮询进度 |

两点实现说明：

- **图片代理有域名白名单**。只有「内置站点域名」和「本库确实返回过的图片域名」能被代理，其它一律 403，避免这个本地服务被当成任意 SSRF 跳板。
- **无权限时前端不隐藏**。VIP 章节会走 `category: "access_denied"` 分支，页面显示服务端原文 + 明确提示；下载任务的状态是 `denied`。前端不会做任何绕过尝试。

---

## 4. 三个站点的差异与能力矩阵

| 能力 | TIBIU（`impl: tibiu`） | 漫蛙（`impl: manhwa`） | 香香腐宅（`impl: boylove`） |
|---|---|---|---|
| 站点域名 | `cache.tibiu.net` | `www.manhwa.wang` | `boylove.cc`（轮换短链入口，如 `fufuhouse.work/xxxxx`） |
| 数据形态 | **纯 JSON API** | **JSON + HTML 混合** | **JSON + HTML 混合** |
| 漫画详情 | JSON `/api/data/comicinfo?cid=` | HTML `/index.php/comic/{id或slug}` | HTML `/home/book/index/id/{id}`（读 `og:*` 与 `p.data`） |
| 章节列表 | JSON `/api/data/chapter?mid=`（按 `xid` 升序重排） | JSON `/index.php/api/comic/chapter?mid=` | JSON `/home/api/chapter_list/tp/{id}`（按**列表顺序**，章节 id 不单调） |
| 章节图片 | JSON `/api/data/pic?cid=`（按 `id` 升序重排，与网页端一致） | 免费章节：阅读页 `.rd-article__pic img`；受限章节：JSON `/index.php/api/comic/isbuy?id=` | 阅读页 `img[data-original]`，按文件名 `{chapter_id}-` 精确过滤（页面混有推荐位缩略图） |
| 搜索 | JSON `/api/data/search?key=&page=`（不返回总数，用满页与否估算） | HTML `/index.php/search/{key}/{page}`（从 `.search_head` 解析总数） | JSON `/home/api/searchk?keyword=&type=&pageNo=`（`type` 0/1 漫画、2 小说） |
| 分类 | JSON `category_api`，参数 `order` / `tags` / `tids` | HTML `/index.php/category/order/{order}[/finish/…][/pay/…][/tags/…]` | JSON `/home/api/cate/tp/{cate}-{tag}-{done}-{order}-{page}-{is18}-1-{vip}?mt=0`（**必须拼满 8 段**） |
| 最近更新 | JSON `update_api?page=` | `categories_filter(order='addtime')` | `categories_filter(order=1)` |
| 榜单 | `ranking_nav()` + `ranking(rank_type, page, max_items=500)` | ❌ 无（只有 `hot_list()`） | `ranking(rank_type)` + `ranking_nav()`（一次返回四组：人气/消费/收藏/搜索） |
| 列表页自带作者与标签 | ✅ JSON 里有 | ❌ 卡片无作者，需进详情页 | ✅ 作者/标签/评分/状态/简介都在列表项里 |
| 筛选项字典 | `filter_options(mode)` | ❌ 无 | ❌ 无 |
| 章节 URL 一次解析两个 id | ✅ `parse_chapter_url(url) -> (comic_id, chapter_id)` | ❌ | ❌ |
| 年龄门（限制级内容） | ✅ `confirm_adult()`；option 里 `client.confirm_adult: true` | ❌ 无此机制 | ❌ 无此机制 |
| `comic_id` 形式 | 数字 id | 数字 id **或 slug**（如 `mozhouweixianzaoyu`） | 数字 id |
| App 端接口 | — | — | **存在但不使用**：`/setting` `/latest` `/api/album` 等与 18comic 的 App API 同构，需客户端签名；本库只用网页端接口，不复用也不推导任何签名 |

> 关于香香腐宅：站点入口是轮换短链（`fufuhouse.work/{code}` → 镜像 → `boylove.cc`），
> 跳转带 `utm_source=18comic`。它确实另有一套与 JM App API 同构的接口，
> 但本库**只使用网页端公开接口**，理由与边界见
> [接口逆向文档 §4](docs/API_REVERSE_ENGINEERING.md)。

两个客户端共享的公共方法（`McClientInterface` / `AbstractMcClient`）：

```python
client.get_comic_detail(comic_id, *, fetch_chapters=True)
client.get_chapter_detail(chapter_id, *, comic_id=None, fetch_image_urls=True)
client.search(keyword, page=1) / client.search_gen(keyword, start_page=1, end_page=None)
client.categories_filter(page=1, **kwargs) / client.categories_filter_gen(start_page=1, end_page=None, **kwargs)
client.update_list(page=1)
client.fetch_image_urls(chapter)          # 写入 chapter 并返回 URL 列表；无权限时抛 AccessDeniedException 子类
client.check_chapter(chapter)             # 下载前的权限/取图校验
client.user_info() / client.is_logged_in() / client.login(username, password, is_log=1, pcode='') / client.logout()
client.describe_access(chapter)
client.download_image(url, save_path) / client.download_by_image_detail(image, save_path, decode_image=False)
client.download_cover(comic_id, save_path, size='')
client.get_comic_id_from_url(url) / client.get_chapter_id_from_url(url)
```

---

## 5. option 配置详解

`McOption` 的四个配置段（`dir_rule` / `download` / `client` / `plugins`）在构造时必须存在，但**只写要覆盖的 key 即可**，其余自动合并默认值：

```python
option = mccms.McOption.construct({'dir_rule': {'base_dir': './downloads'}})
option = mccms.McOption.default()                             # 全默认
option = mccms.create_option_by_file('my_option.yml')         # 从 yml
option = mccms.create_option_by_str("dir_rule: {base_dir: ./a}\n")   # 从字符串（默认按 yml 解析）
option = mccms.create_option_by_env('MCCMS_OPTION_PATH')      # 从环境变量指向的文件
option.to_file('saved_option.yml')                            # 回写 yml
```

相关构造/序列化 API：`McOption.construct(dic, cover_default=True, call_after_init_plugin=True)`、`McOption.default_dict()`、`McOption.merge_default_dict(dic)`、`McOption.from_file(path)`、`McOption.deconstruct()`、`McOption.copy_option()`。

### 5.1 顶层

| key | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `log` | `true` / `false` / `'pretty'` | `true` | `false` → `disable_mc_log()` 全局关日志；`'pretty'` → `enable_pretty_log()` 彩色日志。该 key 不会传给 `McOption.__init__` |
| `version` | str | 写出时 `'2.1'` | **读取时被直接丢弃**（本库不做版本分支），仅用于兼容 jmcomic 的 option 文件形状 |

### 5.2 `dir_rule`

| key | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `rule` | str | `'Bd_Cname_Chindextitle'` | 路径 DSL，见[第 6 节](#6-dir_rule-路径规则-dsl) |
| `base_dir` | str / null | 省略 → `os.getcwd()`；显式 `null` 也等价于 cwd | 支持 `${ENV_VAR}`；相对路径会被转成绝对路径 |
| `normalize_zh` | str / null | `null` | 目录名/文件名的繁简体归一，值透传给 `zhconv.convert`（如 `zh-cn` / `zh-tw`），需要可选依赖 `zhconv` |

### 5.3 `download`

| key | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `cache` | bool | `true` | 目标文件已存在时跳过下载（`decide_download_cache`） |
| `image.decode` | bool | `true` | **保留参数**：对齐 jmcomic 的开关，本站点图片明文直出，当前实现不做任何图片变换；`.gif` 恒为 `false` |
| `image.suffix` | str / null | `null` | 统一改写的保存后缀（如 `'.jpg'`，只改文件名不转码）；`.gif` 不受影响（`decide_image_suffix`） |
| `threading.image` | int | `30`（省略或写 `null` 都会兜底） | 图片并发（`decide_image_batch_count`） |
| `threading.chapter` | int | 省略或写 `null` → 运行时取 `os.cpu_count()` | 章节并发（`decide_chapter_batch_count`） |

> 合并默认值之后，`McOption.construct()` 还会调用一次 `McModuleConfig.fill_runtime_defaults(dic)`：如果 `log` / `dir_rule.base_dir` / `client.impl` / `download.threading.*` 等**必需项**是 `None`（包括你显式写 `null` 的情况），会被兜底成运行时默认值。

并发实现（`McDownloader.execute_on_condition`）：线程数 ≥ 对象数时「一个对象一个线程」，否则用线程池。

### 5.4 `client`

| key | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `impl` | str | 省略或写 `null` → 兜底为 `'tibiu'` | 内置 `tibiu` / `manhwa`，或自己注册的 `client_key` |
| `retry_times` | int | `3` | 单请求重试次数（单个域名连续失败 2 次会切换域名） |
| `domain` | list / str / dict | `[]` → 内置域名 | `[]` 用内置；多行字符串按行切分；`{impl: [...]}` 按实现区分 |
| `username` / `password` | str / null | `null` | 填了就自动登录（`ensure_login`）。**仅用于访问该账号本身有权访问的内容** |
| `confirm_adult` | bool | `false` | 新建 client 时调用 `confirm_adult()` 确认年龄门（TIBIU 限制级内容） |
| `cookies` | dict | `{}` | 合并进 `postman.meta_data.cookies`，随请求发送 |
| `cache` | any | `null` | **预留**：本库未实现元数据缓存 |
| `postman.type` | str | `'requests'` | 底层 HTTP 实现。`'curl_cffi'` / `'curl_cffi_session'` 会用 curl_cffi（需可选依赖 `curl-cffi`，缺失时回退 requests）；或直接给 `meta_data.impersonate` 也会走 curl_cffi |
| `postman.meta_data.headers` | dict / null | `null` → 内置模板 `McModuleConfig.HTML_HEADERS_TEMPLATE` | 请求头 |
| `postman.meta_data.proxies` | dict / null | `null` | requests 风格代理，如 `{http: 'http://127.0.0.1:7890'}` |
| `postman.meta_data.cookies` | dict | `{}` | cookies（与 `client.cookies` 合并） |
| `postman.meta_data.timeout` | int | `20` | 请求超时（秒） |
| `postman.meta_data.impersonate` | str | 无该 key | 如 `chrome`；需要可选依赖 `curl-cffi`，否则回退 requests |

### 5.5 `plugins`

| key | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `dependencies_strategy` | str | `'failed-fast'` | 插件依赖缺失策略：`failed-fast`（直接抛异常）/ `auto-install`（自动 `pip install`）/ `ignore-only-log`（仅日志）。检查发生在 `McOption` 构造阶段（`after_init` 之前） |
| `valid` | str | `'log'` | 插件参数校验失败（`PluginValidationException`）时的处理策略：`ignore` 静默 / `log` 记日志 / `raise` 上抛 |
| `plugins.<事件名>` | list[dict] | 无 | 事件分组，key 名即事件名 |

单个插件条目（`pinfo`）的字段：

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `plugin` | str | 必填 | 插件 key，必须在 `McModuleConfig.REGISTRY_PLUGIN` 中（否则抛异常并列出可用 key） |
| `kwargs` | dict | `{}` | 传给 `plugin.invoke(**kwargs)`；**字符串值会展开 `${ENV_VAR}`** |
| `log` | bool | `true` | `false` → `plugin.log_enable = False` |
| `safe` | bool | `true` | `true` → 插件异常只记日志；`false` → 异常向上抛 |
| `valid` | str | 取 `plugins.valid` | 覆盖全局策略，作用于该插件条目 |

真正会被派发的事件（即源码里 `call_all_plugin` 的调用点）：

| 事件 | 触发时机 | 额外注入给 `invoke()` 的参数 |
|---|---|---|
| `after_init` | `McOption` 构造时（`safe=True`） | — |
| `before_comic` | 开始下载一本漫画 | `comic`, `downloader` |
| `after_comic` | 一本漫画下载完成 | `comic`, `downloader` |
| `before_chapter` | 开始下载一话 | `chapter`, `downloader` |
| `after_chapter` | 一话下载完成 | `chapter`, `downloader` |
| `before_image` | 下载单张图片前 | `image`, `downloader` |
| `after_image` | 单张图片下载完成（**命中缓存也会触发**） | `image`, `downloader` |

> 事件名不做白名单校验：`option.call_all_plugin('任意名字')` 都能派发，因此你也可以自定义事件用于自己的流程。

### 5.6 关于 `${环境变量}`

源码里只有两个位置会展开 `${ENV_VAR}`：

1. `dir_rule.base_dir`（经 `MccmsText.parse_to_abspath` → `parse_dsl_text`）
2. 插件 `kwargs` 里的字符串值（经 `McOption.fix_kwargs`）

**其它位置不会展开**——例如 `client.cookies: {PHPSESSID: '${MY_COOKIE}'}` 会把 `${MY_COOKIE}` 当成字面量发出去。需要 cookie 时请用 CLI 的 `--cookie`、或代码里的 `option.update_cookies({'PHPSESSID': os.environ['MY_COOKIE']})`。

### 5.7 旧配置迁移

`McOption.construct()` 会**先**对用户传入的 dict 调用 `McOption.compatible_with_old_versions(dic)`，再合并默认值（顺序很关键，否则默认值会把旧 key 掩盖掉）。迁移规则：

| 旧写法 | 新写法 | 说明 |
|---|---|---|
| `plugin:`（顶层） | `plugins:` | 直接整体搬过去 |
| `download.threading.batch_count` | `download.threading.image` | 图片并发改名 |
| `plugins.before_album` / `after_album` | `plugins.before_comic` / `after_comic` | 事件名别名 |
| `plugins.before_photo` / `after_photo` | `plugins.before_chapter` / `after_chapter` | 事件名别名 |

别名只在「新名字不存在」时才生效（同时写了新旧名字时以新名字为准）。新写配置请直接使用 `plugins` / `download.threading.image` 与 `*_comic` / `*_chapter` 事件名。

---

## 6. dir_rule 路径规则 DSL

### 6.1 语法

```
最终路径 = 段1 / 段2 / 段3 ...
段 = Bd | Cxxx | Chxxx | f-string 片段
```

- 切分规则：**含 `/` 就按 `/` 切，否则按 `_` 切**；首段不是 `Bd` 会自动补上 `Bd`。
- 由于 `_` 是分隔符，**字段名本身含下划线时（如 `Chupdate_date`、`Chcomic_id`）必须改用 `/` 分隔**：
  `rule: 'Bd/Cserialize/Chupdate_date'`。
- 除 `Bd` 段外，每段都会做 `zhconv` 归一（若配置）与 `fix_windir_name`（`\ / : * ? " < > |` 及换行制表符替换为 `_`，并去掉结尾的点）。
- f-string 片段走 Python `str.format`，支持格式说明符：`第{Chindex:03}话` → `第007话`。

```yaml
dir_rule:
  rule: Bd_Cname_Chindextitle                    # ./downloads/魔咒/第1话
  rule: Bd/{Cauthor}/{Cname}/第{Chindex:03}话     # ./downloads/作者/魔咒/第001话
  rule: Bd_Coname_Cid                            # ./downloads/魔咒/17001
  rule: Bd/Cserialize/Chupdate_date              # 含下划线字段：必须用 '/' 分隔
  base_dir: ./downloads
```

`DirRule` 的公开方法：

| 方法 | 说明 |
|---|---|
| `DirRule(rule, base_dir=None, normalize_zh=None)` | 构造（`base_dir` 会被 `parse_to_abspath`） |
| `apply_rule_to_path(comic, chapter, only_comic_rules=False)` | 生成完整路径 |
| `decide_image_save_dir(comic, chapter)` | 图片保存目录（并 `mkdir`） |
| `decide_comic_root_dir(comic)` | 漫画根目录（只用 `Bd` 与 `Cxxx` 段） |
| `apply_rule_to_filename(comic, chapter, rule)` | 只生成单段文件名（插件 `filename_rule` 用，不支持 `Bd`） |

### 6.2 漫画字段（`C` 前缀，来自 `McComicDetail.get_properties_dict()`）

| DSL | 取值 |
|---|---|
| `Cid` / `Ccomic_id` | 漫画 id |
| `Cname` / `Ctitle` | 漫画名 |
| `Cauthor` | 首位作者（无作者时 `'default_author'`） |
| `Cauthors` | 作者列表 |
| `Coname` | 去掉 `[汉化组]`、`（社团）` 等括号片段后的原名 |
| `Cauthoroname` | `'【作者】原名'` |
| `Cidoname` | `'[id] 原名'` |
| `Cdescription` | 简介 |
| `Ccover` | 封面 URL |
| `Cserialize` | 连载状态（如 `连载中` / `完结`）；`Cis_finished` 是布尔判断 |
| `Cupdate_date` / `Cadd_date` | 更新 / 上架时间 |
| `Cviews` / `Cscore` | 人气 / 评分 |
| `Csite` | 站点 key（`tibiu` / `manhwa`） |
| `Curl` / `Cslug` | 站点路径 / slug |
| `Ctags` | 标签列表（f-string 里会按 Python 的 list 字面量输出，慎用） |
| `Cchapter_count` / `Cpage_count` | 话数 / 各话 `pnum` 之和 |
| `Clatest_chapter_name` | 最后一话的名字 |
| `Cchapter_access` / `Cepisode_list` | 原始章节元数据（不建议进路径） |
| `Csave_path` / `Cexists` / `Cskip` / `Ccache` / `Cduration` | 下载状态字段 |

### 6.3 章节字段（`Ch` 前缀，来自 `McChapterDetail.get_properties_dict()`）

| DSL | 取值 |
|---|---|
| `Chid` / `Chchapter_id` | 章节 id |
| `Chname` / `Chtitle` | 章节名 |
| `Chindextitle` | `'第{index}话 {name}'`；若 `name` 本身已是「第N话/章/集/回」，则原样返回不加前缀 |
| `Chindex` | 章节序号（从 1 开始，站点跳号时会被重排为连续值） |
| `Chcomic_id` / `Chcomic_name` | 所属漫画 id / 名（后者依赖 `from_comic`） |
| `Chcount` | 站点给出的图片数（`pnum`） |
| `Churl` / `Chupdate_date` | 章节路径 / 更新时间 |
| `Chauthor` / `Chtags` | 来自 `from_comic`，缺失时为 `'default_author'` / `[]` |
| `Chvip` / `Chcion` / `Chprice` | 权限标记；`Chis_free` / `Chaccess_desc` 是派生值 |
| `Chimage_url_list` | 图片地址列表 |
| `Chfrom_comic` | 所属 `McComicDetail`（字段名含下划线，须用 `/` 分隔写法） |
| `Chsave_path` / `Chexists` / `Chskip` / `Chcache` / `Chduration` | 下载状态字段 |

> 未列出的公开属性/实例字段同样可用——`get_properties_dict()` 会把 MRO 上的所有 `property` 与实例 `__dict__` 里的公开字段都收集进来，key 一律加前缀。字段写错时异常信息会列出可用字段。

### 6.4 注册自定义字段

```python
from mccms import McModuleConfig, McOption

McModuleConfig.CFIELD_ADVICE['year'] = lambda comic: (comic.update_date or '')[:4]     # 用法：{Cyear}
McModuleConfig.HFIELD_ADVICE['safename'] = lambda chapter: chapter.name.replace('/', '_')  # 用法：{Chsafename}

option = McOption.construct({'dir_rule': {'rule': 'Bd/{Cyear}/{Chsafename}'}})
```

注意前缀规则：`CFIELD_ADVICE` 注册的字段在 f-string 里写作 `C<name>`，`HFIELD_ADVICE` 写作 `Ch<name>`。若字段名本身含下划线，请用 `/` 分隔写法（见 6.1）。

`McOption` 上与路径/命名相关的可重写决策方法：`decide_image_save_dir`、`decide_image_filepath`、`decide_image_filename`、`decide_image_suffix`、`decide_download_cache`、`decide_download_image_decode`、`decide_image_batch_count`、`decide_chapter_batch_count`、`decide_client_domain`。

---

## 7. 内置插件（10 个）

插件 key 就是 `plugin_key`；写在 `plugins.<事件名>` 下即可生效。全部插件类都从 `mccms` 包顶层导出（`LoginPlugin`、`ZipPlugin`…），也可以 `McModuleConfig.REGISTRY_PLUGIN` 查看注册表。

| plugin_key | 适用事件 | kwargs（默认值） | 作用 | 额外依赖 |
|---|---|---|---|---|
| `login` | `after_init` | `username`（必填）、`password`（必填） | 用**你自己的**账号登录，并把会话 cookie 写回 option，供后续 client 复用 | — |
| `image_suffix_filter` | `after_init`（任意早期事件） | `allowed_orig_suffix`（必填，如 `['.jpg','.png']`） | 后缀不在白名单的图片直接 `image.skip = True` | — |
| `replace_path_string` | `after_init` | `replace`（必填，dict，如 `{'[汉化组]': ''}`） | 对图片保存目录做字符串替换 | — |
| `log_topic_filter` | `after_init` | `whitelist`（必填，topic 前缀列表） | 只保留白名单前缀的日志 | — |
| `skip_chapter_with_few_images` | `before_chapter` | `at_least_image_count`（必填，>0） | 图片数少于阈值的章节直接跳过 | — |
| `download_cover` | `before_comic` | `dir_rule`（必填，dict）、`size`（默认 `''`，**预留**） | 下载封面并记入下载清单 | — |
| `zip` | `after_comic` / `after_chapter` | `zip_dir='./'`、`filename_rule='Chindextitle'`、`suffix='zip'`、`dir_rule=None`、`delete_original_file=False`、`encrypt=None` | 打包 zip（放 `after_comic` = 整本一个包，放 `after_chapter` = 每话一个包） | 加密时需 `pyzipper` |
| `img2pdf` | `after_comic` / `after_chapter` | `pdf_dir=None`（默认 cwd）、`filename_rule='Chindextitle'`、`dir_rule=None`、`delete_original_file=False`、`encrypt=None` | 每话合并成 PDF | `img2pdf`；加密还需 `pikepdf` |
| `long_img` | `after_chapter` | `img_dir=None`、`filename_rule='Chindextitle'`、`dir_rule=None`、`delete_original_file=False` | 纵向拼接成长图 PNG（**只支持 `after_chapter`**） | `pillow` |
| `delete_duplicated_files` | `after_comic` | `limit=2`、`delete_original_file=True` | 按 MD5 删除漫画目录下的重复图片（⚠ 默认就会真删） | — |

细节说明：

- **事件注入的参数与自己写的 kwargs 会合并**：例如 `after_chapter` 下所有插件都会额外收到 `chapter` 与 `downloader`。
- `encrypt` 的两种形态：`{password: 'xxx'}`（固定密码）或 `{type: random}`（随机 16 位密码，会打到 `plugin.zip.password` 这个 topic 的日志里，配合 `log_topic_filter` 可单独保留）。
- `filename_rule` 支持 `Cxxx` / `Chxxx` / `{Cname}` 等规则（走 `DirRule.apply_rule_to_filename`，不支持 `Bd`）；给 `zip` / `download_cover` 等传了 `dir_rule` 时，**文件名完全由 `dir_rule` 决定**，想要后缀就得自己写在规则里（如 `rule: 'Bd/{Cname}.zip'`）。
- 导出物会记进 `result.manifest.export_filepath_dict`，用 `manifest.get_export_filepath_list('zip')` 取。
- 把 `after_init` 插件（`login` / `image_suffix_filter` / `replace_path_string` / `log_topic_filter`）放在别的事件里会太晚：它们是在 `invoke` 时改写 option 行为的。

```yaml
plugins:
  after_init:
    - plugin: login
      kwargs: { username: ${MCCMS_USERNAME}, password: ${MCCMS_PASSWORD} }
  before_chapter:
    - plugin: skip_chapter_with_few_images
      kwargs: { at_least_image_count: 3 }
  after_comic:
    - plugin: zip
      kwargs: { zip_dir: ./zip, filename_rule: Cname, delete_original_file: false }
```

---

## 8. 命令行用法

安装后即可使用控制台脚本 `mccms`：

```text
usage: mccms [-h] [-o OPTION_FILE] [--impl {tibiu,manhwa}] [-d BASE_DIR]
             [--rule DIR_RULE]
             [--comic | --chapter | --search SEARCH | --info INFO]
             [--comic-id COMIC_ID] [--page PAGE] [--username USERNAME]
             [--password PASSWORD] [--cookie COOKIE]
             [--thread-image THREAD_IMAGE] [--thread-chapter THREAD_CHAPTER]
             [-v]
             [ids ...]
```

| 参数 | 取值 | 说明 |
|---|---|---|
| `ids` | 0~N 个 | 位置参数：漫画 id / 章节 id / URL。给了多个则逐个下载 |
| `-o, --option` | 文件路径 | option yml（或 json）文件；给了它就不再默认 `impl` |
| `--impl` | `tibiu` \| `manhwa` | 站点实现；**未指定 `--impl` 且未给 `-o` 时默认 `tibiu`** |
| `-d, --dir` | 路径 | 下载根目录（覆盖 option 里的 `dir_rule.base_dir`） |
| `--rule` | DSL 字符串 | 目录规则（如 `Bd_Cname_Chindextitle`），覆盖 option 里的 `dir_rule.rule` |
| `--comic` | flag | 把 `ids` 当漫画 id 下载（**默认行为**） |
| `--chapter` | flag | 把 `ids` 当章节 id 下载 |
| `--search` | 关键字 | 只搜索并打印结果，不下载 |
| `--info` | 漫画 id | 只打印漫画信息（作者/状态/话数/封面/简介 + 前 10 话），不下载 |
| `--comic-id` | 漫画 id | 用 `--chapter` 下载章节时指定所属漫画 id |
| `--page` | int，默认 `1` | `--search` 的页码 |
| `--username` / `--password` | str | 账号密码：填了会在建 client 时自动登录（仅访问该账号有权访问的内容） |
| `--cookie` | `name=value`，可重复 | 手工指定 cookie；会写入 postman 会话与 option |
| `--thread-image` | int | 图片并发数（覆盖 `download.threading.image`） |
| `--thread-chapter` | int | 章节并发数（覆盖 `download.threading.chapter`） |
| `-v, --verbose` | flag | 打开彩色详细日志（`enable_pretty_log()`） |
| `-h, --help` | | 帮助 |

`--comic` / `--chapter` / `--search` / `--info` 四者是**互斥**的（同一个 argparse 互斥组）。

示例：

```bash
mccms --impl tibiu 17001                                  # 下载整本（TIBIU）
mccms --impl manhwa mozhouweixianzaoyu                    # 下载整本（漫蛙，slug 也可）
mccms --chapter 291931 --comic-id 17001                   # 下载单章（注意：漫画 id 用 --comic-id）
mccms --info 17001                                        # 只看信息，不下载
mccms --impl manhwa --search 魔咒 --page 2                 # 搜索
mccms -o assets/option/option_workflow.yml 17001 17002     # 用配置文件批量下载
mccms --impl tibiu -d ./downloads --rule 'Bd/{Cauthor}/{Cname}' 17001
mccms --cookie PHPSESSID=xxxx 17001                        # 用自己浏览器的会话 cookie
mccms -v --thread-image 4 17001                            # 详细日志 + 降低并发
```

> ⚠️ `--comic` 是开关（`store_true`，表示「把位置参数当漫画下载」，这也是默认行为），**不接受值**；要指定漫画 id 请用 `--comic-id 17001`。写成 `--comic 17001` 会让 `argparse` 以退出码 2 报错。

退出码：`0` 成功 / `1` mccms 错误 / `2` 参数错误（argparse）/ `3` 无权限（`AccessDeniedException`，会打印"请使用你自己的账号"提示）/ `130` 被 Ctrl-C 中断。

---

## 9. 扩展：自定义插件 / 下载器 / 客户端

### 9.1 自定义插件

```python
from mccms import McOptionPlugin, McModuleConfig, McOption

class MyRecorder(McOptionPlugin):
    plugin_key = 'my_recorder'          # 唯一注册键
    plugin_dependencies = ()            # 需要第三方库时：(import名, pip包名)，如 (('PIL', 'pillow'),)

    def invoke(self, file_path='./record.txt', chapter=None, downloader=None, **kwargs):
        self.require_param(chapter is not None, 'my_recorder 需要 chapter（请挂在章节事件下）')
        with open(file_path, 'a', encoding='utf-8') as f:
            f.write(f'{chapter.chapter_id}\t{chapter.name}\t{len(chapter)}\n')
        self.log(f'已记录 {chapter.chapter_id}')

McModuleConfig.register_plugin(MyRecorder)

option = McOption.construct({
    'plugins': {'after_chapter': [{'plugin': 'my_recorder', 'kwargs': {'file_path': './record.txt'}}]},
})
option.download_comic('17001')
```

插件基类提供的工具：`self.option`、`self.log(msg, topic=None)`、`self.require_param(case, msg)`（抛 `PluginValidationException`）、`self.decide_filepath(comic, chapter, filename_rule, suffix, base_dir, dir_rule_dict=None)`、`self.enter_wait_list()` / `leave_wait_list()` / `wait_until_finish()`（异步插件用）、`self.execute_deletion(paths)`、`self.execute_cmd(cmd)`、`self.execute_multi_line_cmd(cmd)`、`self.warning_lib_not_install(lib, throw=False)`。

完整可运行示例见 [`usage/custom_plugin.py`](usage/custom_plugin.py)（含**离线自检**，不联网也能验证插件被调用）。

### 9.2 自定义下载器

```python
import mccms
from functools import partial

class OnlyLastChapters(mccms.McDownloader):
    """只下载最后 N 话，且每话只下前 M 张图。"""

    def __init__(self, option, chapter_limit=1, image_limit=3):
        super().__init__(option)
        self.chapter_limit = chapter_limit
        self.image_limit = image_limit

    def do_filter(self, detail):            # BaseDownloader 的官方重写点
        if isinstance(detail, mccms.McComicDetail):
            return detail[-self.chapter_limit:]          # 最后 N 话
        if isinstance(detail, mccms.McChapterDetail):
            return detail[:self.image_limit]             # 前 M 张
        return detail

# api 会以 downloader(option) 的形式实例化，额外参数用 partial 传
option = mccms.McOption.construct({'dir_rule': {'base_dir': './downloads'}})
mccms.download_comic('17001', option, partial(OnlyLastChapters, chapter_limit=2, image_limit=5))
```

其它重写点：`before_comic` / `after_comic` / `before_chapter` / `after_chapter` / `before_image(image, path)` / `after_image(image, path)`（记得调用 `super()`，否则插件不会被触发）、`download_by_image_detail(image)`、`create_client()`、`raise_if_has_exception()`。`SomeDownloader.use()` 可以把默认下载器换成你的子类（`McModuleConfig.CLASS_DOWNLOADER`）。内置的 `DoNotDownloadImage`（只建目录）与 `JustDownloadSpecificCountImage`（每话前 N 张）可直接当参数传。

### 9.3 自定义客户端

```python
from mccms import TibiuClient, McModuleConfig, McOption, McSearchPage

class MyMirrorClient(TibiuClient):
    """继承 TIBIU 客户端，只改域名并把搜索结果过滤一遍。"""

    client_key = 'tibiu_mirror'
    site = 'tibiu_mirror'

    def search(self, keyword, page=1) -> McSearchPage:
        result = super().search(keyword, page)
        result.content = [(cid, info) for cid, info in result.content if info.get('name')]
        return result

McModuleConfig.register_client(MyMirrorClient)
McModuleConfig.DOMAIN_LIST_DICT['tibiu_mirror'] = ['mirror.example.com']   # postman 需要域名
McModuleConfig.SITE_NAME_DICT['tibiu_mirror'] = '我的镜像'

option = McOption.construct({'client': {'impl': 'tibiu_mirror'}})
client = option.build_client()
```

要点：

- 必须实现 `get_comic_detail` / `get_chapter_detail` / `search` / `categories_filter` / `update_list` / `fetch_image_urls`（`McClientInterface` 的抽象方法），并设置 `client_key` 与 `site`。
- 可以直接继承 `TibiuClient` / `ManhwaClient`（如上面例子），只改需要改的部分。
- `client.domain`（option）也可以直接写你的镜像域名，这样就不必改 `DOMAIN_LIST_DICT`。
- CLI 的 `--impl` 取值范围来自 `McMagicConstants.SITE_LIST`，自定义站点想在命令行里可选，需要先扩展它：
  `McMagicConstants.SITE_LIST = McMagicConstants.SITE_LIST + ('tibiu_mirror',)`（要在 `build_parser()` 之前执行）。
- 可替换类：`McModuleConfig.CLASS_OPTION` / `CLASS_DOWNLOADER` / `CLASS_COMIC` / `CLASS_CHAPTER` / `CLASS_IMAGE` / `CLASS_SEARCH_PAGE`（默认走 `option_class()` / `downloader_class()` / … 访问器）。

---

## 访问权限说明（重要）

**本库只访问公开内容，以及「当前会话本身有权访问的内容」。**

1. **不做任何绕过**。源码中没有、也不会加入任何绕过登录、绕过会员、绕过付费、破解图片加密之类的实现；遇到服务端拒绝时，只会如实抛出异常。
2. **需要登录**的章节 → `LoginRequiredException`（TIBIU/漫蛙的服务端 `code == 2` 或 `type == login`）。
3. **需要会员 / 金币 / 月票权益**的章节 → `VipRequiredException`（`type ∈ {vip, cion, ticket, pay}`）。
4. 其它被拒绝的情况（下架、审核、不属于该漫画等）→ `ChapterNotAccessibleException`。以上三者都继承 `AccessDeniedException`，只捕获它即可统一处理。
5. **如何提供你自己的账号凭据**（三选一，可组合）：
   - option：`client.username` + `client.password`（新建 client 时自动登录）；
   - option：`client.cookies: {PHPSESSID: '...'}`（或 `client.postman.meta_data.cookies`）直接复用你浏览器里的会话；
   - 命令行：`--username/--password`、`--cookie name=value`（可重复）。
   - 代码里也可以主动登录一次并把会话写回配置：`option.login()`（= `client.login()` + `option.update_cookies(client.postman.cookies)`）。
6. `download_comic()` / `download_chapter()` 在下载过程中遇到权限异常时，会**优先原样抛出** `LoginRequiredException` / `VipRequiredException`（而不是笼统的 `PartialDownloadFailedException`），便于调用方区分「没权限」和「下载出错」。
7. 命令行遇到无权限时会打印提示并返回退出码 `3`。
8. **鉴权由服务端裁决**：`client.check_chapter()` / `client.fetch_image_urls()` 只是发起正常请求并映射服务端返回码，不做任何猜测或尝试。
9. 账号安全建议：不要把账号密码写进提交到仓库的 yml；用 `${MCCMS_USERNAME}` 这类环境变量（插件 kwargs 会展开），或直接用 `--cookie` / `MCCMS_COOKIE` 传会话。
10. 请遵守目标站点的服务条款与所在地法律，仅下载你有权访问的内容。

补充一个**实测行为**：如果提供了错误的账号密码，登录失败会在新建 client 时抛出 `ResponseUnexpectedException`（服务端返回 `code != 1/2/3`，例如「账号不存在，请注册~」），而**不是** `LoginRequiredException`。所以捕获时建议留一个 `except McException` 兜底。

---

## 11. 常见问题（FAQ）

**Q1. `ModuleNotFoundError: No module named 'mccms'`？**
还没安装。在仓库根目录执行 `pip install -e .`（需要 setuptools ≥ 61；老 pip 也能用，仓库里带了 `setup.py` shim）。

**Q2. 提示缺少 `pyzipper` / `img2pdf` / `PIL` / `zhconv`？**
这些是可选依赖，见[第 2 节](#2-安装)。也可以把 `plugins.dependencies_strategy` 改成 `ignore-only-log`（只告警）或 `auto-install`（自动 `pip install`）。`long_img` 插件已声明 `(('PIL', 'pillow'),)`，因此提示里给出的就是正确的 `pip install pillow`。

**Q3. 怎么只下载前 N 张图片？**
三种方式：
```python
from functools import partial
mccms.download_chapter('291931', comic_id='17001',
                       partial(mccms.JustDownloadSpecificCountImage, count=3))   # 每话前 3 张
```
或者自定义下载器重写 `do_filter`（见 [9.2](#92-自定义下载器)），或先 `comic = client.get_comic_detail(...)` 再手动 `client.download_image(image.download_url, path)`。

**Q4. 怎么只下载前 N 话 / 指定几话？**
本库**没有**内置的"话数范围"开关，请在自定义下载器的 `do_filter` 里返回 `comic[:N]` / `comic[-N:]` / `[comic[i] for i in (0, 2, 5)]`。

**Q5. 重复运行会不会重新下载？**
不会。`download.cache: true`（默认）时，目标文件已存在就跳过下载——但 `after_image` 事件依然会触发，插件统计不会漏。

**Q6. 怎么复用 cookies？**
```python
# 1) 直接写在 option 里（会随请求发送）
option = mccms.McOption.construct({'client': {'cookies': {'PHPSESSID': 'xxx'}}})
# 2) 运行时注入（对之后新建的 client 生效）
option.update_cookies({'PHPSESSID': 'xxx'})
# 3) 登录一次后把会话 cookie 存下来，下次直接塞回去
option.login()
print(option.client.postman.meta_data.cookies)      # 登录后已被写回
# 4) CLI
#    mccms --cookie PHPSESSID=xxx 17001
```
注意：`client.cookies` / `meta_data.cookies` 是**随请求发送**的 cookies，不会出现在 `postman.cookies`（会话 cookie jar）里；只有真正发过请求或调用过 `postman.set_cookies()` 之后，`postman.cookies` 才有内容。

**Q7. 下载很慢 / 想更快？**
调大 `download.threading.image`（默认 30）与 `download.threading.chapter`（默认 CPU 核数），命令行用 `--thread-image/--thread-chapter`。但并发过高容易触发站点风控，建议保守。

**Q8. 日志太多 / 太少？**
`log: false` 全关；`log: 'pretty'` 彩色；`plugins` 里挂 `log_topic_filter`（`whitelist: ['comic','chapter']`）只看关心的 topic；单个插件条目可设 `log: false`。

**Q9. 下载报 `PartialDownloadFailedException`，怎么定位？**
```python
except mccms.PartialDownloadFailedException as e:
    dler = e.context['downloader']
    for image, err in dler.download_failed_image: print(image.download_url, err)
    for chapter, err in dler.download_failed_chapter: print(chapter.chapter_id, err)
```
常见原因：网络抖动（提高 `client.retry_times`）、图片链接过期（重跑即可）、并发过高被限流。

**Q10. 异步下载是真的异步吗？**
不是。`download_comic_async()` / `download_chapter_async()` 只是 `asyncio.to_thread(...)` 门面，底层依旧是同步 + 多线程；它们的作用是让你方便地 `asyncio.gather` 多本一起下，且需要 Python ≥ 3.9。

**Q11. `comic[0]` 的章节为什么没有图片？**
`comic[i]` 只带章节元信息（`episode_list` + `chapter_access`），图片列表要另外取：`client.fetch_image_urls(chapter)` 或 `client.get_chapter_detail(chapter.chapter_id, comic_id=comic.comic_id)`。下载流程内部会自动做这一步。

**Q12. 目录里出现了 `default_author`？**
站点没有提供作者信息时，`McChapterDetail.author` 会退化为 `McModuleConfig.DEFAULT_AUTHOR`（`'default_author'`）。不要在 `dir_rule` 里用 `Chauthor`，或者用 `CFIELD_ADVICE` 注册一个更稳的字段。

**Q13. `copy_option()` 会重复触发 `after_init` 插件吗？**
不会。`copy_option()` 内部用 `construct(..., cover_default=False, call_after_init_plugin=False)` 重建，`login` 之类的 `after_init` 插件不会被执行第二次（与 jmcomic 行为一致）。

```python
copied = option.copy_option()          # 不触发 after_init

# 需要更细的控制时，construct() 也支持该开关
same = mccms.McOption.construct(option.deconstruct(), call_after_init_plugin=False)
```

**Q14. 命令行 `--comic 17001` 报错？**
`--comic` 是开关不是取值参数，正确写法是 `mccms --chapter 291931 --comic-id 17001` 或直接 `mccms 17001`（默认就按漫画下载）。

---

## 12. 示例文件索引

### 可运行示例（`usage/`）

| 文件 | 内容 | 是否需要网络 |
|---|---|---|
| [`usage/get_comic_detail.py`](usage/get_comic_detail.py) | 只用 API：搜索 / 详情 / 章节列表 / 前几张图片 URL / 本地预览 dir_rule | 需要 |
| [`usage/download_comic.py`](usage/download_comic.py) | 下载整本（含 option 配置 + 自定义下载器限制范围 + 批量下载） | 需要 |
| [`usage/download_chapter.py`](usage/download_chapter.py) | 下载单章 + `JustDownloadSpecificCountImage` 只下前 N 张 | 需要 |
| [`usage/search_and_filter.py`](usage/search_and_filter.py) | 搜索 + 分类 + 榜单（按站点分别演示） | 需要 |
| [`usage/custom_plugin.py`](usage/custom_plugin.py) | 自定义插件并注册使用（含**离线自检**） | 离线自检不需要；`--comic` 时下载需要 |
| [`usage/with_account.py`](usage/with_account.py) | 用**自己的账号**访问有权访问的内容，并友好处理无权限异常 | 需要 |

每个脚本都支持 `--help`；默认参数都是小范围（1 话 / 前 3 张），不会一口气拉几百张图。

### 示例配置（`assets/option/`）

| 文件 | 用途 |
|---|---|
| [`assets/option/option_default.yml`](assets/option/option_default.yml) | 全量默认配置，每个 key 带中文注释 |
| [`assets/option/option_tibiu.yml`](assets/option/option_tibiu.yml) | TIBIU 站点示例（含 `login` 插件、封面插件） |
| [`assets/option/option_manhwa.yml`](assets/option/option_manhwa.yml) | 漫蛙站点示例（含按话打包 zip） |
| [`assets/option/option_workflow.yml`](assets/option/option_workflow.yml) | CI / 批量下载（关日志、高并发、zip 产物、`${ENV}` 占位） |

```bash
# 直接使用示例配置
python -c "import mccms; mccms.create_option_by_file('assets/option/option_tibiu.yml').download_comic('17001')"
mccms -o assets/option/option_workflow.yml 17001
```

### 源码结构

```
src/mccms/
├── __init__.py              对外导出与插件/客户端注册
├── api.py                   download_comic / download_chapter / download_batch / create_option_*
├── cli.py                   命令行入口（console_scripts: mccms）
├── mc_config.py             McMagicConstants / McModuleConfig / 日志 / 默认 option
├── mc_option.py             McOption / DirRule
├── mc_downloader.py         McDownloader / 下载清单 / DownloadResult / BatchResult
├── mc_plugin.py             McOptionPlugin + 10 个内置插件
├── mc_entity.py             McComicDetail / McChapterDetail / McImageDetail / 分页
├── mc_client_interface.py   McClientInterface / AbstractMcClient
├── tibiu_client_impl.py     TIBIU 客户端（纯 JSON）
├── manhwa_client_impl.py    漫蛙客户端（JSON + HTML）
├── mc_postman.py            HTTP 层（requests / curl_cffi、域名轮换、重试）
├── mc_html.py               零依赖 HTML 解析 + CSS 选择器子集（仅漫蛙用）
├── mc_toolkit.py            AdvancedDict / PackerUtil / MccmsText / 文件与并发工具
├── mc_exception.py          异常体系
└── mc_task_context.py       并发下载的任务上下文（日志前缀）
```

---

## License

MIT（见 `pyproject.toml`）。

本项目仅供学习与个人备份使用。请遵守目标站点的服务条款与所在地法律法规，**只下载你有权访问的内容**；作者不对任何滥用行为负责。
