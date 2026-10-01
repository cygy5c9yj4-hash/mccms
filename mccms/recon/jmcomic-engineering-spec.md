# JMComic-Crawler-Python 架构与公共 API 复刻规格说明

> 目标版本：`src/jmcomic/__init__.py` 中 `__version__ = '2.7.7'`
> 分析基线路径：`mccms/recon/jmcomic-ref/src/jmcomic/`（源码）、`/tmp/recon/jmcomic/`（含 `assets/option/*.yml` 与 `assets/docs/sources/*.md`）
> 依赖的公共库：`common`（`pip install commonX`，本机位于 `.../site-packages/common`），提供 `AdvancedDict`、`PackerUtil`、`field_cache`、`ProxyBuilder`、`multi_thread_launcher`、`thread_pool_executor`、`fix_windir_name`、`Postmans` 等。
> 模块依赖方向（源码 `__init__.py` 注释）：`config <--- entity <--- toolkit <--- client <--- option <--- downloader`，`jm_plugin` / `jm_feature` 位于最上层。

## 0. 复刻前必须知道的三个事实

1. **`PluginKeys` / `PluginBase` / `PluginSettings` / `plugin_decide` 在本版本不存在。** 全仓库（`.py` 与 `.md`）grep 均无结果。本版本的插件契约是：
   - 基类 `JmOptionPlugin`，类属性 `plugin_key`（str，唯一注册键）；
   - 唯一需要实现的方法是 `invoke(self, **kwargs)`；
   - “插件被调用的时机”由 option 中 `plugins:` 下的一级 key（`after_init` / `before_album` / …）表达，由 `JmOption.call_all_plugin(group, **extra)` 分发。
   - 若目标文档要求 `PluginKeys`，那是 2.2.x～2.3.x 的旧 API，与本基线不兼容。
2. **`JmOption` 没有 `from_dict` 类方法。** 从 dict 构造的入口是 `JmOption.construct(dict)`；对外的 str/文件入口是 `api.create_option_by_str(text, mode='yml')` / `api.create_option_by_file(filepath)`。
3. **`jm_toolkit` 中不存在 `format_album_name`。** 原名提取是 `JmcomicText.parse_orig_album_name(name, default=None)`（内部用 `JmcomicText.tokenize`）；文件名/目录名净化是 `common.fix_windir_name`。

---

## 1. JmOption

文件：`src/jmcomic/jm_option.py`

### 1.1 类结构

```python
class JmOption:
    def __init__(self,
                 dir_rule: Dict,
                 download: Dict,
                 client: Dict,
                 plugins: Dict,
                 filepath=None,
                 call_after_init_plugin=True,
                 ):
        self.dir_rule = DirRule(**dir_rule)   # 不是 AdvancedDict，是真实对象
        self.client   = AdvancedDict(client)
        self.download = AdvancedDict(download)
        self.plugins  = AdvancedDict(plugins)
        self.filepath = filepath
        self.need_wait_plugins = []           # 由 plugin.enter_wait_list() 填充
        self.check_plugins_dependencies()     # 依赖预检/自动安装/仅告警
        if call_after_init_plugin:
            self.call_all_plugin('after_init', safe=True)
```

关键点：

- `__init__` 的 4 个位置参数是**必需**的；因此任何绕过 `construct` 的直接实例化都必须传全。
- `dir_rule` 被立刻转成 `DirRule` 对象（`DirRule(**dir_rule)`），所以后续 `option.dir_rule` 是对象而非 dict；`option.dir_rule.base_dir` / `.rule_dsl` / `.normalize_zh` 是三个真实字段。
- `client` / `download` / `plugins` 是 `AdvancedDict`，支持 `option.download.threading.image` 与 `option.download['threading']['image']` 两种写法；嵌套 dict 在取值时自动包装（`AdvancedDict.wrap_value`）。**注意 `AdvancedDict.__getattr__` 用的是 `self._data[item]`，缺失 key 抛 `KeyError` 而不是 `AttributeError`**，所以 schema 中所有 key 都必须由默认值补全。
- `after_init` 插件在构造末尾同步执行，`safe=True`（异常只记日志）。

### 1.2 构造 / 序列化 API 全表

| 方法 | 签名 | 语义 |
|---|---|---|
| `default_dict` | `@classmethod -> Dict` | 返回 `JmModuleConfig.option_default_dict()`（对 `DEFAULT_OPTION_DICT` 的 deepcopy + 动态填充） |
| `default` | `@classmethod -> JmOption` | `return cls.construct({})`，全默认配置 |
| `construct` | `@classmethod construct(origdic: Dict, cover_default=True) -> JmOption` | 深度合并默认值 → 处理 `log` / `version` → 旧版本兼容 → `cls(**dic)` |
| `from_file` | `@classmethod from_file(filepath: str) -> JmOption` | `PackerUtil.unpack(filepath)[0]` → `dic.setdefault('filepath', filepath)` → `construct` |
| `to_file` | `to_file(filepath=None)` | `PackerUtil.pack(self.deconstruct(), filepath)`；`filepath` 为空时用 `self.filepath`，都没有则抛异常 |
| `deconstruct` | `-> Dict` | 导出含 `version` 与 `log` 的完整 dict |
| `copy_option` | `-> JmOption` | 用当前 4 段配置重建，`call_after_init_plugin=False`（不重复触发 after_init） |
| `merge_default_dict` | `@classmethod (user_dict, default_dict=None)` | 递归 dict 合并；`user_dict` 覆盖 `default_dict` |

```python
@classmethod
def construct(cls, origdic: Dict, cover_default=True) -> 'JmOption':
    dic = cls.merge_default_dict(origdic) if cover_default else origdic

    log = dic.pop('log', True)          # 从 dic 中摘掉，不传给 __init__
    if not log:
        disable_jm_log()
    elif log == 'pretty':
        enable_pretty_log()

    version = dic.pop('version', None)
    if version is not None and float(version) >= float(JmModuleConfig.JM_OPTION_VER):
        return cls(**dic)               # 新版本，跳过兼容代码

    cls.compatible_with_old_versions(dic)
    return cls(**dic)
```

`deconstruct()` 的返回结构（即 `to_file` 写出的 yml）：

```python
{
  'version': JmModuleConfig.JM_OPTION_VER,     # '2.1'
  'log': JmModuleConfig.FLAG_ENABLE_JM_LOG,    # True/False
  'dir_rule': {'rule': ..., 'base_dir': ..., 'normalize_zh': ...},
  'download': self.download.src_dict,
  'client':   self.client.src_dict,
  'plugins':  self.plugins.src_dict,
}
```

### 1.3 旧版本兼容：`compatible_with_old_versions(dic)`

三项迁移，按顺序执行：

1. **并发配置**：`download.threading.batch_count` → 重命名为 `download.threading.image`。
2. **插件配置**：顶层 `plugin:` → 重命名为 `plugins:`。
3. **zip 插件 `level` 参数迁移**（`_migrate_zip_level`）：
   - `after_album` + `level != 'album'` → 把该 pinfo 移到 `after_photo`（按章节压缩）；
   - `after_photo` + `level == 'album'` → 把该 pinfo 移到 `after_album`（整本压缩）；
   - 其他情况只 pop 掉 `level` 并打印迁移建议。
   - 迁移时会用 `yaml.dump` 打印一份“只有 zip 插件”的建议配置片段。

### 1.4 完整 yml 配置 schema（含默认值）

默认值来源：`JmModuleConfig.DEFAULT_OPTION_DICT` + `JmModuleConfig.option_default_dict()` 运行时填充。

#### 顶层

| key | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `log` | `true` \| `false` \| `'pretty'` | `None` → 运行时取 `JmModuleConfig.FLAG_ENABLE_JM_LOG`（初始 `True`） | `false` 调 `disable_jm_log()`；`'pretty'` 调 `enable_pretty_log()`（带 ANSI 颜色）。**该 key 不会传给 `JmOption.__init__`** |
| `version` | str/float | `'2.1'`（写出时） | 读取时若 `float(version) >= JM_OPTION_VER` 则跳过兼容代码 |
| `dir_rule` | dict | 见下 | 路径规则 |
| `download` | dict | 见下 | 下载行为 |
| `client` | dict | 见下 | 客户端/网络 |
| `plugins` | dict | 见下 | 插件 |

#### `dir_rule`

| key | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `rule` | str | `'Bd_Pname'` | 路径 DSL，见第 2 节 |
| `base_dir` | str | `None` → 运行时 `os.getcwd()` | 根目录；支持 `${ENV_VAR}`（经 `JmcomicText.parse_to_abspath` → `parse_dsl_text`） |
| `normalize_zh` | `None` \| `'zh-cn'` \| `'zh-tw'` | `None` | 目录/文件名繁简体归一化，依赖可选库 `zhconv`；失败静默回退 |

#### `download`

| key | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `download.cache` | bool | `True` | 目标文件已存在时是否跳过下载（对应 `image.cache = option.decide_download_cache(image)`） |
| `download.image.decode` | bool | `True` | 是否还原被禁漫混淆的图片；`.gif` 强制不解码（`decide_download_image_decode`） |
| `download.image.suffix` | str \| null | `None` | 统一转换图片后缀（如 `'.jpg'`）；`None` 表示保持原后缀；`.gif` 不受影响 |
| `download.threading.image` | int | `30` | 同时下载的图片线程/协程数（`decide_image_batch_count`） |
| `download.threading.photo` | int \| null | `None` → 运行时 `os.cpu_count()` | 同时下载的章节线程/协程数（`decide_photo_batch_count`） |

#### `client`

