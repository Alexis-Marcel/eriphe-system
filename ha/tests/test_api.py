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
    await resp.write(b'jour"}\n{"delta": " ' + body.get("area", "").encode() + b'"}\n')
    done = {"done": True, "text": "Bonjour", "continue": True}
    await resp.write(json.dumps(done).encode() + b"\n")
    await resp.write_eof()
    return resp


async def _name(request: web.Request) -> web.Response:
    if request.headers.get("Authorization") != f"Bearer {TOKEN}":
        return web.json_response({"error": "bad token"}, status=401)
    return web.json_response({"name": "Jarvis"})


ANNOUNCEMENTS = [
    {
        "id": 1,
        "entity": "assist_satellite.salon",
        "text": "Sam, Eriphe a quelque chose pour toi.",
    },
    {"id": 2, "entity": "media_player.tv", "text": " "},
    {"id": 3, "text": "no entity"},
    "not an object",
]


async def _announce(request: web.Request) -> web.Response:
    if request.headers.get("Authorization") != f"Bearer {TOKEN}":
        return web.json_response({"error": "bad token"}, status=401)
    assert request.query.get("wait") == "50"
    return web.json_response({"announcements": ANNOUNCEMENTS})


def _run(test, named=True):
    async def main():
        app = web.Application()
        app.router.add_post("/v1/voice", _voice)
        if named:
            app.router.add_get("/v1/voice", _name)
            app.router.add_get("/v1/voice/announce", _announce)
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
        body = stream.build_request("Salut", language="fr", area="Bureau")
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


def test_announcements():
    async def test(session, url):
        return await api.EripheClient(session, url, TOKEN).async_announcements()

    assert _run(test) == [
        api.Announcement(
            "assist_satellite.salon", "Sam, Eriphe a quelque chose pour toi."
        )
    ]


def test_announcements_bad_token_and_older_core():
    async def bad(session, url):
        with pytest.raises(api.InvalidAuth):
            await api.EripheClient(session, url, "wrong").async_announcements()

    async def older(session, url):
        with pytest.raises(api.NotSupported):
            await api.EripheClient(session, url, TOKEN).async_announcements()

    _run(bad)
    _run(older, named=False)


def test_service_call():
    say = "Sam, Eriphe a quelque chose pour toi."
    sat = api.Announcement("assist_satellite.salon", say)
    assert api.service_call(sat, []) == (
        "assist_satellite",
        "announce",
        {"entity_id": "assist_satellite.salon", "message": say},
    )
    tv = api.Announcement("media_player.tv", say)
    assert api.service_call(tv, ["tts.piper", "tts.cloud"]) == (
        "tts",
        "speak",
        {
            "entity_id": "tts.cloud",
            "media_player_entity_id": "media_player.tv",
            "message": say,
        },
    )
    assert api.service_call(tv, []) is None
    for entity in ["light.salon", "lock.porte", "script.anything", "assist_satellite"]:
        assert api.service_call(api.Announcement(entity, say), ["tts.piper"]) is None
