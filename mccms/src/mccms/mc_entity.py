"""
mccms 领域实体（对应 jmcomic 的 jm_entity.py）。

层级关系::

    McComicDetail   漫画  （≈ jmcomic 的 album / 本子）
      └ McChapterDetail  章节（≈ jmcomic 的 photo）
          └ McImageDetail   图片

所有实体都是 Sequence，支持索引、切片与迭代::

    comic[0]        # 第 1 话
    comic[:3]       # 前 3 话
    chapter[5]      # 第 6 张图
    for image in chapter: ...
"""

from collections.abc import Sequence
from functools import lru_cache
from typing import Any, Dict, Generator, List, Optional, Tuple, Union

from .mc_config import McModuleConfig
from .mc_exception import ExceptionTool
from .mc_toolkit import MccmsText

__all__ = [
    'Downloadable',
    'McBaseEntity',
    'IndexedEntity',
    'DetailEntity',
    'McImageDetail',
    'McChapterDetail',
    'McComicDetail',
    'McPageContent',
    'McSearchPage',
    'McCategoryPage',
]


# --------------------------------------------------------------------------------------
# 基础设施
# --------------------------------------------------------------------------------------

class Downloadable:
    """可下载实体共有的下载状态字段。"""

    def __init__(self):
        self.save_path: str = ''            # 下载保存路径
        self.exists: bool = False           # 下载前目标是否已存在
        self.skip = False                   # 是否跳过本次下载（可供插件设置）
        self.cache = True                   # 已存在时是否使用缓存
        self.duration: Optional[float] = None  # 下载耗时（秒）


class McBaseEntity:

    # dir_rule 的字段前缀，子类覆写
    prefix: str = ''

    def to_file(self, filepath):
        from .mc_toolkit import PackerUtil
        PackerUtil.pack(self.get_properties_dict(), filepath)

    @classmethod
    def is_image(cls):
        return False

    @classmethod
    def is_chapter(cls):
        return False

    @classmethod
    def is_comic(cls):
        return False

    @classmethod
    def is_page(cls):
        return False

    # jmcomic 命名兼容（必须委托调用，否则子类覆写不会生效）
    @classmethod
    def is_photo(cls):
        return cls.is_chapter()

    @classmethod
    def is_album(cls):
        return cls.is_comic()


class IndexedEntity(Sequence):

    def getindex(self, index: int):
        raise NotImplementedError

    def __len__(self):
        raise NotImplementedError

    def __getitem__(self, item) -> Any:
        if isinstance(item, slice):
            start, stop, step = item.indices(len(self))
            return [self.getindex(index) for index in range(start, stop, step)]

        if isinstance(item, int):
            if item < 0:
                item += len(self)
            if item < 0 or item >= len(self):
                raise IndexError(f'index {item} out of range')
            return self.getindex(item)

        raise TypeError(f'Invalid item type for {self.__class__}')

    def __iter__(self):
        for index in range(len(self)):
            yield self.getindex(index)


class DetailEntity(McBaseEntity, IndexedEntity):

    @property
    def id(self) -> str:
        raise NotImplementedError

    @property
    def title(self) -> str:
        return getattr(self, 'name')

    @property
    def author(self) -> str:
        raise NotImplementedError

    @property
    def oname(self) -> str:
        """原始名称（去掉 [汉化组]、【社团】 这类后缀）。"""
        oname = MccmsText.parse_orig_name(self.title)
        return oname if oname else self.title

    @property
    def authoroname(self):
        return f'【{self.author}】{self.oname}'

    @property
    def idoname(self):
        return f'[{self.id}] {self.oname}'

    def __str__(self):
        return f'{self.__class__.__name__}({self.alias_en()}-{self.id}: "{self.title}")'

    __repr__ = __str__

    @classmethod
    def alias_en(cls):
        return 'comic' if issubclass(cls, McComicDetail) else 'chapter'

    @classmethod
    def alias_cn(cls) -> str:
        return '漫画' if issubclass(cls, McComicDetail) else '章节'

    @classmethod
    def get_dirname(cls, detail: 'DetailEntity', ref: str):
        """
        供 DirRule 使用：返回 ref 字段对应的目录名。
        使用者可通过 McModuleConfig.CFIELD_ADVICE / HFIELD_ADVICE 注册自定义字段。
        """
        advice_func = (McModuleConfig.CFIELD_ADVICE
                       if isinstance(detail, McComicDetail)
                       else McModuleConfig.HFIELD_ADVICE).get(ref, None)
        if advice_func is not None:
            return advice_func(detail)

        if not hasattr(detail, ref):
            ExceptionTool.raises(
                f'[{detail.__class__.__name__}] 不存在字段 [{ref}]，'
                f'可用字段: {sorted(detail.get_properties_dict().keys())}'
            )
        return getattr(detail, ref)

    def get_properties_dict(self) -> Dict[str, Any]:
        """
        返回 f-string 形式的属性字典（key 带前缀），供 dir_rule 的 {Cid} / {Chname} 使用。
        """
        import inspect

        prefix = self.prefix
        result: Dict[str, Any] = {}

        for cls in inspect.getmro(type(self)):
            for name, attr in vars(cls).items():
                if name.startswith('_'):
                    continue
                key = prefix + name
                if key in result:
                    continue
                if isinstance(attr, property):
                    try:
                        result[key] = attr.__get__(self, cls)
                    except Exception:  # pragma: no cover
                        continue

        for name, value in self.__dict__.items():
            if name.startswith('_'):
                continue
            result.setdefault(prefix + name, value)

        advice_dict = McModuleConfig.CFIELD_ADVICE if self.is_comic() else McModuleConfig.HFIELD_ADVICE
        for name, func in advice_dict.items():
            result[prefix + name] = func(self)

        return result


