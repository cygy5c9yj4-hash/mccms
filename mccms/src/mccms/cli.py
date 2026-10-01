"""
命令行入口。

用法示例::

    # 下载整本漫画（默认 tibiu 站点）
    mccms --impl tibiu 17001

    # 下载 manhwa 的漫画
    mccms --impl manhwa mozhouweixianzaoyu

    # 下载单个章节（--chapter 是开关，漫画 id 用 --comic-id 传）
    mccms --impl tibiu --chapter 291931 --comic-id 17001

    # 搜索
    mccms --impl manhwa --search 魔咒

    # 查看漫画信息（不下载）
    mccms --impl tibiu --info 17001

    # 使用自己的配置文件（含账号 / cookie / 插件）
    mccms -o my_option.yml 17001

    # 用自己账号登录后下载（访问该账号有权访问的内容）
    mccms --impl manhwa --username me --password *** 27032
"""

import argparse
import os
import sys
from typing import List, Optional

from .mc_config import McMagicConstants, McModuleConfig
from .mc_exception import AccessDeniedException, McException

__all__ = ['main', 'get_env']


def get_env(name: str, default=None):
    return os.environ.get(name, default)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog='mccms',
        description='Mccms 站点（TIBIU / 漫蛙）Python API 与下载器',
    )

    parser.add_argument('ids', nargs='*', help='漫画 id / 章节 id / URL')

    parser.add_argument('-o', '--option', dest='option_file', default=None,
                        help='option yml 文件路径')

    parser.add_argument('--impl', dest='impl', default=None,
                        choices=list(McMagicConstants.SITE_LIST),
                        help='站点实现：tibiu（cache.tibiu.net）/ manhwa（www.manhwa.wang）')

    parser.add_argument('-d', '--dir', dest='base_dir', default=None,
                        help='下载根目录')

    parser.add_argument('--rule', dest='dir_rule', default=None,
                        help='目录规则，如 Bd_Cname_Chindextitle')

    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--comic', dest='as_comic', action='store_true', default=True,
                      help='把参数当作漫画 id 下载（默认）')
    mode.add_argument('--chapter', dest='as_comic', action='store_false',
                      help='把参数当作章节 id 下载')
    mode.add_argument('--search', dest='search', default=None,
                      help='搜索关键字')
    mode.add_argument('--info', dest='info', default=None,
                      help='只打印漫画信息，不下载')

    parser.add_argument('--comic-id', dest='comic_id', default=None,
                        help='下载章节时指定所属漫画 id')

    parser.add_argument('--page', dest='page', type=int, default=1,
                        help='搜索/列表页码')

    # 账号
    parser.add_argument('--username', dest='username', default=None, help='账号')
    parser.add_argument('--password', dest='password', default=None, help='密码')
    parser.add_argument('--cookie', dest='cookie', action='append', default=None,
                        help='手工指定 cookie，格式 name=value，可重复')

    # 并发
    parser.add_argument('--thread-image', dest='thread_image', type=int, default=None,
                        help='图片并发数')
    parser.add_argument('--thread-chapter', dest='thread_chapter', type=int, default=None,
                        help='章节并发数')

    parser.add_argument('-v', '--verbose', dest='verbose', action='store_true',
                        help='打印详细日志')

    return parser


