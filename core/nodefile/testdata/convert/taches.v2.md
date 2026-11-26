---
format: 2
id: t5c2h8s1
title: Mes tâches
created: 2026-11-01T09:00:00+01:00
---
```eriphe:block liste
{"components": [
  {"id":"root","component":"Column","children":["l","faites"]},
  {"id":"l","component":"List","children":{"componentId":"row","path":"/todo"}},
  {"id":"row","component":"Item","title":{"@path":"title"},"due":{"@path":"due"},"note":{"@path":"note"}},
  {"id":"faites","component":"Fold","label":"{n} faites","list":"done","child":"done"},
  {"id":"done","component":"List","children":{"componentId":"drow","path":"/done"}},
  {"id":"drow","component":"Item","title":{"@path":"title"},"level":"low"}
],
 "data": {"todo":[
   {"title":"Appeler le plombier","due":"2026-11-28","note":"le matin"},
   {"title":"Renouveler le passeport","due":"bientôt"},
   {"title":"Payer la cantine"}
 ],"done":[{"title":"Rendre les livres"},{"title":"Changer l'ampoule"}]}}
```
