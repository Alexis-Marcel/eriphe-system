"""Client of eriphe-core's channel endpoints.

Uses aiohttp only (the session comes from Home Assistant), no Home Assistant
import, so it can be tested without Home Assistant installed.
"""

from __future__ import annotations

from collections.abc import AsyncIterator
from dataclasses import dataclass, field
import logging
from typing import Any

import aiohttp

from .const import CHANNEL_PATH, DEFAULT_NAME, OUT_PATH, OUT_WAIT
from .stream import Event, StreamError, iter_events

_LOGGER = logging.getLogger(__name__)

# eriphe-core says a holding sentence within ~10 s and the whole answer can
# last ~60 s: allow 90 s between two reads, and no total limit.
VOICE_TIMEOUT = aiohttp.ClientTimeout(total=None, sock_connect=10, sock_read=90)
VALIDATE_TIMEOUT = aiohttp.ClientTimeout(total=10)
# The outbound request waits up to OUT_WAIT seconds for something to say.
OUT_TIMEOUT = aiohttp.ClientTimeout(
    total=None, sock_connect=10, sock_read=OUT_WAIT + 30
)


class EripheError(Exception):
    """Base error of the Eriphe client."""


class CannotConnect(EripheError):
    """eriphe-core cannot be reached, or the connection broke."""


class InvalidAuth(EripheError):
    """eriphe-core refused the token."""


class BrainUnavailable(EripheError):
    """eriphe-core is up but its brain is not available."""


class UnexpectedResponse(EripheError):
    """eriphe-core answered something this client does not understand."""


class EripheClient:
    """Talk to eriphe-core."""

    def __init__(self, session: aiohttp.ClientSession, url: str, token: str) -> None:
        """Keep the session, the base URL and the token."""
        self._session = session
        self._url = url.rstrip("/") + CHANNEL_PATH
        self._out_url = url.rstrip("/") + OUT_PATH
        self._headers = {"Authorization": f"Bearer {token}"}

    async def async_validate(self) -> str:
        """Check the URL and the token, and return the assistant's name.

        An empty text is answered 400 once the token is accepted, so 400 means
        "reachable and authorized".
        """
        try:
            async with self._session.post(
                self._url,
                json={"text": ""},
                headers=self._headers,
                timeout=VALIDATE_TIMEOUT,
            ) as resp:
                if resp.status != 400:
                    await _raise_for_status(resp)
                    raise UnexpectedResponse(f"unexpected status {resp.status}")
        except (aiohttp.ClientError, TimeoutError) as err:
            raise CannotConnect(str(err) or type(err).__name__) from err
        return await self.async_name()

    async def async_name(self) -> str:
        """Return what the user calls their assistant, or Eriphe.

        An eriphe-core that fails to give it leaves the default.
        """
        try:
            async with self._session.get(
                self._url, headers=self._headers, timeout=VALIDATE_TIMEOUT
            ) as resp:
                if resp.status != 200:
                    return DEFAULT_NAME
                body = await resp.json(content_type=None)
        except (aiohttp.ClientError, TimeoutError, ValueError) as err:
            _LOGGER.debug("No assistant name from eriphe-core: %s", err)
            return DEFAULT_NAME
        name = body.get("name") if isinstance(body, dict) else None
        if isinstance(name, str) and name.strip():
            return name.strip()[:32]
        return DEFAULT_NAME

    async def async_voice(self, body: dict[str, Any]) -> AsyncIterator[Event]:
        """Send a sentence and yield the answer's events as they arrive."""
        try:
            async with self._session.post(
                self._url,
                json=body,
                headers=self._headers,
                timeout=VOICE_TIMEOUT,
            ) as resp:
                await _raise_for_status(resp)
                async for event in iter_events(resp.content.iter_any()):
                    yield event
        except (aiohttp.ClientError, TimeoutError) as err:
            raise CannotConnect(str(err) or type(err).__name__) from err
        except StreamError as err:
            raise UnexpectedResponse(str(err)) from err

    async def async_out(
        self, places: list[str], here: list[str] | None = None
    ) -> list[Message]:
        """Wait for what eriphe-core has to say in a room.

        ``places`` are the areas this house can speak in; ``here`` those of
        them where someone is now (an occupancy, presence or motion sensor
        on), where eriphe-core's voice line may speak. Returns as soon as
        there is something, or after about OUT_WAIT seconds with an empty
        list. Malformed items are left out.
        """
        try:
            async with self._session.get(
                self._out_url,
                params={
                    "wait": str(OUT_WAIT),
                    "places": ",".join(places),
                    "here": ",".join(here or []),
                },
                headers=self._headers,
                timeout=OUT_TIMEOUT,
            ) as resp:
                await _raise_for_status(resp)
                body = await resp.json(content_type=None)
        except (aiohttp.ClientError, TimeoutError) as err:
            raise CannotConnect(str(err) or type(err).__name__) from err
        except ValueError as err:
            raise UnexpectedResponse(str(err)) from err
        items = body.get("messages") if isinstance(body, dict) else None
        if not isinstance(items, list):
            raise UnexpectedResponse("no messages in the answer")
        return [m for item in items if (m := Message.parse(item))]


