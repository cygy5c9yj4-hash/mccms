"""
HTTP 请求层（对应 jmcomic 使用的 common.Postmans）。

- 默认基于 requests.Session（保持 cookie / 连接复用）
- 若安装了 curl_cffi 且 meta_data.impersonate / type 指定，则用 curl_cffi
- 内置域名轮换与重试：单个域名失败次数用尽后切换到下一个域名
- 支持 host -> ip 解析覆盖（等价 curl --resolve），见 register_dns_override
- 未显式配置 proxies 时，会沿用 requests 的默认行为（含系统代理设置）
"""

import json
import time
from copy import deepcopy
from typing import Any, Dict, List, Optional

from .mc_config import McModuleConfig, mc_log
from .mc_exception import ExceptionTool
from .mc_toolkit import AdvancedDict

__all__ = ['McResp', 'McPostman', 'register_dns_override', 'clear_dns_override']


# --------------------------------------------------------------------------------------
# DNS 解析覆盖（等价 curl --resolve）
# --------------------------------------------------------------------------------------

# host -> ip。为空时完全不生效，也不打补丁。
_DNS_OVERRIDE: Dict[str, str] = {}
_DNS_PATCHER_INSTALLED = False


def register_dns_override(mapping: Optional[Dict[str, str]]):
    """
    注册 host -> ip 的解析覆盖（等价 curl --resolve）。

    典型用途：
    - 站点有多个镜像 / 想让某域名固定走某个 CDN 节点
    - 本机 DNS 对某域名返回的地址不可用，而你知道可用的地址

    实现方式与 curl --resolve 一致：TCP 连接改连到指定 IP，
    但 TLS SNI 与 HTTP Host 仍是原域名，证书校验与虚拟主机路由都不受影响。

    可选配置::

        client:
          postman:
            meta_data:
              resolve:
                example.com: 1.2.3.4

    注意：这是进程级设置（需要挂钩底层 socket 创建），对整个进程生效。
    """
    if not mapping:
        return
    _DNS_OVERRIDE.update({str(k).strip().lower(): str(v).strip()
                          for k, v in mapping.items() if k and v})
    _install_dns_patcher()


def clear_dns_override():
    _DNS_OVERRIDE.clear()


def _install_dns_patcher():
    global _DNS_PATCHER_INSTALLED
    if _DNS_PATCHER_INSTALLED:
        return

    try:
        import urllib3.util.connection as urllib3_connection
    except ImportError:  # pragma: no cover
        return

    original_create_connection = urllib3_connection.create_connection

    def create_connection_with_override(address, *args, **kwargs):
        try:
            host, port = address[0], address[1]
        except (TypeError, IndexError):  # pragma: no cover
            return original_create_connection(address, *args, **kwargs)

        target = _DNS_OVERRIDE.get(str(host).lower())
        if target:
            address = (target, port)

        return original_create_connection(address, *args, **kwargs)

    urllib3_connection.create_connection = create_connection_with_override
    _DNS_PATCHER_INSTALLED = True
    mc_log('req.resolve', f'已启用 DNS 解析覆盖: {_DNS_OVERRIDE}')


class McResp:
    """对底层 HTTP 响应的统一封装。"""

    def __init__(self, resp, url: str, site: str):
        self._resp = resp
        self.url: str = url
        self.site: str = site

    # ------------------------------------------------------------------ 基础

    @property
    def http_code(self) -> int:
        return int(getattr(self._resp, 'status_code', 0))

    @property
    def is_success(self) -> bool:
        return 200 <= self.http_code < 400

    @property
    def is_not_success(self) -> bool:
        return not self.is_success

    @property
    def text(self) -> str:
        text = getattr(self._resp, 'text', None)
        if text is None:
            content = getattr(self._resp, 'content', b'')
            text = content.decode('utf-8', errors='replace')
        return text

    @property
    def content(self) -> bytes:
        return getattr(self._resp, 'content', b'')

    @property
    def cookies(self) -> Dict[str, str]:
        try:
            return dict(self._resp.cookies)
        except Exception:
            return {}

    # ------------------------------------------------------------------ 解析

    def json(self) -> Dict:
        try:
            return json.loads(self.text)
        except Exception as e:
            ExceptionTool.raises(
                f'响应不是合法 JSON: [{self.url}]，异常: [{e}]，'
                f'内容片段: [{self.text[:200]}]',
                {'resp': self},
            )

    def model(self) -> AdvancedDict:
        return AdvancedDict(self.json())

    def require_success(self) -> 'McResp':
        if self.is_not_success:
            ExceptionTool.raises(
                f'请求失败: [{self.url}]，http_code: [{self.http_code}]',
                {'resp': self},
            )
        return self

    def error_msg(self) -> Optional[str]:
        if self.is_success:
            return None
        return f'http_code={self.http_code}, url={self.url}'

    def __str__(self):
        return f'McResp({self.http_code}, {self.url})'

    __repr__ = __str__


