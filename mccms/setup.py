"""
兼容旧版 pip / setuptools 的 shim。

项目元数据以 pyproject.toml 为唯一来源；
这里保留 setup.py 是为了让 `pip install -e .` 在老版本 pip（<21.3）
上也能工作——否则会报 "editable mode currently requires a setuptools-based build"。
"""

from setuptools import setup

setup()
