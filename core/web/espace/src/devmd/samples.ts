import type { ProposalView } from "../a2ui/proposals";

// PROTOTYPE (dev server only, ?markdown): made-up pages in the Markdown
// format, the same pages as today's JSON blocks, and the made-up things
// their references name. No real person, address, amount or order.

export type Sample = { id: string; title: string; md: string; today: string; todayNote: string };

// ---- The things references name (what « the code resolves ») ----

export type Mail = { kind: "mail"; from: string; subject: string; date: string; body: string };
export type Ev = { kind: "event"; title: string; start: string; end?: string; place?: string; status?: string; organizer?: string };
export type Pr = { kind: "pr"; title: string; url: string; state: string; checks: string };
export type Obj = Mail | Ev | Pr | { kind: "file"; name: string; size: string };

const id = (n: number) => `bTEfc2FtQGV4YW1wbGUub3JnH0lOQk9YHzEfMjkw${String(n).padStart(3, "0")}`;

export const objects: Record<string, Obj> = {
  [`mail:${id(1)}`]: { kind: "mail", from: "Voyages Express <billets@example.org>", subject: "Votre option de réservation expire bientôt", date: "2026-11-25T18:02:00+01:00", body: "Bonjour,\n\nVotre option sur le trajet du 4 décembre est conservée jusqu'à confirmation. Rendez-vous dans Mes voyages, référence ABC123.\n\nL'équipe Voyages Express" },
  [`mail:${id(2)}`]: { kind: "mail", from: "Sam <sam@example.org>", subject: "Devis terrasse", date: "2026-11-24T09:41:00+01:00", body: "Bonjour,\n\nVoici le devis pour la terrasse : 4 200 € tout compris. Je peux commencer la semaine du 8 si ça te va. Dis-moi avant vendredi.\n\nSam" },
  [`mail:${id(3)}`]: { kind: "mail", from: "Fournisseur Internet <factures@example.org>", subject: "Votre facture de novembre", date: "2026-11-20T07:00:00+01:00", body: "Votre facture de novembre est disponible : 29,99 €, prélevée le 28/11." },
  [`mail:${id(4)}`]: { kind: "mail", from: "Boutique Exemple <commandes@example.org>", subject: "Votre commande n° 0001 est expédiée", date: "2026-11-21T12:10:00+01:00", body: "Votre colis est en route. Livraison prévue le 22/11." },
  [`mail:${id(5)}`]: { kind: "mail", from: "Le Tri du matin <lettre@example.org>", subject: "Newsletter Le Tri · n° 212", date: "2026-11-26T06:30:00+01:00", body: "Les nouvelles de la semaine en cinq minutes…" },
  // The lie: the page calls this line « Pub », the mail is Sam's.
  [`mail:${id(6)}`]: { kind: "mail", from: "Sam <sam@example.org>", subject: "Les clés pour samedi", date: "2026-11-25T21:14:00+01:00", body: "Je passe prendre les clés samedi vers 10 h, tu seras là ?\n\nSam" },
  "event:ev-standup": { kind: "event", title: "Point d'équipe", start: "2026-11-26T09:30:00+01:00", end: "2026-11-26T10:00:00+01:00", place: "Visio" },
  "event:ev-dentiste": { kind: "event", title: "Dentiste", start: "2026-11-26T12:30:00+01:00", end: "2026-11-26T13:15:00+01:00", place: "Cabinet du centre" },
  "event:ev-invit": { kind: "event", title: "Revue du projet Terrasse", start: "2026-11-26T16:00:00+01:00", end: "2026-11-26T17:00:00+01:00", place: "Chez Sam", status: "needs-action", organizer: "Sam" },
  "event:ev-sport": { kind: "event", title: "Course à pied", start: "2026-11-27T07:30:00+01:00", end: "2026-11-27T08:15:00+01:00", place: "Parc" },
  "pr:example/app#128": { kind: "pr", title: "Fix: l'app plante au lancement sur iOS 19", url: "https://example.org/example/app/pull/128", state: "ouverte, revue approuvée", checks: "44/44 tests, build iOS vert" },
};

// ---- Cards already proposed (card:…), held by code ----

