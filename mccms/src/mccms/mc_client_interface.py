"""
客户端接口层（对应 jmcomic 的 jm_client_interface.py）。

- McClientInterface：所有站点客户端的抽象契约
- AbstractMcClient：封装 Mccms 站点共有的 HTTP / 登录 / 下载 / 分页逻辑

站点实现见 tibiu_client_impl.py / manhwa_client_impl.py。
"""

import os
from typing import Dict, Generator, List, Optional, Tuple

from .mc_config import mc_log
from .mc_entity import McChapterDetail, McComicDetail, McPageContent, McSearchPage
from .mc_exception import (ExceptionTool, McException,
                           ResponseUnexpectedException,
                           raise_access_exception)
from .mc_postman import McPostman, McResp
from .mc_toolkit import MccmsText, mkdir_if_not_exists

__all__ = ['McClientInterface', 'AbstractMcClient']


class McClientInterface:
    """
    站点客户端契约。子类必须设置 client_key / site，并实现下面的抽象方法。
    """

    client_key: str = None
    site: str = None

    def __init__(self,
                 postman: McPostman = None,
                 domain_list: Optional[List[str]] = None,
                 retry_times: Optional[int] = None,
                 ):
        self.postman = postman or McPostman(self.site, domain_list=domain_list, retry_times=retry_times)
        self.retry_times = retry_times

    # ------------------------------------------------------------------ 元数据

    def get_comic_detail(self, comic_id, *, fetch_chapters=True) -> McComicDetail:
        """获取漫画详情（含章节列表）。"""
        raise NotImplementedError

    def get_chapter_detail(self, chapter_id, *, comic_id=None, fetch_image_urls=True) -> McChapterDetail:
        """获取章节详情（含图片地址列表）。"""
        raise NotImplementedError

    def search(self, keyword: str, page: int = 1) -> McSearchPage:
        """按关键字搜索。"""
        raise NotImplementedError

    def categories_filter(self, page: int = 1, **kwargs) -> McSearchPage:
        """分类/筛选浏览。"""
        raise NotImplementedError

    def update_list(self, page: int = 1) -> McSearchPage:
        """最近更新列表。"""
        raise NotImplementedError

    def fetch_image_urls(self, chapter: McChapterDetail) -> List[str]:
        """
        获取并返回章节的图片地址列表（会写入 chapter）。

        无权限时抛出 AccessDeniedException 的子类。
        """
        raise NotImplementedError

    # ------------------------------------------------------------------ 通用实现入口

    def get_comic_id_from_url(self, url: str) -> Optional[str]:
        return MccmsText.parse_comic_id_from_url(url)

    def get_chapter_id_from_url(self, url: str) -> Optional[str]:
        return MccmsText.parse_chapter_id_from_url(url)

    def __str__(self):
        return f'{self.__class__.__name__}(site={self.site})'

    __repr__ = __str__


