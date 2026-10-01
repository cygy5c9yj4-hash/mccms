"""
下载器（对应 jmcomic 的 jm_downloader.py）。

类层次::

    DownloadCallback                 纯日志回调
      └ BaseDownloader               钩子派发 + 插件 + 失败收集 + 下载清单
          └ McDownloader             I/O 调度 + 两级并发
              ├ DoNotDownloadImage             只建目录不下图（测试用）
              └ JustDownloadSpecificCountImage 每话只下前 N 张（测试用）

生命周期与钩子顺序::

    download_comic(comic_id)
      └ before_comic(comic)
           └ 对每个 chapter（可并发）:
                before_chapter(chapter)
                  └ 对每个 image（可并发）:
                       before_image(image, path)
                       [下载 / 命中缓存]
                       after_image(image, path)
                after_chapter(chapter)
      └ after_comic(comic)
"""

from functools import wraps
from time import perf_counter
from typing import Any, Callable, Dict, List, NamedTuple, Optional, Tuple

from .mc_config import McModuleConfig, mc_log
from .mc_entity import (DetailEntity, Downloadable, McChapterDetail,
                        McComicDetail, McImageDetail)
from .mc_exception import (ExceptionTool, LoginRequiredException,
                           PartialDownloadFailedException, VipRequiredException)
from .mc_task_context import bind_mc_task_context, mc_task_context
from .mc_toolkit import file_exists, multi_thread_launcher, thread_pool_executor

__all__ = [
    'DownloadManifest',
    'DownloadCallback',
    'BaseDownloader',
    'McDownloader',
    'DownloadResult',
    'BatchResult',
    'DoNotDownloadImage',
    'JustDownloadSpecificCountImage',
]


# --------------------------------------------------------------------------------------
# 装饰器
# --------------------------------------------------------------------------------------

def catch_exception(func):
    """
    收集下载失败但继续向上抛，最终由 raise_if_has_exception 汇总。
    """

    @wraps(func)
    def wrapper(self, *args, **kwargs):
        try:
            return func(self, *args, **kwargs)
        except Exception as e:
            detail = args[0] if args else None

            if isinstance(detail, McImageDetail):
                mc_log('image.failed', f'图片下载失败: [{detail.download_url}], 异常: [{e}]', e)
                self.download_failed_image.append((detail, e))
            elif isinstance(detail, McChapterDetail):
                mc_log('chapter.failed', f'章节下载失败: [{detail.id}], 异常: [{e}]', e)
                self.download_failed_chapter.append((detail, e))

            raise

    return wrapper


def record_download_duration(label: str = ''):
    """把方法耗时写入返回实体的 duration 字段。"""

    def decorator(func):
        @wraps(func)
        def wrapper(self, *args, **kwargs):
            started = perf_counter()
            result = func(self, *args, **kwargs)
            elapsed = perf_counter() - started
            if isinstance(result, Downloadable):
                result.duration = elapsed
            return result

        return wrapper

    return decorator


# --------------------------------------------------------------------------------------
# 下载清单
# --------------------------------------------------------------------------------------

class DownloadManifest:

    def __init__(self):
        self.image_filepath_list: List[str] = []
        self.export_filepath_dict: Dict[str, List[str]] = {}
        self.duration: Optional[float] = None

    def get_export_filepath_list(self, suffix: str) -> List[str]:
        return list(self.export_filepath_dict.get(str(suffix).lower().lstrip('.'), []))

    def record_export_filepath(self, filepath: str):
        from .mc_toolkit import of_file_name
        name = of_file_name(filepath)
        index = name.rfind('.')
        suffix = name[index + 1:].lower() if index > 0 else ''
        self.export_filepath_dict.setdefault(suffix, []).append(filepath)

    def __repr__(self):
        return (f'DownloadManifest(images={len(self.image_filepath_list)}, '
                f'exports={ {k: len(v) for k, v in self.export_filepath_dict.items()} })')


# --------------------------------------------------------------------------------------
# 回调
# --------------------------------------------------------------------------------------

