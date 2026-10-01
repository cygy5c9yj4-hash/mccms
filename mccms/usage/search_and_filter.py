#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
搜索 + 分类 / 榜单浏览

两个站点的浏览能力并不完全一致（源码事实，见 README 的"能力矩阵"）：

TIBIU（纯 JSON API）
    client.search(keyword, page)                 搜索
    client.categories_filter(page, order, tags, tids)   分类（order 见 McMagicConstants.ORDER_*）
    client.update_list(page)                     最近更新
    client.filter_options(mode)                  筛选项字典（分类/标签 id -> 名称）
    client.ranking_nav()                         榜单导航
    client.ranking(rank_type, page, max_items)   榜单列表

漫蛙（JSON + HTML 混合）
    client.search(keyword, page)                        搜索（解析 HTML 卡片）
    client.categories_filter(page, order, finish, pay, tags, quality, copyright)
    client.update_list(page)                            最近更新（= categories_filter(order='addtime')）
    client.hot_list()                                   热门（首页推荐位接口）
    （没有榜单导航 / 筛选项字典接口）

运行::

    python usage/search_and_filter.py                     # 默认 TIBIU
    python usage/search_and_filter.py --impl manhwa
    python usage/search_and_filter.py --keyword 恋爱 --limit 5

依赖：在 mccms 仓库根目录执行过 `pip install -e .`
"""

import argparse
import sys

try:
    import mccms
except ImportError:
    raise SystemExit('未找到 mccms：请先在 mccms 仓库根目录执行  pip install -e .')


def print_page(title: str, page, limit: int):
    """McSearchPage 的通用打印：content = [(comic_id, info), ...]"""
    print(f'--- {title}：{len(page)} 条（total ≈ {page.total}）---')
    for comic_id, info in page.content[:limit]:
        print(f'  [{comic_id}] {info.get("name", "")}'
              f' | 作者: {info.get("author") or "-"}'
              f' | 人气: {info.get("views") or 0}'
              f' | 更新: {info.get("update_date") or "-"}')
        if info.get('latest_chapter'):
            print(f'        最新: {info["latest_chapter"]}')
    if len(page) == 0:
        print('  （无结果）')


def browse_tibiu(client, keyword: str, limit: int):
    # 1) 搜索
    print_page('搜索', client.search(keyword, 1), limit)

    # 2) 分类浏览：
    #    order 取值见 McMagicConstants.ORDER_LIST = ('addtime', 'hits', 'score', 'nums')
    #    tags / tids 可以是单个 id，也可以是 id 列表（会被拼成逗号分隔）
    order = mccms.McMagicConstants.ORDER_HITS
    print_page(f'分类（order={order}）', client.categories_filter(page=1, order=order), limit)

    # 3) 筛选项字典：拿到 tags / tids 的真实 id
    options = client.filter_options(mode='update')
    print(f'--- 筛选项（key: {sorted(options.keys())[:6]} ...）---')
    for key in list(options.keys())[:2]:
        value = options[key]
        preview = value[:3] if isinstance(value, list) else value
        print(f'  {key}: {preview}')

    # 4) 最近更新
    print_page('最近更新', client.update_list(page=1), limit)

    # 5) 榜单
    nav = client.ranking_nav()
    print(f'--- 榜单导航（{len(nav)} 项）---')
    for item in nav[:limit]:
        print(f'  {item}')      # 导航项字段由站点接口决定，这里原样打印

    if nav:
        first = nav[0]
        # 榜单类型通常放在 type 字段（如 top / ticket / fav / day / week / month）
        rank_type = (first.get('type') or first.get('key') or 'top') if isinstance(first, dict) else 'top'
        print_page(f'榜单（type={rank_type}）', client.ranking(rank_type=rank_type, page=1), limit)


def browse_manhwa(client, keyword: str, limit: int):
    # 1) 搜索（HTML 解析，info 里额外带 latest_chapter / latest_chapter_url）
    print_page('搜索', client.search(keyword, 1), limit)

    # 2) 分类浏览：order='hits'（人气）/ 'addtime'（更新）
    #    finish: 1 已完结 / 2 连载中；pay: 1 免费 / 2 付费
    print_page('分类（人气）', client.categories_filter(page=1, order=mccms.McMagicConstants.ORDER_HITS), limit)
    print_page('分类（已完结）', client.categories_filter(page=1, order=mccms.McMagicConstants.ORDER_HITS,
                                                         finish=1), limit)

    # 3) 最近更新 = categories_filter(order='addtime')
    print_page('最近更新', client.update_list(page=1), limit)

    # 4) 热门（漫蛙独有的 JSON 接口）
    if hasattr(client, 'hot_list'):
        print_page('热门漫画', client.hot_list(), limit)
    else:
        print('该客户端没有 hot_list()')


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description='mccms 示例：搜索 / 分类 / 榜单浏览')
    parser.add_argument('--impl', default=mccms.McMagicConstants.SITE_TIBIU,
                        choices=list(mccms.McMagicConstants.SITE_LIST), help='站点实现，默认 tibiu')
    parser.add_argument('--keyword', default='魔咒', help='搜索关键字')
    parser.add_argument('--limit', type=int, default=3, help='每个列表最多打印几条，默认 3')
    args = parser.parse_args(argv)

    option = mccms.McOption.construct({'log': False, 'client': {'impl': args.impl}})
    client = option.build_client()
    print(f'站点: {mccms.McModuleConfig.site_name(client.site)}  域名: {client.domain}\n')

    try:
        if args.impl == mccms.McMagicConstants.SITE_TIBIU:
            browse_tibiu(client, args.keyword, args.limit)
        else:
            browse_manhwa(client, args.keyword, args.limit)
        return 0
    except mccms.McException as e:
        print(f'[mccms 错误] {e}', file=sys.stderr)
        return 1
    finally:
        client.postman.close()


if __name__ == '__main__':
    sys.exit(main())