export const cards: Record<string, ProposalView> = {
  "calls:412": {
    ref: "calls:412", card: 412, kind: "calls", state: "pending", verb: "Envoyer",
    text: "Répondre à Sam : « Bonjour Sam, le devis me va, on peut caler le début des travaux la semaine du 8. »",
    context: "compte perso", why: "Sam attend une réponse avant vendredi.",
    fields: [{ name: "mail/reply", value: "à : sam@example.org", detail: true }],
  },
  "calls:530": {
    ref: "calls:530", card: 530, kind: "calls", state: "pending", verb: "Publier",
    text: "Publier la version 3.2.1 sur l'App Store", context: "compte développeur",
    fields: [{ name: "store/release", value: "version : 3.2.1", detail: true }],
  },
  "calendar:77": {
    ref: "calendar:77", card: 77, kind: "calls", state: "pending", verb: "Accepter",
    text: "Accepter l'invitation de Sam : Revue du projet Terrasse, 16 h", context: "agenda perso", tainted: true,
  },
};

// ---- The pages ----

const mails = `Écrit le 26/11/2026 à 9 h.

## À traiter
- [ ] [Option de train à valider][m1] : la réservation expire si tu ne la confirmes pas
  - Valider sur le site du transporteur, rubrique Mes voyages, référence ABC123.
  - Date limite introuvable dans le mail, à vérifier sur le site.
- [ ] [Sam · Devis terrasse][m2] : il attend ta réponse avant vendredi 📅 2026-11-28
  - Brouillon prêt : « Bonjour Sam, le devis me va, on peut caler le début des travaux la semaine du 8. »
  - [Envoyer la réponse](card:calls:412)

## À archiver
[Archiver ces mails](do:mail/move)
- [Facture internet de novembre][m3] : payée par prélèvement
- [Commande n° 0001 expédiée][m4] : livrée le 22/11

## À supprimer
[Jeter ces mails](do:mail/trash)
- [Newsletter Le Tri][m5] : newsletter, jamais ouverte
- [Pub Maison Lune · Gift Guide][m6] : publicité

[m1]: mail:${id(1)}
[m2]: mail:${id(2)}
[m3]: mail:${id(3)}
[m4]: mail:${id(4)}
[m5]: mail:${id(5)}
[m6]: mail:${id(6)}
`;

const mailsToday = `\`\`\`eriphe:block mails
{"components": [
  {"id":"root","component":"Sections","items":"mails"}
],
 "sources": {"mails":{"tool":"mail/search","args":{"query":"in:inbox newer_than:7d"},"every":15}}}
\`\`\``;

const taches = `Écrit le 26/11/2026.

- [ ] Valider l'option de train : référence ABC123 📅 2026-11-27
- [ ] Répondre au devis de Sam : voir [[Mes mails]] 📅 2026-11-28
- [/] Publier l'app sur les stores : correctif déjà en staging
- [ ] Renvoyer le pantalon : commande n° 0001, étiquette imprimée
- [x] Payer la cantine 📅 2026-11-25
- [-] Changer de forfait mobile : l'offre a expiré
`;

const tachesToday = `\`\`\`eriphe:block taches
{"components": [
  {"id":"root","component":"Column","children":["afaire","plustard"]},
  {"id":"afaire","component":"Card","title":"À faire","child":"liste"},
  {"id":"liste","component":"List","children":{"path":"/taches","componentId":"tache"}},
  {"id":"tache","component":"Item","title":{"@path":"titre"},"note":{"@path":"note"},"due":{"@path":"echeance"}},
  {"id":"plustard","component":"Fold","label":"Fait ou abandonné","child":"listetard"},
  {"id":"listetard","component":"List","children":{"path":"/fait","componentId":"tache"}}
],
 "data": {"taches":[{"titre":"Valider l'option de train","note":"Référence ABC123.","echeance":"2026-11-27"},{"titre":"Répondre au devis de Sam","note":"Voir Mes mails.","echeance":"2026-11-28"},{"titre":"Publier l'app sur les stores","note":"En cours : correctif déjà en staging.","echeance":""},{"titre":"Renvoyer le pantalon","note":"Commande n° 0001, étiquette imprimée.","echeance":""}],"fait":[{"titre":"Payer la cantine","note":"Fait.","echeance":"2026-11-25"},{"titre":"Changer de forfait mobile","note":"Abandonné : l'offre a expiré.","echeance":""}]}}
\`\`\``;

