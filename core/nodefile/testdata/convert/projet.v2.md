---
format: 2
id: j2r6t9w3
title: La terrasse
created: 2026-11-12T09:00:00+01:00
---
Les travaux de la terrasse, avec Sam.

```eriphe:block etapes
{"components": [
  {"id":"root","component":"Column","children":["h","tl","lien","sep","asks"]},
  {"id":"h","component":"Text","text":"Où on en est","variant":"subtitle"},
  {"id":"tl","component":"Timeline","items":[
    {"label":"Devis signé","state":"done","date":"2026-11-20"},
    {"label":"Commande du bois","state":"current","date":"2026-11-28","detail":"Livraison la semaine suivante."},
    {"label":"Pose","state":"pending"}
  ]},
  {"id":"lien","component":"Link","label":"Le devis en ligne","url":"https://example.org/devis/terrasse"},
  {"id":"sep","component":"Divider"},
  {"id":"asks","component":"Asks","items":["Relance Sam pour le bois",{"text":"Branche l'agenda","until":{"tool":"agenda"}}]}
]}
```

```eriphe:block carte
{"card": "skill:3"}
```

```eriphe:block suivi
{"components":[
  {"id":"root","component":"Row","children":["a","b"]},
  {"id":"a","component":"Button","label":"Relancer","action":{"event":{"name":"ask","context":{"text":"Relance Sam"}}}},
  {"id":"b","component":"Button","label":"Voir le planning","action":{"event":{"name":"ask","context":{"text":"Montre le planning [complet]"}}},"until":{"monitor":"bois"}}
]}
```