class DownloadCallback:

    def before_comic(self, comic: McComicDetail):
        mc_log('comic.before', f'开始下载漫画: [{comic.comic_id}] {comic.name}')

    def after_comic(self, comic: McComicDetail):
        mc_log('comic.after', f'漫画下载完成: [{comic.comic_id}] {comic.name}')

    def before_chapter(self, chapter: McChapterDetail):
        mc_log('chapter.before', f'开始下载章节: [{chapter.chapter_id}] {chapter.name}')

    def after_chapter(self, chapter: McChapterDetail):
        mc_log('chapter.after', f'章节下载完成: [{chapter.chapter_id}] {chapter.name}')

    def before_image(self, image: McImageDetail, img_save_path: str):
        mc_log('image.before', f'开始下载图片: [{image.download_url}]')

    def after_image(self, image: McImageDetail, img_save_path: str):
        mc_log('image.after', f'图片下载完成: [{img_save_path}]')

    # jmcomic 命名兼容
    before_album = before_comic
    after_album = after_comic
    before_photo = before_chapter
    after_photo = after_chapter


# --------------------------------------------------------------------------------------
# 下载器基类
# --------------------------------------------------------------------------------------

class BaseDownloader(DownloadCallback):

    def __init__(self, option):
        self.option = option
        self.client = None

        # comic -> chapter -> [(save_path, image)]
        self.download_success_dict: Dict[Any, Dict[Any, List[Tuple[str, McImageDetail]]]] = {}
        self.download_failed_image: List[Tuple[McImageDetail, BaseException]] = []
        self.download_failed_chapter: List[Tuple[McChapterDetail, BaseException]] = []

        self.manifest_dict: Dict[Any, DownloadManifest] = {}
        self._active_manifest: Dict[Any, DownloadManifest] = {}

    # ------------------------------------------------------------------ 过滤

    def do_filter(self, detail: DetailEntity):
        """
        子类/使用者重写点：返回要下载的子集。

        例：``return comic[-5:]`` 只下最后 5 话。
        """
        return detail

    # ------------------------------------------------------------------ 状态

    @property
    def all_success(self) -> bool:
        return not self.has_download_failures

    @property
    def has_download_failures(self) -> bool:
        return len(self.download_failed_image) > 0 or len(self.download_failed_chapter) > 0

    # ------------------------------------------------------------------ 钩子

    def before_comic(self, comic: McComicDetail):
        super().before_comic(comic)
        self.download_success_dict.setdefault(comic, {})
        self.option.call_all_plugin('before_comic', comic=comic, downloader=self)

    def after_comic(self, comic: McComicDetail):
        super().after_comic(comic)
        self.option.call_all_plugin('after_comic', comic=comic, downloader=self)

    def before_chapter(self, chapter: McChapterDetail):
        super().before_chapter(chapter)
        comic = getattr(chapter, 'from_comic', None)
        if comic is not None:
            self.download_success_dict.setdefault(comic, {}).setdefault(chapter, [])
        self.option.call_all_plugin('before_chapter', chapter=chapter, downloader=self)

    def after_chapter(self, chapter: McChapterDetail):
        super().after_chapter(chapter)
        self.option.call_all_plugin('after_chapter', chapter=chapter, downloader=self)

    def before_image(self, image: McImageDetail, img_save_path: str):
        super().before_image(image, img_save_path)
        self.option.call_all_plugin('before_image', image=image, downloader=self)

    def after_image(self, image: McImageDetail, img_save_path: str):
        super().after_image(image, img_save_path)
        self.option.call_all_plugin('after_image', image=image, downloader=self)

        chapter = image.from_chapter
        comic = getattr(chapter, 'from_comic', None) if chapter is not None else None
        if comic is not None and chapter is not None:
            self.download_success_dict.setdefault(comic, {}).setdefault(chapter, []).append((img_save_path, image))

    # ------------------------------------------------------------------ 清单

    def begin_manifest(self, detail: DetailEntity) -> DownloadManifest:
        manifest = DownloadManifest()
        self.manifest_dict[detail] = manifest
        self._active_manifest[detail] = manifest
        return manifest

    def resolve_manifest_detail(self, detail: DetailEntity) -> Optional[DetailEntity]:
        """
        找到 detail 对应的活跃下载清单 key。

        场景：
        - detail 本身就是当前下载对象 -> 直接命中
        - detail 是章节，但其漫画才是清单持有者 -> 用 comic
        - detail 是漫画，但当前在单独下载某一章节 -> 反向匹配 comic_id
        - 只剩一个活跃清单 -> 直接用它
        """
        if detail in self._active_manifest:
            return detail

        comic = getattr(detail, 'from_comic', None)
        if comic is not None and comic in self._active_manifest:
            return comic

        detail_comic_id = str(getattr(detail, 'comic_id', '') or '')
        if detail_comic_id:
            for key in self._active_manifest:
                is_chapter = getattr(key, 'is_chapter', None)
                if callable(is_chapter) and is_chapter() \
                        and str(getattr(key, 'comic_id', '') or '') == detail_comic_id:
                    return key

        if len(self._active_manifest) == 1:
            return next(iter(self._active_manifest))

        # 退回到已完成但仍在记录中的清单
        if detail in self.manifest_dict:
            return detail

        return None

    def record_export_filepath(self, detail: DetailEntity, filepath: str) -> None:
        key = self.resolve_manifest_detail(detail)
        ExceptionTool.require_true(key is not None, f'当前实体没有活动的下载清单: {detail}')
        self._active_manifest[key].record_export_filepath(filepath)

    def finish_manifest(self, detail: DetailEntity) -> DownloadManifest:
        manifest = self._active_manifest.pop(detail, None)
        if manifest is None:
            manifest = self.manifest_dict.get(detail) or DownloadManifest()

        entries: List[Tuple[int, int, str]] = []
        for _, chapter_map in self.download_success_dict.items():
            for chapter, image_list in chapter_map.items():
                for save_path, image in image_list:
                    entries.append((getattr(chapter, 'index', 0), getattr(image, 'index', 0), save_path))

        entries.sort(key=lambda item: (item[0], item[1]))
        manifest.image_filepath_list = [path for _, _, path in entries]
        manifest.duration = getattr(detail, 'duration', None)
        return manifest

    # ------------------------------------------------------------------ 异常

    def raise_if_has_exception(self):
        if not self.has_download_failures:
            return

        # 权限类异常优先原样抛出，便于调用方区分"没权限"和"下载出错"
        for failed_list in (self.download_failed_chapter, self.download_failed_image):
            for _, e in failed_list:
                if isinstance(e, (LoginRequiredException, VipRequiredException)):
                    raise e

        msg_list = ['部分下载失败', '']

        if self.download_failed_chapter:
            lines = '\n'.join(f'  - {d.id} {d.name}: {e}' for d, e in self.download_failed_chapter[:5])
            msg_list.append(f'共{len(self.download_failed_chapter)}个章节下载失败:\n{lines}')

        if self.download_failed_image:
            lines = '\n'.join(f'  - {d.download_url}: {e}' for d, e in self.download_failed_image[:5])
            msg_list.append(f'共{len(self.download_failed_image)}个图片下载失败:\n{lines}')

        ExceptionTool.raises('\n'.join(msg_list), {'downloader': self}, PartialDownloadFailedException)

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()

    def close(self):
        if self.client is not None:
            postman = getattr(self.client, 'postman', None)
            if postman is not None:
                postman.close()


