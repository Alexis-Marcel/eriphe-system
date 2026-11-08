# Eriphe for Home Assistant

The voice of the house, both ways, on one token: a conversation agent that hands Home Assistant's
Assist pipeline every sentence it cannot handle locally to `eriphe-core`, and speaks its answer as it
streams in (Piper starts reading before the end); and the speakers, on which it says what
`eriphe-core` has to tell you.
Home Assistant stays Eriphe's body, not a second brain: simple commands ("allume la lumière du
bureau") are still handled by Home Assistant itself, everything else goes to Eriphe.

The agent has no "control" feature: Home Assistant does not pass it its LLM API or tools. Eriphe acts
on the house itself through Home Assistant's MCP Server.

## Install

It is not in HACS yet: install it by hand.

1. Copy `custom_components/eriphe` into Home Assistant's `/config/custom_components/`
   (so that `/config/custom_components/eriphe/manifest.json` exists).
2. Restart Home Assistant.
3. **Settings > Devices & services > Add integration > Eriphe**, then enter:
   - the URL of `eriphe-core` on the LAN, for example `http://192.0.2.2:8800` (plain HTTP is
     for the local network and Tailscale only: `eriphe-core` refuses it from a public address, as
     the token would cross the internet in clear; away from home, give its tailnet address,
     `https://eriphe.<tailnet>.ts.net:8800`);
   - the token `eriphe-core` gave for Home Assistant (in the workspace, Settings > Tools > Home
     Assistant > "The house's voice talks to Eriphe", which also shows the URL to enter).

   The form checks both with an empty request, which `eriphe-core` answers 400 once the token is
   accepted, then reads the name you gave your assistant (`GET /v1/voice`, `{"name": "Jarvis"}`):
   the entry and the conversation agent take it (Eriphe with an `eriphe-core` older than the name).
   The URL and token can be changed later with **Reconfigure** on the integration, which also
   reads the name again after a rename (the agent's entity id stays as it was).
4. **Settings > Voice assistants**, open the Assist pipeline (or create one), set **Conversation
   agent** to your assistant (**Eriphe** by default), and keep **Prefer handling commands locally** on.

## What it sends

For each sentence the pipeline hands it, the agent calls:

```
POST {url}/v1/voice
Authorization: Bearer {token}

{"text": "...", "language": "fr", "conversation_id": "...", "device_id": "...",
 "satellite_id": "...", "area": "Bureau"}
```

`area` is the name of the Home Assistant area of the satellite entity (or of its device, or of the
device Home Assistant passed). `"from_user": true` is added when the conversation's context has a
Home Assistant user (someone typing in the Assist dialog). Empty fields are left out.

A sentence with no device, no satellite and no user is an **automation's** (`conversation.process`
in an automation). An automation is not you: `eriphe-core` takes only the closed request
`brief` from it (the point of the moment, read aloud), as an event that can make the assistant
speak but never act; any other text is refused. What an automation knows of the house (you came
home, the TV is on) goes to `POST /v1/context` as a signal, with a source token made on the page
« Ce qu'Eriphe sait de toi » (see `core/README.md`, the context), for example with a
`rest_command`. Simpler: with Home Assistant's tool in Eriphe's catalog, `eriphe-core` can
follow the entities you approve itself (people, room sensors, the bed, the TV, a satellite to speak
in a room), with that tool's token; it speaks through this integration (below).

`eriphe-core` answers NDJSON: `{"delta": "..."}` lines, streamed into the chat log as they come,
then `{"done": true, "text": "...", "continue": false}`. `continue` decides whether the satellite
listens again. If `eriphe-core` cannot be reached, refuses the token or has no brain available, the
agent says "Je n'arrive pas à joindre Eriphe." (with your assistant's name) instead of failing; if
the stream breaks after a partial answer, it adds "La connexion avec Eriphe a été coupée.".

## The speakers

While the integration is set up, it keeps one request open on `eriphe-core`, with the same token:

```
GET {url}/v1/voice/announce?wait=50
Authorization: Bearer {token}

{"announcements": [{"id": 1, "entity": "assist_satellite.salon", "text": "Sam, Eriphe a quelque chose pour toi."}]}
```

It answers at once when there is something to say, or after 50 s with an empty list, and the
integration asks again. Each sentence is said by Home Assistant itself: `assist_satellite.announce`
on a satellite, `tts.speak` with the house's first `tts` entity on a media player. Nothing else is
ever called, whatever comes back. The sentence is written by `eriphe-core`'s code (never a
notification's text), for the speaker of the room you are in, and only on the "voice" line of
« Où te prévenir ». `eriphe-core` never calls Home Assistant to speak, and never borrows the token
of its Home Assistant tool. If `eriphe-core` cannot be reached, the integration tries again (5 s
growing to 5 min); an `eriphe-core` older than the speakers is asked again every hour.

**Anyone in the room may be speaking.** What is said to a satellite joins the conversation as
yours, but `eriphe-core` takes it as coming from someone in the room: Eriphe reads and answers
freely, but anything it would change goes on a card that you confirm on your phone or in the
workspace. A « oui » said aloud never confirms it.

## Develop

`stream.py`, `api.py` and `const.py` do not import Home Assistant. Their tests run with plain pytest
(the client tests need aiohttp and are skipped without it):

```sh
python3 -m pytest ha/tests
python3 -m py_compile ha/custom_components/eriphe/*.py
```

The config flow and the agent are tested inside a test Home Assistant, in a separate environment
(Home Assistant 2026.10 needs Python 3.14):

```sh
uv venv --python 3.14 .venv-ha
uv pip install --python .venv-ha/bin/python homeassistant==2026.10.4 \
  pytest-homeassistant-custom-component \
  hassil==3.12.1 home-assistant-intents==2026.10.6 gazetteer-matcher==1.1.0
.venv-ha/bin/python -m pytest ha/tests_ha -o asyncio_mode=auto
```

Without Home Assistant installed, `ha/tests_ha` is skipped. Run the two folders separately:
the Home Assistant test plugin blocks the sockets the client tests open.
