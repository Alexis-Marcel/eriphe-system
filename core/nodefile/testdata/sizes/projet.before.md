Le devis de Sam est signé : il attend l'acompte de 30 % (1 260 €) avant vendredi pour commander le bois. Le chantier commence lundi 7 décembre.

## Ce qu'on a décidé

- Pin traité classe 4, pas de composite
- Pose par Sam, 4 200 € posé
- Début lundi 7 décembre

## Ensuite

```eriphe:block etapes
{"components":[{"id":"root","component":"Column","children":["p","t"]},{"id":"p","component":"Progress","label":"En cours","value":2,"current":1,"total":6,"step":"Verser l'acompte de 30 %"},{"id":"t","component":"Timeline","items":[{"label":"Choisir le bois","state":"done","date":"2026-11-02","detail":"Pin traité classe 4"},{"label":"Devis de Sam signé","state":"done","date":"2026-11-18"},{"label":"Verser l'acompte de 30 %","state":"current","date":"2026-11-27","detail":"1 260 €, avant vendredi"},{"label":"Commande du bois","state":"pending","date":"2026-11-30"},{"label":"Début du chantier","state":"pending","date":"2026-12-07"},{"label":"Réception des travaux","state":"pending","date":"2026-12-18"}]}]}
```

```eriphe:block sam
{"components":[{"id":"root","component":"Person","name":"Sam Martin","detail":"Artisan, la terrasse","note":"Attend l'acompte pour commander le bois.","facts":[{"label":"Téléphone","value":"06 00 00 00 00"},{"label":"Mail","value":"sam@example.org"}]}]}
```

## Notes

La mairie n'exige pas de déclaration sous 20 m² : la terrasse fait 18 m². Voir [[Devis de Sam]] et [[Choix du bois]]. Léa prête sa scie circulaire pour les finitions.

```eriphe:block reponses
{"components":[{"id":"root","component":"Row","children":["b0","b1"]},{"id":"b0","component":"Button","variant":"primary","label":"Prépare le virement de l'acompte","action":{"event":{"name":"ask","context":{"text":"Prépare le virement de l'acompte"}}}},{"id":"b1","component":"Button","label":"Relance Sam pour la date","action":{"event":{"name":"ask","context":{"text":"Relance Sam pour la date"}}}}]}
```