# --------------------------------------------------------------------------------------
# 具体下载器
# --------------------------------------------------------------------------------------

class McDownloader(BaseDownloader):

    def __init__(self, option):
        super().__init__(option)
        self.client = self.create_client()

    def create_client(self):
        return self.option.build_client()

    # ------------------------------------------------------------------ 漫画

    @record_download_duration('comic')
    def download_comic(self, comic_id):
        with mc_task_context(download_type='comic', mc_id=str(comic_id)):
            comic = self.client.get_comic_detail(comic_id)
            self.begin_manifest(comic)
            try:
                self.download_by_comic_detail(comic)
            finally:
                self.finish_manifest(comic)
        return comic

    def download_by_comic_detail(self, comic: McComicDetail):
        comic.save_path = self.option.dir_rule.decide_comic_root_dir(comic)

        self.before_comic(comic)
        if comic.skip:
            return comic

        self.execute_on_condition(
            comic,
            self.download_by_chapter_detail,
            self.option.decide_chapter_batch_count(comic),
        )

        self.after_comic(comic)
        return comic

    # ------------------------------------------------------------------ 章节

    @record_download_duration('chapter')
    def download_chapter(self, chapter_id, comic_id=None):
        with mc_task_context(download_type='chapter', mc_id=str(chapter_id)):
            chapter = self.client.get_chapter_detail(chapter_id, comic_id=comic_id, fetch_image_urls=False)
            self.attach_comic_context(chapter)
            self.begin_manifest(chapter)
            try:
                self.download_by_chapter_detail(chapter)
            finally:
                self.finish_manifest(chapter)
        return chapter

    @catch_exception
    @record_download_duration('chapter')
    def download_by_chapter_detail(self, chapter: McChapterDetail):
        self.attach_comic_context(chapter)

        chapter.save_path = self.option.decide_image_save_dir(chapter)

        # 取图 + 权限校验（无权限时抛出 AccessDeniedException 子类）
        self.client.check_chapter(chapter)

        self.before_chapter(chapter)
        if chapter.skip:
            return chapter

        self.execute_on_condition(
            chapter,
            self.download_by_image_detail,
            self.option.decide_image_batch_count(chapter),
        )

        self.after_chapter(chapter)
        return chapter

    def attach_comic_context(self, chapter: McChapterDetail):
        """
        给独立下载的章节补上所属漫画上下文，使 dir_rule 里的 Cxxx 字段可用。
        """
        if getattr(chapter, 'from_comic', None) is not None:
            return chapter.from_comic

        comic_id = getattr(chapter, 'comic_id', None)
        if not comic_id:
            return None

        try:
            comic = self.client.get_comic_detail(comic_id, fetch_chapters=False)
        except Exception as e:
            mc_log('chapter', f'获取漫画[{comic_id}]上下文失败: {e}')
            return None

        chapter.from_comic = comic
        return comic

    # ------------------------------------------------------------------ 图片

    @catch_exception
    @record_download_duration('image')
    def download_by_image_detail(self, image: McImageDetail):
        img_save_path = self.option.decide_image_filepath(image)
        image.save_path = img_save_path
        image.exists = file_exists(img_save_path)
        image.cache = self.option.decide_download_cache(image)

        self.before_image(image, img_save_path)
        if image.skip:
            return image

        if image.cache and image.exists:
            # 命中缓存也要触发 after_image（保证插件能统计到全部图片）
            self.after_image(image, img_save_path)
            return image

        decode_image = self.option.decide_download_image_decode(image)
        self.client.download_by_image_detail(image, img_save_path, decode_image=decode_image)
        self.after_image(image, img_save_path)
        return image

    # ------------------------------------------------------------------ 并发

    def execute_on_condition(self, iter_objs, apply: Callable, count_batch: int):
        """
        两级并发：线程数 >= 对象数时一对象一线程，否则用线程池。
        """
        objs = list(self.do_filter(iter_objs))

        if len(objs) == 0:
            return

        apply = bind_mc_task_context(apply)
        count_batch = max(1, int(count_batch or 1))

        if count_batch >= len(objs):
            multi_thread_launcher(iter_objs=objs, apply_each_obj_func=apply, wait_finish=True)
        else:
            thread_pool_executor(iter_objs=objs, apply_each_obj_func=apply,
                                 max_workers=count_batch, wait_finish=True)

    # ------------------------------------------------------------------ 类替换

    @classmethod
    def use(cls):
        McModuleConfig.CLASS_DOWNLOADER = cls
        mc_log('downloader', f'已替换默认下载器为: {cls}')


