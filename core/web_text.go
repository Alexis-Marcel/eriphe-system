package main

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// The workspace's words, in French first and English as an option, and the
// helpers its templates use.

var webStrings = map[string]map[string]string{
	"fr": {
		"app.tagline":              "Ton assistant, chez toi.",
		"nav.chat":                 "Conversation",
		"nav.tasks":                "Tâches",
		"nav.settings":             "Réglages",
		"nav.logout":               "Se déconnecter",
		"nav.main":                 "Navigation principale",
		"login.title":              "Connexion",
		"login.lead":               "Entre ton mot de passe pour ouvrir ton espace.",
		"login.password":           "Mot de passe",
		"login.submit":             "Se connecter",
		"setup.title":              "Bienvenue",
		"setup.lead":               "Choisis le mot de passe de ton espace. Le code d'installation s'affiche à la fin de l'installation ; perdu, cette commande le redonne sur la machine d'Eriphe : %s",
		"setup.code":               "Code d'installation",
		"setup.password":           "Nouveau mot de passe",
		"setup.confirm":            "Confirme le mot de passe",
		"setup.hint":               "10 caractères au moins. Une phrase de quelques mots est idéale.",
		"setup.submit":             "Créer mon espace",
		"err.csrf":                 "La page a expiré : recharge-la et réessaie.",
		"err.toomany":              "Trop d'essais. Réessaie dans un quart d'heure.",
		"err.password":             "Mot de passe incorrect.",
		"err.setupcode":            "Code d'installation incorrect.",
		"err.short":                "Le mot de passe doit faire 10 caractères au moins.",
		"err.mismatch":             "Les deux mots de passe ne sont pas identiques.",
		"err.internal":             "Une erreur est survenue. Réessaie.",
		"chat.label":               "Conversation avec {{name}}",
		"chat.hello":               "Bonjour %s",
		"chat.hellononame":         "Bonjour",
		"chat.empty":               "Dis à {{name}} ce que tu veux faire. Il confie le travail à ses agents, sur tes appareils, et te demande ton accord quand il le faut.",
		"chat.ex1":                 "Corrige le bug de la page de contact sur mon site",
		"chat.ex2":                 "Résume les nouveautés de mes projets cette semaine",
		"chat.ex3":                 "Prépare un devis à partir de mes notes",
		"chat.placeholder":         "Écris à {{name}}…",
		"chat.send":                "Envoyer",
		"chat.you":                 "Toi",
		"chat.eriphe":              "{{name}}",
		"chat.event":               "Événement",
		"chat.thinking":            "{{name}} réfléchit…",
		"chat.nobrain":             "{{name}} n'est pas encore configuré sur ce serveur : tes messages attendront qu'il le soit.",
		"chat.noai":                "{{name}} n'a pas encore d'IA pour réfléchir : tes messages attendent qu'il en ait une.",
		"chat.noai.link":           "Choisir son IA",
		"chat.brainoff":            "{{name}} n'est pas disponible pour l'instant : il lira tes messages dès son retour.",
		"chat.offline":             "Connexion perdue. Nouvel essai…",
		"chat.earlier":             "Messages plus anciens non affichés.",
		"perm.title":               "{{name}} a besoin de ton accord",
		"perm.yours":               "Cette décision te revient",
		"perm.wants":               "veut utiliser",
		"perm.reason":              "Raison (facultatif)",
		"perm.reasonhint":          "Transmise à l'agent, par exemple pour lui proposer une autre façon de faire.",
		"perm.allow":               "Oui, autoriser",
		"perm.deny":                "Non, refuser",
		"perm.allowed":             "Autorisé.",
		"perm.always":              "Toujours autoriser",
		"perm.alwayshint":          "Autoriser, et ne plus demander pour cette action de cet outil (retirable dans Réglages → Outils → Accès).",
		"perm.remembered":          "Autorisé, et sans demander à l'avenir.",
		"perm.denied":              "Refusé.",
		"perm.changed":             "Cette demande a changé entre-temps.",
		"perm.task":                "Voir la tâche",
		"tasks.label":              "Tâches de {{name}}",
		"tasks.close":              "Fermer les tâches",
		"tasks.title":              "Tâches",
		"tasks.none":               "Aucune tâche en cours. Quand {{name}} confie un travail à un agent, il apparaît ici.",
		"tasks.done":               "Terminées récemment",
		"tasks.waitnode":           "En attente d'un appareil",
		"tasks.needsyou":           "Attend ta réponse",
		"tasks.open":               "Ouvrir la tâche",
		"task.close":               "Fermer",
		"task.prompt":              "Consigne",
		"task.activity":            "Activité",
		"task.noactivity":          "Rien encore.",
		"task.reply":               "Répondre à l'agent",
		"task.replyhint":           "L'agent a fini son tour. Ta réponse lui donne la suite.",
		"task.replysend":           "Envoyer",
		"task.sent":                "Message envoyé à l'agent.",
		"task.stop":                "Arrêter la tâche",
		"task.stopconfirm":         "Arrêter cette tâche ? L'agent s'arrête où il en est.",
		"task.stopping":            "Arrêt demandé.",
		"task.node":                "Appareil",
		"task.agent":               "Agent",
		"task.repo":                "Dépôt",
		"task.branch":              "Branche",
		"task.ran":                 "a lancé",
		"task.allowed":             "autorisé",
		"task.denied":              "refusé",
		"task.turnend":             "Tour terminé",
		"task.error":               "Erreur",
		"state.pending":            "En attente",
		"state.running":            "En cours",
		"state.waiting_permission": "Demande ton accord",
		"state.idle":               "Tour terminé",
		"state.stopped":            "Terminée",
		"state.failed":             "Échouée",
		"state.lost":               "Perdue",
		"settings.title":           "Réglages",
		"settings.close":           "Fermer les réglages",
		"settings.machines":        "Appareils",
		"settings.agents":          "Agents",
		"settings.accounts":        "Comptes et outils",
		"settings.journal":         "Journal",
		"settings.g.assistant":     "Assistant",
		"settings.g.connections":   "Connexions",
		"settings.g.devices":       "Appareils & accès",
		"settings.g.space":         "Mon espace",
		"settings.accountnav":      "Compte et langue",
		"settings.attn":            "À vérifier",
		"account.logoutlead":       "Ferme ta session sur ce navigateur.",
		"settings.account":         "Mon espace",
		"machines.lead":            "Les appareils que tu as connectés à {{name}} : il t'aide avec ce qui s'y trouve, et peut y faire travailler ses agents.",
		"machines.none":            "Aucun appareil pour l'instant. Connectes-en un : {{name}} pourra t'aider avec ce qui s'y trouve.",
		"machines.online":          "En ligne",
		"machines.offline":         "Hors ligne",
		"machines.asleep":          "En veille",
		"machines.asleephint":      "Il se déconnecte et revient de lui-même : Eriphe place ailleurs ce qui ne peut pas attendre son réveil.",
		"machines.seen":            "Vu %s",
		"machines.cpus":            "%d processeurs",
		"machines.memory":          "%s de mémoire",
		"machines.runtimes":        "Programmes",
		"machines.labels":          "Étiquettes",
		"machines.alwayson":        "Toujours allumé",
		"machines.model":           "Prête un modèle",
		"machines.modelhint":       "Cet appareil prête son serveur de modèles (--model-server) : tes agents le joignent par son tunnel. Choisis-le dans Agents → Modèle auto-hébergé.",
		"machines.actions":         "Actions pour %s",
		"machines.alwaysonhint":    "Tes outils tournent ici : les mails et l'agenda doivent répondre à toute heure, ils vont sur les appareils qui ne s'éteignent pas.",
		"machines.active":          "%d tâche(s) active(s)",
		"machines.add":             "Connecter un appareil",
		"devices.addhint":          "Un NAS, un PC, un Mac ou un Raspberry : une commande à lancer dessus.",
		"machines.addlead":         "Donne-lui un nom court (lettres minuscules, chiffres, tirets), puis lance la commande affichée sur l'appareil. {{name}} n'y verra que les dossiers que tu ouvriras. Le jeton ne sert qu'une fois, pour ce nom seulement, et expire dans 24 h.",
		"machines.name":            "Nom de l'appareil",
		"machines.labelsopt":       "Étiquettes (facultatif, ex. gpu)",
		"machines.generate":        "Générer la commande",
		"machines.badname":         "Le nom doit faire 1 à 32 caractères : lettres minuscules, chiffres et tirets.",
		"machines.unix":            "Linux ou macOS",
		"machines.unixlead":        "Dans un terminal sur l'appareil. Le script installe ce qu'il faut (Docker sous Linux, une machine virtuelle sous macOS) ; relancé, il met à jour.",
		"machines.linux":           "Docker Compose (NAS, serveur)",
		"machines.linuxlead":       "Dans le compose.yaml du nœud, puis docker compose up -d :",
		"machines.binary":          "Programme seul (Linux ou macOS)",
		"machines.windows":         "Windows",
		"machines.windowslead":     "Dans un PowerShell administrateur. Le script installe WSL s'il manque ; relancé, il met à jour.",
		"machines.windowsfile":     "Windows (script téléchargé)",
		"machines.windowsfilelead": "Dans un PowerShell administrateur, à côté d'install.ps1 :",
		"machines.expires":         "Jeton valable jusqu'au %s, une seule fois.",
		"machines.copy":            "Copier",
		"voice.lead":               "La voix de Home Assistant (Assist) transmet à {{name}} ce qu'elle ne traite pas elle-même, et lit sa réponse à voix haute. Crée un jeton, puis ajoute l'intégration « Eriphe » dans Home Assistant avec cette adresse et ce jeton.",
		"voice.only":               "Ce jeton ne sert qu'à parler au cerveau de {{name}} : il n'ouvre ni les outils, ni les réglages.",
		"voice.has":                "Home Assistant a un jeton. En créer un nouveau remplace l'ancien.",
		"voice.create":             "Créer le jeton de Home Assistant",
		"voice.replace":            "Remplacer le jeton",
		"voice.replaceconfirm":     "Remplacer le jeton ? Home Assistant ne pourra plus parler à {{name}} tant que tu n'auras pas mis le nouveau.",
		"voice.once":               "Copie le jeton maintenant : il ne sera plus affiché.",
		"voice.url":                "L'adresse à donner à l'intégration Eriphe",
		"voice.token":              "Jeton",
		"ha.title":                 "Home Assistant",
		"ha.lead":                  "{{name}} et ta maison, dans les deux sens.",
		"ha.drive":                 "{{name}} pilote la maison",
		"ha.drivelead":             "{{name}} lit l'état de la maison et agit dessus (lumières, chauffage, volets) par le serveur MCP de Home Assistant, avec un jeton d'accès longue durée.",
		"ha.connect":               "Connecter Home Assistant",
		"ha.voice":                 "La voix de la maison parle à {{name}}",
		"ha.voiceon":               "voix branchée",
		"ha.voiceonly":             "La voix de la maison parle à {{name}}. {{name}} ne pilote pas encore la maison.",
		"ha.manage":                "Gérer",
		"chat.via":                 "dit à voix haute",
		"machines.copied":          "Copié",
		"machines.forget":          "Oublier",
		"machines.forgetconfirm":   "Oublier cet appareil ? Ses tâches seront perdues et il devra être connecté à nouveau.",
		"machines.rejoin":          "Réinscrire cet appareil",
		"machines.rejoinconfirm":   "Réinscrire cet appareil ? Le jeton permet à un appareil de reprendre ce nom, avec ses tâches et ses accès.",
		"machines.rejoinlead":      "Jeton de réinscription pour %s. Si Eriphe est encore installé sur l'appareil, efface d'abord son identité : sous Linux ou macOS, lance le script avec --uninstall (ou supprime node.json dans le dossier du nœud) ; sous Windows, -Rejoin s'en charge. Puis lance la commande. L'ancien secret de l'appareil cessera de fonctionner.",
		"machines.exists":          "Un appareil porte déjà ce nom. Pour le réinstaller, utilise « Réinscrire cet appareil » sur sa fiche.",
		"machines.nonode":          "Aucun appareil ne porte ce nom.",
		"machines.notls":           "Connexion non chiffrée · passera en TLS d'elle-même à sa mise à jour",
		"machines.advanced":        "Avancé",
		"machines.advancedsummary": "L'empreinte de l'autorité de certification d'Eriphe",
		"machines.tlslead":         "Les appareils parlent à Eriphe en TLS, avec un certificat chacun, signé par l'autorité de certification d'Eriphe. Les commandes de connexion donnent déjà son empreinte (-ca-hash) : le nouvel appareil vérifie qu'il parle bien à Eriphe. La voici, pour un appareil installé à la main.",
		"machines.tlsplain":        "Encore sans certificat : %s. Un appareil à jour en obtient un de lui-même à sa prochaine connexion ; quand tous en ont un, Eriphe n'accepte plus que les appareils en TLS.",
		"machines.tlsonly":         "Seuls les appareils en TLS, avec leur certificat, sont acceptés.",
		"system.macos-container":   "macOS (Apple container)",
		"system.macos-lima":        "macOS (VM Lima)",
		"system.windows":           "Windows (WSL)",
		"system.linux":             "Linux",
		"system.compose":           "Linux (Docker Compose)",
		"system.docker":            "Linux (conteneur Eriphe)",
		"system.darwin":            "macOS",
		"system.windows-native":    "Windows",
		"agents.lead":              "Les agents à qui {{name}} peut confier un travail. Ils se règlent dans la configuration d'eriphe-core.",
		"agents.none":              "Aucun agent configuré.",
		"agents.default":           "Par défaut",
		"agents.available":         "Disponible",
		"agents.unavailable":       "Aucun appareil en ligne ne peut le lancer",
		"agents.use":               "Quand",
		"agents.needs":             "A besoin de",
		"agents.prefers":           "Préfère",
		"agents.model":             "Modèle",
		"agents.runtime":           "Programme",
		"accounts.lead":            "Les clés et jetons que {{name}} prête à ses agents. Ils sont chiffrés sur le serveur et ne sont jamais réaffichés.",
		"accounts.vault":           "Coffre",
		"accounts.none":            "Le coffre est vide.",
		"accounts.updated":         "Modifié %s",
		"accounts.ref":             "À utiliser dans la configuration :",
		"accounts.add":             "Ajouter ou remplacer un secret",
		"accounts.name":            "Nom",
		"accounts.namehint":        "Lettres minuscules, chiffres, points, tirets, soulignés. Un nom existant est remplacé.",
		"accounts.value":           "Valeur",
		"accounts.valuehint":       "Collée ici, elle est chiffrée aussitôt et n'est plus jamais affichée.",
		"accounts.save":            "Enregistrer",
		"accounts.saved":           "Secret « %s » enregistré.",
		"accounts.delete":          "Supprimer",
		"accounts.deleteconfirm":   "Supprimer ce secret ? Ce qui l'utilise ne fonctionnera plus.",
		"accounts.deleted":         "Secret supprimé.",
		"accounts.novault":         "Le coffre n'est pas ouvert : voir le journal d'eriphe-core.",
		"accounts.config":          "Secrets de la configuration",
		"accounts.configlead":      "Où la configuration trouve chaque secret. Ceux qui sont encore dans un fichier peuvent être copiés dans le coffre ; remplace ensuite le chemin par la référence vault: indiquée.",
		"accounts.infile":          "Fichier",
		"accounts.invault":         "Coffre",
		"accounts.import":          "Copier dans le coffre",
		"accounts.imported":        "Copié dans le coffre sous « %s ». Remplace le chemin par %s dans la configuration.",
		"accounts.tools":           "Outils connectés",
		"accounts.toolslead":       "Les services en ligne (agenda, mails, documents…) que {{name}} peut utiliser, et s'ils répondent. Ceux qui demandent ton autorisation se connectent ici.",
		"tools.count":              "%d outils",
		"tools.local":              "Sur un appareil",
		"tools.connect":            "Connecter",
		"tools.reconnect":          "Reconnecter",
		"tools.step1":              "Ouvre la page d'autorisation du service et accepte.",
		"tools.open":               "Ouvrir",
		"tools.step2":              "Ton navigateur arrive ensuite sur une page 127.0.0.1 qui ne s'affiche pas : copie son adresse et colle-la ici.",
		"tools.finish":             "Terminer",
		"tools.badurl":             "Ce n'est pas l'adresse attendue : elle contient code= et state=.",
		"tools.connected":          "Connecté : %d outils disponibles.",
		"server.ready":             "Connecté",
		"server.connecting":        "Connexion…",
		"server.needs_auth":        "À connecter",
		"server.error":             "Erreur",
		"auth.oauth":               "autorisation OAuth",
		"auth.bearer":              "jeton",
		"auth.none":                "sans authentification",
		"auth.local":               "local",
		"journal.lead":             "Toutes les décisions sur les demandes des agents : qui a autorisé ou refusé quoi.",
		"journal.none":             "Aucune décision pour l'instant.",
		"journal.when":             "Quand",
		"journal.task":             "Tâche",
		"journal.what":             "Demande",
		"journal.decision":         "Décision",
		"journal.by":               "Par",
		"journal.reason":           "Raison",
		"journal.more":             "Plus anciennes",
		"decision.allow":           "Autorisé",
		"decision.deny":            "Refusé",
		"by.user":                  "Toi",
		"by.brain":                 "{{name}}",
		"by.policy":                "Règle",
		"account.lead":             "Ta langue, ta connexion et ton mot de passe.",
		"account.lang":             "Langue",
		"account.password":         "Changer le mot de passe",
		"account.current":          "Mot de passe actuel",
		"account.new":              "Nouveau mot de passe",
		"account.confirm":          "Confirme le nouveau mot de passe",
		"account.save":             "Changer",
		"account.changed":          "Mot de passe changé. Tes autres sessions sont fermées.",
		"update.available":         "Mise à jour disponible",
		"update.unix":              "Linux ou macOS, le script d'installation à nouveau",
		"update.windows":           "Windows, dans un PowerShell administrateur",
		"update.compose":           "Docker Compose, dans le dossier du projet",
		"update.docker":            "Le conteneur Eriphe (NAS, Linux), sur la machine",
		"time.now":                 "à l'instant",
		"time.min":                 "il y a %d min",
		"time.hour":                "il y a %d h",
		"time.day":                 "il y a %d j",
		"time.never":               "jamais",
	},
	"en": {
		"app.tagline":              "Your assistant, at home.",
		"nav.chat":                 "Conversation",
		"nav.tasks":                "Tasks",
		"nav.settings":             "Settings",
		"nav.logout":               "Log out",
		"nav.main":                 "Main navigation",
		"login.title":              "Log in",
		"login.lead":               "Enter your password to open your workspace.",
		"login.password":           "Password",
		"login.submit":             "Log in",
		"setup.title":              "Welcome",
		"setup.lead":               "Choose the password of your workspace. The setup code is printed at the end of the install; lost, this command prints it again on Eriphe's machine: %s",
		"setup.code":               "Setup code",
		"setup.password":           "New password",
		"setup.confirm":            "Confirm the password",
		"setup.hint":               "At least 10 characters. A phrase of a few words is ideal.",
		"setup.submit":             "Create my workspace",
		"err.csrf":                 "The page expired: reload it and try again.",
		"err.toomany":              "Too many attempts. Try again in a quarter of an hour.",
		"err.password":             "Wrong password.",
		"err.setupcode":            "Wrong setup code.",
		"err.short":                "The password must be at least 10 characters long.",
		"err.mismatch":             "The two passwords differ.",
		"err.internal":             "Something went wrong. Try again.",
		"chat.label":               "Conversation with {{name}}",
		"chat.hello":               "Hello %s",
		"chat.hellononame":         "Hello",
		"chat.empty":               "Tell {{name}} what you want done. It hands the work to its agents, on your devices, and asks for your go-ahead when needed.",
		"chat.ex1":                 "Fix the contact page bug on my website",
		"chat.ex2":                 "Sum up what changed in my projects this week",
		"chat.ex3":                 "Draft a quote from my notes",
		"chat.placeholder":         "Write to {{name}}…",
		"chat.send":                "Send",
		"chat.you":                 "You",
		"chat.eriphe":              "{{name}}",
		"chat.event":               "Event",
		"chat.thinking":            "{{name}} is thinking…",
		"chat.nobrain":             "{{name}} is not set up on this server yet: your messages will wait until it is.",
		"chat.noai":                "{{name}} has no AI to think with yet: your messages wait until it has one.",
		"chat.noai.link":           "Choose its AI",
		"chat.brainoff":            "{{name}} is not available right now: it will read your messages when it is back.",
		"chat.offline":             "Connection lost. Retrying…",
		"chat.earlier":             "Older messages are not shown.",
		"perm.title":               "{{name}} needs your go-ahead",
		"perm.yours":               "This decision is yours",
		"perm.wants":               "wants to use",
		"perm.reason":              "Reason (optional)",
		"perm.reasonhint":          "Passed on to the agent, for instance to suggest another way.",
		"perm.allow":               "Yes, allow",
		"perm.deny":                "No, deny",
		"perm.allowed":             "Allowed.",
		"perm.always":              "Always allow",
		"perm.alwayshint":          "Allow, and stop asking for this action of this tool (removable in Settings → Tools → Access).",
		"perm.remembered":          "Allowed, and without asking from now on.",
		"perm.denied":              "Denied.",
		"perm.changed":             "This request has changed in the meantime.",
		"perm.task":                "See the task",
		"tasks.label":              "{{name}}'s tasks",
		"tasks.close":              "Close the tasks",
		"tasks.title":              "Tasks",
		"tasks.none":               "No task going on. When {{name}} hands work to an agent, it shows up here.",
		"tasks.done":               "Recently finished",
		"tasks.waitnode":           "Waiting for a device",
		"tasks.needsyou":           "Waiting for you",
		"tasks.open":               "Open the task",
		"task.close":               "Close",
		"task.prompt":              "Instructions",
		"task.activity":            "Activity",
		"task.noactivity":          "Nothing yet.",
		"task.reply":               "Reply to the agent",
		"task.replyhint":           "The agent's turn is over. Your reply tells it what comes next.",
		"task.replysend":           "Send",
		"task.sent":                "Message sent to the agent.",
		"task.stop":                "Stop the task",
		"task.stopconfirm":         "Stop this task? The agent stops where it is.",
		"task.stopping":            "Stop requested.",
		"task.node":                "Device",
		"task.agent":               "Agent",
		"task.repo":                "Repository",
		"task.branch":              "Branch",
		"task.ran":                 "ran",
		"task.allowed":             "allowed",
		"task.denied":              "denied",
		"task.turnend":             "Turn over",
		"task.error":               "Error",
		"state.pending":            "Pending",
		"state.running":            "Running",
		"state.waiting_permission": "Needs your go-ahead",
		"state.idle":               "Turn over",
		"state.stopped":            "Done",
		"state.failed":             "Failed",
		"state.lost":               "Lost",
		"settings.title":           "Settings",
		"settings.close":           "Close the settings",
		"settings.machines":        "Devices",
		"settings.agents":          "Agents",
		"settings.accounts":        "Accounts & tools",
		"settings.journal":         "Journal",
		"settings.g.assistant":     "Assistant",
		"settings.g.connections":   "Connections",
		"settings.g.devices":       "Devices & access",
		"settings.g.space":         "My workspace",
		"settings.accountnav":      "Account and language",
		"settings.attn":            "Needs a look",
		"account.logoutlead":       "Ends your session in this browser.",
		"settings.account":         "My workspace",
		"machines.lead":            "The devices you connected to {{name}}: it helps you with what is on them, and can put its agents to work there.",
		"machines.none":            "No device yet. Connect one, and {{name}} can help you with what is on it.",
		"machines.online":          "Online",
		"machines.offline":         "Offline",
		"machines.asleep":          "Asleep",
		"machines.asleephint":      "It disconnects and comes back by itself: Eriphe places elsewhere what cannot wait for it to wake.",
		"machines.seen":            "Seen %s",
		"machines.cpus":            "%d CPUs",
		"machines.memory":          "%s of memory",
		"machines.runtimes":        "Programs",
		"machines.labels":          "Labels",
		"machines.alwayson":        "Always on",
		"machines.model":           "Lends a model",
		"machines.modelhint":       "This device lends its model server (--model-server): your agents reach it through its tunnel. Pick it in Agents → Self-hosted model.",
		"machines.actions":         "Actions for %s",
		"machines.alwaysonhint":    "Your tools run here: mail and calendar must answer at any hour, so they go to the devices that never sleep.",
		"machines.active":          "%d active task(s)",
		"machines.add":             "Connect a device",
		"devices.addhint":          "A NAS, a PC, a Mac or a Raspberry: one command to run on it.",
		"machines.addlead":         "Give it a short name (lowercase letters, digits, dashes), then run the command shown on that device. {{name}} will only see the folders you open to it. The token works once, for this name only, and expires in 24 h.",
		"machines.name":            "Device name",
		"machines.labelsopt":       "Labels (optional, e.g. gpu)",
		"machines.generate":        "Generate the command",
		"machines.badname":         "The name is 1 to 32 characters: lowercase letters, digits and dashes.",
		"machines.unix":            "Linux or macOS",
		"machines.unixlead":        "In a terminal on the device. The script installs what it needs (Docker on Linux, a virtual machine on macOS); run again, it updates.",
		"machines.linux":           "Docker Compose (NAS, server)",
		"machines.linuxlead":       "In the node's compose.yaml, then docker compose up -d:",
		"machines.binary":          "Program only (Linux or macOS)",
		"machines.windows":         "Windows",
		"machines.windowslead":     "In an administrator PowerShell. The script installs WSL if it is missing; run again, it updates.",
		"machines.windowsfile":     "Windows (downloaded script)",
		"machines.windowsfilelead": "In an administrator PowerShell, next to install.ps1:",
		"machines.expires":         "Token valid until %s, once.",
		"machines.copy":            "Copy",
		"voice.lead":               "Home Assistant's voice (Assist) hands {{name}} what it does not handle itself, and reads its answer aloud. Create a token, then add the “Eriphe” integration in Home Assistant with this address and this token.",
		"voice.only":               "This token can only talk to {{name}}'s brain: it opens neither the tools nor the settings.",
		"voice.has":                "Home Assistant has a token. A new one replaces it.",
		"voice.create":             "Create Home Assistant's token",
		"voice.replace":            "Replace the token",
		"voice.replaceconfirm":     "Replace the token? Home Assistant cannot talk to {{name}} until you set the new one.",
		"voice.once":               "Copy the token now: it will not be shown again.",
		"voice.url":                "The address to give the Eriphe integration",
		"voice.token":              "Token",
		"ha.title":                 "Home Assistant",
		"ha.lead":                  "{{name}} and your house, both ways.",
		"ha.drive":                 "{{name}} drives the house",
		"ha.drivelead":             "{{name}} reads the state of the house and acts on it (lights, heating, shutters) through Home Assistant's MCP server, with a long-lived access token.",
		"ha.connect":               "Connect Home Assistant",
		"ha.voice":                 "The house's voice talks to {{name}}",
		"ha.voiceon":               "voice on",
		"ha.voiceonly":             "The house's voice talks to {{name}}. {{name}} does not drive the house yet.",
		"ha.manage":                "Manage",
		"chat.via":                 "said aloud",
		"machines.copied":          "Copied",
		"machines.forget":          "Forget",
		"machines.forgetconfirm":   "Forget this device? Its tasks will be lost and it will have to be connected again.",
		"machines.rejoin":          "Re-register this device",
		"machines.rejoinconfirm":   "Re-register this device? The token lets a device take over this name, with its tasks and access.",
		"machines.rejoinlead":      "Re-registration token for %s. If Eriphe is still installed on the device, clear its identity first: on Linux or macOS, run the script with --uninstall (or delete node.json in the node's folder); on Windows, -Rejoin does it. Then run the command. The device's old secret will stop working.",
		"machines.exists":          "A device already has this name. To reinstall it, use \"Re-register this device\" on its card.",
		"machines.nonode":          "No device has this name.",
		"machines.notls":           "Unencrypted connection · moves to TLS by itself once updated",
		"machines.advanced":        "Advanced",
		"machines.advancedsummary": "The fingerprint of Eriphe's certificate authority",
		"machines.tlslead":         "Devices talk to Eriphe over TLS, each with a certificate signed by Eriphe's certificate authority. The connect commands carry its fingerprint already (-ca-hash): the new device checks that it talks to Eriphe. Here it is, for a device set up by hand.",
		"machines.tlsplain":        "Still without a certificate: %s. An updated device gets one by itself at its next connection; once they all have one, Eriphe accepts devices over TLS only.",
		"machines.tlsonly":         "Only devices over TLS, with their certificate, are accepted.",
		"system.macos-container":   "macOS (Apple container)",
		"system.macos-lima":        "macOS (Lima VM)",
		"system.windows":           "Windows (WSL)",
		"system.linux":             "Linux",
		"system.compose":           "Linux (Docker Compose)",
		"system.docker":            "Linux (Eriphe container)",
		"system.darwin":            "macOS",
		"system.windows-native":    "Windows",
		"agents.lead":              "The agents {{name}} can hand work to. They are set in eriphe-core's configuration.",
		"agents.none":              "No agent configured.",
		"agents.default":           "Default",
		"agents.available":         "Available",
		"agents.unavailable":       "No online device can run it",
		"agents.use":               "When",
		"agents.needs":             "Needs",
		"agents.prefers":           "Prefers",
		"agents.model":             "Model",
		"agents.runtime":           "Program",
		"accounts.lead":            "The keys and tokens {{name}} lends its agents. They are encrypted on the server and never shown again.",
		"accounts.vault":           "Vault",
		"accounts.none":            "The vault is empty.",
		"accounts.updated":         "Changed %s",
		"accounts.ref":             "To use in the configuration:",
		"accounts.add":             "Add or replace a secret",
		"accounts.name":            "Name",
		"accounts.namehint":        "Lowercase letters, digits, dots, dashes, underscores. An existing name is replaced.",
		"accounts.value":           "Value",
		"accounts.valuehint":       "Pasted here, it is encrypted at once and never shown again.",
		"accounts.save":            "Save",
		"accounts.saved":           "Secret “%s” saved.",
		"accounts.delete":          "Delete",
		"accounts.deleteconfirm":   "Delete this secret? What uses it will stop working.",
		"accounts.deleted":         "Secret deleted.",
		"accounts.novault":         "The vault is not open: see eriphe-core's log.",
		"accounts.config":          "Secrets of the configuration",
		"accounts.configlead":      "Where the configuration finds each secret. Those still in a file can be copied into the vault; then replace the path with the vault: reference shown.",
		"accounts.infile":          "File",
		"accounts.invault":         "Vault",
		"accounts.import":          "Copy into the vault",
		"accounts.imported":        "Copied into the vault as “%s”. Replace the path with %s in the configuration.",
		"accounts.tools":           "Connected tools",
		"accounts.toolslead":       "The online services (calendar, mail, documents…) {{name}} can use, and whether they answer. Those that need your authorization connect here.",
		"tools.count":              "%d tools",
		"tools.local":              "On a device",
		"tools.connect":            "Connect",
		"tools.reconnect":          "Reconnect",
		"tools.step1":              "Open the service's authorization page and accept.",
		"tools.open":               "Open",
		"tools.step2":              "Your browser then lands on a 127.0.0.1 page that does not load: copy its address and paste it here.",
		"tools.finish":             "Finish",
		"tools.badurl":             "That is not the expected address: it holds code= and state=.",
		"tools.connected":          "Connected: %d tools available.",
		"server.ready":             "Connected",
		"server.connecting":        "Connecting…",
		"server.needs_auth":        "To connect",
		"server.error":             "Error",
		"auth.oauth":               "OAuth authorization",
		"auth.bearer":              "token",
		"auth.none":                "no authentication",
		"auth.local":               "local",
		"journal.lead":             "Every decision on the agents' requests: who allowed or denied what.",
		"journal.none":             "No decision yet.",
		"journal.when":             "When",
		"journal.task":             "Task",
		"journal.what":             "Request",
		"journal.decision":         "Decision",
		"journal.by":               "By",
		"journal.reason":           "Reason",
		"journal.more":             "Older",
		"decision.allow":           "Allowed",
		"decision.deny":            "Denied",
		"by.user":                  "You",
		"by.brain":                 "{{name}}",
		"by.policy":                "Rule",
		"account.lead":             "Your language, your sign-in and your password.",
		"account.lang":             "Language",
		"account.password":         "Change the password",
		"account.current":          "Current password",
		"account.new":              "New password",
		"account.confirm":          "Confirm the new password",
		"account.save":             "Change",
		"account.changed":          "Password changed. Your other sessions are closed.",
		"update.available":         "Update available",
		"update.unix":              "Linux or macOS, the install script again",
		"update.windows":           "Windows, in an administrator PowerShell",
		"update.compose":           "Docker Compose, in the project's folder",
		"update.docker":            "The Eriphe container (NAS, Linux), on the machine",
		"time.now":                 "just now",
		"time.min":                 "%d min ago",
		"time.hour":                "%d h ago",
		"time.day":                 "%d d ago",
		"time.never":               "never",
	},
}

