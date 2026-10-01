"""
对外 API（对应 jmcomic 的 api.py）。

最简单用法::

    import mccms

    # 下载整本漫画
    mccms.download_comic('17001')

    # 下载单个章节
    mccms.download_chapter('291931', comic_id='17001')

    # 用自己的 option
    option = mccms.create_option_by_file('my_option.yml')
    option.download_comic('17001')
"""

import asyncio
from functools import partial
from typing import Iterable, Union

from .mc_config import McModuleConfig, mc_log
from .mc_downloader import BatchResult, DownloadResult, McDownloader
from .mc_exception import ExceptionTool
from .mc_task_context import bind_mc_task_context, mc_task_context
from .mc_toolkit import PackerUtil

try:  # Python 3.9+
    from asyncio import to_thread as _to_thread
except ImportError:  # pragma: no cover - Python 3.8
    async def _to_thread(func, *args, **kwargs):
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(None, partial(func, *args, **kwargs))


__all__ = [
    'download_comic',
    'download_chapter',
    'download_batch',
    'download_comic_async',
    'download_chapter_async',
    'new_downloader',
    'create_option_by_file',
    'create_option_by_env',
    'create_option_by_str',
    'create_option',
    'DownloadResult',
    'BatchResult',
]


def _download_type_of(download_api) -> str:
    name = getattr(download_api, '__name__', download_api.__class__.__name__)
    if name.endswith('_async'):
        name = name[:-6]
    if name.startswith('download_'):
        name = name[9:]
    return name


def new_downloader(option=None, downloader=None) -> McDownloader:
    if option is None:
        option = McModuleConfig.option_class().default()

    if downloader is None:
        downloader = McModuleConfig.downloader_class()

    return downloader(option)


def download_comic(comic_id,
                   option=None,
                   downloader=None,
                   *,
                   check_exception=True,
                   ) -> Union[DownloadResult, BatchResult]:
    """
    下载一本漫画（含全部章节）。

    :param comic_id: 漫画 id / slug / URL；传入可迭代对象时视为批量下载
    :param option: 下载选项
    :param downloader: 下载器类
    :param check_exception: 单个 id 时有效：下载结束后若有失败则抛出 PartialDownloadFailedException
    """
    if not isinstance(comic_id, (str, int)):
        return download_batch(download_comic, comic_id, option, downloader)

    with mc_task_context(download_type='comic', mc_id=str(comic_id)):
        with new_downloader(option, downloader) as dler:
            comic = dler.download_comic(comic_id)

            if check_exception:
                dler.raise_if_has_exception()

        return DownloadResult(comic, dler)


def download_chapter(chapter_id,
                     option=None,
                     downloader=None,
                     *,
                     comic_id=None,
                     check_exception=True,
                     ) -> Union[DownloadResult, BatchResult]:
    """
    下载一个章节。

    :param chapter_id: 章节 id 或 URL
    :param comic_id: 所属漫画 id（可选，能提供更完整的目录命名信息）
    """
    if not isinstance(chapter_id, (str, int)):
        return download_batch(download_chapter, chapter_id, option, downloader, comic_id=comic_id)

    chapter_id, comic_id = _resolve_ids(chapter_id, comic_id, option)

    with mc_task_context(download_type='chapter', mc_id=str(chapter_id)):
        with new_downloader(option, downloader) as dler:
            chapter = dler.download_chapter(chapter_id, comic_id=comic_id)

            if check_exception:
                dler.raise_if_has_exception()

        return DownloadResult(chapter, dler)


def _resolve_ids(chapter_id, comic_id, option):
    """
    允许传入章节 URL（TIBIU 的 /chapter/{comic}/{chapter} 能同时解析出两个 id）。
    """
    text = str(chapter_id)
    if 'http' not in text and '/' not in text:
        return chapter_id, comic_id

    try:
        option = option or McModuleConfig.option_class().default()
        client = option.build_client()
    except Exception:
        return chapter_id, comic_id

    if hasattr(client, 'parse_chapter_url'):
        parsed_comic_id, parsed_chapter_id = client.parse_chapter_url(text)
        return parsed_chapter_id or chapter_id, comic_id or parsed_comic_id

    return (client.get_chapter_id_from_url(text) or chapter_id,
            comic_id or client.get_comic_id_from_url(text))


def download_batch(download_api,
                   mc_id_iter: Iterable,
                   option=None,
                   downloader=None,
                   **kwargs) -> BatchResult:
    """
    批量下载：一个 id 一个线程，失败项收集在 result.failed，不会静默丢失。
    """
    from .mc_toolkit import multi_thread_launcher

    if option is None:
        option = McModuleConfig.option_class().default()

    result = BatchResult()
    download_type = _download_type_of(download_api)

    def _safe_download(mc_id):
        with mc_task_context(download_type=download_type, mc_id=str(mc_id)):
            try:
                ret = download_api(mc_id, option, downloader, **kwargs)
                result.add(ret)
            except Exception as e:
                mc_log('batch.failed', f'批量下载失败: [{mc_id}], 异常: [{e}]', e)
                result.failed[str(mc_id)] = e

    ids = list(dict.fromkeys(str(i) for i in mc_id_iter))
    multi_thread_launcher(
        iter_objs=ids,
        apply_each_obj_func=bind_mc_task_context(_safe_download),
        wait_finish=True,
    )

    return result


# --------------------------------------------------------------------------------------
# 异步门面
# --------------------------------------------------------------------------------------

async def download_comic_async(comic_id, option=None, downloader=None, *, check_exception=True):
    """
    异步版下载。

    注意：本库的下载流程本身是同步 + 多线程的，这里的 async 是把整体放到
    工作线程中执行，便于用 asyncio.gather 并发多本漫画::

        await asyncio.gather(*(download_comic_async(cid) for cid in comic_ids))
    """
    return await _to_thread(download_comic, comic_id, option, downloader,
                            check_exception=check_exception)


async def download_chapter_async(chapter_id, option=None, downloader=None, *,
                                 comic_id=None, check_exception=True):
    return await _to_thread(download_chapter, chapter_id, option, downloader,
                            comic_id=comic_id, check_exception=check_exception)


# --------------------------------------------------------------------------------------
# option 构造
# --------------------------------------------------------------------------------------

def create_option_by_file(filepath):
    return McModuleConfig.option_class().from_file(filepath)


def create_option_by_env(env_name: str = 'MCCMS_OPTION_PATH'):
    from .cli import get_env
    filepath = get_env(env_name, None)
    ExceptionTool.require_true(filepath is not None,
                               f'未配置环境变量: [{env_name}]，请设置为 option 文件路径')
    return create_option_by_file(filepath)


def create_option_by_str(text: str, mode: str = None):
    from .mc_option import McOption
    if mode is None:
        mode = PackerUtil.mode_yml
    data, _ = PackerUtil.unpack_by_str(text, mode)
    return McOption.construct(data or {})


create_option = create_option_by_file
