"""两个站点客户端的解析测试（全部使用预录响应，不联网）。"""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))
sys.path.insert(0, os.path.dirname(__file__))

from fake_http import RoutePostman, load_fixture, load_json  # noqa: E402

from mccms.manhwa_client_impl import ManhwaClient  # noqa: E402
from mccms.tibiu_client_impl import TibiuClient  # noqa: E402


def tibiu_postman(**extra):
    routes = [
        ('/api/data/search', load_fixture('tibiu_search.json')),
        ('/api/data/comicinfo', load_fixture('tibiu_comicinfo.json')),
        ('/api/data/chapter', load_fixture('tibiu_chapter.json')),
        ('/api/data/pic', load_fixture('tibiu_pic.json')),
        ('/index.php/api/data/update_api', load_fixture('tibiu_update.json')),
        ('/index.php/api/data/category_api', load_fixture('tibiu_category.json')),
        ('/index.php/api/rankdata/nav', load_fixture('tibiu_rank_nav.json')),
        ('/index.php/api/rankdata/lists', load_fixture('tibiu_rank_lists.json')),
        ('/index.php/api/user/info', {'code': 1, 'data': {'log': 0, 'vip': 0, 'cion': 0, 'adult': 0}}),
    ]
    routes.extend(extra)
    return RoutePostman(routes)


def manhwa_postman(**extra):
    def chapter_route(path, kwargs):
        return '/api/comic/chapter' in path

    routes = [
        (lambda p, k: '/api/comic/chapter' in p and k.get('params', {}).get('mid') == '26995',
         load_fixture('manhwa_chapter_26995.json')),
        (chapter_route, load_fixture('manhwa_chapter.json')),
        ('/index.php/api/comic/hot', load_fixture('manhwa_hot.json')),
        ('/index.php/api/user/info', {'code': 1, 'data': {'log': 0, 'vip': 0, 'cion': 0, 'adult': 0}}),
        ('/index.php/api/comic/isbuy', {'code': 2, 'msg': '登录超时'}),
        ('/index.php/search/', load_fixture('manhwa_search.html')),
        ('/index.php/chapter/937510', load_fixture('manhwa_free_chap.html')),
        ('/index.php/comic/', load_fixture('manhwa_detail.html')),
    ]
    routes.extend(extra)
    return RoutePostman(routes)


class TestTibiuClient(unittest.TestCase):

    def setUp(self):
        self.client = TibiuClient(postman=tibiu_postman())

    def test_search(self):
        page = self.client.search('love', 1)
        self.assertEqual(len(page), 10)
        comic_id, info = page.content[0]
        self.assertEqual(comic_id, '75842')
        self.assertEqual(info['name'], 'Love Drug')
        self.assertTrue(info['cover'].startswith('http'))
        self.assertEqual(info['site'], 'tibiu')

    def test_search_name_unescaped(self):
        page = self.client.search('love', 1)
        names = [name for _, name in page.iter_id_title()]
        # 站点 JSON 里带 &#40; 这类实体，必须解码
        self.assertFalse(any('&#' in n for n in names), names)

    def test_search_rejects_blank_keyword(self):
        from mccms.mc_exception import McException
        with self.assertRaises(McException):
            self.client.search('   ')

    def test_get_comic_detail(self):
        comic = self.client.get_comic_detail('17001')
        self.assertEqual(comic.comic_id, '17001')
        self.assertEqual(comic.name, '社内恋爱/内部爱情')
        self.assertEqual(len(comic), 54)
        self.assertEqual(comic.serialize, '完结')
        self.assertTrue(comic.cover.startswith('http'))
        self.assertEqual(comic[0].chapter_id, '291931')
        self.assertEqual(comic[0].name, '第01话')
        self.assertEqual([c.index for c in comic[:3]], [1, 2, 3])

    def test_get_comic_detail_without_chapters(self):
        comic = self.client.get_comic_detail('17001', fetch_chapters=False)
        self.assertEqual(len(comic), 1)  # 无章节信息时自成一章

    def test_fetch_image_urls_sorted_ascending_by_id(self):
        comic = self.client.get_comic_detail('17001')
        chapter = comic[0]
        urls = self.client.fetch_image_urls(chapter)

        raw = load_json('tibiu_pic.json')['data']
        expected_ids = sorted(int(item['id']) for item in raw)
        # 站点返回的是降序，客户端必须按 id 升序还原阅读顺序
        self.assertEqual(len(urls), len(raw))

        first_page_id = int(chapter[0].img_id)
        self.assertEqual(first_page_id, expected_ids[0])
        self.assertEqual(int(chapter[-1].img_id), expected_ids[-1])

    def test_chapter_image_detail(self):
        comic = self.client.get_comic_detail('17001')
        chapter = comic[0]
        self.client.fetch_image_urls(chapter)
        image = chapter[0]
        self.assertEqual(image.index, 1)
        self.assertTrue(image.img_file_suffix.startswith('.'))
        self.assertTrue(image.download_url.startswith('http'))

    def test_get_chapter_detail_with_comic_context(self):
        chapter = self.client.get_chapter_detail('291931', comic_id='17001')
        self.assertEqual(chapter.chapter_id, '291931')
        self.assertEqual(chapter.comic_id, '17001')
        self.assertEqual(chapter.index, 1)
        self.assertEqual(len(chapter), 64)

    def test_get_chapter_detail_without_comic_context(self):
        chapter = self.client.get_chapter_detail('291931')
        self.assertEqual(chapter.chapter_id, '291931')
        self.assertEqual(len(chapter), 64)

    def test_update_list(self):
        page = self.client.update_list(1)
        self.assertGreater(len(page), 0)
        self.assertGreater(page.total, 0)

    def test_categories_filter(self):
        page = self.client.categories_filter(page=1, order='hits')
        self.assertGreater(len(page), 0)

    def test_ranking(self):
        nav = self.client.ranking_nav()
        self.assertGreater(len(nav), 0)
        self.assertIn('type', nav[0])

        page = self.client.ranking('top', 1)
        self.assertGreater(len(page), 0)

    def test_parse_chapter_url(self):
        comic_id, chapter_id = self.client.parse_chapter_url(
            'https://cache.tibiu.net/chapter/17001/291931')
        self.assertEqual(comic_id, '17001')
        self.assertEqual(chapter_id, '291931')

    def test_url_helpers(self):
        self.assertEqual(self.client.get_comic_id_from_url('/comic/17001'), '17001')
        self.assertEqual(self.client.get_chapter_id_from_url('/chapter/17001/291931'), '291931')
        self.assertIn('17001', self.client.get_comic_url('17001'))


