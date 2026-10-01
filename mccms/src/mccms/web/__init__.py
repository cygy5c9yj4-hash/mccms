"""
mccms 本地 Web 验证前端。

用法::

    python -m mccms.web            # http://127.0.0.1:8765
    mccms-web --open
"""

from .server import JobRegistry, McWebServer, SiteManager, create_server, main, serve

__all__ = ['SiteManager', 'JobRegistry', 'McWebServer', 'create_server', 'serve', 'main']