// tr is the text of key in lang, French when lang has none, the key when
// neither has: with the assistant's name for {{name}}.
func tr(lang, key string) string {
	if s, ok := webStrings[lang][key]; ok {
		return withName(lang, s, assistantName())
	}
	if s, ok := webStrings["fr"][key]; ok {
		return withName("fr", s, assistantName())
	}
	return key
}

// The name the user gave their assistant, for the words that name it as a
// person ({{name}}). Where the software is named, it stays Eriphe. One core
// runs in a process, as one vault does (vault.go).
var shownName atomic.Value

func setAssistantName(n string) { shownName.Store(n) }

func assistantName() string {
	if n, _ := shownName.Load().(string); n != "" {
		return n
	}
	return defaultDisplayName
}

// Before a vowel, French elides « de » and « que »: written « de {{name}} »,
// it reads « d'Eriphe » or « de Jarvis ».
var elidable = regexp.MustCompile(`\b(de|De|que|Que|jusque) \{\{name\}\}`)

// withName puts the assistant's name in a text.
func withName(lang, s, name string) string {
	if !strings.Contains(s, "{{name}}") {
		return s
	}
	if lang == "fr" && startsWithVowel(name) {
		s = elidable.ReplaceAllStringFunc(s, func(m string) string {
			w, _, _ := strings.Cut(m, " ")
			return w[:len(w)-1] + "'{{name}}"
		})
	}
	return strings.ReplaceAll(s, "{{name}}", name)
}

