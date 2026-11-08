"""Constants for the Eriphe integration.

Kept free of Home Assistant imports so the client modules can be tested alone.
"""

DOMAIN = "eriphe"

# The integration is one of the channels the user adds in eriphe-core
# (Settings > Access): text in, text out, on the channel's token.
CHANNEL_PATH = "/v1/channel"

# What eriphe-core says in a room: a request kept open, which answers as
# soon as there is something to say or after OUT_WAIT seconds with nothing.
OUT_PATH = "/v1/channel/out"
OUT_WAIT = 50

# The assistant's name until eriphe-core gives the one the user chose (GET
# CHANNEL_PATH): the entry's title, the agent's device name.
DEFAULT_NAME = "Eriphe"

# Spoken when eriphe-core cannot be reached or fails before saying anything;
# {name} is the assistant's.
FALLBACK_SPEECH = "Je n'arrive pas à joindre {name}."
# Spoken after a partial answer when the stream breaks before its end.
INTERRUPTED_SPEECH = " La connexion avec {name} a été coupée."
