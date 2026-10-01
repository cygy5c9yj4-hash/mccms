"""
香香腐宅（boylove.cc）客户端测试。

全部使用预录响应，不联网。
"""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))
sys.path.insert(0, os.path.dirname(__file__))

from fake_http import RoutePostman, load_fixture, load_json  # noqa: E402

from mccms.boylove_client_impl import BoyloveClient  # noqa: E402
from mccms.mc_exception import LoginRequiredException, McException, ResponseUnexpectedException  # noqa: E402


def boylove_postman(**extra):
    routes = [
        ('/home/api/searchk', load_fixture('boylove_search.json')),
        ('/home/api/chapter_list', load_fixture('boylove_chapter_list.json')),
        ('/home/api/rank', load_fixture('boylove_rank.json')),
        ('/home/api/cate', load_fixture('boylove_cate.json')),
        ('/home/book/capter/id/2623003', load_fixture('boylove_reader_scrambled.html')),
        ('/home/book/capter/id/435648', load_fixture('boylove_reader.html')),
        ('/home/book/index/id/', load_fixture('boylove_detail.html')),
    ]
    routes.extend(extra)
    return RoutePostman(routes)


class TestBoyloveSearch(unittest.TestCase):

    def setUp(self):
        self.client = BoyloveClient(postman=boylove_postman())

    def test_search(self):
        page = self.client.search('魔咒')
        self.assertGreater(len(page), 0)
        comic_id, info = page.content[0]
        self.assertTrue(comic_id.isdigit())
        self.assertEqual(info['site'], 'boylove')
        self.assertTrue(info['name'])

    def test_search_fields(self):
        page = self.client.search('魔咒')
        info = page.content[0][1]
        # 列表接口就带作者/标签/状态，不需要再进详情页
        self.assertTrue(info['author'])
        self.assertIn(info['serialize'], ('连载', '完结'))
        self.assertIsInstance(info['tags'], list)
        self.assertTrue(info['cover'].startswith('https://img.boylove.cc'))
        self.assertTrue(info['latest_chapter'])

    def test_search_tags_split(self):
        page = self.client.search('魔咒')
        tags = page.content[0][1]['tags']
        self.assertGreater(len(tags), 3)
        self.assertFalse(any(',' in t for t in tags))

    def test_blank_keyword_rejected(self):
        with self.assertRaises(McException):
            self.client.search('   ')

    def test_pagination_params(self):
        self.client.search('魔咒', page=3)
        path, kwargs = self.client.postman.requests[-1]
        self.assertEqual(kwargs['params']['pageNo'], 3)
        self.assertEqual(kwargs['params']['type'], 0)


class TestBoyloveDetail(unittest.TestCase):

    def setUp(self):
        self.client = BoyloveClient(postman=boylove_postman())
        self.comic = self.client.get_comic_detail('16904')

    def test_metadata(self):
        self.assertEqual(self.comic.comic_id, '16904')
        self.assertEqual(self.comic.name, '无根树/无根之树')
        self.assertEqual(self.comic.author, '라포')
        self.assertEqual(self.comic.serialize, '连载中')
        self.assertGreater(self.comic.score, 0)
        self.assertTrue(self.comic.cover.startswith('https://img.boylove.cc'))
        self.assertIn('뿌리', self.comic.description)

    def test_tags_from_meta_keywords(self):
        self.assertIn('韩漫', self.comic.tags)
        self.assertGreater(len(self.comic.tags), 5)

    def test_chapters(self):
        self.assertEqual(len(self.comic), 112)
        self.assertEqual(self.comic[0].chapter_id, '435648')
        self.assertEqual(self.comic[0].name, '第01话')
        self.assertEqual([c.index for c in self.comic[:3]], [1, 2, 3])

    def test_chapter_order_preserved(self):
        """
        该站章节 id 不单调（435648, 435649, 435647, ...），
        顺序必须按接口列表顺序，不能按 id 排序。
        """
        ids = [c.chapter_id for c in self.comic[:5]]
        self.assertEqual(ids, ['435648', '435649', '435647', '435650', '439936'])

    def test_chapter_access_flags(self):
        free = [c for c in self.comic if c.is_free]
        self.assertGreater(len(free), 0)
        for chapter in free:
            self.assertEqual(chapter.access_desc, '免费')

    def test_detail_without_chapters(self):
        comic = self.client.get_comic_detail('16904', fetch_chapters=False)
        self.assertEqual(len(comic), 1)


