package main

// The words of My space → Workspace address (web_address.go).

func init() {
	for lang, words := range addressStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var addressStrings = map[string]map[string]string{
	"fr": {
		"address.title":        "Adresse de l'espace",
		"address.lead":         "Pour ouvrir Eriphe à ton propre nom, derrière ton proxy (Traefik, Nginx Proxy Manager, Caddy…).",
		"address.guide":        "Le guide",
		"address.url":          "Adresse de l'espace",
		"address.urlhint":      "Celle que tu ouvres, en https et sans chemin. Les retours de connexion (fournisseur, outils, Google Drive) y arrivent. Vide : l'adresse de la page.",
		"address.inuse":        "En ce moment : %s",
		"address.trusted":      "Proxies de confiance",
		"address.trustedhint":  "Adresses ou plages CIDR, une par ligne. Venant d'elles seulement, Eriphe croit les en-têtes X-Forwarded-For, -Proto et -Host (et celui de la connexion par proxy) ; jamais d'ailleurs.",
		"address.proxied":      "Cette page vient d'un proxy de confiance, pour %s.",
		"address.direct":       "Cette page vient directement de %s.",
		"address.fallback":     "Seconde adresse pour les appareils",
		"address.fallbackhint": "Facultative : une adresse d'Eriphe joignable hors de la maison (le port 8800, par exemple l'adresse Tailscale de la machine). Les commandes « Connecter un appareil » la donnent après l'adresse locale, comme celle de l'accès à distance. Un appareil déjà connecté la prend en relançant sa commande d'installation avec --core LOCALE,SECONDE, sans jeton.",
		"address.badfallback":  "La seconde adresse doit être une adresse http ou https sans chemin, par exemple http://100.64.0.2:8800.",
		"address.save":         "Enregistrer",
		"address.saved":        "Enregistré.",
		"address.elsewhere":    "Ouvre l'espace à son adresse, %s, pour le faire : c'est là que le navigateur revient.",
		"address.oidclocked":   "La connexion par fournisseur revient à l'adresse actuelle, et le mot de passe est coupé : garde le mot de passe depuis le réseau local avant de changer l'adresse.",
		"address.oidcnew":      "Indique au fournisseur la nouvelle adresse de retour : %s.",
	},
	"en": {
		"address.title":        "Workspace address",
		"address.lead":         "To open Eriphe at your own name, behind your proxy (Traefik, Nginx Proxy Manager, Caddy…).",
		"address.guide":        "The guide",
		"address.url":          "Workspace address",
		"address.urlhint":      "The one you open, in https and without a path. The sign-in returns (provider, tools, Google Drive) come back to it. Empty: the page's address.",
		"address.inuse":        "Right now: %s",
		"address.trusted":      "Trusted proxies",
		"address.trustedhint":  "Addresses or CIDR ranges, one per line. From them only, Eriphe believes the X-Forwarded-For, -Proto and -Host headers (and the proxy sign-in's); never from anywhere else.",
		"address.proxied":      "This page comes through a trusted proxy, for %s.",
		"address.direct":       "This page comes straight from %s.",
		"address.fallback":     "Second address for the devices",
		"address.fallbackhint": "Optional: an address of Eriphe reachable away from home (port 8800, e.g. the machine's Tailscale address). The \"Connect a device\" commands give it after the local one, as the remote access one. A device already connected takes it when its install command runs again with --core LOCAL,SECOND, without a token.",
		"address.badfallback":  "The second address must be an http or https address without a path, e.g. http://100.64.0.2:8800.",
		"address.save":         "Save",
		"address.saved":        "Saved.",
		"address.elsewhere":    "Open the workspace at its address, %s, to do this: the browser comes back there.",
		"address.oidclocked":   "The provider's sign-in comes back to the current address, and the password is off: keep the password from the local network before changing the address.",
		"address.oidcnew":      "Give the provider the new redirect URI: %s.",
	},
}
