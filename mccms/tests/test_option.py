"""DirRule DSL 与 McOption 测试。"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))

from mccms.mc_config import McModuleConfig  # noqa: E402
from mccms.mc_entity import McChapterDetail, McComicDetail  # noqa: E402
from mccms.mc_exception import McException, PluginValidationException  # noqa: E402
from mccms.mc_option import DirRule, McOption  # noqa: E402


def make_comic(comic_id='17001', name='社内恋爱/内部爱情', author='이로비'):
    return McComicDetail(
        comic_id=comic_id,
        name=name,
        author=author,
        authors=[author],
        tags=['BL', '校园'],
        serialize='完结',
        episode_list=[('291931', 1, '第01话'), ('292046', 2, '第54话')],
        chapter_access={
            '291931': {'vip': 0, 'cion': 0, 'pnum': 64},
            '292046': {'vip': 1, 'cion': 0, 'pnum': 69},
        },
    )


class TestDirRuleSplit(unittest.TestCase):

    def test_auto_prefix_bd(self):
        rule = DirRule('Cname_Chindex', base_dir='/tmp')
        self.assertEqual([r for r, _ in rule.parser_list][0], 'Bd')

    def test_slash_separator_wins(self):
        rule = DirRule('Bd/Cname/Chindex', base_dir='/tmp')
        self.assertEqual([r for r, _ in rule.parser_list], ['Bd', 'Cname', 'Chindex'])

    def test_underscore_separator(self):
        rule = DirRule('Cname_Chindex', base_dir='/tmp')
        self.assertEqual([r for r, _ in rule.parser_list], ['Bd', 'Cname', 'Chindex'])

    def test_empty_rule_rejected(self):
        with self.assertRaises(McException):
            DirRule('', base_dir='/tmp')


class TestDirRuleParse(unittest.TestCase):

    def test_comic_field(self):
        comic = make_comic()
        rule = DirRule('Bd_Cname', base_dir='/tmp/base')
        self.assertEqual(rule.decide_comic_root_dir(comic), '/tmp/base/社内恋爱_内部爱情')

    def test_chapter_field(self):
        comic = make_comic()
        chapter = comic[0]
        rule = DirRule('Bd_Cname_Chindextitle', base_dir='/tmp/base')
        self.assertEqual(
            rule.apply_rule_to_path(comic, chapter),
            '/tmp/base/社内恋爱_内部爱情/第01话',
        )

    def test_chapter_id_and_index(self):
        comic = make_comic()
        chapter = comic[1]
        rule = DirRule('Bd_Chid_Chindex', base_dir='/tmp/base')
        # 注意：Chindex 走 detail 语法时不做零填充
        self.assertEqual(rule.apply_rule_to_path(comic, chapter), '/tmp/base/292046/2')

    def test_fstring_rule(self):
        comic = make_comic()
        chapter = comic[1]
        rule = DirRule('Bd/{Cid}/{Chindex:03}', base_dir='/tmp/base')
        self.assertEqual(rule.apply_rule_to_path(comic, chapter), '/tmp/base/17001/002')

    def test_comic_root_dir_excludes_chapter_segments(self):
        comic = make_comic()
        rule = DirRule('Bd_Cname_Chindextitle', base_dir='/tmp/base')
        # 求漫画根目录时应丢掉 Chindextitle
        self.assertEqual(rule.decide_comic_root_dir(comic), '/tmp/base/社内恋爱_内部爱情')

    def test_fstring_rule_with_chapter_field(self):
        comic = make_comic()
        chapter = comic[0]
        rule = DirRule('Bd/{Cid}/{Chindex:03}', base_dir='/tmp/base')
        self.assertEqual(rule.apply_rule_to_path(comic, chapter), '/tmp/base/17001/001')

    def test_comic_root_dir_skips_fstring_chapter_segment(self):
        """f-string 里混用章节字段时，求漫画根目录不能因为缺 chapter 而崩。"""
        comic = make_comic()
        chapter = comic[0]
        chapter.update_date = '2024-03-04'

        McModuleConfig.CFIELD_ADVICE['year'] = lambda c: (c.update_date or '2024')[:4]
        McModuleConfig.HFIELD_ADVICE['safename'] = lambda ch: (ch.name or '').replace('/', '_')
        try:
            rule = DirRule('Bd/{Cyear}/{Chsafename}', base_dir='/tmp/base')
            # 有 chapter：两个片段都要生效
            self.assertEqual(rule.apply_rule_to_path(comic, chapter), '/tmp/base/2024/第01话')
            # 无 chapter：纯章节片段 {Chsafename} 应被跳过
            self.assertEqual(rule.decide_comic_root_dir(comic), '/tmp/base/2024')
        finally:
            McModuleConfig.CFIELD_ADVICE.clear()
            McModuleConfig.HFIELD_ADVICE.clear()

    def test_fstring_unknown_field_gives_clear_error(self):
        comic = make_comic()
        rule = DirRule('Bd/{Cnonexistent}', base_dir='/tmp/base')
        with self.assertRaises(McException) as ctx:
            rule.decide_comic_root_dir(comic)
        self.assertIn('Cnonexistent', str(ctx.exception))
        self.assertIn('可用字段', str(ctx.exception))

    def test_missing_comic_context_gives_actionable_error(self):
        """只给 chapter_id 不给 comic_id 时，应提示如何解决。"""
        comic = make_comic()
        chapter = comic[0]
        rule = DirRule('Bd_Cname_Chindextitle', base_dir='/tmp/base')
        with self.assertRaises(McException) as ctx:
            rule.apply_rule_to_path(None, chapter)
        self.assertIn('comic_id', str(ctx.exception))

    def test_explicit_null_falls_back_to_defaults(self):
        """
        用户显式写 null 时必须回退到运行时默认值，
        否则后面的 int(None) / 站点查找会崩。
        """
        option = McOption.construct({
            'client': {'impl': None},
            'download': {'threading': {'chapter': None, 'image': None}},
            'dir_rule': {'base_dir': None},
        })
        self.assertEqual(option.client.impl, 'tibiu')
        self.assertIsInstance(option.download.threading.chapter, int)
        self.assertIsInstance(option.download.threading.image, int)
        self.assertTrue(os.path.isabs(option.dir_rule.base_dir))
        self.assertIsNotNone(option.build_client())

    def test_normalize_zh_without_zhconv_returns_original(self):
        comic = make_comic(name='測試')
        rule = DirRule('Bd_Cname', base_dir='/tmp/base', normalize_zh='zh-cn')
        # 未安装 zhconv 时应静默回退，不应抛异常
        self.assertIn('測', rule.decide_comic_root_dir(comic))

    def test_unknown_field_raises(self):
        comic = make_comic()
        rule = DirRule('Bd_Cnonexistent', base_dir='/tmp/base')
        with self.assertRaises(McException):
            rule.decide_comic_root_dir(comic)

    def test_apply_rule_to_filename(self):
        comic = make_comic()
        chapter = comic[0]
        self.assertEqual(DirRule.apply_rule_to_filename(comic, chapter, 'Chindextitle'), '第01话')
        self.assertEqual(DirRule.apply_rule_to_filename(comic, None, 'Cname'), '社内恋爱_内部爱情')

    def test_custom_field_advice(self):
        comic = make_comic()
        McModuleConfig.CFIELD_ADVICE['myfield'] = lambda c: f'custom-{c.comic_id}'
        try:
            rule = DirRule('Bd_Cmyfield', base_dir='/tmp/base')
            self.assertEqual(rule.decide_comic_root_dir(comic), '/tmp/base/custom-17001')
        finally:
            McModuleConfig.CFIELD_ADVICE.clear()


class TestMcOption(unittest.TestCase):

    def test_default(self):
        option = McOption.default()
        self.assertEqual(option.client.impl, 'tibiu')
        self.assertEqual(option.dir_rule.rule_dsl, 'Bd_Cname_Chindextitle')
        self.assertEqual(option.download.threading.image, 30)
        self.assertTrue(option.download.cache)

    def test_construct_merges_defaults(self):
        option = McOption.construct({'client': {'impl': 'manhwa'}})
        self.assertEqual(option.client.impl, 'manhwa')
        # 未指定的配置项应保留默认值
        self.assertEqual(option.client.retry_times, McModuleConfig.DEFAULT_RETRY_TIMES)
        self.assertIn('postman', option.client.src_dict)

    def test_construct_missing_section_raises(self):
        with self.assertRaises(McException):
            McOption.construct({'client': {'impl': 'tibiu'}}, cover_default=False)

    def test_deconstruct_roundtrip(self):
        option = McOption.construct({'client': {'impl': 'manhwa'},
                                     'dir_rule': {'base_dir': '/tmp/x'}})
        dic = option.deconstruct()
        self.assertEqual(dic['client']['impl'], 'manhwa')
        self.assertEqual(dic['dir_rule']['base_dir'], '/tmp/x')

        option2 = McOption.construct(dic)
        self.assertEqual(option2.client.impl, 'manhwa')

    def test_from_file_and_to_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, 'opt.yml')
            McOption.construct({'client': {'impl': 'manhwa'}}).to_file(path)
            option = McOption.from_file(path)
            self.assertEqual(option.client.impl, 'manhwa')
            self.assertEqual(option.filepath, path)

    def test_old_version_compat_plugin_aliases(self):
        option = McOption.construct({
            'plugins': {'after_photo': [{'plugin': 'zip'}]},
        })
        self.assertIn('after_chapter', option.plugins.src_dict)
        self.assertNotIn('after_photo', option.plugins.src_dict)

    def test_old_version_compat_threading_batch_count(self):
        option = McOption.construct({
            'download': {'threading': {'batch_count': 5}},
        })
        self.assertEqual(option.download.threading.image, 5)

    def test_decide_image_filepath(self):
        comic = make_comic()
        chapter = comic[0]
        chapter.set_image_url_list(['https://x/a/0001.webp', 'https://x/a/0002.webp'])
        chapter.from_comic = comic

        with tempfile.TemporaryDirectory() as tmp:
            option = McOption.construct({
                'dir_rule': {'base_dir': tmp, 'rule': 'Bd_Cname_Chindextitle'},
            })
            image = chapter[0]
            self.assertEqual(image.filename, '0001.webp')
            path = option.decide_image_filepath(image)
            self.assertTrue(path.startswith(tmp))
            self.assertTrue(path.endswith('0001.webp'))

    def test_decide_image_suffix_override(self):
        comic = make_comic()
        chapter = comic[0]
        chapter.set_image_url_list(['https://x/a/0001.webp'])
        chapter.from_comic = comic

        option = McOption.construct({'download': {'image': {'suffix': '.jpg'}}})
        self.assertEqual(option.decide_image_suffix(chapter[0]), '.jpg')

    def test_copy_option(self):
        option = McOption.construct({'client': {'impl': 'manhwa'}})
        copied = option.copy_option()
        self.assertEqual(copied.client.impl, 'manhwa')
        self.assertIsNot(copied.client, option.client)

    def test_build_client_is_cached(self):
        option = McOption.construct({'client': {'impl': 'tibiu'}})
        client1 = option.build_client()
        client2 = option.build_client()
        self.assertIs(client1, client2)

    def test_new_client_with_overrides(self):
        option = McOption.construct({'client': {'impl': 'tibiu'}})
        client = option.new_client(impl='manhwa')
        self.assertEqual(client.site, 'manhwa')

    def test_update_cookies(self):
        option = McOption.construct({'client': {'impl': 'tibiu'}})
        option.update_cookies({'a': '1'})
        option.update_cookies({'b': '2'})
        cookies = option.client.postman.meta_data.get('cookies')
        self.assertEqual(dict(cookies), {'a': '1', 'b': '2'})

    def test_plugin_dispatch_requires_registered_key(self):
        option = McOption.construct({'plugins': {'after_comic': [{'plugin': 'not_exists'}]}})
        with self.assertRaises(McException):
            option.call_all_plugin('after_comic', safe=False)

    def test_plugin_dispatch_safe_mode_swallows(self):
        from mccms.mc_plugin import McOptionPlugin

        class BoomPlugin(McOptionPlugin):
            plugin_key = 'test_boom'

            def invoke(self, **kwargs):
                raise RuntimeError('boom')

        McModuleConfig.register_plugin(BoomPlugin)
        try:
            option = McOption.construct({'plugins': {'after_comic': [{'plugin': 'test_boom'}]}})
            # safe 默认为 True -> 不应抛出
            option.call_all_plugin('after_comic')
        finally:
            McModuleConfig.REGISTRY_PLUGIN.pop('test_boom', None)

    def test_after_init_plugin_runs_once(self):
        from mccms.mc_plugin import McOptionPlugin

        calls = []

        class InitPlugin(McOptionPlugin):
            plugin_key = 'test_init'

            def invoke(self, **kwargs):
                calls.append(1)

        McModuleConfig.register_plugin(InitPlugin)
        try:
            option = McOption.construct({'plugins': {'after_init': [{'plugin': 'test_init'}]}})
            self.assertEqual(len(calls), 1)

            # copy_option 不应重复触发 after_init
            copied = option.copy_option()
            self.assertEqual(len(calls), 1)
            self.assertEqual(copied.client.impl, option.client.impl)

            # 显式关闭
            McOption.construct({'plugins': {'after_init': [{'plugin': 'test_init'}]}},
                               call_after_init_plugin=False)
            self.assertEqual(len(calls), 1)
        finally:
            McModuleConfig.REGISTRY_PLUGIN.pop('test_init', None)

    def test_plugin_valid_policy(self):
        from mccms.mc_plugin import McOptionPlugin

        class StrictPlugin(McOptionPlugin):
            plugin_key = 'test_valid'

            def invoke(self, must=None, **kwargs):
                self.require_param(must, '必须提供 must 参数')

        McModuleConfig.register_plugin(StrictPlugin)
        try:
            # 默认 valid=log -> 只记日志，不抛
            McOption.construct({'plugins': {'after_comic': [{'plugin': 'test_valid'}]}})

            # valid=raise -> 上抛 PluginValidationException
            option = McOption.construct({
                'plugins': {'after_comic': [{'plugin': 'test_valid', 'valid': 'raise'}]}})
            with self.assertRaises(PluginValidationException):
                option.call_all_plugin('after_comic')

            # valid=ignore -> 静默
            option2 = McOption.construct({
                'plugins': {'after_comic': [{'plugin': 'test_valid', 'valid': 'ignore'}]}})
            option2.call_all_plugin('after_comic')

            # 全局 plugins.valid = raise 也生效
            option3 = McOption.construct({
                'plugins': {'valid': 'raise',
                            'after_comic': [{'plugin': 'test_valid'}]}})
            with self.assertRaises(PluginValidationException):
                option3.call_all_plugin('after_comic')
        finally:
            McModuleConfig.REGISTRY_PLUGIN.pop('test_valid', None)

    def test_dependency_strategy_failed_fast_raises(self):
        from mccms.mc_plugin import McOptionPlugin

        class NeedsLibPlugin(McOptionPlugin):
            plugin_key = 'test_needs_lib'
            plugin_dependencies = ('definitely_not_installed_lib_xyz',)

            def invoke(self, **kwargs):
                pass

        McModuleConfig.register_plugin(NeedsLibPlugin)
        try:
            with self.assertRaises(McException):
                McOption.construct({
                    'plugins': {'dependencies_strategy': 'failed-fast',
                                'after_init': [{'plugin': 'test_needs_lib'}]}})

            # ignore-only-log 时不应抛
            McOption.construct({
                'plugins': {'dependencies_strategy': 'ignore-only-log',
                            'after_init': [{'plugin': 'test_needs_lib'}]}})
        finally:
            McModuleConfig.REGISTRY_PLUGIN.pop('test_needs_lib', None)

    def test_plugin_extra_overrides_kwargs(self):
        from mccms.mc_plugin import McOptionPlugin

        captured = {}

        class CapturePlugin(McOptionPlugin):
            plugin_key = 'test_capture'

            def invoke(self, value=None, **kwargs):
                captured['value'] = value

        McModuleConfig.register_plugin(CapturePlugin)
        try:
            option = McOption.construct({
                'plugins': {'after_comic': [{'plugin': 'test_capture',
                                             'kwargs': {'value': 'from_kwargs'}}]},
            })
            option.call_all_plugin('after_comic', value='from_extra')
            self.assertEqual(captured['value'], 'from_extra')
        finally:
            McModuleConfig.REGISTRY_PLUGIN.pop('test_capture', None)


if __name__ == '__main__':
    unittest.main()
