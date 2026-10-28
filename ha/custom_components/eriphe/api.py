"""Client of eriphe-core's voice endpoint.

Uses aiohttp only (the session comes from Home Assistant), no Home Assistant
import, so it can be tested without Home Assistant installed.
"""

from __future__ import annotations

from collections.abc import AsyncIterator
import logging
from typing import Any

import aiohttp

from .const import VOICE_PATH
from .stream import Event, StreamError, iter_events

_LOGGER = logging.getLogger(__name__)

# eriphe-core says a holding sentence within ~10 s and the whole answer can
# last ~60 s: allow 90 s between two reads, and no total limit.
VOICE_TIMEOUT = aiohttp.ClientTimeout(total=None, sock_connect=10, sock_read=90)
VALIDATE_TIMEOUT = aiohttp.ClientTimeout(total=10)


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
        self._url = url.rstrip("/") + VOICE_PATH
        self._headers = {"Authorization": f"Bearer {token}"}

    async def async_validate(self) -> None:
        """Check the URL and the token with a request eriphe-core must refuse.

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
                if resp.status == 400:
                    return
                await _raise_for_status(resp)
                raise UnexpectedResponse(f"unexpected status {resp.status}")
        except (aiohttp.ClientError, TimeoutError) as err:
            raise CannotConnect(str(err) or type(err).__name__) from err

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
