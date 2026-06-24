# Python Style Guide

Based on the Google Python Style Guide, updated for modern Python (3.11+)
and current tooling standards.

---

## 1. Tooling Setup

Use **ruff** for linting + formatting (replaces pylint, flake8, isort, black
— one tool does all of it, and it's ~100× faster). Use **mypy** for static
type checking.

### `pyproject.toml` (project root)
```toml
[tool.ruff]
line-length = 88
target-version = "py311"

[tool.ruff.lint]
select = [
    "E",   # pycodestyle errors
    "W",   # pycodestyle warnings
    "F",   # pyflakes
    "I",   # isort
    "B",   # flake8-bugbear
    "C4",  # flake8-comprehensions
    "UP",  # pyupgrade (modernize syntax)
    "N",   # pep8 naming
    "RUF", # ruff-specific rules
]
ignore = [
    "E501",  # line length handled by formatter
]

[tool.ruff.lint.per-file-ignores]
"tests/*" = ["S101"]  # allow assert in tests

[tool.ruff.format]
quote-style = "double"
indent-style = "space"

[tool.mypy]
python_version = "3.11"
strict = true
ignore_missing_imports = true
```

CI commands:
```bash
ruff check .     # lint
ruff format .    # format
mypy .           # type check
pytest           # tests
```

---

## 2. Formatting

- **Indentation:** 4 spaces. Never tabs.
- **Line length:** 88 characters (ruff/black default — a practical update
  from the old 80-char limit).
- **Blank lines:**
  - 2 blank lines between top-level definitions (classes, functions).
  - 1 blank line between method definitions inside a class.
- **Trailing comma** in multi-line function signatures and data structures.
  This keeps diffs clean when adding/removing items.
  ```python
  # ✅ — adding a new arg only changes one line in git diff
  def create_user(
      name: str,
      email: str,
      role: str = "viewer",
  ) -> User:
  ```

---

## 3. Imports

Three groups, separated by blank lines, sorted within each group (ruff/isort
handles this automatically):

```python
# 1. stdlib
import os
from pathlib import Path

# 2. third-party
import httpx
from pydantic import BaseModel

# 3. internal / first-party
from myapp.config import settings
from myapp.models import User
```

- Use `import x` for packages/modules.
- Use `from x import y` only when `y` is a class, function, or constant —
  not a submodule.
- Never use wildcard imports (`from x import *`).

---

## 4. Type Annotations

Type annotations are **required** for all public APIs. For internal helpers,
they're strongly encouraged.

```python
# ✅ — fully annotated
def get_user(user_id: str, *, include_deleted: bool = False) -> User | None:
    ...

# ❌ — no annotations on public function
def get_user(user_id, include_deleted=False):
    ...
```

Modern syntax (Python 3.10+):
- Use `X | Y` instead of `Union[X, Y]`.
- Use `X | None` instead of `Optional[X]`.
- Use `list[str]` instead of `List[str]` (lowercase built-ins).

```python
# ✅ modern
def process(items: list[str]) -> dict[str, int] | None:
    ...

# ❌ old-style
from typing import Dict, List, Optional, Union
def process(items: List[str]) -> Optional[Dict[str, int]]:
    ...
```

---

## 5. Naming

| Kind | Convention | Example |
|---|---|---|
| Modules, packages | `snake_case` | `user_service.py` |
| Functions, methods | `snake_case` | `get_user_by_id` |
| Variables | `snake_case` | `user_count` |
| Classes | `PascalCase` | `UserService` |
| Constants | `ALL_CAPS` | `MAX_RETRIES = 3` |
| Internal (module/class) | `_single_leading_underscore` | `_cache` |
| Name mangling (class only) | `__double_leading` | `__private_key` |

- Avoid abbreviations unless standard (`ctx`, `cfg`, `db`, `err`, `req`, `res`).
- Boolean names: `is_*`, `has_*`, `can_*`, `should_*`.

---

## 6. Docstrings

Required for all public modules, classes, and functions. Use Google-style
docstrings (consistent with ruff's `D` rules):

```python
def send_notification(user_id: str, message: str, *, urgent: bool = False) -> bool:
    """Send a push notification to the specified user.

    Args:
        user_id: The unique identifier of the recipient.
        message: Notification body text. Max 256 characters.
        urgent: If True, bypasses the rate limiter.

    Returns:
        True if the notification was delivered, False if queued for retry.

    Raises:
        UserNotFoundError: If user_id does not correspond to an active user.
        NotificationError: If the push service is unreachable.
    """
```

- First line is a one-sentence summary ending with a period.
- Subsequent sections (`Args`, `Returns`, `Raises`) only when applicable.
- Don't include types in docstrings — they're in the annotations.

---

## 7. Error Handling

- Use built-in exception types when they fit (`ValueError`, `TypeError`,
  `KeyError`, `RuntimeError`).
- Create custom exceptions for domain errors:
  ```python
  class UserNotFoundError(Exception):
      """Raised when a user ID does not match any record."""
  ```
- **Never use bare `except:`** — catches `SystemExit`, `KeyboardInterrupt`, etc.
  Minimum: `except Exception:`.
- Include context when re-raising:
  ```python
  try:
      user = db.get_user(user_id)
  except DatabaseError as e:
      raise UserNotFoundError(f"user {user_id} not found") from e
  ```

---

## 8. Classes & Data

- Use **`dataclasses`** or **`pydantic`** for data-holding classes. Don't
  write `__init__` by hand unless you need custom initialization logic.
  ```python
  from dataclasses import dataclass, field

  @dataclass
  class Config:
      host: str
      port: int = 8080
      tags: list[str] = field(default_factory=list)
  ```
- Avoid mutable default arguments:
  ```python
  # ❌ — the list is shared across all calls
  def append_item(item: str, container: list[str] = []) -> list[str]:
      ...

  # ✅
  def append_item(item: str, container: list[str] | None = None) -> list[str]:
      if container is None:
          container = []
      ...
  ```

---

## 9. Booleans & Truthiness

- `if not my_list:` — use implicit truthiness for empty containers.
- `if x is None:` / `if x is not None:` — always use `is` for `None` checks.
  Never `if x == None:`.
- Avoid comparing booleans explicitly:
  ```python
  if is_active:          # ✅
  if is_active == True:  # ❌
  ```

---

## 10. Main

```python
def main() -> None:
    """Entry point."""
    ...

if __name__ == '__main__':
    main()
```

All executable scripts use this pattern. `main()` contains the program logic;
the guard prevents execution on import.

*Primary source: [Google Python Style Guide](https://google.github.io/styleguide/pyguide.html)*
