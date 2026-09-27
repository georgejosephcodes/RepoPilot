"""Find top-level symbols (and class members) in a tree-sitter tree.

This module knows node types only. Chunk sizing, splitting, and gap handling live in
chunk.py. Only top-level statements and class members are examined, so nesting depth
cannot cause deep recursion. Nested functions and classes stay inside their parent.
"""

from dataclasses import dataclass

from tree_sitter import Node


@dataclass(frozen=True)
class Symbol:
    name: str                   # "Scheduler.Run", "Foo.method", "top"
    kind: str                   # function|method|class|struct|interface|type|enum|namespace
    start_line: int             # 1-based inclusive; includes attached decorators and leading comments
    end_line: int
    members: tuple["Symbol", ...] = ()   # methods of a class; empty otherwise


_ATTACHABLE = ("comment", "decorator")
_JS_FUNCTION_VALUES = frozenset({"arrow_function", "function_expression", "function", "generator_function"})
_MAX_ASSIGN_TARGET = 80


def _text(node: Node | None) -> str | None:
    if node is None:
        return None
    return node.text.decode("utf-8", "replace")


def _pos(point) -> tuple[int, int]:
    """(row, column) of a tree-sitter Point.

    Always unpack the Point instead of reading `.row` or `.column` on a temporary:
    with tree-sitter 0.26.0 on Python 3.14, `node.end_point.row` on a large tree crashed
    the interpreter (segmentation fault). Unpacking is safe.
    """
    row, column = point
    return row, column


def _start_row(node: Node) -> int:
    """0-based first row of `node`, extended over directly preceding comments and decorators.

    A preceding sibling is attached only if it ends on the row just above and starts
    its own line, so `x = 1  # note` is never pulled into the next symbol.
    """
    start = _pos(node.start_point)[0]
    cur = node
    while True:
        prev = cur.prev_sibling
        if prev is None or prev.type not in _ATTACHABLE:
            break
        prev_start = _pos(prev.start_point)[0]
        if _pos(prev.end_point)[0] + 1 != start:
            break
        before = prev.prev_sibling
        if before is not None and _pos(before.end_point)[0] >= prev_start:
            break
        start = prev_start
        cur = prev
    return start


def _end_row(node: Node) -> int:
    """0-based last row of `node`, ignoring a trailing newline that ends at column 0."""
    row, column = _pos(node.end_point)
    if column == 0 and row > _pos(node.start_point)[0]:
        row -= 1
    return row


def _symbol(name: str, kind: str, wrapper: Node, members: tuple[Symbol, ...] = ()) -> Symbol:
    return Symbol(name=name, kind=kind, start_line=_start_row(wrapper) + 1,
                  end_line=_end_row(wrapper) + 1, members=members)


# ---------------------------------------------------------------- Python

def _py_node(node: Node, owner: str | None) -> Symbol | None:
    inner = node
    if node.type == "decorated_definition":
        inner = node.child_by_field_name("definition")
        if inner is None:
            return None
    name = _text(inner.child_by_field_name("name"))
    if name is None:
        return None

    if inner.type == "function_definition":
        return _symbol(f"{owner}.{name}" if owner else name, "method" if owner else "function", node)
    if inner.type == "class_definition":
        members: tuple[Symbol, ...] = ()
        body = inner.child_by_field_name("body")
        if owner is None and body is not None:
            found = (_py_node(c, name) for c in body.children)
            members = tuple(m for m in found if m is not None and m.kind == "method")
        return _symbol(f"{owner}.{name}" if owner else name, "class", node, members)
    return None


def _python(root: Node) -> list[Symbol]:
    return [s for c in root.children if (s := _py_node(c, None)) is not None]


# ---------------------------------------------------------------- Go

def _go_receiver(node: Node) -> str | None:
    recv = node.child_by_field_name("receiver")
    if recv is None:
        return None
    for param in recv.named_children:
        if param.type == "parameter_declaration":
            text = _text(param.child_by_field_name("type"))
            if text:
                return text.lstrip("*").split("[", 1)[0].strip() or None
    return None


def _go_spec_kind(spec: Node) -> str:
    if spec.type == "type_spec":
        kind = spec.child_by_field_name("type")
        if kind is not None and kind.type == "struct_type":
            return "struct"
        if kind is not None and kind.type == "interface_type":
            return "interface"
    return "type"


