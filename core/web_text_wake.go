package main

// The words of a device's waking (Wake-on-LAN): its switch, "Wake now" and
// how to get the device ready (web_wake.go, wake.go).

func init() {
	for lang, words := range wakeStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var wakeStrings = map[string]map[string]string{
	"fr": {
		"wake.title":          "Réveil par le réseau",
		"wake.fact":           "Réveiller l'appareil",
		"wake.allow":          "{{name}} peut réveiller cet appareil",
		"wake.allowhint":      "Quand une tâche a besoin de cet appareil et qu'il dort, {{name}} lui envoie un signal de réveil (Wake-on-LAN). Seulement sur le réseau de la maison.",
		"wake.now":            "Réveiller maintenant",
		"wake.isup":           "Il est allumé : rien à réveiller.",
		"wake.nomac":          "Cet appareil n'a pas encore indiqué l'adresse de sa carte réseau. Relance la commande d'installation sur l'appareil (elle garde ses réglages), puis reviens ici.",
		"wake.card":           "Carte réseau :",
		"wake.waiting":        "Signal envoyé. En attente de %s…",
		"wake.back":           "%s est de retour.",
		"wake.late":           "%s n'a pas répondu en 2 minutes. Vérifie qu'il est branché au réseau de la maison et que le réveil par le réseau est activé (voir « Préparer l'appareil »).",
		"wake.err.notallowed": "Active d'abord « {{name}} peut réveiller cet appareil ».",
		"wake.err.nomac":      "Cet appareil n'a pas encore indiqué l'adresse de sa carte réseau : relance sa commande d'installation.",
		"wake.err.online":     "Cet appareil est déjà allumé.",
		"wake.guide":          "Préparer l'appareil",
		"wake.guidelead":      "Le signal passe par la carte réseau : elle doit l'accepter, une fois pour toutes. Il ne traverse pas Internet : l'appareil doit être sur le même réseau que {{name}}.",
		"wake.pc":             "PC (Windows, Linux)",
		"wake.pc1":            "Dans le BIOS/UEFI (touche Suppr, F2 ou F10 au démarrage), active « Wake on LAN » (parfois « Power On by PCI-E »).",
		"wake.pc2":            "Sous Windows : Gestionnaire de périphériques → Cartes réseau → ta carte → Gestion de l'alimentation, coche « Autoriser ce périphérique à sortir l'ordinateur du mode veille » ; onglet Avancé, mets « Wake on Magic Packet » sur Activé.",
		"wake.pc3":            "Branche-le par câble Ethernet : en Wi-Fi, le réveil marche rarement.",
		"wake.pc4":            "Pour le rallumer depuis un arrêt complet, désactive aussi le démarrage rapide de Windows (Options d'alimentation). Depuis la veille, ce n'est pas nécessaire.",
		"wake.macos":          "Mac",
		"wake.macos1":         "Réglages Système → Énergie (sur un portable : Batterie → Options) : active « Réactiver pour l'accès au réseau ».",
		"wake.macos2":         "Un Mac se réveille de la veille, pas d'un arrêt complet : laisse-le en veille.",
	},
	"en": {
		"wake.title":          "Wake over the network",
		"wake.fact":           "Wake the device",
		"wake.allow":          "{{name}} may wake this device",
		"wake.allowhint":      "When a task needs this device and it is asleep, {{name}} sends it a wake-up signal (Wake-on-LAN). Only on the home network.",
		"wake.now":            "Wake now",
		"wake.isup":           "It is on: nothing to wake.",
		"wake.nomac":          "This device has not reported its network card's address yet. Run the install command again on the device (it keeps its settings), then come back here.",
		"wake.card":           "Network card:",
		"wake.waiting":        "Signal sent. Waiting for %s…",
		"wake.back":           "%s is back.",
		"wake.late":           "%s did not answer within 2 minutes. Check that it is plugged into the home network and that waking from the network is on (see “Get the device ready”).",
		"wake.err.notallowed": "First turn on “{{name}} may wake this device”.",
		"wake.err.nomac":      "This device has not reported its network card's address yet: run its install command again.",
		"wake.err.online":     "This device is already on.",
		"wake.guide":          "Get the device ready",
		"wake.guidelead":      "The signal goes through the network card: it must accept it, once and for all. It does not cross the Internet: the device must be on the same network as {{name}}.",
		"wake.pc":             "PC (Windows, Linux)",
		"wake.pc1":            "In the BIOS/UEFI (Del, F2 or F10 at startup), turn on “Wake on LAN” (sometimes “Power On by PCI-E”).",
		"wake.pc2":            "On Windows: Device Manager → Network adapters → your adapter → Power Management, tick “Allow this device to wake the computer”; on the Advanced tab, set “Wake on Magic Packet” to Enabled.",
		"wake.pc3":            "Plug it in with an Ethernet cable: over Wi-Fi, waking rarely works.",
		"wake.pc4":            "To turn it on from a full shutdown, also turn off Windows Fast Startup (Power Options). From sleep, this is not needed.",
		"wake.macos":          "Mac",
		"wake.macos1":         "System Settings → Energy (on a laptop: Battery → Options): turn on “Wake for network access”.",
		"wake.macos2":         "A Mac wakes from sleep, not from a full shutdown: leave it asleep.",
	},
}
