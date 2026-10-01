"""
下载选项（对应 jmcomic 的 jm_option.py）。

包含：
- DirRule：路径规则 DSL（Bd_Cname_Chindextitle 这类写法）
- McOption：从 yml / dict 构造的完整配置对象

配置结构::

    log: true
    dir_rule:
      base_dir: ./downloads
      rule: Bd_Cname_Chindextitle
      normalize_zh: null
    download:
      cache: true
      image: { decode: true, suffix: null }
      threading: { image: 30, chapter: 8 }
    client:
      impl: tibiu            # tibiu | manhwa
      retry_times: 3
      domain: []
      username: null         # 填了就会自动登录（用于访问自己有权访问的内容）
      password: null
      cookies: {}
      postman:
        meta_data: { timeout: 20, proxies: null, headers: null }
    plugins:
      after_init: []
      after_comic: []
"""

import os
from copy import deepcopy
from typing import Dict, List, Optional, Tuple

from .mc_config import McModuleConfig, mc_log
from .mc_entity import DetailEntity, McChapterDetail, McComicDetail
from .mc_exception import ExceptionTool, PluginValidationException
from .mc_postman import McPostman
from .mc_toolkit import (AdvancedDict, MccmsText, field_cache, fix_filepath,
                         fix_windir_name)

__all__ = ['DirRule', 'McOption']


