# 三站接口逆向文档

本文记录三个站点的接口结构，所有结论均来自实际请求验证
（样本见 `recon/samples/` 与 `tests/fixtures/`）：

- `cache.tibiu.net`（TIBIU）
- `www.manhwa.wang`（漫蛙）
- `boylove.cc`（香香腐宅，入口为轮换短链 `fufuhouse.work/{code}`）

前两个站点运行 **Mccms** CMS（页面里带有 `Mccms core JS build` 标记），
但模板与接口层差异很大：TIBIU 是 WAP 模板 + 一套定制的 `/api/data/*` JSON 层；
漫蛙是原生 PC 模板，JSON 与 HTML 混用。
第三个站点是完全不同的技术栈（ThinkPHP + Laravel 短链），见第 4 节。

---

## 1. 通用约定

### 1.1 响应信封

Mccms 有两套信封，必须分别处理（客户端在 `AbstractMcClient.parse_resp` 中统一）：

**A. 详情类接口**

```json
{"code": 1, "msg": "章节列表", "data": [...]}
```

| code | 含义 |
|---|---|
| `1` | 成功，数据在 `data` |
| `2` | 未登录 |
| `3` | 已登录但缺少权益（可能伴随 `type: "vip" \| "cion"`） |
| `-1` | 参数错误 / 非法请求 |

**B. 列表类接口**（`category_api` / `update_api`）

```json
{"code": -1, "status": "success", "message": "Results found.", "error": "...", "page": 1,
 "per_page": 10, "total_pages": 7, "total_items": 62, "data": [...]}
```

> ⚠️ 这类接口的 `code` **恒为 -1**，成功与否只能看 `status == "success"`。
> 如果只判断 `code == 1`，会把所有成功响应误判为失败。

### 1.2 用户态

```
GET /index.php/api/user/info
-> {"code":1,"data":{"log":0,"id":0,"nichen":"游客","email":"","pic":"...",
                     "vip":0,"cion":0,"ticket":0,"viptime":0,"adult":0,
                     "sensitive_content_allowed":0,"fav_count":0,"read_count":0,
                     "bookshelf_count":0}}
```

`log` = 是否登录，`vip` / `cion` / `ticket` = 权益数量，`adult` = 是否已通过年龄门。

登录：

```
GET|POST /index.php/api/user/login?name={账号}&pass={密码}&islog=1&pcode={图形验证码}
```

成功后会话 cookie 落在响应里，后续请求带上即可。

---

## 2. TIBIU（cache.tibiu.net）

### 2.1 业务接口（纯 JSON）

| 用途 | 请求 | 关键返回 |
|---|---|---|
| 搜索 | `GET /api/data/search?key={kw}&page={n}` | `data[]`，每页 10 条 |
| 漫画详情 | `GET /api/data/comicinfo?cid={comic_id}` | `data` 为漫画对象 |
| 章节列表 | `GET /api/data/chapter?mid={comic_id}` | `data[]`，**倒序**（最新在前） |
| 章节图片 | `GET /api/data/pic?cid={chapter_id}` | `data[]`，**按 id 降序** |
| 分类浏览 | `GET /index.php/api/data/category_api?page={n}[&order=][&tags=][&tids=]` | 列表信封 |
| 最近更新 | `GET /index.php/api/data/update_api?page={n}` | 列表信封 |
| 筛选项 | `GET /index.php/api/data/filter_options_api?mode={update\|...}` | 分类/标签字典 |
| 榜单导航 | `GET /index.php/api/rankdata/nav` | `data[]`，含 `type/name/url` |
| 榜单列表 | `GET /index.php/api/rankdata/lists?type={t}&page={n}&max_items={m}` | 列表信封 |
| 小说列表 | `GET /api/data/book` | `data[]`（本库未实现小说下载） |

榜单 `type` 取值：`top`（人气）、`ticket`（月票）、`fav`（收藏）、`day`、`week`、`month`、`ascension`。

### 2.2 关键字段

**comicinfo / 列表项**

