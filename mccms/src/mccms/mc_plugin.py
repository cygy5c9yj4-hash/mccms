"""
插件系统（对应 jmcomic 的 jm_plugin.py）。

插件通过 `plugin_key` 注册，在 option 的 `plugins.<事件名>` 下列出即可生效::

    plugins:
      after_init:
        - plugin: login
          kwargs: { username: xxx, password: yyy }
      after_comic:
        - plugin: zip
          kwargs: { zip_dir: ./zip, filename_rule: Cname }

内置事件名（由下载器在各阶段派发）：
after_init / before_comic / after_comic /
before_chapter / after_chapter / before_image / after_image

事件名就是 plugins: 下的一级 key，不做白名单校验，
你也可以自己调 option.call_all_plugin('任意名字') 派发自定义事件。

（也兼容 jmcomic 的 after_album / after_photo / before_photo 等别名，
 见 McOption.compatible_with_old_versions）

自定义插件::

    from mccms import McOptionPlugin, McModuleConfig

    class MyPlugin(McOptionPlugin):
        plugin_key = 'my_plugin'
        def invoke(self, **kwargs):
            print(kwargs)

    McModuleConfig.register_plugin(MyPlugin)
"""

import importlib.util
import os
import subprocess
import sys
import zipfile
from typing import Any, Dict, List, Optional, Tuple

from .mc_config import mc_log
from .mc_exception import ExceptionTool, PluginValidationException
from .mc_option import DirRule
from .mc_toolkit import (fix_filepath, fix_suffix, mkdir_if_not_exists,
                         of_file_name)

__all__ = [
    'McOptionPlugin',
    'LoginPlugin',
    'ZipPlugin',
    'Img2pdfPlugin',
    'LongImgPlugin',
    'DownloadCoverPlugin',
    'SkipChapterWithFewImagesPlugin',
    'ImageSuffixFilterPlugin',
    'ReplacePathStringPlugin',
    'LogTopicFilterPlugin',
    'DeleteDuplicatedFilesPlugin',
]


