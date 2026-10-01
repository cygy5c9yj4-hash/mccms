"""测试用的假 HTTP 层：不联网，按路由返回预录响应。"""

import json
import os

FIXTURES = os.path.join(os.path.dirname(__file__), 'fixtures')


def load_fixture(name: str) -> str:
    with open(os.path.join(FIXTURES, name), encoding='utf-8') as f:
        return f.read()


def load_json(name: str) -> dict:
    return json.loads(load_fixture(name))


class FakeRawResponse:
    """模拟 requests.Response 的最小接口。"""

    def __init__(self, text: str, status_code: int = 200):
        self.text = text
        self.status_code = status_code
        self.content = text.encode('utf-8')
        self.cookies = {}


class RoutePostman:
    """
    按路由返回预录响应。

    :param routes: [(matcher, response)]，matcher 可以是
                   - str：path 包含该子串即匹配
                   - callable(path, kwargs) -> bool
                   response 可以是 str（原样返回）或 dict（序列化为 JSON）
    """

    site = 'fake'

    def __init__(self, routes, cookies=None):
        self.routes = list(routes)
        self.cookies = dict(cookies or {})
        self.requests = []
        self.current_domain = 'fake.local'
        self.closed = False

    def match(self, path, kwargs):
        for matcher, response in self.routes:
            if callable(matcher):
                if matcher(path, kwargs):
                    return response
            elif matcher in path:
                return response
        raise AssertionError(f'FakePostman 没有匹配到路由: path={path}, kwargs={kwargs}')

    def _build(self, path, kwargs):
        from mccms.mc_postman import McResp

        self.requests.append((path, kwargs))
        response = self.match(path, kwargs)

        if isinstance(response, (dict, list)):
            text = json.dumps(response, ensure_ascii=False)
        else:
            text = response

        return McResp(FakeRawResponse(text), path, self.site)

    def get(self, path, **kwargs):
        return self._build(path, kwargs)

    def post(self, path, **kwargs):
        return self._build(path, kwargs)

    def set_cookies(self, cookies):
        self.cookies.update(cookies or {})

    def update_cookies(self, cookies):
        self.set_cookies(cookies)

    def close(self):
        self.closed = True

    def __enter__(self):
        return self

    def __exit__(self, *args):
        self.close()