func startsWithVowel(s string) bool {
	r, _ := utf8.DecodeRuneInString(strings.ToLower(s))
	return strings.ContainsRune("aeiouyàâäéèêëîïôöùûüœæ", r)
}

// templateFuncs are the templates' helpers, in one language.
func templateFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(key string) string { return tr(lang, key) },
		// name is what the user calls their assistant: the pages' title and brand.
		"name": assistantName,
		"tf":   func(key string, args ...any) string { return fmt.Sprintf(tr(lang, key), args...) },
		"ago": func(t time.Time) string {
			return ago(lang, t, time.Now())
		},
		"clock": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			if time.Since(t) < 20*time.Hour {
				return t.Local().Format("15:04")
			}
			return t.Local().Format("02/01 15:04")
		},
		"ver":      displayVersion,
		"date":     func(t time.Time) string { return t.Local().Format("02/01/2006 15:04") },
		"iso":      func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"md":       markdown,
		"short":    truncate,
		"size":     func(b int64) string { return byteSize(lang, b) },
		"filesize": func(b int64) string { return fileSize(lang, b) },
		"join":     strings.Join,
		"list":     func(s ...string) []string { return s },
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
		"trimvault": func(s string) string { return strings.TrimPrefix(s, vaultPrefix) },
		// tor is the text of key, or of fallback when key has none.
		"tor": func(key, fallback string) string {
			if v := tr(lang, key); v != key {
				return v
			}
			return tr(lang, fallback)
		},
		// steps are the lines of a text, or none when the key has no text.
		"steps": func(key string) []string {
			if v := tr(lang, key); v != key {
				return strings.Split(v, "\n")
			}
			return nil
		},
		// problem words a raw error that happened where (problems.go):
		// brain, brain-start, task, tool or account.
		"problem": func(where string, raw any) problem {
			s := ""
			if raw != nil {
				s = fmt.Sprint(raw)
			}
			if where == "brain-start" {
				p := explain(lang, whereBrain, s)
				if p.Cause == causeUnknown {
					p.Text = tr(lang, "problem.brain.start")
				}
				return p
			}
			return explain(lang, where, s)
		},
		// evfail is the raw words of an event that says a turn failed, or "".
		"evfail": func(e EventRow) string {
			raw, _ := turnFailure(e)
			return raw
		},
		"has":      func(key string) bool { return tr(lang, key) != key },
		"logo":     presetLogo,
		"initial":  func(s string) string { return strings.ToUpper(string([]rune(s + "?")[:1])) },
		"finished": finished,
		"mismatch": func(m mismatch) string { return m.Text(lang) }, // receipt.go
		"money":    func(usd float64) string { return money(lang, usd) },
		"tokens":   func(n int64) string { return tokenCount(lang, n) },
		"evsubject": func(data map[string]any) string {
			tool, _ := data["name"].(string)
			if tool == "" {
				tool, _ = data["tool_name"].(string)
			}
			input, _ := data["input"].(map[string]any)
			return subject(tool, input)
		},
		// browseurl is the folder explorer's address for a machine, a root and a path (web_folders.go).
		"browseurl": func(kv ...string) string {
			q := url.Values{}
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i+1] != "" {
					q.Set(kv[i], kv[i+1])
				}
			}
			return "/settings/folders/browse?" + q.Encode()
		},
	}
}

