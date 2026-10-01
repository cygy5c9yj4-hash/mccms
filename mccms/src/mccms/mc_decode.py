"""
图片乱序还原（对应站点前端的 `do_mergeImg`）。

背景
----
部分站点会把整页图切成 N 条**竖向条带**并倒序后再下发，
由前端用 canvas 还原（香香腐宅的新章节就是这么做的）。

站点前端源码（混淆后）等价于::

    function do_mergeImg(canvas, img, W, H, url, N) {
        img.src = url;
        img.addEventListener('load', function () {
            for (var i = 1; i <= N; i++) {
                if (H >= 4000) {
                    // 超高图：原样拷贝（等于不解码）
                    canvas.drawImage(img, floor(W/N)*(i-1), 0, floor(W/N), H,
                                          floor(W/N)*(i-1), 0, floor(W/N), H);
                } else if (i == N) {
                    // 最后一条：把最左边那条（含宽度余数）放到最右边
                    var w = W - floor(W/N)*(N-1);
                    canvas.drawImage(img, 0, 0, w, H, floor(W/N)*(N-1), 0, w, H);
                } else {
                    // 其余：把右边第 i 条放到左边第 i 个位置
                    var w = floor(W/N);
                    canvas.drawImage(img, W - floor(W/N)*i, 0, w, H,
                                          floor(W/N)*(i-1), 0, w, H);
                }
            }
        });
    }

即：**目标位置 j 放原图第 (N+1-j) 条竖带**，等价于把 N 条竖带整体倒序。

由于倒序是自逆运算，同样的操作既能用于「还原」，也能用于「生成」——
本模块只做还原。

注意
----
- N 由站点按章节下发（同一本漫画不同章节可能不同），必须逐章读取，不能写死。
- 条带宽度不是编码块大小的整数倍时，无法在压缩域里做无损搬移，
  因此还原必然要重新编码；本模块默认用高质量重新编码。
"""

import os
from typing import Optional

__all__ = [
    'reverse_vertical_strips',
    'decode_scrambled_image_file',
    'is_scrambled_layout',
    'ImageDecodeError',
]


class ImageDecodeError(Exception):
    """图片还原失败。"""


def _require_pillow():
    try:
        from PIL import Image  # noqa: F401
    except ImportError:  # pragma: no cover
        raise ImageDecodeError(
            '图片还原需要 Pillow，请先安装: pip install pillow'
        ) from None


def is_scrambled_layout(width: int, height: int, n: int) -> bool:
    """
    判断该图是否值得做倒序还原。

    与站点前端保持一致：``H >= 4000`` 时前端做的是原样拷贝（等于不解码），
    这里同样返回 False，避免把本来就正常的超长图弄坏。
    """
    return n is not None and int(n) > 1 and width // int(n) >= 1 and height < 4000


def reverse_vertical_strips(image, n: int):
    """
    把图像按 ``n`` 条竖带倒序。

    :param image: PIL.Image
    :param n: 条带数量
    :return: 新的 PIL.Image
    """
    _require_pillow()
    from PIL import Image

    n = int(n)
    if n <= 1:
        return image

    width, height = image.size
    strip = width // n
    if strip < 1:
        raise ImageDecodeError(f'图片宽度 {width} 不足以切成 {n} 条竖带')

    out = Image.new(image.mode, (width, height))
    for i in range(1, n + 1):
        if i == n:
            # 最后一条：最左边那条，宽度含余数
            sw = width - strip * (n - 1)
            sx, dx = 0, strip * (n - 1)
        else:
            sw = strip
            sx, dx = width - strip * i, strip * (i - 1)
        out.paste(image.crop((sx, 0, sx + sw, height)), (dx, 0))

    return out


def decode_scrambled_image_file(filepath: str,
                                n: int,
                                quality: int = 95,
                                out_path: Optional[str] = None) -> str:
    """
    就地（或另存）还原一个被竖带倒序的图片文件。

    :param filepath: 原图路径
    :param n: 条带数量（站点下发的 randomClass）
    :param quality: 有损格式重新编码时的质量
    :param out_path: 输出路径；为空则覆盖原文件
    :return: 输出路径
    """
    _require_pillow()
    from PIL import Image

    out_path = out_path or filepath

    with Image.open(filepath) as img:
        img.load()
        width, height = img.size

        if not is_scrambled_layout(width, height, n):
            return filepath

        decoded = reverse_vertical_strips(img, n)

        suffix = os.path.splitext(out_path)[1].lower()
        fmt = {
            '.webp': 'WEBP', '.jpg': 'JPEG', '.jpeg': 'JPEG',
            '.png': 'PNG', '.bmp': 'BMP', '.gif': 'GIF', '.avif': 'AVIF',
        }.get(suffix) or (img.format or 'PNG')

        save_kwargs = {}
        if fmt in ('JPEG', 'WEBP'):
            save_kwargs['quality'] = quality
        if fmt == 'WEBP':
            save_kwargs['method'] = 4

        if decoded.mode in ('RGBA', 'P', 'LA') and fmt in ('JPEG',):
            decoded = decoded.convert('RGB')

        decoded.save(out_path, format=fmt, **save_kwargs)

    return out_path