```
id, name, yname, pic(封面), picx, cid, tid, serialize(连载/完结), author, uid,
notice, pic_author, txt_author, text(短简介), content(长简介),
hits, yhits, zhits, rhits, shits, pay, cion, ticket, sid, nums(章节数),
score, did, ly, yid, msg, addtime, tpl, cadult(是否限制级),
originalname, alias, oid, artwork_id
列表项额外含: tids, tags
```

**chapter**

```
id(章节id), xid(漫画内序号), mid(漫画id), name, vip, cion, pnum(图片数), addtime
```

**pic**

```
id(图片id), img(图片URL), width, height
```

### 2.3 ⚠️ 图片顺序

`/api/data/pic` 返回的 `data` 是 **id 降序**的：

```
id=11238196, 11238195, 11238194, ...
```

而网页端在渲染前做了升序排序（`readpage/chaptercache4.js`）：

```js
return chapterPicData.slice().sort((a, b) => Number(a.id) - Number(b.id));
```

因此客户端**必须按数字 id 升序重排**，否则图片顺序是反的：

```python
raw_list = sorted(raw_list, key=lambda item: int(item['id']))
```

同理，章节列表要按 `xid` 升序排列。

### 2.4 HTML 路由

```
/comic/{comic_id}              漫画详情页
/chapter/{comic_id}/{chapter_id}  阅读页
/rank, /rank/{type}            排行榜
/page/category, /page/update    分类 / 更新
/search?key={kw}               搜索页（与 JSON 搜索等价，本库用 JSON）
```

注意 TIBIU 的章节 URL **同时包含漫画 id 和章节 id**，
所以 `parse_chapter_url()` 能一次解析出两个 id，从而得到完整的章节元信息。
反之只给 `chapter_id` 时无法反查所属漫画（没有对应的接口），
此时章节名会退化为 `第{chapter_id}话`。

### 2.5 年龄门

限制级内容（`cadult == 1`）需要先确认年龄：

```
GET /index.php/user/info/confirmadult
```

客户端在 `McModuleConfig.FLAG_AUTO_CONFIRM_ADULT = True`
或 option 里 `client.confirm_adult: true` 时自动调用。

---

## 3. 漫蛙（www.manhwa.wang）

### 3.1 接口

| 用途 | 请求 | 说明 |
|---|---|---|
| 章节列表 | `GET /index.php/api/comic/chapter?mid={comic_id}` | JSON；`data[].{id,name,link,pnum,price,vip,cion}` |
| 热门列表 | `GET /index.php/api/comic/hot` | JSON；`data[].{id,pic,name,author,text,url}` |
| 章节图片（有权限时） | `GET /index.php/api/comic/isbuy?id={chapter_id}` | 见 3.3 |
| 搜索 | `GET /index.php/search/{keyword}/{page}` | HTML；**路径式分页** |
| 分类 | `GET /index.php/category/order/{order}[/finish/{n}][/pay/{n}][/tags/{n}][/quality/{n}][/copyright/{n}]/list/{page}` | HTML |
| 漫画详情 | `GET /index.php/comic/{id或slug}` | HTML；数字 id 和 slug 都可 |
| 阅读页 | `GET /index.php/chapter/{chapter_id}` | HTML |

`order` 取值实测有效：`hits`（人气）、`addtime`（更新）。

> 搜索分页注意：`?key=x&page=2`、`?key=x&p=2`、`/search/{key}/page/2` **都不生效**，
> 只有 `/index.php/search/{key}/{page}` 这种路径式写法能翻页。

### 3.2 HTML 结构

**列表卡片**（搜索页 / 分类页通用）

```html
<div class="common-comic-item">
  <a class="cover" href="/index.php/comic/{slug}">
    <img class="lazy" data-original="{封面}" alt="{标题}">
    <p class="comic-feature">{简介}</p>
  </a>
  <p class="comic__title"><a href="/index.php/comic/{slug}">{标题}</a></p>
  <p class="comic-update">更至：<a class="hl" href="/index.php/chapter/{id}">{最新章}</a></p>
  <p class="comic-count">人气：{n}</p>
</div>
```

> 搜索/分类卡片**不含作者字段**，所以列表结果的 `author` 为空；
> 要看作者必须进入详情页。

**详情页**

