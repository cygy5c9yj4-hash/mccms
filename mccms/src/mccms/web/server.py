"""
mccms 的本地 Web 验证前端。

一个零额外依赖的 HTTP 服务（stdlib `http.server`），把 mccms 的能力暴露成 JSON 接口，
配一个单页前端用于人工验证「搜索 → 详情 → 章节 → 看图 → 下载」整条链路。

启动::

    python -m mccms.web                 # 默认 127.0.0.1:8765
    python -m mccms.web --port 9000
    mccms-web --open

安全说明：
- 只监听 127.0.0.1，不对外暴露
- `/api/image` 是图片代理，只允许代理「内置站点域名」与「本库确实返回过的图片域名」，
  防止被当成任意 SSRF 跳板
- 不做任何绕过访问控制的事情；无权限的章节会原样返回权限异常，前端显示为明确提示
"""

import json
import os
import threading
import traceback
from functools import partial
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Dict, List, Optional
from urllib.parse import parse_qs, unquote, urlparse

from ..mc_client_interface import AbstractMcClient
from ..mc_config import McMagicConstants, McModuleConfig, mc_log
from ..mc_downloader import McDownloader
from ..mc_entity import McChapterDetail, McComicDetail, McPageContent
from ..mc_exception import AccessDeniedException, McException
from ..mc_option import McOption
from ..mc_toolkit import MccmsText

__all__ = ['McWebServer', 'main', 'serve']

STATIC_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'static')


# --------------------------------------------------------------------------------------
# 序列化
# --------------------------------------------------------------------------------------

def comic_brief(comic_id: str, info: Dict) -> Dict:
    return {
        'id': comic_id,
        'name': info.get('name', ''),
        'author': info.get('author', ''),
        'cover': info.get('cover', ''),
        'text': info.get('text', ''),
        'serialize': info.get('serialize', ''),
        'views': info.get('views', 0),
        'latest_chapter': info.get('latest_chapter', ''),
        'url': info.get('url', ''),
    }


def page_to_dict(page: McPageContent) -> Dict:
    return {
        'total': page.total,
        'page': page.page_number,
        'count': len(page),
        'items': [comic_brief(cid, info) for cid, info in page.content],
    }


def comic_to_dict(comic: McComicDetail, with_chapters: bool = True) -> Dict:
    data = {
        'id': comic.comic_id,
        'name': comic.name,
        'author': comic.author,
        'authors': comic.authors,
        'tags': comic.tags,
        'description': comic.description,
        'cover': comic.cover,
        'serialize': comic.serialize,
        'is_finished': comic.is_finished,
        'views': comic.views,
        'score': comic.score,
        'site': comic.site,
        'url': comic.url,
        'slug': comic.slug,
        'chapter_count': comic.chapter_count,
    }
    if with_chapters:
        data['chapters'] = [chapter_to_dict(c, with_images=False) for c in comic]
    return data


def chapter_to_dict(chapter: McChapterDetail, with_images: bool = True) -> Dict:
    data = {
        'id': chapter.chapter_id,
        'name': chapter.name,
        'index': chapter.index,
        'comic_id': chapter.comic_id,
        'comic_name': chapter.comic_name,
        'is_free': chapter.is_free,
        'access': chapter.access_desc,
        'vip': chapter.vip,
        'cion': chapter.cion,
        'price': chapter.price,
        'image_count': len(chapter) or chapter.count,
        'url': chapter.url,
        # 图片乱序的竖带数量（>1 表示需要还原），前端传给图片代理
        'scramble_n': getattr(chapter, 'scramble_n', 0),
    }
    if with_images:
        data['images'] = [
            {'index': img.index, 'url': img.download_url, 'filename': img.filename}
            for img in chapter
        ]
    return data


# --------------------------------------------------------------------------------------
# 站点管理
# --------------------------------------------------------------------------------------