# --------------------------------------------------------------------------------------
# 图片
# --------------------------------------------------------------------------------------

class McImageDetail(McBaseEntity, Downloadable):

    def __init__(self,
                 chapter_id,
                 img_url: str,
                 img_file_name: str,
                 img_file_suffix: str,
                 from_chapter: 'McChapterDetail' = None,
                 index: int = -1,
                 img_id: Optional[str] = None,
                 scramble_n: int = 0,
                 ):
        super().__init__()
        self.chapter_id: str = str(chapter_id)
        self.img_url: str = img_url
        self.img_file_name: str = img_file_name      # 不含后缀
        self.img_file_suffix: str = img_file_suffix  # 含点，如 .webp
        self.img_id: Optional[str] = img_id
        # 需要还原的竖带数量；<=1 表示原图就是正常顺序
        self.scramble_n: int = int(scramble_n or 0)
        self.from_chapter: 'McChapterDetail' = from_chapter  # type: ignore
        self.index = index  # 从 1 开始

    # ------------------------------------------------------------------ 命名

    @property
    def filename_without_suffix(self) -> str:
        return self.img_file_name

    @property
    def filename(self) -> str:
        return self.img_file_name + self.img_file_suffix

    @property
    def is_gif(self) -> bool:
        return self.img_file_suffix.lower() == '.gif'

    @property
    def download_url(self) -> str:
        return self.img_url

    @property
    def tag(self) -> str:
        total = len(self.from_chapter) if self.from_chapter is not None else '?'
        return f'{self.chapter_id}/{self.filename} [{self.index}/{total}]'

    # ------------------------------------------------------------------ 构造

    @property
    def is_scrambled(self) -> bool:
        """该图是否需要做竖带倒序还原。"""
        return self.scramble_n > 1

    @classmethod
    def of(cls,
           chapter_id,
           img_url: str,
           from_chapter: 'McChapterDetail' = None,
           index: int = -1,
           img_id: Optional[str] = None,
           scramble_n: int = 0,
           ) -> 'McImageDetail':
        url = str(img_url).strip()
        ExceptionTool.require_true(url != '', f'图片地址为空: chapter={chapter_id}, index={index}')

        # 去掉 query，保留路径用于取文件名
        path = url.split('?')[0].split('#')[0]
        slash = path.rfind('/')
        dot = path.rfind('.')

        if dot > slash:
            file_name, suffix = path[slash + 1:dot], path[dot:]
        else:
            file_name, suffix = path[slash + 1:], '.jpg'

        # 有些站点的图片是 /xxx_01_0.jpg 这种，保持原名即可
        if file_name == '':
            file_name = f'{index:03d}'

        if scramble_n is None and from_chapter is not None:
            scramble_n = getattr(from_chapter, 'scramble_n', 0)

        return cls(
            chapter_id=chapter_id,
            img_url=url,
            img_file_name=file_name,
            img_file_suffix=suffix,
            from_chapter=from_chapter,
            index=index,
            img_id=img_id,
            scramble_n=scramble_n or 0,
        )

    @classmethod
    def is_image(cls):
        return True

    def __str__(self):
        return f'McImageDetail(image-[{self.download_url}])'

    __repr__ = __str__


# --------------------------------------------------------------------------------------
# 章节
# --------------------------------------------------------------------------------------