| key | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `client.impl` | str | `None` → 运行时 `JmModuleConfig.DEFAULT_CLIENT_IMPL`（`'api'`） | 客户端实现 key，注册于 `REGISTRY_CLIENT`：`'api'`(JmApiClient) / `'html'`(JmHtmlClient) / `'photo_concurrent_fetcher_proxy'`(PhotoConcurrentFetcherProxy) |
| `client.async_impl` | str | `'async_api'` | 异步客户端实现 key，注册于 `REGISTRY_ASYNC_CLIENT` |
| `client.retry_times` | int | `5` | 单请求失败重试次数，传给 client 构造 |
| `client.cache` | `null`/`false`/`true`/`'level_option'`/`'level_client'` | `None` → 运行时 `JmModuleConfig.DEFAULT_CLIENT_CACHE`（`None`，即不缓存） | 元数据缓存级别，见 `CacheRegistry` |
| `client.domain` | list \| str \| dict | `[]` | 域名列表。`[]`/空 → 按 impl 取内置域名；str → 按行切分（`str_to_list`）；dict → `domain[impl]` |
| `client.postman.type` | str | `'curl_cffi'` | Postman 实现类型（`common.Postmans` 注册表；`'curl_cffi'` / `'curl_cffi_session'` / `'requests'`…） |
| `client.postman.meta_data.impersonate` | str | `'chrome'` | curl_cffi 浏览器指纹伪装 |
| `client.postman.meta_data.headers` | dict \| null | `None` | 请求头；`None` 时由 client 使用内置模板（`APP_HEADERS_TEMPLATE` / `new_html_headers`） |
| `client.postman.meta_data.proxies` | 见下 | `None` → 运行时 `JmModuleConfig.DEFAULT_PROXIES` = `ProxyBuilder.system_proxy()` | 代理。支持：null / `system` / `clash` / `v2ray` / `127.0.0.1:7890` / `{http:..., https:...}` |
| `client.postman.meta_data.cookies` | dict \| null | 无（不含该 key） | cookies；敏感本子需要（一般只需 `AVS`）。cookies 与域名强绑定。`login` 插件会通过 `option.update_cookies()` 合并写入 |
| `client.postman.meta_data.timeout` | int | 无（不含该 key） | 请求超时秒数；由 `new_jm_client(**kwargs)` 的 kwargs 也能覆盖 |

> `new_jm_client(domain_list=None, impl=None, cache=None, domain_retry_strategy=None, **kwargs)` 会把 `**kwargs` **update 进 `meta_data` 的深拷贝**（同一份 postman 配置派生的新 client 不共享元数据）。

#### `plugins`

| key | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `plugins.valid` | `'ignore'` \| `'log'` \| `'raise'` | `'log'` | 插件抛 `PluginValidationException` 时的全局处理策略；可被单个 pinfo 的 `valid` 覆盖 |
| `plugins.dependencies_strategy` | `'failed-fast'` \| `'auto-install'` \| `'ignore-only-log'` | `'failed-fast'` | 插件依赖库缺失时的处理策略，在 `JmOption.__init__` 阶段由 `check_plugins_dependencies()` 统一执行 |
| `plugins.<事件名>` | list[dict] | 无 | 事件分组，key 名任意；内置事件见第 4 节 |

单个插件条目的字段（`pinfo`）：

| 字段 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `plugin` | str | 必填 | 插件 key，必须在 `JmModuleConfig.REGISTRY_PLUGIN` 中 |
| `kwargs` | dict \| null | `{}` | 传给 `plugin.invoke(**kwargs)`；key 会转 str，value 若为 str 会经 `JmcomicText.parse_dsl_text` 展开 `${ENV_VAR}` |
| `log` | bool | `true` | `false` → `plugin.log_enable = False` |
| `safe` | bool | `true` | `true` → 插件异常只记日志；`false` → 异常向上抛（`call_all_plugin` 层面） |
| `valid` | `'ignore'`\|`'log'`\|`'raise'` | `plugins.valid` | 覆盖全局参数校验策略 |

### 1.5 完整注释版示例 yml

```yaml
version: '2.1'
log: pretty                        # false=关闭日志, true=普通, pretty=彩色

dir_rule:
  rule: Bd_Aauthor_Atitle_Pindex   # Bd=base_dir, A*=JmAlbumDetail字段, P*=JmPhotoDetail字段
  base_dir: ${JM_DOWNLOAD_DIR}/     # 支持环境变量；也可写 D:/a/b/c/
  normalize_zh: zh-cn               # null | zh-cn | zh-tw（需 zhconv）

download:
  cache: true                       # 已存在则跳过
  image:
    decode: true                    # 还原图片混淆
    suffix: .jpg                    # null=不改后缀；gif 例外
  threading:
    image: 30                       # 默认 30
    photo: 16                       # 默认 cpu_count()

client:
  impl: api                         # api | html
  async_impl: async_api
  retry_times: 5
  cache: null                       # null|false|true|level_option|level_client
  domain:                           # 留空则按 impl 走内置域名
    html: [ 18comic.vip, 18comic.org ]
    api:  [ www.cdnhjk.net ]
  postman:
    type: curl_cffi
    meta_data:
      impersonate: chrome
      timeout: 15
      headers: null
      proxies: system               # null|system|clash|v2ray|ip:port|{http,https}
      cookies:
        AVS: xxxxxxxx

plugins:
  valid: log
  dependencies_strategy: failed-fast
  after_init:
    - plugin: login
      kwargs: { username: un, password: pw }
  before_album:
    - plugin: download_cover
      kwargs:
        dir_rule: { base_dir: D:/a/b/c/, rule: '{Atitle}/{Aid}_cover.jpg' }
  after_album:
    - plugin: zip
      kwargs:
        zip_dir: D:/jmcomic/zip/
        filename_rule: Atitle        # after_album 只能写 Axxx
        suffix: zip
        delete_original_file: true
        encrypt: { password: '123456' }
  after_photo:
    - plugin: img2pdf
      kwargs: { pdf_dir: D:/pdf/, filename_rule: Pid, encrypt: { password: 123 } }
  main:
    - plugin: favorite_folder_export
      log: false
      kwargs: { zip_enable: true, zip_filepath: ${ZIP_FP}, max_retry: 2 }
```

### 1.6 `decide_*` 系列（全部是可选重写点，插件也大量 monkey-patch 这些方法）

| 方法 | 默认实现 |
|---|---|
| `decide_image_batch_count(photo)` | `self.download.threading.image` |
| `decide_photo_batch_count(album)` | `self.download.threading.photo` |
| `decide_image_filename(image)` | `image.filename_without_suffix`（即 `image.img_file_name`，如 `00001`）**不含后缀** |
| `decide_image_suffix(image)` | `image.is_gif` → `image.img_file_suffix`；否则 `self.download.image.suffix or image.img_file_suffix`（返回不同后缀会触发转码） |
| `decide_image_save_dir(photo, ensure_exists=True)` | `self.dir_rule.decide_image_save_dir(photo.from_album, photo)`，`ensure_exists` 时 `JmcomicText.try_mkdir` |
| `decide_image_filepath(image, consider_custom_suffix=True)` | `os.path.join(save_dir, fix_windir_name(decide_image_filename(image)) + suffix)` |
| `decide_download_cache(_image)` | `self.download.cache`（被 `image_suffix_filter` 插件包装） |
| `decide_download_image_decode(image)` | `image.is_gif` → `False`；否则 `self.download.image.decode` |
| `decide_client_domain(client_key)` | api 系 → `JmModuleConfig.DOMAIN_API_LIST`；html 系 → `DOMAIN_HTML_LIST` 或 `[JmModuleConfig.get_html_domain()]` |

### 1.7 客户端构造

```python
@field_cache()                        # 首次调用创建并缓存到 self，后续调用返回同一对象
def build_jm_client(self, **kwargs):
    return self.new_jm_client(**kwargs)

def new_jm_client(self, domain_list=None, impl=None, cache=None,
                  domain_retry_strategy=None, **kwargs):
    postman_conf = deepcopy(self.client.postman.src_dict)
    meta_data = postman_conf['meta_data']
    if len(kwargs) != 0:
        meta_data.update(kwargs)      # kwargs 覆盖 meta_data
    retry_times = self.client.retry_times
    cache = cache if cache is not None else self.client.cache
    impl  = impl or self.client.impl
    if isinstance(impl, type):        # 支持直接传类
        impl = impl.client_key
    postman = Postmans.create(data=postman_conf)
    clazz = JmModuleConfig.client_impl_class(impl)
    if clazz == AbstractJmClient or not issubclass(clazz, AbstractJmClient):
        raise NotImplementedError(clazz)
    client = clazz(postman=postman,
                   domain_list=decide_domain_list(),
                   retry_times=retry_times,
                   domain_retry_strategy=domain_retry_strategy)
    CacheRegistry.enable_client_cache_on_condition(self, client, cache)
    return client
```

`CacheRegistry`（同文件）：

```python
class CacheRegistry:
    REGISTRY = {}
    @classmethod
    def level_option(cls, option, _client):   # option 级：同 option 的所有 client 共享
        ...
    @classmethod
    def level_client(cls, _option, client):   # client 级：各自独立
        ...

    @classmethod
    def enable_client_cache_on_condition(cls, option, client, cache):
        if cache is None:  return
        elif isinstance(cache, bool):
            if not cache: return
            cache = cls.level_option
        elif isinstance(cache, str):
            func = getattr(cls, cache, None)
            ExceptionTool.require_true(func is not None, f'未实现的cache配置名: {cache}')
            cache = func
        client.set_cache_dict(cache(option, client))
```

- `REGISTRY` 的 key 是 option 或 client 对象本身，value 是缓存 dict（`client.set_cache_dict(...)` 注入）。

其他：`update_cookies(cookies)` 把 cookies 合并进 `self.client.postman.meta_data['cookies']`；`client_key_is_given_type(client_key, ctype)` 用于型别判定；`new_jm_async_client(...)`（`client.async_impl`）；`download_album / download_photo / download_album_async / download_photo_async` 是转发到 `api.py` 的面向对象糖。

### 1.8 插件调用管线（JmOption 侧）