const portefeuille = `Écrit le 26/11/2026 à 18 h, cours de clôture.

> [!figures]
> - Valeur : 18 420 € (▼ 1,1 % aujourd'hui)
> - Plus-value : 194 €
> - PEA : 12 380 € (▲ 1,8 % cette semaine)
> - Liquidités : 610 €

> [!chart|line] Valeur sur 30 jours
> | Jour | Valeur (€) |
> |---|---|
> | 2026-10-28 | 17 950 |
> | 2026-11-02 | 18 120 |
> | 2026-11-06 | 17 880 |
> | 2026-11-10 | 18 240 |
> | 2026-11-14 | 18 610 |
> | 2026-11-18 | 18 390 |
> | 2026-11-22 | 18 530 |
> | 2026-11-26 | 18 420 |

### Répartition face au plan
| Ligne | Part | Cible | Écart |
|---|---|---|---|
| ETF Monde | 62 % | 60 % | +2 % |
| ETF Émergents | 9 % | 10 % | −1 % |
| Obligations | 18 % | 20 % | −2 % |
| Actions en direct | 8 % | 7 % | +1 % |
| Liquidités | 3 % | 3 % | 0 % |

> [!warning] Obligations sous la cible
> La ligne Obligations est 2 points sous le plan depuis trois semaines.

[Propose un rééquilibrage](ask:) · [Montre les frais de l'année](ask:)
`;

const portefeuilleToday = `\`\`\`eriphe:block portefeuille
{"components": [
  {"id":"root","component":"Column","children":["chiffres","courbe","repartition","alerte","reponses"]},
  {"id":"chiffres","component":"Figures","items":[{"label":"Valeur","value":{"@path":"/compte/valeur"},"unit":"€","delta":{"@path":"/compte/jour"},"deltaLabel":"aujourd'hui"},{"label":"Plus-value","value":{"@path":"/compte/pv"},"unit":"€"},{"label":"PEA","value":{"@path":"/compte/pea"},"unit":"€"},{"label":"Liquidités","value":{"@path":"/compte/cash"},"unit":"€"}]},
  {"id":"courbe","component":"Chart","kind":"line","title":"Valeur sur 30 jours","unit":"€","series":{"@path":"/historique/series"}},
  {"id":"repartition","component":"Table","title":"Répartition face au plan","columns":[{"key":"name","label":"Ligne"},{"key":"share","label":"Part","kind":"number"},{"key":"target","label":"Cible","kind":"number"},{"key":"gap","label":"Écart","kind":"change"}],"units":{"share":"%","target":"%"},"rows":{"@path":"/alloc/rows"}},
  {"id":"alerte","component":"Alert","level":"warning","title":"Obligations sous la cible","text":"La ligne Obligations est 2 points sous le plan depuis trois semaines."},
  {"id":"reponses","component":"Asks","items":["Propose un rééquilibrage","Montre les frais de l'année"]}
],
 "sources": {"compte":{"tool":"investissement/get_account","every":60},"historique":{"tool":"investissement/get_history","args":{"days":30},"every":1440},"alloc":{"tool":"investissement/get_allocation","every":60}}}
\`\`\``;

const projet = `Écrit le 26/11/2026 à 11 h par la tâche « Corriger le lancement iOS ».

L'app se ferme au lancement sur iOS 19 : une clé manquante dans \`Info.plist\` fait planter le module de notifications.

> [!steps] Correctif
> - [x] Reproduire le plantage 📅 2026-11-24
>   - Sur simulateur iOS 19.1, à chaque lancement à froid.
> - [x] Trouver la cause 📅 2026-11-25
>   - Le module de notifications lit \`NSUserNotificationsUsageDescription\`, absente depuis la 3.2.0.
> - [/] Corriger et tester 📅 2026-11-26
>   - Clé ajoutée, tests verts ; reste la vérification sur un vrai téléphone.
> - [ ] Publier en production 📅 2026-11-27
>   - Après ton oui, version 3.2.1.

> [!figures|aside] Vérifications
> - Tests : 44/44
> - Build iOS : vert
> - Taille : 38,2 Mo (▲ 0,1 Mo)

\`\`\`diff
--- a/ios/App/Info.plist
+++ b/ios/App/Info.plist
@@ -41,6 +41,8 @@
   <key>CFBundleVersion</key>
-  <string>3.2.0</string>
+  <string>3.2.1</string>
+  <key>NSUserNotificationsUsageDescription</key>
+  <string>Pour te prévenir quand une tâche est finie.</string>
\`\`\`

- [Fix: l'app plante au lancement sur iOS 19][pr1] : revue approuvée
- [Page du ticket](https://example.org/example/app/issues/127)

### Décisions
- Pas de retour à la 3.1 : le correctif est plus rapide que le retour arrière.
- La clé reste obligatoire : un test vérifie sa présence.

### Publier
[Publier en prod](card:calls:530)

[Teste sur un vrai téléphone d'abord](ask:)

[pr1]: pr:example/app#128
`;