class DirRule:
    """
    路径规则 DSL。

    规则由 `_` 或 `/` 分隔，支持三类 segment：

    1. ``Bd``                    —— base_dir
    2. ``Cxxx`` / ``Chxxx``      —— 漫画/章节实体的字段（如 Cname、Chindex）
    3. ``{Cid}_{Chindex:03}``    —— Python f-string 风格

    示例::

        Bd_Cname_Chindextitle
        Bd/{Cauthor}/{Cname}/第{Chindex:03}话
        Bd_Chname
    """

    RULE_BASE_DIR = 'Bd'

    # 前缀必须长前缀优先匹配（Ch 先于 C）
    PREFIX_COMIC = 'C'
    PREFIX_CHAPTER = 'Ch'
    PREFIX_LIST = (PREFIX_CHAPTER, PREFIX_COMIC)

    def __init__(self, rule: str, base_dir: Optional[str] = None, normalize_zh: Optional[str] = None):
        ExceptionTool.require_true(isinstance(rule, str) and rule.strip() != '',
                                   f'dir_rule.rule 不能为空: [{rule}]')
        self.base_dir = MccmsText.parse_to_abspath(base_dir)
        self.rule_dsl = rule
        self.normalize_zh = normalize_zh
        self.parser_list: List[Tuple[str, object]] = self.get_rule_parser_list(rule)

    # ------------------------------------------------------------------ 切分

    def split_rule_dsl(self, rule_dsl: str) -> List[str]:
        if '/' in rule_dsl:
            rule_list = rule_dsl.split('/')
        elif '_' in rule_dsl:
            rule_list = rule_dsl.split('_')
        else:
            rule_list = [rule_dsl]

        rule_list = [e.strip() for e in rule_list if e.strip() != '']

        if not rule_list or rule_list[0] != self.RULE_BASE_DIR:
            rule_list.insert(0, self.RULE_BASE_DIR)

        return rule_list

    def get_rule_parser_list(self, rule_dsl: str) -> List[Tuple[str, object]]:
        return [(rule, self.get_rule_parser(rule)) for rule in self.split_rule_dsl(rule_dsl)]

    def get_rule_parser(self, rule: str):
        if rule == self.RULE_BASE_DIR:
            return self.parse_bd_rule
        if '{' in rule:
            return self.parse_f_string_rule
        if rule.startswith(self.PREFIX_LIST):
            return self.parse_detail_rule
        return self.parse_f_string_rule

    # ------------------------------------------------------------------ 解析器

    def parse_bd_rule(self, comic, chapter, rule: str) -> str:
        return self.base_dir

    @classmethod
    def parse_f_string_rule(cls, comic, chapter, rule: str) -> str:
        properties: Dict = {}
        if comic is not None:
            properties.update(comic.get_properties_dict())
        if chapter is not None:
            properties.update(chapter.get_properties_dict())

        try:
            return rule.format(**properties)
        except KeyError as e:
            ExceptionTool.raises(
                f'f-string 规则 [{rule}] 引用了不存在的字段 {e}。\n'
                f'漫画字段以 C 开头（如 Cid/Cname/Cauthor），章节字段以 Ch 开头'
                f'（如 Chid/Chname/Chindextitle）；\n'
                f'可用字段: {sorted(properties.keys())}',
                {'rule': rule},
            )

    @classmethod
    def is_chapter_only_rule(cls, rule: str) -> bool:
        """
        判断一个 dir_rule 片段是否只依赖章节字段。

        用于 decide_comic_root_dir()：此时没有 chapter 上下文，
        必须跳过纯章节片段，否则会 KeyError / 缺少上下文。
        """
        import re

        if rule.startswith(cls.PREFIX_CHAPTER):
            return True

        if '{' in rule:
            keys = re.findall(r'\{([A-Za-z_][A-Za-z0-9_]*)', rule)
            return bool(keys) and all(k.startswith(cls.PREFIX_CHAPTER) for k in keys)

        return False

    @classmethod
    def parse_detail_rule(cls, comic, chapter, rule: str) -> str:
        for prefix in cls.PREFIX_LIST:
            if not rule.startswith(prefix):
                continue

            ref = rule[len(prefix):]
            detail = chapter if prefix == cls.PREFIX_CHAPTER else comic

            if detail is None:
                # 章节片段缺 chapter，或漫画片段缺 comic：
                # 后者常见于「只给 chapter_id、没给 comic_id」地单独下载章节
                ExceptionTool.raises(
                    f'dir_rule 片段 [{rule}] 缺少'
                    f'{"章节" if prefix == cls.PREFIX_CHAPTER else "漫画"}上下文。\n'
                    f'单独下载章节时请提供 comic_id，'
                    f'或把 dir_rule 改成只依赖章节的规则（例如 Bd_Chindextitle）',
                    {'rule': rule},
                )

            return DetailEntity.get_dirname(detail, ref)

        ExceptionTool.raises(
            f'无法解析的 dir_rule 片段: [{rule}]，'
            f'可用前缀: {list(cls.PREFIX_LIST)}（漫画字段用 C，章节字段用 Ch），'
            f'或使用 f-string 写法如 {{Cid}}_{{Chindex:03}}'
        )

    # ------------------------------------------------------------------ 路径

    def apply_rule_to_path(self, comic, chapter, only_comic_rules: bool = False) -> str:
        path_list = []

        for rule, parser in self.parser_list:
            # 只按漫画规则建目录时，跳过所有纯章节级片段
            # （既包括 Chxxx，也包括 {Chxxx} 这种 f-string 写法）
            if only_comic_rules and self.is_chapter_only_rule(rule):
                continue

            try:
                path = parser(comic, chapter, rule)
            except Exception as e:
                mc_log('dir_rule', f'路径规则"{rule}"解析出错: {e}', e)
                raise

            if rule != self.RULE_BASE_DIR:
                path = MccmsText.to_zh(str(path), self.normalize_zh)
                path = fix_windir_name(path).strip()

            path_list.append(str(path))

        return fix_filepath('/'.join(path_list))

    def decide_image_save_dir(self, comic, chapter) -> str:
        """章节图片的保存目录。"""
        save_dir = self.apply_rule_to_path(comic, chapter)
        return MccmsText.try_mkdir(save_dir)

    def decide_comic_root_dir(self, comic) -> str:
        """漫画根目录（只用 Bd 与 Cxx）。"""
        return self.apply_rule_to_path(comic, None, only_comic_rules=True)

    @classmethod
    def apply_rule_to_filename(cls, comic, chapter, rule: str) -> str:
        """给插件用：只生成单段文件名。"""
        if comic is None and chapter is not None:
            comic = getattr(chapter, 'from_comic', None)

        parser = cls.get_rule_parser_static(rule)
        return fix_windir_name(str(parser(comic, chapter, rule))).strip()

    @classmethod
    def get_rule_parser_static(cls, rule: str):
        if rule == cls.RULE_BASE_DIR:
            ExceptionTool.raises(f'文件名规则不支持 Bd: [{rule}]')
        if '{' in rule:
            return cls.parse_f_string_rule
        if rule.startswith(cls.PREFIX_LIST):
            return cls.parse_detail_rule
        return cls.parse_f_string_rule

    def __str__(self):
        return f'DirRule(base_dir={self.base_dir}, rule={self.rule_dsl})'

    __repr__ = __str__


