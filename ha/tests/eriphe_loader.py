"""Load the integration's Home Assistant-free modules without Home Assistant.

Importing custom_components.eriphe would run its __init__.py, which imports
Home Assistant. Instead, register an empty package pointing at the
integration's folder, so its modules (and their relative imports) load alone.
"""

import importlib
from pathlib import Path
import sys
import types

PACKAGE = "eriphe_under_test"
_DIR = Path(__file__).resolve().parent.parent / "custom_components" / "eriphe"

if PACKAGE not in sys.modules:
    _package = types.ModuleType(PACKAGE)
    _package.__path__ = [str(_DIR)]
    sys.modules[PACKAGE] = _package


def load(name: str) -> types.ModuleType:
    """Import one module of the integration."""
    return importlib.import_module(f"{PACKAGE}.{name}")
