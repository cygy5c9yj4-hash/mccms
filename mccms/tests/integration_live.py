#!/usr/bin/env python3
"""
实网集成冒烟测试（需要联网，不属于单元测试）。

用途：在真实站点上验证「搜索 → 详情 → 章节 → 取图 → 下载」整条链路。

    python3 tests/integration_live.py

它只会：
  - 读取公开页面
  - 下载少量图片（每个用例最多 3 张）
不会做任何越权尝试；遇到需要登录/会员的内容会打印预期的异常类型并继续。

退出码 0 表示全部通过。
"""

import os
import sys
import tempfile
import traceback

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))

import mccms  # noqa: E402
from mccms import JustDownloadSpecificCountImage  # noqa: E402

PASSED = []
FAILED = []
SKIPPED = []


def check(name, func):
    try:
        detail = func()
        if detail is not None and str(detail).startswith('SKIP:'):
            SKIPPED.append((name, str(detail)))
            print(f'  [SKIP] {name} -> {detail}')
            return
        PASSED.append(name)
        print(f'  [OK]   {name}' + (f' -> {detail}' if detail else ''))
    except Exception as e:
        FAILED.append((name, e))
        print(f'  [FAIL] {name}: {type(e).__name__}: {e}')
        traceback.print_exc()


# --------------------------------------------------------------------------------------
# TIBIU
# --------------------------------------------------------------------------------------

def test_tibiu():
    print('\n=== TIBIU (cache.tibiu.net) ===')
    option = mccms.McOption.construct({
        'client': {'impl': 'tibiu'},
        'log': False,
    })
    client = option.build_client()

    def search():
        page = client.search('love', 1)
        assert len(page) > 0, '搜索无结果'
        return f'{len(page)} 条，首条={page.content[0][1]["name"][:20]}'
    check('搜索', search)

    def detail():
        comic = client.get_comic_detail('17001')
        assert len(comic) > 0, '章节列表为空'
        return f'{comic.name} / {len(comic)} 话 / {comic.author}'
    check('漫画详情', detail)

    def chapter_images():
        comic = client.get_comic_detail('17001')
        chapter = comic[0]
        urls = client.fetch_image_urls(chapter)
        assert len(urls) > 0
        return f'{chapter.name} / {len(urls)} 张'
    check('章节取图', chapter_images)

    def ranking():
        page = client.ranking('top', 1)
        assert len(page) > 0
        return f'{len(page)} 条'
    check('排行榜', ranking)

    def categories():
        page = client.categories_filter(page=1)
        assert len(page) > 0
        return f'{len(page)} 条'
    check('分类浏览', categories)

    def update_list():
        page = client.update_list(1)
        assert len(page) > 0
        return f'{len(page)} 条'
    check('最近更新', update_list)

    def download():
        with tempfile.TemporaryDirectory() as tmp:
            dl_option = mccms.McOption.construct({
                'client': {'impl': 'tibiu'},
                'dir_rule': {'base_dir': tmp, 'rule': 'Bd_Cname_Chindextitle'},
                'log': False,
            })
            result = mccms.download_chapter('291931', dl_option,
                                            downloader=JustDownloadSpecificCountImage,
                                            comic_id='17001')
            paths = result.manifest.image_filepath_list
            assert len(paths) == 3, f'期望 3 张，实际 {len(paths)}'
            for p in paths:
                assert os.path.isfile(p), p
            return f'{len(paths)} 张已落盘'
    check('下载章节（限 3 张）', download)


# --------------------------------------------------------------------------------------
# manhwa
# --------------------------------------------------------------------------------------