```python
def call_all_plugin(self, group: str, safe=None, **extra):
    plugin_list = self.plugins.get(group, [])
    if plugin_list is None or len(plugin_list) == 0:
        return
    for pinfo in plugin_list:
        key, kwargs = pinfo['plugin'], pinfo.get('kwargs', None)
        pclass = JmModuleConfig.REGISTRY_PLUGIN.get(key, None)
        ExceptionTool.require_true(pclass is not None, f'[{group}] 未注册的plugin: {key}')
        try:
            self.invoke_plugin(pclass, kwargs, extra, pinfo)
        except BaseException as e:
            if safe is True or pinfo.get('safe', True):
                jm_log('plugin.exception', e)
            else:
                raise e

def invoke_plugin(self, pclass, kwargs, extra, pinfo):
    kwargs = self.fix_kwargs(kwargs)          # key→str; str value→parse_dsl_text(${ENV})
    if len(extra) != 0:
        kwargs.update(extra)                  # extra 覆盖 kwargs
    plugin = pclass.build(self)
    if not pinfo.get('log', True):
        plugin.log_enable = False
    jm_log('plugin.invoke', f'调用插件: [{pclass.plugin_key}]')
    plugin.invoke(**kwargs)
```

异常三路分发：

| 异常 | 处理函数 | 行为 |
|---|---|---|
| `PluginValidationException` | `handle_plugin_valid_exception` | 按 `pinfo.get('valid', self.plugins.valid)`：`ignore` 静默 / `log` 打印 / `raise` 上抛 |
| `JmcomicException` | `handle_plugin_jmcomic_exception` | `jm_log('plugin.exception', ...)` 后 **必然 raise** |
| 其他 `BaseException` | `handle_plugin_unexpected_error` | `jm_log('plugin.error', ...)` 后 **必然 raise**（再由 `call_all_plugin` 的 safe 决定生死） |

`check_plugins_dependencies()`：遍历 `self.plugins.src_dict` 中所有 list 值里的 dict 条目，取 `plugin` 对应类，调用 `pclass.check_plugin_dependency(pinfo.get('kwargs') or {}, strategy=self.plugins.dependencies_strategy)`。

`wait_all_plugins_finish()`：遍历 `need_wait_plugins` 调 `plugin.wait_until_finish()`。

---

## 2. dir_rule：路径规则 DSL

`class DirRule`（`jm_option.py`）。

### 2.1 构造

```python
class DirRule:
    RULE_BASE_DIR = 'Bd'

    def __init__(self, rule: str, base_dir=None, normalize_zh=None):
        base_dir = JmcomicText.parse_to_abspath(base_dir)  # 展开 ${ENV} 后 abspath
        self.base_dir = base_dir
        self.rule_dsl = rule
        self.normalize_zh = normalize_zh
        self.parser_list: List[Tuple[str, Callable]] = self.get_rule_parser_list(rule)
```

### 2.2 DSL 切分：`split_rule_dsl`

```python
def split_rule_dsl(self, rule_dsl: str) -> List[str]:
    if '/' in rule_dsl:      rule_list = rule_dsl.split('/')
    elif '_' in rule_dsl:    rule_list = rule_dsl.split('_')
    else:                    rule_list = [rule_dsl]
    rule_list = [e.strip() for e in rule_list]
    if rule_list[0] != self.RULE_BASE_DIR:
        rule_list.insert(0, self.RULE_BASE_DIR)     # 自动补 Bd
    return rule_list
```

- 分隔符优先级：`/` > `_`。二者混用会以 `/` 为准。
- **`Bd` 可省略**，程序自动插到首位。
- 每一段都会被 `strip()`。

### 2.3 解析器选择：`get_rule_parser(rule)`

| 条件 | parser | 行为 |
|---|---|---|
| `rule == 'Bd'` | `parse_bd_rule` | 直接返回 `self.base_dir`（**不做** `fix_windir_name`/`to_zh`） |
| `'{' in rule` | `parse_f_string_rule` | Python f-string 风格：`rule.format(**properties)`，`properties` 由 album/photo 的 `get_properties_dict()` 合并得到 |
| `rule.startswith(('A', 'P'))` | `parse_detail_rule` | `DetailEntity.get_dirname(detail, rule[1:])`，`detail = album if rule.startswith('A') else photo` |
| 其他（无 `{`、非 A/P 开头） | `parse_f_string_rule` | 等同一个字面量目录名（format 无占位符则原样返回） |

注意判定顺序：**先看 `{`**。因此 `Aauthor` 走 detail 分支，而 `(JM{Aid}-{Pindex})-{Pname}` 整体走 f-string 分支。

### 2.4 路径生成：`apply_rule_to_path`

```python
def apply_rule_to_path(self, album, photo, only_album_rules=False) -> str:
    path_ls = []
    for rule, parser in self.parser_list:
        if only_album_rules and not (rule == self.RULE_BASE_DIR or rule.startswith('A')):
            continue                                       # 求 album root 时丢弃所有 P*
        path = parser(album, photo, rule)
        if parser != self.parse_bd_rule:
            conv_path = JmcomicText.to_zh(str(path), self.normalize_zh)
            path = fix_windir_name(conv_path).strip()      # 净化非法字符并 strip
        path_ls.append(path)
    return fix_filepath('/'.join(path_ls))
```

- 两个对外入口：
  - `decide_image_save_dir(album, photo)` → `apply_rule_to_path(album, photo)`（全部规则）
  - `decide_album_root_dir(album)` → `apply_rule_to_path(album, None, only_album_rules=True)`（只用 `Bd` 与 `A*`）
- 每一段（除 `Bd`）都经过 `to_zh` → `fix_windir_name` → `strip`；最后 `'/'.join` 再 `fix_filepath`（统一为正斜杠、折叠 `//`，若目标是已存在目录则补尾部 `/`）。
- 解析异常会 `jm_log('dir_rule', f'路径规则"{rule}"的解析出错: ...')` 后原样 raise。

### 2.5 `filename_rule` 版本：`apply_rule_to_filename`

供插件（zip / img2pdf / long_img / download_cover / calibre_metadata）使用：

```python
@classmethod
def apply_rule_to_filename(cls, album, photo, rule: str) -> str:
    if album is None:
        album = photo.from_album          # 防止 dsl 里含 Axx 时报错
    return fix_windir_name(cls.get_rule_parser(rule)(album, photo, rule)).strip()
```

**只返回单段文件名，不做 `/` 拼接**；`album is None` 时自动回填 `photo.from_album`。

### 2.6 字段列表

字段来源 = `detail.get_properties_dict()` 的 key 去掉首字母（`prefix = self.__class__.__name__[2]`，即 `JmAlbumDetail`→`A`、`JmPhotoDetail`→`P`）。`get_properties_dict()` 汇总三类：

1. `self.__dict__` 的实例属性；
2. MRO 上的所有 `property`；
3. `JmModuleConfig.AFIELD_ADVICE` / `PFIELD_ADVICE` 注册的自定义函数（key 为自定义名）。

`parse_detail_rule` 走的是 `DetailEntity.get_dirname(detail, ref)`：

```python
@classmethod
def get_dirname(cls, detail, ref: str) -> str:
    advice_func = (JmModuleConfig.AFIELD_ADVICE
                   if isinstance(detail, JmAlbumDetail)
                   else JmModuleConfig.PFIELD_ADVICE).get(ref, None)
    if advice_func is not None:
        return advice_func(detail)
    return getattr(detail, ref)
```

**常用 A 字段（`JmAlbumDetail` / `DetailEntity`）**

| DSL | 取值 | 说明 |
|---|---|---|
| `Aid` | `album.album_id` / `album.id` | 本子 id（字符串） |
| `Aname` | `album.name` | 本子完整名（含汉化组/社团等后缀） |
| `Atitle` | `DetailEntity.title` → `getattr(self,'name')` | 与 `Aname` 同值 |
| `Aauthor` | 首作者，缺失时 `JmModuleConfig.DEFAULT_AUTHOR`（`'default_author'`） | |
| `Aauthors` | 作者列表 | |
| `Aoname` | `JmcomicText.parse_orig_album_name(title)` 提取的原名 | 括号内容会被跳过 |
| `Aauthoroname` | `'【{author}】{oname}'` | |
| `Aidoname` | `'[{id}] {oname}'` | |
| `Apage_count` | 总页数 | |
| `Apub_date` / `Aupdate_date` | 上架/更新日期 | |
| `Alikes` / `Aviews` / `Acomment_count` | 点赞/观看/评论数 | |
| `Aworks` / `Aactors` / `Atags` / `Adescription` | 作品/登场人物/标签/简介 | |
| `Ais_favorite` / `Aliked` | 是否已收藏/已点赞 | |
| `Ascramble_id` | 图片混淆 id | |

**常用 P 字段（`JmPhotoDetail`）**

| DSL | 取值 | 说明 |
|---|---|---|
| `Pid` | `photo.photo_id` / `photo.id` | 章节 id |
| `Pname` | `photo.name` | 章节名 |
| `Ptitle` | `photo.title`（= name） | |
| `Pindex` | `photo.index`（构造时 = `album_index`） | 章节在本子中的序号，从 1 开始；单章本子且 `sort==2` 时归一为 1 |
| `Pindextitle` | `f'第{self.album_index}話 {self.name}'` | 注意用的是繁体「話」且**不带前导零** |
| `Palbum_id` | 所属本子 id（单章本子时 = `photo_id`） | |
| `Pauthor` | 优先 `from_album.author` | |
| `Ptags` | 优先 `from_album.tags`；否则按 `,`(html) 或空格(api) 切分 | |
| `Pis_single_album` | `series_id == 0` | |
| `Pscramble_id` / `Ppage_arr` / `Psort` / `Pdata_original_domain` / `Pdata_original_0` | 原始字段 | |

> 关于 `Pindex` 的零填充：源码**不做**零填充。若要 `001` 形式，需用 f-string 语法，如 `rule: '{Aid}/{Pindex:03}'`（DSL 支持 Python format spec）。README/文档中“章节序号”即 `Pindex` 的裸值。

### 2.7 f-string 规则

