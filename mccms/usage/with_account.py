#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
用自己的账号登录后下载（只访问该账号有权访问的内容）

⚠ 访问权限边界（请务必阅读）
    - 本库只访问「公开内容」和「当前会话本身有权访问的内容」；
    - 需要登录 / 会员权益的章节，服务端会拒绝，本库会抛
      LoginRequiredException / VipRequiredException（二者都继承 AccessDeniedException）；
    - 本库不包含任何绕过访问控制的实现，也不接受任何形式的越权用法；
    - 本示例只演示"用你自己的账号，访问你自己有权访问的内容"。

三种提供凭据的方式（都在本文件里演示）：
    1. option.client.username / option.client.password   -> 新建 client 时自动登录
    2. option.client.cookies（或 client.postman.meta_data.cookies） -> 直接带会话 cookie
    3. option.login()                                    -> 主动登录一次，并把会话 cookie 写回 option
       （cookie 随后会被后续新建的 client 复用）

运行::

    export MCCMS_USERNAME='你的账号'
    export MCCMS_PASSWORD='你的密码'
    python usage/with_account.py --impl manhwa --chapter-id 936939 --comic-id 27032

    # 或者用浏览器里自己的会话 cookie（推荐，避免在环境里留密码）
    export MCCMS_COOKIE='PHPSESSID=xxxxxx; other=yyy'
    python usage/with_account.py --chapter-id 291931 --comic-id 17001

