# Écrire une surface (A2UI 1.0)

Une surface est une tuile de l'espace de travail : un titre, et dedans des composants de ce catalogue. Elle s'écrit comme une liste plate de composants ; chacun a un `id` unique (lettres, chiffres et `_`, ni tiret ni chiffre au début), un `component` (un nom ci-dessous) et ses propriétés. Un seul composant a l'id `root` : la racine. Un composant désigne ses enfants par leur id (`child`, `children`, `tabs`), jamais en les écrivant dedans.

**Valeurs.** Une propriété de type texte, nombre, booléen ou valeur prend un littéral (`"Bonjour"`, `42`), une liaison vers les données de la surface `{"@path": "/ventes/total"}` (un pointeur JSON) ou un appel de fonction `{"@call": "formatNumber", "args": {"value": {"@path": "/total"}}}`. Une propriété marquée « ou liaison » prend une liste littérale ou une liaison vers une liste de même forme.

**Listes répétées.** `children` prend une liste d'ids `["a", "b"]` ou un modèle `{"path": "/mails", "componentId": "ligne"}` qui répète le composant `ligne` pour chaque élément de `/mails`. Dans ce modèle et ses enfants, un chemin sans `/` au début (`{"@path": "sujet"}`) se lit dans l'élément ; un chemin qui commence par `/` se lit depuis la racine des données.

**Règles.**
- Les composants sont des formes, jamais des outils : montre ce qu'un outil rend avec la forme qui lui va (un chiffre : Metric ; une série : Chart ; des lignes : Table ; des étapes : Timeline ; un mail ou un message : Message ; une journée : Day).
- Une surface dit une chose : peu de composants, l'essentiel d'abord. Pas de texte qui répète un titre.
- Lie les composants aux champs que l'outil rend (`@path`) plutôt que de recopier les valeurs : le code relit l'outil et la surface reste à jour.
- Un bouton ne fait qu'une action, d'une liste fermée ; il n'écrit jamais dans un outil. `ask` demande quelque chose à Eriphe avec tes propres mots : `{"event": {"name": "ask", "context": {"text": "Relance la sauvegarde"}}}`, un texte écrit tel quel et rien d'autre, jamais une liaison ; le code y ajoute la surface et le bouton, et Eriphe le reçoit comme un message de l'utilisateur. Les noms locaux `open`, `expand` et `tab` sont les gestes de la page, sans contexte : un bouton n'en a presque jamais besoin. Une proposition se montre avec un composant Proposal, qui seul envoie le oui ou le non (`answer`) ; jamais un bouton. Tout autre nom est refusé.
- N'invente ni composant, ni propriété, ni fonction : ce qui n'est pas ci-dessous est refusé.

