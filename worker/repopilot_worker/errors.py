class UserError(Exception):
    """An error whose message is safe to show to users: no paths, no raw tool output."""