@dataclass(frozen=True)
class Message:
    """One sentence to say, written by eriphe-core, and where."""

    text: str
    place: str = ""

    @classmethod
    def parse(cls, item: Any) -> Message | None:
        """Read one item; None if it is not one."""
        if not isinstance(item, dict):
            return None
        text, place = item.get("text"), item.get("place", "")
        if not isinstance(text, str) or not text.strip() or not isinstance(place, str):
            return None
        return cls(text.strip(), place.strip())


@dataclass
class Speakers:
    """The house's speakers by area: satellites first, then media players."""

    by_area: dict[str, list[str]] = field(default_factory=dict)
    tts: list[str] = field(default_factory=list)

    def add(self, area: str, entity_id: str) -> None:
        """Keep a speaker of an area."""
        domain = entity_id.partition(".")[0]
        if domain not in ("assist_satellite", "media_player"):
            return
        if domain == "media_player" and not self.tts:
            return
        self.by_area.setdefault(area, []).append(entity_id)
        self.by_area[area].sort(
            key=lambda e: (not e.startswith("assist_satellite."), e)
        )

    def places(self) -> list[str]:
        """The areas this house can speak in."""
        return sorted(self.by_area)

    def service_call(self, message: Message) -> tuple[str, str, dict[str, Any]] | None:
        """The service that says a message in its place, or None.

        A satellite announces; a media player is spoken to by the house's
        first text-to-speech entity. Only a speaker of that area is ever
        called, whatever eriphe-core sends; without a place, only a house
        with one area of speakers speaks.
        """
        areas = [a for a in self.by_area if a.casefold() == message.place.casefold()]
        if not message.place and len(self.by_area) == 1:
            areas = list(self.by_area)
        if not areas:
            return None
        entity_id = self.by_area[areas[0]][0]
        if entity_id.startswith("assist_satellite."):
            return (
                "assist_satellite",
                "announce",
                {"entity_id": entity_id, "message": message.text},
            )
        return (
            "tts",
            "speak",
            {
                "entity_id": sorted(self.tts)[0],
                "media_player_entity_id": entity_id,
                "message": message.text,
            },
        )


async def _raise_for_status(resp: aiohttp.ClientResponse) -> None:
    """Raise the error matching a non-200 status."""
    if resp.status == 200:
        return
    message = await _error_message(resp)
    if resp.status in (401, 403):
        raise InvalidAuth(message)
    if resp.status == 503:
        raise BrainUnavailable(message)
    raise UnexpectedResponse(f"status {resp.status}: {message}")


async def _error_message(resp: aiohttp.ClientResponse) -> str:
    try:
        body = await resp.json(content_type=None)
    except (aiohttp.ClientError, ValueError):
        return resp.reason or ""
    if isinstance(body, dict) and isinstance(error := body.get("error"), str):
        return error
    return resp.reason or ""
