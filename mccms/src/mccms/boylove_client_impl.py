"""
香香腐宅（boylove.cc）站点客户端。

入口说明
--------
该站通过短链轮换入口，例如 ``https://fufuhouse.work/TtaTcG``
→ ``https://byblovecw7.org`` → ``https://boylove.cc``，
短链跳转带 ``utm_source=18comic`` 推广参数。

网络说明：主域名与图片域名（``img.boylove.cc``）都能正常直连，
但如果本机配了系统代理，``requests`` 会自动读取系统代理设置
（macOS 下经由 ``urllib.request.getproxies()``），而某些命令行工具
（如系统自带的 LibreSSL 版 curl）可能不走代理而握手失败——
这种情况下请用本库或显式配置 ``client.postman.meta_data.proxies``。

实现范围（重要）
----------------
站点存在两套接口：

1. **网页端 JSON API** —— 本实现使用这一套，公开可访问，无需任何凭证::

       GET /home/api/searchk?keyword=&type=&pageNo=
       GET /home/api/chapter_list/tp/{comic_id}
       GET /home/api/rank/type/{rank_type}
       GET /home/api/cate/tp/{cate}-{tag}-{done}-{order}-{page}-{is18}-1-{vip}?mt=0
       GET /home/book/index/id/{comic_id}        (HTML，含内联章节 JSON)
       GET /home/book/capter/id/{chapter_id}     (HTML，图片直出)

2. **App 端接口** —— ``/setting`` ``/latest`` ``/api/album`` 等，
   信封形如 ``{"code":200,"data":...}``，与 18comic(JM) 的 App API 同构。
   该族接口要求客户端签名::

       token      = md5(ts + <客户端密钥>)
       tokenparam = "{ts},{app_version}"

   缺少签名时返回 ``{"code":401,"data":[],"errorMsg":"Not legal request."}``。

   **本实现不触碰第 2 套接口**：不复用、不推导任何客户端签名或凭证。
   网页端 API 已能覆盖搜索/列表/详情/章节/图片的全部需求，
   因此没有理由去伪造官方客户端身份。

权限语义
--------
章节项带 ``isvip`` 字段；漫画项带 ``vipcanread`` / ``login_read`` /
``unlock`` / ``member_only`` / ``limitVip`` 等权益标志。
本库只访问公开内容与当前会话本身有权访问的内容，无权时抛出明确的
``LoginRequiredException`` / ``VipRequiredException``，不做任何绕过尝试。
"""

import re
from typing import Dict, List, Optional, Tuple

from .mc_client_interface import AbstractMcClient
from .mc_config import McMagicConstants, McModuleConfig
from .mc_entity import McChapterDetail, McComicDetail, McSearchPage
from .mc_exception import ExceptionTool
from .mc_html import Node, parse_html
from .mc_postman import McPostman
from .mc_toolkit import MccmsText

__all__ = ['BoyloveClient']