class McChapterDetail(DetailEntity, Downloadable):
    """
    章节实体。

    :param chapter_id: 章节 id
    :param name: 章节名
    :param comic_id: 所属漫画 id
    :param index: 章节序号（从 1 开始）
    :param count: 图片数量（站点给出的 pnum，可能为 0）
    :param access: 访问权限信息 dict，含 vip / cion / price
    """

    prefix = 'Ch'

    def __init__(self,
                 chapter_id,
                 name,
                 comic_id=None,
                 index: int = 1,
                 count: int = 0,
                 access: Optional[Dict] = None,
                 update_date: str = '',
                 image_url_list: Optional[List[str]] = None,
                 from_comic: 'McComicDetail' = None,
                 scramble_n: int = 0,
                 ):
        super().__init__()
        self.chapter_id: str = str(chapter_id)
        self.name: str = str(name).strip()
        self.comic_id: str = str(comic_id) if comic_id is not None else ''
        self.index: int = int(index)
        self.count: int = int(count or 0)
        self.access: Dict = dict(access or {})
        self.update_date: str = update_date

        self.from_comic: 'McComicDetail' = from_comic  # type: ignore
        # 图片乱序的竖带数量（站点下发，逐章不同）；<=1 表示无需还原
        self.scramble_n: int = int(scramble_n or 0)
        self._image_url_list: List[str] = list(image_url_list or [])
        self._image_id_list: List[Optional[str]] = []

        self.url: str = ''

    # ------------------------------------------------------------------ 权限

    @property
    def vip(self) -> int:
        return MccmsText.safe_int(self.access.get('vip', 0))

    @property
    def cion(self) -> int:
        return MccmsText.safe_int(self.access.get('cion', 0))

    @property
    def price(self) -> int:
        return MccmsText.safe_int(self.access.get('price', 0))

    @property
    def is_free(self) -> bool:
        return self.vip == 0 and self.cion == 0 and self.price == 0

    @property
    def access_desc(self) -> str:
        if self.is_free:
            return '免费'
        flags = []
        if self.vip:
            flags.append('VIP')
        if self.cion:
            flags.append(f'金币({self.cion})')
        if self.price:
            flags.append(f'付费({self.price})')
        return '/'.join(flags) if flags else '受限'

    # ------------------------------------------------------------------ 基础

    @property
    def id(self) -> str:
        return self.chapter_id

    @property
    def title(self) -> str:
        return self.name

    @property
    def author(self) -> str:
        if self.from_comic is not None:
            return self.from_comic.author
        return McModuleConfig.DEFAULT_AUTHOR

    @property
    def tags(self) -> List[str]:
        if self.from_comic is not None:
            return self.from_comic.tags
        return []

    @property
    def indextitle(self) -> str:
        """
        章节标题，形如「第3话 标题」。

        若章节名本身已经是「第N话/章/集」，则不再重复加序号前缀。
        """
        import re
        if re.match(r'^\s*第\s*[\d一二三四五六七八九十百千]+\s*[话話章集回]', self.name):
            return self.name
        return f'第{self.index}话 {self.name}'

    @property
    def comic_name(self) -> str:
        if self.from_comic is not None:
            return self.from_comic.name
        return ''

    @property
    def image_url_list(self) -> List[str]:
        return self._image_url_list

    def set_image_url_list(self, url_list: List[str], id_list: Optional[List[str]] = None):
        self._image_url_list = [str(u) for u in (url_list or [])]
        self._image_id_list = list(id_list) if id_list else [None] * len(self._image_url_list)

        # getindex 带 lru_cache，图片列表变化后必须失效，否则会拿到旧图片
        cache_clear = getattr(self.getindex, 'cache_clear', None)
        if callable(cache_clear):
            cache_clear()

        return self

    # ------------------------------------------------------------------ 图片

    def create_image_detail(self, index: int) -> McImageDetail:
        length = len(self._image_url_list)
        if index >= length:
            ExceptionTool.raises(
                f'图片下标越界: chapter={self.chapter_id}, index={index}, 共 {length} 张',
                {'chapter': self},
            )

        img_id = self._image_id_list[index] if index < len(self._image_id_list) else None
        return McModuleConfig.image_class().of(
            self.chapter_id,
            self._image_url_list[index],
            from_chapter=self,
            index=index + 1,
            img_id=img_id,
            scramble_n=self.scramble_n,
        )

    @lru_cache(None)
    def getindex(self, index) -> McImageDetail:
        return self.create_image_detail(index)

    def __getitem__(self, item) -> Union[McImageDetail, List[McImageDetail]]:
        return super().__getitem__(item)

    def __len__(self):
        return len(self._image_url_list)

    def __iter__(self) -> Generator[McImageDetail, None, None]:
        return super().__iter__()

    @classmethod
    def is_chapter(cls):
        return True

    def __str__(self):
        access = '' if self.is_free else f'[{self.access_desc}]'
        return (f'McChapterDetail(chapter-{self.chapter_id}: "{self.name}"'
                f' {self.index}话{access} {len(self)}p)')