```
.de-info__box .comic-title           标题
.de-info__cover img[src]             封面
.comic-author .name a                作者（可能是 A/B 形式）
.comic-intro .intro-total / .intro   简介
.j-user-collect[data-id]             数字漫画 id（URL 是 slug，这里才是 id）
.de-chapter__title > span            连载 / 完结
.de-chapter__title .update-time      最新章
.comic-status .text                  收藏 / 人气
a[href*="/category/tags/"]           标签
```

**阅读页**

```
.read__crumb a.crumb__title          漫画名 + /index.php/comic/{slug}
.read__crumb h1.comic-title a        章节名
.page-index__btn .count              图片总数
.rd-article__pic img[data-original]  免费章节的内联图片
readPic({mid},{cid},{vip},{cion})    内联 JS，给出漫画 id 与权限标记
```

### 3.3 图片下发与权限

漫蛙的免费章节图片**直接内联在阅读页 HTML 里**；
`isbuy` 接口则要求登录，返回码语义如下：

| 返回 | 含义 | 本库行为 |
|---|---|---|
| `{"code":1,"pic":[{id,img}]}` | 会话本身有权限 | 正常返回图片 |
| `{"code":2,"msg":"登录超时"}` | 未登录 | 抛 `LoginRequiredException` |
| `{"code":3,"type":"vip"}` | 已登录但缺会员权益 | 抛 `VipRequiredException` |
| `{"code":3,"type":"cion"}` | 已登录但缺金币 | 抛 `VipRequiredException` |

客户端的取图顺序：

1. 读阅读页 HTML 里的 `.rd-article__pic img[data-original]`（免费章节走这条）
2. 为空则调用 `isbuy`，由服务端裁决权限

**本库不包含任何绕过上述判定的实现**；无权时只做「明确报错」。

### 3.4 内容可访问性实测

对热门列表前 5 本漫画统计（`tests/integration_live.py` 会打印）：

```
共 560 话，其中免费 1 话
```

即漫蛙站点几乎所有章节都是 `vip=1`，且唯一那话免费章节的图片已经
从 CDN 移除（HTTP 404）。所以该站的实际可用性取决于账号权益。

---

## 4. 香香腐宅（boylove.cc）

### 4.1 入口是轮换短链

```
https://fufuhouse.work/TtaTcG
   ↓ 302（带 utm_source=18comic&utm_medium=18comic&utm_campaign=240826）
https://byblovecw7.org          ← 轮换镜像
   ↓ 301
https://boylove.cc              ← 主站
```

短链服务是 Cloudflare 上的 Laravel 应用（会下发 `XSRF-TOKEN` / `laravel_session`），
同一路径多次请求会给出不同镜像域名。主站 `boylove.cc` 与图片站 `img.boylove.cc`
都是 Cloudflare 前置。

### 4.2 站点存在两套接口

| | 网页端 | App 端 |
|---|---|---|
| 信封 | `{"code":0|1,"result":...}` | `{"code":200,"data":...}` |
| 鉴权 | 无 | 需要客户端签名 |
| 路径示例 | `/home/api/searchk` | `/latest` `/api/album` |
| 本库 | **使用这一套** | **不使用** |

App 端接口的信封与路径跟 18comic(JM) 的 App API 同构，缺少签名时返回：

```json
{"code":401,"data":[],"errorMsg":"Not legal request."}
```

签名形式与 jmcomic 里的实现一致（`token = md5(ts + 客户端密钥)`、
`tokenparam = "{ts},{app_version}"`，`/setting` 自报版本 `2.0.2`）。

**本库不触碰 App 端接口**：不复用、不推导任何客户端签名或凭证。
网页端 API 已经覆盖搜索/列表/详情/章节/图片的全部需求，
没有理由去伪造官方客户端身份。

### 4.3 网页端 JSON API