def _go(root: Node) -> list[Symbol]:
    out: list[Symbol] = []
    for node in root.children:
        if node.type == "function_declaration":
            name = _text(node.child_by_field_name("name"))
            if name:
                out.append(_symbol(name, "function", node))
        elif node.type == "method_declaration":
            name = _text(node.child_by_field_name("name"))
            if name:
                recv = _go_receiver(node)
                out.append(_symbol(f"{recv}.{name}" if recv else name, "method", node))
        elif node.type == "type_declaration":
            specs = [c for c in node.children if c.type in ("type_spec", "type_alias")]
            named = [(s, _text(s.child_by_field_name("name"))) for s in specs]
            named = [(s, n) for s, n in named if n]
            if len(named) == 1:
                spec, name = named[0]
                out.append(_symbol(name, _go_spec_kind(spec), node))
            else:
                for spec, name in named:
                    out.append(_symbol(name, _go_spec_kind(spec), spec))
    return out


# ---------------------------------------------------------------- TypeScript / TSX / JavaScript

def _js_member(member: Node, owner: str) -> Symbol | None:
    if member.type in ("method_definition", "abstract_method_signature"):
        name = _text(member.child_by_field_name("name"))
    elif member.type in ("public_field_definition", "field_definition"):
        value = member.child_by_field_name("value")
        if value is None or value.type not in _JS_FUNCTION_VALUES:
            return None
        name = _text(member.child_by_field_name("name") or member.child_by_field_name("property"))
    else:
        return None
    if not name:
        return None
    return _symbol(f"{owner}.{name}", "method", member)


def _js_class(inner: Node, name: str, wrapper: Node) -> Symbol:
    members: tuple[Symbol, ...] = ()
    body = inner.child_by_field_name("body")
    if body is not None:
        found = (_js_member(c, name) for c in body.children)
        members = tuple(m for m in found if m is not None)
    return _symbol(name, "class", wrapper, members)


def _js_node(node: Node) -> Symbol | None:
    wrapper, inner, exported = node, node, False
    if node.type == "export_statement":
        inner = node.child_by_field_name("declaration") or node.child_by_field_name("value")
        exported = True
        if inner is None:  # `export { a }`, `export * from "x"`
            return None
    kind = inner.type
    name = _text(inner.child_by_field_name("name"))

    if kind in ("function_declaration", "generator_function_declaration"):
        return _symbol(name, "function", wrapper) if name else None
    if exported and kind in _JS_FUNCTION_VALUES:
        return _symbol(name or "default", "function", wrapper)  # export default function () {}
    if kind in ("class_declaration", "abstract_class_declaration", "class"):
        if kind == "class" and not exported:
            return None
        return _js_class(inner, name or "default", wrapper)
    simple = {"interface_declaration": "interface", "type_alias_declaration": "type", "enum_declaration": "enum",
              "internal_module": "namespace"}
    if kind in simple:
        return _symbol(name, simple[kind], wrapper) if name else None

    if kind in ("lexical_declaration", "variable_declaration"):
        for decl in inner.named_children:
            if decl.type != "variable_declarator":
                continue
            target = decl.child_by_field_name("name")
            value = decl.child_by_field_name("value")
            if target is not None and target.type == "identifier" and value is not None \
                    and value.type in _JS_FUNCTION_VALUES:
                return _symbol(_text(target), "function", wrapper)
        return None

    if kind == "expression_statement" and not exported:
        expr = next((c for c in inner.named_children if c.type != "comment"), None)
        if expr is None:
            return None
        if expr.type == "internal_module":
            ns = _text(expr.child_by_field_name("name"))
            return _symbol(ns, "namespace", wrapper) if ns else None
        if expr.type == "assignment_expression":
            right = expr.child_by_field_name("right")
            left = _text(expr.child_by_field_name("left"))
            if right is not None and right.type in _JS_FUNCTION_VALUES and left \
                    and len(left) <= _MAX_ASSIGN_TARGET and "\n" not in left and " " not in left:
                return _symbol(left, "function", wrapper)
    return None


def _javascript(root: Node) -> list[Symbol]:
    return [s for c in root.children if (s := _js_node(c)) is not None]


_EXTRACTORS = {
    "python": _python,
    "go": _go,
    "javascript": _javascript,
    "typescript": _javascript,
    "tsx": _javascript,
}


def extract(grammar: str, root: Node) -> list[Symbol]:
    """Top-level symbols of a parsed file, in source order."""
    return sorted(_EXTRACTORS[grammar](root), key=lambda s: (s.start_line, s.end_line))
