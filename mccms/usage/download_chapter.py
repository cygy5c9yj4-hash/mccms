#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
下载单章，并演示"只下前 N 张图"

本库内置了测试用下载器 JustDownloadSpecificCountImage（每话只下前 N 张）：
    class JustDownloadSpecificCountImage(McDownloader):
        def __init__(self, option, count: int = 3): ...
        def do_filter(self, detail):
            return detail[:self.count] if isinstance(detail, McChapterDetail) else detail

因为 api 会以 downloader(option) 的形式实例化下载器，想改 count 就用 functools.partial。

运行::

    # 默认：TIBIU 的 17001 漫画里的 291931 话，只下前 3 张
    python usage/download_chapter.py

    # 指定章节与张数
    python usage/download_chapter.py --comic-id 17001 --count 5 291931

    # 下载整章（图片可能较多，脚本会先提示张数）
    python usage/download_chapter.py --all 291931 --comic-id 17001

    # 也可以直接传章节 URL（TIBIU 的 /chapter/{comic_id}/{chapter_id} 能同时解析出两个 id）
    python usage/download_chapter.py https://cache.tibiu.net/chapter/17001/291931

依赖：在 mccms 仓库根目录执行过 `pip install -e .`
"""

import argparse
import sys
from functools import partial

try:
    import mccms
except ImportError:
    raise SystemExit('未找到 mccms：请先在 mccms 仓库根目录执行  pip install -e .')


def build_option(impl: str, base_dir: str) -> 'mccms.McOption':
    return mccms.McOption.construct({
        'log': 'pretty',
        'dir_rule': {
            # 单独下一章时，库会自动补上所属漫画上下文（McDownloader.attach_comic_context），
            # 所以 Cname 这类漫画字段依然可用
            'rule': 'Bd_Cname_Chindextitle',
            'base_dir': base_dir,
        },
        'download': {
            'cache': True,
            'threading': {'image': 4, 'chapter': 1},
        },
        'client': {'impl': impl},
        'plugins': {},
    })


def show_result(result, hint: str):
    chapter = result.detail
    manifest = result.manifest
    print(f'{hint}')
    print(f'  章节      : [{chapter.chapter_id}] {chapter.name}（第 {chapter.index} 话，{chapter.access_desc}）')
    print(f'  保存目录  : {chapter.save_path}')
    print(f'  落盘图片  : {len(manifest.image_filepath_list)} 张')
    for path in manifest.image_filepath_list[:5]:
        print(f'    - {path}')
    if result.duration is not None:
        print(f'  耗时      : {result.duration:.2f}s')
    if len(manifest.image_filepath_list) < len(chapter):
        print(f'  说明      : 本章共 {len(chapter)} 张，只下载了前 {len(manifest.image_filepath_list)} 张')


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description='mccms 示例：下载单章 / 只下前 N 张')
    parser.add_argument('chapter_id', nargs='?', default='291931', help='章节 id 或章节 URL，默认 291931')
    parser.add_argument('--comic-id', default=None, help='所属漫画 id（建议提供，目录命名更完整）')
    parser.add_argument('--impl', default=mccms.McMagicConstants.SITE_TIBIU,
                        choices=list(mccms.McMagicConstants.SITE_LIST), help='站点实现，默认 tibiu')
    parser.add_argument('--dir', default='./downloads/usage_chapter', help='下载根目录')
    parser.add_argument('--count', type=int, default=3, help='只下载前 N 张图，默认 3')
    parser.add_argument('--all', action='store_true', help='下载整章（忽略 --count）')
    args = parser.parse_args(argv)

    option = build_option(args.impl, args.dir)
    # 先只调 API 看一眼这一章有多少张图（不写文件）
    client = option.build_client()
    comic_id = args.comic_id
    try:
        # 传 URL 时，尽量把 comic_id 也解析出来：
        # 目录规则里一旦用到 Cxxx（漫画字段），就必须有漫画上下文，
        # 否则 DirRule 会报「无法解析的 dir_rule 片段: [Cname]」。
        if 'http' in str(args.chapter_id) or '/' in str(args.chapter_id):
            if hasattr(client, 'parse_chapter_url'):        # TIBIU 专属：一次解析出两个 id
                parsed_comic_id, parsed_chapter_id = client.parse_chapter_url(str(args.chapter_id))
            else:                                            # 漫蛙：只能分别解析
                parsed_comic_id, parsed_chapter_id = (
                    client.get_comic_id_from_url(str(args.chapter_id)),
                    client.get_chapter_id_from_url(str(args.chapter_id)),
                )
            print(f'从 URL 解析: comic_id={parsed_comic_id}, chapter_id={parsed_chapter_id}')
            comic_id = comic_id or parsed_comic_id

        preview = client.get_chapter_detail(args.chapter_id, comic_id=comic_id,
                                            fetch_image_urls=True)
        print(f'章节预览: [{preview.chapter_id}] {preview.name} | {preview.access_desc} | '
              f'{len(preview)} 张图 | comic_id={preview.comic_id or "-"}')
    except mccms.AccessDeniedException as e:
        print(f'[无权限] {e.msg}', file=sys.stderr)
        return 3
    except mccms.McException as e:
        print(f'[mccms 错误] {e}', file=sys.stderr)
        return 1
    finally:
        client.postman.close()

    try:
        if args.all:
            print(f'>>> 下载整章 {preview.chapter_id}（共 {len(preview)} 张）')
            result = mccms.download_chapter(preview.chapter_id, option, comic_id=comic_id)
            show_result(result, '整章下载完成')
            return 0

        # 方式 1：内置下载器 + partial 传 count
        limited = partial(mccms.JustDownloadSpecificCountImage, count=args.count)
        print(f'>>> 只下载前 {args.count} 张（JustDownloadSpecificCountImage）')
        result = mccms.download_chapter(preview.chapter_id, option, limited, comic_id=comic_id)
        show_result(result, '限量下载完成')

        # 方式 2：也可以用自定义下载器重写 do_filter（等价写法，见 usage/download_comic.py）
        # class MyLimited(mccms.McDownloader):
        #     def __init__(self, option, count=3):
        #         super().__init__(option); self.count = count
        #     def do_filter(self, detail):
        #         return detail[:self.count] if isinstance(detail, mccms.McChapterDetail) else detail
        #
        # 方式 3：只预览不下载（只建目录，不落图片）
        # mccms.download_chapter(preview.chapter_id, option, mccms.DoNotDownloadImage,
        #                        comic_id=args.comic_id)
        return 0

    except mccms.PartialDownloadFailedException as e:
        print(f'[部分下载失败] {e.msg}', file=sys.stderr)
        return 1
    except mccms.LoginRequiredException as e:
        print(f'[需要登录] {e.msg}\n'
              f'  提示：配置 option.client.username/password 或 cookies 后重试', file=sys.stderr)
        return 3
    except mccms.VipRequiredException as e:
        print(f'[需要会员权益] {e.msg}\n'
              f'  提示：本库只访问公开内容与当前账号有权访问的内容，不会绕过访问控制', file=sys.stderr)
        return 3
    except mccms.McException as e:
        print(f'[mccms 错误] {e}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
