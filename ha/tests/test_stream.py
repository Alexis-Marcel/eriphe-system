"""Tests of the voice request body and the NDJSON answer stream."""

import asyncio

import pytest

from eriphe_loader import load

stream = load("stream")


async def _chunks(*chunks: bytes):
    for chunk in chunks:
        await asyncio.sleep(0)
        yield chunk


def _events(*chunks: bytes) -> list:
    async def collect():
        return [event async for event in stream.iter_events(_chunks(*chunks))]

    return asyncio.run(collect())


def test_deltas_then_done():
    events = _events(
        b'{"delta": "Bonjour"}\n{"delta": " sam."}\n',
        b'{"done": true, "text": "Bonjour sam.", "continue": false}\n',
    )
    assert events == [
        stream.Delta("Bonjour"),
        stream.Delta(" sam."),
        stream.Done(text="Bonjour sam.", continue_conversation=False),
    ]


def test_lines_split_across_chunks():
    raw = (
        '{"delta": "Il fait 20 °C"}\n'
        '{"done": true, "text": "Il fait 20 °C", "continue": true}\n'
    ).encode()
    # Cut everywhere, including inside the two bytes of "°".
    events = _events(*(raw[i : i + 3] for i in range(0, len(raw), 3)))
    assert events == [
        stream.Delta("Il fait 20 °C"),
        stream.Done(text="Il fait 20 °C", continue_conversation=True),
    ]


def test_blank_lines_are_ignored():
    events = _events(
        b"\n\r\n",
        b'{"delta": "Oui"}\r\n\n   \n',
        b'{"done": true, "text": "Oui"}',  # last line without newline
    )
    assert events == [
        stream.Delta("Oui"),
        stream.Done(text="Oui", continue_conversation=False),
    ]


def test_done_without_deltas():
    events = _events(b'{"done": true, "text": "Voila.", "continue": false}\n')
    assert events == [stream.Done(text="Voila.", continue_conversation=False)]


def test_stops_at_done():
    events = _events(b'{"done": true, "text": "Fini"}\n{"delta": "trop tard"}\n')
    assert events == [stream.Done(text="Fini", continue_conversation=False)]


def test_unknown_lines_are_ignored():
    events = _events(b'{"ping": 1}\n{"delta": "a"}\n{"done": true, "text": "a"}\n')
    assert events == [stream.Delta("a"), stream.Done("a", False)]


def test_end_without_done_is_an_error():
    with pytest.raises(stream.StreamError):
        _events(b'{"delta": "coup"}\n')


def test_invalid_json_is_an_error():
    with pytest.raises(stream.StreamError):
        _events(b"not json\n")


def test_error_line_is_an_error():
    with pytest.raises(stream.StreamError, match="brain"):
        _events(b'{"error": "brain not available"}\n')


def test_decoder_keeps_the_tail():
    decoder = stream.NdjsonDecoder()
    assert decoder.feed(b'{"delta": "a"}\n{"del') == [{"delta": "a"}]
    assert decoder.feed(b'ta": "b"}') == []
    assert decoder.flush() == [{"delta": "b"}]
    assert decoder.flush() == []


def test_build_request_omits_empty_fields():
    assert stream.build_request(
        "Allume la lumière",
        language="fr",
        conversation_id="01ABC",
        device_id=None,
        satellite_id="assist_satellite.kitchen",
        area="",
    ) == {
        "text": "Allume la lumière",
        "language": "fr",
        "conversation_id": "01ABC",
        "satellite_id": "assist_satellite.kitchen",
    }


def test_build_request_says_a_person_spoke():
    # A person typing in Home Assistant's chat has no device: from_user tells
    # eriphe-core it is not an automation.
    assert stream.build_request("Bonjour", from_user=True) == {
        "text": "Bonjour",
        "from_user": True,
    }
    assert "from_user" not in stream.build_request("brief")