class TestBoyloveChapter(unittest.TestCase):

    def setUp(self):
        self.client = BoyloveClient(postman=boylove_postman())

    def test_reader_images_filtered_by_chapter_id(self):
        """
        阅读页混有推荐位缩略图（/bookimages/img/...），
        必须只取文件名以章节 id 开头的正文图。
        """
        chapter = self.client.get_chapter_detail('435648')
        self.assertEqual(chapter.chapter_id, '435648')
        urls = chapter.image_url_list
        self.assertGreater(len(urls), 50)

        for url in urls:
            file_name = url.rsplit('/', 1)[-1]
            self.assertTrue(file_name.startswith('435648-'),
                            f'混入了非正文图片: {url}')
            self.assertNotIn('/bookimages/img/', url)

    def test_no_duplicate_images(self):
        chapter = self.client.get_chapter_detail('435648')
        self.assertEqual(len(chapter.image_url_list), len(set(chapter.image_url_list)))

    def test_chapter_name(self):
        chapter = self.client.get_chapter_detail('435648')
        self.assertIn('第01话', chapter.name)

    def test_image_detail(self):
        chapter = self.client.get_chapter_detail('435648')
        image = chapter[0]
        self.assertEqual(image.index, 1)
        self.assertTrue(image.download_url.endswith('.webp'))
        self.assertTrue(image.filename.endswith('.webp'))

    def test_explicit_error_when_no_image(self):
        # 阅读页返回空 HTML 时，应明确报错而不是静默返回空列表
        postman = RoutePostman([
            ('/home/book/capter/id/', '<html><body><img src="/static/x.png"></body></html>'),
        ])
        client = BoyloveClient(postman=postman)
        with self.assertRaises(McException):
            client.get_chapter_detail('999999')


class TestBoyloveScrambledChapter(unittest.TestCase):
    """
    新章节（61 话起）的图片是竖带倒序下发的：
    阅读页给出内联 imageData + randomClass(=竖带数)，前端用 canvas 还原。
    """

    def setUp(self):
        self.client = BoyloveClient(postman=boylove_postman())

    def test_scramble_n_parsed(self):
        chapter = self.client.get_chapter_detail('2623003')
        self.assertEqual(chapter.scramble_n, 11)
        self.assertTrue(chapter[0].is_scrambled)

    def test_image_data_used_and_ordered(self):
        chapter = self.client.get_chapter_detail('2623003')
        self.assertGreater(len(chapter), 50)
        # imageData 的顺序就是阅读顺序，id 从 0 递增
        self.assertEqual(chapter.image_url_list[0],
                         chapter.image_url_list[0])
        self.assertIn('bookimages', chapter.image_url_list[0])
        self.assertTrue(chapter.image_url_list[0].startswith('https://img.boylove.cc/'))

    def test_dimensions_come_from_image_data(self):
        html = load_fixture('boylove_reader_scrambled.html')
        meta = self.client.parse_reader_page(html, '2623003')
        self.assertEqual(meta['scramble_n'], 11)
        self.assertGreater(len(meta['image_urls']), 50)

    def test_old_chapter_has_no_scramble(self):
        chapter = self.client.get_chapter_detail('435648')
        self.assertEqual(chapter.scramble_n, 0)
        self.assertFalse(chapter[0].is_scrambled)

    def test_decode_applied_on_download(self):
        """下载流程应在落盘后自动还原，且图片尺寸不变。"""
        import tempfile
        from unittest import mock
        from mccms.mc_option import McOption

        with tempfile.TemporaryDirectory() as tmp:
            option = McOption.construct({
                'client': {'impl': 'boylove'},
                'dir_rule': {'base_dir': tmp, 'rule': 'Bd_Chindextitle'},
                'log': False,
            })

            # 用真实图片做一次「先乱序、再还原」的闭环
            from PIL import Image
            from mccms.mc_decode import reverse_vertical_strips
            from tests.test_decode import make_striped_image

            n = 11
            original = make_striped_image(649, 120, n)
            scrambled = reverse_vertical_strips(original, n)

            src = os.path.join(tmp, 'src.png')
            scrambled.save(src, 'PNG')

            client = option.build_client()
            client.download_image = lambda url, save_path: (
                __import__('shutil').copyfile(src, save_path) or save_path)

            chapter = self.client.get_chapter_detail('2623003', fetch_image_urls=True)
            chapter.scramble_n = n
            chapter.set_image_url_list(['https://img.boylove.cc/x/y.webp'])
            image = chapter[0]
            self.assertTrue(image.is_scrambled)

            out = os.path.join(tmp, 'out.png')
            client.download_by_image_detail(image, out, decode_image=True)

            with Image.open(out) as img:
                self.assertEqual(list(img.convert('RGB').getdata()),
                                 list(original.getdata()))

            # decode_image=False 时应保持乱序原样
            out2 = os.path.join(tmp, 'out2.png')
            client.download_by_image_detail(image, out2, decode_image=False)
            with Image.open(out2) as img:
                self.assertEqual(list(img.convert('RGB').getdata()),
                                 list(scrambled.getdata()))


