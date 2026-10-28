"""Fixtures for the tests that need Home Assistant.

They run only where pytest-homeassistant-custom-component is installed, and
are skipped otherwise.
"""

from pathlib import Path
import sys

import pytest

try:
    import pytest_homeassistant_custom_component  # noqa: F401
except ImportError:
    collect_ignore_glob = ["test_*.py"]
else:
    # Make custom_components.eriphe importable.
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

    @pytest.fixture(autouse=True)
    def auto_enable_custom_integrations(enable_custom_integrations):
        """Load the integration from custom_components."""
        return
