"""工具层测试：文本 / 路径 / 容器 / 序列化 / 任务上下文。"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'src'))

from mccms.mc_exception import ExceptionTool, McException  # noqa: E402
from mccms.mc_task_context import (bind_mc_task_context,  # noqa: E402
                                   get_mc_task_context, mc_task_context)
from mccms.mc_toolkit import (AdvancedDict, MccmsText, PackerUtil,  # noqa: E402
                              field_cache, fix_filepath, fix_suffix,
                              fix_windir_name, of_file_name)


class TestFixNames(unittest.TestCase):

    def test_fix_windir_name_replaces_forbidden_chars(self):
        self.assertEqual(fix_windir_name('a/b\\c:d*e?f"g<h>i|j'), 'a_b_c_d_e_f_g_h_i_j')
        self.assertEqual(fix_windir_name('line\nbreak\ttab'), 'line_break_tab')
        self.assertEqual(fix_windir_name('trailing...'), 'trailing')

    def test_fix_windir_name_keeps_normal_text(self):
        self.assertEqual(fix_windir_name('社内恋爱/内部爱情'), '社内恋爱_内部爱情')
        self.assertEqual(fix_windir_name('第1话 暴君'), '第1话 暴君')

    def test_fix_suffix(self):
        self.assertEqual(fix_suffix('zip'), '.zip')
        self.assertEqual(fix_suffix('.zip'), '.zip')
        self.assertEqual(fix_suffix(''), '')

    def test_fix_filepath(self):
        self.assertEqual(fix_filepath('a\\b\\c'), 'a/b/c')
        self.assertEqual(fix_filepath('a//b///c'), 'a/b/c')

    def test_of_file_name(self):
        self.assertEqual(of_file_name('/a/b/c.webp'), 'c.webp')
        self.assertEqual(of_file_name('/a/b/c.webp', trim_suffix=True), 'c')


class TestMccmsText(unittest.TestCase):

    def test_parse_to_mc_id(self):
        cases = {
            '17001': '17001',
            '/comic/17001': '17001',
            'https://cache.tibiu.net/comic/17001': '17001',
            '/chapter/17001/291931': '291931',
            'https://www.manhwa.wang/index.php/chapter/936939': '936939',
            'https://www.manhwa.wang/index.php/comic/27032': '27032',
        }
        for text, expected in cases.items():
            self.assertEqual(MccmsText.parse_to_mc_id(text), expected, text)

    def test_parse_to_mc_id_invalid(self):
        with self.assertRaises(McException):
            MccmsText.parse_to_mc_id('no-digits-here')

    def test_parse_ids_from_url(self):
        self.assertEqual(MccmsText.parse_chapter_id_from_url('/chapter/17001/291931'), '291931')
        self.assertEqual(MccmsText.parse_chapter_id_from_url('/index.php/chapter/936939'), '936939')
        self.assertEqual(MccmsText.parse_comic_id_from_url('/comic/17001'), '17001')
        self.assertIsNone(MccmsText.parse_comic_id_from_url('/chapter/1/2'))

    def test_parse_slug_from_url(self):
        self.assertEqual(MccmsText.parse_slug_from_url('/index.php/comic/mozhouweixianzaoyu'),
                         'mozhouweixianzaoyu')
        self.assertIsNone(MccmsText.parse_slug_from_url('/index.php/comic/27032'))

    def test_parse_human_number(self):
        self.assertEqual(MccmsText.parse_human_number('1487'), 1487)
        self.assertEqual(MccmsText.parse_human_number('14 万'), 140000)
        self.assertEqual(MccmsText.parse_human_number('1.2亿'), 120000000)
        self.assertEqual(MccmsText.parse_human_number(''), 0)
        self.assertEqual(MccmsText.parse_human_number(None), 0)
        self.assertEqual(MccmsText.parse_human_number('1,234'), 1234)

    def test_safe_int(self):
        self.assertEqual(MccmsText.safe_int('42'), 42)
        self.assertEqual(MccmsText.safe_int('abc', -1), -1)
        self.assertEqual(MccmsText.safe_int(None, 7), 7)

    def test_parse_dsl_text_env(self):
        os.environ['MCCMS_TEST_DIR'] = '/tmp/x'
        self.assertEqual(MccmsText.parse_dsl_text('${MCCMS_TEST_DIR}/a'), '/tmp/x/a')
        with self.assertRaises(McException):
            MccmsText.parse_dsl_text('${MCCMS_NOT_EXIST_ENV}/a')

    def test_tokenize_and_orig_name(self):
        title = '喂我吃吧 老師! [漢化組] [DL版]'
        self.assertEqual(MccmsText.parse_orig_name(title), '喂我吃吧 老師!')
        self.assertEqual(MccmsText.tokenize('a [b] c'), ['a', '[b]', 'c'])
        self.assertEqual(MccmsText.parse_orig_name('[only-bracket]'), None)

    def test_unescape(self):
        self.assertEqual(MccmsText.unescape('1 &#40;2&#41;'), '1 (2)')
        self.assertEqual(MccmsText.unescape(None), '')

    def test_limit_text(self):
        self.assertEqual(MccmsText.limit_text('abcdef', 3), 'abc...(3 more)')
        self.assertEqual(MccmsText.limit_text('ab', 3), 'ab')


class TestAdvancedDict(unittest.TestCase):

    def test_attribute_access(self):
        d = AdvancedDict({'a': {'b': {'c': 1}}})
        self.assertEqual(d.a.b.c, 1)
        self.assertEqual(d['a']['b']['c'], 1)

    def test_missing_key_raises_key_error(self):
        d = AdvancedDict({'a': 1})
        with self.assertRaises(KeyError):
            _ = d.nope

    def test_list_wrapping(self):
        d = AdvancedDict({'chapters': [{'x': 1}, {'x': 2}]})
        self.assertEqual([i.x for i in d.chapters], [1, 2])

    def test_reserved_method_names_require_index_access(self):
        """
        dict 风格的方法名（items/keys/values/get）会遮蔽同名配置项，
        此时需要用下标访问，这是 AdvancedDict 的已知取舍。
        """
        d = AdvancedDict({'items': [{'x': 1}]})
        self.assertTrue(callable(d.items))          # 属性访问拿到的是方法
        self.assertEqual(d['items'][0].x, 1)        # 下标访问拿到的是数据

    def test_src_dict_is_copy(self):
        d = AdvancedDict({'a': {'b': 1}})
        src = d.src_dict
        src['a']['b'] = 999
        self.assertEqual(d.a.b, 1)

    def test_get_default(self):
        d = AdvancedDict({})
        self.assertEqual(d.get('missing', 5), 5)

    def test_nested_dict_in_list_is_wrapped(self):
        # option.plugins 就是这样被包装的，插件分发依赖这个行为
        d = AdvancedDict({'plugins': {'after_comic': [{'plugin': 'zip'}]}})
        pinfo = d.plugins.after_comic[0]
        self.assertEqual(pinfo.get('plugin'), 'zip')
        self.assertNotIsInstance(pinfo, dict)
        self.assertIsInstance(pinfo, AdvancedDict)


class TestPackerUtil(unittest.TestCase):

    def test_yml_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, 'a.yml')
            data = {'dir_rule': {'rule': 'Bd_Cname'}, '中文': '值'}
            PackerUtil.pack(data, path)
            loaded, mode = PackerUtil.unpack(path)
            self.assertEqual(mode, 'yml')
            self.assertEqual(loaded, data)

    def test_json_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, 'a.json')
            data = {'a': [1, 2, 3]}
            PackerUtil.pack(data, path)
            loaded, mode = PackerUtil.unpack(path)
            self.assertEqual(mode, 'json')
            self.assertEqual(loaded, data)

    def test_unpack_by_str(self):
        data, mode = PackerUtil.unpack_by_str('a: 1\n', 'yml')
        self.assertEqual(data, {'a': 1})
        self.assertEqual(mode, 'yml')


class TestFieldCache(unittest.TestCase):

    def test_cached_once(self):
        calls = []

        class A:
            @field_cache()
            def build(self):
                calls.append(1)
                return object()

        a = A()
        first = a.build()
        second = a.build()
        self.assertIs(first, second)
        self.assertEqual(len(calls), 1)


class TestTaskContext(unittest.TestCase):

    def test_context_manager_restores(self):
        self.assertEqual(get_mc_task_context(), {})
        with mc_task_context(download_type='comic', mc_id='1'):
            self.assertEqual(get_mc_task_context()['mc_id'], '1')
            with mc_task_context(mc_id='2'):
                self.assertEqual(get_mc_task_context()['mc_id'], '2')
                self.assertEqual(get_mc_task_context()['download_type'], 'comic')
            self.assertEqual(get_mc_task_context()['mc_id'], '1')
        self.assertEqual(get_mc_task_context(), {})

    def test_bind_snapshot_in_new_thread(self):
        import threading

        seen = {}

        def worker():
            seen.update(get_mc_task_context())

        with mc_task_context(download_type='chapter', mc_id='99'):
            t = threading.Thread(target=bind_mc_task_context(worker))
            t.start()
            t.join()

        self.assertEqual(seen.get('mc_id'), '99')
        self.assertEqual(seen.get('download_type'), 'chapter')


class TestExceptionTool(unittest.TestCase):

    def test_require_true(self):
        ExceptionTool.require_true(True, 'ok')
        with self.assertRaises(McException):
            ExceptionTool.require_true(False, 'boom')

    def test_listener_notified(self):
        from mccms.mc_config import McModuleConfig

        received = []
        McModuleConfig.register_exception_listener(McException, received.append)
        try:
            ExceptionTool.raises('hello')
        except McException:
            pass
        finally:
            McModuleConfig.REGISTRY_EXCEPTION_LISTENER.clear()

        self.assertEqual(len(received), 1)
        self.assertEqual(received[0].msg, 'hello')


if __name__ == '__main__':
    unittest.main()
