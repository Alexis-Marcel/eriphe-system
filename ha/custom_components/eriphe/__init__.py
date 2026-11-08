"""Eriphe: hand Home Assistant's conversations to eriphe-core."""

from __future__ import annotations

import asyncio
import logging

import voluptuous as vol

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_TOKEN, CONF_URL, Platform
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import (
    area_registry as ar,
    device_registry as dr,
    entity_registry as er,
)
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import EripheClient, EripheError, InvalidAuth, NotSupported, Speakers

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
    """Say in the house's rooms what eriphe-core has to say, for ever.

    The channel's outbound side: one request kept open on eriphe-core with
    the channel's token, telling the areas this house can speak in; each
    sentence comes with its area, said by a speaker there. eriphe-core never
    calls Home Assistant.
    """
    wait = RETRY_MIN
    while True:
        speakers = async_speakers(hass)
        try:
            messages = await client.async_out(speakers.places())
        except NotSupported:
            await asyncio.sleep(RETRY_NOT_SUPPORTED)
            continue
        except EripheError as err:
            level = logging.WARNING if isinstance(err, InvalidAuth) else logging.DEBUG
            _LOGGER.log(level, "Eriphe's channel: %s", err)
            await asyncio.sleep(wait)
            wait = min(wait * 2, RETRY_MAX)
            continue
        wait = RETRY_MIN
        speakers = async_speakers(hass)
        for message in messages:
            if (call := speakers.service_call(message)) is None:
                _LOGGER.debug(
                    "Eriphe asked to speak in %r, which has no speaker", message.place
                )
                continue
            domain, service, data = call
            try:
                await hass.services.async_call(domain, service, data)
            except (HomeAssistantError, vol.Invalid) as err:
                _LOGGER.warning("Eriphe could not speak in %s: %s", message.place, err)


@callback
def async_speakers(hass: HomeAssistant) -> Speakers:
    """The house's satellites and media players, by the name of their area."""
    speakers = Speakers(tts=hass.states.async_entity_ids("tts"))
    entities = er.async_get(hass)
    devices = dr.async_get(hass)
    areas = ar.async_get(hass)
    for entity_id in hass.states.async_entity_ids(("assist_satellite", "media_player")):
        entry = entities.async_get(entity_id)
        area_id = entry.area_id if entry else None
        if not area_id and entry and entry.device_id:
            device = devices.async_get(entry.device_id)
            area_id = device.area_id if device else None
        if area_id and (area := areas.async_get_area(area_id)):
            speakers.add(area.name, entity_id)
    return speakers
