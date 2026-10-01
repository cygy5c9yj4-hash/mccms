"""HTML DOM 解析与 CSS 选择器测试（使用真实页面夹具）。"""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))

from mccms.mc_html import Node, parse_html  # noqa: E402

FIXTURES = os.path.join(os.path.dirname(__file__), 'fixtures')


def read_fixture(name: str) -> str:
    with open(os.path.join(FIXTURES, name), encoding='utf-8') as f:
        return f.read()


class TestBasicParsing(unittest.TestCase):

    def test_simple_document(self):
        root = parse_html('<html><body><div id="a" class="x y"><p>hi</p></div></body></html>')
        div = root.find('#a')
        self.assertIsNotNone(div)
        self.assertEqual(div.tag, 'div')
        self.assertEqual(div.classes, ['x', 'y'])
        self.assertTrue(div.has_class('x'))
        self.assertEqual(div.find('p').text, 'hi')

    def test_void_tags_do_not_swallow_siblings(self):
        root = parse_html('<div><img src="a.png"><span>after</span></div>')
        div = root.find('div')
        self.assertEqual(len(div.children), 2)
        self.assertEqual(div.find('span').text, 'after')

    def test_unclosed_tags_are_tolerated(self):
        root = parse_html('<ul><li>a<li>b</ul>')
        items = root.find_all('li')
        self.assertEqual([i.text for i in items], ['a', 'b'])

    def test_nested_same_tag(self):
        root = parse_html('<div><div><span>deep</span></div></div>')
        self.assertEqual(len(root.find_all('div')), 2)
        self.assertEqual(root.find('span').text, 'deep')

    def test_text_skips_script_and_style(self):
        root = parse_html('<div><script>var a=1;</script><style>.a{}</style><p>real</p></div>')
        self.assertEqual(root.find('div').text, 'real')

    def test_empty_html_raises(self):
        from mccms.mc_html import HtmlParseError
        with self.assertRaises(HtmlParseError):
            parse_html(None)


class TestSelectors(unittest.TestCase):

    def setUp(self):
        self.root = parse_html('''
            <div class="list">
              <div class="item" data-id="1"><a class="title" href="/a">A</a></div>
              <div class="item vip" data-id="2"><a class="title" href="/b">B</a></div>
              <div class="item" data-id="3"><span class="title">C</span></div>
            </div>
        ''')

    def test_class_selector(self):
        self.assertEqual(len(self.root.find_all('.item')), 3)
        self.assertEqual(len(self.root.find_all('.item.vip')), 1)

    def test_attribute_selector(self):
        self.assertEqual(self.root.find('[data-id="2"]').text, 'B')
        self.assertEqual(len(self.root.find_all('[data-id]')), 3)
        self.assertEqual(len(self.root.find_all('[href^="/a"]')), 1)
        self.assertEqual(len(self.root.find_all('[href$="/b"]')), 1)
        self.assertEqual(len(self.root.find_all('[data-id*="1"]')), 1)

    def test_descendant_and_child(self):
        self.assertEqual(len(self.root.find_all('.list .title')), 3)
        self.assertEqual(len(self.root.find_all('.list > .item')), 3)
        self.assertEqual(len(self.root.find_all('.item > .title')), 3)

    def test_group_selector(self):
        self.assertEqual(len(self.root.find_all('span.title, a.title')), 3)

    def test_find_returns_first_in_document_order(self):
        self.assertEqual(self.root.find('.title').text, 'A')

    def test_find_all_does_not_include_self(self):
        root = parse_html('<div class="a"><div class="a"></div></div>')
        self.assertEqual(len(root.find_all('.a')), 2)

    def test_closest(self):
        node = self.root.find('[data-id="2"]')
        self.assertIsNotNone(node.closest('.vip'))
        self.assertIsNone(node.closest('.nonexistent'))

    def test_invalid_selector_raises(self):
        from mccms.mc_html import HtmlParseError
        with self.assertRaises(HtmlParseError):
            self.root.find_all('')
        with self.assertRaises(HtmlParseError):
            self.root.find_all('[unclosed')


class TestRealManhwaPages(unittest.TestCase):
    """用真实抓取的页面验证解析结果。"""

    def test_search_page_cards(self):
        root = parse_html(read_fixture('manhwa_search.html'))
        items = root.find_all('.common-comic-item')
        self.assertGreater(len(items), 0)

        first = items[0]
        link = first.find('.comic__title a')
        self.assertIsNotNone(link)
        self.assertTrue(link.attr('href').startswith('/index.php/comic/'))
        self.assertGreater(len(link.text), 0)

        img = first.find('img')
        self.assertIn('http', img.attr('data-original'))

    def test_detail_page_metadata(self):
        root = parse_html(read_fixture('manhwa_detail.html'))

        title = root.find('.de-info__box .comic-title')
        self.assertIsNotNone(title)
        self.assertGreater(len(title.text), 0)

        cover = root.find('.de-info__cover img')
        self.assertTrue(cover.attr('src').startswith('http'))

        author = root.find('.comic-author .name a')
        self.assertGreater(len(author.text), 0)

        collect = root.find('.j-user-collect')
        self.assertTrue(collect.attr('data-id').isdigit())

        chapter_title = root.find('.de-chapter__title')
        self.assertIsNotNone(chapter_title)
        self.assertIn(chapter_title.children_by_tag('span')[0].text.strip(), ('连载', '完结'))

    def test_reader_page(self):
        root = parse_html(read_fixture('manhwa_free_chap.html'))

        crumb = root.find('.read__crumb a.crumb__title')
        self.assertIsNotNone(crumb)
        self.assertIn('/index.php/comic/', crumb.attr('href'))

        chapter = root.find('.read__crumb h1.comic-title a')
        self.assertGreater(len(chapter.text), 0)

        pics = root.find_all('.rd-article__pic')
        self.assertGreater(len(pics), 100)

        first_img = pics[0].find('img')
        self.assertIn('http', first_img.attr('data-original'))
        self.assertTrue(pics[0].attr('data-pid').isdigit())

        count = root.find('.page-index__btn .count')
        self.assertEqual(int(count.text), len(pics))


class TestRealTibiuPages(unittest.TestCase):

    def test_detail_page_has_chapter_links(self):
        root = parse_html(read_fixture('tibiu_detail.html'))
        links = [a.attr('href') for a in root.find_all('a') if a.attr('href', '').startswith('/chapter/')]
        self.assertGreater(len(links), 0)
        self.assertRegex(links[0], r'^/chapter/\d+/\d+$')

    def test_chapter_page_title(self):
        html = read_fixture('tibiu_chap.html')
        self.assertIn('<title>', html)
        self.assertIn('TIBIU', html)


if __name__ == '__main__':
    unittest.main()
