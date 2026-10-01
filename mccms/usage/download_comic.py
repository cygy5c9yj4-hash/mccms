#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
下载整本漫画（带 option 配置 + 自定义下载器限制范围）

重点演示三件事：
1. 用 McOption.construct(dict) 写完整配置（dir_rule / download / client / plugins）
2. 用「自定义下载器」重写 do_filter，把下载范围压到很小
   —— 本库没有"只下前 N 话"的内置开关，官方做法就是重写 do_filter
3. 读取 DownloadResult：detail / manifest / duration

运行::

    # 默认：TIBIU 的 17001，只下第 1 话、每话只下 3 张
    python usage/download_comic.py

    # 换站点 / 换 id / 换范围
    python usage/download_comic.py --impl manhwa mozhouweixianzaoyu --chapters 2 --images 5

    # 批量下载（传多个 id，内部走 download_batch，一个 id 一个线程）
    python usage/download_comic.py 17001 17002

依赖：在 mccms 仓库根目录执行过 `pip install -e .`
"""

import argparse
import sys
from functools import partial

try:
    import mccms
except ImportError:
    raise SystemExit('未找到 mccms：请先在 mccms 仓库根目录执行  pip install -e .')


class SmallScaleDownloader(mccms.McDownloader):
    """
    限制下载范围的自定义下载器。

    do_filter 是 BaseDownloader 提供的重写点，会在并发派发前被调用：
      - 下载整本时，detail 是 McComicDetail  -> 返回要下载的章节子集
      - 下载章节时，detail 是 McChapterDetail -> 返回要下载的图片子集
    基类默认实现是"原样返回"（全下）。
    """

    def __init__(self, option, chapter_limit: int = 1, image_limit: int = 3):
        super().__init__(option)
        self.chapter_limit = chapter_limit
        self.image_limit = image_limit

    def do_filter(self, detail):
        if isinstance(detail, mccms.McComicDetail):
            return detail[:self.chapter_limit]      # Sequence 切片，返回章节列表
        if isinstance(detail, mccms.McChapterDetail):
            return detail[:self.image_limit]        # 每话只保留前 N 张
        return detail


def build_option(impl: str, base_dir: str) -> 'mccms.McOption':
    return mccms.McOption.construct({
        # true=普通日志, false=关闭, 'pretty'=彩色（按 topic 上色）
        'log': 'pretty',

        'dir_rule': {
            # Bd=base_dir, Cname=漫画名, Chindextitle=「第N话 章节名」
            'rule': 'Bd_Cname_Chindextitle',
            'base_dir': base_dir,
            'normalize_zh': None,          # 装了 zhconv 可填 'zh-cn' / 'zh-tw'
        },

        'download': {
            'cache': True,                 # 已存在的图片跳过，重复运行不会重下
            'image': {'decode': True, 'suffix': None},   # decode 是保留参数，本库站点图片明文直出
            'threading': {'image': 4, 'chapter': 2},     # 示例故意开小，别把站点打疼
        },

        'client': {
            'impl': impl,                  # tibiu | manhwa
            'retry_times': 3,
            # 需要访问"你自己账号有权访问的内容"时，在这里填 username/password 或 cookies，
            # 详见 usage/with_account.py。本库不包含任何绕过访问控制的实现。
            'username': None,
            'password': None,
        },

        'plugins': {
            'dependencies_strategy': 'failed-fast',
            # 内置插件按事件挂载；这里演示"下载完整本后打包成一个 zip"
            # 'after_comic': [
            #     {'plugin': 'zip', 'kwargs': {'zip_dir': './downloads/_zip', 'filename_rule': 'Cname'}},
            # ],
        },
    })


def download_one(comic_id: str, option, chapter_limit: int, image_limit: int) -> int:
    # 自定义下载器通过 functools.partial 传入"额外参数"：
    # api 会把下载器当作 downloader(option) 调用，partial 正好补齐 chapter_limit/image_limit
    downloader = partial(SmallScaleDownloader, chapter_limit=chapter_limit, image_limit=image_limit)

    print(f'\n>>> 开始下载漫画: {comic_id}（第 1~{chapter_limit} 话，每话前 {image_limit} 张）')
    result = mccms.download_comic(comic_id, option, downloader)

    # 返回值是 DownloadResult（NamedTuple：detail + downloader）
    comic = result.detail
    manifest = result.manifest

    print(f'漫画        : [{comic.comic_id}] {comic.name}（共 {comic.chapter_count} 话）')
    print(f'漫画根目录  : {comic.save_path}')
    print(f'实际下载图片: {len(manifest.image_filepath_list)} 张')
    for path in manifest.image_filepath_list[:3]:
        print(f'  - {path}')
    if result.duration is not None:
        print(f'耗时        : {result.duration:.2f}s')

    # manifest.export_filepath_dict 记录插件导出的文件（zip / pdf / 长图 / 封面）
    zip_files = manifest.get_export_filepath_list('zip')
    if zip_files:
        print(f'插件导出的 zip: {zip_files}')

    # downloader 上还挂着失败明细
    downloader_obj = result.downloader
    print(f'失败章节数  : {len(downloader_obj.download_failed_chapter)}，'
          f'失败图片数: {len(downloader_obj.download_failed_image)}')
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description='mccms 示例：下载整本漫画（小范围）')
    parser.add_argument('comic_ids', nargs='*', default=['17001'],
                        help='一个或多个漫画 id / slug，默认 17001')
    parser.add_argument('--impl', default=mccms.McMagicConstants.SITE_TIBIU,
                        choices=list(mccms.McMagicConstants.SITE_LIST), help='站点实现，默认 tibiu')
    parser.add_argument('--dir', default='./downloads/usage_comic', help='下载根目录')
    parser.add_argument('--chapters', type=int, default=1, help='最多下载几话，默认 1')
    parser.add_argument('--images', type=int, default=3, help='每话最多下载几张图，默认 3')
    args = parser.parse_args(argv)

    option = build_option(args.impl, args.dir)

    # 也可以把默认下载器换掉：之后 option.download_comic(...) 也会用它
    # SmallScaleDownloader.use()

    try:
        if len(args.comic_ids) > 1:
            # 传可迭代对象 -> 自动走 download_batch（一个 id 一个线程，失败项收在 result.failed）
            print(f'>>> 批量下载 {len(args.comic_ids)} 本：{args.comic_ids}')
            downloader = partial(SmallScaleDownloader,
                                 chapter_limit=args.chapters, image_limit=args.images)
            batch = mccms.download_comic(args.comic_ids, option, downloader)
            print(f'批量结果: {batch!r}，total={batch.total}')
            for comic_id, error in batch.failed.items():
                print(f'  失败 [{comic_id}]: {error!r}')
            return 0 if batch.all_succeeded else 1

        return download_one(args.comic_ids[0], option, args.chapters, args.images)

    except mccms.PartialDownloadFailedException as e:
        # 部分图片/章节失败：明细在 e.context['downloader'] 上
        print(f'[部分下载失败] {e.msg}', file=sys.stderr)
        return 1
    except mccms.LoginRequiredException as e:
        print(f'[需要登录] {e.msg}\n'
              f'  提示：请配置 option.client.username/password 或 cookies（见 usage/with_account.py）',
              file=sys.stderr)
        return 3
    except mccms.VipRequiredException as e:
        print(f'[需要会员权益] {e.msg}\n'
              f'  提示：当前账号无权访问；本库只访问公开内容与你账号有权访问的内容', file=sys.stderr)
        return 3
    except mccms.McException as e:
        print(f'[mccms 错误] {e}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
