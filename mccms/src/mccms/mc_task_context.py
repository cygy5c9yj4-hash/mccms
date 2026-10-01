"""
任务上下文（对应 jmcomic 的 jm_task_context.py）。

用于在并发下载时把"当前在下载哪个资源"传递给日志与插件，
并支持在子线程中继承父线程的上下文。
"""

from contextlib import contextmanager
from contextvars import ContextVar
from typing import Dict, Optional

__all__ = [
    'mc_task_context',
    'get_mc_task_context',
    'bind_mc_task_context',
    'current_task_id',
]

_mc_task_context: ContextVar[Optional[Dict]] = ContextVar('mc_task_context', default=None)


def get_mc_task_context() -> Dict:
    return dict(_mc_task_context.get() or {})


@contextmanager
def mc_task_context(**kwargs):
    """
    进入一个任务上下文，退出时恢复。

    用法::

        with mc_task_context(download_type='comic', mc_id='17001'):
            ...
    """
    current = _mc_task_context.get()
    merged = {**(current or {}), **kwargs}
    token = _mc_task_context.set(merged)
    try:
        yield merged
    finally:
        _mc_task_context.reset(token)


def bind_mc_task_context(func):
    """
    把当前上下文快照绑定到函数上，供子线程使用。

    contextvars 在线程池中不会自动继承，所以需要显式快照 + 重设。
    """
    from functools import wraps

    snapshot = get_mc_task_context()

    @wraps(func)
    def wrapper(*args, **kwargs):
        if not snapshot:
            return func(*args, **kwargs)

        token = _mc_task_context.set(snapshot)
        try:
            return func(*args, **kwargs)
        finally:
            _mc_task_context.reset(token)

    return wrapper


def current_task_id() -> Optional[str]:
    return get_mc_task_context().get('task_id')