class McOptionPlugin:
    """所有插件的基类。"""

    # 唯一注册键
    plugin_key: str = None
    # 依赖的第三方库：'psutil' 或 ('import_name', 'pip-package-name')
    plugin_dependencies: tuple = ()

    # ------------------------------------------------------------------ 依赖

    @classmethod
    def required_dependencies_for(cls, kwargs: dict) -> tuple:
        return cls.plugin_dependencies

    @classmethod
    def parse_dependency_spec(cls, dep) -> Tuple[str, str]:
        if isinstance(dep, (tuple, list)):
            return str(dep[0]), str(dep[1])
        return str(dep), str(dep)

    @classmethod
    def check_plugin_dependency(cls, kwargs: dict, strategy: str = 'failed-fast') -> None:
        missing = []
        for dep in cls.required_dependencies_for(kwargs or {}):
            import_name, pip_name = cls.parse_dependency_spec(dep)
            if importlib.util.find_spec(import_name) is None:
                missing.append(pip_name)

        if not missing:
            return

        if strategy == 'auto-install':
            cls.install_missing_dependencies(missing)
            importlib.invalidate_caches()
            return

        msg = (f'插件[{cls.plugin_key}]缺少依赖: {missing}\n'
               f'解决方案:\n'
               f'  1. pip install {" ".join(missing)}\n'
               f'  2. 或修改 plugins.dependencies_strategy 为 auto-install\n'
               f'  3. 或修改为 ignore-only-log 仅告警')

        if strategy == 'ignore-only-log':
            mc_log(f'plugin.{cls.plugin_key}.dependency', msg)
            return

        ExceptionTool.raises(msg, {'plugin': cls.plugin_key})

    @classmethod
    def install_missing_dependencies(cls, pip_packages: List[str]) -> None:
        cmd = [sys.executable, '-m', 'pip', 'install', *pip_packages]
        mc_log(f'plugin.{cls.plugin_key}.dependency', f'正在安装依赖: {cmd}')
        result = subprocess.run(cmd, capture_output=True, text=True)
        if result.returncode != 0:
            ExceptionTool.raises(
                f'插件[{cls.plugin_key}]依赖安装失败:\n{result.stdout}\n{result.stderr}',
                {'plugin': cls.plugin_key},
            )

    # ------------------------------------------------------------------ 生命周期

    def __init__(self, option):
        self.option = option
        self.log_enable = True
        self.delete_original_file = False

    @classmethod
    def build(cls, option) -> 'McOptionPlugin':
        return cls(option)

    def invoke(self, **kwargs) -> None:
        raise NotImplementedError

    # ------------------------------------------------------------------ 工具

    def log(self, msg, topic: Optional[str] = None):
        if not self.log_enable:
            return
        full_topic = f'plugin.{self.plugin_key}' + (f'.{topic}' if topic else '')
        mc_log(full_topic, msg)

    def require_param(self, case: Any, msg: str):
        if not case:
            raise PluginValidationException(self, msg)

    def warning_lib_not_install(self, lib: str, throw: bool = False):
        msg = f'插件[{self.plugin_key}]需要库 [{lib}]，请先安装: pip install {lib}'
        if throw:
            ExceptionTool.raises(msg, {'plugin': self.plugin_key})
        self.log(msg, 'warning')

    def execute_deletion(self, paths: List[str]):
        if not self.delete_original_file:
            return
        for path in paths:
            try:
                if os.path.isdir(path):
                    import shutil
                    shutil.rmtree(path, ignore_errors=True)
                elif os.path.isfile(path):
                    os.remove(path)
            except OSError as e:  # pragma: no cover
                self.log(f'删除失败: [{path}], {e}', 'warning')

    def execute_cmd(self, cmd) -> int:
        return os.system(cmd)

    def execute_multi_line_cmd(self, cmd: str):
        return subprocess.run(cmd, shell=True, check=True)

    def enter_wait_list(self):
        if self not in self.option.need_wait_plugins:
            self.option.need_wait_plugins.append(self)

    def leave_wait_list(self):
        if self in self.option.need_wait_plugins:
            self.option.need_wait_plugins.remove(self)

    def wait_until_finish(self):
        """异步插件覆写此方法。"""

    # ------------------------------------------------------------------ 路径

    def decide_filepath(self,
                        comic,
                        chapter,
                        filename_rule: str,
                        suffix: str,
                        base_dir: Optional[str],
                        dir_rule_dict: Optional[Dict] = None) -> str:
        """
        统一的导出文件路径决策：

        1. 给了 dir_rule_dict -> 用完整规则决定路径
        2. 否则 -> base_dir + filename_rule + suffix
        """
        if comic is None and chapter is not None:
            comic = getattr(chapter, 'from_comic', None)

        if dir_rule_dict is not None:
            dir_rule = DirRule(**dir_rule_dict)
            filepath = dir_rule.apply_rule_to_path(comic, chapter)
            base_dir = os.path.dirname(filepath)
        else:
            base_dir = base_dir or os.getcwd()
            filename = DirRule.apply_rule_to_filename(comic, chapter, filename_rule)
            filepath = os.path.join(base_dir, filename + fix_suffix(suffix))

        mkdir_if_not_exists(base_dir)
        return fix_filepath(filepath)


# --------------------------------------------------------------------------------------
# 内置插件
# --------------------------------------------------------------------------------------

class LoginPlugin(McOptionPlugin):
    """
    登录插件：登录后把会话 cookie 写回 option，使后续 client 复用登录态。

    仅用于访问该账号本身有权访问的内容。
    """

    plugin_key = 'login'

    def invoke(self, username: str = None, password: str = None, **kwargs):
        self.require_param(username, 'login 插件需要 username')
        self.require_param(password, 'login 插件需要 password')

        client = self.option.build_client()
        info = client.login(username, password)
        self.option.update_cookies(client.postman.cookies)
        self.log(f'登录成功: {info.get("nichen") or username}')


class ImageSuffixFilterPlugin(McOptionPlugin):
    """只保留指定后缀的原图，其余跳过下载。"""

    plugin_key = 'image_suffix_filter'

    def invoke(self, allowed_orig_suffix: List[str] = None, **kwargs):
        self.require_param(allowed_orig_suffix, 'image_suffix_filter 需要 allowed_orig_suffix')

        allowed = {fix_suffix(s).lower() for s in allowed_orig_suffix}
        option = self.option

        def decide_download_cache(image):
            if image.img_file_suffix.lower() not in allowed:
                image.skip = True
                return False
            return option.download.cache

        option.decide_download_cache = decide_download_cache
        self.log(f'已启用后缀过滤: {sorted(allowed)}')


