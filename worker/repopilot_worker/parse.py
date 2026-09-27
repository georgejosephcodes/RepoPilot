"""tree-sitter grammars and parsers.

Parsers are cached per grammar. A tree-sitter Parser is not thread-safe; the worker
processes one job at a time on one thread.
"""

from functools import lru_cache

import tree_sitter_go
import tree_sitter_javascript
import tree_sitter_python
import tree_sitter_typescript
from tree_sitter import Language, Parser, Tree

_LANGUAGES = {
    "python": tree_sitter_python.language,
    "go": tree_sitter_go.language,
    "javascript": tree_sitter_javascript.language,  # also handles JSX
    "typescript": tree_sitter_typescript.language_typescript,
    "tsx": tree_sitter_typescript.language_tsx,
}


def grammar_for(language: str, rel_path: str) -> str | None:
    """Grammar name for a scanner language, or None when we have no grammar (Markdown, ...)."""
    if language == "typescript":
        return "tsx" if rel_path.lower().endswith(".tsx") else "typescript"
    if language in ("python", "go", "javascript"):
        return language
    return None


@lru_cache(maxsize=None)
def _parser(grammar: str) -> Parser:
    return Parser(Language(_LANGUAGES[grammar]()))


def parse(grammar: str, source: bytes) -> Tree:
    return _parser(grammar).parse(source)