class SiteManager:
    """按站点缓存 option / client，并串行化底层请求。"""

    def __init__(self, base_dir: Optional[str] = None, download_dir: Optional[str] = None):
        self.base_dir = base_dir or os.getcwd()
        self.download_dir = download_dir or os.path.join(self.base_dir, 'downloads', 'web')
        self._options: Dict[str, McOption] = {}
        self._lock = threading.RLock()
        # 允许被图片代理的域名：内置站点域名 + 库实际返回过的图片域名
        self.image_hosts = set()
        for domains in McModuleConfig.DOMAIN_LIST_DICT.values():
            self.image_hosts.update(domains)

    def option_of(self, site: str) -> McOption:
        with self._lock:
            if site not in self._options:
                option = McOption.construct({
                    'client': {'impl': site},
                    'dir_rule': {
                        'base_dir': self.download_dir,
                        'rule': 'Bd_Cname_Chindextitle',
                    },
                    'log': False,
                })
                self._options[site] = option
            return self._options[site]

    def client_of(self, site: str) -> AbstractMcClient:
        option = self.option_of(site)
        with self._lock:
            return option.build_client()

    def call(self, site: str, func, *args, **kwargs):
        """串行执行客户端调用（requests.Session 并发安全性有限）。"""
        client = self.client_of(site)
        with self._lock:
            return func(client, *args, **kwargs)

    def register_image_hosts(self, urls: List[str]):
        for url in urls:
            host = MccmsText.parse_domain(url)
            if host:
                self.image_hosts.add(host)

    def check_site(self, site: str) -> str:
        if site not in McMagicConstants.SITE_LIST:
            raise McException(f'未知站点: [{site}]，可用: {list(McMagicConstants.SITE_LIST)}')
        return site


# --------------------------------------------------------------------------------------
# 下载任务
# --------------------------------------------------------------------------------------

class WebDownloader(McDownloader):
    """把图片级进度回调给 job。"""

    def __init__(self, option, job: Dict):
        super().__init__(option)
        self.job = job

    def after_image(self, image, img_save_path):
        super().after_image(image, img_save_path)
        with job_lock:
            self.job['done_images'] += 1


job_lock = threading.Lock()


class JobRegistry:

    def __init__(self):
        self.jobs: Dict[str, Dict] = {}
        self.counter = 0
        self.lock = threading.Lock()

    def submit(self, target) -> Dict:
        with self.lock:
            self.counter += 1
            job_id = str(self.counter)
            job = {
                'id': job_id,
                'status': 'running',
                'created_at': None,
                'total_images': 0,
                'done_images': 0,
                'result': None,
                'error': None,
                'error_type': None,
            }
            self.jobs[job_id] = job

        def runner():
            try:
                job['result'] = target(job)
                job['status'] = 'done'
            except AccessDeniedException as e:
                job['status'] = 'denied'
                job['error'] = e.msg
                job['error_type'] = type(e).__name__
            except McException as e:
                job['status'] = 'failed'
                job['error'] = e.msg
                job['error_type'] = type(e).__name__
            except Exception as e:  # pragma: no cover
                job['status'] = 'failed'
                job['error'] = f'{type(e).__name__}: {e}'
                job['error_type'] = type(e).__name__
                mc_log('web.job', f'下载任务失败: {e}', e)

        threading.Thread(target=runner, daemon=True, name=f'mccms-job-{job_id}').start()
        return job

    def get(self, job_id: str) -> Optional[Dict]:
        return self.jobs.get(job_id)


# --------------------------------------------------------------------------------------
# HTTP 处理
# --------------------------------------------------------------------------------------

