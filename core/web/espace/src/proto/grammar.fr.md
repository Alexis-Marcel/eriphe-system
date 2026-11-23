- **Figures** : Quelques chiffres côte à côte ; le premier bloc d'une page les montre en grand.
  items* [{label* texte · value* texte ou nombre · unit texte · delta nombre · deltaLabel texte · trend [nombre, …]}, …] ou liaison · title texte
- **Sections** : Les éléments d'une source, rangés par le code : ce qui est à faire d'abord, une ligne chacun avec ta note et sa carte ; un lot (une carte sur plusieurs) replié avec son Oui ; le reste replié. Vide, chargement, erreur : le code les montre.
  items* nom de la source · title texte · row {title, detail, note} (les champs de l'élément, si ceux de `items` ne vont pas)
  Exemple : `{"component": "Sections", "items": "mails"}`
- **Gallery** : Des images en vignettes, leur titre dessous ; `by` ajoute un filtre par valeur d'un champ.
  items* nom de la source ou [{title, photo, …}, …] · title texte · by champ · units {champ: unité}
- **Asks** : Des réponses probables en boutons ; un clic t'envoie son texte ; le premier est celui par défaut.
  items* [texte, …]
- **Table** : `columns` devient facultatif (les champs de la première ligne, dans l'ordre ; la clé est l'en-tête) ; `rows` prend aussi le nom d'une source ; `units` {colonne: unité}.
- Unités (`units`, `unit`) : `€`, `%`, `±%` (un écart signé), `★` (une note sur 5), ou un mot (« min »).
- **Chart** : `series` prend aussi {nom: [nombres]} (une valeur par jour jusqu'à `end`) ou {nom: {x: y}}.
- **Timeline** : dit d'elle-même où en est le travail (« 2 sur 6 », l'étape en cours) : plus besoin de Progress.