class McPostman:
    """
    一个站点一个 postman，内部持有一个 requests.Session（或 curl_cffi 会话）。
    """

    def __init__(self,
                 site: str,
                 meta_data: Optional[Dict] = None,
                 domain_list: Optional[List[str]] = None,
                 retry_times: Optional[int] = None,
                 ):
        self.site = site
        self.meta_data: AdvancedDict = AdvancedDict(meta_data or {})

        domains = domain_list or McModuleConfig.new_domain_list(site)
        ExceptionTool.require_true(len(domains) > 0, f'站点[{site}]的域名列表为空')
        self.domain_list: List[str] = list(domains)
        self.domain_index = 0

        self.retry_times = retry_times if retry_times is not None else McModuleConfig.DEFAULT_RETRY_TIMES
        # 记录每个域名的连续失败次数
        self.domain_failed_counter: Dict[str, int] = {}

        # DNS 解析覆盖（可选）：{"boylove.cc": "104.21.83.189"}
        # 注意 meta_data 是 AdvancedDict，取出来的嵌套 dict 会被包装成 AdvancedDict，
        # 因此这里用 dict(...) 归一化，不能只判断 isinstance(x, dict)
        resolve_map = self.meta_data.get('resolve', None)
        if resolve_map:
            register_dns_override({str(k): str(v) for k, v in dict(resolve_map).items()})

        self._session = None
        self._session_kind = None

    # ------------------------------------------------------------------ 会话

    @property
    def current_domain(self) -> str:
        return self.domain_list[self.domain_index % len(self.domain_list)]

    def switch_domain(self):
        if len(self.domain_list) > 1:
            self.domain_index = (self.domain_index + 1) % len(self.domain_list)
            mc_log('req.domain', f'切换域名: [{self.current_domain}]')

    @property
    def session(self):
        if self._session is None:
            self._session, self._session_kind = self.create_session()
        return self._session

    def create_session(self):
        meta = self.meta_data
        impersonate = meta.get('impersonate', None)
        postman_type = meta.get('type', None)

        # 两种方式都会走 curl_cffi：显式指定 type，或给了浏览器指纹
        use_cffi = postman_type in ('curl_cffi', 'curl_cffi_session') or bool(impersonate)

        if use_cffi:
            try:
                from curl_cffi import requests as cffi_requests
                session = cffi_requests.Session(impersonate=impersonate or 'chrome')
                return session, 'curl_cffi'
            except ImportError:
                mc_log('req', '未安装 curl_cffi，回退到 requests')

        import requests
        session = requests.Session()
        return session, 'requests'

    def close(self):
        if self._session is not None:
            try:
                self._session.close()
            except Exception:  # pragma: no cover
                pass
            self._session = None

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()

    # ------------------------------------------------------------------ cookies / headers

    @property
    def cookies(self) -> Dict[str, str]:
        try:
            return dict(self.session.cookies.get_dict())
        except Exception:  # pragma: no cover
            return {}

    def set_cookies(self, cookies: Dict[str, str]):
        if not cookies:
            return
        session = self.session
        for k, v in cookies.items():
            try:
                session.cookies.set(k, v)
            except Exception:  # pragma: no cover
                pass
        self.meta_data['cookies'] = {**self.meta_data.get('cookies', {}), **cookies}

    def update_cookies(self, cookies: Dict[str, str]):
        self.set_cookies(cookies)

    @property
    def headers(self) -> Dict[str, str]:
        headers = self.meta_data.get('headers', None)
        if headers is None:
            return dict(McModuleConfig.HTML_HEADERS_TEMPLATE)
        return dict(headers)

    def set_headers(self, headers: Dict[str, str]):
        self.meta_data['headers'] = {**self.headers, **headers}

    # ------------------------------------------------------------------ 请求

    def build_url(self, path: str, domain: Optional[str] = None) -> str:
        path = str(path)
        if path.startswith('http://') or path.startswith('https://'):
            return path
        if not path.startswith('/'):
            path = '/' + path
        return f'{McModuleConfig.PROT}{domain or self.current_domain}{path}'

    def req(self,
            path: str,
            method: str = 'GET',
            *,
            allow_redirects=True,
            **kwargs) -> McResp:
        """
        发起请求，失败时按域名轮换重试。
        """
        last_error = None
        last_resp: Optional[McResp] = None
        attempts = max(1, int(self.retry_times))

        for attempt in range(attempts):
            url = self.build_url(path)
            try:
                resp = self._single_request(url, method, allow_redirects=allow_redirects, **kwargs)
                last_resp = resp

                if resp.is_success:
                    self.domain_failed_counter[self.current_domain] = 0
                    return resp

                last_error = Exception(f'http_code={resp.http_code}')

            except Exception as e:
                last_error = e
                if attempt < attempts - 1:
                    self._on_failure()

            if attempt < attempts - 1:
                self._on_failure()
                time.sleep(0.2 * (attempt + 1))

        if last_resp is not None:
            return last_resp

        ExceptionTool.raises(
            f'请求失败: [{self.build_url(path)}]，已重试 {attempts} 次，异常: [{last_error}]',
            {'site': self.site, 'path': path},
        )

    def _on_failure(self):
        domain = self.current_domain
        count = self.domain_failed_counter.get(domain, 0) + 1
        self.domain_failed_counter[domain] = count
        # 单个域名连续失败 2 次就换域名
        if count >= 2:
            self.switch_domain()
            self.domain_failed_counter[domain] = 0

    def _single_request(self, url: str, method: str, allow_redirects=True, **kwargs) -> McResp:
        session = self.session
        headers = {**self.headers, **(kwargs.pop('headers', None) or {})}

        meta = self.meta_data
        timeout = kwargs.pop('timeout', None) or meta.get('timeout', McModuleConfig.DEFAULT_TIMEOUT)
        proxies = kwargs.pop('proxies', None) or meta.get('proxies', None)
        cookies = kwargs.pop('cookies', None) or meta.get('cookies', None) or None

        request_kwargs: Dict[str, Any] = {
            'headers': headers,
            'timeout': timeout,
            'allow_redirects': allow_redirects,
        }
        if proxies:
            request_kwargs['proxies'] = proxies
        if cookies:
            request_kwargs['cookies'] = cookies
        request_kwargs.update(kwargs)

        method = method.upper()
        if method == 'GET':
            raw = session.get(url, **request_kwargs)
        elif method == 'POST':
            raw = session.post(url, **request_kwargs)
        else:
            raw = session.request(method, url, **request_kwargs)

        return McResp(raw, url, self.site)

    # ------------------------------------------------------------------ 便捷方法

    def get(self, path: str, **kwargs) -> McResp:
        return self.req(path, 'GET', **kwargs)

    def post(self, path: str, **kwargs) -> McResp:
        return self.req(path, 'POST', **kwargs)

    def get_json(self, path: str, **kwargs) -> Dict:
        return self.get(path, **kwargs).json()

    def get_html(self, path: str, **kwargs) -> str:
        resp = self.get(path, **kwargs)
        resp.require_success()
        return resp.text

    def download(self, url: str, **kwargs) -> McResp:
        """下载二进制资源（图片）。"""
        resp = self.get(url, **kwargs)
        resp.require_success()
        return resp

    def copy(self) -> 'McPostman':
        postman = McPostman(
            site=self.site,
            meta_data=deepcopy(self.meta_data.src_dict),
            domain_list=list(self.domain_list),
            retry_times=self.retry_times,
        )
        postman.set_cookies(self.cookies)
        return postman
