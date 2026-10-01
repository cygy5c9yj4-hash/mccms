"""
mccms 全局配置中心。

对应 jmcomic 的 jm_config.py：
- 日志设施（mc_logger / mc_log / PrettyFormatter）
- 常量（McMagicConstants）
- 模块级配置与注册表（McModuleConfig）
- option 默认值（DEFAULT_OPTION_DICT / option_default_dict）

本模块不 import 任何 mccms 内部模块（避免循环依赖），
需要异常工具时在函数内部延迟 import。
"""

import logging
import os
import sys
from copy import deepcopy
from random import shuffle
from typing import Dict, List

__all__ = [
    'mc_logger',
    'mc_log',
    'disable_mc_log',
    'enable_pretty_log',
    'setup_default_mc_logger',
    'McMagicConstants',
    'McModuleConfig',
]

# --------------------------------------------------------------------------------------
# 日志
# --------------------------------------------------------------------------------------

mc_logger = logging.getLogger('mccms')

VAR_LOG_FMT = '[%(asctime)s] [%(threadName)s]:%(mc_task_context_prefix)s【%(topic)s】%(message)s'
VAR_DATE_FMT = '%H:%M:%S'


class McLogFormatter(logging.Formatter):
    """
    在日志里注入任务上下文前缀（下载类型 / 资源 id），便于并发下载时定位日志。
    """

    def format(self, record: logging.LogRecord):
        if not hasattr(record, 'mc_task_context_prefix'):
            record.mc_task_context_prefix = self.build_task_context_prefix()
        return super().format(record)

    @staticmethod
    def build_task_context_prefix() -> str:
        try:
            from .mc_task_context import get_mc_task_context
        except ImportError:  # pragma: no cover
            return ''

        ctx = get_mc_task_context()
        if not ctx:
            return ''

        download_type = ctx.get('download_type')
        mc_id = ctx.get('mc_id')
        if download_type is None and mc_id is None:
            return ''

        return f'[{download_type}:{mc_id}]'


# topic -> 颜色，用于 pretty log
_TOPIC_COLOR = {
    'album': '\033[36m',
    'chapter': '\033[36m',
    'image': '\033[35m',
    'comic': '\033[36m',
    'plugin': '\033[33m',
    'req': '\033[34m',
    'api': '\033[34m',
    'failed': '\033[31m',
    'error': '\033[31m',
    'warn': '\033[33m',
}
_COLOR_RESET = '\033[0m'


class PrettyFormatter(McLogFormatter):
    """按 topic 前缀着色的 formatter。"""

    def format(self, record: logging.LogRecord):
        text = super().format(record)
        topic = str(getattr(record, 'topic', ''))
        for prefix, color in _TOPIC_COLOR.items():
            if topic.startswith(prefix):
                return f'{color}{text}{_COLOR_RESET}'
        return text


def setup_default_mc_logger():
    if len(mc_logger.handlers) != 0:
        return

    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(McLogFormatter(fmt=VAR_LOG_FMT, datefmt=VAR_DATE_FMT))
    mc_logger.addHandler(handler)
    mc_logger.setLevel(logging.INFO)


def default_mc_logging(topic, msg, e=None):
    if e is None:
        mc_logger.info(msg, extra={'topic': topic})
    else:
        mc_logger.exception(msg, extra={'topic': topic})


def enable_pretty_log():
    """开启彩色日志。"""
    global EXECUTOR_LOG

    setup_default_mc_logger()
    for handler in mc_logger.handlers:
        handler.setFormatter(PrettyFormatter(fmt=VAR_LOG_FMT, datefmt=VAR_DATE_FMT))

    if sys.platform == 'win32':  # pragma: no cover
        os.system('')

    EXECUTOR_LOG = default_mc_logging


def disable_mc_log():
    McModuleConfig.FLAG_ENABLE_MC_LOG = False


def mc_log(topic, msg, e=None):
    """
    统一日志入口，可写为 mc_log(topic, e)。

    由 McModuleConfig.EXECUTOR_LOG 决定最终落点，方便使用者替换日志实现。
    """
    if not McModuleConfig.FLAG_ENABLE_MC_LOG:
        return

    if msg is None and isinstance(topic, BaseException):
        topic, msg, e = 'error', topic, None

    if isinstance(topic, BaseException):
        e = topic
        topic = 'error'
        msg = str(topic)

    if e is not None:
        McModuleConfig.EXECUTOR_LOG(topic, msg, e)
    else:
        McModuleConfig.EXECUTOR_LOG(topic, msg)