class TestManhwaClient(unittest.TestCase):

    def setUp(self):
        self.client = ManhwaClient(postman=manhwa_postman())

    def test_search(self):
        page = self.client.search('魔咒', 1)
        self.assertEqual(len(page), 3)
        comic_id, info = page.content[0]
        self.assertEqual(comic_id, 'mozhouweixianzaoyu')
        self.assertIn('魔咒', info['name'])
        self.assertTrue(info['cover'].startswith('http'))
        self.assertTrue(info['url'].startswith('/index.php/comic/'))

    def test_get_comic_detail_by_slug(self):
        comic = self.client.get_comic_detail('mozhouweixianzaoyu')
        self.assertEqual(comic.comic_id, '27032')
        self.assertEqual(comic.serialize, '连载')
        self.assertEqual(comic.author, 'MinGwa')
        self.assertTrue(comic.cover.startswith('http'))
        self.assertEqual(len(comic), 279)
        self.assertEqual(comic[0].chapter_id, '936939')

    def test_chapter_access_flags_from_api(self):
        comic = self.client.get_comic_detail('mozhouweixianzaoyu')
        self.assertTrue(comic[0].vip == 1)
        self.assertEqual(comic[0].access_desc, 'VIP')

    def test_get_chapter_detail_free_chapter(self):
        chapter = self.client.get_chapter_detail('937510')
        self.assertEqual(chapter.chapter_id, '937510')
        self.assertEqual(chapter.comic_id, '26995')
        self.assertEqual(chapter.name, '第67章')

        # index 取章节在接口列表中的位置（站点列表顺序与章节名编号不一定一致）
        raw = load_json('manhwa_chapter_26995.json')['data']
        expected_index = [str(c['id']) for c in raw].index('937510') + 1
        self.assertEqual(chapter.index, expected_index)
        self.assertEqual(len(chapter), 540)
        self.assertTrue(chapter.is_free)

    def test_images_have_correct_order(self):
        chapter = self.client.get_chapter_detail('937510')
        urls = chapter.image_url_list
        self.assertIn('zhouyi67p36_01_0.jpg', urls[0])
        self.assertEqual(len(urls), len(set(urls)))

    def test_hot_list(self):
        page = self.client.hot_list()
        self.assertEqual(len(page), 10)
        comic_id, info = page.content[0]
        self.assertTrue(comic_id.isdigit())
        self.assertTrue(info['slug'])

    def test_reader_page_meta(self):
        html = load_fixture('manhwa_free_chap.html')
        meta = self.client.parse_reader_page(html, '937510')
        self.assertEqual(meta['comic_id'], '26995')
        self.assertEqual(meta['name'], '第67章')
        self.assertEqual(meta['count'], 540)
        self.assertEqual(len(meta['image_urls']), 540)
        self.assertEqual(meta['access']['vip'], 0)

    def test_url_helpers(self):
        self.assertEqual(
            self.client.get_comic_id_from_url('/index.php/comic/mozhouweixianzaoyu'),
            'mozhouweixianzaoyu')
        self.assertEqual(
            self.client.get_comic_id_from_url('/index.php/comic/27032'), '27032')
        self.assertEqual(
            self.client.get_chapter_id_from_url('/index.php/chapter/936939'), '936939')


if __name__ == '__main__':
    unittest.main()
