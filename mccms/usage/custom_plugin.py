#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
自定义插件：写一个插件并注册使用

插件机制（与 jmcomic 一致）：
    1. 继承 mccms.McOptionPlugin，设置类属性 plugin_key（唯一注册键）
    2. 实现 invoke(self, **kwargs)
    3. McModuleConfig.register_plugin(你的类)  或者放进 option 的 plugins.<事件名> 里

事件（真正会被派发的，见 McDownloader / McOption 源码）：
    after_init（构造 McOption 时，safe=True）
    before_comic / after_comic     额外参数: comic, downloader
    before_chapter / after_chapter 额外参数: chapter, downloader
    before_image / after_image     额外参数: image, downloader

本脚本分两部分：
    A. 离线自检：造一个假的章节实体，直接触发 after_chapter 事件，验证插件真的被调用
       （不需要网络，直接 `python usage/custom_plugin.py` 就能跑）
    B. 真实下载：`python usage/custom_plugin.py --comic 17001` 时，下载 1 话并记录到文件

依赖：在 mccms 仓库根目录执行过 `pip install -e .`
"""

import argparse
import os
import sys
from datetime import datetime
from functools import partial

try:
    import mccms
except ImportError:
    raise SystemExit('未找到 mccms：请先在 mccms 仓库根目录执行  pip install -e .')


# ======================================================================================
# A. 插件定义
# ======================================================================================

class UsageChapterRecorder(mccms.McOptionPlugin):
    """
    把"已经下载完成的章节"追加记录到一个文本文件里。

    适合放在 after_chapter 事件下：此时该话的图片都已经落盘。
    """

    plugin_key = 'usage_chapter_recorder'

    # 需要第三方库时这样声明：('import_name', 'pip-package-name') 或 'name'（两者相同）
    # 缺失时的处理策略由 option 的 plugins.dependencies_strategy 决定
    plugin_dependencies = ()

    def invoke(self,
               file_path: str = './downloads/usage_plugin/chapters.txt',
               chapter=None,
               downloader=None,
               **kwargs):
        # require_param 会抛 PluginValidationException（带插件名，便于定位）
        self.require_param(chapter is not None,
                           'usage_chapter_recorder 需要在 before_chapter / after_chapter 事件中使用')

        save_path = getattr(chapter, 'save_path', '') or '-'
        line = (f'{datetime.now():%Y-%m-%d %H:%M:%S}\t{chapter.comic_id}\t{chapter.chapter_id}\t'
                f'{chapter.index}\t{chapter.name}\t{len(chapter)}p\t{save_path}\n')

        directory = os.path.dirname(os.path.abspath(file_path))
        if directory:
            os.makedirs(directory, exist_ok=True)
        with open(file_path, 'a', encoding='utf-8') as f:
            f.write(line)

        # self.log(...) 受 pinfo 里的 log: false 控制
        self.log(f'已记录章节 [{chapter.chapter_id}] {chapter.name} -> {file_path}')

        # 插件里也能拿到 downloader，例如统计失败数、读 download_success_dict
        failed = len(downloader.download_failed_image) if downloader is not None else '-'
        self.log(f'当前失败图片数: {failed}', 'stat')


# ======================================================================================
# B. option 构造（插件配置既可以写 dict，也等价于 yml）
# ======================================================================================

def build_option(impl: str, base_dir: str, record_path: str) -> 'mccms.McOption':
    return mccms.McOption.construct({
        'log': True,
        'dir_rule': {'rule': 'Bd_Cname_Chindextitle', 'base_dir': base_dir},
        'download': {'cache': True, 'threading': {'image': 4, 'chapter': 1}},
        'client': {'impl': impl},
        'plugins': {
            'dependencies_strategy': 'failed-fast',
            # 与下面这段 yml 完全等价：
            #   plugins:
            #     after_chapter:
            #       - plugin: usage_chapter_recorder
            #         kwargs: { file_path: ./downloads/usage_plugin/chapters.txt }
            #         log: true      # false 时不打插件日志
            #         safe: true     # false 时插件异常直接向上抛
            'after_chapter': [
                {
                    'plugin': UsageChapterRecorder.plugin_key,
                    'kwargs': {'file_path': record_path},
                    'log': True,
                    'safe': True,
                },
            ],
        },
    })


class OneChapterFewImages(mccms.McDownloader):
    """真实下载时把范围压到最小：只下 1 话，每话 3 张。"""

    def __init__(self, option, chapter_limit: int = 1, image_limit: int = 3):
        super().__init__(option)
        self.chapter_limit = chapter_limit
        self.image_limit = image_limit

    def do_filter(self, detail):
        if isinstance(detail, mccms.McComicDetail):
            return detail[:self.chapter_limit]
        if isinstance(detail, mccms.McChapterDetail):
            return detail[:self.image_limit]
        return detail


# ======================================================================================
# C. 两种运行方式
# ======================================================================================

def offline_self_test(record_path: str) -> int:
    """不联网：造实体 + 手动触发事件，验证插件被调用、文件被写入。"""
    print('=== A. 离线自检（不联网）===')

    # 1) 注册插件（注册后 plugin_key -> 类，进入 McModuleConfig.REGISTRY_PLUGIN）
    mccms.McModuleConfig.register_plugin(UsageChapterRecorder)
    print(f'已注册插件: {UsageChapterRecorder.plugin_key} -> '
          f'{mccms.McModuleConfig.REGISTRY_PLUGIN[UsageChapterRecorder.plugin_key].__name__}')

    # 2) 构造 option：after_chapter 下挂上插件
    option = build_option(mccms.McMagicConstants.SITE_TIBIU, './downloads/usage_plugin', record_path)

    # 3) 造两个假章节（真实下载时由 client 构造）
    comic = mccms.McComicDetail(
        comic_id='17001', name='示例漫画',
        episode_list=[('291931', 1, '第1话'), ('291932', 2, '第2话')],
    )
    for chapter in comic[:2]:
        chapter.set_image_url_list([f'https://cdn.example.com/{i}.jpg' for i in range(1, 4)])
        chapter.save_path = f'./downloads/usage_plugin/示例漫画/{chapter.chapter_id}'

        # 下载器内部就是这么派发的：option.call_all_plugin('after_chapter', chapter=chapter, downloader=self)
        option.call_all_plugin('after_chapter', chapter=chapter, downloader=None)

    print(f'\n记录文件内容（{record_path}）:')
    if os.path.exists(record_path):
        with open(record_path, encoding='utf-8') as f:
            print('  ' + f.read().replace('\n', '\n  ').rstrip())
    else:
        print('  （文件不存在，插件可能没有被触发）')
        return 1

    # 4) 插件参数校验失败的样子（PluginValidationException）
    print('\n参数校验演示:')
    try:
        UsageChapterRecorder(option).invoke()       # 故意不传 chapter
    except mccms.PluginValidationException as e:
        print(f'  {e}')
    print('  说明：require_param 会在参数不合法时抛 PluginValidationException；'
          '而插件在事件里执行时，call_all_plugin 默认 safe=True，只记日志、不中断下载。')
    return 0


def real_download(comic_id: str, impl: str, base_dir: str, record_path: str) -> int:
    print(f'\n=== B. 真实下载（{impl}：{comic_id}，只下 1 话 / 每话 3 张）===')
    option = build_option(impl, base_dir, record_path)
    downloader = partial(OneChapterFewImages, chapter_limit=1, image_limit=3)

    try:
        result = mccms.download_comic(comic_id, option, downloader)
    except mccms.PartialDownloadFailedException as e:
        print(f'[部分下载失败] {e.msg}', file=sys.stderr)
        return 1
    except mccms.AccessDeniedException as e:
        print(f'[无权限] {e.msg}\n'
              f'  提示：本库只访问公开内容与你账号有权访问的内容（见 usage/with_account.py）',
              file=sys.stderr)
        return 3
    except mccms.McException as e:
        print(f'[mccms 错误] {e}', file=sys.stderr)
        return 1

    print(f'下载完成: [{result.detail.comic_id}] {result.detail.name}，'
          f'{len(result.manifest.image_filepath_list)} 张图')
    if os.path.exists(record_path):
        with open(record_path, encoding='utf-8') as f:
            print('记录文件最后一行: ' + f.read().strip().split('\n')[-1])
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description='mccms 示例：自定义插件')
    parser.add_argument('--comic', default=None, help='给了 id 就真的下载一本（只下 1 话）')
    parser.add_argument('--impl', default=mccms.McMagicConstants.SITE_TIBIU,
                        choices=list(mccms.McMagicConstants.SITE_LIST), help='站点实现，默认 tibiu')
    parser.add_argument('--dir', default='./downloads/usage_plugin', help='下载根目录')
    parser.add_argument('--record', default='./downloads/usage_plugin/chapters.txt', help='插件记录文件')
    args = parser.parse_args(argv)

    if os.path.exists(args.record):
        os.remove(args.record)      # 每次运行都从干净状态开始，方便观察

    code = offline_self_test(args.record)
    if code != 0 or args.comic is None:
        if args.comic is None:
            print('\n（加 --comic <id> 可以看插件在真实下载流程里的效果）')
        return code

    return real_download(args.comic, args.impl, args.dir, args.record)


if __name__ == '__main__':
    sys.exit(main())
