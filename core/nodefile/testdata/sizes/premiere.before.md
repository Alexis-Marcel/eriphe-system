Je relis tes flux toutes les heures et je te garde ici ce qui vaut la peine d'être lu, le plus court d'abord.

```eriphe:block lectures
{"sources":{"lectures":{"tool":"lectures/list_articles","args":{"unread":true},"every":60,"items":{"path":"articles","id":"id","title":"title","detail":"site","open":{"tool":"lectures/read_article","id":"id"}}}},"components":[{"id":"root","component":"Column","children":["h","l","info"]},{"id":"h","component":"Text","variant":"subtitle","text":"À lire"},{"id":"l","component":"List","children":{"path":"/lectures/articles[_group=À lire][_state!=done]","componentId":"row"},"emptyText":"Rien à lire pour l'instant : je relis tes flux toutes les heures."},{"id":"row","component":"Item","title":{"@path":"title"},"detail":{"@path":"site"},"note":{"@path":"_note"}},{"id":"info","component":"Fold","label":"Les {n} articles pour info","child":"infos"},{"id":"infos","component":"List","children":{"path":"/lectures/articles[_group=Pour info]","componentId":"inforow"}},{"id":"inforow","component":"Item","level":"low","title":{"@path":"title"},"detail":{"@path":"site"},"note":{"@path":"_note"}}]}
```