def create_option(args):
    from .mc_option import McOption

    if args.option_file:
        option = McOption.from_file(args.option_file)
    else:
        option = McOption.default()

    # 命令行覆盖
    if args.impl:
        option.client.impl = args.impl

    if args.base_dir:
        from .mc_option import DirRule
        option.dir_rule = DirRule(
            rule=args.dir_rule or option.dir_rule.rule_dsl,
            base_dir=args.base_dir,
            normalize_zh=option.dir_rule.normalize_zh,
        )
    elif args.dir_rule:
        from .mc_option import DirRule
        option.dir_rule = DirRule(
            rule=args.dir_rule,
            base_dir=option.dir_rule.base_dir,
            normalize_zh=option.dir_rule.normalize_zh,
        )

    if args.username:
        option.client.username = args.username
    if args.password:
        option.client.password = args.password

    if args.thread_image:
        option.download.threading.image = args.thread_image
    if args.thread_chapter:
        option.download.threading.chapter = args.thread_chapter

    if args.cookie:
        cookies = {}
        for item in args.cookie:
            if '=' in item:
                name, value = item.split('=', 1)
                cookies[name.strip()] = value.strip()
        client = option.build_client()
        client.postman.set_cookies(cookies)
        option.update_cookies(cookies)

    return option


def cmd_search(option, keyword: str, page: int) -> int:
    client = option.build_client()
    result = client.search(keyword, page)

    print(f'搜索「{keyword}」第 {page} 页，共 {len(result)} 条：')
    for comic_id, info in result.content:
        print(f'  [{comic_id}] {info.get("name", "")}'
              f'  作者: {info.get("author", "") or "-"}'
              f'  更新: {info.get("latest_chapter", "") or "-"}')

    if len(result) == 0:
        print('（无结果）')
    return 0


def cmd_info(option, comic_id: str) -> int:
    client = option.build_client()
    comic = client.get_comic_detail(comic_id)

    print(f'漫画: [{comic.comic_id}] {comic.name}')
    print(f'  作者: {comic.author}')
    print(f'  状态: {comic.serialize or "-"}')
    print(f'  话数: {comic.chapter_count}')
    print(f'  封面: {comic.cover}')
    print(f'  简介: {comic.description[:200]}')
    print(f'  站点: {McModuleConfig.site_name(comic.site)}')

    preview = comic[:10]
    print(f'  章节（前 {len(preview)} 话）:')
    for chapter in preview:
        print(f'    [{chapter.chapter_id}] {chapter.name}  {chapter.access_desc}  {chapter.count}p')

    return 0


def cmd_download(option, args) -> int:
    from . import api

    if args.as_comic:
        targets = args.ids
        if not targets:
            print('请提供漫画 id 或 URL', file=sys.stderr)
            return 2

        for target in targets:
            print(f'开始下载漫画: {target}')
            result = api.download_comic(target, option)
            manifest = result.manifest
            duration = f'，耗时 {result.duration:.2f}s' if result.duration else ''
            print(f'  完成: {result.detail.name}，'
                  f'{len(manifest.image_filepath_list)} 张图片{duration}')
        return 0

    targets = args.ids
    if not targets:
        print('请提供章节 id 或 URL', file=sys.stderr)
        return 2

    for target in targets:
        print(f'开始下载章节: {target}')
        result = api.download_chapter(target, option, comic_id=args.comic_id)
        manifest = result.manifest
        print(f'  完成: {result.detail.name}，{len(manifest.image_filepath_list)} 张图片')
    return 0


def main(argv: Optional[List[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.verbose:
        from .mc_config import enable_pretty_log
        enable_pretty_log()

    if not args.impl and not args.option_file:
        # 未指定 impl 时默认 tibiu
        args.impl = McMagicConstants.SITE_TIBIU

    try:
        option = create_option(args)

        if args.search:
            return cmd_search(option, args.search, args.page)
        if args.info:
            return cmd_info(option, args.info)
        if args.ids:
            return cmd_download(option, args)

        parser.print_help()
        return 0

    except AccessDeniedException as e:
        print(f'\n[无权限] {e.msg}', file=sys.stderr)
        print('提示：该内容需要登录或会员权益。请使用 --username/--password 或 --cookie '
              '提供你自己的账号，本工具不会绕过任何访问控制。', file=sys.stderr)
        return 3
    except McException as e:
        print(f'\n[错误] {e}', file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print('\n已中断', file=sys.stderr)
        return 130


if __name__ == '__main__':
    sys.exit(main())
