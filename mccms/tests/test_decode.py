"""
图片竖带倒序还原的测试。

算法与站点前端 ``do_mergeImg`` 一致：目标位置 j 放原图第 (N+1-j) 条竖带。
由于倒序是自逆运算，「生成」和「还原」用同一个函数，测试里可以直接互为验证。
"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))
sys.path.insert(0, os.path.dirname(__file__))

from PIL import Image, ImageDraw  # noqa: E402

from mccms.mc_decode import (ImageDecodeError, decode_scrambled_image_file,  # noqa: E402
                             is_scrambled_layout, reverse_vertical_strips)


def make_striped_image(width, height, n):
    """生成 n 条颜色各异的竖带图，便于精确断言顺序。"""
    img = Image.new('RGB', (width, height))
    draw = ImageDraw.Draw(img)
    strip = width // n
    palette = [(255, 0, 0), (0, 255, 0), (0, 0, 255), (255, 255, 0),
               (0, 255, 255), (255, 0, 255), (128, 128, 128), (255, 128, 0),
               (0, 128, 255), (128, 255, 0), (255, 0, 128), (0, 0, 0)]
    for i in range(n):
        x0 = strip * i
        x1 = width if i == n - 1 else strip * (i + 1)
        draw.rectangle([x0, 0, x1 - 1, height - 1], fill=palette[i % len(palette)])
    return img


def strip_colors(img, n):
    """取每条竖带最左列的像素颜色。"""
    width, height = img.size
    strip = width // n
    px = img.load()
    return [px[strip * i, height // 2] for i in range(n)]


class TestReverseVerticalStrips(unittest.TestCase):

    def test_strips_are_reversed(self):
        n = 8
        img = make_striped_image(240, 40, n)
        before = strip_colors(img, n)

        out = reverse_vertical_strips(img, n)
        after = strip_colors(out, n)

        self.assertEqual(after, list(reversed(before)))

    def test_reverse_is_involution(self):
        """倒序两次应回到原图（这也是「站点用它乱序、我们用它还原」成立的前提）。"""
        n = 11
        img = make_striped_image(649, 120, n)
        twice = reverse_vertical_strips(reverse_vertical_strips(img, n), n)
        self.assertEqual(list(img.getdata()), list(twice.getdata()))

    def test_remainder_strip(self):
        """
        宽度不能整除 n 时的映射（严格复刻站点 do_mergeImg）：

        W=100, n=3, strip=33
          i=1: src[67:100] -> dst[0:33]
          i=2: src[34:67]  -> dst[33:66]
          i=3: src[0:34]   -> dst[66:100]   （最左那条含余数，落到最右）
        """
        n = 3
        width = 100
        strip = width // n

        img = Image.new('RGB', (width, 4))
        px = img.load()
        for x in range(width):
            px[x, 0] = (x % 256, 0, 0)

        out = reverse_vertical_strips(img, n)
        out_px = out.load()

        expected = ([0] * width)
        for i in range(1, n + 1):
            if i == n:
                sw, sx, dx = width - strip * (n - 1), 0, strip * (n - 1)
            else:
                sw, sx, dx = strip, width - strip * i, strip * (i - 1)
            for k in range(sw):
                expected[dx + k] = (sx + k) % 256

        actual = [out_px[x, 0][0] for x in range(width)]
        self.assertEqual(actual, expected)

        # 关键位置再点一下，便于读测试的人理解
        self.assertEqual(actual[0], 67)      # 原图最右那条的开头
        self.assertEqual(actual[99], 33)     # 原图最右那条的结尾
        self.assertEqual(actual[66], 0)      # 原图最左那条落到最右

    def test_n_le_1_is_noop(self):
        img = make_striped_image(100, 10, 4)
        self.assertIs(reverse_vertical_strips(img, 1), img)
        self.assertIs(reverse_vertical_strips(img, 0), img)

    def test_width_too_small(self):
        img = Image.new('RGB', (4, 10))
        with self.assertRaises(ImageDecodeError):
            reverse_vertical_strips(img, 10)


class TestIsScrambledLayout(unittest.TestCase):

    def test_normal(self):
        self.assertTrue(is_scrambled_layout(649, 993, 11))

    def test_n_le_1(self):
        self.assertFalse(is_scrambled_layout(649, 993, 1))
        self.assertFalse(is_scrambled_layout(649, 993, 0))

    def test_very_tall_image_not_decoded(self):
        """站点前端对 H >= 4000 的图做的是原样拷贝，我们保持一致，避免弄坏正常超长图。"""
        self.assertFalse(is_scrambled_layout(800, 4000, 11))
        self.assertFalse(is_scrambled_layout(800, 9000, 11))
        self.assertTrue(is_scrambled_layout(800, 3999, 11))


class TestDecodeFile(unittest.TestCase):

    def test_file_roundtrip(self):
        n = 11
        original = make_striped_image(649, 120, n)
        scrambled = reverse_vertical_strips(original, n)

        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, 'scrambled.png')
            scrambled.save(path, 'PNG')

            decode_scrambled_image_file(path, n)

            with Image.open(path) as decoded:
                self.assertEqual(decoded.size, original.size)
                self.assertEqual(list(decoded.convert('RGB').getdata()),
                                 list(original.getdata()))

    def test_decode_skips_when_not_needed(self):
        original = make_striped_image(300, 60, 3)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, 'plain.png')
            original.save(path, 'PNG')

            returned = decode_scrambled_image_file(path, 1)   # n=1 -> 不处理
            self.assertEqual(returned, path)

            with Image.open(path) as img:
                self.assertEqual(list(img.convert('RGB').getdata()),
                                 list(original.getdata()))

    def test_decode_to_separate_path(self):
        n = 5
        original = make_striped_image(505, 40, n)
        scrambled = reverse_vertical_strips(original, n)

        with tempfile.TemporaryDirectory() as tmp:
            src = os.path.join(tmp, 's.png')
            dst = os.path.join(tmp, 'd.png')
            scrambled.save(src, 'PNG')

            out = decode_scrambled_image_file(src, n, out_path=dst)
            self.assertEqual(out, dst)
            self.assertTrue(os.path.isfile(dst))
            with Image.open(dst) as img:
                self.assertEqual(list(img.convert('RGB').getdata()),
                                 list(original.getdata()))


class TestEntityIntegration(unittest.TestCase):
    """确认乱序标记能从章节传递到图片，并被下载流程使用。"""

    def test_scramble_n_propagates(self):
        from mccms.mc_entity import McChapterDetail

        chapter = McChapterDetail(chapter_id='1', name='第1话', scramble_n=11)
        chapter.set_image_url_list(['https://cdn/x/a.webp'])
        image = chapter[0]

        self.assertEqual(chapter.scramble_n, 11)
        self.assertEqual(image.scramble_n, 11)
        self.assertTrue(image.is_scrambled)

    def test_plain_chapter_not_marked(self):
        from mccms.mc_entity import McChapterDetail

        chapter = McChapterDetail(chapter_id='1', name='第1话')
        chapter.set_image_url_list(['https://cdn/x/a.webp'])
        self.assertEqual(chapter.scramble_n, 0)
        self.assertFalse(chapter[0].is_scrambled)


if __name__ == '__main__':
    unittest.main()
