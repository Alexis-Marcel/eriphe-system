# Écrire une surface (A2UI 1.0)

Un bloc est une partie de la page d'un nœud : des composants de ce catalogue, entre le texte. Il s'écrit comme une liste plate de composants ; chacun a un `id` unique (lettres, chiffres et `_`, ni tiret ni chiffre au début), un `component` (un nom ci-dessous) et ses propriétés. Un seul composant a l'id `root` : la racine. Un composant désigne ses enfants par leur id (`child`, `children`, `tabs`), jamais en les écrivant dedans.

**Valeurs.** Une propriété de type texte, nombre, booléen ou valeur prend un littéral (`"Bonjour"`, `42`), une liaison vers les données du bloc `{"@path": "/ventes/total"}` (un pointeur JSON) ou un appel de fonction `{"@call": "formatNumber", "args": {"value": {"@path": "/total"}}}`. Une propriété marquée « ou liaison » prend une liste littérale ou une liaison vers une liste de même forme.

**Listes répétées.** `children` prend une liste d'ids `["a", "b"]` ou un modèle `{"path": "/mails", "componentId": "ligne"}` qui répète le composant `ligne` pour chaque élément de `/mails`. Dans ce modèle et ses enfants, un chemin sans `/` au début (`{"@path": "sujet"}`) se lit dans l'élément ; un chemin qui commence par `/` se lit depuis la racine des données.

**Règles.**
- Les composants sont des formes, jamais des outils : montre ce qu'un outil rend avec la forme qui lui va (un chiffre : Metric ; une série : Chart ; une répartition, aujourd'hui et la cible : Chart `stack` ; des lignes : Table ; des étapes : Timeline ; un mail ou un message : Message ; une personne : Person ; des mots d'un autre, cités : Quote ; un calendrier, une journée ou une semaine : Agenda ; des cartes en galerie : Grid ; un lien vers une page : Link ; du code ou un diff : Code). Des options, une liste, un brouillon ne sont pas des composants : compose-les de ceux-ci.
- Un bloc dit une chose : peu de composants, l'essentiel d'abord. Pas de texte qui répète un titre.
- Lie les composants aux champs que l'outil rend (`@path`) plutôt que de recopier les valeurs : le code relit l'outil et le bloc reste à jour.
- Un bouton ne fait qu'une chose : `ask`, il demande quelque chose à Eriphe avec tes propres mots : `{"event": {"name": "ask", "context": {"text": "Relance la sauvegarde"}}}`, un texte écrit tel quel et rien d'autre, jamais une liaison ; le code le relit dans le bloc, et Eriphe le reçoit comme un message de l'utilisateur sur ce nœud. Il n'agit sur rien. Tout autre nom est refusé.
- Un oui n'est jamais un composant : ce qui attend le oui de l'utilisateur est une carte, que le code dessine d'après ses registres. La carte d'un élément se montre d'elle-même sur sa ligne (un seul Oui, ✕ pour refuser) ; une autre, là où un bloc carte la place, sinon au pied de la page.
- Un Link montre son adresse en clair et ne s'ouvre que si l'utilisateur clique. Son `url` est une adresse http(s) écrite telle quelle, ou une liaison vers un champ que l'outil rend déjà comme une adresse (`{"@path": "url"}` dans un modèle) ; jamais une adresse construite à partir des données : pas de formatString, rien d'ajouté à la requête, pas d'identifiant ni de mot de passe dans l'adresse. Une adresse qui ne suit pas cette règle est refusée, ou n'est pas montrée.
- Du code se montre avec Code, tel quel ; un diff unifié avec `"mode": "diff"`. Pour relire des modifications : leur description (Markdown), le diff (Code), les vérifications (Timeline) et un Link « Ouvrir dans … » vers leur page, pour aller plus loin là où elles sont.
- N'invente ni composant, ni propriété, ni fonction : ce qui n'est pas ci-dessous est refusé.