const projetToday = `L'app se ferme au lancement sur iOS 19 : une clé manquante dans \`Info.plist\` fait planter le module de notifications.

\`\`\`eriphe:block projet
{"components": [
  {"id":"root","component":"Row","children":["gauche","droite"]},
  {"id":"gauche","component":"Column","children":["etapes","diff","pr","ticket"]},
  {"id":"etapes","component":"Timeline","title":"Correctif","items":[{"label":"Reproduire le plantage","state":"done","date":"2026-11-24","detail":"Sur simulateur iOS 19.1, à chaque lancement à froid."},{"label":"Trouver la cause","state":"done","date":"2026-11-25","detail":"Le module de notifications lit NSUserNotificationsUsageDescription, absente depuis la 3.2.0."},{"label":"Corriger et tester","state":"current","date":"2026-11-26","detail":"Clé ajoutée, tests verts ; reste la vérification sur un vrai téléphone."},{"label":"Publier en production","state":"pending","date":"2026-11-27","detail":"Après ton oui, version 3.2.1."}]},
  {"id":"diff","component":"Code","mode":"diff","text":{"@path":"/pr/diff"}},
  {"id":"pr","component":"Link","label":"Fix: l'app plante au lancement sur iOS 19","url":{"@path":"/pr/url"}},
  {"id":"ticket","component":"Link","label":"Page du ticket","url":"https://example.org/example/app/issues/127"},
  {"id":"droite","component":"Figures","title":"Vérifications","items":[{"label":"Tests","value":{"@path":"/pr/tests"}},{"label":"Build iOS","value":{"@path":"/pr/build"}},{"label":"Taille","value":38.2,"unit":"Mo","delta":0.1}]}
],
 "sources": {"pr":{"tool":"github/get_pull_request","args":{"repo":"example/app","number":128},"every":10}}}
\`\`\`

### Décisions
- Pas de retour à la 3.1 : le correctif est plus rapide que le retour arrière.
- La clé reste obligatoire : un test vérifie sa présence.

\`\`\`eriphe:block publier
{"card": "calls:530"}
\`\`\`

\`\`\`eriphe:block reponses
{"components": [{"id":"root","component":"Asks","items":["Teste sur un vrai téléphone d'abord"]}]}
\`\`\``;

const journee = `Écrit le 26/11/2026 à 7 h.

> [!agenda]
> ### Jeudi 26 novembre
> - 09:30–10:00 [Point d'équipe][e1] · visio
> - 12:30–13:15 [Dentiste][e2] · cabinet du centre
> - 16:00–17:00 [Revue du projet Terrasse][e3] · chez Sam
>   - Invitation de Sam, pas encore répondue. [Accepter](card:calendar:77)
> ### Vendredi 27 novembre
> - 07:30–08:15 [Course à pied][e4] · parc
> - 14:00–15:00 Appel avec la banque · téléphone

- [ ] Préparer les questions pour le dentiste
- [ ] Relire le devis de Sam avant 16 h : voir [[Travaux de la terrasse avec Sam]]
- [x] Sortir les poubelles

[Montre la semaine](ask:)

[e1]: event:ev-standup
[e2]: event:ev-dentiste
[e3]: event:ev-invit
[e4]: event:ev-sport
`;

const journeeToday = `\`\`\`eriphe:block journee
{"components": [
  {"id":"root","component":"Column","children":["agenda","taches","reponses"]},
  {"id":"agenda","component":"Agenda","days":2,"events":{"@path":"/agenda/events"}},
  {"id":"taches","component":"List","children":{"path":"/taches","componentId":"tache"}},
  {"id":"tache","component":"Item","title":{"@path":"titre"},"note":{"@path":"note"}},
  {"id":"reponses","component":"Asks","items":["Montre la semaine"]}
],
 "data": {"taches":[{"titre":"Préparer les questions pour le dentiste"},{"titre":"Relire le devis de Sam avant 16 h","note":"Voir Travaux de la terrasse avec Sam."}]},
 "sources": {"agenda":{"tool":"calendar/list_events","args":{"days":2},"every":15}}}
\`\`\``;