```python
@classmethod
def parse_f_string_rule(cls, album, photo, rule: str):
    properties = {}
    if album is not None: properties.update(album.get_properties_dict())
    if photo is not None: properties.update(photo.get_properties_dict())
    return rule.format(**properties)
```

- key 形如 `{Aid}` `{Pname}` `{Pindex:03}` `{Atitle}`。
- **photo 的属性覆盖 album 的同名 key**（不存在同名场景，因为前缀不同）。
- 整段结果仍会被 `fix_windir_name(...).strip()` 处理，因此 `(JM{Aid}-{Pindex})-{Pname}` 中的 `(`/`)` 是合法字符（不在 `_win_forbid_char` 内）。
- 一个 segment 内既写文件名又写后缀是允许的（zip/calibre 就用这种写法：`rule: 'Bd / zip / JM{Aid}-{Atitle}.zip'`）。

### 2.8 组合语义总结

```
最终 filepath = base_dir  (+ '/' + seg2) (+ '/' + seg3) ...
图片全路径   = decide_image_save_dir(photo) + '/' + fix_windir_name(decide_image_filename(image)) + suffix
本子根目录   = apply_rule_to_path(album, None, only_album_rules=True)
```

`base_dir` 本身**不参与** `fix_windir_name`/`to_zh`；其余每一段都参与。

---

## 3. JmDownloader

文件：`src/jmcomic/jm_downloader.py`；异步版 `src/jmcomic/jm_async_downloader.py`。

### 3.1 类层次

```
DownloadCallback                      # 纯日志默认实现（before/after_album/photo/image）
   └── BaseDownloader                 # 无 I/O：回调派发 + 插件 + Feature + 清单 + 失败收集
          ├── JmDownloader            # 同步 I/O 调度
          │      ├── DoNotDownloadImage            # 只建目录不下图（测试用）
          │      └── JustDownloadSpecificCountImage# 只下前 N 张（测试用）
          └── JmAsyncDownloader       # asyncio + ThreadPoolExecutor 流水线
                 └── AsyncProgressDownloader        # 见 jm_plugin.py
```

`JmDownloader.__init__(option)`：`super().__init__(option)` 后 `self.client = self.create_client()`（默认 `self.option.build_jm_client()`）。`BaseDownloader.__init__` 只设 `self.client = None`。

### 3.2 `Downloadable` 注记字段（`jm_entity.py`）

```
save_path: str          # 下载保存路径
exists: bool            # 下载前目标是否已存在
skip: bool = False      # 是否跳过本次下载（供插件设置，如 skip_photo_with_few_images / image_suffix_filter）
cache: bool = True      # 已存在时是否使用缓存
duration: Optional[float]  # 下载耗时（秒），由 record_download_duration 写入
```

### 3.3 生命周期与钩子调用顺序

**专辑（album）**：`download_album(album_id)`（装饰 `@record_download_duration('album_started_at')`）

1. `album = self.client.get_album_detail(album_id)`
2. `self.begin_manifest(album)`
3. `download_by_album_detail(album)`（同样带 `@record_download_duration('album_started_at')`）
   1. `album.save_path = self.option.dir_rule.decide_album_root_dir(album)`
   2. `self.before_album(album)`
   3. `if album.skip: return`
   4. `self.execute_on_condition(album, self.download_by_photo_detail, self.option.decide_photo_batch_count(album))`
   5. `self.after_album(album)`
4. `finally: self.finish_manifest(album)`
5. `return album`

**章节（photo）**：`download_photo(photo_id)` → `client.get_photo_detail` → `begin_manifest` → `download_by_photo_detail` → `finish_manifest`

`download_by_photo_detail(photo)`（`@catch_exception` + `@record_download_duration('photo_started_at')`）：

1. `photo.save_path = self.option.decide_image_save_dir(photo)`
2. `self.client.check_photo(photo)`（登录/可见性校验）
3. `self.before_photo(photo)`
4. `if photo.skip: return`
5. `self.execute_on_condition(photo, self.download_by_image_detail, self.option.decide_image_batch_count(photo))`
6. `self.after_photo(photo)`

**图片（image）**：`download_by_image_detail(image)`（`@catch_exception` + `@record_download_duration('image_started_at')`）：

```python
img_save_path = self.option.decide_image_filepath(image)
image.save_path = img_save_path
image.exists = file_exists(img_save_path)
image.cache  = self.option.decide_download_cache(image)

self.before_image(image, img_save_path)
if image.skip:
    return
if image.cache and image.exists:          # 缓存命中：仍触发 after_image
    self.after_image(image, img_save_path)
    return
decode_image = self.option.decide_download_image_decode(image)
self.client.download_by_image_detail(image, img_save_path, decode_image=decode_image)
self.after_image(image, img_save_path)
```

**钩子调用顺序总览**

```
download_album
  └─ before_album(album)            → plugins[before_album]
       └─ 对每个 photo（可并发）:
            before_photo(photo)     → plugins[before_photo]
              └─ 对每个 image（可并发）:
                   before_image(image, path)  → plugins[before_image]
                   [下载 / 缓存命中]
                   after_image(image, path)   → plugins[after_image] + 记录成功清单
            after_photo(photo)      → plugins[after_photo] + Feature(after_photo)
  └─ after_album(album)             → plugins[after_album]  + Feature(after_album)
```

`BaseDownloader` 中各钩子覆写时的编排（**顺序很重要**，复刻时必须一致）：

| 钩子 | 顺序 |
|---|---|
| `before_album` | `super().before_album` → `download_success_dict.setdefault(album, {})` → `call_all_plugin('before_album', album=album, downloader=self)` |
| `after_album` | `super().after_album` → `call_all_plugin('after_album', album=album, downloader=self)` → `_invoke_features_for('after_album', album=album, downloader=self)` |
| `before_photo` | `super().before_photo` → `download_success_dict.setdefault(photo.from_album, {})` → `[photo.from_album].setdefault(photo, [])` → `call_all_plugin('before_photo', photo=photo, downloader=self)` |
| `after_photo` | `super().after_photo` → `call_all_plugin('after_photo', photo=photo, downloader=self)` → `_invoke_features_for('after_photo', ...)` |
| `before_image` | `super().before_image` → `call_all_plugin('before_image', image=image, downloader=self)` |
| `after_image` | `super().after_image` → `call_all_plugin('after_image', image=image, downloader=self)` → `download_success_dict[album][photo].append((image.save_path, image))` |

> `call_all_plugin` 的 `extra` 就是 `{album/photo/image: ..., downloader: self}`，会覆盖插件 kwargs 中的同名参数 —— 这正是 zip/img2pdf 能拿到 `album`/`downloader` 的原因。

`DownloadCallback` 的默认实现只 `jm_log`，topic 依次为 `album.before` / `album.after` / `photo.before` / `photo.after` / `image.before` / `image.after`。

### 3.4 线程模型：`execute_on_condition`

```python
def execute_on_condition(self, iter_objs: DetailEntity, apply: Callable, count_batch: int):
    iter_objs = self.do_filter(iter_objs)
    count_real = len(iter_objs)
    if count_real == 0:
        return
    apply = bind_jm_task_context(apply)      # 把 TaskContext 快照绑进子线程
    if count_batch >= count_real:
        multi_thread_launcher(iter_objs=iter_objs, apply_each_obj_func=apply)   # 一对象一线程
    else:
        thread_pool_executor(iter_objs=iter_objs, apply_each_obj_func=apply, max_workers=count_batch)
```

- 两级并发：**album→photo** 用 `option.decide_photo_batch_count(album)`；**photo→image** 用 `option.decide_image_batch_count(photo)`。
- 线程数 ≥ 对象数时退化为「一对象一线程」；否则用线程池。
- 两个 launcher 都来自 `common`，默认 `wait_finish=True`（同步等待）。
- `do_filter(detail)` 是用户重写点：默认 `return detail`；返回 `[album[-1]]` / `[photo[:10]]` 之类即可裁剪。
- `bind_jm_task_context` 保证子线程里 `get_jm_task_context()` 能看到 `download_type` / `jm_id`（日志前缀与 Feature 判定依赖它）。

### 3.5 异常监听机制（两层）

**第一层：下载级失败收集（downloader 自身）**

```python
def catch_exception(func):
    @wraps(func)
    def wrapper(self, *args, **kwargs):
        try:
            return func(self, *args, **kwargs)
        except Exception as e:
            detail = args[0]
            if detail.is_image():
                jm_log('image.failed', f'图片下载失败: [{detail.download_url}], 异常: [{e}]', e)
                self.download_failed_image.append((detail, e))
            elif detail.is_photo():
                jm_log('photo.failed', f'章节下载失败: [{detail.id}], 异常: [{e}]', e)
                self.download_failed_photo.append((detail, e))
            raise e
    return wrapper
```

- 装在 `download_by_photo_detail` 与 `download_by_image_detail` 上。**注意：装饰器写在 `@record_download_duration` 外层**（`catch_exception` 在上，`record_download_duration` 在下），因此计时先于异常捕获生效。
- 失败最终由 `api.download_album(..., check_exception=True)` 触发的 `raise_if_has_exception()` 汇总：

```python
def raise_if_has_exception(self):
    if not self.has_download_failures: return
    msg_ls = ['部分下载失败', '', '']
    if self.download_failed_photo: msg_ls[1] = f'共{N}个章节下载失败: ...'
    if self.download_failed_image: msg_ls[2] = f'共{N}个图片下载失败: ...'
    ExceptionTool.raises('\n'.join(msg_ls), {'downloader': self}, PartialDownloadFailedException)
```

- 相关只读属性：`all_success`（成功数与实体长度逐层比对；**被 filter 裁剪时会为 False**）、`has_download_failures`。

**第二层：全局异常监听注册表**