## Exemple : des mails, liés aux données
```json
[
  {"id": "root", "component": "List", "children": {"path": "/messages", "componentId": "mail"}, "emptyText": "Rien à traiter"},
  {"id": "mail", "component": "Message", "from": {"@path": "from"}, "date": {"@path": "date"}, "subject": {"@path": "subject"}, "excerpt": {"@path": "snippet"}}
]
```
Données : `{"messages": [{"from": "Sam", "date": "2026-11-12T08:10:00Z", "subject": "Devis", "snippet": "Voici le devis…"}]}`

## Exemple : des chiffres composés une fois
```json
[
  {"id": "root", "component": "Row", "children": ["cpu", "disque"]},
  {"id": "cpu", "component": "Metric", "label": "Processeur", "value": 37, "unit": "%", "delta": -5, "better": "down"},
  {"id": "disque", "component": "Metric", "label": "Disque libre", "value": 1.2, "unit": "To"}
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
- **Row** : Des enfants côte à côte.
  children* enfants ([ids] ou modèle {path, componentId}) · align start|center|end|stretch · justify start|center|end|spaceBetween
- **Column** : Des enfants l'un sous l'autre.
  children* enfants ([ids] ou modèle {path, componentId}) · align start|center|end|stretch · justify start|center|end|spaceBetween
- **List** : Une liste d'éléments semblables, souvent répétés depuis les données par un modèle.
  children* enfants ([ids] ou modèle {path, componentId}) · direction vertical|horizontal · emptyText texte (Ce qui s'affiche quand la liste est vide)
- **Grid** : Des enfants en grille, autant par rangée qu'il en tient : une galerie de cartes.
  children* enfants ([ids] ou modèle {path, componentId}) · emptyText texte (Ce qui s'affiche quand la grille est vide) · size small|medium|large (La largeur d'une case)
- **Card** : Un cadre autour d'un enfant, avec un titre.
  child* id d'un composant · title texte
- **Tabs** : Des onglets, un enfant par onglet.
  tabs* [{title* texte · child* id d'un composant}, …]
- **Fold** : Une ligne qui se déplie : ce qu'elle résume (« 4 étapes faites »), et en dessous un enfant, montré quand on l'ouvre.
  label* texte · child* id d'un composant · aside id d'un composant (Sur sa ligne, au bout, ouverte ou non : un bouton qui demande quelque chose à Eriphe sur tout ce qu'elle résume) · list texte fixe (Un lot : l'id de la List qu'elle replie, liée aux éléments d'une source. Sa ligne dit `{n}` (le nombre de lignes) dans `label` et porte le Oui de la carte que ses lignes partagent ; dépliée, on décoche ce qu'on garde. Vide, elle disparaît) · open booléen (Ouverte d'abord (non par défaut))
- **Divider** : Un trait qui sépare.
- **Button** : Un bouton qui demande quelque chose à Eriphe (`ask`) : il envoie tes mots, n'agit sur rien.
  label* texte · action* action ({"event": {"name", "context"}} ou {"functionCall": appel}) · icon alert|arrowDown|arrowUp|calendar|chart|check|clock|close|cloud|document|error|event|folder|home|info|link|lock|mail|message|money|note|person|phone|place|refresh|search|server|settings|star|task|trendDown|trendUp|warning ou liaison · variant primary|default|quiet
  Exemple : `{"id": "relancer", "component": "Button", "action": {"event": {"context": {"text": "Relance la sauvegarde de cette nuit"}, "name": "ask"}}, "label": "Relancer la sauvegarde"}`
- **Choice** : Un choix parmi des options, rangé dans les données du bloc.
  options* [{label* texte · value* texte fixe}, …] · value* liste de textes (Les valeurs choisies : une liaison vers une liste de textes) · label texte · multiple booléen fixe (Plusieurs choix possibles)
- **TextField** : Un champ de saisie, rangé dans les données du bloc.
  value* texte (Une liaison vers le texte saisi) · label texte · multiline booléen fixe · placeholder texte
- **DateField** : Un champ de date ou d'heure, rangé dans les données du bloc.
  value* texte (Une liaison vers la date ISO 8601) · label texte · mode date|time|dateTime
- **Metric** : Un chiffre clé et son évolution. Plusieurs dans un Row font une rangée de chiffres.
  label* texte · value* texte ou nombre · better up|down (Le sens qui est une bonne nouvelle) · delta nombre (L'écart signé depuis la dernière fois) · deltaLabel texte (Depuis quand, en quelques mots (« depuis hier »)) · trend [nombre fixe, …] ou liaison (Ses dernières valeurs, de la plus ancienne à la dernière : une petite courbe sous le chiffre) · unit texte
- **Progress** : Où en est un travail : son mot d'état et, quand il a des étapes, une barre de leur avancée (faites, en cours, sur le total) et le nom de celle en cours.
  label* texte (Le mot d'état (« En cours », « À toi »)) · current nombre (Combien sont en cours) · step texte (Le nom de l'étape en cours (« Relecture »)) · tone default|urgent|success|muted (Sa couleur : urgent, success pour ce qui est fini, muted pour ce qui dort. Ce qui attend l'utilisateur est une carte, jamais une couleur) · total nombre (Combien en tout ; sans total, le mot seul) · value nombre (Combien sont faites)
- **Chart** : Une ou plusieurs séries en courbes ou en barres ; stack : une barre par x, empilant les séries (une répartition, aujourd'hui et la cible).
  kind* line|bar|stack · series* [{name* texte fixe · points* [{x* texte fixe ou nombre fixe (Une date ISO 8601, un nombre ou une étiquette) · y* nombre fixe}, …]}, …] ou liaison · unit texte (L'unité des valeurs)
  Exemple : `{"id": "conso", "component": "Chart", "kind": "bar", "series": [{"name": "Maison", "points": [{"x": "lun.", "y": 12.5}, {"x": "mar.", "y": 9.8}]}], "unit": "kWh"}`
- **Table** : Des lignes en colonnes.
  columns* [{key* texte fixe (Le champ de la ligne à montrer) · label* texte fixe · kind text|number|date|change (change : un écart signé, en %, vert à la hausse, rouge à la baisse)}, …] · rows* [objet, …] ou liaison (Une ligne est un objet ; chaque colonne lit le champ de sa clé) · emptyText texte
  Exemple : `{"id": "tickets", "component": "Table", "columns": [{"key": "title", "label": "Ticket"}, {"key": "age", "kind": "number", "label": "Âge"}], "rows": {"@path": "/issues"}}`
- **Timeline** : Des étapes dans l'ordre, chacune avec son état ; quand toutes ont une date, ce qui s'est passé, la date en marge.
  items* [{label* texte fixe · state* done|current|pending|failed · date texte fixe (Date ISO 8601) · detail texte fixe}, …] ou liaison
  Exemple : `{"id": "deploiement", "component": "Timeline", "items": [{"label": "Construire", "state": "done"}, {"label": "Tester", "state": "current"}, {"label": "Publier", "state": "pending"}]}`
- **Item** : Une ligne d'une liste : ce qu'elle est, à côté d'où elle vient, en dessous une note ; à droite une échéance et une marque.
  title* texte · detail texte (À côté du titre, plus discret (qui l'envoie, d'où elle vient)) · due texte (Son échéance, date ISO 8601, en pastille) · level texte (normal (par défaut) ou low (une ligne, discrète). Ce qui attend l'utilisateur est la carte de l'élément, sur sa ligne) · mark texte (Une marque courte (« extérieur »)) · note texte (Ce qu'il faut en savoir, en une phrase)
- **Message** : Un message : un mail, un message d'une messagerie, une note reçue.
  from* texte · body texte (Le texte entier, quand il compte) · date texte (Date ISO 8601) · excerpt texte (Le début, en une ou deux lignes) · subject texte
- **Person** : Une personne : son nom, ce qu'elle est pour l'utilisateur, une note (ce qu'elle attend, le dernier échange) et quelques faits en lignes (téléphone, adresse, prochain rendez-vous).
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
