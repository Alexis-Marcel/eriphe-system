# Écrire une surface (A2UI 1.0)

Un bloc est une partie de la page d'un nœud, entre le texte. Le plus souvent, un seul composant : `{"component": "Sections", "items": "mails"}`, que le code complète avec l'id `root`. Pour en composer plusieurs, une liste plate : chacun a un `id` unique (lettres, chiffres et `_`, ni tiret ni chiffre au début), un seul est `root`, et un composant désigne ses enfants par leur id (`child`, `children`, `tabs`), jamais en les écrivant dedans.

**Valeurs.** Une propriété de type texte, nombre, booléen ou valeur prend un littéral (`"Bonjour"`, `42`), une liaison vers les données du bloc `{"@path": "/ventes/total"}` (un pointeur JSON ; la valeur d'une source est en `/<nom>`) ou un appel de fonction `{"@call": "formatNumber", "args": {"value": {"@path": "/total"}}}`. « ou liaison » : une liste littérale ou une liaison vers une liste de même forme. Le nom d'une source (Sections, Gallery, Table) : le code y lit ses éléments, toujours à jour.

**Règles.**
- Les composants sont des formes, jamais des outils : des chiffres : Figures ; les éléments d'une source, rangés d'après tes notes : Sections ; des images : Gallery ; des lignes : Table ; une série : Chart (une répartition, aujourd'hui et la cible : `stack`) ; des étapes : Timeline ; des réponses probables : Asks ; un message : Message ; une personne : Person ; des mots d'un autre, cités : Quote ; des jours : Agenda ; un lien : Link ; du code ou un diff : Code.
- Le code fait le reste : groupes, comptes, unités, mise en page, et ce qui est vide, en lecture ou en erreur. Pas de texte qui répète un titre, ni « Rien à traiter ».
- Une page qui dure tient ses faits de ses sources (une liaison), jamais d'un texte figé, faux demain (« 18 385 € », « 3 mails »).
- Asks n'agit sur rien : un clic t'envoie son texte tel quel, comme un message de l'utilisateur sur ce nœud. Un oui n'est jamais un composant : ce qui attend le oui de l'utilisateur est une carte, que le code dessine d'après ses registres, sur la ligne de son élément, sinon au pied de la page.
- Un Link montre son adresse en clair et ne s'ouvre qu'au clic. Son `url` est une adresse http(s) écrite telle quelle, ou une liaison vers un champ qui est déjà une adresse ; jamais construite à partir des données (pas de formatString, rien d'ajouté à la requête, ni identifiant ni mot de passe) : sinon elle est refusée, ou n'est pas montrée.
- Du code se montre avec Code, tel quel ; un diff unifié avec `"mode": "diff"`. Pour relire des modifications : leur description (Markdown), le diff (Code), les vérifications (Timeline) et un Link « Ouvrir dans … » vers leur page.
- N'invente ni composant, ni propriété, ni fonction : ce qui n'est pas ci-dessous est refusé.

## Exemple : des chiffres, l'un lié à sa source
```json
{"component": "Figures", "items": [{"label": "Processeur", "value": {"@path": "/machine/cpu"}, "unit": "%", "delta": -5}, {"label": "Disque libre", "value": 1.2, "unit": "To"}]}
```

## Exemple : deux composants côte à côte
```json
[
  {"id": "root", "component": "Row", "children": ["sam", "etapes"]},
  {"id": "sam", "component": "Person", "name": "Sam", "detail": "Artisan, la terrasse"},
  {"id": "etapes", "component": "Timeline", "items": [{"label": "Devis signé", "state": "done"}, {"label": "Acompte", "state": "current"}]}
]
```

## Composants
`*` : obligatoire. « fixe » : une valeur écrite telle quelle, sans liaison.

- **Text** : Un texte court.
  text* texte · variant title|subtitle|body|caption|mark (Son style ; body par défaut. mark : une marque courte attachée à ce qui la suit (« extérieur »))
- **Markdown** : Un texte plus long en Markdown simple (titres, listes, gras, liens), sans HTML ni image.
  text* texte
- **Image** : Une image.
  url* texte (Son adresse) · alt texte (Ce qu'elle montre, en quelques mots) · fit contain|cover
- **Icon** : Une icône.
  name* alert|arrowDown|arrowUp|calendar|chart|check|clock|close|cloud|document|error|event|folder|home|info|link|lock|mail|message|money|note|person|phone|place|refresh|search|server|settings|star|task|trendDown|trendUp|warning ou liaison
- **Link** : Un lien vers une page web, son adresse montrée en clair ; il s'ouvre dans un nouvel onglet quand l'utilisateur clique, jamais de lui-même.
  url* texte fixe ou liaison (L'adresse http(s), écrite telle quelle, ou une liaison vers un champ qui est déjà une adresse ; jamais construite) · label texte (Ce que le lien ouvre, en quelques mots ; son hôte se voit à côté)
  Exemple : `{"id": "ouvrir", "component": "Link", "label": "Ouvrir la page du projet", "url": "https://example.org/projets/terrasse"}`
- **Code** : Du code ou un texte à chasse fixe, tel quel ; en mode diff, un diff unifié : ses fichiers, ses blocs @@, ses lignes ajoutées et retirées.
  text* texte (Le code, ou le diff unifié entier) · language texte fixe (Le langage, montré en étiquette (« go », « json »)) · mode code|diff (diff pour un diff unifié ; code par défaut)
  Exemple : `{"id": "modif", "component": "Code", "mode": "diff", "text": {"@path": "/diff"}}`
- **Sections** : Les éléments d'une source, rangés par le code d'après tes notes (item_note) : d'abord les groupes où il y a à faire, une ligne par élément avec ta note et sa carte ; un groupe dont les éléments partagent une carte, un lot replié qui porte son oui ; les autres groupes repliés. Vide, en lecture, en erreur : le code le dit.
  items* texte fixe ou liaison (La source du bloc qui liste ses éléments (items)) · title texte
  Exemple : `{"id": "mails", "component": "Sections", "items": "mails"}`
- **Figures** : Quelques chiffres côte à côte, en bandeau ; le premier bloc d'une page les montre en grand.
  items* [{label* texte · delta nombre (L'écart signé, à l'encre avec ▲ ou ▼) · deltaLabel texte (Depuis quand (« depuis hier »)) · trend [nombre fixe, …] ou liaison (Ses dernières valeurs : une petite courbe) · unit texte · value texte ou nombre}, …] ou liaison · title texte
  Exemple : `{"id": "chiffres", "component": "Figures", "items": [{"delta": 1.8, "deltaLabel": "cette semaine", "label": "PEA", "unit": "€", "value": 18385}, {"label": "Lignes", "value": 6}]}`
- **Gallery** : Des images en vignettes, leur titre dessous ; by ajoute un filtre par la valeur d'un champ.
  items* texte fixe ou [objet, …] ou liaison (Chacun a title et son image en photo (ou image)) · by texte fixe (Le champ qui fait les filtres) · title texte · units {clé: texte fixe} (Les champs montrés sous le titre, et leur unité, comme Table)
  Exemple : `{"id": "pieces", "component": "Gallery", "by": "categorie", "items": "pieces", "units": {"portee": "fois"}}`
- **Asks** : Des réponses probables en boutons : un clic t'envoie son texte, tel quel, comme son message dans ce nœud ; il n'agit sur rien. La première est celle par défaut. Chacune dit ce qu'elle demande, jamais un « Oui » seul.
  items* [texte fixe ou {text* texte fixe · until* {monitor texte fixe · skill texte fixe · tool texte fixe}}, …]
  Exemple : `{"id": "reponses", "component": "Asks", "items": ["Fais comme tu proposes", "Écris-le pour des managers"]}`
- **Row** : Des enfants côte à côte.
  children* enfants ([ids] ou modèle {path, componentId}) · align start|center|end|stretch · justify start|center|end|spaceBetween
- **Column** : Des enfants l'un sous l'autre.
  children* enfants ([ids] ou modèle {path, componentId}) · align start|center|end|stretch · justify start|center|end|spaceBetween
- **Tabs** : Des onglets, un enfant par onglet.
  tabs* [{title* texte · child* id d'un composant}, …]
- **Divider** : Un trait qui sépare.
- **Choice** : Un choix parmi des options, rangé dans les données du bloc.
  options* [{label* texte · value* texte fixe}, …] · value* liste de textes (Les valeurs choisies : une liaison vers une liste de textes) · label texte · multiple booléen fixe (Plusieurs choix possibles)
- **TextField** : Un champ de saisie, rangé dans les données du bloc.
  value* texte (Une liaison vers le texte saisi) · label texte · multiline booléen fixe · placeholder texte
- **DateField** : Un champ de date ou d'heure, rangé dans les données du bloc.
  value* texte (Une liaison vers la date ISO 8601) · label texte · mode date|time|dateTime
- **Chart** : Une ou plusieurs séries en courbes ou en barres ; stack : une barre par x, empilant les séries (une répartition, aujourd'hui et la cible).
  kind* line|bar|stack · series* [{name* texte fixe · points* [{x* texte fixe ou nombre fixe (Une date ISO 8601, un nombre ou une étiquette) · y* nombre fixe}, …]}, …] ou liaison · title texte · unit texte (L'unité des valeurs)
  Exemple : `{"id": "conso", "component": "Chart", "kind": "bar", "series": [{"name": "Maison", "points": [{"x": "lun.", "y": 12.5}, {"x": "mar.", "y": 9.8}]}], "unit": "kWh"}`
- **Table** : Des lignes en colonnes. Sans columns : les champs de la première ligne, dans l'ordre, la clé en en-tête ; les nombres à droite.
  rows* texte fixe ou [objet, …] ou liaison (Une ligne est un objet ; chaque colonne lit le champ de sa clé) · columns [{key* texte fixe (Le champ de la ligne à montrer) · label* texte fixe · kind text|number|date|change (change : un écart signé, en %, à l'encre (+, −))}, …] (Quand les clés ne sont pas ses mots) · title texte · units {clé: texte fixe} (Par colonne : €, %, ±% (un écart signé), ★ (une note sur 5) ou un mot (« min »))
  Exemple : `{"id": "recettes", "component": "Table", "rows": "recettes", "title": "Mes recettes", "units": {"note": "★", "temps": "min"}}`
- **Timeline** : Des étapes dans l'ordre, chacune avec son état ; elle dit d'elle-même où en est le travail (« 2 sur 6 », l'étape en cours) ; quand toutes ont une date, ce qui s'est passé, la date en marge.
  items* [{label* texte fixe · state* done|current|pending|failed · date texte fixe (Date ISO 8601) · detail texte fixe}, …] ou liaison · title texte
  Exemple : `{"id": "deploiement", "component": "Timeline", "items": [{"label": "Construire", "state": "done"}, {"label": "Tester", "state": "current"}, {"label": "Publier", "state": "pending"}]}`
- **Message** : Un message : un mail, un message d'une messagerie, une note reçue.
  from* texte · body texte (Le texte entier, quand il compte) · date texte (Date ISO 8601) · excerpt texte (Le début, en une ou deux lignes) · subject texte
- **Person** : Une personne : son nom, ce qu'elle est pour l'utilisateur, une note (ce qu'elle attend, le dernier échange) et quelques faits en lignes (téléphone, adresse, prochain rendez-vous). Un fait qui est en entier un numéro de téléphone ou une adresse mail s'ouvre d'un clic (appel, mail) ; aucun autre.
  name* texte · detail texte (Ce qu'elle est (« Artisan, la terrasse », « Collègue »)) · facts [{label* texte fixe · value* texte fixe}, …] ou liaison · note texte (Une phrase : ce qu'elle attend, où on en est)
- **Quote** : Des mots de quelqu'un d'autre, tels quels : un passage d'un mail, d'un article, d'un message ; qui les a écrits et quand. Pour citer, jamais pour tes propres mots.
  text* texte · date texte (Quand, ISO 8601) · from texte (Qui, ou d'où (« Sam », « Le Journal »)) · url texte fixe ou liaison (La page d'où il vient, montrée en clair)
- **Agenda** : Un agenda : tout ce qui vient d'un calendrier. Le premier jour se dessine heure par heure (aujourd'hui avec une ligne à l'heure qu'il est), le suivant en colonne compacte, les autres en petites colonnes. Une invitation sans réponse (statut needs-action) et une proposition datée qui attend un oui s'y montrent en fantôme « à répondre », avec le Oui de leur carte ; un statut se dit toujours en mots, jamais tel que la source l'écrit. Lie events aux éléments d'une source (items) : la carte qui attend sur un événement se montre d'elle-même sur son fantôme.
  events* [{title* texte fixe · start* texte fixe (Date et heure ISO 8601, ou la date seule pour toute la journée) · allDay booléen fixe · end texte fixe · place texte fixe · status texte fixe (Où en est l'événement, tel que la source le dit : confirmed, tentative, needs-action (une invitation sans réponse), declined, cancelled (accepted, needsAction, canceled aussi, en majuscules ou non). Un autre ne se montre pas)}, …] ou liaison · date texte (Le premier jour, AAAA-MM-JJ ; aujourd'hui s'il manque) · days nombre fixe (Combien de jours depuis date : 1 la journée, 2 avec demain, jusqu'à 7 la semaine) · proposals booléen fixe (Montrer les propositions datées de ces jours (oui par défaut) ; false pour un agenda qui n'est pas celui de l'utilisateur)
  Exemple : `{"id": "semaine", "component": "Agenda", "days": 7, "events": {"@path": "/agenda/events"}}`
- **Alert** : Un avertissement qui doit se voir.
  text* texte · level info|success|warning|error · title texte
- **Placeholder** : Une place réservée pendant que le bloc se prépare.
  text texte (Ce qui se prépare (« En préparation » par défaut))

## Fonctions
Un appel s'écrit `{"@call": "nom", "args": {…}}`, là où une valeur est attendue.

- **formatString**(value* texte) → texte : Un texte où `${/chemin}` (ou `${chemin}` dans un modèle) est remplacé par la donnée.
- **formatNumber**(value* nombre · decimals nombre (Le nombre de décimales)) → texte : Un nombre écrit à la française.
- **formatDate**(value* valeur · format* texte) → texte : Une date ISO 8601 écrite selon un motif Unicode (« EEEE d MMMM », « HH:mm »).
- **formatRelative**(value* valeur) → texte : Une date ISO 8601 dite par rapport à maintenant (« il y a 2 h », « demain »).
