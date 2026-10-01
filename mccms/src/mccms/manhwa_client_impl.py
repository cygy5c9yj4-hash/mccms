"""
漫蛙站点客户端（www.manhwa.wang）。

该站是原生 Mccms PC 模板，接口与 TIBIU 不同，是 JSON + HTML 混合的：

+--------------------------------------+---------------------------------------------------+
| 用途                                 | 方式                                              |
+======================================+===================================================+
| 章节列表                             | GET /index.php/api/comic/chapter?mid=             |
| 热门漫画                             | GET /index.php/api/comic/hot                      |
| 章节图片（有权限时）                  | GET /index.php/api/comic/isbuy?id=                |
| 章节图片（免费章节，未登录）           | HTML /index.php/chapter/{id} 里的                  |
|                                      | .rd-article__pic img[data-original]               |
| 搜索                                 | HTML /index.php/search/{key}/{page}               |
| 分类                                 | HTML /index.php/category/order/{order}[/list/{p}]  |
| 漫画详情                             | HTML /index.php/comic/{id或slug}                   |
+--------------------------------------+---------------------------------------------------+

权限语义（实测）：
- `isbuy` 未登录 -> `{"msg":"登录超时","code":2}`
- `isbuy` 已登录但无会员/金币 -> `code:3`，并带 `type`（vip / cion）
- `isbuy` 有权限 -> `code:1`，`pic` 为图片数组

本实现只在"当前会话确实有权限"时下发图片，无权限时抛出明确的
LoginRequiredException / VipRequiredException，不做任何越权尝试。
"""

import re
from typing import Dict, List, Optional, Tuple
from urllib.parse import quote

from .mc_client_interface import AbstractMcClient
from .mc_config import McMagicConstants, McModuleConfig, mc_log
from .mc_entity import McChapterDetail, McComicDetail, McSearchPage
from .mc_exception import ExceptionTool
from .mc_html import Node, parse_html
from .mc_postman import McPostman
from .mc_toolkit import MccmsText

__all__ = ['ManhwaClient']


