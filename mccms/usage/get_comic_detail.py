#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
只用 API，不下载：搜索 -> 看详情 -> 列章节 -> 打印前几张图片 URL

本示例演示 mccms 的"客户端层"用法（McOption.build_client() 之后的一切都是纯 API 调用），
不会往磁盘写任何图片。

运行::

    # 默认：TIBIU 站点，漫画 id 17001
    python usage/get_comic_detail.py

    # 指定站点与漫画（漫蛙的 comic_id 可以是数字 id，也可以是 slug）
    python usage/get_comic_detail.py --impl manhwa mozhouweixianzaoyu

    # 换搜索词 / 控制打印条数
    python usage/get_comic_detail.py --keyword 魔咒 --limit 2

依赖：在 mccms 仓库根目录执行过 `pip install -e .`
"""

import argparse
import sys

try:
    import mccms
except ImportError:
    raise SystemExit('未找到 mccms：请先在 mccms 仓库根目录执行  pip install -e .')


def build_option(impl: str) -> 'mccms.McOption':
    """只读场景的 option：关掉日志（log=False），并指定站点实现。"""
    return mccms.McOption.construct({
        'log': False,                      # 不打日志，只保留我们自己 print 的结果
        'client': {'impl': impl},          # 'tibiu' 或 'manhwa'
    })


def demo_search(client, keyword: str, limit: int):
    print(f'=== 1. 搜索：{keyword} ===')

    # search() 返回 McSearchPage（content = [(comic_id, info), ...]）
    page = client.search(keyword, 1)
    print(f'第 {page.page_number} 页，共 {len(page)} 条（total ≈ {page.total}，每页 {page.page_size} 条）')

    # iter_id_title() 是最常用的迭代器；同族还有 iter_id_title_author() / iter_id_title_tag()
    for comic_id, name in list(page.iter_id_title())[:limit]:
        print(f'  [{comic_id}] {name}')

    # 分页生成器：这里只取前 2 页，避免无限翻页
    print('  用 search_gen 连续翻页（示例只取 2 页）：')
    for sub_page in client.search_gen(keyword, start_page=1, end_page=2):
        print(f'    第 {sub_page.page_number} 页: {len(sub_page)} 条')

    # to_comic_list() 可以把分页结果转成轻量 McComicDetail 列表（不含章节，不发额外请求）
    light_comics = page.to_comic_list()
    if light_comics:
        first = light_comics[0]
        print(f'  to_comic_list()[0] -> {first.comic_id} / {first.name} / 作者: {first.author or "-"}')
    return page


def demo_detail(client, comic_id: str):
    print(f'=== 2. 漫画详情：{comic_id} ===')

    # get_comic_detail(comic_id, *, fetch_chapters=True)
    # fetch_chapters=False 时只取漫画本身的信息，不请求章节列表（更快）
    comic = client.get_comic_detail(comic_id, fetch_chapters=False)
    print(f'  comic_id      : {comic.comic_id}')
    print(f'  name          : {comic.name}')
    print(f'  author        : {comic.author}')
    print(f'  site          : {mccms.McModuleConfig.site_name(comic.site)}')
    print(f'  serialize     : {comic.serialize or "-"}   is_finished={comic.is_finished}')
    print(f'  cover         : {comic.cover}')
    print(f'  url / slug    : {comic.url} / {comic.slug or "-"}')
    print(f'  description   : {comic.description[:80]}...')

    # 再取一次带章节列表的完整详情
    comic = client.get_comic_detail(comic_id, fetch_chapters=True)
    print(f'  章节数        : {comic.chapter_count}   总页数(站点提供时): {comic.page_count}')
    print(f'  最新一话      : {comic.latest_chapter_name}')
    return comic


def demo_chapters(comic, limit: int):
    print(f'=== 3. 章节列表（只列前 {limit} 话）===')

    # McComicDetail 是 Sequence：支持索引、切片、for 迭代
    for chapter in comic[:limit]:
        print(f'  index={chapter.index:<3} id={chapter.chapter_id:<10} '
              f'{chapter.indextitle} | {chapter.access_desc} | {chapter.count}p')
        # chapter.access_desc 由 access 字典里的 vip/cion/price 推导：
        #   免费 / VIP / 金币(x) / 付费(x)；chapter.is_free 是布尔判断

    # 也可以用 chapter_at(n) 按 1 起始的序号取章节（= comic[n - 1]）
    if len(comic) > 0:
        print(f'  comic.chapter_at(1) -> {comic.chapter_at(1).name}')

    return comic[0]


def demo_image_urls(client, comic, chapter, limit: int):
    print(f'=== 4. 图片 URL（第 {chapter.index} 话前 {limit} 张）===')

    # 注意：comic[i] 造出来的章节默认没有图片列表（len(chapter) == 0），
    # 需要显式取一次。下载流程里 McDownloader 会自动做这一步。
    if len(chapter) == 0:
        chapter = client.get_chapter_detail(
            chapter.chapter_id,
            comic_id=comic.comic_id,       # 传上所属漫画 id，目录命名信息更完整
            fetch_image_urls=True,         # 默认就是 True
        )

    print(f'  该话共 {len(chapter)} 张图')
    for image in chapter[:limit]:
        # McImageDetail 字段：img_url/download_url、img_file_name、img_file_suffix、index
        print(f'    [{image.index}] {image.filename}  <- {image.download_url}')

    # fetch_image_urls(chapter) 是等价写法（把 URL 写进 chapter 并返回列表）
    # client.fetch_image_urls(chapter)
    return chapter


def demo_dir_rule(comic, chapter, base_dir: str):
    print('=== 5. 本地预览 dir_rule 的落盘路径（不发请求）===')
    option = mccms.McOption.construct({
        'log': False,
        'dir_rule': {'rule': 'Bd_Cname_Chindextitle', 'base_dir': base_dir},
    })
    print(f'  漫画根目录 : {option.dir_rule.decide_comic_root_dir(comic)}')
    print(f'  本话目录   : {option.decide_image_save_dir(chapter, ensure_exists=False)}')
    if len(chapter) > 0:
        print(f'  第一张图片 : {option.decide_image_filepath(chapter[0])}')
    print('  （decide_* 系列都是 McOption 上可重写的决策方法，插件也靠它们改行为）')


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description='mccms API 示例：搜索 / 详情 / 章节 / 图片 URL')
    parser.add_argument('comic_id', nargs='?', default='17001',
                        help='漫画 id 或 slug（tibiu 用数字 id，manhwa 可用 slug），默认 17001')
    parser.add_argument('--impl', default=mccms.McMagicConstants.SITE_TIBIU,
                        choices=list(mccms.McMagicConstants.SITE_LIST), help='站点实现，默认 tibiu')
    parser.add_argument('--keyword', default='魔咒', help='搜索关键字')
    parser.add_argument('--limit', type=int, default=3, help='每处最多打印多少条，默认 3')
    args = parser.parse_args(argv)

    option = build_option(args.impl)
    client = option.build_client()

    try:
        page = demo_search(client, args.keyword, args.limit)
        comic = demo_detail(client, args.comic_id)
        if len(comic) == 0:
            print('该漫画没有任何章节，结束。')
            return 0

        chapter = demo_chapters(comic, args.limit)
        chapter = demo_image_urls(client, comic, chapter, args.limit)

        # 顺带展示搜索页里第一条的轻量实体
        if len(page) > 0:
            print('=== 附：搜索结果的轻量实体 ===')
            print(f'  {page[0][0]} -> {page[0][1].get("name")}')

        demo_dir_rule(comic, chapter, './downloads/usage_api')
        return 0

    except mccms.LoginRequiredException as e:
        # 该内容需要登录（本库不会绕过）
        print(f'[需要登录] {e.msg}\n'
              f'  提示：请在 option.client 里配置 username/password 或 cookies（见 usage/with_account.py）',
              file=sys.stderr)
        return 3
    except mccms.VipRequiredException as e:
        print(f'[需要会员权益] {e.msg}\n'
              f'  提示：当前账号无权访问该章节，请更换为你自己有权访问的内容', file=sys.stderr)
        return 3
    except mccms.AccessDeniedException as e:
        print(f'[无权限] {e.msg}', file=sys.stderr)
        return 3
    except mccms.McException as e:
        print(f'[mccms 错误] {e}', file=sys.stderr)
        return 1
    finally:
        # client.postman 是 McPostman（requests.Session 封装），用完关掉更干净
        client.postman.close()


if __name__ == '__main__':
    sys.exit(main())
