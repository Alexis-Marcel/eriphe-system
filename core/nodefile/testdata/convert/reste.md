---
format: 4
id: r3s8t1e6
title: Ce qui ne se convertit pas
created: 2026-11-15T09:00:00+01:00
---
```json
{"components": [
  {"id":"root","component":"Column","children":["champ","note"]},
  {"id":"champ","component":"TextField","label":"Ton avis","value":{"@path":"/avis"}},
  {"id":"note","component":"Text","text":"Merci"}
],
 "data": {"avis": "Très bien"}}
```

```json
{
  "values": {
    "inutile": {
      "x": 1
    }
  }
}
```

Pas encore lu

````go
x := "```"
fmt.Println(x)
````

> [!person] Sam
> - Rôle : artisan
> - Téléphone : 01 23 45 67 89
>
> Préfère être appelé le matin.

> Le bois sera là mardi.
> — Sam, 24/11/2026

> [!warning] Bois en retard
> Deux jours de retard.

Un **texte** avec [un lien](https://example.org) et \[pas une action](ask:).

**Le bois** · Sam · 24/11/2026

> Il arrive mardi.

> [!chart|bar] Dépenses
> | x | Dépenses (€) |
> |---|---|
> | 2026-10 | 450 |
> | 2026-11 | 1280 |