class BoyloveClient(AbstractMcClient):
    client_key = McMagicConstants.SITE_BOYLOVE
    site = McMagicConstants.SITE_BOYLOVE

    # ------------------------------------------------------------------ 路径

    PATH_SEARCH = '/home/api/searchk'
    PATH_CHAPTER_LIST = '/home/api/chapter_list/tp/{comic}'
    PATH_RANK = '/home/api/rank/type/{rank_type}'
    PATH_CATE = '/home/api/cate/tp/{spec}'
    PATH_COMIC = '/home/book/index/id/{comic}'
    PATH_READER = '/home/book/capter/id/{chapter}'

    # 图片 CDN
    IMAGE_BASE = 'https://img.boylove.cc'

    # 搜索 type：0/1 = 漫画，2 = 小说
    SEARCH_TYPE_COMIC = 0
    SEARCH_TYPE_NOVEL = 2

    # 分类默认参数
    CATE_DEFAULT = {
        'cate': 1,      # 栏目
        'tag': 0,       # 标签
        'done': 2,      # 进度：0 连载 / 1 完结 / 2 全部
        'order': 1,     # 排序：1 更新 / 2 人气 …
        'is18': 0,      # 是否 18+
        'vip': 2,       # 权益：0 免费 / 1 VIP / 2 全部
    }

    def __init__(self, postman: McPostman = None, domain_list=None, retry_times=None,
                 username=None, password=None, confirm_adult=False):
        super().__init__(postman, domain_list, retry_times)
        self.ensure_login(username, password)

    # ================================================================== 响应信封

    def _api(self, path: str, context: Optional[Dict] = None, **kwargs) -> Dict:
        """
        调用网页端 JSON API 并拆信封。

        成功码：``0``（搜索）与 ``1``（列表/章节/榜单）都表示成功。
        """
        resp = self.postman.get(path, **kwargs)
        if resp.is_not_success:
            ExceptionTool.raises(
                f'请求失败: [{resp.url}]，http_code: [{resp.http_code}]',
                {'site': self.site, **(context or {})},
            )

        payload = resp.json()
        code = MccmsText.safe_int(payload.get('code', -1), -1)

        if code in (0, 1):
            if 'result' in payload:
                return payload['result'] or {}
            return payload.get('data') or {}

        msg = (payload.get('errorMsg') or payload.get('msg')
               or f'接口返回异常: code={code}')
        ctx = {'site': self.site, 'url': resp.url, 'code': code, **(context or {})}

        if code == 401:
            # App 端签名校验失败 / 未登录
            from .mc_exception import LoginRequiredException
            raise LoginRequiredException(f'{msg}（该接口需要有效的登录态或客户端凭证）', ctx)

        if code == 403:
            from .mc_exception import AccessDeniedException
            raise AccessDeniedException(f'{msg}', ctx)

        from .mc_exception import ResponseUnexpectedException
        ExceptionTool.raises(msg, ctx, ResponseUnexpectedException)

    # ================================================================== 搜索

    def search(self, keyword: str, page: int = 1,
               search_type: int = SEARCH_TYPE_COMIC) -> McSearchPage:
        keyword = str(keyword).strip()
        ExceptionTool.require_true(keyword != '', '搜索关键字不能为空')

        result = self._api(self.PATH_SEARCH, params={
            'keyword': keyword,
            'type': search_type,
            'pageNo': max(1, int(page)),
        })

        raw_list = result.get('list') or []
        content = [(str(raw.get('id')), self._build_info(raw)) for raw in raw_list]

        total = self._guess_total(len(content), page)
        return McSearchPage(content, total, page)

    @staticmethod
    def _guess_total(current_count: int, page: int, page_size: int = 30) -> int:
        if current_count == 0:
            return 0
        if current_count < page_size:
            return (page - 1) * page_size + current_count
        return page * page_size + 1

    @classmethod
    def _build_info(cls, raw: Dict) -> Dict:
        image = str(raw.get('image') or raw.get('cover') or '')
        cover = image if image.startswith('http') else (cls.IMAGE_BASE + image if image else '')

        tags = [t.strip() for t in str(raw.get('keyword') or '').split(',') if t.strip()]

        return {
            'id': str(raw.get('id')),
            'name': MccmsText.unescape(raw.get('title', '')),
            'author': MccmsText.unescape(raw.get('auther', '') or ''),
            'cover': cover,
            'text': MccmsText.unescape(raw.get('desc', '') or ''),
            'serialize': '完结' if MccmsText.safe_int(raw.get('mhstatus', 0)) == 1 else '连载',
            'update_date': str(raw.get('update_time', '') or ''),
            'views': MccmsText.safe_int(raw.get('watchtimes', raw.get('view', 0))),
            'score': float(raw.get('pingfen', 0) or 0),
            'tags': tags,
            'latest_chapter': MccmsText.unescape(raw.get('last_chapter_title', '') or ''),
            'site': cls.site,
            'url': f'/home/book/index/id/{raw.get("id")}',
            'raw': raw,
        }

    # ================================================================== 详情

    def get_comic_detail(self, comic_id, *, fetch_chapters: bool = True) -> McComicDetail:
        comic_id = MccmsText.parse_to_mc_id(comic_id)

        html = self.req_text(self.PATH_COMIC.format(comic=comic_id))
        root = parse_html(html)

        comic = self._parse_detail_page(root, html, comic_id)

        if fetch_chapters:
            episode_list, access_map = self._fetch_chapter_list(comic_id)
            comic.episode_list = comic.distinct_episode(episode_list)
            comic.chapter_access = access_map

        return comic

    def _parse_detail_page(self, root: Node, html: str, comic_id: str) -> McComicDetail:
        # 标题：og:title 比 <title> 干净（后者带站点后缀）
        name = ''
        og_title = root.find('meta[property="og:title"]')
        if og_title is not None:
            name = og_title.attr('content', '') or ''
        if not name:
            title_node = root.find('h1')
            name = title_node.text if title_node is not None else ''
        name = name.split(' - 香香腐宅')[0].strip()

        # 简介
        description = ''
        og_desc = root.find('meta[property="og:description"]')
        if og_desc is not None:
            description = og_desc.attr('content', '') or ''
        description = description.replace('"br /"', '\n').strip()

        # 封面
        cover = ''
        og_image = root.find('meta[property="og:image"]')
        if og_image is not None:
            cover = og_image.attr('content', '') or ''
        if cover and not cover.startswith('http'):
            cover = self.IMAGE_BASE + cover

        # 标签：meta keywords
        tags: List[str] = []
        kw_node = root.find('meta[name="keywords"]')
        if kw_node is not None:
            tags = [t.strip() for t in (kw_node.attr('content', '') or '').split(',') if t.strip()]

        # 详情区：<p class="data"> 里带 <i>字段名：</i>
        serialize = ''
        authors: List[str] = []
        views = 0

        for p in root.find_all('p.data'):
            label_node = p.find('i')
            label = label_node.text.replace('：', '').replace(':', '').strip() if label_node is not None else ''
            value = p.text.strip()

            if label == '' and serialize == '':
                serialize = value
                continue
            if '作者' in label:
                for a in p.find_all('a'):
                    author_name = a.text.strip()
                    if author_name and author_name not in authors:
                        authors.append(author_name)
            elif '觀看' in label or '观看' in label:
                views = MccmsText.parse_human_number(value.replace(label, ''))

        score = 0.0
        rating = root.find('.rating')
        if rating is not None:
            try:
                score = float(rating.attr('data-avg', '0') or 0)
            except ValueError:
                score = 0.0

        return McComicDetail(
            comic_id=comic_id,
            name=name,
            author=authors[0] if authors else '',
            authors=authors,
            tags=tags,
            description=description,
            cover=cover,
            serialize=serialize,
            views=views,
            score=score,
            site=self.site,
            url=f'/home/book/index/id/{comic_id}',
        )

    def _fetch_chapter_list(self, comic_id: str) -> Tuple[List[Tuple], Dict[str, Dict]]:
        result = self._api(self.PATH_CHAPTER_LIST.format(comic=comic_id),
                           context={'comic_id': comic_id})

        raw_list = result.get('list') or []
        if not raw_list:
            return [], {}

        episode_list: List[Tuple] = []
        access_map: Dict[str, Dict] = {}

        # 接口已按章节顺序返回；章节 id 并不单调，所以用列表下标作为序号
        for index, raw in enumerate(raw_list, start=1):
            chapter_id = str(raw.get('id'))
            episode_list.append((chapter_id, index, MccmsText.unescape(raw.get('title', ''))))
            access_map[chapter_id] = {
                'vip': MccmsText.safe_int(raw.get('isvip', 0)),
                'cion': 0,
                'price': 0,
                'pnum': 0,          # 该接口不返回图片数，取图后可知
                'addtime': str(raw.get('create_time', '') or ''),
            }

        return episode_list, access_map

    # ================================================================== 章节

    def get_chapter_detail(self,
                           chapter_id,
                           *,
                           comic_id=None,
                           fetch_image_urls: bool = True) -> McChapterDetail:
        chapter_id = MccmsText.parse_to_mc_id(chapter_id)

        html = self.req_text(self.PATH_READER.format(chapter=chapter_id))
        meta = self.parse_reader_page(html, chapter_id)

        comic_id = str(comic_id) if comic_id else ''
        name = meta['name'] or f'第{chapter_id}话'
        index = 1

        if comic_id:
            # 顺带拿到该话在漫画中的序号
            try:
                episode_list, access_map = self._fetch_chapter_list(comic_id)
                for cid, cindex, cname in episode_list:
                    if str(cid) == chapter_id:
                        index, name = cindex, cname
                        break
                meta['access'].update(access_map.get(chapter_id, {}))
            except Exception:
                pass

        chapter = McChapterDetail(
            chapter_id=chapter_id,
            name=name,
            comic_id=comic_id,
            index=index,
            count=len(meta['image_urls']),
            access=meta['access'],
            from_comic=None,
        )
        chapter.url = f'/home/book/capter/id/{chapter_id}'
        chapter.scramble_n = meta.get('scramble_n', 0)
        chapter._reader_html = html

        if fetch_image_urls:
            self.fetch_image_urls(chapter)

        return chapter

    def parse_reader_page(self, html: str, chapter_id: str) -> Dict:
        root = parse_html(html)

        # 阅读页的章节名在 <title> 的方括号里：
        #   <title>[第01话] 无根树/无根之树 - 香香腐宅BoyLove│...</title>
        name = ''
        match = re.search(r'<title>\s*\[([^\]]+)\]', html)
        if match:
            name = match.group(1).strip()
        else:
            match = re.search(r'<title>\s*([^<\-–]+)', html)
            if match:
                name = match.group(1).strip()

        if not name:
            node = root.find('.read__title') or root.find('.chapter-title')
            if node is not None:
                name = node.text.strip()

        # 新章节把图片清单放在内联的 imageData 里（含真实顺序与尺寸），
        # 优先用它；老章节没有这个变量，退回解析 DOM。
        data_list, scramble_n = self._parse_image_data(html)

        if data_list:
            image_urls = [item['src'] for item in data_list]
            image_ids = [str(item.get('id', '')) for item in data_list]
        else:
            image_urls, image_ids = self._parse_reader_images(root, chapter_id)

        # 阅读页的章节权限标记（存在则用于补充 isvip）
        is_vip = 1 if re.search(r'class="[^"]*vip[^"]*"[^>]*>\s*VIP', html, re.I) else 0

        return {
            'name': name,
            'image_urls': image_urls,
            'image_ids': image_ids,
            'scramble_n': scramble_n,
            'access': {'vip': is_vip, 'cion': 0, 'price': 0, 'pnum': len(image_urls)},
        }

    @staticmethod
    def _parse_image_data(html: str) -> Tuple[List[Dict], int]:
        """
        解析阅读页内联的图片清单与乱序参数::

            var imageData = [{"id":0,"src":"...","width":649,"height":993}, ...];
            var randomClass = 11;   // 竖带数量

        返回 ``(图片列表, 竖带数量)``；老章节没有这两个变量时返回 ``([], 0)``。
        """
        data_list: List[Dict] = []
        match = re.search(r'imageData\s*=\s*(\[.*?\])\s*;', html, re.S)
        if match:
            try:
                import json
                raw = json.loads(match.group(1).replace('\\/', '/'))
                data_list = [item for item in raw if item.get('src')]
            except (ValueError, AttributeError):
                data_list = []

        scramble_n = 0
        n_match = re.search(r'randomClass\s*=\s*(\d+)', html)
        if n_match:
            scramble_n = int(n_match.group(1))

        # 只有确实要上 canvas 重组的章节才标记为乱序
        if 'canvas' not in html and scramble_n <= 1:
            scramble_n = 0

        return data_list, scramble_n

    def _parse_reader_images(self, root: Node, chapter_id: str) -> Tuple[List[str], List[str]]:
        """
        提取章节图片。

        阅读页里混有推荐位缩略图（``/bookimages/img/...``），
        正文图片的路径形如 ``/bookimages/{yyyymmdd}/{chapter_id}-{hash}.webp``，
        因此用「文件名以章节 id 开头」来精确筛选。
        """
        precise_urls: List[str] = []
        precise_ids: List[str] = []
        fallback_urls: List[str] = []

        for img in root.find_all('img'):
            url = (img.attr('data-original', '') or img.attr('src', '') or '').strip()
            if not url.startswith('http'):
                continue
            if 'lazyload_img' in url or '/static/' in url:
                continue

            node_id = img.attr('id', '') or ''
            if node_id.startswith('webp'):
                fallback_urls.append(url)

            file_name = url.split('?')[0].rsplit('/', 1)[-1]
            if file_name.startswith(f'{chapter_id}-'):
                precise_urls.append(url)
                precise_ids.append(node_id)

        if precise_urls:
            return precise_urls, precise_ids

        return fallback_urls, ['' for _ in fallback_urls]

    def fetch_image_urls(self, chapter: McChapterDetail) -> List[str]:
        html = getattr(chapter, '_reader_html', None)
        if html is None:
            html = self.req_text(self.PATH_READER.format(chapter=chapter.chapter_id))
            chapter._reader_html = html

        meta = self.parse_reader_page(html, chapter.chapter_id)
        url_list = meta['image_urls']

        if not url_list:
            ExceptionTool.raises(
                f'章节 [{chapter.chapter_id}] 未解析到任何图片；'
                f'该章节可能受权益保护（App 端接口返回 401 时本库不会尝试绕过）',
                {'site': self.site, 'chapter_id': chapter.chapter_id},
            )

        if meta['access'].get('vip'):
            chapter.access = {**chapter.access, 'vip': 1}

        chapter.scramble_n = meta.get('scramble_n', 0) or chapter.scramble_n
        chapter.set_image_url_list(url_list, meta['image_ids'])
        chapter.count = len(url_list)
        return url_list

    # ================================================================== 浏览

    def categories_filter(self,
                          page: int = 1,
                          cate: int = None,
                          tag: int = None,
                          done: int = None,
                          order: int = None,
                          is18: int = None,
                          vip: int = None,
                          **kwargs) -> McSearchPage:
        """
        分类浏览。

        :param done: 进度，0 连载 / 1 完结 / 2 全部
        :param vip: 权益，0 免费 / 1 VIP / 2 全部
        """
        conf = dict(self.CATE_DEFAULT)
        for key, value in (('cate', cate), ('tag', tag), ('done', done),
                           ('order', order), ('is18', is18), ('vip', vip)):
            if value is not None:
                conf[key] = value

        page = max(1, int(page))
        spec = (f"{conf['cate']}-{conf['tag']}-{conf['done']}-{conf['order']}-"
                f"{page}-{conf['is18']}-1-{conf['vip']}")

        result = self._api(self.PATH_CATE.format(spec=spec), params={'mt': 0})
        raw_list = result.get('list') or []

        content = [(str(raw.get('id')), self._build_info(raw)) for raw in raw_list]
        total = self._guess_total(len(content), page) if not result.get('lastPage') else \
            (page - 1) * 30 + len(content)

        return McSearchPage(content, total, page)

    def update_list(self, page: int = 1) -> McSearchPage:
        """最近更新（按更新时间排序的分类列表）。"""
        return self.categories_filter(page=page, order=1)

    def ranking(self, rank_type: int = 1, page: int = 1) -> McSearchPage:
        """
        排行榜。

        接口一次返回四组榜单（most_clicks / most_consumes / most_favorites / most_search），
        这里取 ``most_clicks``（人气）作为主榜单。
        """
        result = self._api(self.PATH_RANK.format(rank_type=rank_type))
        raw_list = result.get('most_clicks') or []

        content = [(str(raw.get('id')), self._build_info(raw)) for raw in raw_list]
        return McSearchPage(content, len(content), page)

    def ranking_nav(self) -> List[Dict]:
        """榜单分组导航。"""
        result = self._api(self.PATH_RANK.format(rank_type=1))
        return [{'type': key, 'name': _RANK_NAME.get(key, key)} for key in result.keys()]

    # ================================================================== url

    def get_comic_id_from_url(self, url: str) -> Optional[str]:
        match = re.search(r'/home/book/index/id/(\d+)', str(url))
        if match:
            return match.group(1)
        return super().get_comic_id_from_url(url)

    def get_chapter_id_from_url(self, url: str) -> Optional[str]:
        match = re.search(r'/home/book/capter/id/(\d+)', str(url))
        if match:
            return match.group(1)
        return super().get_chapter_id_from_url(url)

    def get_comic_url(self, comic_id) -> str:
        return MccmsText.format_url(f'/home/book/index/id/{comic_id}', self.domain, McModuleConfig.PROT)

    def get_chapter_url(self, chapter_id) -> str:
        return MccmsText.format_url(f'/home/book/capter/id/{chapter_id}', self.domain, McModuleConfig.PROT)


_RANK_NAME = {
    'most_clicks': '人气榜',
    'most_consumes': '消费榜',
    'most_favorites': '收藏榜',
    'most_search': '搜索榜',
}
