package main

// The words of a connection app's page (connection_apps.go,
// templates/apps.html). %s is the provider's name; a guide's steps are
// app.<provider>.<step>.

func init() {
	for lang, words := range appStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var appStrings = map[string]map[string]string{
	"fr": {
		"app.title":        "Ton app",
		"app.heading":      "Ton app %s",
		"app.lead":         "{{name}} se connecte à %s avec ta propre app, créée une fois dans sa console : sa clé n'est qu'à toi, et elle sert pour tous tes comptes et tous les outils qui s'y connectent.",
		"app.guide":        "Crée ton app (une fois, quelques minutes)",
		"app.source":       "Aide du fournisseur",
		"app.json":         "Fichier JSON du client",
		"app.jsonhint":     "Celui que la console télécharge (client_secret_….json).",
		"app.or":           "Ou colle l'ID client et le code secret :",
		"app.clientid":     "ID client",
		"app.clientsecret": "Code secret du client",
		"app.save":         "Enregistrer mon app",
		"app.saved":        "Ton app est enregistrée : tu peux connecter tes comptes.",
		"app.noredirect":   "Ton app est enregistrée, mais son fichier ne liste pas %s comme adresse de retour : ajoute-la dans la console, sinon la connexion sera refusée.",
		"app.current":      "Ton app :",
		"app.secretkept":   "code secret enregistré (jamais réaffiché)",
		"app.replace":      "Remplacer mon app",
		"app.continue":     "Continuer",
		"app.origin.title": "Cette adresse ne convient pas à %s",
		"app.origin.ip":    "Tu ouvres Eriphe à %s : ce fournisseur n'accepte pas d'adresse IP comme adresse de retour d'une app.",
		"app.origin.local": "Tu ouvres Eriphe à %s : ce fournisseur n'accepte comme adresse de retour qu'un nom qui finit par un domaine public (.com, .fr, .net…), pas .local, .lan ou un nom de la maison.",
		"app.origin.http":  "Tu ouvres Eriphe à %s : ce fournisseur exige une adresse https.",
		"app.alt.lead":     "Pour connecter tes comptes :",

		"app.google.project":  "Crée un projet nommé « Eriphe » (« New project » → « Create »).",
		"app.google.apis":     "Active les API de ce projet d'un coup : ouvre ce lien, choisis le projet Eriphe, puis « Enable ».",
		"app.google.branding": "Google Auth Platform → « Get started » : « App name » Eriphe, ton adresse en « User support email » ; « Audience » : « External » ; ton adresse en « Contact Information » ; accepte le règlement → « Create ».",
		"app.google.audience": "« Audience » → « Test users » → « Add users » : ajoute ton adresse Google → « Save ». (Ou « Publish app » : en « Testing », Google fait expirer l'autorisation au bout de 7 jours.)",
		"app.google.scopes":   "« Data access » → « Add or remove scopes » → sous « Manually add scopes », colle cette liste → « Add to table » → « Update » → « Save » :",
		"app.google.client":   "« Clients » → « Create client » : « Application type » « Web application », « Name » Eriphe ; sous « Authorized redirect URIs », « Add URI » et colle exactement cette adresse, puis « Create » :",
		"app.google.secret":   "Google montre l'ID client et le code secret une seule fois : clique « Download JSON » (ou copie les deux), puis donne-les ci-dessous.",
	},
	"en": {
		"app.title":        "Your app",
		"app.heading":      "Your %s app",
		"app.lead":         "{{name}} connects to %s with your own app, made once in its console: its key is yours alone, and it serves all your accounts and every tool that connects to them.",
		"app.guide":        "Create your app (once, a few minutes)",
		"app.source":       "The provider's help",
		"app.json":         "Client JSON file",
		"app.jsonhint":     "The one the console downloads (client_secret_….json).",
		"app.or":           "Or paste the client ID and secret:",
		"app.clientid":     "Client ID",
		"app.clientsecret": "Client secret",
		"app.save":         "Save my app",
		"app.saved":        "Your app is saved: you can connect your accounts.",
		"app.noredirect":   "Your app is saved, but its file does not list %s as a redirect address: add it in the console, or the connection will be refused.",
		"app.current":      "Your app:",
		"app.secretkept":   "secret saved (never shown again)",
		"app.replace":      "Replace my app",
		"app.continue":     "Continue",
		"app.origin.title": "%s does not take this address",
		"app.origin.ip":    "You open Eriphe at %s: this provider takes no IP address as an app's redirect address.",
		"app.origin.local": "You open Eriphe at %s: this provider only takes a redirect address whose name ends in a public domain (.com, .org, .net…), not .local, .lan or a home name.",
		"app.origin.http":  "You open Eriphe at %s: this provider requires an https address.",
		"app.alt.lead":     "To connect your accounts:",

		"app.google.project":  "Create a project named “Eriphe” (“New project” → “Create”).",
		"app.google.apis":     "Turn on the project's APIs at once: open this link, pick the Eriphe project, then “Enable”.",
		"app.google.branding": "Google Auth Platform → “Get started”: “App name” Eriphe, your address as “User support email”; “Audience”: “External”; your address under “Contact Information”; accept the policy → “Create”.",
		"app.google.audience": "“Audience” → “Test users” → “Add users”: add your Google address → “Save”. (Or “Publish app”: in “Testing”, Google expires the authorization after 7 days.)",
		"app.google.scopes":   "“Data access” → “Add or remove scopes” → under “Manually add scopes”, paste this list → “Add to table” → “Update” → “Save”:",
		"app.google.client":   "“Clients” → “Create client”: “Application type” “Web application”, “Name” Eriphe; under “Authorized redirect URIs”, “Add URI” and paste exactly this address, then “Create”:",
		"app.google.secret":   "Google shows the client ID and secret only once: click “Download JSON” (or copy both), then give them below.",
	},
}
