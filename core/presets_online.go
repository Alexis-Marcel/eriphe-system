package main

import (
	"fmt"
	"strings"
)

// The catalog of online tools: services that host their own MCP server and
// let Eriphe in like Linear, with OAuth and a client registered on the fly
// (dynamic client registration, or a Client ID Metadata Document), so that
// no app is made at the vendor. Each address, its registration and its
// scopes were read from the service's public metadata on 2026-10-30, and
// on 2026-10-31 for the second and third batches (Akiflow to Square, then
// Baserow to IFTTT), found in the MCP registry or the vendor's docs
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
	{id: "akiflow", title: "Akiflow", url: "https://mcp.akiflow.com/mcp", cat: "tasks", paid: true,
		fr: [3]string{"Tâches, agenda et créneaux Akiflow.", "Akiflow : mes tâches et mon planning", "Akiflow est payant, après un essai gratuit."},
		en: [3]string{"Akiflow tasks, calendar and time slots.", "Akiflow: my tasks and schedule", "Akiflow is paid, after a free trial."}},
	{id: "sunsama", title: "Sunsama", url: "https://api.sunsama.com/mcp", cat: "tasks", paid: true,
		fr: [3]string{"Ton planning du jour et tes tâches Sunsama.", "Sunsama : mon planning et mes tâches", "Sunsama est payant, après un essai gratuit."},
		en: [3]string{"Your daily plan and tasks in Sunsama.", "Sunsama: my plan and tasks", "Sunsama is paid, after a free trial."}},
	{id: "basecamp", title: "Basecamp", url: "https://mcp.basecamp.com/mcp", cat: "tasks",
		fr: [3]string{"Projets, to-dos, messages et documents Basecamp.", "Basecamp : mes projets et to-dos"},
		en: [3]string{"Basecamp projects, to-dos, messages and docs.", "Basecamp: my projects and to-dos"}},
	{id: "coda", title: "Coda", url: "https://coda.io/apis/mcp", cat: "tasks",
		fr: [3]string{"Docs, tables et pages Coda.", "Coda : mes docs et tables"},
		en: [3]string{"Coda docs, tables and pages.", "Coda: my docs and tables"}},
	{id: "craft", title: "Craft", url: "https://mcp.craft.do/my/mcp", cat: "tasks",
		fr: [3]string{"Tes documents et notes Craft.", "Craft : mes documents"},
		en: [3]string{"Your Craft documents and notes.", "Craft: my documents"}},
	{id: "capacities", title: "Capacities", url: "https://api.capacities.io/mcp", cat: "tasks",
		fr: [3]string{"Objets, notes du jour et espaces Capacities.", "Capacities : mes notes"},
		en: [3]string{"Capacities objects, daily notes and spaces.", "Capacities: my notes"}},
	{id: "slite", title: "Slite", url: "https://api.slite.com/mcp", cat: "tasks",
		fr: [3]string{"La base de connaissances de ton équipe, dans Slite.", "Slite : mes documents d'équipe"},
		en: [3]string{"Your team's knowledge base, in Slite.", "Slite: my team docs"}},
	{id: "baserow", title: "Baserow", url: "https://api.baserow.io/mcp", cat: "tasks",
		fr: [3]string{"Bases, tables et lignes Baserow.", "Baserow : mes bases et tables", "Pour Baserow en ligne (baserow.io). Une installation à toi a sa propre adresse."},
		en: [3]string{"Baserow databases, tables and rows.", "Baserow: my databases and tables", "For Baserow online (baserow.io). A self-hosted one has its own address."}},
	{id: "guru", title: "Guru", url: "https://mcp.api.getguru.com/mcp", cat: "tasks", paid: true,
		fr: [3]string{"La base de connaissances de ton équipe, dans Guru.", "Guru : les fiches de mon équipe", "Guru est payant, après un essai gratuit."},
		en: [3]string{"Your team's knowledge base, in Guru.", "Guru: my team's cards", "Guru is paid, after a free trial."}},

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
	{id: "egnyte", title: "Egnyte", url: "https://mcp-server.egnyte.com/mcp", cat: "files", paid: true,
		fr: [3]string{"Chercher et lire les fichiers Egnyte de ton entreprise.", "Egnyte : les fichiers de mon entreprise",
			"Abonnement Egnyte pour entreprises. Un administrateur doit d'abord autoriser l'accès des IA (MCP) dans les réglages d'Egnyte."},
		en: [3]string{"Search and read your company's Egnyte files.", "Egnyte: my company's files",
			"An Egnyte business plan. An admin must first allow AI (MCP) access in Egnyte's settings."}},
	{id: "cloudinary", title: "Cloudinary", url: "https://asset-management.mcp.cloudinary.com/mcp", cat: "files",
		fr: [3]string{"Images et vidéos de ta médiathèque Cloudinary.", "Cloudinary : mes images et vidéos"},
		en: [3]string{"Images and videos of your Cloudinary media library.", "Cloudinary: my images and videos"}},

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
	{id: "fathom", title: "Fathom", url: "https://api.fathom.ai/mcp", cat: "meetings",
		fr: [3]string{"Notes et transcriptions de réunion Fathom.", "Fathom : mes réunions"},
		en: [3]string{"Fathom meeting notes and transcripts.", "Fathom: my meetings"}},
	{id: "readai", title: "Read AI", url: "https://api.read.ai/mcp", cat: "meetings",
		fr: [3]string{"Comptes rendus et transcriptions Read AI.", "Read AI : mes comptes rendus de réunion"},
		en: [3]string{"Read AI meeting reports and transcripts.", "Read AI: my meeting reports"}},
	{id: "grain", title: "Grain", url: "https://api.grain.com/_/mcp", cat: "meetings",
		fr: [3]string{"Enregistrements, transcriptions et notes de réunion Grain.", "Grain : mes réunions enregistrées", "{{name}} voit les réunions auxquelles tu as participé."},
		en: [3]string{"Grain recordings, transcripts and meeting notes.", "Grain: my recorded meetings", "{{name}} sees the meetings you took part in."}},
	{id: "wave", title: "Wave", url: "https://mcp.wave.co", cat: "meetings",
		fr: [3]string{"Réunions, appels et notes enregistrés par Wave.", "Wave : mes réunions et notes", "Wave, la prise de notes par IA de wave.co."},
		en: [3]string{"Meetings, calls and notes recorded by Wave.", "Wave: my meetings and notes", "Wave, the AI note taker of wave.co."}},

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
	{id: "whimsical", title: "Whimsical", url: "https://mcp.whimsical.com/mcp", cat: "design",
		fr: [3]string{"Diagrammes, cartes mentales et maquettes Whimsical.", "Whimsical : mes diagrammes et maquettes"},
		en: [3]string{"Whimsical flowcharts, mind maps and wireframes.", "Whimsical: my diagrams and wireframes"}},
	{id: "mural", title: "Mural", url: "https://mcp-canvas.mural.co/mcp", cat: "design",
		fr: [3]string{"Tableaux blancs Mural.", "Mural : mes tableaux"},
		en: [3]string{"Mural whiteboards.", "Mural: my boards"}},
	{id: "lucid", title: "Lucid", url: "https://mcp.lucid.app/mcp", cat: "design",
		fr: [3]string{"Diagrammes Lucidchart et tableaux Lucidspark.", "Lucid : mes diagrammes"},
		en: [3]string{"Lucidchart diagrams and Lucidspark boards.", "Lucid: my diagrams"}},
	{id: "padlet", title: "Padlet", url: "https://mcp.padlet.com/", cat: "design",
		fr: [3]string{"Tes murs et tableaux Padlet.", "Padlet : mes padlets"},
		en: [3]string{"Your Padlet boards and walls.", "Padlet: my padlets"}},

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
	{id: "neon", title: "Neon", url: "https://mcp.neon.tech/mcp", cat: "dev",
		fr: [3]string{"Projets et bases Postgres Neon.", "Neon : mes bases de données Postgres"},
		en: [3]string{"Neon Postgres projects and databases.", "Neon: my Postgres databases"}},
	{id: "posthog", title: "PostHog", url: "https://mcp.posthog.com/mcp", cat: "dev",
		fr: [3]string{"Analyses produit, feature flags et erreurs PostHog.", "PostHog : mes analyses produit"},
		en: [3]string{"PostHog product analytics, feature flags and errors.", "PostHog: my product analytics"}},
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
	{id: "amplitude", title: "Amplitude", url: "https://mcp.amplitude.com/mcp", cat: "dev",
		fr: [3]string{"Graphiques, tableaux de bord et analyses produit Amplitude.", "Amplitude : mes analyses produit", "Pour les comptes Amplitude aux États-Unis."},
		en: [3]string{"Amplitude charts, dashboards and product analytics.", "Amplitude: my product analytics", "For Amplitude accounts in the United States."}},
	{id: "grafana", title: "Grafana", url: "https://mcp.grafana.com/mcp", cat: "dev",
		fr: [3]string{"Tableaux de bord, métriques et alertes Grafana Cloud.", "Grafana : mes tableaux de bord et métriques", "Pour Grafana Cloud. Un Grafana installé chez toi n'y est pas."},
		en: [3]string{"Grafana Cloud dashboards, metrics and alerts.", "Grafana: my dashboards and metrics", "For Grafana Cloud. A self-hosted Grafana is not covered."}},
	{id: "honeycomb", title: "Honeycomb", url: "https://mcp.honeycomb.io/mcp", cat: "dev",
		fr: [3]string{"Traces, requêtes et alertes Honeycomb.", "Honeycomb : l'observabilité de mes applis"},
		en: [3]string{"Honeycomb traces, queries and triggers.", "Honeycomb: my apps' observability"}},
	{id: "axiom", title: "Axiom", url: "https://mcp.axiom.co/mcp", cat: "dev",
		fr: [3]string{"Journaux et événements de tes jeux de données Axiom.", "Axiom : mes journaux"},
		en: [3]string{"Logs and events of your Axiom datasets.", "Axiom: my logs"}},
	{id: "datadog", title: "Datadog", url: "https://mcp.datadoghq.com/v1/mcp", cat: "dev",
		fr: [3]string{"Journaux, métriques, moniteurs et incidents Datadog.", "Datadog : la surveillance de mes applis",
			"Pour le site Datadog US1 (datadoghq.com). Un administrateur doit activer l'accès MCP, et l'écriture MCP pour créer ou modifier."},
		en: [3]string{"Datadog logs, metrics, monitors and incidents.", "Datadog: my apps' monitoring",
			"For the Datadog US1 site (datadoghq.com). An admin must enable MCP access, and MCP write access to create or change things."}},
	{id: "hex", title: "Hex", url: "https://app.hex.tech/mcp", cat: "dev", paid: true,
		fr: [3]string{"Projets, données et analyses Hex.", "Hex : mes analyses de données", "Demande un abonnement Hex Team ou Enterprise."},
		en: [3]string{"Hex projects, data and analyses.", "Hex: my data analyses", "Needs a Hex Team or Enterprise plan."}},
	{id: "railway", title: "Railway", url: "https://mcp.railway.com/", cat: "dev",
		fr: [3]string{"Projets, services, déploiements et journaux Railway.", "Railway : mes projets et déploiements", "Sur l'écran de Railway, tu choisis les espaces et projets que {{name}} voit."},
		en: [3]string{"Railway projects, services, deployments and logs.", "Railway: my projects and deployments", "On Railway's screen, you pick the workspaces and projects {{name}} sees."}},
	{id: "digitalocean", title: "DigitalOcean", url: "https://mcp.digitalocean.com/mcp", cat: "dev",
		fr: [3]string{"Droplets, applis, bases et Spaces DigitalOcean.", "DigitalOcean : mes serveurs et applis",
			"Sur l'écran de DigitalOcean, tu choisis ce que {{name}} peut faire. Créer des ressources se paie sur ton compte."},
		en: [3]string{"DigitalOcean Droplets, apps, databases and Spaces.", "DigitalOcean: my servers and apps",
			"On DigitalOcean's screen, you choose what {{name}} may do. Resources it creates are billed to your account."}},
	{id: "planetscale", title: "PlanetScale", url: "https://mcp.pscale.dev/mcp/planetscale", cat: "dev", paid: true,
		fr: [3]string{"Bases, branches et requêtes PlanetScale.", "PlanetScale : mes bases de données",
			"Sur l'écran de PlanetScale, tu choisis pour chaque organisation ou base : aucun accès, lecture seule ou complet."},
		en: [3]string{"PlanetScale databases, branches and queries.", "PlanetScale: my databases",
			"On PlanetScale's screen, you choose for each organization or database: no access, read only or full."}},
	{id: "typesense", title: "Typesense", url: "https://cloud.typesense.org/mcp/v1", cat: "dev", paid: true,
		fr: [3]string{"Clusters, collections et recherches Typesense Cloud.", "Typesense : mes clusters de recherche", "Pour Typesense Cloud, payant à l'usage."},
		en: [3]string{"Typesense Cloud clusters, collections and searches.", "Typesense: my search clusters", "For Typesense Cloud, paid by use."}},
	{id: "lovable", title: "Lovable", url: "https://mcp.lovable.dev", cat: "dev",
		fr: [3]string{"Tes projets d'applis Lovable.", "Lovable : mes projets d'applis", "Faire évoluer une appli consomme tes crédits Lovable."},
		en: [3]string{"Your Lovable app projects.", "Lovable: my app projects", "Changing an app uses your Lovable credits."}},
	{id: "mintlify", title: "Mintlify", url: "https://mcp.mintlify.com", cat: "dev",
		fr: [3]string{"Pages, navigation et déploiements de ta doc Mintlify.", "Mintlify : ma documentation"},
		en: [3]string{"Pages, navigation and deployments of your Mintlify docs.", "Mintlify: my docs"}},
	{id: "postman", title: "Postman", url: "https://mcp.postman.com/mcp", cat: "dev",
		fr: [3]string{"Collections, requêtes et environnements Postman.", "Postman : mes collections d'API"},
		en: [3]string{"Postman collections, requests and environments.", "Postman: my API collections"}},
	{id: "apify", title: "Apify", url: "https://mcp.apify.com/", cat: "dev",
		fr: [3]string{"Les Actors Apify : extraire des données du web.", "Apify : extraire des données du web", "Lancer un Actor consomme les crédits de ton compte Apify."},
		en: [3]string{"Apify Actors: extract data from the web.", "Apify: extract data from the web", "Running an Actor uses your Apify account's credits."}},

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
	{id: "close", title: "Close", url: "https://mcp.close.com/mcp", cat: "web", paid: true,
		fr: [3]string{"Ton CRM Close : prospects, affaires, activités.", "Close : mes prospects et affaires", "Close est payant, après un essai gratuit."},
		en: [3]string{"Your Close CRM: leads, opportunities, activities.", "Close: my leads and opportunities", "Close is paid, after a free trial."}},
	{id: "pandadoc", title: "PandaDoc", url: "https://mcp.pandadoc.com/v1/mcp", cat: "web",
		fr: [3]string{"Devis, contrats et signatures PandaDoc.", "PandaDoc : mes devis et contrats"},
		en: [3]string{"PandaDoc proposals, contracts and signatures.", "PandaDoc: my proposals and contracts"}},
	{id: "typeform", title: "Typeform", url: "https://api.typeform.com/mcp", cat: "web",
		fr: [3]string{"Formulaires et réponses Typeform.", "Typeform : mes formulaires et réponses"},
		en: [3]string{"Typeform forms and responses.", "Typeform: my forms and responses"}},
	{id: "jotform", title: "Jotform", url: "https://mcp.jotform.com/", cat: "web",
		fr: [3]string{"Formulaires et réponses Jotform.", "Jotform : mes formulaires et réponses"},
		en: [3]string{"Jotform forms and submissions.", "Jotform: my forms and submissions"}},
	{id: "framer", title: "Framer", url: "https://mcp.framer.com/mcp", cat: "web",
		fr: [3]string{"Tes sites Framer : pages, CMS, composants.", "Framer : mes sites"},
		en: [3]string{"Your Framer sites: pages, CMS, components.", "Framer: my sites"}},
	{id: "wordpress", title: "WordPress.com", url: "https://public-api.wordpress.com/wpcom/v2/mcp/v1", cat: "web",
		fr: [3]string{"Tes sites WordPress.com : articles, pages, réglages.", "WordPress.com : mes sites",
			"Les sites WordPress installés ailleurs passent par Jetpack."},
		en: [3]string{"Your WordPress.com sites: posts, pages, settings.", "WordPress.com: my sites",
			"Self-hosted WordPress sites go through Jetpack."}},
	{id: "upwork", title: "Upwork", url: "https://mcp.upwork.com/mcp", cat: "web",
		fr: [3]string{"Ton activité de freelance sur Upwork.", "Upwork : mon activité de freelance"},
		en: [3]string{"Your freelance work on Upwork.", "Upwork: my freelance work"}},
	{id: "sanity", title: "Sanity", url: "https://mcp.sanity.io/", cat: "web",
		fr: [3]string{"Contenus et schémas de tes projets Sanity.", "Sanity : mes contenus"},
		en: [3]string{"Content and schemas of your Sanity projects.", "Sanity: my content"}},
	{id: "contentful", title: "Contentful", url: "https://mcp.contentful.com/mcp", cat: "web",
		fr: [3]string{"Entrées, médias et modèles de contenu Contentful.", "Contentful : mes contenus"},
		en: [3]string{"Contentful entries, assets and content models.", "Contentful: my content"}},
	{id: "leadpages", title: "Leadpages", url: "https://mcp.leadpages.com/mcp", cat: "web", paid: true,
		fr: [3]string{"Pages d'atterrissage, sites et blogs Leadpages.", "Leadpages : mes pages et sites", "Leadpages est payant, après un essai gratuit."},
		en: [3]string{"Leadpages landing pages, sites and blogs.", "Leadpages: my pages and sites", "Leadpages is paid, after a free trial."}},
	{id: "kajabi", title: "Kajabi", url: "https://mcp.kajabi.com/mcp", cat: "web", paid: true,
		fr: [3]string{"Formations, offres, contacts et mails Kajabi.", "Kajabi : mes formations et contacts",
			"Kajabi est payant. Ce que {{name}} écrit arrive en brouillon ; le thème et les utilisateurs du compte restent hors de sa portée."},
		en: [3]string{"Kajabi courses, offers, contacts and emails.", "Kajabi: my courses and contacts",
			"Kajabi is paid. What {{name}} writes lands as drafts; the theme and the account's users stay out of its reach."}},
	{id: "helpscout", title: "Help Scout", url: "https://mcp.helpscout.net/mcp", cat: "web",
		fr: [3]string{"Conversations et clients du support, dans Help Scout.", "Help Scout : le support client"},
		en: [3]string{"Support conversations and customers, in Help Scout.", "Help Scout: customer support"}},
	{id: "plain", title: "Plain", url: "https://mcp.plain.com/mcp", cat: "web",
		fr: [3]string{"Fils, clients et tickets du support, dans Plain.", "Plain : le support client"},
		en: [3]string{"Support threads, customers and tickets, in Plain.", "Plain: customer support"}},
	{id: "similarweb", title: "Similarweb", url: "https://mcp.similarweb.com/", cat: "web", paid: true,
		fr: [3]string{"Trafic et audience des sites web, par Similarweb.", "Similarweb : le trafic des sites web", "Demande un abonnement Similarweb avec accès aux données."},
		en: [3]string{"Website traffic and audience, from Similarweb.", "Similarweb: website traffic", "Needs a Similarweb plan with data access."}},
	{id: "bitly", title: "Bitly", url: "https://api-ssl.bitly.com/v4/mcp", cat: "web",
		fr: [3]string{"Liens courts, QR codes et clics Bitly.", "Bitly : mes liens et leurs clics"},
		en: [3]string{"Bitly short links, QR codes and clicks.", "Bitly: my links and their clicks"}},

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
	{id: "circle", title: "Circle", url: "https://app.circle.so/api/mcp", cat: "social", paid: true,
		fr: [3]string{"Ta communauté Circle : espaces, posts, membres.", "Circle : ma communauté", "Circle est payant, après un essai gratuit."},
		en: [3]string{"Your Circle community: spaces, posts, members.", "Circle: my community", "Circle is paid, after a free trial."}},
	{id: "mailerlite", title: "MailerLite", url: "https://mcp.mailerlite.com/mcp", cat: "social",
		fr: [3]string{"Newsletter MailerLite : abonnés, campagnes, stats.", "MailerLite : ma newsletter, abonnés et campagnes"},
		en: [3]string{"MailerLite newsletter: subscribers, campaigns, stats.", "MailerLite: my newsletter, subscribers and campaigns"}},
	{id: "klaviyo", title: "Klaviyo", url: "https://mcp.klaviyo.com/mcp", cat: "social",
		fr: [3]string{"Mails et SMS marketing Klaviyo : profils, campagnes, stats.", "Klaviyo : mes campagnes et profils"},
		en: [3]string{"Klaviyo email and SMS marketing: profiles, campaigns, stats.", "Klaviyo: my campaigns and profiles"}},

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
	{id: "qonto", title: "Qonto", url: "https://mcp.qonto.com/mcp", cat: "finance", paid: true,
		fr: [3]string{"Comptes, transactions et factures Qonto.", "Qonto : comptes, transactions et factures", "Qonto est une banque en ligne pour les pros, en Europe."},
		en: [3]string{"Qonto accounts, transactions and invoices.", "Qonto: accounts, transactions and invoices", "Qonto is an online bank for businesses, in Europe."}},
	{id: "pennylane", title: "Pennylane", url: "https://mcp.pennylane.com/mcp", cat: "finance", paid: true, beta: true,
		fr: [3]string{"Ta comptabilité Pennylane : factures, écritures, TVA.", "Pennylane : ma comptabilité", "Le serveur de Pennylane est récent et pas encore documenté par Pennylane."},
		en: [3]string{"Your Pennylane accounting: invoices, entries, VAT.", "Pennylane: my accounting", "Pennylane's server is new and not documented by Pennylane yet."}},
	{id: "square", title: "Square", url: "https://mcp.squareup.com/mcp", cat: "finance",
		fr: [3]string{"Paiements, commandes et clients Square.", "Square : paiements, commandes et clients"},
		en: [3]string{"Square payments, orders and customers.", "Square: payments, orders and customers"}},
	{id: "paddle", title: "Paddle", url: "https://mcp.paddle.com/mcp", cat: "finance",
		fr: [3]string{"Produits, clients, abonnements et transactions Paddle.", "Paddle : mes ventes et abonnements",
			"La connexion ne fait que lire au départ. Pour que {{name}} puisse écrire, élargis-la chez Paddle (Paddle > Connectors > MCP)."},
		en: [3]string{"Paddle products, customers, subscriptions and transactions.", "Paddle: my sales and subscriptions",
			"The connection only reads at first. For {{name}} to write, widen it at Paddle (Paddle > Connectors > MCP)."}},

	// Automation.
	{id: "zapier", title: "Zapier", url: "https://mcp.zapier.com/api/mcp/mcp", cat: "automation",
		fr: [3]string{"Des actions dans des milliers d'applis, par Zapier.", "Zapier : des actions dans mes autres applis", "Les actions que {{name}} peut faire se choisissent chez Zapier."},
		en: [3]string{"Actions in thousands of apps, through Zapier.", "Zapier: actions in my other apps", "The actions {{name}} may take are picked at Zapier."}},

	{id: "make", title: "Make", url: "https://mcp.make.com/mcp", cat: "automation",
		fr: [3]string{"Tes scénarios Make, lancés comme des actions.", "Make : lancer mes scénarios", "{{name}} voit les scénarios que tu lui donnes chez Make."},
		en: [3]string{"Your Make scenarios, run as actions.", "Make: run my scenarios", "{{name}} sees the scenarios you give it at Make."}},
	{id: "ifttt", title: "IFTTT", url: "https://ifttt.com/mcp", cat: "automation",
		fr: [3]string{"Tes applets IFTTT et les actions de tes services connectés.", "IFTTT : lancer des actions dans mes services"},
		en: [3]string{"Your IFTTT applets and the actions of your connected services.", "IFTTT: run actions in my services"}},

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
