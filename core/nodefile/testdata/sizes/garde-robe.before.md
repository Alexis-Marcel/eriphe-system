Demain 8° et pluie l'après-midi : je propose le pull marine, le chino beige, le manteau camel et les boots. Cinq pièces n'ont pas été portées cette saison.

```eriphe:block tenue
{"components":[{"id":"root","component":"Column","children":["h","g"]},{"id":"h","component":"Text","variant":"subtitle","text":"La tenue de demain"},{"id":"g","component":"Grid","size":"small","children":{"path":"/tenue","componentId":"c"}},{"id":"c","component":"Card","child":"cc"},{"id":"cc","component":"Column","children":["img","t"]},{"id":"img","component":"Image","url":{"@path":"photo"},"alt":{"@path":"title"},"fit":"cover"},{"id":"t","component":"Text","variant":"caption","text":{"@path":"title"}}],"data":{"tenue":[{"title":"Pull col rond marine","photo":"/espace/media?url=pull-marine.svg"},{"title":"Chino beige","photo":"/espace/media?url=bas-beige.svg"},{"title":"Manteau camel","photo":"/espace/media?url=veste-camel.svg"},{"title":"Boots marron","photo":"/espace/media?url=chaussure-brun.svg"}]}}
```

```eriphe:block chiffres
{"components":[{"id":"root","component":"Row","children":["n","jamais","plus"]},{"id":"n","component":"Metric","label":"Pièces","value":18},{"id":"jamais","component":"Metric","label":"Jamais portées cette saison","value":5},{"id":"plus","component":"Metric","label":"La plus portée","value":"Jean brut","deltaLabel":"31 fois"}]}
```

```eriphe:block pieces
{"sources":{"pieces":{"tool":"eriphe-core/node_find","args":{"tags":["garde-robe"],"sort":"categorie","columns":["categorie","photo","portee"]}}},"components":[{"id":"root","component":"Column","children":["h","g"]},{"id":"h","component":"Text","variant":"subtitle","text":"Toutes mes pièces"},{"id":"g","component":"Grid","size":"small","children":{"path":"/pieces/nodes","componentId":"c"}},{"id":"c","component":"Card","child":"cc"},{"id":"cc","component":"Column","children":["img","name","meta"]},{"id":"img","component":"Image","url":{"@path":"photo"},"alt":{"@path":"title"},"fit":"cover"},{"id":"name","component":"Text","text":{"@path":"title"}},{"id":"meta","component":"Text","variant":"caption","text":{"@call":"formatString","args":{"value":"${categorie} · portée ${portee} fois"}}}]}
```

