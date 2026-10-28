"""Conversation agent that hands sentences to eriphe-core and streams its answer."""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import aclosing
from dataclasses import dataclass
import logging
from typing import Literal

from homeassistant.components import conversation
from homeassistant.const import MATCH_ALL
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers import (
    area_registry as ar,
    device_registry as dr,
    entity_registry as er,
)
from homeassistant.helpers.device_registry import DeviceEntryType, DeviceInfo
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import EripheConfigEntry
from .api import EripheError
from .const import DOMAIN, FALLBACK_SPEECH, INTERRUPTED_SPEECH
from .stream import Delta, build_request

_LOGGER = logging.getLogger(__name__)

PARALLEL_UPDATES = 0


async def async_setup_entry(
    hass: HomeAssistant,
    entry: EripheConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Set up the Eriphe conversation agent."""
    async_add_entities([EripheConversationEntity(entry)])


@dataclass
class _Outcome:
    """What the stream told about the end of the answer."""

    continue_conversation: bool | None = None


class EripheConversationEntity(conversation.ConversationEntity):
    """Eriphe conversation agent.

    It has no CONTROL feature: Eriphe acts on the house itself through Home
    Assistant's MCP Server, so Home Assistant must not hand it an LLM API.
    """

    _attr_has_entity_name = True
    _attr_name = None
    _attr_supports_streaming = True

    def __init__(self, entry: EripheConfigEntry) -> None:
        """Initialize the agent."""
        self._entry = entry
        self._attr_unique_id = entry.entry_id
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, entry.entry_id)},
            name=entry.title,
            manufacturer="Eriphe",
            entry_type=DeviceEntryType.SERVICE,
        )

    @property
    def supported_languages(self) -> list[str] | Literal["*"]:
        """Return a list of supported languages."""
        return MATCH_ALL

    async def _async_handle_message(
        self,
        user_input: conversation.ConversationInput,
        chat_log: conversation.ChatLog,
    ) -> conversation.ConversationResult:
        """Send the sentence to eriphe-core and stream its answer into the chat log."""
        body = build_request(
            user_input.text,
            language=user_input.language,
            conversation_id=chat_log.conversation_id,
            device_id=user_input.device_id,
            satellite_id=user_input.satellite_id,
            area=_async_resolve_area(
                self.hass, user_input.device_id, user_input.satellite_id
            ),
        )
        outcome = _Outcome()
        async for _content in chat_log.async_add_delta_content_stream(
            self.entity_id, self._async_answer(body, outcome)
        ):
            pass

        result = conversation.async_get_result_from_chat_log(user_input, chat_log)
        # eriphe-core says whether its answer asks something; Home Assistant
        # would only guess it from a trailing question mark.
        if outcome.continue_conversation is not None:
            result.continue_conversation = outcome.continue_conversation
        return result

    async def _async_answer(
        self, body: dict[str, str], outcome: _Outcome
    ) -> AsyncIterator[conversation.AssistantContentDeltaDict]:
        """Yield the answer's deltas, or a short sentence when eriphe-core fails."""
        yield {"role": "assistant"}
        said = ""
        try:
            async with aclosing(self._entry.runtime_data.async_voice(body)) as events:
                async for event in events:
                    if isinstance(event, Delta):
                        if event.text:
                            said += event.text
                            yield {"content": event.text}
                        continue
                    # The done line is the last event of the stream.
                    outcome.continue_conversation = event.continue_conversation
                    if not said:
                        # No delta: the whole answer is in the done line. An
                        # empty content is kept as a silent answer.
                        yield {"content": event.text}
                    elif event.text and event.text != said:
                        _LOGGER.debug("Final text differs from the deltas, kept")
        except EripheError as err:
            _LOGGER.warning("Eriphe could not answer: %s", err)
            yield {"content": INTERRUPTED_SPEECH if said else FALLBACK_SPEECH}


@callback
def _async_resolve_area(
    hass: HomeAssistant, device_id: str | None, satellite_id: str | None
) -> str | None:
    """Return the name of the area the sentence came from, if known.

    The satellite entity's own area wins, then its device's, then the area of
    the device Home Assistant passed.
    """
    area_id: str | None = None
    device_ids: list[str] = []
    if satellite_id and (entity := er.async_get(hass).async_get(satellite_id)):
        area_id = entity.area_id
        if entity.device_id:
            device_ids.append(entity.device_id)
    if device_id:
        device_ids.append(device_id)

    device_registry = dr.async_get(hass)
    for candidate in device_ids:
        if area_id:
            break
        if device := device_registry.async_get(candidate):
            area_id = device.area_id

    if area_id and (area := ar.async_get(hass).async_get_area(area_id)):
        return area.name
    return None
