"""下载器测试：生命周期、并发、清单、失败收集、插件钩子（全部离线）。"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))

from mccms.mc_config import McModuleConfig  # noqa: E402
from mccms.mc_downloader import (BatchResult, DoNotDownloadImage,  # noqa: E402
                                 JustDownloadSpecificCountImage,
                                 McDownloader, DownloadResult)
from mccms.mc_entity import McChapterDetail, McComicDetail  # noqa: E402
from mccms.mc_exception import (PartialDownloadFailedException,  # noqa: E402
                                VipRequiredException)
from mccms.mc_option import McOption  # noqa: E402
from mccms.mc_plugin import McOptionPlugin  # noqa: E402


class FakeClient:
    """最小可用客户端：不出网，直接写字节。"""

    def __init__(self, comic, chapters, fail_image_index=None, access_error=None):
        self._comic = comic
        self._chapters = chapters          # {chapter_id: McChapterDetail}
        self.fail_image_index = fail_image_index
        self.access_error = access_error
        self.downloaded = []
        self.check_calls = []

    def get_comic_detail(self, comic_id, fetch_chapters=True):
        return self._comic

    def get_chapter_detail(self, chapter_id, comic_id=None, fetch_image_urls=True):
        return self._chapters[str(chapter_id)]

    def check_chapter(self, chapter):
        self.check_calls.append(chapter.chapter_id)
        if self.access_error is not None:
            raise self.access_error

        # 模拟真实客户端：漫画详情给出的章节对象没有图片，需要在这里补齐
        if len(chapter) == 0:
            source = self._chapters.get(str(chapter.chapter_id))
            if source is None:
                raise AssertionError(f'未知章节: {chapter.chapter_id}')
            chapter.set_image_url_list(source.image_url_list)
            chapter.access = source.access
        return True

    def fetch_image_urls(self, chapter):
        return chapter.image_url_list

    def download_by_image_detail(self, image, save_path, decode_image=False):
        if self.fail_image_index is not None and image.index == self.fail_image_index:
            raise RuntimeError('模拟下载失败')
        os.makedirs(os.path.dirname(save_path), exist_ok=True)
        with open(save_path, 'wb') as f:
            f.write(f'image-{image.index}'.encode())
        self.downloaded.append(save_path)
        return save_path


def build_fixture(n_chapters=2, n_images=3):
    episodes = []
    access = {}
    chapters = {}

    for c in range(1, n_chapters + 1):
        chapter_id = str(1000 + c)
        episodes.append((chapter_id, c, f'第{c}话'))
        access[chapter_id] = {'vip': 0, 'cion': 0, 'pnum': n_images, 'addtime': ''}

        chapter = McChapterDetail(
            chapter_id=chapter_id,
            name=f'第{c}话',
            comic_id='17001',
            index=c,
            access=access[chapter_id],
        )
        chapter.set_image_url_list(
            [f'https://cdn.example.com/{chapter_id}/{i}.webp' for i in range(1, n_images + 1)])
        chapters[chapter_id] = chapter

    comic = McComicDetail(
        comic_id='17001',
        name='测试漫画',
        author='tester',
        authors=['tester'],
        episode_list=episodes,
        chapter_access=access,
    )
    for chapter in chapters.values():
        chapter.from_comic = comic

    return comic, chapters


class DownloaderTestBase(unittest.TestCase):

    def make_option(self, tmp, client, **overrides):
        conf = {
            'client': {'impl': 'tibiu'},
            'dir_rule': {'base_dir': tmp, 'rule': 'Bd_Cname_Chindextitle'},
            'log': False,
            'download': {'threading': {'image': 4, 'chapter': 2}},
        }
        conf.update(overrides)
        option = McOption.construct(conf)
        option.build_client = lambda **kwargs: client
        return option


class TestMcDownloader(DownloaderTestBase):

    def test_download_comic(self):
        comic, chapters = build_fixture(n_chapters=2, n_images=3)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            result = McDownloader(option).download_comic('17001')

            self.assertIsInstance(result, McComicDetail)
            self.assertEqual(len(client.downloaded), 6)
            for path in client.downloaded:
                self.assertTrue(os.path.isfile(path), path)
            self.assertIn('测试漫画', client.downloaded[0])

    def test_download_chapter(self):
        comic, chapters = build_fixture()
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            result = McDownloader(option).download_chapter('1001')

            self.assertIsInstance(result, McChapterDetail)
            self.assertEqual(len(client.downloaded), 3)
            self.assertEqual(len(result.save_path) > 0, True)

    def test_manifest_records_images_in_order(self):
        comic, chapters = build_fixture(n_chapters=2, n_images=4)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            downloader = McDownloader(option)
            result = downloader.download_comic('17001')

            manifest = downloader.manifest_dict[result]
            self.assertEqual(len(manifest.image_filepath_list), 8)

            names = [os.path.basename(p) for p in manifest.image_filepath_list]
            self.assertEqual(names[:4], ['1.webp', '2.webp', '3.webp', '4.webp'])
            # 第二话排在后面
            self.assertIn('第2话', manifest.image_filepath_list[4])

    def test_cache_hit_skips_download_but_still_records(self):
        comic, chapters = build_fixture(n_chapters=1, n_images=3)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            McDownloader(option).download_comic('17001')
            first_round = list(client.downloaded)

            client.downloaded.clear()
            option2 = self.make_option(tmp, client)
            downloader = McDownloader(option2)
            result = downloader.download_comic('17001')

            # 第二次全部命中缓存，不应再下载
            self.assertEqual(client.downloaded, [])
            # 但清单里仍应记录全部图片
            self.assertEqual(len(downloader.manifest_dict[result].image_filepath_list), 3)
            self.assertEqual(len(first_round), 3)

    def test_image_failure_is_collected_and_raised(self):
        comic, chapters = build_fixture(n_chapters=1, n_images=3)
        client = FakeClient(comic, chapters, fail_image_index=2)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            downloader = McDownloader(option)
            downloader.download_comic('17001')

            self.assertTrue(downloader.has_download_failures)
            self.assertEqual(len(downloader.download_failed_image), 1)
            self.assertFalse(downloader.all_success)

            with self.assertRaises(PartialDownloadFailedException):
                downloader.raise_if_has_exception()

    def test_access_error_is_reraised_as_is(self):
        comic, chapters = build_fixture(n_chapters=1, n_images=3)
        client = FakeClient(comic, chapters, access_error=VipRequiredException())

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            downloader = McDownloader(option)
            downloader.download_comic('17001')

            # 权限异常应原样抛出，而不是被包装成 PartialDownloadFailedException
            with self.assertRaises(VipRequiredException):
                downloader.raise_if_has_exception()

    def test_threading_config_respected(self):
        comic, chapters = build_fixture(n_chapters=4, n_images=2)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client, download={'threading': {'image': 2, 'chapter': 4}})
            self.assertEqual(option.decide_chapter_batch_count(comic), 4)
            self.assertEqual(option.decide_image_batch_count(chapters['1001']), 2)

            McDownloader(option).download_comic('17001')
            self.assertEqual(len(client.downloaded), 8)


class TestSpecialDownloaders(DownloaderTestBase):

    def test_do_not_download_image(self):
        comic, chapters = build_fixture(n_chapters=1, n_images=3)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            downloader = DoNotDownloadImage(option)
            result = downloader.download_comic('17001')

            self.assertEqual(client.downloaded, [])
            self.assertEqual(len(downloader.manifest_dict[result].image_filepath_list), 3)

    def test_just_download_specific_count(self):
        comic, chapters = build_fixture(n_chapters=2, n_images=5)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client)
            McDownloader(option)  # 触碰一次，确保 option 可用
            JustDownloadSpecificCountImage(option, count=2).download_comic('17001')

            self.assertEqual(len(client.downloaded), 4)  # 2 话 × 2 张


class TestPluginHooks(DownloaderTestBase):

    def test_hooks_are_called_in_order(self):
        events = []

        class SpyPlugin(McOptionPlugin):
            plugin_key = 'test_spy'

            def invoke(self, event=None, **kwargs):
                events.append(event)

        McModuleConfig.register_plugin(SpyPlugin)
        try:
            comic, chapters = build_fixture(n_chapters=1, n_images=2)
            client = FakeClient(comic, chapters)

            with tempfile.TemporaryDirectory() as tmp:
                # 图片并发设为 1，保证钩子调用顺序可预测
                option = self.make_option(tmp, client,
                                          download={'threading': {'image': 1, 'chapter': 1}},
                                          plugins={
                    'before_comic': [{'plugin': 'test_spy', 'kwargs': {'event': 'before_comic'}}],
                    'after_comic': [{'plugin': 'test_spy', 'kwargs': {'event': 'after_comic'}}],
                    'before_chapter': [{'plugin': 'test_spy', 'kwargs': {'event': 'before_chapter'}}],
                    'after_chapter': [{'plugin': 'test_spy', 'kwargs': {'event': 'after_chapter'}}],
                    'before_image': [{'plugin': 'test_spy', 'kwargs': {'event': 'before_image'}}],
                    'after_image': [{'plugin': 'test_spy', 'kwargs': {'event': 'after_image'}}],
                })
                McDownloader(option).download_comic('17001')
        finally:
            McModuleConfig.REGISTRY_PLUGIN.pop('test_spy', None)

        self.assertEqual(events[0], 'before_comic')
        self.assertEqual(events[1], 'before_chapter')
        self.assertEqual(events[2], 'before_image')
        self.assertEqual(events[3], 'after_image')
        self.assertEqual(events[4], 'before_image')
        self.assertEqual(events[5], 'after_image')
        self.assertEqual(events[6], 'after_chapter')
        self.assertEqual(events[-1], 'after_comic')

    def test_skip_chapter_plugin(self):
        from mccms.mc_plugin import SkipChapterWithFewImagesPlugin

        comic, chapters = build_fixture(n_chapters=2, n_images=2)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = self.make_option(tmp, client, plugins={
                'before_chapter': [{'plugin': 'skip_chapter_with_few_images',
                                    'kwargs': {'at_least_image_count': 3}}],
            })
            self.assertIn('skip_chapter_with_few_images', McModuleConfig.REGISTRY_PLUGIN)
            McDownloader(option).download_comic('17001')

            # 每话只有 2 张，阈值 3 -> 全部跳过
            self.assertEqual(client.downloaded, [])

            _ = SkipChapterWithFewImagesPlugin  # 保持导入引用


class TestBatchResultAndApi(unittest.TestCase):

    def test_download_result_properties(self):
        comic, chapters = build_fixture(n_chapters=1, n_images=2)
        client = FakeClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = McOption.construct({
                'client': {'impl': 'tibiu'},
                'dir_rule': {'base_dir': tmp},
                'log': False,
            })
            option.build_client = lambda **kwargs: client

            from mccms.api import download_comic
            result = download_comic('17001', option)

            self.assertIsInstance(result, DownloadResult)
            self.assertEqual(result.detail.comic_id, '17001')
            self.assertEqual(len(result.manifest.image_filepath_list), 2)
            self.assertIsNotNone(result.duration)

    def test_batch_download_collects_failures(self):
        comic, chapters = build_fixture(n_chapters=1, n_images=2)
        client = FakeClient(comic, chapters)

        class FlakyClient(FakeClient):
            def get_comic_detail(self, comic_id, fetch_chapters=True):
                if str(comic_id) == 'bad':
                    raise RuntimeError('模拟失败')
                return super().get_comic_detail(comic_id, fetch_chapters)

        flaky = FlakyClient(comic, chapters)

        with tempfile.TemporaryDirectory() as tmp:
            option = McOption.construct({
                'client': {'impl': 'tibiu'},
                'dir_rule': {'base_dir': tmp},
                'log': False,
            })
            option.build_client = lambda **kwargs: flaky

            from mccms.api import download_comic
            result = download_comic(['17001', 'bad'], option)

            self.assertIsInstance(result, BatchResult)
            self.assertEqual(len(result.failed), 1)
            self.assertIn('bad', result.failed)
            self.assertFalse(result.all_succeeded)
            self.assertEqual(result.total, 2)


if __name__ == '__main__':
    unittest.main()
