package main

import (
	"fmt"
	"strings"
)

// The catalog of online tools: services that host their own MCP server and
// let Eriphe in like Linear, with OAuth and a client registered on the fly
// (dynamic client registration, or a Client ID Metadata Document), so that
// no app is made at the vendor. Each address, its registration and its
// scopes were read from the service's public metadata on 2026-10-30
// (/.well-known/oauth-protected-resource and oauth-authorization-server);
// the sign-in itself was not tried for each.
//
// The services that want an app made at their place (Figma, Slack, Asana,
// HubSpot, Box, Discord, Google, Microsoft 365) or approved clients only
// (Atlassian, ClickUp) are not here: the gallery says why ("Pas encore
// possible"). Their address can still be given to "Autre service".

// Categories of the gallery, in its order.
var toolCategories = []string{"tasks", "mail", "files", "meetings", "design", "dev", "web", "social", "finance", "automation", "home", "other"}

type onlineService struct {
	id, title, url, cat string
	auth                string // oauth when empty; none for a server open without an account
	paid, beta          bool
	readsOnly           bool      // the service only reads, whatever the scopes
	fr, en              [3]string // the tile's line, the tool's use, a note on its page
}

var onlineServices = []onlineService{
	// Tasks and notes.
	{id: "todoist", title: "Todoist", url: "https://ai.todoist.net/mcp", cat: "tasks",
		fr: [3]string{"Tâches et projets dans Todoist.", "Todoist : mes tâches et projets"},
		en: [3]string{"Tasks and projects in Todoist.", "Todoist: my tasks and projects"}},
	{id: "ticktick", title: "TickTick", url: "https://mcp.ticktick.com/", cat: "tasks",
		fr: [3]string{"Tâches et listes dans TickTick.", "TickTick : mes tâches et listes"},
		en: [3]string{"Tasks and lists in TickTick.", "TickTick: my tasks and lists"}},
	{id: "evernote", title: "Evernote", url: "https://mcp.evernote.com/mcp", cat: "tasks", paid: true,
		fr: [3]string{"Notes et carnets Evernote.", "Evernote : mes notes et carnets", "Réservé aux abonnements Evernote payants."},
		en: [3]string{"Evernote notes and notebooks.", "Evernote: my notes and notebooks", "For paid Evernote plans only."}},
	{id: "mem", title: "Mem", url: "https://mcp.mem.ai/mcp", cat: "tasks",
		fr: [3]string{"Tes notes dans Mem.", "Mem : mes notes"},
		en: [3]string{"Your notes in Mem.", "Mem: my notes"}},
	{id: "readwise", title: "Readwise", url: "https://mcp2.readwise.io/mcp", cat: "tasks",
		fr: [3]string{"Surlignages et lectures, Readwise et Reader.", "Readwise : mes surlignages et lectures"},
		en: [3]string{"Highlights and reading, Readwise and Reader.", "Readwise: my highlights and reading"}},
	{id: "raindrop", title: "Raindrop.io", url: "https://api.raindrop.io/rest/v2/ai/mcp", cat: "tasks",
		fr: [3]string{"Favoris et collections Raindrop.", "Raindrop : mes favoris et collections"},
		en: [3]string{"Raindrop bookmarks and collections.", "Raindrop: my bookmarks and collections"}},
	{id: "airtable", title: "Airtable", url: "https://mcp.airtable.com/mcp", cat: "tasks",
		fr: [3]string{"Bases, tables et fiches Airtable.", "Airtable : mes bases et tables"},
		en: [3]string{"Airtable bases, tables and records.", "Airtable: my bases and tables"}},
	{id: "monday", title: "monday.com", url: "https://mcp.monday.com/mcp", cat: "tasks",
		fr: [3]string{"Tableaux et éléments de monday.com.", "monday.com : mes tableaux et éléments"},
		en: [3]string{"monday.com boards and items.", "monday.com: my boards and items"}},

	// Mail and calendar.
	{id: "fastmail", title: "Fastmail", url: "https://api.fastmail.com/mcp", cat: "mail", paid: true,
		fr: [3]string{"Mails, agenda et contacts Fastmail, d'un coup.", "Fastmail : mes mails, mon agenda et mes contacts",
			"Compte Fastmail payant. Sur l'écran de Fastmail, tu choisis ce que {{name}} peut lire, écrire ou envoyer."},
		en: [3]string{"Fastmail mail, calendar and contacts at once.", "Fastmail: my mail, calendar and contacts",
			"A paid Fastmail account. On Fastmail's screen, you choose what {{name}} may read, write or send."}},
	{id: "calendly", title: "Calendly", url: "https://mcp.calendly.com", cat: "mail",
		fr: [3]string{"Types de rendez-vous et réservations Calendly.", "Calendly : mes rendez-vous et réservations"},
		en: [3]string{"Calendly event types and bookings.", "Calendly: my meetings and bookings"}},
	{id: "calcom", title: "Cal.com", url: "https://mcp.cal.com/mcp", cat: "mail",
		fr: [3]string{"Rendez-vous et disponibilités Cal.com.", "Cal.com : mes rendez-vous et disponibilités"},
		en: [3]string{"Cal.com bookings and availability.", "Cal.com: my bookings and availability"}},

	// Files.
	{id: "dropbox", title: "Dropbox", url: "https://mcp.dropbox.com/mcp", cat: "files", beta: true,
		fr: [3]string{"Chercher et lire tes fichiers Dropbox.", "Dropbox : mes fichiers", "Le serveur de Dropbox est en bêta."},
		en: [3]string{"Search and read your Dropbox files.", "Dropbox: my files", "Dropbox's server is in beta."}},

	// Meetings.
	{id: "granola", title: "Granola", url: "https://mcp.granola.ai/mcp", cat: "meetings",
		fr: [3]string{"Tes notes de réunion Granola.", "Granola : mes notes de réunion"},
		en: [3]string{"Your Granola meeting notes.", "Granola: my meeting notes"}},
	{id: "fireflies", title: "Fireflies", url: "https://api.fireflies.ai/mcp", cat: "meetings",
		fr: [3]string{"Comptes rendus et transcriptions Fireflies.", "Fireflies : mes comptes rendus de réunion"},
		en: [3]string{"Fireflies summaries and transcripts.", "Fireflies: my meeting summaries"}},
	{id: "otter", title: "Otter", url: "https://mcp.otter.ai/mcp", cat: "meetings", readsOnly: true,
		fr: [3]string{"Tes conversations transcrites par Otter.", "Otter : mes réunions transcrites"},
		en: [3]string{"Your conversations transcribed by Otter.", "Otter: my transcribed meetings"}},
	{id: "tldv", title: "tl;dv", url: "https://mcp.tldv.io/mcp", cat: "meetings",
		fr: [3]string{"Enregistrements et notes de réunion tl;dv.", "tl;dv : mes réunions enregistrées"},
		en: [3]string{"tl;dv meeting recordings and notes.", "tl;dv: my recorded meetings"}},

	// Design.
	{id: "canva", title: "Canva", url: "https://mcp.canva.com/mcp", cat: "design",
		fr: [3]string{"Designs, dossiers et modèles Canva.", "Canva : mes designs et modèles"},
		en: [3]string{"Canva designs, folders and templates.", "Canva: my designs and templates"}},
	{id: "miro", title: "Miro", url: "https://mcp.miro.com/", cat: "design",
		fr: [3]string{"Tableaux blancs Miro.", "Miro : mes tableaux"},
		en: [3]string{"Miro whiteboards.", "Miro: my boards"}},
	{id: "gamma", title: "Gamma", url: "https://mcp.gamma.app/mcp", cat: "design",
		fr: [3]string{"Présentations et documents Gamma.", "Gamma : mes présentations"},
		en: [3]string{"Gamma presentations and documents.", "Gamma: my presentations"}},

	// Development.
	{id: "cloudflare", title: "Cloudflare", url: "https://mcp.cloudflare.com/mcp", cat: "dev",
		fr: [3]string{"Ton compte Cloudflare : domaines, DNS, Workers.", "Cloudflare : mes domaines, DNS et Workers"},
		en: [3]string{"Your Cloudflare account: domains, DNS, Workers.", "Cloudflare: my domains, DNS and Workers"}},
	{id: "sentry", title: "Sentry", url: "https://mcp.sentry.dev/mcp", cat: "dev",
		fr: [3]string{"Erreurs et incidents de tes applis.", "Sentry : les erreurs de mes applis"},
		en: [3]string{"Errors and issues of your apps.", "Sentry: my apps' errors"}},
	{id: "vercel", title: "Vercel", url: "https://mcp.vercel.com", cat: "dev",
		fr: [3]string{"Projets, déploiements et journaux Vercel.", "Vercel : mes projets et déploiements"},
		en: [3]string{"Vercel projects, deployments and logs.", "Vercel: my projects and deployments"}},
	{id: "netlify", title: "Netlify", url: "https://netlify-mcp.netlify.app/mcp", cat: "dev",
		fr: [3]string{"Sites et déploiements Netlify.", "Netlify : mes sites et déploiements"},
		en: [3]string{"Netlify sites and deployments.", "Netlify: my sites and deployments"}},
	{id: "supabase", title: "Supabase", url: "https://mcp.supabase.com/mcp", cat: "dev",
		fr: [3]string{"Projets, bases de données et fonctions Supabase.", "Supabase : mes projets et bases de données"},
		en: [3]string{"Supabase projects, databases and functions.", "Supabase: my projects and databases"}},
	// Without ?login the server answers without an account, with its public tools only.
	{id: "huggingface", title: "Hugging Face", url: "https://huggingface.co/mcp?login", cat: "dev",
		fr: [3]string{"Modèles, jeux de données et Spaces.", "Hugging Face : modèles, jeux de données et Spaces"},
		en: [3]string{"Models, datasets and Spaces.", "Hugging Face: models, datasets and Spaces"}},
	{id: "jam", title: "Jam", url: "https://mcp.jam.dev/mcp", cat: "dev",
		fr: [3]string{"Rapports de bug Jam : vidéo, console, réseau.", "Jam : mes rapports de bug"},
		en: [3]string{"Jam bug reports: video, console, network.", "Jam: my bug reports"}},
	{id: "context7", title: "Context7", url: "https://mcp.context7.com/mcp", cat: "dev", auth: "none",
		fr: [3]string{"La documentation à jour des bibliothèques de code.", "Context7 : la documentation à jour des bibliothèques", "Marche sans compte."},
		en: [3]string{"Up-to-date documentation of code libraries.", "Context7: up-to-date library documentation", "Works without an account."}},

	// Websites and customers.
	{id: "webflow", title: "Webflow", url: "https://mcp.webflow.com/mcp", cat: "web",
		fr: [3]string{"Tes sites Webflow : pages et CMS.", "Webflow : mes sites"},
		en: [3]string{"Your Webflow sites: pages and CMS.", "Webflow: my sites"}},
	{id: "wix", title: "Wix", url: "https://mcp.wix.com/mcp", cat: "web",
		fr: [3]string{"Tes sites Wix.", "Wix : mes sites"},
		en: [3]string{"Your Wix sites.", "Wix: my sites"}},
	{id: "intercom", title: "Intercom", url: "https://mcp.intercom.com/mcp", cat: "web",
		fr: [3]string{"Conversations et contacts du support client.", "Intercom : le support client"},
		en: [3]string{"Customer support conversations and contacts.", "Intercom: customer support"}},
	{id: "attio", title: "Attio", url: "https://mcp.attio.com/mcp", cat: "web",
		fr: [3]string{"Ton CRM : contacts, entreprises, affaires.", "Attio : mes contacts et affaires"},
		en: [3]string{"Your CRM: people, companies, deals.", "Attio: my contacts and deals"}},

	// Social networks and newsletters. The networks themselves (LinkedIn,
	// Meta, X, TikTok, YouTube) host no MCP server: these services post
	// through their own approved apps.
	{id: "buffer", title: "Buffer", url: "https://mcp.buffer.com/mcp", cat: "social",
		fr: [3]string{"Posts, programmation, stats et commentaires, par Buffer.", "Buffer : mes posts, leurs stats et commentaires",
			"Par ton compte Buffer, {{name}} atteint Instagram, Facebook, LinkedIn, X, Threads, TikTok, YouTube, Pinterest, Bluesky, Mastodon et les Notes Substack."},
		en: [3]string{"Posts, scheduling, stats and comments, through Buffer.", "Buffer: my posts, their stats and comments",
			"Through your Buffer account, {{name}} reaches Instagram, Facebook, LinkedIn, X, Threads, TikTok, YouTube, Pinterest, Bluesky, Mastodon and Substack Notes."}},
	{id: "typefully", title: "Typefully", url: "https://mcp.typefully.com/mcp", cat: "social",
		fr: [3]string{"Brouillons et posts pour X, LinkedIn, Threads, Bluesky, Mastodon.", "Typefully : mes brouillons et posts",
			"Écrire ou programmer demande un abonnement Typefully payant."},
		en: [3]string{"Drafts and posts for X, LinkedIn, Threads, Bluesky, Mastodon.", "Typefully: my drafts and posts",
			"Writing or scheduling needs a paid Typefully plan."}},
	{id: "hootsuite", title: "Hootsuite", url: "https://mcp.hootsuite.com/perch", cat: "social", paid: true,
		fr: [3]string{"Publier et suivre les stats, par Hootsuite.", "Hootsuite : publier et suivre mes stats",
			"Les commentaires et messages sont dans Hootsuite Inbox, un outil à part."},
		en: [3]string{"Publish and follow the stats, through Hootsuite.", "Hootsuite: publish and follow my stats",
			"Comments and messages are in Hootsuite Inbox, a tool of its own."}},
	{id: "hootsuite-inbox", title: "Hootsuite Inbox", url: "https://mcp.hootsuite.com/nest", cat: "social", paid: true,
		fr: [3]string{"Commentaires et messages de tes réseaux, par Hootsuite.", "Hootsuite Inbox : mes commentaires et messages"},
		en: [3]string{"Comments and messages of your networks, through Hootsuite.", "Hootsuite Inbox: my comments and messages"}},
	{id: "metricool", title: "Metricool", url: "https://ai.metricool.com/mcp", cat: "social",
		fr: [3]string{"Programmation et stats de tes réseaux, par Metricool.", "Metricool : mes posts et stats"},
		en: [3]string{"Scheduling and stats of your networks, through Metricool.", "Metricool: my posts and stats"}},
	{id: "beehiiv", title: "Beehiiv", url: "https://mcp.beehiiv.com/mcp", cat: "social",
		fr: [3]string{"Newsletter Beehiiv : posts, abonnés, stats.", "Beehiiv : ma newsletter, abonnés et stats",
			"Le plan gratuit ne permet que de lire ; les brouillons demandent un plan payant. L'envoi se fait dans Beehiiv."},
		en: [3]string{"Beehiiv newsletter: posts, subscribers, stats.", "Beehiiv: my newsletter, subscribers and stats",
			"The free plan reads only; drafts need a paid plan. Sending is done in Beehiiv."}},
	{id: "kit", title: "Kit", url: "https://app.kit.com/mcp", cat: "social", paid: true,
		fr: [3]string{"Newsletter Kit (ConvertKit) : abonnés, envois, stats.", "Kit : ma newsletter, abonnés et envois",
			"Plans Kit payants (Creator, Pro). L'envoi se confirme dans Kit."},
		en: [3]string{"Kit (ConvertKit) newsletter: subscribers, broadcasts, stats.", "Kit: my newsletter, subscribers and broadcasts",
			"Paid Kit plans (Creator, Pro). Sending is confirmed in Kit."}},

	// Payments and finance.
	{id: "stripe", title: "Stripe", url: "https://mcp.stripe.com", cat: "finance",
		fr: [3]string{"Paiements, clients et factures Stripe.", "Stripe : paiements, clients et factures"},
		en: [3]string{"Stripe payments, customers and invoices.", "Stripe: payments, customers and invoices"}},
	{id: "paypal", title: "PayPal", url: "https://mcp.paypal.com/mcp", cat: "finance",
		fr: [3]string{"Transactions et factures PayPal.", "PayPal : transactions et factures"},
		en: [3]string{"PayPal transactions and invoices.", "PayPal: transactions and invoices"}},
	{id: "mercury", title: "Mercury", url: "https://mcp.mercury.com/mcp", cat: "finance",
		fr: [3]string{"Comptes et transactions Mercury.", "Mercury : comptes et transactions", "Mercury est une banque pour les entreprises, aux États-Unis."},
		en: [3]string{"Mercury accounts and transactions.", "Mercury: accounts and transactions", "Mercury is a bank for businesses, in the United States."}},
	{id: "ramp", title: "Ramp", url: "https://mcp.ramp.com/mcp", cat: "finance",
		fr: [3]string{"Dépenses, cartes et factures Ramp.", "Ramp : dépenses, cartes et factures", "Ramp est fait pour les entreprises, aux États-Unis."},
		en: [3]string{"Ramp spend, cards and bills.", "Ramp: spend, cards and bills", "Ramp is for businesses, in the United States."}},

	// Automation.
	{id: "zapier", title: "Zapier", url: "https://mcp.zapier.com/api/mcp/mcp", cat: "automation",
		fr: [3]string{"Des actions dans des milliers d'applis, par Zapier.", "Zapier : des actions dans mes autres applis", "Les actions que {{name}} peut faire se choisissent chez Zapier."},
		en: [3]string{"Actions in thousands of apps, through Zapier.", "Zapier: actions in my other apps", "The actions {{name}} may take are picked at Zapier."}},

	{id: "make", title: "Make", url: "https://mcp.make.com/mcp", cat: "automation",
		fr: [3]string{"Tes scénarios Make, lancés comme des actions.", "Make : lancer mes scénarios", "{{name}} voit les scénarios que tu lui donnes chez Make."},
		en: [3]string{"Your Make scenarios, run as actions.", "Make: run my scenarios", "{{name}} sees the scenarios you give it at Make."}},

	// Other.
	{id: "strava", title: "Strava", url: "https://mcp.strava.com/mcp", cat: "other", readsOnly: true,
		fr: [3]string{"Tes activités sportives Strava.", "Strava : mes activités sportives"},
		en: [3]string{"Your Strava activities.", "Strava: my activities"}},
	{id: "exa", title: "Exa", url: "https://mcp.exa.ai/mcp", cat: "other", auth: "none",
		fr: [3]string{"Recherche sur le web.", "Exa : chercher sur le web", "Marche sans compte, dans une limite d'usage."},
		en: [3]string{"Web search.", "Exa: search the web", "Works without an account, within a usage limit."}},
}

