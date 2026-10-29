"""Config flow and conversation agent, inside a test Home Assistant."""

from collections.abc import AsyncIterator
from typing import Any
from unittest.mock import patch

import pytest
from pytest_homeassistant_custom_component.common import MockConfigEntry

from homeassistant import config_entries
from homeassistant.components import conversation
from homeassistant.const import CONF_TOKEN, CONF_URL
from homeassistant.core import Context, HomeAssistant
from homeassistant.data_entry_flow import FlowResultType
from homeassistant.helpers import (
    area_registry as ar,
    device_registry as dr,
    entity_registry as er,
)
from homeassistant.setup import async_setup_component

from custom_components.eriphe.api import CannotConnect, InvalidAuth
from custom_components.eriphe.const import DOMAIN, FALLBACK_SPEECH
from custom_components.eriphe.stream import Delta, Done

URL = "http://192.0.2.2:8800"
VALIDATE = "custom_components.eriphe.api.EripheClient.async_validate"
VOICE = "custom_components.eriphe.api.EripheClient.async_voice"
AGENT = "conversation.eriphe"


@pytest.fixture(autouse=True)
async def setup_core(hass: HomeAssistant) -> None:
    """Set up what conversation needs before Eriphe pulls it in."""
    assert await async_setup_component(hass, "homeassistant", {})


async def test_user_flow(hass: HomeAssistant) -> None:
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": config_entries.SOURCE_USER}
    )
    assert result["type"] is FlowResultType.FORM

    with patch(VALIDATE, side_effect=InvalidAuth("bad token")):
        result = await hass.config_entries.flow.async_configure(
            result["flow_id"], {CONF_URL: URL, CONF_TOKEN: "wrong"}
        )
    assert result["errors"] == {"base": "invalid_auth"}

    with patch(VALIDATE, side_effect=CannotConnect("refused")):
        result = await hass.config_entries.flow.async_configure(
            result["flow_id"], {CONF_URL: URL, CONF_TOKEN: "secret"}
        )
    assert result["errors"] == {"base": "cannot_connect"}

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_URL: "192.0.2.2:8800", CONF_TOKEN: "secret"}
    )
    assert result["errors"] == {CONF_URL: "invalid_url"}

    # The entry takes the name the user gave their assistant.
    with patch(VALIDATE, return_value="Jarvis"):
        result = await hass.config_entries.flow.async_configure(
            result["flow_id"], {CONF_URL: URL + "/", CONF_TOKEN: " secret "}
        )
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["title"] == "Jarvis"
    assert result["data"] == {CONF_URL: URL, CONF_TOKEN: "secret"}


async def test_reconfigure(hass: HomeAssistant) -> None:
    entry = MockConfigEntry(
        domain=DOMAIN, title="Eriphe", data={CONF_URL: URL, CONF_TOKEN: "old"}
    )
    entry.add_to_hass(hass)
    result = await entry.start_reconfigure_flow(hass)
    assert result["type"] is FlowResultType.FORM
    with patch(VALIDATE, return_value="Jarvis"):
        result = await hass.config_entries.flow.async_configure(
            result["flow_id"], {CONF_URL: "http://192.0.2.3:8800", CONF_TOKEN: "new"}
        )
    assert result["type"] is FlowResultType.ABORT
    assert result["reason"] == "reconfigure_successful"
    assert entry.data == {CONF_URL: "http://192.0.2.3:8800", CONF_TOKEN: "new"}
    # Its name follows the one given in eriphe-core since.
    assert entry.title == "Jarvis"


@pytest.fixture
async def entry(hass: HomeAssistant) -> MockConfigEntry:
    entry = MockConfigEntry(
        domain=DOMAIN, title="Eriphe", data={CONF_URL: URL, CONF_TOKEN: "secret"}
    )
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    return entry


def _fake_voice(events: list[Any], sent: list[dict[str, Any]]):
    async def voice(self, body: dict[str, Any]) -> AsyncIterator[Any]:
        sent.append(body)
        for event in events:
            if isinstance(event, Exception):
                raise event
            yield event

    return voice


async def test_streamed_answer(hass: HomeAssistant, entry: MockConfigEntry) -> None:
    device_registry = dr.async_get(hass)
    area = ar.async_get(hass).async_create("Bureau")
    device = device_registry.async_get_or_create(
        config_entry_id=entry.entry_id, identifiers={("test", "satellite")}
    )
    device_registry.async_update_device(device.id, area_id=area.id)

    sent: list[dict[str, Any]] = []
    events = [
        Delta("Il fait 20 degrés."),
        Delta(" Tu veux autre chose"),
        Done("Il fait 20 degrés. Tu veux autre chose", continue_conversation=True),
    ]
    with patch(VOICE, _fake_voice(events, sent)):
        result = await conversation.async_converse(
            hass,
            "Quelle température ?",
            None,
            Context(),
            language="fr",
            agent_id=AGENT,
            device_id=device.id,
        )

    assert result.response.speech["plain"]["speech"] == (
        "Il fait 20 degrés. Tu veux autre chose"
    )
    assert result.continue_conversation is True
    assert sent == [
        {
            "text": "Quelle température ?",
            "language": "fr",
            "conversation_id": result.conversation_id,
            "device_id": device.id,
            "area": "Bureau",
        }
    ]


async def test_area_of_the_satellite(
    hass: HomeAssistant, entry: MockConfigEntry
) -> None:
    area = ar.async_get(hass).async_create("Cuisine")
    satellite = er.async_get(hass).async_get_or_create(
        "assist_satellite", "test", "kitchen", suggested_object_id="kitchen"
    )
    er.async_get(hass).async_update_entity(satellite.entity_id, area_id=area.id)

    sent: list[dict[str, Any]] = []
    with patch(VOICE, _fake_voice([Done("Oui.", False)], sent)):
        await conversation.async_converse(
            hass,
            "Allume",
            None,
            Context(),
            language="fr",
            agent_id=AGENT,
            satellite_id=satellite.entity_id,
        )
    assert sent[0]["satellite_id"] == "assist_satellite.kitchen"
    assert sent[0]["area"] == "Cuisine"


async def test_done_only_and_no_continue(
    hass: HomeAssistant, entry: MockConfigEntry
) -> None:
    events = [Done("Tu es sûr ?", continue_conversation=False)]
    with patch(VOICE, _fake_voice(events, [])):
        result = await conversation.async_converse(
            hass, "Éteins tout", None, Context(), language="fr", agent_id=AGENT
        )
    assert result.response.speech["plain"]["speech"] == "Tu es sûr ?"
    # eriphe-core's flag wins over the trailing question mark.
    assert result.continue_conversation is False


async def test_unreachable(hass: HomeAssistant, entry: MockConfigEntry) -> None:
    with patch(VOICE, _fake_voice([CannotConnect("refused")], [])):
        result = await conversation.async_converse(
            hass, "Bonjour", None, Context(), language="fr", agent_id=AGENT
        )
    assert result.response.speech["plain"]["speech"] == FALLBACK_SPEECH.format(
        name="Eriphe"
    )
    assert result.continue_conversation is False


async def test_no_control_feature(hass: HomeAssistant, entry: MockConfigEntry) -> None:
    state = hass.states.get(AGENT)
    assert state is not None
    assert state.attributes.get("supported_features", 0) == 0
    info = conversation.async_get_agent_info(hass, AGENT)
    assert info is not None and info.supports_streaming
