"""
TIBIU 站点客户端（cache.tibiu.net）。

接口清单（均为实测确认）：

+-----------------------------------------------+------------------------------------------+
| 用途                                          | 接口                                     |
+===============================================+==========================================+
| 搜索                                          | GET /api/data/search?key=&page=           |
| 漫画详情                                      | GET /api/data/comicinfo?cid=              |
| 章节列表                                      | GET /api/data/chapter?mid=                |
| 章节图片                                      | GET /api/data/pic?cid=   (按 id 升序!)    |
| 分类浏览                                      | GET /index.php/api/data/category_api      |
| 最近更新                                      | GET /index.php/api/data/update_api        |
| 筛选项                                        | GET /index.php/api/data/filter_options_api|
| 榜单导航 / 榜单                                | GET /index.php/api/rankdata/{nav,lists}   |
| 用户信息 / 登录                                | GET /index.php/api/user/{info,login}      |
| 年龄确认                                      | GET /index.php/user/info/confirmadult     |
+-----------------------------------------------+------------------------------------------+

注意：`/api/data/pic` 返回的图片是按 id **降序**排列的，
而网页端在渲染前做了 `.sort((a,b) => Number(a.id) - Number(b.id))`，
因此本实现同样按 id 升序排序，保证图片顺序与网页端一致。
"""

from typing import Dict, List, Optional, Tuple

from .mc_client_interface import AbstractMcClient
from .mc_config import McMagicConstants, McModuleConfig, mc_log
from .mc_entity import McChapterDetail, McComicDetail, McSearchPage
from .mc_exception import ExceptionTool
from .mc_postman import McPostman
from .mc_toolkit import MccmsText

__all__ = ['TibiuClient']