# --------------------------------------------------------------------------------------
# 测试用下载器
# --------------------------------------------------------------------------------------

class DoNotDownloadImage(McDownloader):
    """只创建目录结构，不真正下载图片。"""

    def download_by_image_detail(self, image: McImageDetail):
        img_save_path = self.option.decide_image_filepath(image)
        image.save_path = img_save_path
        self.before_image(image, img_save_path)
        self.after_image(image, img_save_path)
        return image


class JustDownloadSpecificCountImage(McDownloader):
    """每个章节只下载前 N 张图。"""

    def __init__(self, option, count: int = 3):
        super().__init__(option)
        self.count = count

    def do_filter(self, detail: DetailEntity):
        if isinstance(detail, McChapterDetail):
            return detail[:self.count]
        return detail


# --------------------------------------------------------------------------------------
# 返回值
# --------------------------------------------------------------------------------------

class DownloadResult(NamedTuple):
    detail: DetailEntity
    downloader: BaseDownloader

    @property
    def manifest(self) -> DownloadManifest:
        return self.downloader.manifest_dict[self.detail]

    @property
    def duration(self) -> Optional[float]:
        return self.detail.duration


class BatchResult(set):
    """批量下载结果，失败项收集在 failed 中。"""

    def __init__(self, *args):
        super().__init__(*args)
        self.failed: Dict[str, BaseException] = {}

    @property
    def all_succeeded(self) -> bool:
        return len(self.failed) == 0

    @property
    def total(self) -> int:
        return len(self) + len(self.failed)

    def __repr__(self):
        return f'BatchResult(success={len(self)}, failed={len(self.failed)})'
