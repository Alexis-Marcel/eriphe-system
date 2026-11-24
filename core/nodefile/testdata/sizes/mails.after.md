2 réponses attendues, dont Sam avant vendredi. 1 rendez-vous à confirmer. 2 tentatives d'hameçonnage dans le lot à jeter.

**Pour info** · La facture d'électricité de novembre est arrivée (62 €, prélevés le 5 décembre). Ton colis a été livré hier. Le club avance l'entraînement de samedi à 9 h.

```eriphe:block sam
{"components":{"component":"Quote","text":"Voici le devis pour la terrasse : 4 200 € posé, début possible lundi 7 décembre. Tu peux me dire avant vendredi si ça te va ?","from":"Sam <sam@example.org>","date":"2026-11-24T13:17:14.603Z"}}
```

```eriphe:block mails
{"sources":{"mails":{"tool":"mail/search_messages","args":{"query":"in:inbox","max":150},"items":{"path":"messages","id":"id","title":"subject","detail":"from","open":{"tool":"mail/read_message","id":"id"}}}},"components":{"component":"Sections","items":"mails"}}
```

```eriphe:block semaine
{"components":{"component":"Figures","title":"Cette semaine","items":[{"label":"Mails reçus","value":212,"delta":-46,"deltaLabel":"sur la semaine passée","trend":[41,38,35,29,33,22,14]},{"label":"Jetés sur ton oui","value":164,"trend":[30,31,26,22,27,17,11]},{"label":"Réponses envoyées","value":9,"delta":2,"deltaLabel":"sur la semaine passée"}]}}
```

