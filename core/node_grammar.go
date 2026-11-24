package main

// nodeGrammar is what catalog_read gives after the components
// (a2ui.CatalogPrompt): what a block of a node is beyond its components,
// as nodefile and code read it (docs/node-format.md). It is the one place
// the brain and the workers learn it; its example is checked by
// TestNodeGrammarExample.
const nodeGrammar = "# Un bloc d'Eriphe\n\n" +
	"Le corps d'un nœud est du Markdown ; un bloc s'y écrit entre une ligne ```eriphe:block <nom> et une ligne ``` " +
	"(nom : minuscules, chiffres et _, 32 au plus). Entre les deux, un objet JSON : `components` (la liste plate ci-dessus, " +
	"ou un seul composant, que le code complète en `root`), `data` (ses valeurs), `sources` (ses sources vivantes, par nom). " +
	"Un bloc qui ne contient que `{\"card\": \"<ref>\"}` place la carte d'une de tes propositions de ce nœud à cet endroit du texte ; " +
	"sinon une carte se montre sur les lignes de ses éléments, ou au pied de la page. " +
	"Une proposition datée (un rendez-vous à ajouter, une heure posée) se montre dans l'`Agenda` qui dessine son jour, avec son Oui, et non au pied ; " +
	"un `Agenda` dont les `events` lisent les éléments d'une source porte leurs cartes sur leurs événements. " +
	"Ses événements ont la forme que rend l'agenda (`title`, `start`, `end`, `place`, `allDay`, `status`) : lie-les tels quels.\n\n" +
	"## Sources\n" +
	"Le code les lit sans modèle et les tient à jour (une lecture qui échoue garde la dernière bonne valeur) ; la valeur d'une source est en `/<nom>`. Une source est :\n" +
	"- un outil, une lecture libre (une tâche : un outil qu'on lui a prêté) : `{\"tool\": \"serveur/outil\", \"args\": {…}}` (sources_read les liste, avec leurs arguments) ;\n" +
	"- des nœuds du graphe, comme `node_find` les trouve : `{\"tool\": \"eriphe-core/node_find\", \"args\": {\"tags\": [\"etude\"], \"where\": [{\"key\": \"temps\", \"op\": \"<\", \"value\": 20}], \"sort\": \"temps\", \"columns\": [\"temps\"]}}` ; " +
	"sa valeur est `{\"nodes\": [{\"id\", \"title\", \"status\", \"changed\", et chaque colonne}]}`, relue dès qu'un nœud change ; une `Table` ou une `Gallery` les montre (`\"rows\": \"<nom>\"`), une ligne ouvre son nœud. Un titre écrit après une lecture extérieure, les propriétés d'un nœud extérieur n'y sont qu'avec `include_external` : le bloc est alors extérieur ;\n" +
	"- une ressource : `{\"resource\": \"serveur\", \"uri\": \"…\"}` ;\n" +
	"- une surveillance approuvée, par son nom : `{\"monitor\": \"nom\"}`.\n" +
	"`every` : les minutes entre deux lectures (1 à 1440 ; le défaut sinon). Après un appel qui change quelque chose sur un serveur, ses sources sont relues aussitôt : rien à rafraîchir.\n\n" +
	"**Éléments.** Une source qui liste des choses dit où elles sont : `\"items\": {\"path\": \"messages\", \"id\": \"id\", \"title\": \"subject\", \"detail\": \"from\"}` ; " +
	"chaque élément est une ligne qu'un skill note (`item_note`). `\"open\": {\"tool\": \"serveur/outil\", \"id\": \"<paramètre>\"}`, dans `items`, " +
	"nomme l'outil de lecture du même serveur qui lit un élément entier par son id : l'utilisateur ouvre la ligne sur place.\n\n" +
	"**Sections.** `Sections` range les éléments d'après leurs notes (`item_note` : sa note, son état act, look, info, none ou done, son groupe) : " +
	"d'abord les groupes où un élément est à faire (act) ou à regarder (look), une ligne chacun, ta note et sa carte (`_card`) ; " +
	"un groupe dont les éléments partagent une carte (le même appel sur plusieurs) est un lot replié qui porte son oui, où l'on décoche ; les autres groupes se replient ; un élément fait (done) s'en va.\n\n" +
	"**Coup d'œil.** Une source peut porter `glance`, un seul par nœud : `{\"label\": \"PEA\", \"path\": \"total\", \"threshold\": 2}` " +
	"(`path` : champs séparés par des points, l'indice d'une liste en chiffre ; `threshold` en % de la référence, ou dans l'unité du chiffre avec `\"unit\": \"amount\"` ; " +
	"référence : la première valeur du jour, `\"compare\": \"previous\"` la précédente, `\"compare\": \"fixed\"` avec `\"base\"`). " +
	"Quand le chiffre dépasse le seuil, la bulle du nœud le montre jusqu'à ce que l'utilisateur l'ouvre : c'est la seule façon de dire qu'un chiffre a bougé, jamais une demande.\n\n" +
	"## Exemple : une vue de mails en sections\n" +
	"```json\n" + nodeGrammarExample + "\n```\n"

// nodeGrammarExample is the grammar's example block, checked by
// TestNodeGrammarExample.
const nodeGrammarExample = `{"sources": {"mails": {"tool": "mail/search_messages", "args": {"query": "in:inbox"}, "every": 15,
  "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from", "open": {"tool": "mail/read_message", "id": "id"}}}},
 "components": {"component": "Sections", "items": "mails"}}`