## Exemple : des mails, liés aux données
```json
[
  {"id": "root", "component": "Card", "title": "Mails à traiter", "child": "mails"},
  {"id": "mails", "component": "List", "children": {"path": "/messages", "componentId": "mail"}, "emptyText": "Rien à traiter"},
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
  text* texte · variant title|subtitle|body|caption (Son style ; body par défaut)
- **Markdown** : Un texte plus long en Markdown simple (titres, listes, gras, liens), sans HTML ni image.
  text* texte
- **Image** : Une image.
  url* texte (Son adresse) · alt texte (Ce qu'elle montre, en quelques mots) · fit contain|cover
- **Icon** : Une icône.
  name* alert|arrowDown|arrowUp|calendar|chart|check|clock|close|cloud|document|error|event|folder|home|info|link|lock|mail|message|money|note|person|phone|place|refresh|search|server|settings|star|task|trendDown|trendUp|warning ou liaison
- **Row** : Des enfants côte à côte.
  children* enfants ([ids] ou modèle {path, componentId}) · align start|center|end|stretch · justify start|center|end|spaceBetween
- **Column** : Des enfants l'un sous l'autre.
  children* enfants ([ids] ou modèle {path, componentId}) · align start|center|end|stretch · justify start|center|end|spaceBetween
- **List** : Une liste d'éléments semblables, souvent répétés depuis les données par un modèle.
  children* enfants ([ids] ou modèle {path, componentId}) · direction vertical|horizontal · emptyText texte (Ce qui s'affiche quand la liste est vide)
- **Card** : Un cadre autour d'un enfant, avec un titre.
  child* id d'un composant · title texte
- **Tabs** : Des onglets, un enfant par onglet.
  tabs* [{title* texte · child* id d'un composant}, …]
- **Divider** : Un trait qui sépare.
- **Button** : Un bouton qui déclenche une action.
  label* texte · action* action ({"event": {"name", "context"}} ou {"functionCall": appel}) · icon alert|arrowDown|arrowUp|calendar|chart|check|clock|close|cloud|document|error|event|folder|home|info|link|lock|mail|message|money|note|person|phone|place|refresh|search|server|settings|star|task|trendDown|trendUp|warning ou liaison · variant primary|default|quiet
  Exemple : `{"id": "relancer", "component": "Button", "action": {"event": {"context": {"text": "Relance la sauvegarde de cette nuit"}, "name": "ask"}}, "label": "Relancer la sauvegarde"}`
- **Choice** : Un choix parmi des options, rangé dans les données de la surface.
  options* [{label* texte · value* texte fixe}, …] · value* liste de textes (Les valeurs choisies : une liaison vers une liste de textes) · label texte · multiple booléen fixe (Plusieurs choix possibles)
- **TextField** : Un champ de saisie, rangé dans les données de la surface.
  value* texte (Une liaison vers le texte saisi) · label texte · multiline booléen fixe · placeholder texte
- **DateField** : Un champ de date ou d'heure, rangé dans les données de la surface.
  value* texte (Une liaison vers la date ISO 8601) · label texte · mode date|time|dateTime
- **Metric** : Un chiffre clé et son évolution.
  label* texte · value* texte ou nombre · better up|down (Le sens qui est une bonne nouvelle) · delta nombre (L'écart signé depuis la dernière fois) · deltaLabel texte (Depuis quand, en quelques mots (« depuis hier »)) · unit texte
- **Chart** : Une ou plusieurs séries en courbes ou en barres.
  kind* line|bar · series* [{name* texte fixe · points* [{x* texte fixe ou nombre fixe (Une date ISO 8601, un nombre ou une étiquette) · y* nombre fixe}, …]}, …] ou liaison · unit texte (L'unité des valeurs)
  Exemple : `{"id": "conso", "component": "Chart", "kind": "bar", "series": [{"name": "Maison", "points": [{"x": "lun.", "y": 12.5}, {"x": "mar.", "y": 9.8}]}], "unit": "kWh"}`
- **Table** : Des lignes en colonnes.
  columns* [{key* texte fixe (Le champ de la ligne à montrer) · label* texte fixe · kind text|number|date}, …] · rows* [objet, …] ou liaison (Une ligne est un objet ; chaque colonne lit le champ de sa clé) · emptyText texte
  Exemple : `{"id": "tickets", "component": "Table", "columns": [{"key": "title", "label": "Ticket"}, {"key": "age", "kind": "number", "label": "Âge"}], "rows": {"@path": "/issues"}}`
- **Timeline** : Des étapes dans l'ordre, chacune avec son état.
  items* [{label* texte fixe · state* done|current|pending|failed · date texte fixe (Date ISO 8601) · detail texte fixe}, …] ou liaison
  Exemple : `{"id": "deploiement", "component": "Timeline", "items": [{"label": "Construire", "state": "done"}, {"label": "Tester", "state": "current"}, {"label": "Publier", "state": "pending"}]}`
- **Message** : Un message : un mail, un message d'une messagerie, une note reçue.
  from* texte · body texte (Le texte entier, quand il compte) · date texte (Date ISO 8601) · excerpt texte (Le début, en une ou deux lignes) · subject texte
- **Day** : Une journée et ses événements, dans l'ordre des heures.
  date* texte (Le jour, AAAA-MM-JJ) · events* [{title* texte fixe · start* texte fixe (Date et heure ISO 8601, ou la date seule pour toute la journée) · allDay booléen fixe · end texte fixe · place texte fixe}, …] ou liaison
  Exemple : `{"id": "demain", "component": "Day", "date": "2026-11-13", "events": [{"end": "2026-11-13T10:00:00+01:00", "place": "Centre", "start": "2026-11-13T09:30:00+01:00", "title": "Dentiste"}]}`
- **Proposal** : Une proposition d'Eriphe qui attend un oui ou un non ; ses boutons Oui et Non envoient `answer`, que le code lie à cette proposition. Sur une surface au contenu extérieur, elle se répond dans la conversation.
  proposalId* texte (L'identifiant de la proposition)
- **Alert** : Un avertissement qui doit se voir.
  text* texte · level info|success|warning|error · title texte
- **Placeholder** : Une place réservée pendant que la surface se prépare.
  text texte (Ce qui se prépare (« En préparation » par défaut))

## Fonctions
Un appel s'écrit `{"@call": "nom", "args": {…}}`, là où une valeur est attendue.

- **formatString**(value* texte) → texte : Un texte où `${/chemin}` (ou `${chemin}` dans un modèle) est remplacé par la donnée.
- **formatNumber**(value* nombre · decimals nombre (Le nombre de décimales)) → texte : Un nombre écrit à la française.
- **formatDate**(value* valeur · format* texte) → texte : Une date ISO 8601 écrite selon un motif Unicode (« EEEE d MMMM », « HH:mm »).
- **formatRelative**(value* valeur) → texte : Une date ISO 8601 dite par rapport à maintenant (« il y a 2 h », « demain »).
