"""Python mirror of api/internal/repos/url.go.

Both validators are tested against testdata/urls.json so they cannot drift.
The worker re-validates because it is the component that runs `git clone`.
"""

import re
from dataclasses import dataclass

MAX_URL_LEN = 200

# GitHub usernames: 1-39 chars, alphanumeric or inner hyphens.
_OWNER = re.compile(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?")
_NAME = re.compile(r"[A-Za-z0-9._-]{1,100}")
_ASCII_SPACE = " \t\n\v\f\r"


class InvalidURL(ValueError):
    pass


@dataclass(frozen=True)
class RepoRef:
    owner: str
    name: str
    url: str  # canonical, rebuilt from validated parts


def parse_github_url(raw: str) -> RepoRef:
    """Accept only https://github.com/<owner>/<repo> (optional trailing .git and /)."""
    s = raw.strip(_ASCII_SPACE)
    if not s:
        raise InvalidURL("empty")
    if len(s) > MAX_URL_LEN:
        raise InvalidURL("too long")
    # printable ASCII only: blocks control chars, spaces, unicode lookalikes
    if any(not (0x21 <= ord(c) <= 0x7E) for c in s):
        raise InvalidURL("contains a disallowed character")
    if any(c in s for c in "%\\?#"):
        raise InvalidURL("contains a disallowed character")

    if s[:8].lower() != "https://":
        raise InvalidURL("scheme must be https")
    authority, slash, rest = s[8:].partition("/")
    # any userinfo ("@") or port (":") makes this differ from plain github.com
    if authority.lower() != "github.com":
        raise InvalidURL("host must be github.com")

    path = (slash + rest).removesuffix("/").removesuffix(".git")
    parts = path.split("/")
    if len(parts) != 3 or parts[0] != "":
        raise InvalidURL("path must be /<owner>/<repo>")
    owner, name = parts[1], parts[2]
    if not _OWNER.fullmatch(owner):
        raise InvalidURL("invalid owner")
    if not _NAME.fullmatch(name) or name in (".", "..") or name.startswith("-"):
        raise InvalidURL("invalid repository name")

    return RepoRef(owner=owner, name=name, url=f"https://github.com/{owner}/{name}")
