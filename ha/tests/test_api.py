"""Tests of the eriphe-core client against a local aiohttp server.

Skipped when aiohttp is not installed (Home Assistant ships it).
"""

import asyncio
import json

import pytest

aiohttp = pytest.importorskip("aiohttp")
from aiohttp import web  # noqa: E402

from eriphe_loader import load  # noqa: E402

api = load("api")
stream = load("stream")

TOKEN = "test-token"


async def _voice(request: web.Request) -> web.StreamResponse:
    if request.headers.get("Authorization") != f"Bearer {TOKEN}":
        return web.json_response({"error": "bad token"}, status=401)
    body = await request.json()
    if not body.get("text"):
        return web.json_response({"error": "empty text"}, status=400)
    if body["text"] == "brain down":
        return web.json_response({"error": "brain not available"}, status=503)
    resp = web.StreamResponse(headers={"Content-Type": "application/x-ndjson"})
    await resp.prepare(request)
    await resp.write(b'{"delta": "Bon')
    await resp.write(b'jour"}\n{"delta": " ' + body.get("place", "").encode() + b'"}\n')
    done = {"done": True, "text": "Bonjour", "continue": True}
    await resp.write(json.dumps(done).encode() + b"\n")
    await resp.write_eof()
    return resp


async def _name(request: web.Request) -> web.Response:
    if request.headers.get("Authorization") != f"Bearer {TOKEN}":
        return web.json_response({"error": "bad token"}, status=401)
    return web.json_response({"name": "Jarvis"})


MESSAGES = [
    {"id": 1, "text": "Sam, Eriphe a quelque chose pour toi.", "place": "Salon"},
    {"id": 2, "text": " ", "place": "Salon"},
    {"id": 3, "place": "Salon"},
    {"id": 4, "text": "x", "place": 3},
    "not an object",
]


async def _out(request: web.Request) -> web.Response:
    if request.headers.get("Authorization") != f"Bearer {TOKEN}":
        return web.json_response({"error": "bad token"}, status=401)
    assert request.query.get("wait") == "50"
    assert request.query.get("places") == "Bureau,Salon"
    assert request.query.get("here") in ("", "Salon")
    return web.json_response({"messages": MESSAGES})


def _run(test, named=True):
    async def main():
        app = web.Application()
        app.router.add_post("/v1/channel", _voice)
        if named:
            app.router.add_get("/v1/channel", _name)
            app.router.add_get("/v1/channel/out", _out)
        runner = web.AppRunner(app)
        await runner.setup()
        site = web.TCPSite(runner, "127.0.0.1", 0)
        await site.start()
        port = site._server.sockets[0].getsockname()[1]
        try:
            async with aiohttp.ClientSession() as session:
                return await test(session, f"http://127.0.0.1:{port}/")
        finally:
            await runner.cleanup()

    return asyncio.run(main())


def test_validate_accepts_400():
    async def test(session, url):
        assert await api.EripheClient(session, url, TOKEN).async_validate() == "Jarvis"

    _run(test)


def test_name_of_an_older_core():
    async def test(session, url):
        assert await api.EripheClient(session, url, TOKEN).async_validate() == "Eriphe"

    _run(test, named=False)


def test_validate_rejects_bad_token():
    async def test(session, url):
        with pytest.raises(api.InvalidAuth):
            await api.EripheClient(session, url, "wrong").async_validate()

    _run(test)


def test_validate_cannot_connect():
    async def main():
        async with aiohttp.ClientSession() as session:
            client = api.EripheClient(session, "http://127.0.0.1:1", TOKEN)
            with pytest.raises(api.CannotConnect):
                await client.async_validate()

    asyncio.run(main())


def test_voice_streams_events():
    async def test(session, url):
        client = api.EripheClient(session, url, TOKEN)
        body = stream.build_request("Salut", language="fr", place="Bureau")
        return [event async for event in client.async_voice(body)]

    events = _run(test)
    assert events[:2] == [stream.Delta("Bonjour"), stream.Delta(" Bureau")]
    assert events[2].continue_conversation is True
    assert len(events) == 3


def test_voice_brain_unavailable():
    async def test(session, url):
        client = api.EripheClient(session, url, TOKEN)
        with pytest.raises(api.BrainUnavailable, match="brain not available"):
            async for _ in client.async_voice({"text": "brain down"}):
                pass

    _run(test)


def test_out():
    async def test(session, url):
        return await api.EripheClient(session, url, TOKEN).async_out(
            ["Bureau", "Salon"], ["Salon"]
        )

    assert _run(test) == [api.Message("Sam, Eriphe a quelque chose pour toi.", "Salon")]


def test_out_bad_token_and_older_core():
    async def bad(session, url):
        with pytest.raises(api.InvalidAuth):
            await api.EripheClient(session, url, "wrong").async_out([])

    async def older(session, url):
        with pytest.raises(api.NotSupported):
            await api.EripheClient(session, url, TOKEN).async_out([])

    _run(bad)
    _run(older, named=False)


def test_speakers():
    say = "Sam, Eriphe a quelque chose pour toi."
    speakers = api.Speakers(tts=["tts.piper", "tts.cloud"])
    speakers.add("Salon", "media_player.tv")
    speakers.add("Salon", "assist_satellite.salon")
    speakers.add("Bureau", "media_player.bureau")
    speakers.add("Cuisine", "light.cuisine")  # not a speaker
    assert speakers.places() == ["Bureau", "Salon"]
    # A satellite first; the place matched without case.
    assert speakers.service_call(api.Message(say, "salon")) == (
        "assist_satellite",
        "announce",
        {"entity_id": "assist_satellite.salon", "message": say},
    )
    assert speakers.service_call(api.Message(say, "Bureau")) == (
        "tts",
        "speak",
        {
            "entity_id": "tts.cloud",
            "media_player_entity_id": "media_player.bureau",
            "message": say,
        },
    )
    # A room with no speaker, or no room in a house of several: silence.
    assert speakers.service_call(api.Message(say, "Cuisine")) is None
    assert speakers.service_call(api.Message(say)) is None
    # Without text-to-speech, a media player cannot speak.
    mute = api.Speakers()
    mute.add("Salon", "media_player.tv")
    assert mute.places() == []
    one = api.Speakers()
    one.add("Salon", "assist_satellite.salon")
    assert (
        one.service_call(api.Message(say))[2]["entity_id"] == "assist_satellite.salon"
    )
