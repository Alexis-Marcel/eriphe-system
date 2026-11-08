"""Eriphe: hand Home Assistant's conversations to eriphe-core."""

from __future__ import annotations

import asyncio
import logging

import voluptuous as vol

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_TOKEN, CONF_URL, Platform
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import EripheClient, EripheError, InvalidAuth, NotSupported, service_call

_LOGGER = logging.getLogger(__name__)

PLATFORMS = [Platform.CONVERSATION]

# Waits before asking eriphe-core again after a failure, growing to the max;
# an eriphe-core older than the speakers is asked again rarely.
RETRY_MIN = 5
RETRY_MAX = 300
RETRY_NOT_SUPPORTED = 3600

type EripheConfigEntry = ConfigEntry[EripheClient]


async def async_setup_entry(hass: HomeAssistant, entry: EripheConfigEntry) -> bool:
    """Set up Eriphe from a config entry."""
    entry.runtime_data = EripheClient(
        async_get_clientsession(hass), entry.data[CONF_URL], entry.data[CONF_TOKEN]
    )
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    entry.async_create_background_task(
        hass, async_speak(hass, entry.runtime_data), "eriphe speakers"
    )
    return True


async def async_unload_entry(hass: HomeAssistant, entry: EripheConfigEntry) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)


async def async_speak(hass: HomeAssistant, client: EripheClient) -> None:
    """Say on the house's speakers what eriphe-core has to say, for ever.

    The voice's outbound side: one request kept open on eriphe-core with the
    voice's token; eriphe-core never calls Home Assistant to speak.
    """
    wait = RETRY_MIN
    while True:
        try:
            announcements = await client.async_announcements()
        except NotSupported:
            await asyncio.sleep(RETRY_NOT_SUPPORTED)
            continue
        except EripheError as err:
            level = logging.WARNING if isinstance(err, InvalidAuth) else logging.DEBUG
            _LOGGER.log(level, "Eriphe's speakers: %s", err)
            await asyncio.sleep(wait)
            wait = min(wait * 2, RETRY_MAX)
            continue
        wait = RETRY_MIN
        tts = hass.states.async_entity_ids("tts")
        for announcement in announcements:
            if (call := service_call(announcement, tts)) is None:
                _LOGGER.debug("Eriphe asked to speak on %s", announcement.entity)
                continue
            domain, service, data = call
            try:
                await hass.services.async_call(domain, service, data)
            except (HomeAssistantError, vol.Invalid) as err:
                _LOGGER.warning(
                    "Eriphe could not speak on %s: %s", announcement.entity, err
                )