# --------------------------------------------------------------------------------------
# 漫画
# --------------------------------------------------------------------------------------

class McComicDetail(DetailEntity, Downloadable):
    """
    漫画实体。

    :param comic_id: 漫画 id
    :param name: 漫画名
    :param episode_list: [(chapter_id, chapter_index, chapter_name), ...]
    :param chapter_access: {chapter_id: {vip, cion, price, pnum, addtime}}
    """

    prefix = 'C'

    def __init__(self,
                 comic_id,
                 name,
                 episode_list: Optional[List[Tuple]] = None,
                 author: str = '',
                 authors: Optional[List[str]] = None,
                 tags: Optional[List[str]] = None,
                 description: str = '',
                 cover: str = '',
                 serialize: str = '',
                 update_date: str = '',
                 add_date: str = '',
                 views: int = 0,
                 score: float = 0.0,
                 site: str = '',
                 url: str = '',
                 slug: str = '',
                 chapter_access: Optional[Dict[str, Dict]] = None,
                 ):
        super().__init__()
        self.comic_id: str = str(comic_id)
        self.name: str = str(name).strip()
        self.description: str = str(description or '').strip()
        self.cover: str = cover
        self.serialize: str = serialize          # 连载状态：完结/连载中
        self.update_date: str = update_date
        self.add_date: str = add_date
        self.views: int = int(views or 0)
        self.score: float = float(score or 0.0)
        self.site: str = site
        self.url: str = url
        self.slug: str = slug

        self.authors: List[str] = list(authors) if authors else ([author] if author else [])
        self.tags: List[str] = list(tags or [])
        self.chapter_access: Dict[str, Dict] = dict(chapter_access or {})

        episode_list = list(episode_list or [])
        if len(episode_list) == 0:
            # 无章节信息的漫画，自成一章
            episode_list = [(self.comic_id, 1, self.name)]
        self.episode_list: List[Tuple] = self.distinct_episode(episode_list)

    # ------------------------------------------------------------------ 基础

    @property
    def id(self) -> str:
        return self.comic_id

    @property
    def author(self) -> str:
        if self.authors:
            return self.authors[0]
        return McModuleConfig.DEFAULT_AUTHOR

    @property
    def page_count(self) -> int:
        """总图片数（各章节 pnum 之和，若站点提供）。"""
        return sum(MccmsText.safe_int(meta.get('pnum', 0))
                   for meta in self.chapter_access.values())

    @property
    def chapter_count(self) -> int:
        return len(self.episode_list)

    @property
    def is_finished(self) -> bool:
        return self.serialize in ('完结', '已完结', 'finished')

    @property
    def latest_chapter_name(self) -> str:
        return self.episode_list[-1][2] if self.episode_list else ''

    # ------------------------------------------------------------------ 章节

    @staticmethod
    def distinct_episode(episode_list: List[Tuple]) -> List[Tuple]:
        """按章节序号排序并去重。"""
        def index_of(item):
            return MccmsText.safe_int(item[1], 0)

        episode_list = sorted(episode_list, key=index_of)
        result = []
        seen_ids = set()

        for item in episode_list:
            chapter_id = str(item[0])
            if chapter_id in seen_ids:
                continue
            seen_ids.add(chapter_id)
            result.append((chapter_id, index_of(item), item[2]))

        # 序号重排为连续值，避免站点数据跳号导致目录名怪异
        return [(cid, i + 1, name) for i, (cid, _, name) in enumerate(result)]

    def create_chapter_detail(self, index: int) -> McChapterDetail:
        length = len(self.episode_list)
        if index >= length:
            ExceptionTool.raises(
                f'章节下标越界: comic={self.comic_id}, index={index}, 共 {length} 话',
                {'comic': self},
            )

        chapter_id, chapter_index, chapter_name = self.episode_list[index]
        access = self.chapter_access.get(str(chapter_id), {})

        return McModuleConfig.chapter_class()(
            chapter_id=chapter_id,
            name=chapter_name,
            comic_id=self.comic_id,
            index=chapter_index,
            count=MccmsText.safe_int(access.get('pnum', 0)),
            access=access,
            update_date=str(access.get('addtime', '')),
            from_comic=self,
        )

    def chapter_at(self, index: int) -> McChapterDetail:
        """按 1 起始的章节序号取章节（= self[index - 1]）。"""
        return self[index - 1]

    @lru_cache(None)
    def getindex(self, index) -> McChapterDetail:
        return self.create_chapter_detail(index)

    def __getitem__(self, item) -> Union[McChapterDetail, List[McChapterDetail]]:
        return super().__getitem__(item)

    def __len__(self):
        return len(self.episode_list)

    def __iter__(self) -> Generator[McChapterDetail, None, None]:
        return super().__iter__()

    @classmethod
    def is_comic(cls):
        return True

    def __str__(self):
        return (f'McComicDetail(comic-{self.comic_id}: "{self.name}" '
                f'{len(self.episode_list)}话/{self.author})')