class McWebHandler(BaseHTTPRequestHandler):

    server_version = 'mccms-web/1.0'
    protocol_version = 'HTTP/1.1'

    # 由 create_server 注入
    manager: SiteManager = None
    jobs: JobRegistry = None

    # ------------------------------------------------------------------ 基础

    def log_message(self, fmt, *args):
        mc_log('web', f'{self.address_string()} {fmt % args}')

    def send_json(self, data: Any, status: int = 200):
        body = json.dumps(data, ensure_ascii=False).encode('utf-8')
        self.send_response(status)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(body)

    def send_error_json(self, e: Exception, status: int = 200):
        """业务异常统一以 200 返回，body 里带 ok=false，前端按类型提示。"""
        if isinstance(e, AccessDeniedException):
            payload = {
                'ok': False,
                'error_type': type(e).__name__,
                'category': 'access_denied',
                'message': e.msg,
                'hint': '该内容需要登录或会员权益。本工具不会绕过访问控制；'
                        '如需访问请配置你自己的账号（option.client.username/password 或 cookies）。',
                'context': {k: v for k, v in (e.context or {}).items() if k != 'resp'},
            }
        elif isinstance(e, McException):
            payload = {
                'ok': False,
                'error_type': type(e).__name__,
                'category': 'error',
                'message': e.msg,
            }
        else:
            payload = {
                'ok': False,
                'error_type': type(e).__name__,
                'category': 'internal',
                'message': f'{type(e).__name__}: {e}',
                'traceback': traceback.format_exc().splitlines()[-6:],
            }
        self.send_json(payload, status)

    def read_json_body(self) -> Dict:
        length = int(self.headers.get('Content-Length') or 0)
        if length <= 0:
            return {}
        raw = self.rfile.read(length)
        try:
            return json.loads(raw.decode('utf-8'))
        except Exception:
            return {}

    # ------------------------------------------------------------------ 路由

    def do_GET(self):
        parsed = urlparse(self.path)
        path = parsed.path
        query = {k: v[0] for k, v in parse_qs(parsed.query).items()}

        try:
            if path == '/' or path == '/index.html':
                return self.serve_static('index.html')
            if path.startswith('/static/'):
                return self.serve_static(path[len('/static/'):])
            if path.startswith('/api/'):
                return self.handle_api(path[len('/api/'):], query)
            self.send_json({'ok': False, 'message': f'未知路径: {path}'}, 404)
        except Exception as e:
            self.send_error_json(e)

    def do_POST(self):
        parsed = urlparse(self.path)
        path = parsed.path

        try:
            if path.startswith('/api/'):
                body = self.read_json_body()
                return self.handle_api(path[len('/api/'):], {}, body)
            self.send_json({'ok': False, 'message': f'未知路径: {path}'}, 404)
        except Exception as e:
            self.send_error_json(e)

    def serve_static(self, relpath: str):
        safe = os.path.normpath(relpath).lstrip('/')
        full = os.path.join(STATIC_DIR, safe)
        if not full.startswith(STATIC_DIR) or not os.path.isfile(full):
            self.send_json({'ok': False, 'message': f'静态文件不存在: {relpath}'}, 404)
            return

        ctype = {
            '.html': 'text/html; charset=utf-8',
            '.js': 'application/javascript; charset=utf-8',
            '.css': 'text/css; charset=utf-8',
            '.svg': 'image/svg+xml',
        }.get(os.path.splitext(full)[1], 'application/octet-stream')

        with open(full, 'rb') as f:
            body = f.read()

        self.send_response(200)
        self.send_header('Content-Type', ctype)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(body)

    # ------------------------------------------------------------------ API

    def handle_api(self, action: str, query: Dict, body: Optional[Dict] = None):
        body = body or {}
        params = {**query, **body}

        if action == 'sites':
            return self.api_sites()
        if action == 'search':
            return self.api_search(params)
        if action == 'comic':
            return self.api_comic(params)
        if action == 'chapter':
            return self.api_chapter(params)
        if action == 'ranking':
            return self.api_ranking(params)
        if action == 'ranknav':
            return self.api_ranknav(params)
        if action == 'update':
            return self.api_update(params)
        if action == 'hot':
            return self.api_hot(params)
        if action == 'user':
            return self.api_user(params)
        if action == 'image':
            return self.api_image(params)
        if action == 'download':
            return self.api_download(params)
        if action == 'job':
            return self.api_job(params)

        self.send_json({'ok': False, 'message': f'未知接口: /api/{action}'}, 404)

    def api_sites(self):
        sites = []
        for site in McMagicConstants.SITE_LIST:
            # 能力按客户端实际实现的方法探测，新增站点无需改这里
            try:
                client = self.manager.client_of(site)
                capabilities = {
                    'ranking': hasattr(client, 'ranking'),
                    'rank_nav': hasattr(client, 'ranking_nav'),
                    'update': hasattr(client, 'update_list'),
                    'hot': hasattr(client, 'hot_list'),
                    'categories': hasattr(client, 'categories_filter'),
                }
            except Exception:
                capabilities = {}

            sites.append({
                'key': site,
                'name': McModuleConfig.site_name(site),
                'domains': McModuleConfig.domain_list_of(site),
                'capabilities': capabilities,
            })
        self.send_json({'ok': True, 'sites': sites})

    def api_search(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        keyword = (params.get('keyword') or '').strip()
        page = int(params.get('page') or 1)
        if not keyword:
            return self.send_json({'ok': False, 'message': '关键字不能为空'})

        result = self.manager.call(site, lambda c: c.search(keyword, page))
        self.send_json({'ok': True, 'site': site, **page_to_dict(result)})

    def api_comic(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        comic_id = (params.get('id') or '').strip()
        if not comic_id:
            return self.send_json({'ok': False, 'message': '缺少漫画 id'})

        comic = self.manager.call(site, lambda c: c.get_comic_detail(comic_id))
        self.manager.register_image_hosts([comic.cover])
        self.send_json({'ok': True, 'site': site, 'comic': comic_to_dict(comic)})

    def api_chapter(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        chapter_id = (params.get('id') or '').strip()
        comic_id = (params.get('comic_id') or '').strip() or None
        if not chapter_id:
            return self.send_json({'ok': False, 'message': '缺少章节 id'})

        chapter = self.manager.call(
            site,
            lambda c: c.get_chapter_detail(chapter_id, comic_id=comic_id),
        )
        self.manager.register_image_hosts([img.download_url for img in chapter])
        self.send_json({'ok': True, 'site': site, 'chapter': chapter_to_dict(chapter)})

    def api_ranking(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        rank_type = params.get('type') or 'top'
        page = int(params.get('page') or 1)

        if not hasattr(self.manager.client_of(site), 'ranking'):
            return self.send_json({'ok': False, 'message': f'站点 [{site}] 不支持榜单'})

        result = self.manager.call(site, lambda c: c.ranking(rank_type, page))
        self.send_json({'ok': True, 'site': site, 'rank_type': rank_type, **page_to_dict(result)})

    def api_ranknav(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        client = self.manager.client_of(site)
        if not hasattr(client, 'ranking_nav'):
            return self.send_json({'ok': True, 'nav': []})
        nav = self.manager.call(site, lambda c: c.ranking_nav())
        self.send_json({'ok': True, 'nav': nav})

    def api_update(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        page = int(params.get('page') or 1)
        result = self.manager.call(site, lambda c: c.update_list(page))
        self.send_json({'ok': True, 'site': site, **page_to_dict(result)})

    def api_hot(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_MANHWA))
        client = self.manager.client_of(site)
        if not hasattr(client, 'hot_list'):
            return self.send_json({'ok': False, 'message': f'站点 [{site}] 不支持热门列表'})
        result = self.manager.call(site, lambda c: c.hot_list())
        self.send_json({'ok': True, 'site': site, **page_to_dict(result)})

    def api_user(self, params: Dict):
        from ..mc_exception import McException as _McException
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))

        def fetch(client):
            info = client.user_info()
            try:
                logged = client.is_logged_in()
            except _McException:
                logged = False
            return {'info': info, 'logged_in': logged}

        self.send_json({'ok': True, 'site': site,
                        **self.manager.call(site, fetch)})

    def api_image(self, params: Dict):
        url = unquote(params.get('url') or '')
        scramble_n = MccmsText.safe_int(params.get('scramble_n', 0), 0)
        if not url.startswith('http'):
            return self.send_json({'ok': False, 'message': '图片地址非法'}, 400)

        host = MccmsText.parse_domain(url)
        if host not in self.manager.image_hosts:
            return self.send_json(
                {'ok': False, 'message': f'拒绝代理未登记的图片域名: [{host}]'}, 403)

        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        client = self.manager.client_of(site)
        with self.manager._lock:
            resp = client.postman.get(url)

        if resp.is_not_success:
            return self.send_json(
                {'ok': False, 'message': f'图片请求失败: http {resp.http_code}'}, 200)

        body = resp.content
        # 上游 Content-Type 不可信，按魔数判定
        content_type = _sniff_image_type(body)

        # 站点把整页图切成竖带倒序下发，浏览器端原本靠 canvas 还原；
        # 本地前端直接在这里还原好再返回，避免页面看到错位图。
        if scramble_n > 1:
            decoded = _decode_image_bytes(body, scramble_n)
            if decoded is not None:
                body, content_type = decoded
                content_type = _sniff_image_type(body)

        self.send_response(200)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'public, max-age=3600')
        self.end_headers()
        self.wfile.write(body)

    def api_download(self, params: Dict):
        site = self.manager.check_site(params.get('site', McMagicConstants.SITE_TIBIU))
        chapter_id = str(params.get('chapter_id') or '').strip()
        comic_id = str(params.get('comic_id') or '').strip() or None
        limit = int(params.get('limit') or 0)

        if not chapter_id:
            return self.send_json({'ok': False, 'message': '缺少章节 id'})

        job = self.jobs.submit(lambda j: self._run_download(j, site, chapter_id, comic_id, limit))

        # 预估图片数，前端可显示进度分母
        try:
            chapter = self.manager.call(
                site, lambda c: c.get_chapter_detail(chapter_id, comic_id=comic_id,
                                                     fetch_image_urls=False))
            job['total_images'] = chapter.count
            job['comic_name'] = chapter.comic_name
            job['chapter_name'] = chapter.name
        except Exception:
            pass

        self.send_json({'ok': True, 'job': job})

    def _run_download(self, job: Dict, site: str, chapter_id: str,
                      comic_id: Optional[str], limit: int):
        from .. import api as mc_api

        option = self.manager.option_of(site)

        # new_downloader 会以 downloader(option) 的方式实例化，
        # 用 partial 把 job / limit 预先绑进去
        if limit > 0:
            downloader = partial(_LimitedWebDownloader, job=job, limit=limit)
        else:
            downloader = partial(WebDownloader, job=job)

        result = mc_api.download_chapter(chapter_id, option, downloader, comic_id=comic_id)
        manifest = result.manifest

        with job_lock:
            job['done_images'] = len(manifest.image_filepath_list)

        return {
            'chapter': chapter_to_dict(result.detail, with_images=False),
            'image_count': len(manifest.image_filepath_list),
            'save_path': result.detail.save_path,
            'duration': result.duration,
            'files': manifest.image_filepath_list[:20],
        }

    def api_job(self, params: Dict):
        job = self.jobs.get(str(params.get('id') or ''))
        if job is None:
            return self.send_json({'ok': False, 'message': '任务不存在'}, 404)
        self.send_json({'ok': True, 'job': job})


class _LimitedWebDownloader(WebDownloader):
    """每个章节只下前 N 张，同时上报进度。"""

    def __init__(self, option, job: Dict, limit: int = 0):
        super().__init__(option, job)
        self.limit = int(limit or 0)

    def do_filter(self, detail):
        if self.limit > 0 and isinstance(detail, McChapterDetail):
            return detail[:self.limit]
        return detail


# --------------------------------------------------------------------------------------
# 服务
# --------------------------------------------------------------------------------------

def _sniff_image_type(data: bytes) -> str:
    """
    按魔数判断图片类型。

    站点的 Content-Type 并不总是可信（实测有 .webp 后缀实际是 JPEG 的情况），
    所以这里直接看文件头。
    """
    if data[:3] == b'\xff\xd8\xff':
        return 'image/jpeg'
    if data[:8] == b'\x89PNG\r\n\x1a\n':
        return 'image/png'
    if data[:4] == b'RIFF' and data[8:12] == b'WEBP':
        return 'image/webp'
    if data[:6] in (b'GIF87a', b'GIF89a'):
        return 'image/gif'
    if data[:2] == b'BM':
        return 'image/bmp'
    return 'application/octet-stream'


def _decode_image_bytes(data: bytes, scramble_n: int):
    """
    在内存里还原竖带倒序的图片。

    :return: (bytes, content_type)；不需要还原或失败时返回 None
    """
    try:
        import io

        from PIL import Image

        from ..mc_decode import is_scrambled_layout, reverse_vertical_strips

        with Image.open(io.BytesIO(data)) as img:
            img.load()
            if not is_scrambled_layout(img.width, img.height, scramble_n):
                return None

            decoded = reverse_vertical_strips(img, scramble_n)
            buffer = io.BytesIO()
            fmt = 'WEBP' if img.format in ('WEBP', None) else img.format
            kwargs = {'quality': 95} if fmt in ('WEBP', 'JPEG') else {}
            if decoded.mode in ('RGBA', 'P', 'LA') and fmt == 'JPEG':
                decoded = decoded.convert('RGB')
            decoded.save(buffer, format=fmt, **kwargs)
            return buffer.getvalue(), f'image/{fmt.lower()}'
    except Exception as e:  # pragma: no cover
        mc_log('web.image', f'图片还原失败，返回原图: {e}')
        return None


class McWebServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def create_server(host: str = '127.0.0.1',
                  port: int = 8765,
                  base_dir: Optional[str] = None,
                  download_dir: Optional[str] = None) -> McWebServer:
    manager = SiteManager(base_dir=base_dir, download_dir=download_dir)
    jobs = JobRegistry()

    handler = type('BoundHandler', (McWebHandler,), {'manager': manager, 'jobs': jobs})
    server = McWebServer((host, port), handler)
    server.manager = manager
    server.jobs = jobs
    return server


def serve(host: str = '127.0.0.1', port: int = 8765, open_browser: bool = False, **kwargs):
    server = create_server(host, port, **kwargs)
    url = f'http://{host}:{server.server_address[1]}/'
    print(f'mccms Web 验证前端已启动: {url}')
    print(f'图片下载目录: {server.manager.download_dir}')
    print('按 Ctrl+C 停止')

    if open_browser:
        import webbrowser
        threading.Timer(0.5, lambda: webbrowser.open(url)).start()

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print('\n已停止')
    finally:
        server.server_close()


def main(argv=None):
    import argparse

    parser = argparse.ArgumentParser(prog='mccms-web', description='mccms 本地 Web 验证前端')
    parser.add_argument('--host', default='127.0.0.1', help='监听地址（默认只监听本机）')
    parser.add_argument('--port', type=int, default=8765, help='端口，默认 8765')
    parser.add_argument('--download-dir', default=None, help='图片下载目录')
    parser.add_argument('--open', dest='open_browser', action='store_true', help='启动后自动打开浏览器')
    args = parser.parse_args(argv)

    serve(args.host, args.port, open_browser=args.open_browser,
          download_dir=args.download_dir)
    return 0
