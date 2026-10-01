"""
通用工具（对应 jmcomic 里由 common 包提供的那部分能力）。

包含：AdvancedDict / PackerUtil / 多线程启动器 / field_cache /
文件名与路径净化 / MccmsText 文本工具。
"""

import json
import os
import re
import threading
from copy import deepcopy
from functools import wraps
from typing import Callable, Dict, Iterable, List, Optional

from .mc_config import mc_log
from .mc_exception import ExceptionTool

__all__ = [
    'ExceptionTool',
    'AdvancedDict',
    'PackerUtil',
    'field_cache',
    'multi_thread_launcher',
    'thread_pool_executor',
    'fix_windir_name',
    'fix_filepath',
    'fix_suffix',
    'mkdir_if_not_exists',
    'file_exists',
    'file_not_exists',
    'files_of_dir',
    'of_file_name',
    'MccmsText',
    'VAR_FILE_NAME_LENGTH_LIMIT',
]

VAR_FILE_NAME_LENGTH_LIMIT = 100


# --------------------------------------------------------------------------------------
# AdvancedDict
# --------------------------------------------------------------------------------------

class AdvancedDict:
    """
    支持属性访问的 dict：`d.a.b.c` 等价于 `d['a']['b']['c']`。

    与 jmcomic 使用的 common.AdvancedDict 语义保持一致：
    - 缺失的 key 抛 KeyError（而不是 AttributeError），便于尽早暴露配置错误
    - 嵌套 dict / list 中的 dict 会被自动包装
    """

    def __init__(self, data: Optional[Dict] = None):
        object.__setattr__(self, '_data', data if data is not None else {})

    # -------------------------------------------------------------- 包装

    @classmethod
    def wrap_value(cls, value):
        if isinstance(value, AdvancedDict):
            return value
        if isinstance(value, dict):
            return cls(value)
        if isinstance(value, list):
            return [cls.wrap_value(v) for v in value]
        if isinstance(value, tuple):
            return tuple(cls.wrap_value(v) for v in value)
        return value

    @classmethod
    def wrap(cls, data) -> 'AdvancedDict':
        if isinstance(data, AdvancedDict):
            return data
        return cls(data if isinstance(data, dict) else {})

    # -------------------------------------------------------------- 访问

    def __getattr__(self, item):
        try:
            return self.wrap_value(self._data[item])
        except KeyError:
            raise KeyError(f'配置项缺失: [{item}]，请检查 option 配置是否完整') from None

    def __setattr__(self, key, value):
        if key == '_data':
            object.__setattr__(self, key, value)
        else:
            self._data[key] = value

    def __getitem__(self, item):
        return self.wrap_value(self._data[item])

    def __setitem__(self, key, value):
        self._data[key] = value

    def __contains__(self, item):
        return item in self._data

    def __iter__(self):
        return iter(self._data)

    def __len__(self):
        return len(self._data)

    def keys(self):
        return self._data.keys()

    def values(self):
        return (self.wrap_value(v) for v in self._data.values())

    def items(self):
        return ((k, self.wrap_value(v)) for k, v in self._data.items())

    def get(self, key, default=None):
        if key not in self._data:
            return default
        return self.wrap_value(self._data[key])

    def setdefault(self, key, default=None):
        if key not in self._data:
            self._data[key] = default
        return self.wrap_value(self._data[key])

    def update(self, other):
        data = other.src_dict if isinstance(other, AdvancedDict) else other
        self._data.update(data)

    @property
    def src_dict(self) -> Dict:
        """取回原始 dict（深拷贝）。"""
        return deepcopy(self._data)

    def __repr__(self):
        return f'AdvancedDict({self._data!r})'


# --------------------------------------------------------------------------------------
# 序列化
# --------------------------------------------------------------------------------------