```python
# jm_config.py
REGISTRY_EXCEPTION_LISTENER = {}     # key: 异常类, value: listener(e)

@classmethod
def register_exception_listener(cls, etype, listener):
    cls.REGISTRY_EXCEPTION_LISTENER[etype] = listener

# jm_exception.py
@classmethod
def raises(cls, msg, context=None, etype=None):
    e = (etype or JmcomicException)(msg, context)
    cls.notify_all_listeners(e)
    raise e

@classmethod
def notify_all_listeners(cls, e):
    for accept_type, listener in JmModuleConfig.REGISTRY_EXCEPTION_LISTENER.items():
        if isinstance(e, accept_type):
            listener(e)
```

即：**任何**通过 `ExceptionTool.raises*` 抛出的异常，在 raise 之前都会同步通知匹配类型的 listener（listener 签名 `(e)`，无返回值）。所有内置异常（`JmcomicException` / `PartialDownloadFailedException` / `MissingAlbumPhotoException` / `RegularNotMatchException` / `ResponseUnexpectedException` …）都定义在 `jm_exception.py`。

### 3.6 下载清单（Manifest）与返回值

```python
class DownloadManifest:
    image_filepath_list: List[str] = []
    export_filepath_dict: Dict[str, List[str]] = {}     # suffix(小写,无点) -> [filepath]
    duration: Optional[float] = None
    def get_export_filepath_list(self, suffix): ...
```

- `begin_manifest(detail)` / `finish_manifest(detail)` 配对；`finish_manifest` 按 `photo.index`、`image.index` 排序填充 `image_filepath_list`。
- `resolve_manifest_detail(detail)`：detail 本身在清单里 → 用它；否则若 detail 是 photo 且其 `from_album` 在清单里 → 用 album。
- `record_export_filepath(detail, filepath)`：插件导出产物登记（zip/img2pdf/long_img 调用）。无活动清单时 `ExceptionTool.raises(f'当前实体没有活动的下载清单: {detail}')`。

```python
class DownloadResult(NamedTuple):
    detail: DetailEntity
    downloader: BaseDownloader
    @property
    def manifest(self) -> DownloadManifest: ...
    @property
    def duration(self) -> Optional[float]: ...

class BatchResult(set):
    failed: Dict[str, BaseException] = {}
    @property
    def all_succeeded(self) -> bool: return len(self.failed) == 0
    @property
    def total(self) -> int: return len(self) + len(self.failed)
```

`api.download_album(id, option, downloader, *, check_exception=True, extra=None)`：
- `id` 不是 str/int → 转 `download_batch`（`multi_thread_launcher`，一个 id 一个线程一个 option，失败进 `BatchResult.failed`，不抛）；
- 否则在 `jm_task_context(download_type='album', jm_id=..., task_started_at=...)` 中 `with new_downloader(option, downloader) as dler:` → `dler.add_features(extra)` → `dler.download_album(id)` → 可选 `raise_if_has_exception()` → `DownloadResult`。

`downloader` 参数既可是类（`FindUpdatePlugin` 传 `FindUpdateDownloader`），也可是实例；`new_downloader` 里 `downloader(option)` 或默认 `JmModuleConfig.downloader_class()`。

### 3.7 Feature 机制（与 downloader 的交互）

- `dler.add_features(extra)`：接受 `Feature` / `FeatureChain` / `list` / `None`；**必须在下载任务上下文中调用**（`_require_feature_context` 要求 `download_type in ('album','photo')`，否则抛异常）。
- `after_album` / `after_photo` 钩子内调 `_invoke_features_for(when, ...)`，逐个 `feature.should_invoke(when)` 判定后 `feature.invoke(self.option, when=when, **kwargs)`，异常只记 `downloader.feature.exception`。
- 内置：`Feature.export_pdf` / `Feature.export_zip` / `Feature.export_long_img`（均为 `PluginFeature`）。`PluginFeature.should_invoke` 依据 `task_context['download_type']` 推导 `after_album` / `after_photo`；`_adapt_plugin_kwargs` 自动补 `filename_rule`（album: `[JM{Aid}]{Atitle}`，photo: `[JM{Pid}]{Ptitle}`）与 `zip_dir`/`pdf_dir`/`img_dir`（=`option.dir_rule.base_dir`）。

### 3.8 其他

- `record_download_duration(context_key, clock=None)`：装饰器，通过 `jm_task_context` 做“顶层负责计时、内层复用”，最终写 `detail.duration`。context_key 取值：`album_started_at` / `photo_started_at` / `image_started_at`。
- `JmDownloader.use()` / `JmAsyncDownloader.use()`：类方法，替换 `JmModuleConfig.CLASS_DOWNLOADER` / `CLASS_ASYNC_DOWNLOADER`，并打印替换前后的类。
- `with JmDownloader(option) as dler:` 支持；`__exit__` 在异常时记 `dler.exception`。
- `JmAsyncDownloader`：`__init__(option, image_concurrency=None, photo_concurrency=None, decode_worker=None)`，从 `option.download.threading.*` 取并发数（`<=0` 抛 `ValueError`），用 `asyncio.Semaphore` 限流 + `ThreadPoolExecutor(max_workers=decode_worker, thread_name_prefix='jm-async-decode')` 卸载解密；`async with` 进入/退出，`__aexit__` 关闭线程池；钩子为 `async def before_album/...`（会 await `super()` 链）。

---

## 4. 插件系统

文件：`src/jmcomic/jm_plugin.py`（2157 行，21 个内置插件）。

### 4.1 基类 `JmOptionPlugin`

```python
class JmOptionPlugin:
    plugin_key: str
    plugin_dependencies: tuple = ()      # 元素: 'psutil' 或 ('import_name', 'pip-pkg-name')

    @classmethod
    def required_dependencies_for(cls, kwargs: dict) -> tuple:
        return cls.plugin_dependencies          # 可按 kwargs 动态收窄

    @classmethod
    def parse_dependency_spec(cls, dep) -> Tuple[str, str]

    @classmethod
    def check_plugin_dependency(cls, kwargs: dict, strategy: str = 'failed-fast') -> None

    @classmethod
    def install_missing_dependencies(cls, pip_packages: List[str]) -> None

    def __init__(self, option: JmOption):
        self.option = option
        self.log_enable = True
        self.delete_original_file = False

    def invoke(self, **kwargs) -> None:
        raise NotImplementedError

    @classmethod
    def build(cls, option: JmOption) -> 'JmOptionPlugin':
        return cls(option)

    def log(self, msg, topic=None):
        if not self.log_enable: return
        jm_log(topic=f'plugin.{self.plugin_key}' + (f'.{topic}' if topic else ''), msg=msg)

    def require_param(self, case: Any, msg: str):
        if case: return
        raise PluginValidationException(self, msg)

    def warning_lib_not_install(self, lib: str, throw=False)
    def execute_deletion(self, paths: List[str])       # 仅当 self.delete_original_file 为真才删
    def execute_cmd(self, cmd)                         # os.system
    def execute_multi_line_cmd(self, cmd: str)         # subprocess.run(shell=True, check=True)
    def enter_wait_list(self) / leave_wait_list(self)  # option.need_wait_plugins 增删
    def wait_until_finish(self)                        # 空实现，异步插件覆写

    def decide_filepath(self, album, photo, filename_rule, suffix, base_dir, dir_rule_dict) -> str
```

`decide_filepath` 的优先级（zip / img2pdf / long_img / download_cover / calibre_metadata 共用）：

```python
def decide_filepath(self, album, photo, filename_rule, suffix, base_dir, dir_rule_dict):
    if album is None:
        album = photo.from_album                  # 防止 Axx 规则在 photo 场景报错
    if dir_rule_dict is not None:                 # 最高优先级
        dir_rule = DirRule(**dir_rule_dict)
        filepath = dir_rule.apply_rule_to_path(album, photo)
        base_dir = os.path.dirname(filepath)
    else:
        base_dir = base_dir or os.getcwd()
        filepath = os.path.join(
            base_dir,
            DirRule.apply_rule_to_filename(album, photo, filename_rule) + fix_suffix(suffix))
    mkdir_if_not_exists(base_dir)
    return fix_filepath(filepath)
```

- `AlbumValidationException` 之外，唯一的专用异常是 `PluginValidationException(plugin, msg)`（`__init__` 保存 `self.plugin` / `self.msg`）。

### 4.2 注册与分发（替代 `PluginKeys` 的机制）

```python
# jm_config.py
REGISTRY_PLUGIN = {}                        # plugin_key -> class

@classmethod
def register_plugin(cls, plugin_class):
    ExceptionTool.require_true(getattr(plugin_class, 'plugin_key', None) is not None,
                               f'未配置plugin_key, class: {plugin_class}')
    cls.REGISTRY_PLUGIN[plugin_class.plugin_key] = plugin_class

# __init__.py —— 自动扫描本包 globals() 中所有 JmOptionPlugin 子类并注册
gb = dict(filter(lambda pair: isinstance(pair[1], type), globals().items()))
register_jmcomic_component(gb, JmModuleConfig.register_plugin, JmOptionPlugin)
# 同理注册同步/异步客户端
```

自定义插件官方写法（`tutorial/6_plugin.md`）：

```python
from jmcomic import JmOptionPlugin, JmModuleConfig

class MyPlugin(JmOptionPlugin):
    plugin_key = 'myplugin'
    def invoke(self, word) -> None:
        print(word)

JmModuleConfig.register_plugin(MyPlugin)
```

### 4.3 yml 注册形态与事件名

```yaml
plugins:
  valid: log
  dependencies_strategy: failed-fast
  after_init:      [ {plugin: ..., kwargs: {...}} ]
  main:            [...]
  before_album:    [...]
  after_album:     [...]
  before_photo:    [...]
  after_photo:     [...]
  before_image:    [...]
  after_image:     [...]
```

- 内置事件：`after_init`（option 创建后）、`main`（CLI 主流程/导出场景）、`before_album` / `after_album` / `before_photo` / `after_photo` / `before_image` / `after_image`。
- **事件名就是 `plugins:` 下的一级 key，没有枚举校验**：`option.call_all_plugin('my_event')` 也能工作，因此复刻时不要硬编码事件白名单（`check_plugins_dependencies` 只跳过非 list 值，如 `valid` / `dependencies_strategy` 两个 str 值）。
- 插件条目字段：`plugin`(必填) / `kwargs` / `log` / `safe` / `valid`（见 1.4 表）。

