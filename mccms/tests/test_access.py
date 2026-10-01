"""
访问权限语义测试。

这些用例刻意覆盖「无权限」路径，验证库的行为是：
  - 服务端说需要登录 -> LoginRequiredException
  - 服务端说需要会员 -> VipRequiredException
  - 服务端返回异常码  -> ResponseUnexpectedException
  - 有权限（会话本身有权）-> 正常返回图片

本库不包含任何绕过访问控制的逻辑，测试也一并固化这一点。
"""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))
sys.path.insert(0, os.path.dirname(__file__))

from fake_http import RoutePostman, load_fixture  # noqa: E402

from mccms.mc_exception import (AccessDeniedException,  # noqa: E402
                                LoginRequiredException, McException,
                                ResponseUnexpectedException,
                                VipRequiredException)
from mccms.manhwa_client_impl import ManhwaClient  # noqa: E402
from mccms.tibiu_client_impl import TibiuClient  # noqa: E402


class TestTibiuAccess(unittest.TestCase):

    def make_client(self, pic_response, user_response=None):
        routes = [
            ('/api/data/comicinfo', load_fixture('tibiu_comicinfo.json')),
            ('/api/data/chapter', load_fixture('tibiu_chapter.json')),
            ('/api/data/pic', pic_response),
            ('/index.php/api/user/info',
             user_response or {'code': 1, 'data': {'log': 0, 'vip': 0, 'cion': 0}}),
        ]
        return TibiuClient(postman=RoutePostman(routes))

    def test_login_required(self):
        client = self.make_client({'code': 2, 'msg': '登陆超时!!!'})
        comic = client.get_comic_detail('17001', fetch_chapters=False)
        chapter = client.get_chapter_detail('291931', comic_id='17001', fetch_image_urls=False)

        with self.assertRaises(LoginRequiredException) as ctx:
            client.fetch_image_urls(chapter)

        # 服务端原文保留在 context 里，抛出的 message 带可操作提示
        self.assertEqual(ctx.exception.context.get('raw_msg'), '登陆超时!!!')
        self.assertIn('需要登录', str(ctx.exception.msg))
        self.assertEqual(ctx.exception.context.get('access_code'), 2)
        self.assertEqual(ctx.exception.context.get('chapter_id'), '291931')

    def test_vip_required(self):
        client = self.make_client({'code': 3, 'msg': '需要会员', 'type': 'vip'})
        chapter = client.get_chapter_detail('291931', comic_id='17001', fetch_image_urls=False)

        with self.assertRaises(VipRequiredException):
            client.fetch_image_urls(chapter)

    def test_unexpected_code(self):
        client = self.make_client({'code': -1, 'msg': '非法请求'})
        chapter = client.get_chapter_detail('291931', comic_id='17001', fetch_image_urls=False)

        with self.assertRaises(ResponseUnexpectedException):
            client.fetch_image_urls(chapter)

    def test_all_access_errors_share_base_class(self):
        client = self.make_client({'code': 2, 'msg': '登录超时'})
        chapter = client.get_chapter_detail('291931', comic_id='17001', fetch_image_urls=False)

        with self.assertRaises(AccessDeniedException):
            client.fetch_image_urls(chapter)

    def test_missing_comic_raises(self):
        routes = [('/api/data/comicinfo', {'code': 1, 'msg': '漫画详情', 'data': {}})]
        client = TibiuClient(postman=RoutePostman(routes))
        with self.assertRaises(McException):
            client.get_comic_detail('99999')