class TibiuClient(AbstractMcClient):
    client_key = McMagicConstants.SITE_TIBIU
    site = McMagicConstants.SITE_TIBIU

    # ------------------------------------------------------------------ 路径

    PATH_SEARCH = '/api/data/search'
    PATH_COMIC_INFO = '/api/data/comicinfo'
    PATH_CHAPTER_LIST = '/api/data/chapter'
    PATH_IMAGE_LIST = '/api/data/pic'
    PATH_CATEGORY = '/index.php/api/data/category_api'
    PATH_UPDATE = '/index.php/api/data/update_api'
    PATH_FILTER_OPTIONS = '/index.php/api/data/filter_options_api'
    PATH_RANK_NAV = '/index.php/api/rankdata/nav'
    PATH_RANK_LISTS = '/index.php/api/rankdata/lists'

    def __init__(self, postman: McPostman = None, domain_list=None, retry_times=None,
                 username=None, password=None, confirm_adult=False):
        super().__init__(postman, domain_list, retry_times)
        self._rank_nav_cache: Optional[List[Dict]] = None
        self.ensure_login(username, password)
        if confirm_adult:
            self.confirm_adult()

    # ================================================================== 搜索

    def search(self, keyword: str, page: int = 1) -> McSearchPage:
        ExceptionTool.require_true(str(keyword).strip() != '', '搜索关键字不能为空')

        data = self.req_json(self.PATH_SEARCH, params={'key': keyword, 'page': page})
        raw_list = data.get('data') or []

        content = []
        for raw in raw_list:
            comic_id, info = self.build_comic_info(raw, self.site)
            info['url'] = f'/comic/{comic_id}'
            content.append((comic_id, info))

        total = self._guess_total(len(content), page)
        return McSearchPage(content, total, page)

    def _guess_total(self, current_count: int, page: int) -> int:
        """搜索接口不返回总数，用满页与否估算下界。"""
        if current_count == 0:
            return 0
        if current_count < McModuleConfig.PAGE_SIZE_SEARCH:
            return (page - 1) * McModuleConfig.PAGE_SIZE_SEARCH + current_count
        return page * McModuleConfig.PAGE_SIZE_SEARCH + 1

    # ================================================================== 详情

    def get_comic_detail(self, comic_id, *, fetch_chapters: bool = True) -> McComicDetail:
        comic_id = MccmsText.parse_to_mc_id(comic_id)

        data = self.req_json(self.PATH_COMIC_INFO, params={'cid': comic_id})
        raw = data.get('data') or {}

        if not raw:
            ExceptionTool.raises(f'漫画不存在: [{comic_id}]', {'site': self.site, 'comic_id': comic_id})

        comic = self._build_comic(raw, comic_id)

        # 限制级内容需要先确认年龄
        if MccmsText.safe_int(raw.get('cadult', 0)) == 1 and McModuleConfig.FLAG_AUTO_CONFIRM_ADULT:
            self.confirm_adult()

        if fetch_chapters:
            episode_list, access_map = self._fetch_chapter_list(comic_id)
            comic.episode_list = comic.distinct_episode(episode_list)
            comic.chapter_access = access_map

        return comic

    def _build_comic(self, raw: Dict, comic_id: str) -> McComicDetail:
        author_text = MccmsText.unescape(raw.get('author', ''))
        authors = [a.strip() for a in author_text.replace('，', '/').split('/') if a.strip()]

        return McComicDetail(
            comic_id=comic_id,
            name=MccmsText.unescape(raw.get('name', '')),
            author=authors[0] if authors else '',
            authors=authors,
            tags=[],
            description=MccmsText.unescape(raw.get('content', '') or raw.get('text', '') or ''),
            cover=raw.get('pic', '') or '',
            serialize=str(raw.get('serialize', '') or ''),
            update_date=str(raw.get('addtime', '') or ''),
            views=MccmsText.parse_human_number(raw.get('hits', 0)),
            score=float(raw.get('score', 0) or 0),
            site=self.site,
            url=f'/comic/{comic_id}',
            slug=str(raw.get('yname', '') or ''),
        )

    def _fetch_chapter_list(self, comic_id: str) -> Tuple[List[Tuple], Dict[str, Dict]]:
        """
        返回 (episode_list, chapter_access)。

        接口返回的章节是倒序的（最新在前），这里按 xid 升序整理。
        """
        data = self.req_json(self.PATH_CHAPTER_LIST, params={'mid': comic_id})
        raw_list = data.get('data') or []

        if not raw_list:
            return [], {}

        # 按 xid 升序（xid 是漫画内的章节序号）
        raw_list = sorted(raw_list, key=lambda item: MccmsText.safe_int(item.get('xid', 0), 0))

        episode_list: List[Tuple] = []
        access_map: Dict[str, Dict] = {}

        for index, raw in enumerate(raw_list, start=1):
            chapter_id = str(raw.get('id'))
            episode_list.append((chapter_id, index, MccmsText.unescape(raw.get('name', ''))))
            access_map[chapter_id] = {
                'vip': MccmsText.safe_int(raw.get('vip', 0)),
                'cion': MccmsText.safe_int(raw.get('cion', 0)),
                'price': MccmsText.safe_int(raw.get('pay', 0)),
                'pnum': MccmsText.safe_int(raw.get('pnum', 0)),
                'addtime': str(raw.get('addtime', '') or ''),
                'xid': MccmsText.safe_int(raw.get('xid', 0)),
            }

        return episode_list, access_map

    # ================================================================== 章节

    def get_chapter_detail(self,
                           chapter_id,
                           *,
                           comic_id=None,
                           fetch_image_urls: bool = True) -> McChapterDetail:
        chapter_id = MccmsText.parse_to_mc_id(chapter_id)
        comic_id = MccmsText.parse_to_mc_id(comic_id) if comic_id else None

        name = ''
        index = 1
        access: Dict = {}
        from_comic = None

        if comic_id is not None:
            # 从章节列表里取到章节的元信息
            episode_list, access_map = self._fetch_chapter_list(comic_id)
            access = access_map.get(chapter_id, {})

            for cid, cindex, cname in episode_list:
                if str(cid) == chapter_id:
                    name, index = cname, cindex
                    break

            if name == '':
                mc_log('chapter',
                       f'章节 [{chapter_id}] 不在漫画 [{comic_id}] 的章节列表中，'
                       f'将仅按章节 id 下载')

        chapter = McChapterDetail(
            chapter_id=chapter_id,
            name=name or f'第{chapter_id}话',
            comic_id=comic_id or '',
            index=index,
            count=MccmsText.safe_int(access.get('pnum', 0)),
            access=access,
            update_date=str(access.get('addtime', '') or ''),
            from_comic=from_comic,
        )
        chapter.url = (f'/chapter/{comic_id}/{chapter_id}' if comic_id else f'/chapter/-/{chapter_id}')

        if fetch_image_urls:
            self.fetch_image_urls(chapter)

        return chapter

    def fetch_image_urls(self, chapter: McChapterDetail) -> List[str]:
        """
        获取章节图片地址。

        站点返回的 data 是按 id 降序的，这里按 id 升序排列以匹配网页端的阅读顺序。
        """
        data = self.req_json(
            self.PATH_IMAGE_LIST,
            params={'cid': chapter.chapter_id},
            context={'chapter_id': chapter.chapter_id, 'comic_id': chapter.comic_id},
        )

        raw_list = data.get('data') or []
        if not raw_list:
            ExceptionTool.raises(
                f'章节 [{chapter.chapter_id}] 没有返回任何图片',
                {'site': self.site, 'chapter_id': chapter.chapter_id},
            )

        raw_list = sorted(raw_list, key=lambda item: MccmsText.safe_int(item.get('id', 0), 0))

        url_list = [str(item.get('img', '')).strip() for item in raw_list if item.get('img')]
        id_list = [str(item.get('id', '')) for item in raw_list if item.get('img')]

        chapter.set_image_url_list(url_list, id_list)
        chapter.count = len(url_list)
        return url_list

    # ================================================================== 浏览

    def categories_filter(self,
                          page: int = 1,
                          order: Optional[str] = None,
                          tags=None,
                          tids=None,
                          **kwargs) -> McSearchPage:
        """
        分类浏览。

        :param order: 排序，见 McMagicConstants.ORDER_*
        :param tags: 标签 id（单个或列表）
        :param tids: 分类 id（单个或列表）
        """
        params = {'page': page}
        if order:
            params['order'] = order
        if tags is not None:
            params['tags'] = tags if isinstance(tags, str) else ','.join(str(t) for t in tags)
        if tids is not None:
            params['tids'] = tids if isinstance(tids, str) else ','.join(str(t) for t in tids)

        return self._parse_list_page(self.PATH_CATEGORY, params, page)

    def update_list(self, page: int = 1) -> McSearchPage:
        """最近更新。"""
        return self._parse_list_page(self.PATH_UPDATE, {'page': page}, page)

    def filter_options(self, mode: str = 'update') -> Dict:
        """筛选项（分类/标签的 id -> 名称）。"""
        data = self.req_json(self.PATH_FILTER_OPTIONS, params={'mode': mode})
        return data.get('data') or {}

    def _parse_list_page(self, path: str, params: Dict, page: int) -> McSearchPage:
        data = self.req_json(path, params=params)
        raw_list = data.get('data') or []

        content = []
        for raw in raw_list:
            comic_id, info = self.build_comic_info(raw, self.site)
            info['url'] = f'/comic/{comic_id}'
            info['slug'] = str(raw.get('yname', '') or '')
            content.append((comic_id, info))

        total = MccmsText.safe_int(data.get('total_items', 0))
        if total == 0:
            total = self._guess_total(len(content), page)

        return McSearchPage(content, total, page)

    # ================================================================== 榜单

    def ranking_nav(self) -> List[Dict]:
        """榜单分类导航（人气榜 / 月票榜 / 收藏榜 / 日榜 / 周榜 / 月榜 / 飙升榜）。"""
        if self._rank_nav_cache is None:
            data = self.req_json(self.PATH_RANK_NAV)
            self._rank_nav_cache = data.get('data') or []
        return self._rank_nav_cache

    def ranking(self,
                rank_type: str = 'top',
                page: int = 1,
                max_items: int = 500) -> McSearchPage:
        """
        榜单列表。

        :param rank_type: 见 ranking_nav() 的 type 字段，常用 top / ticket / fav / day / week / month
        """
        params = {'type': rank_type, 'page': page, 'max_items': max_items}
        data = self.req_json(self.PATH_RANK_LISTS, params=params)
        raw_list = data.get('data') or []

        content = []
        for raw in raw_list:
            comic_id, info = self.build_comic_info(raw, self.site)
            info['url'] = f'/comic/{comic_id}'
            content.append((comic_id, info))

        total = MccmsText.safe_int(data.get('total_items', 0))
        return McSearchPage(content, total, page)

    # ================================================================== url

    def get_comic_id_from_url(self, url: str) -> Optional[str]:
        comic_id = MccmsText.parse_comic_id_from_url(url)
        if comic_id:
            return comic_id
        return super().get_comic_id_from_url(url)

    def get_chapter_id_from_url(self, url: str) -> Optional[str]:
        return MccmsText.parse_chapter_id_from_url(url)

    def parse_chapter_url(self, url: str) -> Tuple[Optional[str], Optional[str]]:
        """
        解析 TIBIU 的章节 URL，返回 (comic_id, chapter_id)。

        TIBIU 的章节路径形如 /chapter/{comic_id}/{chapter_id}，
        同时带有漫画和章节两个 id，比单独传 chapter_id 能得到更完整的元信息。
        """
        import re

        match = re.search(r'/chapter/(\d+)/(\d+)', str(url))
        if match:
            return match.group(1), match.group(2)

        return self.get_comic_id_from_url(url), self.get_chapter_id_from_url(url)

    def get_comic_url(self, comic_id) -> str:
        return MccmsText.format_url(f'/comic/{comic_id}', self.domain, McModuleConfig.PROT)

    def get_chapter_url(self, comic_id, chapter_id) -> str:
        return MccmsText.format_url(f'/chapter/{comic_id}/{chapter_id}', self.domain, McModuleConfig.PROT)