### 4.4 依赖管理

`check_plugin_dependency` 在 `JmOption.__init__` 阶段（`after_init` 之前）由 `check_plugins_dependencies()` 触发：

| strategy | 行为 |
|---|---|
| `failed-fast`（默认） | `ExceptionTool.raises` 并输出 3 条解决方案（单独 pip install / `pip install jmcomic[plugins]` / 改 `dependencies_strategy`） |
| `auto-install` | `cls.install_missing_dependencies(pip_names)`：`[sys.executable, '-m', 'pip', 'install', ...]`，成功则 `importlib.invalidate_caches()`；失败严格抛错 |
| `ignore-only-log` | `jm_log(topic=f'plugin.{key}.dependency', ...)` 仅告警 |

检查用 `importlib.util.find_spec(import_name)`。动态收窄的例子：`ZipPlugin.required_dependencies_for`（未加密 → `()`；`impl == '7z'` → `('py7zr',)`；否则 → `('pyzipper',)`），`Img2pdfPlugin.required_dependencies_for`（`encrypt` 为真时追加 `('pikepdf',)`）。

### 4.5 内置插件速查（21 个）

| plugin_key | 类 | 依赖 | 典型事件 | 主要 kwargs | 功能 |
|---|---|---|---|---|---|
| `login` | `JmLoginPlugin` | — | `after_init`/`main` | `username`(必填), `password`(必填), `impl` | `client.login()` 后 `option.update_cookies(dict(client['cookies']))`，使后续所有 client 带登录态 |
| `usage_log` | `UsageLogPlugin` | `psutil` | `after_init` | `interval`, `enable_warning` | 起 daemon 线程实时打印 CPU/内存等硬件占用；线程留痕在 `option.thread_usage_log` |
| `find_update` | `FindUpdatePlugin` | — | `after_init` | 形如 `{本子id: 章节id}`（即 `**kwargs`） | 自定义 `FindUpdateDownloader(JmDownloader).do_filter`，只下载指定章节之后的新章；通过 `api.download_album(..., downloader=FindUpdateDownloader)` 触发 |
| `zip` | `ZipPlugin` | 按 encrypt 动态 | `after_album` / `after_photo` | `zip_dir='./'`, `filename_rule='Ptitle'`, `suffix='zip'`, `dir_rule=None`, `delete_original_file=False`, `encrypt=None`, `level=None`(废弃) | 打包下载结果。**打包粒度由所在钩子自动推导**（`level = 'album' if album is not None else 'photo'`）。`encrypt` 为 dict：`{password: xxx}` 或 `{type: random}`；`{impl: '7z', ...}` 走 py7zr（`FILTER_COPY` + `header_encryption=True`），否则 pyzipper AES-128（随机密码写入 zip comment）。压缩后 `execute_deletion` 原文件与空目录 |
| `client_proxy` | `ClientProxyPlugin` | — | `after_init` | `proxy_client_key`(必填), `whitelist`, 其余透传代理类构造 | monkey-patch `option.new_jm_client`，把 client 包成代理类（如 `photo_concurrent_fetcher_proxy`），`whitelist` 命中才代理 |
| `image_suffix_filter` | `ImageSuffixFilterPlugin` | — | `after_init` | `allowed_orig_suffix`（list，如 `['.gif']`） | monkey-patch `option.decide_download_cache`：后缀不在集合内则 `image.skip = True` |
| `send_qq_email` | `SendQQEmailPlugin` | — | `after_album`/`main` | `msg_from`,`msg_to`,`password`,`title`,`content`（前三个必填），可选 `album`,`downloader` | 用 `common.EmailConfig` 发 QQ 邮件 |
| `log_topic_filter` | `LogTopicFilterPlugin` | — | `after_init` | `whitelist` | 给 `jm_logger` 加 `logging.Filter`，只保留白名单 topic；重复调用会先移除旧的同类 filter |
| `download_progress` | `DownloadProgressPlugin` | `rich` | `after_init` | `log_file='jmcomic-download.log'`, `terminal_log_lines=6` | 把默认 Downloader 换成 `ProgressDownloader` / `AsyncProgressDownloader`，普通日志重定向到文件，终端显示 rich 两级进度条；非交互终端退化为结束后汇总 |
| `auto_set_browser_cookies` | `AutoSetBrowserCookiesPlugin` | `browser_cookie3` | `after_init` | `browser`(必填), `domain`(必填) | 读浏览器 cookies，只保留 `{yuo1, remember_id, remember}` 后 `option.update_cookies` |
| `favorite_folder_export` | `FavoriteFolderExportPlugin` | — | `main` | `save_dir`(默认 `cwd/export/`), `zip_enable=False`, `zip_filepath`, `zip_password`, `delete_original_file=False`, `max_retry=2` | 导出全部收藏夹为 CSV（表头 `id,author,name`，文件名 `【{fid}】{fname}.csv`）；一个收藏夹一线程；失败按 `min(2**attempt, 10)` 退避重试；可 zip/7z 加密打包；失败收藏夹最后统一抛错 |
| `img2pdf` | `Img2pdfPlugin` | `img2pdf`（+`pikepdf`） | `after_photo` / `after_album` | `pdf_dir=None`, `filename_rule='Pid'`, `dir_rule=None`, `delete_original_file=False`, `encrypt=None` | 把章节（或整本）图片合并为一个 PDF；`encrypt.password` 用 pikepdf 加密 |
| `long_img` | `LongImgPlugin` | `PIL` | `after_photo` / `after_album` | `img_dir=None`, `filename_rule='Pid'`, `dir_rule=None`, `delete_original_file=False` | 把图片纵向拼成长图（按最小宽度等比缩放，LANCZOS），保存为 png |
| `jm_server` | `JmServerPlugin` | `jm-view-server`（旧包 `plugin_jm_server`） | `after_init`/`main` | `password=''`, `base_dir`(默认 `option.dir_rule.base_dir`), `run`(默认 `{host:'0.0.0.0', port:'80', debug:False}`), 其余透传 `JmServer(...)` | 起 Flask 服务浏览本子；非 debug 模式新线程 + `atexit` 注册停机；`build` 为**单例**（`single_instance` + `single_instance_lock`）；debug 模式强制主线程且 `use_reloader=False` |
| `subscribe_album_update` | `SubscribeAlbumUpdatePlugin` | — | `after_init`/`main` | `album_photo_dict`(必填, `{album_id: photo_id}`), `email_notify`, `download_if_has_update=True`, `auto_update_after_download=True` | 检查是否有新章 → 可选发邮件 → `option.download_photo(new_list)` → 回写 `album_photo_dict` 并 `option.to_file()` 持久化 |
| `skip_photo_with_few_images` | `SkipPhotoWithFewImagesPlugin` | — | `before_photo` / `before_image` | `at_least_image_count`(必填) | 图片数少于阈值时 `photo.skip = True`；`build` 带 `@field_cache()` 单例 |
| `delete_duplicated_files` | `DeleteDuplicatedFilesPlugin` | — | `after_album` | `limit`(必填), `delete_original_file=True` | 对 `dir_rule.decide_album_root_dir(album)` 递归算 MD5，出现次数 ≥ `limit` 的文件打印/删除 |
| `replace_path_string` | `ReplacePathStringPlugin` | — | `after_init` | `replace: {old: new}` | monkey-patch `option.decide_image_save_dir`，对路径做字符串替换 |
| `advanced_retry` | `AdvancedRetryPlugin` | — | `after_init` | `retry_config: {retry_rounds: N, retry_domain_max_times: M}` | 作为 `domain_retry_strategy` 注入 `option.new_jm_client`；按历史失败次数给域名排序后轮询重试；`__call__(client, *args, **kwargs)` 在无 args 时做 client 初始化（`domain_req_failed_counter` / `domain_counter_lock`） |
| `download_cover` | `DownloadCoverPlugin` | — | `before_album` | `dir_rule`(必填 dict), `size=''`（如 `'_3x4'`） | 用 `decide_filepath` 定位后 `downloader.client.download_album_cover(album_id, save_path, size)`；`option.download.cache` 且文件存在则跳过 |
| `calibre_metadata` | `CalibreMetadataPlugin` | `jmcomic-calibre` | `after_album` | `dir_rule`(必填 dict), `include_cover=False`, `fields=None` | 生成 Calibre `metadata.opf`（identifier 固定 `jmcomic:{album_id}`）；`fields` 按 Dublin Core 元素写入，不支持的 key 走 `on_ignored` 告警；`include_cover` 需 `downloader` |

---

## 5. JmConfig

文件：`src/jmcomic/jm_config.py`。

### 5.1 日志设施

```python
jm_logger = logging.getLogger('jmcomic')          # 全库唯一 logger

class JmLogFormatter(logging.Formatter):          # 注入 record.jm_task_context_prefix
    # 从 task_context 提取 task_id / download_type / jm_id
    # 无 task_id 时不打印前缀
def setup_default_jm_logger()                     # 无 handler 时加 StreamHandler(stdout) + INFO
def default_jm_logging(topic, msg, e=None)        # 默认 EXECUTOR_LOG；支持 jm_log(topic, e) 简写

class PrettyFormatter(JmLogFormatter)             # 按 topic 前缀着色（album/photo/image/plugin/req/api）
def enable_pretty_log()                           # 清空 handler，装 PrettyFormatter；win32 开启 VT100
```

`JmModuleConfig.jm_log(cls, topic, msg, e=None)`：
- `FLAG_ENABLE_JM_LOG` 为假直接返回；
- 调 `EXECUTOR_LOG(topic, msg)` 或 `(topic, msg, e)`；通过 `inspect.signature` 数参数个数判断；若自定义 executor 只接受 2 个参数则发 `warnings.warn`（兼容旧签名）。