const terrasse = `Écrit le 26/11/2026.

> [!person|aside] Sam
> - Rôle : artisan, la terrasse
> - Téléphone : 01 23 45 67 89
> - Mail : sam@example.org
>
> Il attend ta réponse au devis avant vendredi.

On refait la terrasse en bois avant l'hiver. Sam a visité le 18/11 et envoyé son devis le 24 : 4 200 €, la pose comprise.

> Le bois est commandé, je peux commencer la semaine du 8 si le temps tient. Il faudra juste vider la terrasse la veille.
> — Sam, 24/11

> [!steps] Où on en est
> - [x] Visite de Sam 📅 2026-11-18
> - [x] Devis reçu 📅 2026-11-24
>   - 4 200 € : bois, quincaillerie, pose.
> - [/] Répondre au devis 📅 2026-11-28
> - [ ] Acompte de 30 % 📅 2026-12-01
> - [ ] Travaux 📅 2026-12-08
>   - Une semaine, si le temps tient.

> [!chart|bar] Budget : dépensé et prévu
> | Poste | Prévu (€) | Dépensé (€) |
> |---|---|---|
> | Bois | 2 400 | 0 |
> | Quincaillerie | 300 | 120 |
> | Pose | 1 500 | 0 |
> | Outillage | 200 | 180 |

### Décisions
- Pin traité classe 4 plutôt que composite : moins cher, Sam le pose plus vite.
- Pas d'éclairage cette année.

### Prochaine étape
- [ ] Répondre à Sam : le brouillon est dans [[Mes mails]] 📅 2026-11-28
`;

const terrasseToday = `On refait la terrasse en bois avant l'hiver. Sam a visité le 18/11 et envoyé son devis le 24 : 4 200 €, la pose comprise.

\`\`\`eriphe:block terrasse
{"components": [
  {"id":"root","component":"Row","children":["gauche","sam"]},
  {"id":"gauche","component":"Column","children":["citation","etapes","budget"]},
  {"id":"sam","component":"Person","name":"Sam","detail":"Artisan, la terrasse","facts":[{"label":"Téléphone","value":"01 23 45 67 89"},{"label":"Mail","value":"sam@example.org"}],"note":"Il attend ta réponse au devis avant vendredi."},
  {"id":"citation","component":"Quote","text":"Le bois est commandé, je peux commencer la semaine du 8 si le temps tient. Il faudra juste vider la terrasse la veille.","from":"Sam","date":"2026-11-24"},
  {"id":"etapes","component":"Timeline","title":"Où on en est","items":[{"label":"Visite de Sam","state":"done","date":"2026-11-18"},{"label":"Devis reçu","state":"done","date":"2026-11-24","detail":"4 200 € : bois, quincaillerie, pose."},{"label":"Répondre au devis","state":"current","date":"2026-11-28"},{"label":"Acompte de 30 %","state":"pending","date":"2026-12-01"},{"label":"Travaux","state":"pending","date":"2026-12-08","detail":"Une semaine, si le temps tient."}]},
  {"id":"budget","component":"Chart","kind":"bar","title":"Budget : dépensé et prévu","unit":"€","series":[{"name":"Prévu","points":[{"x":"Bois","y":2400},{"x":"Quincaillerie","y":300},{"x":"Pose","y":1500},{"x":"Outillage","y":200}]},{"name":"Dépensé","points":[{"x":"Bois","y":0},{"x":"Quincaillerie","y":120},{"x":"Pose","y":0},{"x":"Outillage","y":180}]}]}
]}
\`\`\`

### Décisions
- Pin traité classe 4 plutôt que composite : moins cher, Sam le pose plus vite.
- Pas d'éclairage cette année.

### Prochaine étape
Répondre à Sam : le brouillon est dans [[Mes mails]], avant le 28/11.`;