class ReplacePathStringPlugin(McOptionPlugin):
    """对下载路径做字符串替换。"""

    plugin_key = 'replace_path_string'

    def invoke(self, replace: Dict[str, str] = None, **kwargs):
        self.require_param(replace, 'replace_path_string 需要 replace 参数')

        option = self.option
        origin = option.decide_image_save_dir

        def decide_image_save_dir(chapter, ensure_exists=True):
            path = origin(chapter, ensure_exists=ensure_exists)
            for old, new in replace.items():
                path = path.replace(old, new)
            mkdir_if_not_exists(path)
            return path

        option.decide_image_save_dir = decide_image_save_dir
        self.log(f'已启用路径替换: {replace}')


class LogTopicFilterPlugin(McOptionPlugin):
    """只保留白名单 topic 的日志。"""

    plugin_key = 'log_topic_filter'

    def invoke(self, whitelist: List[str] = None, **kwargs):
        self.require_param(whitelist, 'log_topic_filter 需要 whitelist')

        import logging

        class TopicFilter(logging.Filter):
            def filter(self, record):
                topic = str(getattr(record, 'topic', ''))
                return any(topic.startswith(prefix) for prefix in whitelist)

        from .mc_config import mc_logger

        # 移除旧的同类 filter，避免重复叠加
        for handler in mc_logger.handlers:
            handler.filters = [f for f in handler.filters if not isinstance(f, TopicFilter)]
            handler.addFilter(TopicFilter())

        self.log(f'已启用日志过滤: {whitelist}')


class SkipChapterWithFewImagesPlugin(McOptionPlugin):
    """
    图片数少于阈值的章节直接跳过。

    放在 before_chapter 事件下使用（此时图片列表已经取好）。
    """

    plugin_key = 'skip_chapter_with_few_images'

    def invoke(self, at_least_image_count: int = 0, **kwargs):
        self.require_param(at_least_image_count and int(at_least_image_count) > 0,
                           'skip_chapter_with_few_images 需要 at_least_image_count > 0')
        threshold = int(at_least_image_count)

        chapter = kwargs.get('chapter', None)
        if chapter is None:
            return

        if len(chapter) < threshold:
            self.log(f'章节[{chapter.chapter_id}]只有 {len(chapter)} 张图（阈值 {threshold}），跳过下载')
            chapter.skip = True


class DownloadCoverPlugin(McOptionPlugin):
    """下载漫画封面。"""

    plugin_key = 'download_cover'

    def invoke(self, dir_rule: Dict = None, size: str = '', **kwargs):
        self.require_param(dir_rule, 'download_cover 需要 dir_rule')
        comic = kwargs.get('comic', None)
        downloader = kwargs.get('downloader', None)
        self.require_param(comic is not None and downloader is not None,
                           'download_cover 需要在 before_comic 事件中使用')

        save_path = self.decide_filepath(comic, None, 'Cname', '.jpg', None, dir_rule)

        if self.option.download.cache and os.path.exists(save_path):
            self.log(f'封面已存在，跳过: {save_path}')
            return

        downloader.client.download_cover(comic.comic_id, save_path, size)
        downloader.record_export_filepath(comic, save_path)
        self.log(f'封面已保存: {save_path}')


