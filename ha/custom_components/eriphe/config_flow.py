"""Config flow for Eriphe: eriphe-core's URL and token."""

from __future__ import annotations

import logging
from typing import Any

import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.const import CONF_TOKEN, CONF_URL
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.selector import (
    TextSelector,
    TextSelectorConfig,
    TextSelectorType,
)

from .api import (
    BrainUnavailable,
    CannotConnect,
    EripheClient,
    InvalidAuth,
    UnexpectedResponse,
)
from .const import DOMAIN

_LOGGER = logging.getLogger(__name__)

SCHEMA = vol.Schema(
    {
        vol.Required(CONF_URL): TextSelector(
            TextSelectorConfig(type=TextSelectorType.URL)
        ),
        vol.Required(CONF_TOKEN): TextSelector(
            TextSelectorConfig(type=TextSelectorType.PASSWORD)
        ),
    }
)


class EripheConfigFlow(ConfigFlow, domain=DOMAIN):
    """Set up Eriphe, or change its URL and token."""

    VERSION = 1

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Ask for eriphe-core's URL and token."""
        errors: dict[str, str] = {}
        if user_input is not None:
            data, errors = await self._async_check(user_input)
            if not errors:
                self._async_abort_entries_match({CONF_URL: data[CONF_URL]})
                return self.async_create_entry(title="Eriphe", data=data)
        return self.async_show_form(
            step_id="user",
            data_schema=self.add_suggested_values_to_schema(SCHEMA, user_input),
            errors=errors,
        )

    async def async_step_reconfigure(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Change eriphe-core's URL or token."""
        entry = self._get_reconfigure_entry()
        errors: dict[str, str] = {}
        if user_input is not None:
            data, errors = await self._async_check(user_input)
            if not errors:
                return self.async_update_reload_and_abort(entry, data_updates=data)
        return self.async_show_form(
            step_id="reconfigure",
            data_schema=self.add_suggested_values_to_schema(
                SCHEMA, user_input or entry.data
            ),
            errors=errors,
        )

    async def _async_check(
        self, user_input: dict[str, Any]
    ) -> tuple[dict[str, str], dict[str, str]]:
        """Return the cleaned data and the form errors."""
        data = {
            CONF_URL: str(user_input[CONF_URL]).strip().rstrip("/"),
            CONF_TOKEN: str(user_input[CONF_TOKEN]).strip(),
        }
        if not data[CONF_URL].startswith(("http://", "https://")):
            return data, {CONF_URL: "invalid_url"}

        client = EripheClient(
            async_get_clientsession(self.hass), data[CONF_URL], data[CONF_TOKEN]
        )
        try:
            await client.async_validate()
        except CannotConnect:
            return data, {"base": "cannot_connect"}
        except InvalidAuth:
            return data, {"base": "invalid_auth"}
        except BrainUnavailable:
            return data, {"base": "brain_unavailable"}
        except UnexpectedResponse as err:
            _LOGGER.warning("Unexpected answer from eriphe-core: %s", err)
            return data, {"base": "unexpected_response"}
        except Exception:
            _LOGGER.exception("Unexpected error while checking eriphe-core")
            return data, {"base": "unknown"}
        return data, {}
