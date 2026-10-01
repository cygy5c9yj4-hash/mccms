"""实体层测试：索引 / 切片 / 下载状态字段 / 权限判定。"""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))

from mccms.mc_entity import (McChapterDetail, McComicDetail,  # noqa: E402
                             McImageDetail, McSearchPage)
from mccms.mc_exception import McException  # noqa: E402


def make_chapter(chapter_id='291931', index=1, name='第01话', access=None, n_images=3):
    chapter = McChapterDetail(
        chapter_id=chapter_id,
        name=name,
        comic_id='17001',
        index=index,
        access=access or {'vip': 0, 'cion': 0, 'pnum': n_images},
    )
    urls = [f'https://cdn.example.com/ch/{chapter_id}/{i:04d}.webp' for i in range(1, n_images + 1)]
    chapter.set_image_url_list(urls)
    return chapter


class TestDownloadableState(unittest.TestCase):
    """确认 Downloadable.__init__ 能通过 MRO 正确初始化。"""

    def test_comic_has_download_state(self):
        comic = McComicDetail(comic_id='1', name='x')
        self.assertEqual(comic.save_path, '')
        self.assertFalse(comic.exists)
        self.assertFalse(comic.skip)
        self.assertTrue(comic.cache)
        self.assertIsNone(comic.duration)

    def test_chapter_has_download_state(self):
        chapter = make_chapter()
        self.assertEqual(chapter.save_path, '')
        self.assertTrue(chapter.cache)

    def test_image_has_download_state(self):
        chapter = make_chapter()
        image = chapter[0]
        self.assertFalse(image.exists)
        self.assertTrue(image.cache)


class TestMcImageDetail(unittest.TestCase):

    def test_of_parses_filename_and_suffix(self):
        image = McImageDetail.of('1', 'https://cdn/a/b/c.webp', index=1)
        self.assertEqual(image.img_file_name, 'c')
        self.assertEqual(image.img_file_suffix, '.webp')
        self.assertEqual(image.filename, 'c.webp')
        self.assertFalse(image.is_gif)

    def test_of_handles_query_string(self):
        image = McImageDetail.of('1', 'https://cdn/a/b/c.jpg?v=123', index=1)
        self.assertEqual(image.filename, 'c.jpg')

    def test_of_gif(self):
        image = McImageDetail.of('1', 'https://cdn/a/b/c.gif', index=1)
        self.assertTrue(image.is_gif)

    def test_of_rejects_empty_url(self):
        with self.assertRaises(McException):
            McImageDetail.of('1', '   ', index=1)

    def test_of_without_suffix_defaults_jpg(self):
        image = McImageDetail.of('1', 'https://cdn/a/b/noext', index=1)
        self.assertEqual(image.img_file_suffix, '.jpg')


class TestMcChapterDetail(unittest.TestCase):

    def test_indexing_and_len(self):
        chapter = make_chapter(n_images=5)
        self.assertEqual(len(chapter), 5)
        self.assertEqual(chapter[0].index, 1)
        self.assertEqual(chapter[4].index, 5)
        self.assertEqual(chapter[-1].index, 5)
        self.assertEqual(len(chapter[:2]), 2)
        self.assertEqual([i.index for i in chapter], [1, 2, 3, 4, 5])

    def test_index_out_of_range(self):
        chapter = make_chapter(n_images=2)
        with self.assertRaises(McException):
            chapter.create_image_detail(5)

    def test_set_image_url_list_resets_count(self):
        chapter = make_chapter(n_images=1)
        chapter.set_image_url_list(['https://a/1.jpg', 'https://a/2.jpg'])
        self.assertEqual(len(chapter), 2)

    def test_access_flags(self):
        free = make_chapter(access={'vip': 0, 'cion': 0, 'price': 0})
        self.assertTrue(free.is_free)
        self.assertEqual(free.access_desc, '免费')

        vip = make_chapter(access={'vip': 1, 'cion': 0, 'price': 0})
        self.assertFalse(vip.is_free)
        self.assertEqual(vip.access_desc, 'VIP')

        coin = make_chapter(access={'vip': 0, 'cion': 50, 'price': 0})
        self.assertIn('金币', coin.access_desc)

    def test_indextitle_avoids_duplicate_prefix(self):
        self.assertEqual(make_chapter(index=3, name='暴君').indextitle, '第3话 暴君')
        self.assertEqual(make_chapter(index=3, name='第3章：暴君').indextitle, '第3章：暴君')
        self.assertEqual(make_chapter(index=3, name='第3话').indextitle, '第3话')

    def test_is_chapter_and_alias(self):
        chapter = make_chapter()
        self.assertTrue(chapter.is_chapter())
        self.assertTrue(chapter.is_photo())
        self.assertFalse(chapter.is_comic())
        self.assertFalse(chapter.is_image())