def test_manhwa():
    print('\n=== manhwa (www.manhwa.wang) ===')
    option = mccms.McOption.construct({
        'client': {'impl': 'manhwa'},
        'log': False,
    })
    client = option.build_client()

    def search():
        page = client.search('魔咒', 1)
        assert len(page) > 0, '搜索无结果'
        return f'{len(page)} 条，首条={page.content[0][1]["name"][:20]}'
    check('搜索', search)

    def detail_by_slug():
        comic = client.get_comic_detail('mozhouweixianzaoyu')
        assert len(comic) > 0
        return f'[{comic.comic_id}] {comic.name[:20]} / {len(comic)} 话 / {comic.serialize}'
    check('漫画详情（slug）', detail_by_slug)

    def hot():
        page = client.hot_list()
        assert len(page) > 0
        return f'{len(page)} 条'
    check('热门列表', hot)

    def free_chapter():
        chapter = client.get_chapter_detail('937510')
        assert len(chapter) > 0
        assert chapter.is_free
        return f'{chapter.name} / {len(chapter)} 张 / {chapter.access_desc}'
    check('免费章节取图', free_chapter)

    def vip_chapter_expected_error():
        """VIP 章节应当抛出明确的权限异常（这就是预期行为，不是失败）。"""
        chapter = client.get_chapter_detail('936939', comic_id='27032', fetch_image_urls=False)
        assert not chapter.is_free, '该章节应当是 VIP'
        try:
            client.fetch_image_urls(chapter)
        except mccms.LoginRequiredException as e:
            return f'预期异常 LoginRequiredException: {e.context.get("raw_msg")}'
        except mccms.AccessDeniedException as e:
            return f'预期异常 {type(e).__name__}'
        raise AssertionError('VIP 章节竟然直接返回了图片，这与站点行为不符')
    check('VIP 章节报错语义', vip_chapter_expected_error)

    def download_free():
        with tempfile.TemporaryDirectory() as tmp:
            dl_option = mccms.McOption.construct({
                'client': {'impl': 'manhwa'},
                'dir_rule': {'base_dir': tmp, 'rule': 'Bd_Cname_Chindextitle'},
                'log': False,
            })
            try:
                result = mccms.download_chapter('937510', dl_option,
                                                downloader=JustDownloadSpecificCountImage)
            except mccms.PartialDownloadFailedException as e:
                # 该站唯一免费章节的图片已从 CDN 移除（404），
                # 说明链路本身正常，只是资源不存在，按跳过处理。
                if 'http_code: [404]' in str(e):
                    return 'SKIP: 该免费章节的图片已从 CDN 移除（404），链路本身正常'
                raise

            paths = result.manifest.image_filepath_list
            assert len(paths) == 3, f'期望 3 张，实际 {len(paths)}'
            return f'{len(paths)} 张已落盘'
    check('下载免费章节（限 3 张）', download_free)


def test_manhwa_paywall_overview():
    """
    记录一个事实：漫蛙站点几乎全部章节都是 vip=1。
    库对此的行为是「明确报错」，而不是静默失败。
    """
    print('\n=== manhwa 内容可访问性概览 ===')
    option = mccms.McOption.construct({'client': {'impl': 'manhwa'}, 'log': False})
    client = option.build_client()

    page = client.hot_list()
    total_free = 0
    total_chapter = 0
    for comic_id, _ in page.content[:5]:
        try:
            comic = client.get_comic_detail(comic_id)
        except Exception:
            continue
        total_chapter += len(comic)
        total_free += sum(1 for c in comic if c.is_free)

    print(f'  抽查 5 本热门漫画: 共 {total_chapter} 话，其中免费 {total_free} 话')
    print('  -> 需要登录/会员的章节会抛 LoginRequiredException / VipRequiredException')


