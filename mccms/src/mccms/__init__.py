"""
mccms —— 面向 Mccms 站点（TIBIU / 漫蛙）的 Python API 与下载器。

设计对齐 JMComic-Crawler-Python（jmcomic）：

======================  ==========================  ==========================
jmcomic                 mccms                       说明
======================  ==========================  ==========================
JmOption                McOption                    配置对象（yml / dict）
JmDownloader            McDownloader                下载器
JmOptionPlugin          McOptionPlugin              插件基类
JmAlbumDetail           McComicDetail               漫画（≈ 本子）
JmPhotoDetail           McChapterDetail             章节
JmImageDetail           McImageDetail               图片
JmSearchPage            McSearchPage                搜索结果分页
download_album()        download_comic()            下载整本
download_photo()        download_chapter()          下载单章
======================  ==========================  ==========================

快速开始::

    import mccms

    # 1. 下载整本漫画（默认 TIBIU）
    mccms.download_comic('17001')

    # 2. 切换站点并下载
    option = mccms.McOption.construct({
        'client': {'impl': 'manhwa'},
        'dir_rule': {'rule': 'Bd_Cname_Chindextitle', 'base_dir': './downloads'},
    })
    option.download_comic('mozhouweixianzaoyu')

    # 3. 只用 API，不下载
    client = mccms.McOption.default().build_client()
    comic = client.get_comic_detail('17001')
    print(comic.name, len(comic))
    chapter = comic[0]
    print(len(chapter), chapter[0].download_url)

    # 4. 搜索
    page = client.search('魔咒')
    for comic_id, name in page.iter_id_title():
        print(comic_id, name)

访问权限说明::

    本库只访问「公开内容」与「当前会话本身有权访问的内容」。
    需要登录/会员的章节会抛出 LoginRequiredException / VipRequiredException，
    调用方可通过 option.client.username/password 或 cookies 提供自己的账号。
    本库不包含任何绕过访问控制的实现。
"""

from .mc_config import *          # noqa: F401,F403
from .mc_config import McMagicConstants, McModuleConfig, mc_log, mc_logger
from .mc_exception import *       # noqa: F401,F403
from .mc_exception import (AccessDeniedException, ExceptionTool,
                           LoginRequiredException, McException,
                           PartialDownloadFailedException,
                           PluginValidationException, VipRequiredException,
                           register_exception_listener)
from .mc_toolkit import *         # noqa: F401,F403
from .mc_toolkit import AdvancedDict, MccmsText, PackerUtil
from .mc_postman import McPostman, McResp, clear_dns_override, register_dns_override
from .mc_decode import (ImageDecodeError, decode_scrambled_image_file,
                        is_scrambled_layout, reverse_vertical_strips)
from .mc_html import Node, parse_html
from .mc_task_context import get_mc_task_context, mc_task_context
from .mc_entity import *          # noqa: F401,F403
from .mc_entity import (McChapterDetail, McComicDetail, McImageDetail,
                        McPageContent, McSearchPage)
from .mc_client_interface import AbstractMcClient, McClientInterface
from .tibiu_client_impl import TibiuClient
from .manhwa_client_impl import ManhwaClient
from .boylove_client_impl import BoyloveClient
from .mc_option import DirRule, McOption
from .mc_downloader import *      # noqa: F401,F403
from .mc_downloader import (BatchResult, DownloadResult, McDownloader)
from .mc_plugin import *          # noqa: F401,F403
from .mc_plugin import McOptionPlugin
from .api import *                # noqa: F401,F403
from .api import (create_option, create_option_by_env, create_option_by_file,
                  create_option_by_str, download_batch, download_chapter,
                  download_chapter_async, download_comic, download_comic_async,
                  new_downloader)
from .cli import main

__version__ = '1.0.0'

# --------------------------------------------------------------------------------------
# 注册站点客户端
# --------------------------------------------------------------------------------------

McModuleConfig.register_client(TibiuClient)
McModuleConfig.register_client(ManhwaClient)
McModuleConfig.register_client(BoyloveClient)

# --------------------------------------------------------------------------------------
# 注册内置插件（扫描本模块 globals 中所有 McOptionPlugin 子类）
# --------------------------------------------------------------------------------------

_registered_plugins = []

for _name, _obj in list(globals().items()):
    if (isinstance(_obj, type)
            and issubclass(_obj, McOptionPlugin)
            and _obj is not McOptionPlugin
            and getattr(_obj, 'plugin_key', None)):
        McModuleConfig.register_plugin(_obj)
        _registered_plugins.append(_obj.plugin_key)

del _name, _obj

# --------------------------------------------------------------------------------------
# jmcomic 命名兼容别名
# --------------------------------------------------------------------------------------

# 下载入口
download_album = download_comic
download_photo = download_chapter
download_album_async = download_comic_async
download_photo_async = download_chapter_async

# 客户端类名
JmAlbumDetail = McComicDetail
JmPhotoDetail = McChapterDetail
JmImageDetail = McImageDetail
JmSearchPage = McSearchPage
JmOption = McOption
JmDownloader = McDownloader
JmOptionPlugin = McOptionPlugin
JmModuleConfig = McModuleConfig

__all__ = [
    'McModuleConfig',
    'McMagicConstants',
    'McOption',
    'DirRule',
    'McDownloader',
    'McOptionPlugin',
    'McComicDetail',
    'McChapterDetail',
    'McImageDetail',
    'McSearchPage',
    'McPageContent',
    'McClientInterface',
    'AbstractMcClient',
    'TibiuClient',
    'ManhwaClient',
    'BoyloveClient',
    'McPostman',
    'McResp',
    'McException',
    'AccessDeniedException',
    'LoginRequiredException',
    'VipRequiredException',
    'PartialDownloadFailedException',
    'PluginValidationException',
    'ExceptionTool',
    'register_exception_listener',
    'download_comic',
    'download_chapter',
    'download_batch',
    'download_comic_async',
    'download_chapter_async',
    'create_option_by_file',
    'create_option_by_str',
    'create_option_by_env',
    'create_option',
    'parse_html',
    'reverse_vertical_strips',
    'decode_scrambled_image_file',
    'ImageDecodeError',
    'register_dns_override',
    'Node',
    'AdvancedDict',
    'MccmsText',
    'main',
    '__version__',
]
