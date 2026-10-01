"""
零依赖的迷你 HTML DOM + CSS 选择器。

为什么不用 BeautifulSoup/lxml：本库希望核心依赖只有 requests + pyyaml，
而这些站点需要解析的页面结构都很规整（列表卡片 + 详情页元信息），
一个几百行的 parser 足够，且行为完全可控、可测试。

支持的 CSS 选择器子集：
- 标签名            div
- 类                .comic-title
- id                #main
- 属性              [data-id] / [data-id="12"] / [href^="/index.php/comic/"]
                    [class~="hl"] / [class*="comic"] / [class$=".jpg"]
- 后代组合          .a .b
- 子代组合          .a > .b
- 分组              .a, .b
"""

from html.parser import HTMLParser
from typing import Dict, Iterator, List, Optional, Tuple

__all__ = ['Node', 'parse_html', 'HtmlParseError']

# HTML 里的自闭合/空元素
VOID_TAGS = {
    'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input',
    'link', 'meta', 'param', 'source', 'track', 'wbr',
}

# 这些标签出现新的同名标签时，隐式闭合前一个（HTML 的 implied end tag 行为）
IMPLICIT_CLOSE_SAME_TAG = {'li', 'p', 'td', 'th', 'tr', 'dt', 'dd', 'option', 'thead', 'tbody'}

# 这些标签内的文本不参与 text 提取
SKIP_TEXT_TAGS = {'script', 'style'}


class HtmlParseError(Exception):
    pass


class Node:
    __slots__ = ('tag', 'attrs', 'children', 'parent', '_text')

    def __init__(self, tag: str, attrs: Optional[Dict[str, str]] = None, parent: Optional['Node'] = None):
        self.tag: str = tag
        self.attrs: Dict[str, str] = attrs or {}
        self.children: List['Node'] = []
        self.parent: Optional['Node'] = parent
        self._text: str = ''

    # ------------------------------------------------------------------ 基础

    @property
    def classes(self) -> List[str]:
        return (self.attrs.get('class') or '').split()

    def has_class(self, name: str) -> bool:
        return name in self.classes

    def attr(self, name: str, default=None):
        return self.attrs.get(name, default)

    def get(self, name: str, default=None):
        return self.attrs.get(name, default)

    @property
    def own_text(self) -> str:
        return self._text

    @property
    def text(self) -> str:
        """所有后代文本拼接（script/style 除外），压缩空白。"""
        if self.tag in SKIP_TEXT_TAGS:
            return ''

        parts = [self._text]
        for child in self.children:
            if child.tag in SKIP_TEXT_TAGS:
                continue
            parts.append(child.text)
        return ' '.join(''.join(parts).split())

    @property
    def raw_text(self) -> str:
        return self._text

    def iter_nodes(self) -> Iterator['Node']:
        """深度优先遍历自身及所有后代。"""
        yield self
        for child in self.children:
            yield from child.iter_nodes()

    def __repr__(self):
        cls = f'.{".".join(self.classes)}' if self.classes else ''
        return f'<Node {self.tag}{cls}>'

    # ------------------------------------------------------------------ 查询

    def find(self, selector: str) -> Optional['Node']:
        result = self.find_all(selector)
        return result[0] if result else None

    def select(self, selector: str) -> List['Node']:
        return self.find_all(selector)

    def find_all(self, selector: str) -> List['Node']:
        """
        在自身子树内查询（不包含自身）。
        """
        matchers = _parse_selector(selector)
        result: List[Node] = []
        seen = set()
        for matcher in matchers:
            for node in matcher.search(self):
                if id(node) not in seen:
                    seen.add(id(node))
                    result.append(node)
        return result

    def closest(self, selector: str) -> Optional['Node']:
        """向上查找最近的匹配祖先（含自身）。"""
        matchers = _parse_selector(selector)
        node: Optional[Node] = self
        while node is not None:
            if any(matcher.matches_chain(node) for matcher in matchers):
                return node
            node = node.parent
        return None

    def children_by_tag(self, tag: str) -> List['Node']:
        return [c for c in self.children if c.tag == tag]

    # 语义化便捷访问
    def first_child(self) -> Optional['Node']:
        return self.children[0] if self.children else None


# --------------------------------------------------------------------------------------
# 选择器
# --------------------------------------------------------------------------------------

def _split_attr_expr(expr: str) -> Tuple[str, Optional[str], Optional[str]]:
    """
    解析 [attr] / [attr=v] / [attr^=v] / [attr$=v] / [attr*=v] / [attr~=v]
    返回 (name, op, value)
    """
    for op in ('^=', '$=', '*=', '~=', '|='):
        if op in expr:
            name, value = expr.split(op, 1)
            return name.strip(), op, _unquote(value.strip())
    if '=' in expr:
        name, value = expr.split('=', 1)
        return name.strip(), '=', _unquote(value.strip())
    return expr.strip(), None, None


def _unquote(value: str) -> str:
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ('"', "'"):
        return value[1:-1]
    return value


class _Compound:
    """单个复合选择器，如 div.comic-title[data-id]。"""

    __slots__ = ('tag', 'id', 'classes', 'attrs')

    def __init__(self):
        self.tag: Optional[str] = None
        self.id: Optional[str] = None
        self.classes: List[str] = []
        self.attrs: List[Tuple[str, Optional[str], Optional[str]]] = []

    def match_compound(self, node: Node) -> bool:
        if self.tag is not None and node.tag != self.tag:
            return False
        if self.id is not None and node.attrs.get('id') != self.id:
            return False
        if self.classes:
            node_classes = node.classes
            if not all(c in node_classes for c in self.classes):
                return False
        for name, op, value in self.attrs:
            actual = node.attrs.get(name)
            if actual is None:
                return False
            if op is None:
                continue
            if op == '=' and actual != value:
                return False
            if op == '^=' and not actual.startswith(value):
                return False
            if op == '$=' and not actual.endswith(value):
                return False
            if op == '*=' and value not in actual:
                return False
            if op == '~=' and value not in actual.split():
                return False
            if op == '|=' and not (actual == value or actual.startswith(value + '-')):
                return False
        return True