class TestMcComicDetail(unittest.TestCase):

    def setUp(self):
        self.comic = McComicDetail(
            comic_id='17001',
            name='社内恋爱/内部爱情',
            author='이로비',
            authors=['이로비', '홈ㄹ1스'],
            tags=['BL'],
            serialize='完结',
            episode_list=[('1002', 2, '第2话'), ('1001', 1, '第1话'), ('1003', 3, '第3话')],
            chapter_access={
                '1001': {'vip': 0, 'pnum': 10},
                '1002': {'vip': 1, 'pnum': 20},
                '1003': {'vip': 0, 'pnum': 30},
            },
        )

    def test_episodes_sorted_and_reindexed(self):
        self.assertEqual([c.chapter_id for c in self.comic], ['1001', '1002', '1003'])
        self.assertEqual([c.index for c in self.comic], [1, 2, 3])

    def test_indexing_and_slicing(self):
        self.assertEqual(len(self.comic), 3)
        self.assertEqual(self.comic[0].chapter_id, '1001')
        self.assertEqual(self.comic[-1].chapter_id, '1003')
        self.assertEqual([c.chapter_id for c in self.comic[:2]], ['1001', '1002'])
        self.assertEqual(self.comic[1].access_desc, 'VIP')

    def test_chapter_carries_comic_context(self):
        chapter = self.comic[0]
        self.assertIs(chapter.from_comic, self.comic)
        self.assertEqual(chapter.author, '이로비')
        self.assertEqual(chapter.tags, ['BL'])
        self.assertEqual(chapter.comic_name, self.comic.name)

    def test_page_count_and_finished(self):
        self.assertEqual(self.comic.page_count, 60)
        self.assertTrue(self.comic.is_finished)

    def test_distinct_episode_dedupes(self):
        deduped = McComicDetail.distinct_episode([('1', 1, 'a'), ('1', 1, 'a'), ('2', 2, 'b')])
        self.assertEqual(deduped, [('1', 1, 'a'), ('2', 2, 'b')])

    def test_empty_episode_list_becomes_single_chapter(self):
        comic = McComicDetail(comic_id='9', name='单章')
        self.assertEqual(len(comic), 1)
        self.assertEqual(comic[0].chapter_id, '9')

    def test_missing_chapter_raises(self):
        with self.assertRaises(McException):
            self.comic.create_chapter_detail(99)

    def test_is_comic_and_alias(self):
        self.assertTrue(self.comic.is_comic())
        self.assertTrue(self.comic.is_album())

    def test_oname(self):
        comic = McComicDetail(comic_id='1', name='喂我吃吧 老師! [漢化組] [DL版]')
        self.assertEqual(comic.oname, '喂我吃吧 老師!')
        self.assertEqual(comic.authoroname.startswith('【'), True)


class TestMcSearchPage(unittest.TestCase):

    def setUp(self):
        self.page = McSearchPage([
            ('1', {'name': 'A', 'author': 'x', 'tags': ['t1']}),
            ('2', {'name': 'B', 'author': 'y', 'tags': []}),
        ], total=25, page_number=1)

    def test_basic(self):
        self.assertEqual(len(self.page), 2)
        self.assertEqual(list(self.page.iter_id()), ['1', '2'])
        self.assertEqual(list(self.page.iter_id_title()), [('1', 'A'), ('2', 'B')])
        self.assertEqual(self.page.page_count, 3)

    def test_to_comic_list(self):
        comics = self.page.to_comic_list()
        self.assertEqual(len(comics), 2)
        self.assertEqual(comics[0].comic_id, '1')
        self.assertEqual(comics[0].name, 'A')

    def test_is_page(self):
        self.assertTrue(self.page.is_page())


if __name__ == '__main__':
    unittest.main()
