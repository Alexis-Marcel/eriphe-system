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
{"sources":{"ce_soir":{"tool":"eriphe-core/node_find","args":{"tags":["cuisine/recette"],"where":[{"key":"manque","op":"=","value":0}],"sort":"temps","columns":["note","temps","photo"]}}},"components":{"component":"Gallery","title":"Possible ce soir","items":"ce_soir","units":{"temps":"min","note":"★"}}}
```

```eriphe:block recettes
{"sources":{"recettes":{"tool":"eriphe-core/node_find","args":{"tags":["cuisine/recette"],"sort":"-note","columns":["note","temps"]}}},"components":{"component":"Table","title":"Toutes mes recettes","rows":"recettes","units":{"note":"★","temps":"min"}}}
```

```eriphe:block reponses
{"components":{"component":"Asks","items":["Fais la liste de courses","Propose le menu de la semaine"]}}
```