class _Matcher:
    """
    一个选择器组（后代/子代组合）。
    steps: [(combinator, compound), ...]，combinator ∈ {' ', '>'}
    """

    def __init__(self, steps: List[Tuple[str, _Compound]]):
        self.steps = steps

    def matches_chain(self, node: Node) -> bool:
        """
        判断 node 是否匹配本选择器（用于 closest）：
        node 匹配最后一个 compound，且其祖先链满足前面的 steps。
        """
        steps = self.steps
        if not steps[-1][1].match_compound(node):
            return False

        current = node
        for i in range(len(steps) - 1, 0, -1):
            combinator, _ = steps[i]
            prev_compound = steps[i - 1][1]

            if combinator == '>':
                parent = current.parent
                if parent is None or not prev_compound.match_compound(parent):
                    return False
                current = parent
            else:
                ancestor = current.parent
                while ancestor is not None and not prev_compound.match_compound(ancestor):
                    ancestor = ancestor.parent
                if ancestor is None:
                    return False
                current = ancestor

        return True

    def search(self, root: Node) -> List[Node]:
        # 从第一个 compound 开始筛，再逐级向内匹配
        combinator, first = self.steps[0]
        if combinator == '>':
            candidates = list(root.children)
        else:
            candidates = [n for n in root.iter_nodes() if n is not root]

        matched = [n for n in candidates if first.match_compound(n)]

        for combinator, compound in self.steps[1:]:
            next_matched = []
            for node in matched:
                if combinator == '>':
                    pool = node.children
                else:
                    pool = [n for n in node.iter_nodes() if n is not node]
                for cand in pool:
                    if compound.match_compound(cand):
                        next_matched.append(cand)
            matched = next_matched
            if not matched:
                break

        return matched


def _parse_compound(text: str) -> _Compound:
    compound = _Compound()
    i = 0
    length = len(text)

    while i < length:
        char = text[i]

        if char == '[':
            end = text.find(']', i)
            if end == -1:
                raise HtmlParseError(f'选择器属性括号未闭合: {text}')
            compound.attrs.append(_split_attr_expr(text[i + 1:end]))
            i = end + 1

        elif char == '.':
            j = i + 1
            while j < length and (text[j].isalnum() or text[j] in '_-'):
                j += 1
            compound.classes.append(text[i + 1:j])
            i = j

        elif char == '#':
            j = i + 1
            while j < length and (text[j].isalnum() or text[j] in '_-'):
                j += 1
            compound.id = text[i + 1:j]
            i = j

        elif char == '*':
            i += 1

        else:
            j = i
            while j < length and (text[j].isalnum() or text[j] in '_-'):
                j += 1
            if j == i:
                raise HtmlParseError(f'无法解析的选择器片段: {text[i:]!r}')
            compound.tag = text[i:j].lower()
            i = j

    return compound


def _parse_selector(selector: str) -> List[_Matcher]:
    matchers = []
    for group in selector.split(','):
        group = group.strip()
        if not group:
            continue

        steps: List[Tuple[str, _Compound]] = []
        combinator = ' '
        token = ''
        depth = 0

        for char in group:
            if char == '[':
                depth += 1
                token += char
            elif char == ']':
                depth -= 1
                token += char
            elif depth == 0 and char == '>':
                if token.strip():
                    steps.append((combinator, _parse_compound(token.strip())))
                combinator = '>'
                token = ''
            elif depth == 0 and char.isspace():
                if token.strip():
                    steps.append((combinator, _parse_compound(token.strip())))
                    combinator = ' '
                token = ''
            else:
                token += char

        if token.strip():
            steps.append((combinator, _parse_compound(token.strip())))

        if steps:
            matchers.append(_Matcher(steps))

    if not matchers:
        raise HtmlParseError(f'空选择器: {selector!r}')
    return matchers


# --------------------------------------------------------------------------------------
# 解析
# --------------------------------------------------------------------------------------

class _DomBuilder(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node('#document')
        self.stack: List[Node] = [self.root]

    @property
    def current(self) -> Node:
        return self.stack[-1]

    def handle_starttag(self, tag, attrs):
        tag = tag.lower()

        if tag in IMPLICIT_CLOSE_SAME_TAG:
            # <li>a<li>b 这类写法：遇到同名标签时先隐式闭合
            for i in range(len(self.stack) - 1, 0, -1):
                if self.stack[i].tag == tag:
                    del self.stack[i:]
                    break

        node = Node(tag, {k.lower(): (v if v is not None else '') for k, v in attrs}, self.current)
        self.current.children.append(node)
        if tag not in VOID_TAGS:
            self.stack.append(node)

    def handle_startendtag(self, tag, attrs):
        node = Node(tag.lower(), {k.lower(): (v if v is not None else '') for k, v in attrs}, self.current)
        self.current.children.append(node)

    def handle_endtag(self, tag):
        tag = tag.lower()
        # 找到栈中最近的同名节点并弹栈
        for i in range(len(self.stack) - 1, 0, -1):
            if self.stack[i].tag == tag:
                del self.stack[i:]
                return

    def handle_data(self, data):
        if data:
            self.current._text += data


def parse_html(html: str) -> Node:
    """把 HTML 文本解析为文档根节点。"""
    if html is None:
        raise HtmlParseError('HTML 内容为空')
    builder = _DomBuilder()
    builder.feed(html)
    builder.close()
    return builder.root
