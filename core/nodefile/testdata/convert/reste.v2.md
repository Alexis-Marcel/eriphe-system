---
format: 2
id: r3s8t1e6
title: Ce qui ne se convertit pas
created: 2026-11-15T09:00:00+01:00
---
```eriphe:block formulaire
{"components": [
  {"id":"root","component":"Column","children":["champ","note"]},
  {"id":"champ","component":"TextField","label":"Ton avis","value":{"@path":"/avis"}},
  {"id":"note","component":"Text","text":"Merci"}
],
 "data": {"avis": "Très bien"}}
```

```eriphe:block jamais_lu
{"components": [{"id":"root","component":"List","children":{"componentId":"r","path":"/flux/items"},"emptyText":"Pas encore lu"},
  {"id":"r","component":"Item","title":{"@path":"title"}}],
 "sources": {"flux": {"tool":"rss/read_feed","items":{"path":"items","id":"link","title":"title"}}}}
```

```eriphe:block code
{"components": [{"id":"root","component":"Code","language":"go","text":"x := \"```\"\nfmt.Println(x)"}]}
```

```eriphe:block gens
{"components": [
  {"id":"root","component":"Column","children":["p","q","al","m","msg"]},
  {"id":"p","component":"Person","name":"Sam","detail":"artisan","note":"Préfère être appelé le matin.","facts":[{"label":"Téléphone","value":"01 23 45 67 89"}]},
  {"id":"q","component":"Quote","text":"Le bois sera là mardi.","from":"Sam","date":"2026-11-24"},
  {"id":"al","component":"Alert","level":"error","title":"Bois en retard","text":"Deux jours de retard."},
  {"id":"m","component":"Markdown","text":"Un **texte** avec [un lien](https://example.org) et [pas une action](ask:)."},
  {"id":"msg","component":"Message","from":"Sam","subject":"Le bois","date":"2026-11-24T10:00:00Z","excerpt":"Il arrive mardi."}
]}
```

```eriphe:block courbe
{"components": [{"id":"root","component":"Chart","kind":"bar","title":"Dépenses","unit":"€","series":[{"name":"Dépenses","points":[{"x":"2026-10","y":450},{"x":"2026-11","y":1280}]}]}]}
```