class PackerUtil:
    mode_yml = 'yml'
    mode_json = 'json'

    @classmethod
    def unpack(cls, filepath: str, mode: Optional[str] = None):
        with open(filepath, 'r', encoding='utf-8') as f:
            text = f.read()
        mode = mode or cls.guess_mode(filepath)
        return cls.unpack_by_str(text, mode)

    @classmethod
    def unpack_by_str(cls, text: str, mode: str):
        if mode == cls.mode_yml:
            import yaml
            return yaml.safe_load(text), mode
        if mode == cls.mode_json:
            return json.loads(text), mode
        ExceptionTool.raises(f'不支持的序列化格式: [{mode}]')

    @classmethod
    def pack(cls, obj, filepath: str = None, mode: Optional[str] = None):
        mode = mode or cls.guess_mode(filepath)
        if mode == cls.mode_yml:
            import yaml
            text = yaml.safe_dump(obj, allow_unicode=True, sort_keys=False, default_flow_style=False)
        elif mode == cls.mode_json:
            text = json.dumps(obj, ensure_ascii=False, indent=2)
        else:
            ExceptionTool.raises(f'不支持的序列化格式: [{mode}]')

        if filepath is None:
            return text

        mkdir_if_not_exists(os.path.dirname(os.path.abspath(filepath)))
        with open(filepath, 'w', encoding='utf-8') as f:
            f.write(text)
        return filepath

    @classmethod
    def guess_mode(cls, filepath: Optional[str]) -> str:
        if filepath is not None and str(filepath).endswith('.json'):
            return cls.mode_json
        return cls.mode_yml


# --------------------------------------------------------------------------------------
# 并发
# --------------------------------------------------------------------------------------

def _safe_apply(func: Callable, obj):
    """
    并发任务的统一包装。

    下载失败的收集由 McDownloader.catch_exception 负责，
    这里只保证「一个任务失败不会打断其它任务」，并把异常记进日志，
    避免线程默认 excepthook 打印裸 traceback 干扰输出。
    """
    try:
        return func(obj)
    except BaseException as e:
        mc_log('thread.error', f'并发任务执行失败: {e}', e)
        return None


def multi_thread_launcher(iter_objs: Iterable,
                          apply_each_obj_func: Callable,
                          wait_finish=True,
                          ):
    """一个对象一个线程。"""
    threads = []
    for obj in iter_objs:
        t = threading.Thread(target=_safe_apply, args=(apply_each_obj_func, obj))
        t.start()
        threads.append(t)

    if wait_finish:
        for t in threads:
            t.join()

    return threads


def thread_pool_executor(iter_objs: Iterable,
                         apply_each_obj_func: Callable,
                         max_workers: Optional[int] = None,
                         wait_finish=True,
                         ):
    """线程池并发。"""
    from concurrent.futures import ThreadPoolExecutor

    if max_workers is None:
        max_workers = min(32, (os.cpu_count() or 4) + 4)
    max_workers = max(1, int(max_workers))

    futures = []
    with ThreadPoolExecutor(max_workers=max_workers, thread_name_prefix='mccms') as executor:
        for obj in iter_objs:
            futures.append(executor.submit(_safe_apply, apply_each_obj_func, obj))
        if wait_finish:
            for f in futures:
                f.result()

    return futures


def field_cache(field_name: Optional[str] = None, sentinel=None):
    """
    把方法首次调用的结果缓存到实例属性上（对应 common.field_cache）。

    用法::

        @field_cache()
        def build_client(self): ...
    """

    def decorator(func):
        name = field_name or f'__cache_{func.__name__}'

        @wraps(func)
        def wrapper(self, *args, **kwargs):
            if kwargs:
                return func(self, *args, **kwargs)

            cached = getattr(self, name, sentinel)
            if cached is not sentinel:
                return cached

            result = func(self, *args, **kwargs)
            setattr(self, name, result)
            return result

        wrapper.__cache_field__ = name
        return wrapper

    return decorator


# --------------------------------------------------------------------------------------
# 文件 / 路径
# --------------------------------------------------------------------------------------

_WIN_FORBID_CHAR = set('\\/:*?"<>|\n\t\r')


def fix_windir_name(name: str, replace_char='_') -> str:
    """把 Windows/Unix 下非法的文件名与目录名字符替换掉。"""
    text = ''.join(replace_char if c in _WIN_FORBID_CHAR else c for c in str(name))
    text = text.strip()
    while text.endswith('.'):
        text = text[:-1]
    return text


def fix_filepath(filepath: str) -> str:
    """统一路径分隔符并折叠多余斜杠（保留 URL 的 '://'）。"""
    filepath = str(filepath).replace('\\', '/')
    filepath = re.sub(r'(?<!:)//+', '/', filepath)
    if os.path.isdir(filepath) and not filepath.endswith('/'):
        return filepath + '/'
    return filepath