const bilan = `Écrit le 27/11/2026 à 18 h.

Une semaine chargée mais tenue : le correctif iOS est prêt, le devis de la terrasse est arrivé, et la boîte mail est revenue sous les dix messages.

> [!figures]
> - Tâches finies : 9 (▲ 3 sur la semaine d'avant)
> - Mails traités : 64
> - Heures de réunion : 6 h (▼ 2 h)

Le temps est allé surtout au projet iOS, comme prévu ; la maison a pris plus que la semaine d'avant à cause de la terrasse.

> [!chart|stack] Où est allé le temps (h)
> | Semaine | Travail (h) | Maison (h) | Sport (h) |
> |---|---|---|---|
> | 16/11 | 31 | 4 | 3 |
> | 23/11 | 33 | 7 | 2 |

> [!success] Correctif prêt
> La version 3.2.1 attend ton oui pour partir en production.

### Ce qui reste
![[Mes tâches]]

[Prépare la semaine prochaine](ask:) · [Qu'est-ce que j'ai repoussé ?](ask:)
`;

const bilanToday = `Une semaine chargée mais tenue : le correctif iOS est prêt, le devis de la terrasse est arrivé, et la boîte mail est revenue sous les dix messages.

\`\`\`eriphe:block chiffres
{"components": [
  {"id":"root","component":"Figures","items":[{"label":"Tâches finies","value":9,"delta":3,"deltaLabel":"sur la semaine d'avant"},{"label":"Mails traités","value":64},{"label":"Heures de réunion","value":6,"unit":"h","delta":-2}]}
]}
\`\`\`

Le temps est allé surtout au projet iOS, comme prévu ; la maison a pris plus que la semaine d'avant à cause de la terrasse.

\`\`\`eriphe:block temps
{"components": [
  {"id":"root","component":"Column","children":["pile","ok","reste","reponses"]},
  {"id":"pile","component":"Chart","kind":"stack","title":"Où est allé le temps","unit":"h","series":[{"name":"Travail","points":[{"x":"16/11","y":31},{"x":"23/11","y":33}]},{"name":"Maison","points":[{"x":"16/11","y":4},{"x":"23/11","y":7}]},{"name":"Sport","points":[{"x":"16/11","y":3},{"x":"23/11","y":2}]}]},
  {"id":"ok","component":"Alert","level":"success","title":"Correctif prêt","text":"La version 3.2.1 attend ton oui pour partir en production."},
  {"id":"reste","component":"Sections","title":"Ce qui reste","items":"taches"},
  {"id":"reponses","component":"Asks","items":["Prépare la semaine prochaine","Qu'est-ce que j'ai repoussé ?"]}
],
 "sources": {"taches":{"tool":"node_find","args":{"node":"Mes tâches","open":true}}}}
\`\`\``;

export const samples: Sample[] = [
  { id: "mails", title: "Mes mails", md: mails, today: mailsToday, todayNote: "Aujourd'hui, les mails ne sont pas dans le fichier : la source mail/search est relue à l'affichage, Sections les range d'après les notes d'Eriphe (item_note, hors du fichier). Le fichier seul ne dit rien de la boîte." },
  { id: "taches", title: "Mes tâches", md: taches, today: tachesToday, todayNote: "Bloc écrit avec les données dedans (comme l'exemple taches-json.md)." },
  { id: "portefeuille", title: "Mon portefeuille", md: portefeuille, today: portefeuilleToday, todayNote: "Les chiffres viennent de trois sources relues à l'affichage : le fichier ne contient aucun chiffre." },
  { id: "projet", title: "Projet : App qui ne se lance pas sur iOS", md: projet, today: projetToday, todayNote: "Texte + bloc (diff et état de la PR depuis la source github) + carte + Asks." },
  { id: "journee", title: "Ma journée", md: journee, today: journeeToday, todayNote: "L'agenda vient de la source calendar, relue : les rendez-vous ne sont pas dans le fichier." },
  { id: "terrasse", title: "Travaux de la terrasse avec Sam", md: terrasse, today: terrasseToday, todayNote: "Tout écrit dans le bloc (pas de source)." },
  { id: "bilan", title: "Bilan de la semaine", md: bilan, today: bilanToday, todayNote: "Texte + deux blocs ; « Ce qui reste » est une source node_find sur Mes tâches." },
];

// The page the blind test wrote, read from the scratchpad by the caller
// (dev-markdown.tsx), rendered untouched.
export const demenagementTitle = "Déménagement (écrit par un agent)";