# --------------------------------------------------------------------------------------
# 分页
# --------------------------------------------------------------------------------------

class McPageContent(McBaseEntity, IndexedEntity):
    """
    分页内容。content 的结构为 [(comic_id, {name, author, cover, url, ...}), ...]
    """

    ContentItem = Tuple[str, Dict[str, Any]]

    def __init__(self, content: List[ContentItem], total: int = 0, page_number: Optional[int] = None):
        self.content = list(content or [])
        self.total = int(total or 0)
        self.page_number = page_number

    @property
    def page_size(self) -> int:
        return McModuleConfig.PAGE_SIZE_SEARCH

    @property
    def page_count(self) -> int:
        import math
        if self.total <= 0:
            return 1 if self.content else 0
        return math.ceil(self.total / self.page_size)

    # ------------------------------------------------------------------ 迭代器

    def iter_id(self) -> Generator[str, None, None]:
        for comic_id, _ in self.content:
            yield comic_id

    def iter_id_title(self) -> Generator[Tuple[str, str], None, None]:
        for comic_id, info in self.content:
            yield comic_id, info.get('name', '')

    def iter_id_title_tag(self) -> Generator[Tuple[str, str, List[str]], None, None]:
        for comic_id, info in self.content:
            yield comic_id, info.get('name', ''), info.get('tags', [])

    def iter_id_title_author(self) -> Generator[Tuple[str, str, str], None, None]:
        for comic_id, info in self.content:
            yield comic_id, info.get('name', ''), info.get('author', '')

    # ------------------------------------------------------------------ 容器协议

    def __len__(self):
        return len(self.content)

    def __iter__(self):
        return self.iter_id_title()

    def __getitem__(self, item) -> Union[ContentItem, List[ContentItem]]:
        return super().__getitem__(item)

    def getindex(self, index: int) -> ContentItem:
        return self.content[index]

    def to_comic_list(self) -> List[McComicDetail]:
        """把分页结果转成轻量 McComicDetail 列表（不含章节）。"""
        result = []
        for comic_id, info in self.content:
            result.append(McModuleConfig.comic_class()(
                comic_id=comic_id,
                name=info.get('name', ''),
                author=info.get('author', ''),
                authors=[info['author']] if info.get('author') else [],
                tags=info.get('tags', []),
                description=info.get('text', ''),
                cover=info.get('cover', ''),
                serialize=info.get('serialize', ''),
                update_date=info.get('update_date', ''),
                views=info.get('views', 0),
                score=info.get('score', 0.0),
                site=info.get('site', ''),
                url=info.get('url', ''),
                slug=info.get('slug', ''),
                episode_list=[(comic_id, 1, info.get('name', ''))],
            ))
        return result

    @classmethod
    def is_page(cls):
        return True


class McSearchPage(McPageContent):

    @property
    def page_size(self) -> int:
        return McModuleConfig.PAGE_SIZE_SEARCH

    @property
    def is_single_comic(self) -> bool:
        return hasattr(self, 'comic')

    @property
    def single_comic(self) -> McComicDetail:
        return getattr(self, 'comic')

    @classmethod
    def wrap_single_comic(cls, comic: McComicDetail, page_number: Optional[int] = None) -> 'McSearchPage':
        page = cls([(comic.comic_id, {'name': comic.name, 'tags': comic.tags})], 1, page_number)
        setattr(page, 'comic', comic)
        return page

    # jmcomic 命名兼容
    is_single_album = is_single_comic
    single_album = single_comic
    wrap_single_album = wrap_single_comic


# 分类页与搜索页结构一致
McCategoryPage = McSearchPage