def fix_suffix(suffix: str) -> str:
    suffix = str(suffix)
    if suffix == '':
        return suffix
    return suffix if suffix.startswith('.') else f'.{suffix}'


def mkdir_if_not_exists(dirpath: str):
    if dirpath and not os.path.exists(dirpath):
        os.makedirs(dirpath, exist_ok=True)


def file_exists(filepath: str) -> bool:
    return os.path.isfile(filepath)


def file_not_exists(filepath: str) -> bool:
    return not file_exists(filepath)


def files_of_dir(abs_dir_path: str) -> List[str]:
    if not os.path.isdir(abs_dir_path):
        return []
    result = []
    for name in sorted(os.listdir(abs_dir_path)):
        path = os.path.join(abs_dir_path, name)
        if os.path.isdir(path):
            result.append(path + '/')
        else:
            result.append(path)
    return result


def of_file_name(filepath: str, trim_suffix=False) -> str:
    name = os.path.basename(str(filepath))
    if trim_suffix:
        index = name.rfind('.')
        if index > 0:
            name = name[:index]
    return name


# --------------------------------------------------------------------------------------
# 文本
# --------------------------------------------------------------------------------------

_DSL_REPLACER = []


class MccmsText:

    @classmethod
    def parse_dsl_text(cls, dsl_text: str) -> str:
        """展开 ${ENV_VAR}。"""
        text = str(dsl_text)
        for pattern, replacer in _DSL_REPLACER:
            text = pattern.sub(replacer, text)
        return text

    @classmethod
    def parse_to_abspath(cls, dsl_text: Optional[str]) -> str:
        if dsl_text is None:
            return os.getcwd()
        return os.path.abspath(cls.parse_dsl_text(dsl_text))

    @classmethod
    def match_os_env(cls, match) -> str:
        name = match.group(1)
        value = os.environ.get(name, None)
        ExceptionTool.require_true(value is not None, f'未配置环境变量: [{name}]')
        return value

    @classmethod
    def to_zh(cls, text: str, target: Optional[str] = None) -> str:
        """繁简体转换，需要可选依赖 zhconv；未安装时原样返回。"""
        if not target:
            return text
        try:
            import zhconv
            return zhconv.convert(text, target)
        except ImportError:
            return text
        except Exception as e:  # pragma: no cover
            mc_log('zhconv.error', f'繁简体转换失败: {e}', e)
            return text

    @classmethod
    def try_mkdir(cls, save_dir: str) -> str:
        """创建目录，容忍超长路径（Errno 36）。"""
        try:
            mkdir_if_not_exists(save_dir)
            return save_dir
        except OSError as e:
            if e.errno == 36:
                shorten = save_dir[:VAR_FILE_NAME_LENGTH_LIMIT]
                mc_log('mkdir', f'路径过长，截断为: {shorten}')
                mkdir_if_not_exists(shorten)
                return shorten
            raise

    @classmethod
    def limit_text(cls, text: str, limit: int) -> str:
        text = str(text)
        if len(text) <= limit:
            return text
        return text[:limit] + f'...({len(text) - limit} more)'

    @classmethod
    def unescape(cls, text) -> str:
        """解码站点 JSON 里残留的 HTML 实体（如 &#40; &#039; &amp;）。"""
        if text is None:
            return ''
        import html as html_module
        return html_module.unescape(str(text)).strip()

    # ------------------------------------------------------------------ 名称

    _BRACKET_PAIRS = {'(': ')', '[': ']', '【': '】', '（': '）', '《': '》'}

    @classmethod
    def tokenize(cls, title: str) -> List[str]:
        """
        按成对括号切分名称（只在括号处切分，不在空格处切分）。

        '繞道#2 [暴碧漢化組] よりみち#2 (COMIC 快樂天)' ->
        ['繞道#2', '[暴碧漢化組]', 'よりみち#2', '(COMIC 快樂天)']

        '喂我吃吧 老師! [漢化組] [DL版]' ->
        ['喂我吃吧 老師!', '[漢化組]', '[DL版]']
        """
        text = str(title)
        tokens: List[str] = []
        buffer = ''
        closing: Optional[str] = None

        for char in text:
            if closing is None:
                if char in cls._BRACKET_PAIRS:
                    if buffer.strip():
                        tokens.append(buffer.strip())
                    buffer = char
                    closing = cls._BRACKET_PAIRS[char]
                else:
                    buffer += char
            else:
                buffer += char
                if char == closing:
                    tokens.append(buffer.strip())
                    buffer = ''
                    closing = None

        if buffer.strip():
            tokens.append(buffer.strip())

        return tokens

    @classmethod
    def parse_orig_name(cls, title: str, default=None):
        """
        提取原始名称：第一个不以括号开头的词。

        例: '喂我吃吧 老師! [漢化組] [DL版]' -> '喂我吃吧 老師!'
        """
        for token in cls.tokenize(title):
            if token and token[0] not in cls._BRACKET_PAIRS:
                return token
        return default

    # ------------------------------------------------------------------ id 解析

    @classmethod
    def parse_to_mc_id(cls, text) -> str:
        """
        从 id / URL / 混合文本中提取数字资源 id。

        支持::

            17001
            /comic/17001
            https://cache.tibiu.net/comic/17001
            /chapter/17001/291931
            https://www.manhwa.wang/index.php/chapter/936939
        """
        if isinstance(text, int):
            return str(text)

        text = str(text).strip()
        if text.isdigit():
            return text

        patterns = [
            r'/chapter/\d+/(\d+)',          # TIBIU: /chapter/{comic}/{chapter}
            r'/chapter/(\d+)',              # manhwa: /index.php/chapter/{id}
            r'/comic/(\d+)',
            r'/comic/[^/?#]+?[/-](\d+)',
            r'[?&](?:id|mid|cid)=(\d+)',
            r'(\d{2,})',
        ]
        for pattern in patterns:
            match = re.search(pattern, text)
            if match:
                return match.group(1)

        ExceptionTool.raises(f'无法从文本中解析出资源 id: [{text}]')

    @classmethod
    def parse_chapter_id_from_url(cls, url: str) -> Optional[str]:
        match = re.search(r'/chapter/(?:\d+/)?(\d+)', str(url))
        return match.group(1) if match else None

    @classmethod
    def parse_comic_id_from_url(cls, url: str) -> Optional[str]:
        match = re.search(r'/comic/(\d+)', str(url))
        return match.group(1) if match else None

    @classmethod
    def parse_slug_from_url(cls, url: str) -> Optional[str]:
        """提取 manhwa 风格的 slug，如 /index.php/comic/mozhouweixianzaoyu -> mozhouweixianzaoyu"""
        match = re.search(r'/comic/([A-Za-z0-9_\-]+)', str(url))
        if match is None:
            return None
        return None if match.group(1).isdigit() else match.group(1)

    @classmethod
    def parse_domain(cls, text: str) -> str:
        match = re.search(r'https?://([^/]+)', str(text))
        if match:
            return match.group(1)
        return str(text).strip('/')

    @classmethod
    def format_url(cls, path: str, domain: str, prot: str = 'https://') -> str:
        path = str(path)
        if path.startswith('http://') or path.startswith('https://'):
            return path
        if not path.startswith('/'):
            path = '/' + path
        return f'{prot}{domain}{path}'

    # ------------------------------------------------------------------ 数字

    @classmethod
    def parse_human_number(cls, text) -> int:
        """
        解析站点上的中文计数：'14 万' -> 140000, '1.2亿' -> 120000000, '1487' -> 1487
        """
        if text is None:
            return 0
        if isinstance(text, (int, float)):
            return int(text)

        text = str(text).replace(',', '').replace(' ', '').strip()
        if text == '':
            return 0

        unit = 1
        if text.endswith('万'):
            unit, text = 10_000, text[:-1]
        elif text.endswith('亿'):
            unit, text = 100_000_000, text[:-1]
        elif text.endswith('千'):
            unit, text = 1_000, text[:-1]

        try:
            return int(float(text) * unit)
        except ValueError:
            digits = re.sub(r'\D', '', text)
            return int(digits) if digits else 0

    @classmethod
    def safe_int(cls, value, default=0) -> int:
        try:
            return int(str(value).strip())
        except (TypeError, ValueError):
            return default


# 注册 ${ENV} 替换
_DSL_REPLACER.append((re.compile(r'\$\{(.*?)\}'), MccmsText.match_os_env))
