# Eriphe for Home Assistant

One of Eriphe's channels, both ways, on its token: a conversation agent that hands Home Assistant's
Assist pipeline every sentence it cannot handle locally to `eriphe-core`, and speaks its answer as it
streams in (Piper starts reading before the end); and the speakers, on which it says, in the room
you are in, what `eriphe-core` has to tell you.
Home Assistant stays Eriphe's body, not a second brain: simple commands ("allume la lumière du
bureau") are still handled by Home Assistant itself, everything else goes to Eriphe.

The agent has no "control" feature: Home Assistant does not pass it its LLM API or tools. Eriphe acts
on the house itself through Home Assistant's MCP Server.

## Install

It is not in HACS yet: install it by hand.

1. In the workspace, **Settings > Access > Add an access > API access**: name it (Home
   Assistant), leave **Trusted** off since anyone in the room can talk to a satellite, and pick
   **A sentence in your room** so that the speakers can tell you things. Add it, then copy the
   address and the token it shows once.
2. Copy `custom_components/eriphe` into Home Assistant's `/config/custom_components/`
   (so that `/config/custom_components/eriphe/manifest.json` exists).
3. Restart Home Assistant.
4. **Settings > Devices & services > Add integration > Eriphe**, then enter:
   - the URL of `eriphe-core` on the LAN, for example `http://192.0.2.2:8800` (plain HTTP is
     for the local network and Tailscale only: `eriphe-core` refuses it from a public address, as
     the token would cross the internet in clear; away from home, give its tailnet address,
     `https://eriphe.<tailnet>.ts.net:8800`);
   - the channel's token.

   The form checks both with an empty request, which `eriphe-core` answers 400 once the token is
   accepted, then reads the name you gave your assistant (`GET /v1/channel`, `{"name": "Jarvis"}`):
   the entry and the conversation agent take it. The URL and token can be changed later with
   **Reconfigure** on the integration, which also reads the name again after a rename (the agent's
   entity id stays as it was).
5. **Settings > Voice assistants**, open the Assist pipeline (or create one), set **Conversation
   agent** to your assistant (**Eriphe** by default), and keep **Prefer handling commands locally** on.

## What it sends

For each sentence the pipeline hands it, the agent calls:

```
POST {url}/v1/channel
Authorization: Bearer {token}

{"text": "...", "language": "fr", "place": "Bureau"}
```

`place` is the name of the Home Assistant area of the satellite entity (or of its device, or of the
device Home Assistant passed). A sentence with no device, no satellite and no user is an
automation's (`conversation.process` in an automation): it adds `"wait": 80`, to wait for the answer
it sends on. Empty fields are left out.

`eriphe-core` takes every sentence of this channel the same way, as you set the channel up: not
trusted, what is said joins the conversation but Eriphe takes it as maybe not yours. It reads and
answers freely, but anything it would change goes on a card that you confirm on your phone or in the
workspace; a « oui » said aloud never confirms it. With each request for what to say, the integration also tells the areas
where someone is now (an occupancy, presence or motion sensor on): eriphe-core's voice line
speaks only there. What the house has to tell Eriphe (you came home, you went to bed) goes from
an automation (`rest_command`) to an incoming address made in Eriphe's Settings → Skills, as any
system's; a skill reacts to it (`when: hook.received key=<name>`). To read the house, Eriphe uses
Home Assistant's tool in its catalog, like any tool.

`eriphe-core` answers NDJSON: `{"delta": "..."}` lines, streamed into the chat log as they come,
then `{"done": true, "text": "...", "continue": false}`. `continue` decides whether the satellite
listens again. If `eriphe-core` cannot be reached, refuses the token or has no brain available, the
agent says "Je n'arrive pas à joindre Eriphe." (with your assistant's name) instead of failing; if
the stream breaks after a partial answer, it adds "La connexion avec Eriphe a été coupée.".

## The speakers

While the integration is set up, it keeps one request open on `eriphe-core`, with the same token,
telling the areas that have a satellite or a media player (one with text-to-speech in the house):

```
GET {url}/v1/channel/out?wait=50&places=Bureau,Salon
Authorization: Bearer {token}

{"messages": [{"id": 1, "text": "Sam, Eriphe a quelque chose pour toi.", "place": "Salon"}]}
```

It answers at once when there is something to say, or after 50 s with an empty list, and the
integration asks again. Each sentence is said by Home Assistant itself, on a speaker of its area: a
satellite first (`assist_satellite.announce`), else a media player (`tts.speak` with the house's
first `tts` entity). Nothing else is ever called, whatever comes back; a place with no speaker stays
silent. The sentence is written by `eriphe-core`'s code (never a notification's text), for the room
you are in, and only on the "voice" line of « Où te prévenir ». `eriphe-core` never calls Home
Assistant to speak, and never borrows the token of its Home Assistant tool. If `eriphe-core` cannot
be reached, the integration tries again (5 s growing to 5 min); an `eriphe-core` without channels is
asked again every hour.

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