class TestManhwaAccess(unittest.TestCase):

    def make_client(self, isbuy_response, user_response=None):
        routes = [
            ('/index.php/api/comic/isbuy', isbuy_response),
            ('/index.php/chapter/937510', load_fixture('manhwa_free_chap.html')),
            ('/index.php/api/user/info',
             user_response or {'code': 1, 'data': {'log': 0, 'vip': 0, 'cion': 0}}),
            ('/index.php/api/comic/chapter', load_fixture('manhwa_chapter_26995.json')),
        ]
        return ManhwaClient(postman=RoutePostman(routes))

    def test_login_required_when_not_logged_in(self):
        client = self.make_client({'code': 2, 'msg': '登录超时'})
        self.assertFalse(client.is_logged_in())

        with self.assertRaises(LoginRequiredException):
            client._fetch_images_via_api(self._dummy_chapter())

    def test_vip_required_when_logged_in_without_entitlement(self):
        client = self.make_client(
            {'code': 3, 'msg': '需要VIP', 'type': 'vip'},
            user_response={'code': 1, 'data': {'log': 1, 'vip': 0, 'cion': 0}},
        )
        self.assertTrue(client.is_logged_in())

        with self.assertRaises(VipRequiredException) as ctx:
            client._fetch_images_via_api(self._dummy_chapter())
        self.assertEqual(ctx.exception.context.get('access_type'), 'vip')

    def test_coin_required_maps_to_vip_exception(self):
        client = self.make_client({'code': 3, 'msg': '需要金币', 'type': 'cion'})
        with self.assertRaises(VipRequiredException):
            client._fetch_images_via_api(self._dummy_chapter())

    def test_entitled_session_gets_images(self):
        """会话本身有权限时，应正常返回图片（这里模拟一个已购权益的账号）。"""
        client = self.make_client({
            'code': 1,
            'pic': [{'id': 1, 'img': 'https://cdn.example.com/1.jpg'},
                    {'id': 2, 'img': 'https://cdn.example.com/2.jpg'}],
        })
        urls, ids = client._fetch_images_via_api(self._dummy_chapter())
        self.assertEqual(urls, ['https://cdn.example.com/1.jpg', 'https://cdn.example.com/2.jpg'])
        self.assertEqual(ids, ['1', '2'])

    def test_empty_pic_list_raises(self):
        client = self.make_client({'code': 1, 'pic': []})
        with self.assertRaises(McException):
            client._fetch_images_via_api(self._dummy_chapter())

    def test_free_chapter_uses_inline_html(self):
        """免费章节直接解析阅读页，不调用 isbuy。"""
        client = self.make_client({'code': 2, 'msg': '登录超时'})
        chapter = client.get_chapter_detail('937510')
        self.assertEqual(len(chapter), 540)

    @staticmethod
    def _dummy_chapter():
        from mccms.mc_entity import McChapterDetail
        return McChapterDetail(chapter_id='936939', name='第1章', comic_id='27032',
                               access={'vip': 1, 'cion': 0, 'price': 0})


class TestLoginFlow(unittest.TestCase):

    def test_login_stores_cookies_and_marks_logged_in(self):
        state = {'logged_in': False}

        def login_route(path, kwargs):
            state['logged_in'] = True
            return {'code': 1, 'data': {'log': 1, 'nichen': 'tester', 'vip': 0, 'cion': 0}}

        routes = [
            ('/index.php/api/user/login', login_route),
            (lambda p, k: '/index.php/api/user/info' in p,
             lambda path, kwargs: {'code': 1, 'data': {'log': 1, 'nichen': 'tester'}}
             if state['logged_in'] else {'code': 1, 'data': {'log': 0}}),
        ]

        # RoutePostman 的 response 支持 callable 时直接调用
        postman = RoutePostman(routes)
        original_match = postman.match

        def match(path, kwargs):
            result = original_match(path, kwargs)
            return result(path, kwargs) if callable(result) else result

        postman.match = match

        client = ManhwaClient(postman=postman)
        self.assertFalse(client.is_logged_in())

        info = client.login('tester', 'secret')
        self.assertEqual(info['nichen'], 'tester')
        self.assertTrue(client.is_logged_in())

    def test_login_rejected(self):
        routes = [('/index.php/api/user/login', {'code': -1, 'msg': '账号密码错误'})]
        client = ManhwaClient(postman=RoutePostman(routes))
        with self.assertRaises(McException):
            client.login('tester', 'wrong')


if __name__ == '__main__':
    unittest.main()