// The categories of the presets that come with Eriphe.
var presetCategories = map[string]string{"linear": "tasks", "notion": "tasks", "mail": "mail", "calendar": "mail",
	"gdrive": "files", "github": "dev", "home-assistant": "home"}

func init() {
	at := len(toolPresets)
	for i, p := range toolPresets {
		toolPresets[i].Category = presetCategories[p.ID]
		if p.ID == "remote" && at == len(toolPresets) {
			at = i
		}
	}
	var add []toolPreset
	for _, s := range onlineServices {
		auth := s.auth
		if auth == "" {
			auth = "oauth"
		}
		add = append(add, toolPreset{ID: s.id, Form: "generic", Name: s.id, Mark: letters(s.title), Kind: "remote",
			URL: s.url, Auth: auth, Trust: true, Category: s.cat, Paid: s.paid, Beta: s.beta, ReadsOnly: s.readsOnly})
		for lang, w := range map[string][3]string{"fr": s.fr, "en": s.en} {
			m := webStrings[lang]
			m["preset."+s.id+".title"], m["preset."+s.id+".desc"], m["preset."+s.id+".use"] = s.title, w[0], w[1]
			help := w[2]
			if auth == "oauth" {
				help = fmt.Sprintf(map[string]string{
					"fr": "Choisis comment l'appeler et ce que {{name}} peut faire seul, puis connecte-toi à %s.",
					"en": "Pick what to call it and what {{name}} may do on its own, then sign in to %s.",
				}[lang], s.title) + " " + help
			}
			help = strings.TrimSpace(help)
			if help != "" {
				m["preset."+s.id+".help"] = help
			}
		}
	}
	toolPresets = append(toolPresets[:at], append(add, toolPresets[at:]...)...)
}

// letters is a service's letter mark, when it has no logo: its first
// letter, or the first of two words.
func letters(title string) string {
	r := []rune(title)
	for i := 1; i < len(r)-1; i++ {
		if r[i] == ' ' {
			return string(r[0]) + string(r[i+1])
		}
	}
	return string(r[0])
}