| 用途 | 请求 | 备注 |
|---|---|---|
| 搜索 | `GET /home/api/searchk?keyword=&type=&pageNo=` | `type` 0/1=漫画，2=小说；返回 `lastPage` 而非总数 |
| 章节列表 | `GET /home/api/chapter_list/tp/{comic_id}` | 一次返回全部章节；`pageSize` 字段是幌子 |
| 榜单 | `GET /home/api/rank/type/{n}` | 一次返回四组：`most_clicks` / `most_consumes` / `most_favorites` / `most_search` |
| 分类 | `GET /home/api/cate/tp/{cate}-{tag}-{done}-{order}-{page}-{is18}-1-{vip}?mt=0` | **必须拼满 8 段**，少一段就 `Server Error` |
| 漫画详情 | `GET /home/book/index/id/{comic_id}` | HTML |
| 章节阅读 | `GET /home/book/capter/id/{chapter_id}` | HTML（路径就是 `capter` 这个拼写） |

成功码不统一：**搜索返回 `code:0`，列表/章节/榜单返回 `code:1`**，两者都要当成功处理。
失败形如 `{"code":500,"data":[],"errorMsg":"Server Error"}`。

### 4.4 列表项字段（信息量很大）

搜索/榜单/分类返回同一套对象，含：

```
id, title, auther, desc, image/cover, keyword(标签,逗号分隔),
mhstatus(0连载/1完结), pingfen(评分), watchtimes(观看), mark(收藏),
last_chapter_title, type, status, cjname/cj_url(来源站)
权限标志: vipcanread, login_read, unlock, member_only, isfree, limitVip,
          latest_two_chapters_vip, app_only
```

也就是说**列表页就能拿到作者、标签、简介、评分、状态**，不必再进详情页。

### 4.5 详情页与阅读页结构

详情页：

```html
<meta property="og:title"       content="无根树/无根之树 - 香香腐宅...">
<meta property="og:description" content="《뿌리 없는 나무》 ...">
<meta property="og:image"       content="/bookimages/img/20260910/xxx.webp">   <!-- 需补 https://img.boylove.cc -->
<meta name="keywords"           content="韩漫,偏执攻,美人,...">                 <!-- 标签 -->

<div class="stui-content__detail">
  <h1>无根树/无根之树</h1>
  <p class="data">连载中</p>
  <p class="data"><i>作者：</i><a href="...">라포</a></p>
  <p class="data"><i>觀看數：</i>7488276</p>
  <p class="data"><i>最新话数：</i>第102话</p>
  <div class="rating" data-avg="4.73" data-count="302">
</div>

<script> function getChapterList(asc) {
  let data = JSON.parse("{\"list\":[{\"id\":435648,\"isvip\":\"0\",\"title\":\"第01话\",...}]}");
} </script>
```

阅读页：

```html
<title>[第01话] 无根树/无根之树 - 香香腐宅BoyLove│...</title>
<img id="webp1" src="https://img.boylove.cc/static/images/load.png"
     data-original="https://img.boylove.cc/bookimages/20221209/435648-8b12a7....webp"
     alt="图-1" class="lazy">
```

### 4.6 ⚠️ 三个坑

1. **章节 id 不单调**。章节列表是 `435648, 435649, 435647, 435650, ...`，
   阅读顺序由**列表顺序**决定，绝不能按 id 排序。
2. **阅读页混有推荐位缩略图**。正文图路径是
   `/bookimages/{yyyymmdd}/{chapter_id}-{hash}.webp`，
   而推荐位是 `/bookimages/img/{yyyymmdd}/{hash}.jpg`（注意中间那个 `img/`）。
   只按 `img[data-original]` 抓会把推荐位一起收进来，
   必须用「文件名以章节 id 开头」精确筛选。

3. **新章节的图片是乱序的**，见 4.7。

### 4.7 图片乱序：竖带倒序（重要）

站点有两代图片管线，**以 61 话左右为界**：

| | 老章节（约 1–41 话） | 新章节（约 61 话起） |
|---|---|---|
| 文件名 | `{chapter_id}-{md5}.webp` | 随机串 `IdR3FjXlm...webp?w=650_993` |
| 下载后 | 正常图片，直接可看 | **竖带被倒序，错位** |
| 前端处理 | 直接 `<img>` 显示 | 隐藏 `<img>`，画到 `<canvas>` 上还原 |
| 内联数据 | 无 | `var imageData=[{id,src,width,height}...]` + `var randomClass=N` |

前端还原逻辑（`do_mergeImg`，站点里被混淆过，这里给出等价源码）：

