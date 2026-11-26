---
format: 2
id: k3v9q2m7
title: Mes mails
goal: Tableau de bord des mails à traiter, tenu par le skill mails.
created: 2026-11-17T09:00:00+01:00
---
Les mails de la boîte, triés par le skill.

```eriphe:block mails
{"components": [
  {"id":"root","component":"Column","children":["titre","sections","relancer"]},
  {"id":"titre","component":"Text","text":"Mes mails","variant":"title"},
  {"id":"sections","component":"Sections","items":"mails"},
  {"id":"relancer","component":"Button","label":"Trier","action":{"event":{"name":"ask","context":{"text":"Trie mes mails maintenant"}}}}
],
 "sources": {"mails": {"tool":"mail/search_messages","args":{"query":"in:inbox"},"items":{"path":"messages","id":"id","title":"subject","detail":"from","open":{"tool":"mail/read_message","id":"id"}},"every":5}}}
```