class ManhwaClient(AbstractMcClient):
    client_key = McMagicConstants.SITE_MANHWA
    site = McMagicConstants.SITE_MANHWA

    # ------------------------------------------------------------------ 路径

    PATH_CHAPTER_LIST = '/index.php/api/comic/chapter'
    PATH_ISBUY = '/index.php/api/comic/isbuy'
    PATH_HOT = '/index.php/api/comic/hot'
    PATH_SEARCH = '/index.php/search/{key}/{page}'
    PATH_CATEGORY = '/index.php/category/order/{order}'
    PATH_COMIC = '/index.php/comic/{comic}'
    PATH_READER = '/index.php/chapter/{chapter}'

    def __init__(self, postman: McPostman = None, domain_list=None, retry_times=None,
                 username=None, password=None, confirm_adult=False):
        super().__init__(postman, domain_list, retry_times)
        self.ensure_login(username, password)

    # ================================================================== 搜索

    def search(self, keyword: str, page: int = 1) -> McSearchPage:
        keyword = str(keyword).strip()
        ExceptionTool.require_true(keyword != '', '搜索关键字不能为空')

        path = self.PATH_SEARCH.format(key=quote(keyword), page=max(1, int(page)))
        html = self.req_text(path)
        root = parse_html(html)

        content = self._parse_comic_cards(root)
        total = self._parse_search_total(root)
        if total == 0:
            total = page * 30 if len(content) >= 30 else (page - 1) * 30 + len(content)

        return McSearchPage(content, total, page)

    @staticmethod
    def _parse_search_total(root: Node) -> int:
        head = root.find('.search_head')
        if head is None:
            return 0
        match = re.search(r'（(\d+)）', head.text)
        return MccmsText.safe_int(match.group(1)) if match else 0

    def _parse_comic_cards(self, root: Node) -> List[Tuple[str, Dict]]:
        """解析 .common-comic-item 卡片列表。"""
        content: List[Tuple[str, Dict]] = []

        for item in root.find_all('.common-comic-item'):
            link = item.find('.comic__title a') or item.find('a.cover')
            if link is None:
                continue

            href = link.attr('href', '') or ''
            slug = MccmsText.parse_slug_from_url(href) or ''
            comic_id = slug or MccmsText.parse_to_mc_id(href)

            img = item.find('img')
            cover = ''
            name = ''
            if img is not None:
                cover = img.attr('data-original', '') or img.attr('src', '') or ''
                name = img.attr('alt', '') or ''
            if not name:
                name = link.text

            update_link = item.find('.comic-update .hl')
            views_node = item.find('.comic-count')
            feature = item.find('.comic-feature')

            info = {
                'id': comic_id,
                'name': name.strip(),
                'author': '',
                'cover': cover,
                'text': feature.text if feature is not None else '',
                'serialize': '',
                'update_date': '',
                'views': MccmsText.parse_human_number(
                    views_node.text.replace('人气：', '').replace('人气:', '') if views_node is not None else 0),
                'score': 0.0,
                'tags': [],
                'site': self.site,
                'slug': slug,
                'url': href,
                'latest_chapter': update_link.text if update_link is not None else '',
                'latest_chapter_url': update_link.attr('href', '') if update_link is not None else '',
            }
            content.append((comic_id, info))

        return content

    # ================================================================== 详情

    def get_comic_detail(self, comic_id, *, fetch_chapters: bool = True) -> McComicDetail:
        comic_id = str(comic_id).strip()
        html = self.req_text(self.PATH_COMIC.format(comic=comic_id))
        root = parse_html(html)

        comic = self._parse_comic_detail(root, comic_id)

        if fetch_chapters:
            episode_list, access_map = self._fetch_chapter_list(comic.comic_id)
            comic.episode_list = comic.distinct_episode(episode_list)
            comic.chapter_access = access_map

        return comic

    def _parse_comic_detail(self, root: Node, given_id: str) -> McComicDetail:
        title_node = root.find('.de-info__box .comic-title')
        name = title_node.text if title_node is not None else ''

        cover_node = root.find('.de-info__cover img')
        cover = ''
        if cover_node is not None:
            cover = cover_node.attr('src', '') or cover_node.attr('data-original', '') or ''

        author_node = root.find('.comic-author .name a')
        author_text = author_node.text if author_node is not None else ''
        authors = [a.strip() for a in author_text.replace('，', '/').split('/') if a.strip()]

        intro_node = root.find('.comic-intro .intro-total') or root.find('.comic-intro .intro')
        description = intro_node.text if intro_node is not None else ''

        # 数字 id 藏在收藏按钮的 data-id 上
        numeric_id = ''
        collect = root.find('.j-user-collect')
        if collect is not None:
            numeric_id = collect.attr('data-id', '') or ''
        if not numeric_id:
            numeric_id = MccmsText.parse_to_mc_id(given_id) if given_id.isdigit() else given_id

        # 连载状态：.de-chapter__title 的第一个 span
        serialize = ''
        chapter_title = root.find('.de-chapter__title')
        if chapter_title is not None:
            for span in chapter_title.children_by_tag('span'):
                text = span.text.strip()
                if text and not span.has_class('update-time'):
                    serialize = text
                    break

        # 统计：收藏 / 人气
        views = 0
        for status in root.find_all('.comic-status .text'):
            text = status.text
            if '人气' in text:
                views = MccmsText.parse_human_number(text.split('：')[-1].split(':')[-1])

        # 标签
        tags = []
        for link in root.find_all('a'):
            href = link.attr('href', '') or ''
            if '/category/tags/' in href:
                text = link.text.strip()
                if text and text not in tags:
                    tags.append(text)

        slug = '' if given_id.isdigit() else given_id
        comic_id = numeric_id or given_id

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
            site=self.site,
            url=f'/index.php/comic/{given_id}',
            slug=slug,
        )

    def _fetch_chapter_list(self, comic_id: str) -> Tuple[List[Tuple], Dict[str, Dict]]:
        data = self.req_json(self.PATH_CHAPTER_LIST, params={'mid': comic_id})
        raw_list = data.get('data') or []

        if not raw_list:
            return [], {}

        episode_list: List[Tuple] = []
        access_map: Dict[str, Dict] = {}

        for index, raw in enumerate(raw_list, start=1):
            chapter_id = str(raw.get('id'))
            episode_list.append((chapter_id, index, MccmsText.unescape(raw.get('name', ''))))
            access_map[chapter_id] = {
                'vip': MccmsText.safe_int(raw.get('vip', 0)),
                'cion': MccmsText.safe_int(raw.get('cion', 0)),
                'price': MccmsText.safe_int(raw.get('price', 0)),
                'pnum': MccmsText.safe_int(raw.get('pnum', 0)),
                'addtime': '',
                'link': str(raw.get('link', '') or ''),
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

        if comic_id is not None and str(comic_id) != meta['comic_id']:
            comic_id = MccmsText.parse_to_mc_id(comic_id)
        else:
            comic_id = meta['comic_id']

        index = 1
        if comic_id:
            # 章节序号需要从所属漫画的章节列表里定位
            try:
                episode_list, access_map = self._fetch_chapter_list(comic_id)
                access_map_entry = access_map.get(chapter_id, {})
                for cid, cindex, _ in episode_list:
                    if str(cid) == chapter_id:
                        index = cindex
                        break
                meta['access'].update({
                    'vip': access_map_entry.get('vip', meta['access'].get('vip', 0)),
                    'cion': access_map_entry.get('cion', meta['access'].get('cion', 0)),
                    'price': access_map_entry.get('price', 0),
                    'pnum': access_map_entry.get('pnum', meta['access'].get('pnum', 0)),
                })
            except Exception as e:
                mc_log('chapter', f'获取章节序号失败(comic={comic_id}): {e}')

        chapter = McChapterDetail(
            chapter_id=chapter_id,
            name=meta['name'] or f'第{chapter_id}话',
            comic_id=comic_id or '',
            index=index,
            count=MccmsText.safe_int(meta['access'].get('pnum', 0)) or meta['count'],
            access=meta['access'],
            from_comic=None,
        )
        chapter.url = f'/index.php/chapter/{chapter_id}'
        # 缓存阅读页 HTML，避免取图时重复请求
        chapter._reader_html = html
        chapter._comic_slug = meta.get('slug', '')
        chapter._comic_name = meta.get('comic_name', '')

        if fetch_image_urls:
            self.fetch_image_urls(chapter)

        return chapter

    def parse_reader_page(self, html: str, chapter_id: str) -> Dict:
        """解析阅读页的元信息与内联图片。"""
        root = parse_html(html)

        comic_link = root.find('.read__crumb a.crumb__title')
        comic_name = comic_link.text if comic_link is not None else ''
        slug = MccmsText.parse_slug_from_url(comic_link.attr('href', '')) if comic_link is not None else ''

        title_node = root.find('.read__crumb h1.comic-title a') or root.find('h1.comic-title a')
        chapter_name = title_node.text.strip() if title_node is not None else ''

        count = 0
        page_index = root.find('.page-index__btn .count')
        if page_index is not None:
            count = MccmsText.safe_int(page_index.text)

        # readPic(mid, cid, vip, cion)
        match = re.search(r'readPic\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)', html)
        comic_id, vip, cion = '', 0, 0
        if match:
            comic_id = match.group(1)
            vip = MccmsText.safe_int(match.group(3))
            cion = MccmsText.safe_int(match.group(4))

        image_urls = self.parse_inline_images(root)
        image_ids = self.parse_inline_image_ids(root)

        return {
            'comic_id': comic_id,
            'comic_name': comic_name,
            'slug': slug or '',
            'name': chapter_name,
            'count': count,
            'access': {'vip': vip, 'cion': cion, 'price': 0, 'pnum': count},
            'image_urls': image_urls,
            'image_ids': image_ids,
        }

    @staticmethod
    def parse_inline_images(root: Node) -> List[str]:
        """免费章节的图片直出在 HTML 里。"""
        result = []
        for pic in root.find_all('.rd-article__pic'):
            img = pic.find('img')
            if img is None:
                continue
            url = img.attr('data-original', '') or img.attr('src', '') or ''
            url = url.strip()
            if url and 'lazyload_img.png' not in url:
                result.append(url)
        return result

    @staticmethod
    def parse_inline_image_ids(root: Node) -> List[str]:
        result = []
        for pic in root.find_all('.rd-article__pic'):
            pid = pic.attr('data-pid', '') or ''
            result.append(pid)
        return result

    def fetch_image_urls(self, chapter: McChapterDetail) -> List[str]:
        """
        获取章节图片地址。

        顺序：
        1. 阅读页 HTML 里的内联图片（免费章节）
        2. /index.php/api/comic/isbuy?id=  （登录且有权限时返回图片数组）

        两者都拿不到时，按服务端返回的 code/type 抛出
        LoginRequiredException 或 VipRequiredException。
        """
        html = getattr(chapter, '_reader_html', None)
        if html is None:
            html = self.req_text(self.PATH_READER.format(chapter=chapter.chapter_id))
            chapter._reader_html = html

        root = parse_html(html)
        url_list = self.parse_inline_images(root)
        id_list = self.parse_inline_image_ids(root)

        if url_list:
            chapter.set_image_url_list(url_list, id_list)
            chapter.count = len(url_list)
            return url_list

        # 内联图片为空 => 该章节受权限控制，向服务端申请
        url_list, id_list = self._fetch_images_via_api(chapter)
        chapter.set_image_url_list(url_list, id_list)
        chapter.count = len(url_list)
        return url_list

    def _fetch_images_via_api(self, chapter: McChapterDetail) -> Tuple[List[str], List[str]]:
        """
        调用 isbuy 接口取图。

        该接口的返回码语义由服务端决定，本方法只做映射，不尝试绕过：
        - code 1 -> 有权限，返回 pic 数组
        - code 2 -> 未登录
        - code 3 -> 已登录但缺少 vip/cion 权益
        """
        resp = self.postman.get(self.PATH_ISBUY, params={'id': chapter.chapter_id})
        data = self.parse_resp(resp, context={
            'chapter_id': chapter.chapter_id,
            'comic_id': chapter.comic_id,
        })

        pic_list = data.get('pic') or []
        url_list = [str(item.get('img', '')).strip() for item in pic_list if item.get('img')]
        id_list = [str(item.get('id', '')) for item in pic_list if item.get('img')]

        if not url_list:
            ExceptionTool.raises(
                f'章节 [{chapter.chapter_id}] 未返回任何图片（该章节可能需要会员权益）',
                {'site': self.site, 'chapter_id': chapter.chapter_id},
            )

        return url_list, id_list

    # ================================================================== 浏览

    def categories_filter(self,
                          page: int = 1,
                          order: str = McMagicConstants.ORDER_HITS,
                          finish=None,
                          pay=None,
                          tags=None,
                          quality=None,
                          copyright=None,
                          **kwargs) -> McSearchPage:
        """
        分类浏览。

        :param order: hits（人气）/ addtime（更新）
        :param finish: 1 已完结 / 2 连载中
        :param pay: 1 免费 / 2 付费
        :param tags: 标签 id
        :param quality: 画质 id
        :param copyright: 版权 id
        """
        path = self.PATH_CATEGORY.format(order=order or McMagicConstants.ORDER_HITS)
        if finish is not None:
            path += f'/finish/{finish}'
        if pay is not None:
            path += f'/pay/{pay}'
        if tags is not None:
            path += f'/tags/{tags}'
        if quality is not None:
            path += f'/quality/{quality}'
        if copyright is not None:
            path += f'/copyright/{copyright}'

        path += f'/list/{max(1, int(page))}'

        html = self.req_text(path)
        root = parse_html(html)
        content = self._parse_comic_cards(root)

        total = 0
        page_node = root.find('.pagination') or root.find('[class*=page]')
        if page_node is not None:
            numbers = [MccmsText.safe_int(n) for n in re.findall(r'\d+', page_node.text)]
            if numbers:
                total = max(numbers) * 30

        return McSearchPage(content, total, page)

    def update_list(self, page: int = 1) -> McSearchPage:
        """最近更新（按 addtime 排序）。"""
        return self.categories_filter(page=page, order=McMagicConstants.ORDER_ADDTIME)

    def hot_list(self) -> McSearchPage:
        """热门漫画（首页推荐位的接口）。"""
        data = self.req_json(self.PATH_HOT)
        raw_list = data.get('data') or []

        content = []
        for raw in raw_list:
            comic_id = str(raw.get('id'))
            url = str(raw.get('url', '') or '')
            slug = MccmsText.parse_slug_from_url(url) or ''
            info = {
                'id': comic_id,
                'name': MccmsText.unescape(raw.get('name', '')),
                'author': MccmsText.unescape(raw.get('author', '')),
                'cover': raw.get('pic', '') or '',
                'text': MccmsText.unescape(raw.get('text', '')),
                'serialize': '',
                'update_date': '',
                'views': 0,
                'score': 0.0,
                'tags': [],
                'site': self.site,
                'slug': slug,
                'url': url,
            }
            content.append((comic_id, info))

        return McSearchPage(content, len(content), 1)

    # ================================================================== url

    def get_comic_id_from_url(self, url: str) -> Optional[str]:
        numeric = MccmsText.parse_comic_id_from_url(url)
        if numeric:
            return numeric
        return MccmsText.parse_slug_from_url(url)

    def get_chapter_id_from_url(self, url: str) -> Optional[str]:
        return MccmsText.parse_chapter_id_from_url(url)

    def get_comic_url(self, comic_id) -> str:
        return MccmsText.format_url(f'/index.php/comic/{comic_id}', self.domain, McModuleConfig.PROT)

    def get_chapter_url(self, chapter_id) -> str:
        return MccmsText.format_url(f'/index.php/chapter/{chapter_id}', self.domain, McModuleConfig.PROT)