依赖：在 mccms 仓库根目录执行过 `pip install -e .`
"""

import argparse
import os
import sys
from functools import partial

try:
    import mccms
except ImportError:
    raise SystemExit('未找到 mccms：请先在 mccms 仓库根目录执行  pip install -e .')


def parse_cookie_text(text: str) -> dict:
    """把 'a=1; b=2' 解析成 {'a': '1', 'b': '2'}"""
    cookies = {}
    for item in (text or '').split(';'):
        if '=' in item:
            name, value = item.split('=', 1)
            cookies[name.strip()] = value.strip()
    return cookies


def build_option(impl: str, base_dir: str, username: str, password: str, cookies: dict) -> 'mccms.McOption':
    return mccms.McOption.construct({
        'log': 'pretty',
        'dir_rule': {'rule': 'Bd_Cname_Chindextitle', 'base_dir': base_dir},
        'download': {'cache': True, 'threading': {'image': 4, 'chapter': 1}},
        'client': {
            'impl': impl,
            # 方式 1：填了账号密码，new_client() 时会自动调用 login()（等价于 ensure_login）
            'username': username or None,
            'password': password or None,
            # 方式 2：直接带上自己的会话 cookie（这里会合并进 postman.meta_data.cookies）
            'cookies': cookies or {},
        },
    })


def show_account(client):
    """打印当前会话的账号信息（Mccms 的 /index.php/api/user/info 接口）。"""
    try:
        info = client.user_info()
    except mccms.McException as e:
        print(f'  读取账号信息失败（可能是未登录）：{e}')
        return

    if mccms.MccmsText.safe_int(info.get('log', 0)) != 1:
        print('  当前会话未登录（或凭据无效）')
        return

    print(f"  已登录: 昵称={info.get('nichen') or '-'} "
          f"uid={info.get('id')} vip={info.get('vip')} 金币={info.get('cion')}")


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description='mccms 示例：用自己的账号访问有权访问的内容')
    parser.add_argument('--impl', default=mccms.McMagicConstants.SITE_MANHWA,
                        choices=list(mccms.McMagicConstants.SITE_LIST), help='站点实现，默认 manhwa')
    parser.add_argument('--chapter-id', default='936939', help='要下载的章节 id')
    parser.add_argument('--comic-id', default=None, help='所属漫画 id（可选，命名更完整）')
    parser.add_argument('--dir', default='./downloads/usage_account', help='下载根目录')
    parser.add_argument('--count', type=int, default=2, help='每话只下前 N 张，默认 2（示例保持小范围）')
    args = parser.parse_args(argv)

    username = os.environ.get('MCCMS_USERNAME', '')
    password = os.environ.get('MCCMS_PASSWORD', '')
    cookies = parse_cookie_text(os.environ.get('MCCMS_COOKIE', ''))

    if not (username and password) and not cookies:
        print('未提供凭据：请设置环境变量 MCCMS_USERNAME / MCCMS_PASSWORD，'
              '或 MCCMS_COOKIE="name=value; ..."\n'
              '（本示例不会使用任何"公共账号"，也不会绕过访问控制）', file=sys.stderr)
        print('\n演示：仅访问公开内容（不带任何凭据）——')
        username = password = ''
        cookies = {}
        # 继续往下走，用匿名会话访问公开章节；受限章节会被服务端拒绝

    option = build_option(args.impl, args.dir, username, password, cookies)
    client = option.build_client()

    print('=== 1. 检查会话状态 ===')
    show_account(client)

    print('=== 2. 主动登录（方式 3）并把 cookie 写回 option ===')
    if username and password:
        try:
            info = option.login()          # = client.login() + option.update_cookies(...)
            print(f"  登录成功: {info.get('nichen') or username}")
            print(f'  会话 cookie 已写回 option，后续新建的 client 会复用: '
                  f'{sorted(client.postman.cookies.keys())}')
        except mccms.McException as e:
            print(f'  登录失败: {e}', file=sys.stderr)
            print('  提示：账号密码错误 / 站点要求图形验证码时都会失败，'
                  '此时可以改用 MCCMS_COOKIE 直接带会话 cookie', file=sys.stderr)
            client.postman.close()
            return 1
    else:
        print('  （未配置账号密码，跳过主动登录；将使用匿名会话或已提供的 cookie）')

    print('=== 3. 查看目标章节的权限标记 ===')
    try:
        chapter = client.get_chapter_detail(args.chapter_id, comic_id=args.comic_id,
                                            fetch_image_urls=False)
        print(f'  [{chapter.chapter_id}] {chapter.name} | {chapter.access_desc} | '
              f'is_free={chapter.is_free}')
        print(f'  访问描述: {client.describe_access(chapter)}')
    except mccms.McException as e:
        print(f'  [错误] {e}', file=sys.stderr)
        client.postman.close()
        return 1

    print(f'=== 4. 下载（每话前 {args.count} 张，访问我自己有权访问的内容）===')
    limited = partial(mccms.JustDownloadSpecificCountImage, count=args.count)
    try:
        result = mccms.download_chapter(args.chapter_id, option, limited, comic_id=args.comic_id)
        print(f"  完成: [{result.detail.chapter_id}] {result.detail.name}，"
              f"{len(result.manifest.image_filepath_list)} 张 -> {result.detail.save_path}")

        # 复用登录态的小技巧：把会话 cookie 存下来，下次可直接塞进 option.client.cookies
        # （option.build_client() 带缓存，所以下载器用的就是这个 client，会话是同一个）
        session_cookies = client.postman.cookies
        print(f'  当前会话 cookie: {sorted(session_cookies.keys())}'
              f'（可写入 yml 的 client.cookies，或下次用 MCCMS_COOKIE 传入）')
        return 0

    except mccms.LoginRequiredException as e:
        # 服务端明确要求登录：本库如实抛出，不做任何绕过尝试
        print(f'\n[需要登录] {e.msg}', file=sys.stderr)
        print('  友好提示：该章节需要登录后才能访问。请用你自己的账号：\n'
              '    export MCCMS_USERNAME=... MCCMS_PASSWORD=...\n'
              '  或 export MCCMS_COOKIE="PHPSESSID=..." 后重试。\n'
              '  本库不包含任何绕过访问控制的实现。', file=sys.stderr)
        return 3
    except mccms.VipRequiredException as e:
        print(f'\n[需要会员权益] {e.msg}', file=sys.stderr)
        print('  友好提示：当前账号没有该章节所需的会员/金币权益。\n'
              '  请更换为你自己有权访问的章节，或使用具备相应权益的账号。\n'
              '  本库不会、也无法替你获取这些权益。', file=sys.stderr)
        return 3
    except mccms.AccessDeniedException as e:
        print(f'\n[无权限] {e.msg}\n'
              f'  服务端拒绝提供该资源（可能已下架 / 审核中 / 不属于该漫画）。', file=sys.stderr)
        return 3
    except mccms.PartialDownloadFailedException as e:
        print(f'\n[部分下载失败] {e.msg}', file=sys.stderr)
        return 1
    except mccms.McException as e:
        print(f'\n[mccms 错误] {e}', file=sys.stderr)
        return 1
    finally:
        client.postman.close()


if __name__ == '__main__':
    sys.exit(main())