日志格式：`'[%(asctime)s] [%(threadName)s]:%(jm_task_context_prefix)s【%(topic)s】%(message)s'`。

### 5.2 `JmMagicConstants`（协议层常量）

- 排序：`ORDER_BY_LATEST='mr'` / `VIEW='mv'` / `PICTURE='mp'` / `LIKE='tf'` / `SCORE='tr'` / `COMMENT='md'`；收藏夹：`ORDER_FF_FAVORITE_TIME='mr'` / `ORDER_FF_UPDATE_TIME='mp'`；排行：`ORDER_MONTH_RANKING='mv_m'` / `WEEK='mv_w'` / `DAY='mv_t'`。
- 时间段：`TIME_TODAY='t'` / `WEEK='w'` / `MONTH='m'` / `ALL='a'`。
- 分类：`CATEGORY_ALL='0'` / `DOUJIN='doujin'` / `SINGLE='single'` / `SHORT='short'` / `ANOTHER='another'` / `HANMAN='hanman'` / `MEIMAN='meiman'` / `DOUJIN_COSPLAY='doujin_cosplay'` / `CATEGORY_3D='3D'` / `ENGLISH_SITE='english_site'`；副分类 `SUB_*`。
- 图片分割：`SCRAMBLE_220980` / `SCRAMBLE_268850` / `SCRAMBLE_421926`。
- 移动端密钥：`APP_TOKEN_SECRET='185Hcomic3PAPP7R'` / `APP_TOKEN_SECRET_2='18comicAPPContent'` / `APP_DATA_SECRET='185Hcomic3PAPP7R'` / `API_DOMAIN_SERVER_SECRET='diosfjckwpqpdfjkvnqQjsik'` / `APP_VERSION='2.1.7'`。

### 5.3 `JmModuleConfig`（模块级配置中心）

**站点常量**

| 名称 | 值/含义 |
|---|---|
| `PROT` | `'https://'` |
| `JM_REDIRECT_URL` | `https://jm365.work/3YeBdF`（永久网域，用于动态探测可用域名） |
| `JM_PUB_URL` | `https://jmcomicgo.org`（发布页，用于枚举全部域名） |
| `JM_CDN_IMAGE_URL_TEMPLATE` | `https://cdn-msp.{domain}/media/photos/{photo_id}/{index:05}{suffix}`（index 从 1 开始，5 位零填充） |
| `JM_IMAGE_SUFFIX` | `['.jpg', '.webp', '.png', '.gif']` |
| `JM_ERROR_RESPONSE_TEXT` | 错误文案 → 中文说明（mysql 报错 / Restricted Access） |
| `JM_ERROR_STATUS_CODE` | `{403, 500, 520, 524}` → 中文说明 |
| `PAGE_SIZE_SEARCH` / `PAGE_SIZE_FAVORITE` | `80` / `20` |
| `SCRAMBLE_CACHE` | `{}` |
| `DEFAULT_AUTHOR` | `'default_author'` |
| `APP_COOKIES` | `None` |

**域名**

| 名称 | 值 |
|---|---|
| `DOMAIN_IMAGE_LIST` | `shuffled(...)`：`cdn-msp.jmapiproxy1.cc`、`cdn-msp.jmapiproxy2.cc`、`cdn-msp2.jmapiproxy2.cc`、`cdn-msp3.jmapiproxy2.cc`、`cdn-msp.jmapinodeudzn.net`、`cdn-msp3.jmapinodeudzn.net`（每次 import 随机打乱） |
| `DOMAIN_API_LIST` | `shuffled(...)`：`www.cdnhjk.net`、`www.cdngwc.cc`、`www.cdngwc.net`、`www.cdngwc.club` |
| `DOMAIN_API_UPDATED_LIST` | `None`（运行时从域名服务器拉取后填充） |
| `API_URL_DOMAIN_SERVER_LIST` | 3 个 `tos-*bytepluses.com` 的 `newsvr-2025.txt` 地址（`shuffled`） |
| `DOMAIN_HTML` / `DOMAIN_HTML_LIST` | `None` / `None`；由 `get_html_domain()` / `get_html_domain_all()` 运行时获取并 `@field_cache` 缓存 |

域名相关方法：`get_html_domain(postman=None)`（访问永久网域→`JmcomicText.parse_to_jm_domain`）、`get_html_url`、`get_html_domain_all`（解析发布页，`JmcomicText.analyse_jm_pub_html`）、`get_html_domain_all_via_github`（**已废弃**，`DeprecationWarning` 后转发到 `get_html_domain_all`）、`new_html_headers(domain='18comic.vip')`。

**Headers 模板**：`APP_HEADERS_TEMPLATE`（Android UA）、`APP_HEADERS_IMAGE`（`X-Requested-With: com.JMComic3.app` 等）、`HTML_HEADERS_TEMPLATE`（Chrome UA + sec-ch-ua 系列）。

**班级注册表 / 可替换类**

```python
CLASS_DOWNLOADER = None          # 默认解析为 jm_downloader.JmDownloader
CLASS_ASYNC_DOWNLOADER = None    # 默认 JmAsyncDownloader
CLASS_OPTION = None              # 默认 JmOption
CLASS_ALBUM = None / CLASS_PHOTO = None / CLASS_IMAGE = None   # 默认 jm_entity 对应类

REGISTRY_CLIENT = {}             # client_key -> 类   ('api' / 'html' / 'photo_concurrent_fetcher_proxy')
REGISTRY_ASYNC_CLIENT = {}       # client_key -> 类   ('async_api')
REGISTRY_PLUGIN = {}             # plugin_key -> 类
REGISTRY_EXCEPTION_LISTENER = {} # 异常类 -> listener(e)

# 访问器
downloader_class() / async_downloader_class() / option_class()
album_class() / photo_class() / image_class()
client_impl_class(client_key)         # 找不到 → ExceptionTool.raises
async_client_impl_class(client_key)
register_client / register_async_client / register_plugin / register_exception_listener
```

**行为开关（FLAG_\* / VAR_\*）**

| 名称 | 默认 | 含义 |
|---|---|---|
| `FLAG_USE_FIX_TIMESTAMP` | `True` | token 时间戳固定 |
| `FLAG_API_CLIENT_REQUIRE_COOKIES` | `True` | api client 初始化需 cookies（不校验内容） |
| `FLAG_API_CLIENT_AUTO_UPDATE_DOMAIN` | `True` | 自动更新 api 域名 |
| `FLAG_ENABLE_JM_LOG` | `True` | 日志总开关（被 `log: false` 关闭） |
| `FLAG_DECODE_URL_WHEN_LOGGING` | `True` | 日志里解码 URL |
| `FLAG_USE_VERSION_NEWER_IF_BEHIND` | `True` | 内置 app 版本落后时用最新 |
| `FLAG_DUMP_HTML_ON_REGEX_ERROR` | `False` | 正则失败时把响应文本落盘到 `jmcomic_debug/` |
| `AFIELD_ADVICE` / `PFIELD_ADVICE` | `{}` | 自定义 dir_rule 字段：`AFIELD_ADVICE['myname'] = lambda album: '...'` → 可用 `Amyname` |
| `VAR_FILE_NAME_LENGTH_LIMIT` | `100` | `Errno 36` 时文件名截断长度 |
| `VAR_LOG_FMT` | 见 5.1 | 默认格式串 |
| `EXECUTOR_LOG` | `default_jm_logging` | 日志执行函数 |

**Option 默认值相关**

```python
JM_OPTION_VER = '2.1'
DEFAULT_CLIENT_IMPL = 'api'
DEFAULT_CLIENT_CACHE = None
DEFAULT_PROXIES = ProxyBuilder.system_proxy()

DEFAULT_OPTION_DICT = {
    'log': None,
    'dir_rule': {'rule': 'Bd_Pname', 'base_dir': None, 'normalize_zh': None},
    'download': {
        'cache': True,
        'image': {'decode': True, 'suffix': None},
        'threading': {'image': 30, 'photo': None},
    },
    'client': {
        'cache': None,
        'domain': [],
        'postman': {
            'type': 'curl_cffi',
            'meta_data': {'impersonate': 'chrome', 'headers': None, 'proxies': None},
        },
        'impl': None,
        'async_impl': 'async_api',
        'retry_times': 5,
    },
    'plugins': {
        'valid': 'log',
        'dependencies_strategy': 'failed-fast',
    },
}

@classmethod
def option_default_dict(cls) -> dict:
    option_dict = deepcopy(cls.DEFAULT_OPTION_DICT)
    if option_dict['log'] is None:            option_dict['log'] = cls.FLAG_ENABLE_JM_LOG
    if dir_rule['base_dir'] is None:          dir_rule['base_dir'] = os.getcwd()
    if client['cache'] is None:               client['cache'] = cls.DEFAULT_CLIENT_CACHE
    if client['impl'] is None:                client['impl'] = cls.DEFAULT_CLIENT_IMPL
    if meta_data['proxies'] is None:          meta_data['proxies'] = cls.DEFAULT_PROXIES
    if dt['photo'] is None:                   dt['photo'] = os.cpu_count()
    return option_dict
```

其他：`new_postman(session=False, **kwargs)`（默认 `impersonate='chrome'`、html headers、`DEFAULT_PROXIES`）；`get_fix_ts_token_tokenparam()`（`@field_cache()` 的单次 token）；`setup_default_jm_logger()` 在模块底部被调用；模块级别名 `jm_log = JmModuleConfig.jm_log`、`disable_jm_log = JmModuleConfig.disable_jm_log`。

---

## 6. 文件名/目录名格式化辅助

### 6.1 `common`（外部依赖，复刻时要么引入要么内联）