class McOption:

    def __init__(self,
                 dir_rule: Dict,
                 download: Dict,
                 client: Dict,
                 plugins: Dict,
                 filepath=None,
                 call_after_init_plugin=True,
                 ):
        self.dir_rule = DirRule(**dir_rule)
        self.client = AdvancedDict(client)
        self.download = AdvancedDict(download)
        self.plugins = AdvancedDict(plugins)
        self.filepath = filepath
        self.need_wait_plugins = []

        self.check_plugins_dependencies()

        if call_after_init_plugin:
            self.call_all_plugin('after_init', safe=True)

    # ================================================================== 构造

    @classmethod
    def default_dict(cls) -> Dict:
        return McModuleConfig.option_default_dict()

    @classmethod
    def default(cls) -> 'McOption':
        return cls.construct({})

    @classmethod
    def construct(cls,
                  origdic: Dict,
                  cover_default: bool = True,
                  call_after_init_plugin: bool = True,
                  ) -> 'McOption':
        dic = deepcopy(origdic or {})

        # 旧版本兼容必须发生在合并默认值之前，
        # 否则默认值会把用户的旧 key 掩盖掉，迁移逻辑失效。
        cls.compatible_with_old_versions(dic)

        if cover_default:
            dic = cls.merge_default_dict(dic)
        else:
            # 不补默认值时，四个配置段都必须由调用方提供
            for key in ('dir_rule', 'download', 'client', 'plugins'):
                ExceptionTool.require_true(
                    key in dic,
                    f'option 缺少配置段: [{key}]（cover_default=False 时四个配置段都必须提供）',
                )

        # 用户显式写 null 时，合并结果会是 None，这里再兜一次底
        McModuleConfig.fill_runtime_defaults(dic)

        log = dic.pop('log', True)
        if log is False or log == 'false':
            from .mc_config import disable_mc_log
            disable_mc_log()
        elif log == 'pretty':
            from .mc_config import enable_pretty_log
            enable_pretty_log()

        dic.pop('version', None)

        for key in ('dir_rule', 'download', 'client', 'plugins'):
            ExceptionTool.require_true(key in dic, f'option 缺少配置段: [{key}]')

        return cls(**dic, call_after_init_plugin=call_after_init_plugin)

    @classmethod
    def from_file(cls, filepath: str) -> 'McOption':
        from .mc_toolkit import PackerUtil
        dic, _ = PackerUtil.unpack(filepath)
        dic = dic or {}
        dic.setdefault('filepath', filepath)
        return cls.construct(dic)

    def to_file(self, filepath: Optional[str] = None):
        from .mc_toolkit import PackerUtil
        filepath = filepath or self.filepath
        ExceptionTool.require_true(filepath is not None, '未指定 filepath，无法保存 option')
        return PackerUtil.pack(self.deconstruct(), filepath)

    def deconstruct(self) -> Dict:
        return {
            'version': McModuleConfig.DEFAULT_OPTION_VER,
            'log': McModuleConfig.FLAG_ENABLE_MC_LOG,
            'dir_rule': {
                'rule': self.dir_rule.rule_dsl,
                'base_dir': self.dir_rule.base_dir,
                'normalize_zh': self.dir_rule.normalize_zh,
            },
            'download': self.download.src_dict,
            'client': self.client.src_dict,
            'plugins': self.plugins.src_dict,
        }

    def copy_option(self) -> 'McOption':
        """复制一份配置（不会重复触发 after_init 插件）。"""
        dic = self.deconstruct()
        dic.pop('log', None)
        dic.pop('version', None)
        return self.construct(dic, cover_default=False, call_after_init_plugin=False)

    @classmethod
    def merge_default_dict(cls, user_dict: Dict, default_dict: Optional[Dict] = None) -> Dict:
        default_dict = cls.default_dict() if default_dict is None else deepcopy(default_dict)

        def merge(default: Dict, user: Dict) -> Dict:
            result = dict(default)
            for key, value in (user or {}).items():
                if key in result and isinstance(result[key], dict) and isinstance(value, dict):
                    result[key] = merge(result[key], value)
                else:
                    result[key] = value
            return result

        return merge(default_dict, user_dict or {})

    @classmethod
    def compatible_with_old_versions(cls, dic: Dict):
        """jmcomic 风格的旧配置迁移。"""
        download = dic.get('download') or {}
        threading_conf = download.get('threading') or {}
        if 'batch_count' in threading_conf and 'image' not in threading_conf:
            threading_conf['image'] = threading_conf.pop('batch_count')

        if 'plugin' in dic and 'plugins' not in dic:
            dic['plugins'] = dic.pop('plugin')

        plugins = dic.get('plugins') or {}
        # jmcomic 的事件名 -> 本库事件名
        alias = {
            'before_album': 'before_comic',
            'after_album': 'after_comic',
            'before_photo': 'before_chapter',
            'after_photo': 'after_chapter',
        }
        for old, new in alias.items():
            if old in plugins and new not in plugins:
                plugins[new] = plugins.pop(old)

    # ================================================================== 客户端

    @field_cache()
    def build_client(self, **kwargs):
        return self.new_client(**kwargs)

    # jmcomic 命名兼容
    build_jm_client = build_client

    def new_client(self,
                   domain_list=None,
                   impl=None,
                   cache=None,
                   **kwargs):
        postman_conf = deepcopy(self.client.postman.src_dict)
        meta_data = postman_conf.setdefault('meta_data', {})

        # postman.type 决定底层用 requests 还是 curl_cffi，透传给 postman
        if postman_conf.get('type', None) and 'type' not in meta_data:
            meta_data['type'] = postman_conf['type']

        # option 顶层的 cookies / username / password 也允许直接写
        cookies = self.client.get('cookies', None)
        if cookies:
            meta_data['cookies'] = {**(meta_data.get('cookies') or {}), **cookies}

        if kwargs:
            meta_data.update(kwargs)

        impl = impl or self.client.impl
        if isinstance(impl, type):
            impl = impl.client_key

        domain_conf = self.client.get('domain', None)
        if domain_list is None:
            domain_list = self.decide_client_domain(impl, domain_conf)

        retry_times = self.client.get('retry_times', McModuleConfig.DEFAULT_RETRY_TIMES)

        postman = McPostman(
            site=impl,
            meta_data=meta_data,
            domain_list=domain_list,
            retry_times=retry_times,
        )

        clazz = McModuleConfig.client_impl_class(impl)
        client = clazz(
            postman=postman,
            domain_list=domain_list,
            retry_times=retry_times,
            username=self.client.get('username', None),
            password=self.client.get('password', None),
            confirm_adult=bool(self.client.get('confirm_adult', False)),
        )
        return client

    def decide_client_domain(self, impl: str, domain_conf) -> List[str]:
        if isinstance(domain_conf, dict):
            domain_conf = domain_conf.get(impl, None)
        if isinstance(domain_conf, str):
            domain_conf = [line.strip() for line in domain_conf.splitlines() if line.strip()]
        if domain_conf:
            return list(domain_conf)
        return McModuleConfig.new_domain_list(impl)

    def update_cookies(self, cookies: Dict[str, str]):
        """把 cookies 合并进配置，后续新建的 client 都会带上。"""
        client_conf = self.client
        postman_conf = client_conf.get('postman')
        meta_data = postman_conf.setdefault('meta_data', {})
        meta_data['cookies'] = {**(meta_data.get('cookies') or {}), **(cookies or {})}

    # ================================================================== 下载决策

    def decide_image_batch_count(self, chapter: McChapterDetail) -> int:
        return int(self.download.threading.image)

    def decide_chapter_batch_count(self, comic: McComicDetail) -> int:
        return int(self.download.threading.chapter)

    # jmcomic 命名兼容
    decide_photo_batch_count = decide_chapter_batch_count

    def decide_image_filename(self, image) -> str:
        return image.filename_without_suffix

    def decide_image_suffix(self, image) -> str:
        if image.is_gif:
            return image.img_file_suffix

        suffix = self.download.image.get('suffix', None)
        return suffix or image.img_file_suffix

    def decide_image_save_dir(self, chapter: McChapterDetail, ensure_exists: bool = True) -> str:
        comic = getattr(chapter, 'from_comic', None)
        save_dir = self.dir_rule.apply_rule_to_path(comic, chapter)
        if ensure_exists:
            return MccmsText.try_mkdir(save_dir)
        return save_dir

    def decide_image_filepath(self, image, consider_custom_suffix: bool = True) -> str:
        save_dir = self.decide_image_save_dir(image.from_chapter)
        filename = fix_windir_name(self.decide_image_filename(image))
        suffix = self.decide_image_suffix(image) if consider_custom_suffix else image.img_file_suffix
        return os.path.join(save_dir, filename + suffix)

    def decide_download_cache(self, image) -> bool:
        return bool(self.download.cache)

    def decide_download_image_decode(self, image) -> bool:
        if image.is_gif:
            return False
        return bool(self.download.image.decode)

    # ================================================================== 插件

    def call_all_plugin(self, group: str, safe: Optional[bool] = None, **extra):
        plugin_list = self.plugins.get(group, [])
        if not isinstance(plugin_list, list) or len(plugin_list) == 0:
            return

        for pinfo in plugin_list:
            # 注意：AdvancedDict 会把嵌套 dict 包装成 AdvancedDict，
            # 所以这里不能只判断 isinstance(pinfo, dict)
            if not isinstance(pinfo, (dict, AdvancedDict)):
                continue

            key = pinfo.get('plugin', None)
            ExceptionTool.require_true(key is not None, f'[{group}] 插件配置缺少 plugin 字段: {pinfo}')

            pclass = McModuleConfig.REGISTRY_PLUGIN.get(key, None)
            ExceptionTool.require_true(
                pclass is not None,
                f'[{group}] 未注册的 plugin: [{key}]，可用: {sorted(McModuleConfig.REGISTRY_PLUGIN.keys())}',
            )

            try:
                self.invoke_plugin(pclass, pinfo.get('kwargs', None), extra, pinfo)
            except PluginValidationException as e:
                # 参数校验失败走 valid 策略：ignore（静默）/ log（默认）/ raise
                self.handle_plugin_valid_exception(e, group, pinfo)
            except BaseException as e:
                if safe is True or pinfo.get('safe', True):
                    # 被吞掉的插件异常只记一行，不打印整段 traceback，避免刷屏；
                    # 需要完整堆栈时把该插件的 safe 设为 false。
                    mc_log('plugin.exception', f'插件[{key}]执行失败: {e}')
                else:
                    raise

    def handle_plugin_valid_exception(self, e, group: str, pinfo):
        policy = pinfo.get('valid', None) or self.plugins.get('valid', 'log')
        if policy == 'ignore':
            return
        if policy == 'raise':
            raise e
        mc_log('plugin.validation', f'[{group}] {e}')

    def invoke_plugin(self, pclass, kwargs, extra, pinfo):
        kwargs = self.fix_kwargs(kwargs)

        if extra:
            kwargs.update(extra)

        plugin = pclass.build(self)
        if not pinfo.get('log', True):
            plugin.log_enable = False

        mc_log('plugin.invoke', f'调用插件: [{pclass.plugin_key}]')
        plugin.invoke(**kwargs)

    @staticmethod
    def fix_kwargs(kwargs) -> Dict:
        if not kwargs:
            return {}
        result = {}
        for key, value in kwargs.items():
            if isinstance(value, str):
                value = MccmsText.parse_dsl_text(value)
            result[str(key)] = value
        return result

    def check_plugins_dependencies(self):
        strategy = self.plugins.get('dependencies_strategy', 'failed-fast')

        for group, plugin_list in self.plugins.items():
            if not isinstance(plugin_list, list):
                continue

            for pinfo in plugin_list:
                if not isinstance(pinfo, (dict, AdvancedDict)):
                    continue
                key = pinfo.get('plugin', None)
                pclass = McModuleConfig.REGISTRY_PLUGIN.get(key, None)
                if pclass is None:
                    continue
                if strategy == 'ignore-only-log':
                    try:
                        pclass.check_plugin_dependency(dict(pinfo.get('kwargs') or {}), strategy=strategy)
                    except Exception as e:
                        mc_log('plugin.dependency', f'插件[{key}]依赖检查失败: {e}')
                else:
                    # failed-fast / auto-install：依赖问题必须在此刻暴露，
                    # 否则会拖到插件真正执行时才报错（还可能被 safe 吞掉）
                    pclass.check_plugin_dependency(dict(pinfo.get('kwargs') or {}), strategy=strategy)

    def wait_all_plugins_finish(self):
        for plugin in self.need_wait_plugins:
            plugin.wait_until_finish()

    # ================================================================== 下载糖

    def download_comic(self, comic_id, downloader=None, **kwargs):
        from .api import download_comic
        return download_comic(comic_id, self, downloader, **kwargs)

    def download_chapter(self, chapter_id, downloader=None, **kwargs):
        from .api import download_chapter
        return download_chapter(chapter_id, self, downloader, **kwargs)

    def download_comics(self, comic_id_iter, downloader=None, **kwargs):
        from .api import download_comic
        return download_comic(comic_id_iter, self, downloader, **kwargs)

    # jmcomic 命名兼容
    download_album = download_comic
    download_photo = download_chapter

    # ================================================================== 账号

    def login(self, username: Optional[str] = None, password: Optional[str] = None):
        """
        使用账号登录并把会话 cookie 写回 option，使后续新建的 client 复用登录态。

        仅用于访问当前账号本身有权访问的内容。
        """
        username = username or self.client.get('username', None)
        password = password or self.client.get('password', None)
        ExceptionTool.require_true(bool(username) and bool(password),
                                   '未配置账号密码，无法登录（请在 option.client 中配置 username/password）')

        client = self.build_client()
        info = client.login(username, password)
        self.update_cookies(client.postman.cookies)
        return info

    def __str__(self):
        return (f'McOption(impl={self.client.impl}, '
                f'rule={self.dir_rule.rule_dsl}, base_dir={self.dir_rule.base_dir})')

    __repr__ = __str__