func ago(lang string, t, now time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return tr(lang, "time.never")
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return tr(lang, "time.now")
	case d < time.Hour:
		return fmt.Sprintf(tr(lang, "time.min"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(tr(lang, "time.hour"), int(d.Hours()))
	}
	return fmt.Sprintf(tr(lang, "time.day"), int(d.Hours()/24))
}

func byteSize(lang string, b int64) string {
	unit := "GB"
	if lang == "fr" {
		unit = "Go"
	}
	g := float64(b) / (1 << 30)
	if g >= 10 {
		return fmt.Sprintf("%.0f %s", g, unit)
	}
	s := fmt.Sprintf("%.1f %s", g, unit)
	if lang == "fr" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// markdown renders the little Markdown agents write: paragraphs, lists,
// code blocks, inline code, bold, italics, headings and links. Everything
// is escaped first; links are http(s), or a page of the workspace.
func markdown(src string) template.HTML {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var para []string
	list := ""
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			b.WriteString("</" + list + ">")
			list = ""
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flush()
			closeList()
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, html.EscapeString(lines[i]))
			}
			b.WriteString("<pre><code>" + strings.Join(code, "\n") + "</code></pre>")
		case trimmed == "":
			flush()
			closeList()
		case headingRe.MatchString(trimmed):
			flush()
			closeList()
			m := headingRe.FindStringSubmatch(trimmed)
			b.WriteString("<p class=\"md-h\">" + inline(m[2]) + "</p>")
		case bulletRe.MatchString(line):
			flush()
			if list != "ul" {
				closeList()
				b.WriteString("<ul>")
				list = "ul"
			}
			b.WriteString("<li>" + inline(bulletRe.FindStringSubmatch(line)[1]) + "</li>")
		case numberRe.MatchString(line):
			flush()
			if list != "ol" {
				closeList()
				b.WriteString("<ol>")
				list = "ol"
			}
			b.WriteString("<li>" + inline(numberRe.FindStringSubmatch(line)[1]) + "</li>")
		default:
			closeList()
			para = append(para, inline(trimmed))
		}
	}
	flush()
	closeList()
	return template.HTML(b.String())
}

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	bulletRe  = regexp.MustCompile(`^\s*[-*•]\s+(.*)$`)
	numberRe  = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	codeRe    = regexp.MustCompile("`([^`]+)`")
	boldRe    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicRe  = regexp.MustCompile(`(^|[^*\w])\*([^*\s][^*]*)\*`)
	mdLinkRe  = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s)]+|/[^\s)]*)\)`)
	urlRe     = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)
)

// inline escapes a line and renders its inline Markdown. Code spans are
// set aside first so nothing inside them is touched.
func inline(s string) string {
	var spans []string
	s = codeRe.ReplaceAllStringFunc(s, func(m string) string {
		spans = append(spans, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return fmt.Sprintf("\x00%d\x00", len(spans)-1)
	})
	var links []string
	keep := func(h string) string {
		links = append(links, h)
		return fmt.Sprintf("\x01%d\x01", len(links)-1)
	}
	s = mdLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		p := mdLinkRe.FindStringSubmatch(m)
		return keep(link(p[2], html.EscapeString(p[1])))
	})
	s = urlRe.ReplaceAllStringFunc(s, func(m string) string {
		u, tail := trimURL(m)
		return keep(link(u, html.EscapeString(u))) + tail
	})
	s = html.EscapeString(s)
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = italicRe.ReplaceAllString(s, "$1<em>$2</em>")
	for i, h := range links {
		s = strings.Replace(s, fmt.Sprintf("\x01%d\x01", i), h, 1)
	}
	for i, h := range spans {
		s = strings.Replace(s, fmt.Sprintf("\x00%d\x00", i), h, 1)
	}
	return s
}

func trimURL(u string) (string, string) {
	tail := ""
	for len(u) > 0 && strings.ContainsRune(".,;:!?", rune(u[len(u)-1])) {
		tail = u[len(u)-1:] + tail
		u = u[:len(u)-1]
	}
	return u, tail
}

// workspacePages are the pages a message may link to inside the workspace,
// e.g. [Connecter un appareil](/settings/devices#connect): they open in the
// same tab. Anything else that is not http(s) stays plain text.
var workspacePages = []string{"/settings", "/memory", "/tasks/", "/welcome"}

func link(u, text string) string {
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.Contains(u, "\\") {
		if p, err := url.Parse(u); err == nil && p.Scheme == "" && p.Host == "" {
			for _, w := range workspacePages {
				if p.Path == w || strings.HasPrefix(p.Path, strings.TrimSuffix(w, "/")+"/") {
					return `<a href="` + html.EscapeString(p.String()) + `">` + text + `</a>`
				}
			}
		}
		return text
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return text
	}
	return `<a href="` + html.EscapeString(p.String()) + `" target="_blank" rel="noopener noreferrer">` + text + `</a>`
}