class ZipPlugin(McOptionPlugin):
    """
    打包插件：把下载好的图片压缩成 zip。

    放在 after_comic 下 -> 整本漫画打包成一个 zip
    放在 after_chapter 下 -> 每话打包成一个 zip
    """

    plugin_key = 'zip'

    @classmethod
    def required_dependencies_for(cls, kwargs: dict) -> tuple:
        encrypt = (kwargs or {}).get('encrypt', None)
        if not encrypt:
            return ()
        return ('pyzipper',)

    def invoke(self,
               zip_dir: str = './',
               filename_rule: str = 'Chindextitle',
               suffix: str = 'zip',
               dir_rule: Dict = None,
               delete_original_file: bool = False,
               encrypt: Dict = None,
               **kwargs):
        comic = kwargs.get('comic', None)
        chapter = kwargs.get('chapter', None)
        downloader = kwargs.get('downloader', None)

        self.require_param(downloader is not None, 'zip 插件需要 downloader（请放在下载事件中使用）')
        self.require_param(comic is not None or chapter is not None,
                           'zip 插件需要在 after_comic / after_chapter 事件中使用')

        self.delete_original_file = delete_original_file

        if chapter is not None and comic is None:
            comic = getattr(chapter, 'from_comic', None)

        zip_path = self.decide_filepath(comic, chapter, filename_rule, suffix, zip_dir, dir_rule)
        mkdir_if_not_exists(os.path.dirname(os.path.abspath(zip_path)))

        # 收集要打包的文件
        file_list: List[str] = []
        if chapter is not None:
            file_list = self._collect_chapter_files(downloader, comic, chapter)
        else:
            chapter_map = downloader.download_success_dict.get(comic, {})
            for ch in sorted(chapter_map.keys(), key=lambda c: getattr(c, 'index', 0)):
                file_list.extend(self._collect_chapter_files(downloader, comic, ch))

        if not file_list:
            self.log('没有可打包的文件，跳过')
            return

        password = None
        if encrypt:
            password = self._decide_password(encrypt, zip_path)

        self._write_zip(zip_path, file_list, password)

        downloader.record_export_filepath(comic if comic is not None else chapter, zip_path)
        self.log(f'打包完成: [{zip_path}]，共 {len(file_list)} 个文件')

        if self.delete_original_file:
            roots = {os.path.dirname(f) for f in file_list}
            self.execute_deletion(sorted(roots))

    @staticmethod
    def _collect_chapter_files(downloader, comic, chapter) -> List[str]:
        chapter_map = downloader.download_success_dict.get(comic, {}) if comic is not None else {}
        entries = chapter_map.get(chapter, [])
        return [path for path, _ in entries if os.path.isfile(path)]

    def _decide_password(self, encrypt: Dict, zip_path: str) -> Optional[str]:
        if 'password' in encrypt:
            return str(encrypt['password'])
        if encrypt.get('type') == 'random':
            import random
            import string
            password = ''.join(random.choices(string.ascii_letters + string.digits, k=16))
            self.log(f'随机密码: {password}', 'password')
            return password
        return None

    def _write_zip(self, zip_path: str, file_list: List[str], password: Optional[str]):
        if password:
            try:
                import pyzipper
                with pyzipper.AESZipFile(zip_path, 'w', compression=pyzipper.ZIP_DEFLATED,
                                         encryption=pyzipper.WZ_AES) as zf:
                    zf.setpassword(password.encode('utf-8'))
                    self._zip_write_files(zf, file_list)
                return
            except ImportError:
                self.log('未安装 pyzipper，改用无加密 zip', 'warning')

        with zipfile.ZipFile(zip_path, 'w', compression=zipfile.ZIP_DEFLATED) as zf:
            self._zip_write_files(zf, file_list)

    @staticmethod
    def _zip_write_files(zf, file_list: List[str]):
        # 按 (章节目录, 文件名) 组织 zip 内的目录结构
        parents = [os.path.dirname(os.path.abspath(f)) for f in file_list]
        common = os.path.commonpath(parents) if parents else ''
        for filepath in file_list:
            abspath = os.path.abspath(filepath)
            arcname = os.path.relpath(abspath, common) if common else of_file_name(abspath)
            zf.write(abspath, arcname)


class Img2pdfPlugin(McOptionPlugin):
    """把图片合并成 PDF。"""

    plugin_key = 'img2pdf'

    @classmethod
    def required_dependencies_for(cls, kwargs: dict) -> tuple:
        deps = ['img2pdf']
        if (kwargs or {}).get('encrypt'):
            deps.append('pikepdf')
        return tuple(deps)

    def invoke(self,
               pdf_dir: str = None,
               filename_rule: str = 'Chindextitle',
               dir_rule: Dict = None,
               delete_original_file: bool = False,
               encrypt: Dict = None,
               **kwargs):
        comic = kwargs.get('comic', None)
        chapter = kwargs.get('chapter', None)
        downloader = kwargs.get('downloader', None)
        self.require_param(downloader is not None, 'img2pdf 插件需要 downloader')
        self.require_param(chapter is not None or comic is not None,
                           'img2pdf 需要在 after_comic / after_chapter 事件中使用')

        self.delete_original_file = delete_original_file

        if chapter is not None and comic is None:
            comic = getattr(chapter, 'from_comic', None)

        if chapter is not None:
            self._convert_one(downloader, comic, chapter, pdf_dir, filename_rule, dir_rule, encrypt)
            return

        chapter_map = downloader.download_success_dict.get(comic, {})
        for ch in sorted(chapter_map.keys(), key=lambda c: getattr(c, 'index', 0)):
            self._convert_one(downloader, comic, ch, pdf_dir, filename_rule, dir_rule, encrypt)

    def _convert_one(self, downloader, comic, chapter, pdf_dir, filename_rule, dir_rule_dict, encrypt):
        import img2pdf

        file_list = ZipPlugin._collect_chapter_files(downloader, comic, chapter)
        if not file_list:
            return

        pdf_path = self.decide_filepath(comic, chapter, filename_rule, 'pdf', pdf_dir, dir_rule_dict)

        with open(pdf_path, 'wb') as f:
            f.write(img2pdf.convert([open(p, 'rb') for p in file_list]))

        if encrypt and encrypt.get('password'):
            try:
                import pikepdf
                with pikepdf.open(pdf_path, allow_overwriting_input=True) as pdf:
                    pdf.save(pdf_path, encryption=pikepdf.Encryption(
                        owner=str(encrypt['password']), user=str(encrypt['password'])))
            except ImportError:
                self.log('未安装 pikepdf，PDF 未加密', 'warning')

        downloader.record_export_filepath(comic if comic is not None else chapter, pdf_path)
        self.log(f'PDF 已生成: [{pdf_path}]')

        if self.delete_original_file:
            self.execute_deletion(sorted({os.path.dirname(p) for p in file_list}))


