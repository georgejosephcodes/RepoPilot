"""Print node types of small samples so the chunker's node table matches the real grammars."""
import tree_sitter_go as go
import tree_sitter_javascript as js
import tree_sitter_python as py
import tree_sitter_typescript as ts
from tree_sitter import Language, Parser

SAMPLES = {
    "python": (py.language(), b'''# leading comment
import os

@decorator(1)
def top(a):
    def inner():
        return 1
    return inner

class Foo(Base):
    """doc"""
    x = 1

    @staticmethod
    def method(self):
        pass

    async def amethod(self):
        pass

async def atop():
    pass

VALUE = 3
'''),
    "go": (go.language(), b'''package main

import "fmt"

const Max = 10

var counter int

// Scheduler runs jobs.
type Scheduler struct {
	n int
}

type Runner interface {
	Run() error
}

type (
	A int
	B struct{}
)

type Alias = string

// Run starts it.
func (s *Scheduler) Run(ctx context.Context) error {
	return nil
}

func (s Scheduler) Value() int { return s.n }

func Top() {}

func Generic[T any](x T) T { return x }
'''),
    "typescript": (ts.language_typescript(), b'''import { a } from "b";

// comment above
export function exported(x: number): number {
  return x;
}

function plain() {}

export default function () {}

class Foo extends Bar {
  private x = 1;
  constructor() { super(); }
  method(): void {}
  static s() {}
  get value() { return 1; }
}

export class Baz {}

const arrow = (a: number) => a + 1;
export const arrow2 = async () => {};
const fnExpr = function () {};
let notFn = 5;

interface I { a: number }
type T = { a: number };
enum E { A, B }
namespace N { export const z = 1; }
abstract class Abs { abstract m(): void; }
'''),
    "tsx": (ts.language_tsx(), b'''export const Comp = () => <div>hi</div>;
function Other() { return <span/>; }
'''),
    "javascript": (js.language(), b'''const x = require("x");

// comment above
function top() {}

export function exported() {}

export default function () {}

class Foo extends Bar {
  constructor() { super(); }
  method() {}
  static s() {}
  #priv() {}
  get v() { return 1; }
}

const arrow = () => 1;
export const arrow2 = async (a) => a;
let fnExpr = function () {};
var old = function named() {};
module.exports = { top };
exports.helper = function () {};
Foo.prototype.bar = function () {};
'''),
}

MAX_DEPTH = 3


def walk(node, depth, src):
    if node.is_named and depth <= MAX_DEPTH:
        name = node.child_by_field_name("name")
        label = f" name={name.text.decode()!r}" if name is not None else ""
        first = src[node.start_byte:node.end_byte].split(b"\n")[0][:50].decode(errors="replace")
        print(f"{'  ' * depth}{node.type} [{node.start_point.row + 1}-{node.end_point.row + 1}]{label}  | {first}")
        for child in node.children:
            walk(child, depth + 1, src)
    elif not node.is_named:
        return


for lang, (ptr, src) in SAMPLES.items():
    print(f"\n===== {lang} =====")
    tree = Parser(Language(ptr)).parse(src)
    print("has_error:", tree.root_node.has_error)
    walk(tree.root_node, 0, src)
