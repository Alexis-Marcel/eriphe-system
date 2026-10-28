"""Eriphe: hand Home Assistant's conversations to eriphe-core."""

from __future__ import annotations

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_TOKEN, CONF_URL, Platform
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import EripheClient

PLATFORMS = [Platform.CONVERSATION]

type EripheConfigEntry = ConfigEntry[EripheClient]


async def async_setup_entry(hass: HomeAssistant, entry: EripheConfigEntry) -> bool:
    """Set up Eriphe from a config entry."""
    entry.runtime_data = EripheClient(
        async_get_clientsession(hass), entry.data[CONF_URL], entry.data[CONF_TOKEN]
    )
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: EripheConfigEntry) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