class AbstractMcClient(McClientInterface):
    """
    Mccms 站点共有逻辑：
    - JSON 信封解析（code=1 成功；code=2 需登录；code=3 需会员）
    - 用户信息 / 登录 / 退出 / 年龄确认
    - 图片下载
    - 分页生成器
    """

    # 各站点覆写
    PATH_USER_INFO = '/index.php/api/user/info'
    PATH_LOGIN = '/index.php/api/user/login'
    PATH_LOGOUT = '/index.php/api/user/logout'
    PATH_CONFIRM_ADULT = '/index.php/user/info/confirmadult'

    # ------------------------------------------------------------------ 基础请求

    @property
    def domain(self) -> str:
        return self.postman.current_domain

    def req_json(self, path: str, context: Optional[Dict] = None, **kwargs) -> Dict:
        resp = self.postman.get(path, **kwargs)
        return self.parse_resp(resp, context=context)

    def parse_resp(self, resp: McResp, context: Optional[Dict] = None) -> Dict:
        """校验 HTTP 状态并解析 Mccms JSON 信封。"""
        if resp.is_not_success:
            ExceptionTool.raises(
                f'请求失败: [{resp.url}]，http_code: [{resp.http_code}]',
                {'resp': resp, **(context or {})},
                ResponseUnexpectedException,
            )

        data = resp.json()

        # Mccms 有两套信封：
        # 1) 详情类接口：{"code": 1, "msg": ..., "data": ...}
        # 2) 列表类接口（category_api / update_api）：{"code": -1, "status": "success",
        #    "message": "Results found.", "data": [...]}
        #    —— 这类接口的 code 恒为 -1，必须以 status 为准，否则会把成功当失败。
        if str(data.get('status', '')).lower() == 'success':
            return data

        code = MccmsText.safe_int(data.get('code', 1), 1)

        if code == 1:
            return data

        msg = data.get('msg') or data.get('message') or f'接口返回异常: code={code}'
        ctx = {
            'site': self.site,
            'url': resp.url,
            'code': code,
            'access_type': data.get('type'),
            **(context or {}),
        }

        if code in (2, 3):
            raise_access_exception(msg, ctx, access_code=code, access_type=data.get('type'))

        ExceptionTool.raises(msg, ctx, ResponseUnexpectedException)

    def req_text(self, path: str, **kwargs) -> str:
        resp = self.postman.get(path, **kwargs)
        resp.require_success()
        return resp.text

    # ------------------------------------------------------------------ 用户

    def user_info(self) -> Dict:
        """
        返回当前会话的用户信息。

        Mccms 字段: log(是否登录) / id / nichen(昵称) / vip / cion(金币) / ticket / adult
        """
        data = self.req_json(self.PATH_USER_INFO)
        return data.get('data') or {}

    def is_logged_in(self) -> bool:
        try:
            return MccmsText.safe_int(self.user_info().get('log', 0)) == 1
        except McException:
            return False

    def login(self, username: str, password: str, is_log: int = 1, pcode: str = '') -> Dict:
        """
        使用账号密码登录，成功后会话 cookie 保存在 postman 中。

        :param is_log: 是否记住登录（1 记住 / 0 不记住）
        :param pcode: 图形验证码（站点要求时提供）
        """
        params = {
            'name': username,
            'pass': password,
            'islog': is_log,
            'pcode': pcode,
        }
        data = self.req_json(self.PATH_LOGIN, params=params)
        info = data.get('data') or {}
        self._login_info = info
        mc_log('login', f'登录成功: {info.get("nichen") or username}')
        return info

    def logout(self) -> bool:
        try:
            self.req_json(self.PATH_LOGOUT)
            return True
        except McException:
            return False

    def confirm_adult(self) -> bool:
        """
        确认年龄门（TIBIU 的限制级内容需要）。
        """
        try:
            resp = self.postman.get(self.PATH_CONFIRM_ADULT)
            data = resp.json()
            return MccmsText.safe_int(data.get('code', -1), -1) == 1
        except Exception as e:
            mc_log('adult', f'年龄确认失败: {e}', e)
            return False

    def ensure_login(self, username: Optional[str], password: Optional[str]):
        """option 中配置了账号则自动登录。"""
        if not username or not password:
            return
        if self.is_logged_in():
            return
        self.login(username, password)

    # ------------------------------------------------------------------ 权限判定

    def check_chapter(self, chapter: McChapterDetail) -> bool:
        """
        校验章节是否可访问。

        返回 True 表示可访问；不可访问时抛出 AccessDeniedException 的子类。
        默认实现：直接尝试取图片列表，由服务端裁决。
        """
        if len(chapter) == 0:
            self.fetch_image_urls(chapter)

        return True

    def describe_access(self, chapter: McChapterDetail) -> str:
        base = chapter.access_desc
        if chapter.is_free:
            return base
        try:
            logged = self.is_logged_in()
        except McException:
            logged = False

        if not logged:
            return f'{base}（未登录）'
        return base

    # ------------------------------------------------------------------ 下载

    def download_image(self, url: str, save_path: str) -> str:
        """下载图片到 save_path。"""
        resp = self.postman.download(url)
        mkdir_if_not_exists(os.path.dirname(os.path.abspath(save_path)))
        with open(save_path, 'wb') as f:
            f.write(resp.content)
        return save_path

    def download_by_image_detail(self, image, save_path: str, decode_image: bool = False) -> str:
        """
        下载图片实体，并按需还原站点的图片乱序。

        - ``decode_image=False``：原样落盘（字节级拷贝）
        - ``decode_image=True`` 且该图带 ``scramble_n``（站点下发的竖带数量）：
          下载后做竖带倒序还原，见 :mod:`mccms.mc_decode`

        是否需要还原由图片自身决定（``image.is_scrambled``），
        所以对「部分章节乱序、部分章节不乱序」的站点也能逐图正确处理。
        """
        self.download_image(image.download_url, save_path)

        if decode_image and getattr(image, 'is_scrambled', False):
            from .mc_decode import decode_scrambled_image_file
            decode_scrambled_image_file(save_path, image.scramble_n)

        return save_path

    def download_cover(self, comic_id, save_path: str, size: str = '') -> Optional[str]:
        """下载漫画封面。"""
        comic = self.get_comic_detail(comic_id, fetch_chapters=False)
        if not comic.cover:
            return None
        return self.download_image(comic.cover, save_path)

    # ------------------------------------------------------------------ 分页生成器

    def do_page_iter(self, page: int, get_page_method, *args, **kwargs) -> McPageContent:
        return get_page_method(*args, page=page, **kwargs)

    def search_gen(self,
                   keyword: str,
                   start_page: int = 1,
                   end_page: Optional[int] = None,
                   ) -> Generator[McSearchPage, None, None]:
        """搜索分页生成器。"""
        page = start_page
        while True:
            result = self.search(keyword, page)
            yield result

            if end_page is not None and page >= end_page:
                break
            if len(result) == 0 or len(result) < result.page_size:
                break

            page += 1

    def categories_filter_gen(self,
                              start_page: int = 1,
                              end_page: Optional[int] = None,
                              **kwargs) -> Generator[McPageContent, None, None]:
        page = start_page
        while True:
            result = self.categories_filter(page=page, **kwargs)
            yield result

            if end_page is not None and page >= end_page:
                break
            if len(result) == 0 or len(result) < result.page_size:
                break

            page += 1

    # jmcomic 命名兼容
    def search_album(self, keyword: str, page: int = 1) -> McSearchPage:
        return self.search(keyword, page)

    def get_album_detail(self, album_id, *, fetch_chapters=True) -> McComicDetail:
        return self.get_comic_detail(album_id, fetch_chapters=fetch_chapters)

    def get_photo_detail(self, photo_id, *, comic_id=None, fetch_image_urls=True) -> McChapterDetail:
        return self.get_chapter_detail(photo_id, comic_id=comic_id, fetch_image_urls=fetch_image_urls)

    def check_photo(self, photo: McChapterDetail) -> bool:
        return self.check_chapter(photo)

    # ------------------------------------------------------------------ 列表项构造

    @classmethod
    def build_comic_info(cls, raw: Dict, site: str, url: str = '', slug: str = '') -> Tuple[str, Dict]:
        """
        把站点返回的列表项统一成 (comic_id, info) 结构，供 McPageContent 使用。
        """
        comic_id = str(raw.get('id'))
        info = {
            'id': comic_id,
            'name': MccmsText.unescape(raw.get('name', '')),
            'author': MccmsText.unescape(raw.get('author', '') or ''),
            'cover': raw.get('pic', '') or raw.get('cover', '') or '',
            'text': MccmsText.unescape(raw.get('text', '') or raw.get('description', '') or ''),
            'serialize': MccmsText.unescape(raw.get('serialize', '') or ''),
            'update_date': str(raw.get('addtime', '') or raw.get('update_date', '') or ''),
            'views': MccmsText.parse_human_number(raw.get('hits', raw.get('views', 0))),
            'score': float(raw.get('score', 0) or 0),
            'tags': cls.parse_tags(raw.get('tags', None)),
            'site': site,
            'url': url,
            'slug': slug,
            'raw': raw,
        }
        return comic_id, info

    @staticmethod
    def parse_tags(tags) -> List[str]:
        if tags is None or tags == '':
            return []
        if isinstance(tags, list):
            return [str(t).strip() for t in tags if str(t).strip()]
        return [t.strip() for t in str(tags).replace('，', ',').replace('|', ',').split(',') if t.strip()]
