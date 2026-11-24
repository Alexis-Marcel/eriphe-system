2 réponses attendues, dont Sam avant vendredi. 1 rendez-vous à confirmer. 2 tentatives d'hameçonnage dans le lot à jeter.

**Pour info** · La facture d'électricité de novembre est arrivée (62 €, prélevés le 5 décembre). Ton colis a été livré hier. Le club avance l'entraînement de samedi à 9 h.

```eriphe:block sam
{"components":[{"id":"root","component":"Quote","text":"Voici le devis pour la terrasse : 4 200 € posé, début possible lundi 7 décembre. Tu peux me dire avant vendredi si ça te va ?","from":"Sam <sam@example.org>","date":"2026-11-24T13:17:14.603Z"}]}
```

```eriphe:block mails
{"sources":{"mails":{"tool":"mail/search_messages","args":{"query":"in:inbox","max":150},"items":{"path":"messages","id":"id","title":"subject","detail":"from","open":{"tool":"mail/read_message","id":"id"}}}},"components":[{"id":"root","component":"Column","children":["todo_h","todo","trash","info"]},{"id":"todo_h","component":"Text","variant":"subtitle","text":"À traiter"},{"id":"todo","component":"List","children":{"path":"/mails/messages[_group=À traiter][_state!=done]","componentId":"todo_item"},"emptyText":"Rien ne t'attend."},{"id":"todo_item","component":"Item","title":{"@path":"_note"},"detail":{"@path":"from"},"note":{"@path":"subject"}},{"id":"trash","component":"Fold","list":"jeter","label":"{n} mails inutiles à jeter","child":"jeter"},{"id":"jeter","component":"List","children":{"path":"/mails/messages[_group=À jeter][_state!=done]","componentId":"jeter_row"}},{"id":"jeter_row","component":"Item","level":"low","title":{"@path":"subject"},"detail":{"@path":"from"},"note":{"@path":"_note"}},{"id":"info","component":"Fold","list":"infos","label":"Les {n} mails pour info","child":"infos"},{"id":"infos","component":"List","children":{"path":"/mails/messages[_group=Pour info]","componentId":"info_row"}},{"id":"info_row","component":"Item","level":"low","title":{"@path":"subject"},"detail":{"@path":"from"},"note":{"@path":"_note"}}]}
```

```eriphe:block semaine
{"components":[{"id":"root","component":"Column","children":["h","row"]},{"id":"h","component":"Text","variant":"subtitle","text":"Cette semaine"},{"id":"row","component":"Row","children":["recus","jetes","repondus"]},{"id":"recus","component":"Metric","label":"Mails reçus","value":212,"delta":-46,"deltaLabel":"sur la semaine passée","trend":[41,38,35,29,33,22,14]},{"id":"jetes","component":"Metric","label":"Jetés sur ton oui","value":164,"trend":[30,31,26,22,27,17,11]},{"id":"repondus","component":"Metric","label":"Réponses envoyées","value":9,"delta":2,"deltaLabel":"sur la semaine passée"}]}
```