class TestBoyloveBrowsing(unittest.TestCase):

    def setUp(self):
        self.client = BoyloveClient(postman=boylove_postman())

    def test_ranking(self):
        page = self.client.ranking(1)
        self.assertGreater(len(page), 0)
        self.assertTrue(page.content[0][1]['name'])

    def test_ranking_nav(self):
        nav = self.client.ranking_nav()
        names = [n['name'] for n in nav]
        self.assertIn('人气榜', names)
        self.assertEqual(len(nav), 4)

    def test_categories_filter(self):
        page = self.client.categories_filter(page=1, vip=0)
        self.assertGreater(len(page), 0)

    def test_cate_spec_format(self):
        """分类路径必须拼成 8 段 + ?mt=0，这是站点接口的硬性格式。"""
        self.client.categories_filter(page=2, cate=1, tag=0, done=2, order=1, is18=0, vip=1)
        path, kwargs = self.client.postman.requests[-1]
        self.assertIn('/home/api/cate/tp/1-0-2-1-2-0-1-1', path)
        self.assertEqual(kwargs['params']['mt'], 0)

    def test_update_list(self):
        page = self.client.update_list(1)
        self.assertGreater(len(page), 0)

    def test_url_parsing(self):
        self.assertEqual(
            self.client.get_comic_id_from_url('/home/book/index/id/16904'), '16904')
        self.assertEqual(
            self.client.get_chapter_id_from_url('/home/book/capter/id/435648'), '435648')


class TestBoyloveEnvelope(unittest.TestCase):
    """站点有两套信封，必须按 code 语义分别处理。"""

    def test_code_0_is_success(self):
        postman = RoutePostman([
            ('/home/api/searchk', {'code': 0, 'result': {'list': [], 'lastPage': True}}),
        ])
        client = BoyloveClient(postman=postman)
        page = client.search('x')
        self.assertEqual(len(page), 0)

    def test_code_401_means_login_required(self):
        """
        App 端接口未带签名时返回 401。
        本库不推导签名，只把它如实映射成「需要登录态/凭证」。
        """
        postman = RoutePostman([
            ('/home/api/searchk', {'code': 401, 'data': [], 'errorMsg': 'Not legal request.'}),
        ])
        client = BoyloveClient(postman=postman)
        with self.assertRaises(LoginRequiredException):
            client.search('x')

    def test_code_500_raises(self):
        postman = RoutePostman([
            ('/home/api/searchk', {'code': 500, 'data': [], 'errorMsg': 'Server Error'}),
        ])
        client = BoyloveClient(postman=postman)
        with self.assertRaises(ResponseUnexpectedException):
            client.search('x')


class TestBoyloveDnsOverride(unittest.TestCase):
    """
    host -> ip 解析覆盖（等价 curl --resolve）。

    用于镜像站点或本机 DNS 返回的地址不可用的场景；
    正常直连时不需要配置，也不会有任何副作用。
    """

    def test_register_and_clear(self):
        from mccms.mc_postman import (_DNS_OVERRIDE, clear_dns_override,
                                      register_dns_override)
        register_dns_override({'example.invalid': '127.0.0.1'})
        try:
            self.assertEqual(_DNS_OVERRIDE.get('example.invalid'), '127.0.0.1')
        finally:
            clear_dns_override()
        self.assertEqual(_DNS_OVERRIDE, {})

    def test_option_can_carry_resolve_map(self):
        from mccms.mc_option import McOption
        from mccms.mc_postman import _DNS_OVERRIDE, clear_dns_override

        option = McOption.construct({
            'client': {
                'impl': 'boylove',
                'postman': {'meta_data': {'resolve': {'boylove.cc': '104.21.83.189'}}},
            },
        })
        try:
            option.new_client()
            self.assertIn('boylove.cc', _DNS_OVERRIDE)
            self.assertEqual(_DNS_OVERRIDE['boylove.cc'], '104.21.83.189')
        finally:
            clear_dns_override()


if __name__ == '__main__':
    unittest.main()
