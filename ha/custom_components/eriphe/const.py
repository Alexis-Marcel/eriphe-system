"""Constants for the Eriphe integration.

Kept free of Home Assistant imports so the client modules can be tested alone.
"""

DOMAIN = "eriphe"

VOICE_PATH = "/v1/voice"

# Spoken when eriphe-core cannot be reached or fails before saying anything.
FALLBACK_SPEECH = "Je n'arrive pas à joindre Eriphe."
# Spoken after a partial answer when the stream breaks before its end.
INTERRUPTED_SPEECH = " La connexion avec Eriphe a été coupée."