# --------------------------------------------------------------------------------------
# 常量
# --------------------------------------------------------------------------------------

class McMagicConstants:
    """协议层常量。"""

    # 站点 key
    SITE_TIBIU = 'tibiu'
    SITE_MANHWA = 'manhwa'
    SITE_BOYLOVE = 'boylove'

    SITE_LIST = (SITE_TIBIU, SITE_MANHWA, SITE_BOYLOVE)

    # 列表排序（TIBIU category_api 的 order 参数）
    ORDER_DEFAULT = 'addtime'
    ORDER_HITS = 'hits'
    ORDER_SCORE = 'score'
    ORDER_NUMS = 'nums'
    ORDER_ADDTIME = 'addtime'

    ORDER_LIST = (ORDER_ADDTIME, ORDER_HITS, ORDER_SCORE, ORDER_NUMS)

    # 完结状态
    FINISH_ALL = ''
    FINISH_YES = '1'
    FINISH_NO = '0'

    # 章节访问权限（Mccms 通用字段）
    ACCESS_FREE = 0
    ACCESS_VIP = 1

    # 图片后缀
    IMAGE_SUFFIX_LIST = ('.jpg', '.jpeg', '.png', '.webp', '.gif', '.bmp', '.avif')


class McModuleConfig:
    """模块级配置与注册表。"""

    # ---------------------------------------------------------------- 站点域名

    PROT = 'https://'

    # TIBIU 主域名（可自行追加镜像）
    DOMAIN_TIBIU_LIST = ['cache.tibiu.net']
    # manhwa 主域名
    DOMAIN_MANHWA_LIST = ['www.manhwa.wang']
    # 香香腐宅（boylove.cc）。该站通过短链轮换入口，
    # 如 fufuhouse.work/{code} → byblovecw7.org → boylove.cc；
    # 镜像域名可直接加到这个列表里。
    DOMAIN_BOYLOVE_LIST = ['boylove.cc']

    DOMAIN_LIST_DICT = {
        McMagicConstants.SITE_TIBIU: DOMAIN_TIBIU_LIST,
        McMagicConstants.SITE_MANHWA: DOMAIN_MANHWA_LIST,
        McMagicConstants.SITE_BOYLOVE: DOMAIN_BOYLOVE_LIST,
    }

    # 站点展示名
    SITE_NAME_DICT = {
        McMagicConstants.SITE_TIBIU: 'TIBIU',
        McMagicConstants.SITE_MANHWA: '漫蛙',
        McMagicConstants.SITE_BOYLOVE: '香香腐宅',
    }

    # ---------------------------------------------------------------- 请求头

    HTML_HEADERS_TEMPLATE = {
        'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) '
                      'AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36',
        'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8',
        'Accept-Language': 'zh-CN,zh;q=0.9,en;q=0.8',
        'Connection': 'keep-alive',
    }

    JSON_HEADERS_TEMPLATE = {
        'User-Agent': HTML_HEADERS_TEMPLATE['User-Agent'],
        'Accept': 'application/json, text/javascript, */*; q=0.01',
        'Accept-Language': HTML_HEADERS_TEMPLATE['Accept-Language'],
        'X-Requested-With': 'XMLHttpRequest',
    }

    # ---------------------------------------------------------------- 分页

    PAGE_SIZE_SEARCH = 10
    PAGE_SIZE_CATEGORY = 10

    # ---------------------------------------------------------------- 默认值

    DEFAULT_AUTHOR = 'default_author'
    DEFAULT_OPTION_VER = '2.1'

    # 请求超时（秒）
    DEFAULT_TIMEOUT = 20
    # 重试次数
    DEFAULT_RETRY_TIMES = 3

    # ---------------------------------------------------------------- 可替换类

    CLASS_OPTION = None
    CLASS_DOWNLOADER = None
    CLASS_COMIC = None
    CLASS_CHAPTER = None
    CLASS_IMAGE = None
    CLASS_SEARCH_PAGE = None

    # ---------------------------------------------------------------- 注册表

    # client_key -> 类
    REGISTRY_CLIENT = {}
    # plugin_key -> 类
    REGISTRY_PLUGIN = {}
    # 异常类型 -> listener(e)
    REGISTRY_EXCEPTION_LISTENER = {}

    # ---------------------------------------------------------------- 行为开关

    FLAG_ENABLE_MC_LOG = True
    # 域名随机打散（多域名负载均衡）
    FLAG_SHUFFLE_DOMAIN = True
    # 遇到限制级内容时自动确认年龄
    FLAG_AUTO_CONFIRM_ADULT = False

    # ---------------------------------------------------------------- dir_rule 自定义字段

    # 注册后即可在 dir_rule 中使用: CFIELD_ADVICE['myfield'] = lambda comic: '...'
    CFIELD_ADVICE = {}
    HFIELD_ADVICE = {}

    # ---------------------------------------------------------------- 日志执行器

    EXECUTOR_LOG = default_mc_logging

    # ---------------------------------------------------------------- option 默认值

    DEFAULT_OPTION_DICT = {
        'log': None,
        'dir_rule': {
            'rule': 'Bd_Cname_Chindextitle',
            'base_dir': None,
            'normalize_zh': None,
        },
        'download': {
            'cache': True,
            'image': {
                'decode': True,
                'suffix': None,
            },
            'threading': {
                'image': 30,
                'chapter': None,
            },
        },
        'client': {
            'impl': None,
            'cache': None,
            'retry_times': DEFAULT_RETRY_TIMES,
            'domain': [],
            'postman': {
                'type': 'requests',
                'meta_data': {
                    'headers': None,
                    'proxies': None,
                    'cookies': {},
                    'timeout': DEFAULT_TIMEOUT,
                },
            },
            # 账号（可留空，留空则不自动登录）
            'username': None,
            'password': None,
            # 是否自动确认年龄门（TIBIU 的限制级内容）
            'confirm_adult': False,
        },
        'plugins': {
            'valid': 'log',
            'dependencies_strategy': 'failed-fast',
        },
    }

    # ================================================================ 访问器

    @classmethod
    def option_class(cls):
        if cls.CLASS_OPTION is None:
            from .mc_option import McOption
            cls.CLASS_OPTION = McOption
        return cls.CLASS_OPTION

    @classmethod
    def downloader_class(cls):
        if cls.CLASS_DOWNLOADER is None:
            from .mc_downloader import McDownloader
            cls.CLASS_DOWNLOADER = McDownloader
        return cls.CLASS_DOWNLOADER

    @classmethod
    def comic_class(cls):
        if cls.CLASS_COMIC is None:
            from .mc_entity import McComicDetail
            cls.CLASS_COMIC = McComicDetail
        return cls.CLASS_COMIC

    @classmethod
    def chapter_class(cls):
        if cls.CLASS_CHAPTER is None:
            from .mc_entity import McChapterDetail
            cls.CLASS_CHAPTER = McChapterDetail
        return cls.CLASS_CHAPTER

    @classmethod
    def image_class(cls):
        if cls.CLASS_IMAGE is None:
            from .mc_entity import McImageDetail
            cls.CLASS_IMAGE = McImageDetail
        return cls.CLASS_IMAGE

    @classmethod
    def search_page_class(cls):
        if cls.CLASS_SEARCH_PAGE is None:
            from .mc_entity import McSearchPage
            cls.CLASS_SEARCH_PAGE = McSearchPage
        return cls.CLASS_SEARCH_PAGE

    # ================================================================ 注册

    @classmethod
    def register_client(cls, client_class):
        from .mc_toolkit import ExceptionTool
        key = getattr(client_class, 'client_key', None)
        ExceptionTool.require_true(key is not None, f'未配置client_key, class: {client_class}')
        cls.REGISTRY_CLIENT[key] = client_class

    @classmethod
    def register_plugin(cls, plugin_class):
        from .mc_toolkit import ExceptionTool
        key = getattr(plugin_class, 'plugin_key', None)
        ExceptionTool.require_true(key is not None, f'未配置plugin_key, class: {plugin_class}')
        cls.REGISTRY_PLUGIN[key] = plugin_class

    @classmethod
    def register_exception_listener(cls, etype, listener):
        cls.REGISTRY_EXCEPTION_LISTENER[etype] = listener

    @classmethod
    def client_impl_class(cls, client_key) -> type:
        from .mc_toolkit import ExceptionTool
        clazz = cls.REGISTRY_CLIENT.get(client_key, None)
        ExceptionTool.require_true(clazz is not None,
                                   f'未注册的client impl: [{client_key}]，'
                                   f'可用: {sorted(cls.REGISTRY_CLIENT.keys())}')
        return clazz

    @classmethod
    def plugin_class(cls, plugin_key) -> type:
        from .mc_toolkit import ExceptionTool
        clazz = cls.REGISTRY_PLUGIN.get(plugin_key, None)
        ExceptionTool.require_true(clazz is not None,
                                   f'未注册的plugin: [{plugin_key}]，'
                                   f'可用: {sorted(cls.REGISTRY_PLUGIN.keys())}')
        return clazz

    # ================================================================ 域名

    @classmethod
    def domain_list_of(cls, site: str) -> List[str]:
        from .mc_toolkit import ExceptionTool
        ExceptionTool.require_true(site in cls.DOMAIN_LIST_DICT,
                                   f'未知站点: [{site}]，可用: {list(cls.DOMAIN_LIST_DICT.keys())}')
        return list(cls.DOMAIN_LIST_DICT[site])

    @classmethod
    def new_domain_list(cls, site: str) -> List[str]:
        domain_list = cls.domain_list_of(site)
        if cls.FLAG_SHUFFLE_DOMAIN and len(domain_list) > 1:
            shuffle(domain_list)
        return domain_list

    @classmethod
    def site_name(cls, site: str) -> str:
        return cls.SITE_NAME_DICT.get(site, site)

    # ================================================================ option 默认值

    @classmethod
    def fill_runtime_defaults(cls, option_dict: Dict) -> Dict:
        """
        把值为 None 的必需配置项填充为运行时默认值。

        为什么需要它：merge_default_dict 是「用户值覆盖默认值」，
        如果用户显式写了 `chapter: null` / `impl: null`，合并结果就是 None，
        而默认填充只作用在默认字典上，会被用户值覆盖掉，
        最终在 int(None) / 站点查找时炸掉。
        所以在合并之后再调用一次本方法。
        """
        if option_dict.get('log', None) is None:
            option_dict['log'] = cls.FLAG_ENABLE_MC_LOG

        dir_rule = option_dict.setdefault('dir_rule', {})
        if dir_rule.get('base_dir', None) is None:
            dir_rule['base_dir'] = os.getcwd()
        if not dir_rule.get('rule', None):
            dir_rule['rule'] = cls.DEFAULT_OPTION_DICT['dir_rule']['rule']
        dir_rule.setdefault('normalize_zh', None)

        client = option_dict.setdefault('client', {})
        if client.get('impl', None) is None:
            client['impl'] = McMagicConstants.SITE_TIBIU
        client.setdefault('cache', None)
        client.setdefault('domain', [])
        client.setdefault('retry_times', cls.DEFAULT_RETRY_TIMES)
        client.setdefault('username', None)
        client.setdefault('password', None)
        client.setdefault('confirm_adult', False)
        if not isinstance(client.get('cookies', None), dict):
            client['cookies'] = {}

        postman = client.setdefault('postman', {})
        meta_data = postman.setdefault('meta_data', {})
        if meta_data.get('timeout', None) is None:
            meta_data['timeout'] = cls.DEFAULT_TIMEOUT

        download = option_dict.setdefault('download', {})
        download.setdefault('cache', True)
        threading_conf = download.setdefault('threading', {})
        if threading_conf.get('chapter', None) is None:
            threading_conf['chapter'] = os.cpu_count() or 4
        if threading_conf.get('image', None) is None:
            threading_conf['image'] = 30

        plugins = option_dict.setdefault('plugins', {})
        plugins.setdefault('valid', 'log')
        plugins.setdefault('dependencies_strategy', 'failed-fast')

        return option_dict

    @classmethod
    def option_default_dict(cls) -> Dict:
        return cls.fill_runtime_defaults(deepcopy(cls.DEFAULT_OPTION_DICT))


setup_default_mc_logger()
