"""
支持 `python -m mccms` 直接调用命令行。

等价于控制台脚本 `mccms`。
"""

import sys

from .cli import main

if __name__ == '__main__':
    sys.exit(main())