```python
_win_forbid_char = list('\\/:*?"<>|\n\t\r')

def fix_windir_name(dn: str, attr_char='_') -> str:
    name = ''.join(attr_char if c in _win_forbid_char else c for c in dn)
    if name.endswith('.'):
        name = name.rstrip('.')
    return name

def fix_filepath(filepath: str, *args, **kwargs) -> str:
    filepath = filepath.replace('\\', '/').replace('//', '/')
    if os.path.isdir(filepath):
        return filepath if filepath[-1] == '/' else filepath + '/'
    return filepath

def fix_suffix(suffix: str) -> str:      # 保证以 '.' 开头：'png' -> '.png'
    return suffix if suffix[0] == '.' else f'.{suffix}'

def mkdir_if_not_exists(dirpath); def file_exists(fp); def file_not_exists(fp)
def files_of_dir(abs_dir_path) -> List[str]      # 排序后的绝对路径列表，目录带尾 '/'
def of_file_name(filepath, trim_suffix=False) -> str
```

`AdvancedDict` 的关键语义（`common/util/json_util.py`）：`__getattr__` = `wrap_value(self._data[item])`；`__setattr__` 非 `_data` 时直接写 `_data`；list/tuple 中的 dict 元素自动包装；`src_dict` 取原始 dict。**缺失 key 抛 `KeyError`**。

`PackerUtil`（`common/base/packer.py`）：`mode_yml='yml'` / `mode_json='json'` / `mode_py_pickle='pickle'`；`unpack(filepath) -> (obj, packer)`（用 `[0]` 取数据）；`pack(obj, filepath, packer=None)`；`unpack_by_str(text, mode, clazz=None)`。

`multi_thread_launcher(iter_objs, apply_each_obj_func, wait_finish=True, batch_size=None, pause_duration=-1, flag=None, **metadata)`；`thread_pool_executor(iter_objs, apply_each_obj_func, wait_finish=True, max_workers=None, flag=None)`（`max_workers` 默认 `min(32, cpu+4)`，内部 worker 捕获 `BaseException` 只打印 traceback）。

`field_cache(field_name=None, sentinel=None, obj=None)`：把首次调用结果缓存到实例（或类）属性上；`JmOption.build_jm_client`、`JmModuleConfig.get_html_domain`、`SkipPhotoWithFewImagesPlugin.build`、`JmServerPlugin.build` 用它做单例/缓存。

### 6.2 `JmcomicText`（`jm_toolkit.py`）中与命名/路径相关的部分

| 方法 | 行为 |
|---|---|
| `parse_dsl_text(dsl_text)` | 依次应用 `dsl_replacer` 中注册的正则替换。**唯一注册项**：模块底部 `JmcomicText.dsl_replacer.add_dsl_and_replacer(r'\$\{(.*?)\}', JmcomicText.match_os_env)` → 支持 `${ENV_NAME}`，未配置则 `ExceptionTool.require_true` 抛「未配置环境变量: NAME」 |
| `parse_to_abspath(dsl_text)` | `os.path.abspath(parse_dsl_text(dsl_text))` —— `DirRule.__init__` 用它处理 `base_dir` |
| `to_zh(s, target=None)` | `zhconv.convert(s, target)`；`target` 为 `None`/空直接返回原串；缺 `zhconv` 或异常时 `jm_log('zhconv.error', ...)` 并回退原串 |
| `to_zh_cn(s)` | `to_zh(s, 'zh-cn')`（兼容旧接口） |
| `try_mkdir(save_dir)` | `mkdir_if_not_exists`；捕获 `OSError` 且 `errno == 36`（File name too long）时截断到 `JmModuleConfig.VAR_FILE_NAME_LENGTH_LIMIT`（100）后递归重试；返回实际使用的 `save_dir` |
| `parse_orig_album_name(name, default=None)` | `tokenize` 后取第一个**不以括号开头**的词；否则返回 `default`。这就是 `Aoname` / `Poname` 的来源（**不存在 `format_album_name`**） |
| `tokenize(title)` | 按 `() [] 【】 （）` 配对切分，返回词列表。例：`'繞道#2 [暴碧漢化組] [えーすけ（123）] よりみち#2 (COMIC 快樂天 2024年1月號) [中國翻譯] [DL版]'` → `['繞道#2', '[暴碧漢化組]', '[えーすけ（123）]', 'よりみち#2', '(COMIC 快樂天 2024年1月號)', '[中國翻譯]', '[DL版]']` |
| `limit_text(text, limit)` | 超长时截断并追加 `...({剩余长度}` |
| `parse_to_jm_id(text)` | `43210` / `JM43210` / `jm43210` / URL（`/photo/412038`、`?id=412038`）→ 纯数字串 |
| `parse_to_jm_domain(text)` | 从 `https://xxx` 提取域名 |
| `format_url(path, domain)` / `format_album_url(aid, domain='18comic.vip')` | 拼 URL |
| `get_album_cover_url(album_id, image_domain=None, size='')` | 封面 URL，`image_domain` 缺省时从 `DOMAIN_IMAGE_LIST` 随机取 |
| `compare_versions(v1, v2)` | 逐段整数比较 |

**命名/路径净化链（复刻时保持一致的顺序）**

```
DirRule 每段：str(raw) → to_zh(target=normalize_zh) → fix_windir_name() → .strip()
最终：        '/'.join(segments) → fix_filepath()
图片文件名：  fix_windir_name(option.decide_image_filename(image)) + option.decide_image_suffix(image)
插件文件名：  fix_windir_name(DirRule.apply_rule_to_filename(album, photo, rule)).strip() + fix_suffix(suffix)
目录创建：    JmcomicText.try_mkdir(...) / mkdir_if_not_exists(...)
```

`fix_windir_name` 只替换 `\ / : * ? " < > | \n \t \r`，并去掉结尾的 `.`；**不限制长度、不处理保留字（CON 等）**。长度限制只在 `JmcomicText.try_mkdir` 的 `Errno 36` 分支里做。

---

## 7. 复刻检查清单（契约级）

1. `JmOption.__init__(dir_rule, download, client, plugins, filepath=None, call_after_init_plugin=True)`，`after_init` 默认触发。
2. `construct` 必须 pop 掉 `log` 与 `version` 再 `cls(**dic)`；`log` 支持 `True/False/'pretty'`。
3. `DEFAULT_OPTION_DICT` 的 9 个运行时填充点（log / base_dir / client.cache / client.impl / proxies / threading.photo）一个都不能少，否则 `AdvancedDict` 会抛 `KeyError`。
4. `plugins.valid`、`plugins.dependencies_strategy` 与事件 key 同处一个 dict —— 遍历插件列表时必须 `isinstance(plist, list)` 过滤。
5. `DirRule.split_rule_dsl` 的 `/` → `_` 优先级、自动补 `Bd`、`Bd` 段不净化。
6. `apply_rule_to_path(only_album_rules=True)` 时只保留 `Bd` 与 `A*`。
7. 图片下载流程里 **缓存命中也要触发 `after_image`**，且 `before_image` 在 `image.skip` 判定之前。
8. `catch_exception` 收集失败但**继续 re-raise**；`all_success` 在 filter 场景会为 False。
9. 线程模型：`count_batch >= count_real` → 一对象一线程；否则线程池。
10. 插件 extra（`album`/`photo`/`image`/`downloader`）**覆盖** kwargs。
11. `${ENV}` 展开同时发生在 `dir_rule.base_dir`（`parse_to_abspath`）和插件 kwargs（`fix_kwargs` → `parse_dsl_text`）。
12. 事件名不做白名单校验，`call_all_plugin` 对任意 group 都可用。

---

## 附：本次读取的文件

**成功读取**

| 路径 | 用途 |
|---|---|
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_option.py` | 全文（812 行） |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_downloader.py` | 全文（611 行） |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_config.py` | 全文（679 行） |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_plugin.py` | 结构 + 关键段落（基类 1-300、394-753、1080-1539、1533-1642、1643-1792、1780-2029、2063-2157） |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_toolkit.py` | 结构 + 1-120、255-454 |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_entity.py` | 结构 + 62-116、120-279、303-422、465-544 |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_exception.py` | `ExceptionTool` 相关段（97-240） |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_feature.py` | 全文（173 行） |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_task_context.py` | 全文 |
| `mccms/recon/jmcomic-ref/src/jmcomic/api.py` | 全文 |
| `mccms/recon/jmcomic-ref/src/jmcomic/__init__.py` | 全文（注册机制） |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_async_downloader.py` | 1-80 + 结构 |
| `mccms/recon/jmcomic-ref/src/jmcomic/jm_client_impl.py` / `jm_client_interface.py` / `jm_async_client.py` | 仅 grep（client_key / 方法签名） |
| `/tmp/recon/jmcomic/assets/option/option_test_api.yml` | 全文 |
| `/tmp/recon/jmcomic/assets/option/option_test_html.yml` | 全文 |
| `/tmp/recon/jmcomic/assets/option/option_workflow_download.yml` | 全文 |
| `/tmp/recon/jmcomic/assets/option/option_workflow_export_favorites.yml` | 全文 |
| `/tmp/recon/jmcomic/assets/docs/sources/option_file_syntax.md` | 全文（371 行，官方配置指南） |
| `/tmp/recon/jmcomic/assets/docs/sources/tutorial/6_plugin.md` | 全文（212 行） |
| `~/Library/Python/3.9/lib/python/site-packages/common/**` | `fix_windir_name` / `fix_filepath` / `fix_suffix` / `files_of_dir` / `AdvancedDict` / `multi_thread_launcher` / `thread_pool_executor` / `PackerUtil` / `Postmans` |

**无法读取 / 不存在**

| 目标 | 情况 |
|---|---|
| `jmcomic.PluginKeys`、`PluginBase`、`PluginSettings`、`plugin_decide` | 全仓库不存在（已对 `*.py` / `*.md` grep）。属于旧版本 API。 |
| `jm_toolkit.format_album_name` | 不存在；对应功能是 `JmcomicText.parse_orig_album_name` / `tokenize`。 |
| `/tmp/recon/jmcomic/assets/option/` 下的“默认 option 模板” | 只有 4 个 workflow/test 用 yml，无默认全量模板；默认值以 `JmModuleConfig.DEFAULT_OPTION_DICT` 为准。 |
