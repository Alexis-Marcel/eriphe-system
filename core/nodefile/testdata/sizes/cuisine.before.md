Avec ce qu'il reste au frigo, quatre recettes sont possibles ce soir, la plus rapide en 15 min. Les œufs sont à finir avant jeudi.

## Le frigo

- Œufs (6)
- Crème fraîche
- Épinards
- Parmesan
- Lardons
- Citrons (2)
- Beurre
- Yaourts (4)
- Lait

```eriphe:block ce_soir
{"sources":{"ce_soir":{"tool":"eriphe-core/node_find","args":{"tags":["cuisine/recette"],"where":[{"key":"manque","op":"=","value":0}],"sort":"temps","columns":["note","temps","photo"]}}},"components":[{"id":"root","component":"Column","children":["h","g"]},{"id":"h","component":"Text","variant":"subtitle","text":"Possible ce soir"},{"id":"g","component":"Grid","size":"small","children":{"path":"/ce_soir/nodes","componentId":"c"}},{"id":"c","component":"Card","child":"cc"},{"id":"cc","component":"Column","children":["img","name","meta"]},{"id":"img","component":"Image","url":{"@path":"photo"},"alt":{"@path":"title"},"fit":"cover"},{"id":"name","component":"Text","text":{"@path":"title"}},{"id":"meta","component":"Text","variant":"caption","text":{"@call":"formatString","args":{"value":"${temps} min · ${note}/5"}}}]}
```

```eriphe:block recettes
{"sources":{"recettes":{"tool":"eriphe-core/node_find","args":{"tags":["cuisine/recette"],"sort":"-note","columns":["note","temps"]}}},"components":[{"id":"root","component":"Column","children":["h","t"]},{"id":"h","component":"Text","variant":"subtitle","text":"Toutes mes recettes"},{"id":"t","component":"Table","columns":[{"key":"title","label":"Recette"},{"key":"note","label":"Note (/5)","kind":"number"},{"key":"temps","label":"Temps (min)","kind":"number"}],"rows":{"@path":"/recettes/nodes"}}]}
```

```eriphe:block reponses
{"components":[{"id":"root","component":"Row","children":["b0","b1"]},{"id":"b0","component":"Button","variant":"primary","label":"Fais la liste de courses","action":{"event":{"name":"ask","context":{"text":"Fais la liste de courses"}}}},{"id":"b1","component":"Button","label":"Propose le menu de la semaine","action":{"event":{"name":"ask","context":{"text":"Propose le menu de la semaine"}}}}]}
```

