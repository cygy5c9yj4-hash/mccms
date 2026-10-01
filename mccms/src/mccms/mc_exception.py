"""
mccms 异常体系。

对应 jmcomic 的 jm_exception.py：
- 所有异常都继承 McException
- 通过 ExceptionTool.raises 抛出，抛出前会通知 REGISTRY_EXCEPTION_LISTENER 中注册的监听器
"""

from typing import Any, Optional

from .mc_config import McModuleConfig, mc_log

__all__ = [
    'McException',
    'MissingComicChapterException',
    'RegularNotMatchException',
    'ResponseUnexpectedException',
    'PartialDownloadFailedException',
    'PluginValidationException',
    'AccessDeniedException',
    'LoginRequiredException',
    'VipRequiredException',
    'ChapterNotAccessibleException',
    'ExceptionTool',
    'register_exception_listener',
]


class McException(Exception):
    """mccms 所有异常的基类。"""

    def __init__(self, msg, context=None, *args):
        super().__init__(msg, *args)
        self.msg = msg
        self.context = context or {}

    def __str__(self):
        return f'{self.__class__.__name__}: {self.msg}'


class MissingComicChapterException(McException):
    """漫画/章节不存在，或章节不属于该漫画。"""


class RegularNotMatchException(McException):
    """解析响应时正则/结构不匹配。"""


class ResponseUnexpectedException(McException):
    """响应不符合预期（如 code != 1）。"""


class PartialDownloadFailedException(McException):
    """部分资源下载失败。"""


class PluginValidationException(McException):
    """插件参数校验失败。"""

    def __init__(self, plugin, msg: str):
        super().__init__(msg)
        self.plugin = plugin

    def __str__(self):
        key = getattr(self.plugin, 'plugin_key', self.plugin)
        return f'插件[{key}]参数校验失败: {self.msg}'


class AccessDeniedException(McException):
    """
    访问被拒绝（服务端明确拒绝提供资源）。

    这是所有"无权访问"异常的基类，调用方可以只捕获它来处理全部权限问题。
    context 中通常包含 site / comic_id / chapter_id / reason。
    """


class LoginRequiredException(AccessDeniedException):
    """需要登录后才能访问。"""

    def __init__(self, msg='该章节需要登录后才能访问，请在 option 中配置 client.username/password 或 client.cookies',
                 context=None):
        super().__init__(msg, context)


class VipRequiredException(AccessDeniedException):
    """需要会员（或金币/月票）权益后才能访问。"""

    def __init__(self, msg='该章节需要会员权益，当前账号无权访问', context=None):
        super().__init__(msg, context)


class ChapterNotAccessibleException(AccessDeniedException):
    """章节不可访问（下架 / 审核 / 未发布）。"""


class ExceptionTool:

    @classmethod
    def raises(cls, msg, context=None, etype=None):
        """抛出异常，并在抛出前通知所有匹配的监听器。"""
        e = (etype or McException)(msg, context)
        cls.notify_all_listeners(e)
        raise e

    @classmethod
    def raise_if(cls, condition, msg, context=None, etype=None):
        if condition:
            cls.raises(msg, context, etype)

    @classmethod
    def require_true(cls, case: Any, msg: str, context=None, etype=None):
        if not case:
            cls.raises(msg, context, etype)

    @classmethod
    def require_not_none(cls, obj, msg: str, context=None, etype=None):
        if obj is None:
            cls.raises(msg, context, etype)
        return obj

    @classmethod
    def notify_all_listeners(cls, e: BaseException):
        for accept_type, listener in McModuleConfig.REGISTRY_EXCEPTION_LISTENER.items():
            if isinstance(e, accept_type):
                try:
                    listener(e)
                except Exception as listener_error:  # pragma: no cover
                    mc_log('exception.listener', f'异常监听器执行失败: {listener_error!r}', listener_error)


def register_exception_listener(etype, listener):
    """注册异常监听器。"""
    McModuleConfig.register_exception_listener(etype, listener)


def raise_access_exception(msg: str,
                           context: Optional[dict] = None,
                           access_code: Optional[int] = None,
                           access_type: Optional[str] = None):
    """
    统一的"无权访问"异常工厂。

    各站点的 client 解析响应时，把服务端的拒绝原因归一化成：
    - access_code == 2 / access_type == 'login'  -> LoginRequiredException
    - access_type == 'vip' / 'cion'              -> VipRequiredException
    - 其他                                        -> ChapterNotAccessibleException

    服务端原文会保留在 context['raw_msg']，抛出的 message 会追加一句可操作的提示。
    """
    context = dict(context or {})
    context['raw_msg'] = msg
    if access_code is not None:
        context['access_code'] = access_code
    if access_type is not None:
        context['access_type'] = access_type

    if access_code == 2 or access_type == 'login':
        raise LoginRequiredException(
            f'{msg} —— 该内容需要登录后才能访问。'
            f'请通过 option.client.username/password、client.cookies 或 CLI 的 '
            f'--username/--password/--cookie 提供你自己的账号',
            context,
        )

    if access_type in ('vip', 'cion', 'ticket', 'pay'):
        raise VipRequiredException(
            f'{msg} —— 该内容需要会员/金币权益，当前账号无权访问',
            context,
        )

    raise ChapterNotAccessibleException(
        f'{msg} —— 该章节当前不可访问（可能是权益、下架或未发布）',
        context,
    )
