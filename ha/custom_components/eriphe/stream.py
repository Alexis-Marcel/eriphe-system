"""Request body and NDJSON answer stream of eriphe-core's voice endpoint.

This module must not import Home Assistant: it is unit tested on its own.
"""

from __future__ import annotations

from collections.abc import AsyncIterable, AsyncIterator
from dataclasses import dataclass
import json
from typing import Any


class StreamError(Exception):
    """The answer stream is malformed or ended before its last line."""


@dataclass(frozen=True, slots=True)
class Delta:
    """A piece of the answer, to append to what was already said."""

    text: str


@dataclass(frozen=True, slots=True)
class Done:
    """The last line of the stream."""

    text: str
    continue_conversation: bool


Event = Delta | Done


def build_request(
    text: str,
    *,
    language: str | None = None,
    conversation_id: str | None = None,
    device_id: str | None = None,
    satellite_id: str | None = None,
    area: str | None = None,
) -> dict[str, str]:
    """Return the body of a voice request, without its empty fields."""
    body = {
        "text": text,
        "language": language,
        "conversation_id": conversation_id,
        "device_id": device_id,
        "satellite_id": satellite_id,
        "area": area,
    }
    return {key: value for key, value in body.items() if value}


class NdjsonDecoder:
    """Split a byte stream into JSON objects, one per line.

    Chunks may cut a line (or a UTF-8 character) anywhere: the unfinished tail
    is kept until the next chunk. Blank lines are ignored.
    """

    def __init__(self) -> None:
        """Start with an empty buffer."""
        self._buffer = bytearray()

    def feed(self, chunk: bytes) -> list[dict[str, Any]]:
        """Add a chunk and return the objects of the lines it completed."""
        self._buffer.extend(chunk)
        *lines, tail = self._buffer.split(b"\n")
        self._buffer = bytearray(tail)
        return [obj for line in lines if (obj := _decode_line(line)) is not None]

    def flush(self) -> list[dict[str, Any]]:
        """Return the object of a last line that had no trailing newline."""
        line, self._buffer = bytes(self._buffer), bytearray()
        obj = _decode_line(line)
        return [] if obj is None else [obj]


def _decode_line(line: bytes) -> dict[str, Any] | None:
    line = line.strip()
    if not line:
        return None
    try:
        obj = json.loads(line.decode("utf-8"))
    except (UnicodeDecodeError, ValueError) as err:
        raise StreamError(f"invalid line in the answer stream: {line[:200]!r}") from err
    if not isinstance(obj, dict):
        raise StreamError(f"expected a JSON object, got: {line[:200]!r}")
    return obj


def parse_event(obj: dict[str, Any]) -> Event | None:
    """Return the event of one line, or None for a line this client ignores."""
    if obj.get("done") is True:
        text = obj.get("text")
        return Done(
            text=text if isinstance(text, str) else "",
            continue_conversation=obj.get("continue") is True,
        )
    if isinstance(delta := obj.get("delta"), str):
        return Delta(delta)
    if "error" in obj:
        raise StreamError(str(obj["error"]))
    return None


async def iter_events(chunks: AsyncIterable[bytes]) -> AsyncIterator[Event]:
    """Yield the deltas of an answer stream, then its Done event.

    Raise StreamError if the stream ends without a done line.
    """
    decoder = NdjsonDecoder()
    async for chunk in chunks:
        for obj in decoder.feed(chunk):
            if (event := parse_event(obj)) is None:
                continue
            yield event
            if isinstance(event, Done):
                return
    for obj in decoder.flush():
        if (event := parse_event(obj)) is None:
            continue
        yield event
        if isinstance(event, Done):
            return
    raise StreamError("the answer stream ended before its done line")
