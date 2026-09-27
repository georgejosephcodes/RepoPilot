"""Module docstring."""
import os

DEFAULT = 3
LIMIT = 10
NAME = "x"


# Adds numbers.
# Second comment line.
def add(a, b):
    def helper():
        return a
    return a + b + helper()


x = 1  # trailing note
def after_trailing():
    return 2


# separated by a blank line

def separated():
    return 3


@decorator(1)
@other
def decorated(a):
    return a


class Foo(Base):
    """Doc."""
    attr = 1

    @staticmethod
    def static_method():
        return 1

    async def amethod(self):
        return 2

    # note on method
    def method(self):
        return 3


async def coroutine():
    pass