```js
function do_mergeImg(canvas, img, W, H, url, N) {
    img.src = url;
    img.addEventListener('load', function () {
        for (var i = 1; i <= N; i++) {
            if (H >= 4000) {                       // 超高图：原样拷贝（等于不解码）
                canvas.drawImage(img, floor(W/N)*(i-1), 0, floor(W/N), H,
                                      floor(W/N)*(i-1), 0, floor(W/N), H);
            } else if (i == N) {                   // 最左那条（含宽度余数）落到最右
                var w = W - floor(W/N)*(N-1);
                canvas.drawImage(img, 0, 0, w, H, floor(W/N)*(N-1), 0, w, H);
            } else {                               // 右边第 i 条 -> 左边第 i 个位置
                var w = floor(W/N);
                canvas.drawImage(img, W - floor(W/N)*i, 0, w, H,
                                      floor(W/N)*(i-1), 0, w, H);
            }
        }
    });
}
```

即：**把整页图切成 N 条竖带，整体倒序**。
倒序是自逆运算，所以同一段逻辑既可乱序也可还原。

**N（`randomClass`）逐章不同**，实测同一本漫画里出现过 `11 / 13 / 18 / 23`，
必须从每章的阅读页读取，不能写死。

#### 实测验证方法

把「竖带接缝处的列间断强度」与「全图随机列间断强度」做 z 检验：
还原前接缝平均 z ≈ **+5.6**（明显断裂），还原后 ≈ **0.0**（连续）。

```
接缝   x    修复前   修复后    前z     后z
  4   236   33.99    3.49   +7.68   +0.16
  7   413   57.49    3.65  +13.48   +0.20
  9   531   38.13    0.45   +8.70   -0.59
平均 z:  +5.62  ->  -0.04
```

本库在 `mccms/mc_decode.py` 里复刻了这套还原，
下载时由 `image.scramble_n` 自动触发（可用 `download.image.decode: false` 关闭）。

### 4.8 请求注意事项：系统代理

本机实测：`curl`（macOS 自带 LibreSSL 版）直连 `boylove.cc` 会握手失败
（`SSL_ERROR_SYSCALL`），而 `requests` 正常——原因是
**`requests` 会经 `urllib.request.getproxies()` 读取系统代理设置**
（macOS 上是 `scutil --proxy`，本机为 `127.0.0.1:7890`），某些命令行工具则不会。

所以：用本库访问该站不需要任何额外处理；
若工具不走系统代理，请显式配置 `client.postman.meta_data.proxies`，
或用 `client.postman.meta_data.resolve` 指定可用 IP。

---

## 5. 与 jmcomic 的对应关系

| 概念 | jmcomic | mccms |
|---|---|---|
| 配置对象 | `JmOption` | `McOption` |
| 客户端抽象 | `JmClientInterface` | `McClientInterface` / `AbstractMcClient` |
| 客户端实现注册 | `REGISTRY_CLIENT['api'/'html']` | `REGISTRY_CLIENT['tibiu'/'manhwa']` |
| 顶层实体 | `JmAlbumDetail` | `McComicDetail` |
| 次级实体 | `JmPhotoDetail` | `McChapterDetail` |
| 图片实体 | `JmImageDetail` | `McImageDetail` |
| 分页 | `JmSearchPage` | `McSearchPage` |
| 下载器 | `JmDownloader` | `McDownloader` |
| 插件 | `JmOptionPlugin` | `McOptionPlugin` |
| 路径规则 | `dir_rule` + `Bd_Aid_Pindex` | `dir_rule` + `Bd_Cid_Chindex` |
| 下载入口 | `download_album` / `download_photo` | `download_comic` / `download_chapter`（保留 album/photo 别名） |
| 图片解密 | 需要 `decode` 还原混淆图 | 三个站点的图片均为明文直出，`decode` 参数保留但恒为空操作 |

---

## 6. 复现方式

```bash
# 单元测试（离线，使用 tests/fixtures 里的真实响应）
PYTHONPATH=src python3 -m unittest discover -s tests -p 'test_*.py'

# 实网集成冒烟
python3 tests/integration_live.py
```

原始页面样本保存在 `recon/samples/`，
接口响应样本保存在 `tests/fixtures/`。