class LongImgPlugin(McOptionPlugin):
    """把章节图片纵向拼成长图。"""

    plugin_key = 'long_img'

    # (import 名, pip 包名)：import 名是 PIL，但装的是 pillow
    plugin_dependencies = (('PIL', 'pillow'),)

    def invoke(self,
               img_dir: str = None,
               filename_rule: str = 'Chindextitle',
               dir_rule: Dict = None,
               delete_original_file: bool = False,
               **kwargs):
        from PIL import Image

        comic = kwargs.get('comic', None)
        chapter = kwargs.get('chapter', None)
        downloader = kwargs.get('downloader', None)
        self.require_param(downloader is not None, 'long_img 插件需要 downloader')
        self.require_param(chapter is not None, 'long_img 目前只支持 after_chapter 事件')

        self.delete_original_file = delete_original_file
        if comic is None:
            comic = getattr(chapter, 'from_comic', None)

        file_list = ZipPlugin._collect_chapter_files(downloader, comic, chapter)
        if not file_list:
            return

        images = []
        for path in file_list:
            try:
                images.append(Image.open(path).convert('RGB'))
            except Exception as e:
                self.log(f'跳过无法读取的图片 [{path}]: {e}', 'warning')

        if not images:
            return

        target_width = min(img.width for img in images)
        resized = []
        for img in images:
            if img.width != target_width:
                height = int(img.height * target_width / img.width)
                img = img.resize((target_width, height), Image.LANCZOS)
            resized.append(img)

        total_height = sum(img.height for img in resized)
        canvas = Image.new('RGB', (target_width, total_height), 'white')

        offset = 0
        for img in resized:
            canvas.paste(img, (0, offset))
            offset += img.height

        img_path = self.decide_filepath(comic, chapter, filename_rule, 'png', img_dir, dir_rule)
        canvas.save(img_path, 'PNG')

        downloader.record_export_filepath(comic if comic is not None else chapter, img_path)
        self.log(f'长图已生成: [{img_path}]')

        if self.delete_original_file:
            self.execute_deletion(sorted({os.path.dirname(p) for p in file_list}))


class DeleteDuplicatedFilesPlugin(McOptionPlugin):
    """删除漫画目录下重复的图片（按 MD5 判断）。"""

    plugin_key = 'delete_duplicated_files'

    def invoke(self, limit: int = 2, delete_original_file: bool = True, **kwargs):
        self.require_param(limit and int(limit) >= 2, 'delete_duplicated_files 需要 limit >= 2')
        self.delete_original_file = delete_original_file

        comic = kwargs.get('comic', None)
        self.require_param(comic is not None, 'delete_duplicated_files 需要在 after_comic 事件中使用')

        import hashlib
        from collections import defaultdict

        root = self.option.dir_rule.decide_comic_root_dir(comic)
        digest_map = defaultdict(list)

        for dirpath, _, filenames in os.walk(root):
            for name in filenames:
                filepath = os.path.join(dirpath, name)
                try:
                    with open(filepath, 'rb') as f:
                        digest = hashlib.md5(f.read()).hexdigest()
                    digest_map[digest].append(filepath)
                except OSError:
                    continue

        to_delete = []
        for digest, paths in digest_map.items():
            if len(paths) >= int(limit):
                to_delete.extend(sorted(paths)[1:])

        if not to_delete:
            self.log('没有发现重复文件')
            return

        self.log(f'发现 {len(to_delete)} 个重复文件')
        for path in to_delete:
            self.log(f'  - {path}')
        self.execute_deletion(to_delete)