def test_boylove():
    print('\n=== 香香腐宅 (boylove.cc) ===')
    option = mccms.McOption.construct({'client': {'impl': 'boylove'}, 'log': False})
    client = option.build_client()

    def search():
        page = client.search('魔咒')
        assert len(page) > 0, '搜索无结果'
        return f'{len(page)} 条，首条={page.content[0][1]["name"][:20]}'
    check('搜索', search)

    def detail():
        comic = client.get_comic_detail('16904')
        assert len(comic) > 100
        return f'{comic.name} / {len(comic)} 话 / {comic.author} / {comic.serialize}'
    check('漫画详情', detail)

    def chapter_images():
        comic = client.get_comic_detail('16904')
        chapter = comic[0]
        urls = client.fetch_image_urls(chapter)
        assert len(urls) > 50
        # 正文图必须都以章节 id 开头（页面里混有推荐位缩略图）
        bad = [u for u in urls if not u.rsplit('/', 1)[-1].startswith(chapter.chapter_id + '-')]
        assert not bad, f'混入非正文图: {bad[:2]}'
        return f'{chapter.name} / {len(urls)} 张（推荐位已过滤）'
    check('章节取图', chapter_images)

    def ranking():
        nav = client.ranking_nav()
        page = client.ranking(1)
        assert len(page) > 0
        return f'{len(nav)} 组榜单，人气榜 {len(page)} 条'
    check('榜单', ranking)

    def categories():
        page = client.categories_filter(page=1, vip=0)
        assert len(page) > 0
        return f'免费分类 {len(page)} 条'
    check('分类浏览', categories)

    def download():
        with tempfile.TemporaryDirectory() as tmp:
            dl_option = mccms.McOption.construct({
                'client': {'impl': 'boylove'},
                'dir_rule': {'base_dir': tmp, 'rule': 'Bd_Cname_Chindextitle'},
                'log': False,
            })
            result = mccms.download_chapter('435648', dl_option,
                                            downloader=JustDownloadSpecificCountImage,
                                            comic_id='16904')
            paths = result.manifest.image_filepath_list
            assert len(paths) == 3, f'期望 3 张，实际 {len(paths)}'
            for p in paths:
                assert os.path.isfile(p), p
            return f'{len(paths)} 张已落盘 -> {os.path.basename(result.detail.save_path)}'
    check('下载章节（限 3 张）', download)

    def scrambled_chapter():
        """新章节的图片是竖带倒序下发的，下载后必须还原成连续画面。"""
        chapter = client.get_chapter_detail('2623003', comic_id='16904',
                                            fetch_image_urls=False)
        assert chapter.scramble_n > 1, f'该章节应当标记为乱序，实际 {chapter.scramble_n}'

        with tempfile.TemporaryDirectory() as tmp:
            dl_option = mccms.McOption.construct({
                'client': {'impl': 'boylove'},
                'dir_rule': {'base_dir': tmp, 'rule': 'Bd_Chindextitle'},
                'log': False,
            })
            result = mccms.download_chapter('2623003', dl_option,
                                            downloader=JustDownloadSpecificCountImage,
                                            comic_id='16904')
            paths = result.manifest.image_filepath_list
            assert len(paths) == 3

            from PIL import Image
            import numpy as np

            zs = []
            for path in paths:
                a = np.asarray(Image.open(path).convert('L'), dtype=np.float32)
                _, width = a.shape
                n = chapter.scramble_n
                strip = width // n
                costs = [float(np.abs(a[:, x] - a[:, x - 1]).mean())
                         for x in range(5, width - 5, 7)]
                base, sd = float(np.mean(costs)), float(np.std(costs))
                seams = [float(np.abs(a[:, strip * k] - a[:, strip * k - 1]).mean())
                         for k in range(1, n) if 0 < strip * k < width]
                zs.append(float(np.mean([(s - base) / sd for s in seams])))

            worst = max(abs(z) for z in zs)
            assert worst < 2.0, f'接缝仍然断裂，z={zs}'
            return f'{len(paths)} 张已还原，接缝 z={max(zs):+.2f}'
    check('乱序章节自动还原', scrambled_chapter)


def main():
    print('mccms 实网集成冒烟测试')
    test_tibiu()
    test_manhwa()
    test_boylove()
    test_manhwa_paywall_overview()

    print(f'\n结果: {len(PASSED)} 通过, {len(SKIPPED)} 跳过, {len(FAILED)} 失败')
    for name, detail in SKIPPED:
        print(f'  ~ {name}: {detail}')
    if FAILED:
        for name, e in FAILED:
            print(f'  - {name}: {type(e).__name__}: {e}')
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
