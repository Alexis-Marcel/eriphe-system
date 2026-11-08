"""Constants for the Eriphe integration.

Kept free of Home Assistant imports so the client modules can be tested alone.
"""

DOMAIN = "eriphe"

VOICE_PATH = "/v1/voice"

# What eriphe-core says on the house's speakers: a request kept open, which
# answers as soon as there is something to say or after ANNOUNCE_WAIT
# seconds with nothing.
ANNOUNCE_PATH = "/v1/voice/announce"
ANNOUNCE_WAIT = 50

# The assistant's name until eriphe-core gives the one the user chose (GET
# VOICE_PATH): the entry's title, the agent's device name.
DEFAULT_NAME = "Eriphe"

# Spoken when eriphe-core cannot be reached or fails before saying anything;
# {name} is the assistant's.
FALLBACK_SPEECH = "Je n'arrive pas à joindre {name}."
# Spoken after a partial answer when the stream breaks before its end.
INTERRUPTED_SPEECH = " La connexion avec {name} a été coupée."
