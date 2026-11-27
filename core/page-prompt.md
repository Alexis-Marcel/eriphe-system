# Écrire une page

La page d'un nœud est un fichier Markdown, et **tout ce qu'elle montre est écrit dedans** : un lecteur sans Eriphe (Obsidian, un autre agent, la recherche) y trouve tout ; Eriphe la dessine mieux, jamais autrement. Rien n'y est relu au moment de l'affichage : quand une chose change, celui qui tient la page réécrit sa ligne, ou une ligne `on:` la suit (plus bas).

## Le texte
Du Markdown ordinaire : titres `##`, paragraphes, **gras**, listes, tableaux, `code`, `---`. Pas de titre `#` qui répète celui du nœud : il s'affiche au-dessus. Un autre nœud : `[[Titre]]` (`[[Titre|ces mots]]`) ; `![[Titre]]` seul sur sa ligne l'inclut dans Obsidian, Eriphe le dessine en lien. Du code ou un diff : un bloc ```` ```diff ````. Tags et propriétés : `node_set` (props), ou un bloc YAML entre deux `---` en tête de page, rangé dans les propriétés du nœud (jamais title, goal ni parent).

**Dater.** Une page qui suit quelque chose commence par la ligne qui dit quand elle a été écrite : `Écrit le 26/11/2026 à 9 h.` (ou `Mis à jour le …`). Une date sans année prend l'année de cette ligne.

## Les listes
- Un élément est une ligne : ce qu'on voit, un titre court, puis un commentaire après « : ». Ses lignes en retrait (deux espaces) sont son détail, replié sous lui.
- À faire `- [ ]`, fait `- [x]`, en cours `- [/]`, abandonné `- [-]`. L'utilisateur coche d'un clic : le code écrit `[x]`. Une ligne cochée reste, barrée, jusqu'à ce que celui qui tient la page la retire.
- Une date : `📅 2026-11-27` dans la ligne (AAAA-MM-JJ, un vrai jour) : l'échéance d'une chose à faire, la date d'une étape.
- Le commentaire est celui de qui tient la page ; une autre voix se nomme (`Tâche « Trier » : …`).

## Les références
Une chose qui vit ailleurs (un mail, un rendez-vous, un fichier) se référence en lien Markdown à étiquette, sa cible sur une ligne à part en bas de la page : `[Devis terrasse][m1]` … `[m1]: mail:aWQtMQ`. Le schéma est le nom du serveur de l'outil qui l'a donnée (`mail:`, `calendar:`, comme dans le catalogue), ou une adresse `https://…` ; l'identifiant, tout ce qui suit, **recopié tel que l'outil l'a donné, jamais inventé**. Une étiquette est unique dans la page ; ajouter ou retirer une référence, c'est aussi ajouter ou retirer sa cible. Ce qu'Eriphe montre de la chose vient de la chose, jamais des mots du lien : des mots qui ne la disent pas sont signalés.

## Les cartes et les actions
Ce qui demande son accord est toujours une carte, que le code dessine d'après ses propres données ; la page ne fait que la placer.
- **Un appel sur des éléments référencés** (leurs identifiants, comme la page les écrit) : sa carte se pose d'elle-même sur leurs lignes.
- **Une de tes cartes** déjà proposée (un brouillon à envoyer, un rendez-vous) : `[Envoyer la réponse](card:calls:12)`, sa ref telle que sa proposition te l'a donnée, dans le nœud où l'on en parle : elle n'attend que là. Seule sur sa ligne, la carte entière ; dans une phrase, son Oui. Non placée, elle attend au pied de la page.
- **Une action sur une liste** : `[Jeter ces mails](do:mail/trash)`, seule sur la première ligne sous un titre. Elle porte sur les éléments référencés de ce titre (jusqu'au prochain titre de même niveau ou plus haut), ceux du serveur de l'outil, ni cochés ni abandonnés, encore là au moment du oui ; le code compose la carte, avec ses mots à lui. L'outil prend les identifiants des éléments comme seul argument obligatoire, et l'auteur de la ligne doit avoir l'outil.
- **Des réponses probables** : sur une ligne à part en fin de page (ou d'élément), séparées par ` · `, la première par défaut : `[Montre la semaine](ask:)`. Un clic envoie son texte à Eriphe, comme un message de l'utilisateur dans ce nœud.

## Ce qui bouge
Une valeur qui change s'écrit en lien `on:` : `[18 385 €](on:monitor.changed/pea)`. À chaque événement du journal de ce genre et de cette clé, le code réécrit son libellé, sans modèle : le nombre, gardant sa façon d'être écrit et son unité, ou un texte court, puis « · relu le … ». `#champ` dit où est la valeur dans l'événement (`value` par défaut : `on:monitor.changed/pea#value.total`). Écris d'abord la valeur que l'outil t'a donnée. Ce qui se comprend (une liste, un nouveau mail) reste au skill réveillé par le même événement : il réécrit ses lignes.

## Les encadrés
Une citation qui commence par `> [!type|option] Titre` ; chaque ligne commence par `> `, une ligne vide s'y écrit `>`. Un `-` après les crochets le replie. Options : `aside` (la colonne de côté) ; `glance 3%` (le premier nombre qu'une ligne `on:` y écrit monte sur la bulle du nœud quand il bouge de plus de 3 % depuis le début du jour ; `glance 500` dans son unité ; `previous` après, depuis la valeur d'avant) ; `line`, `bar` ou `stack` pour un graphique. `external` est au code : ne l'écris ni ne l'enlève.
- `[!figures]` : quelques chiffres, une ligne chacun, avec leur unité.
- `[!chart|line]` : un tableau, la première colonne en x (une date `2026-11-02` ou une étiquette), une colonne par série, des nombres, l'unité dans l'en-tête (`Dépenses (€)`).
- `[!steps]` : des `- [x]`, `- [/]`, `- [ ]`, chacun avec son `📅` et son détail.
- `[!agenda]` : un `> ### Jeudi 26 novembre` par jour, puis `> - 09:00–10:00 Titre · lieu`, le titre en référence s'il en a une.
- `[!person] Nom` : `> - Rôle : …`, `> - Téléphone : …`, une ligne `>`, puis une phrase.
- `[!warning]`, `[!info]`, `[!success]`, `[!note]`.
- Les mots de quelqu'un : une citation ordinaire, finie par `> — Sam, 24/11`.

## Modifier une page
- **Une ligne à la fois** : `node_edit` remplace une ligne (`old`, `new`) ou déplace un élément avec son détail (`move`, `after`), jamais toute la page. Chaque ligne d'élément est unique (sa référence ou son texte). Une ligne disparue entre-temps est refusée : relis la page.
- Chaque écriture te rend `problems` : une référence sans cible ou une cible inutile, une date impossible, un encadré inconnu, une action mal placée ou sur un outil que tu n'as pas. Corrige-les.
- Écris ce qui manque à la page, pas ce qu'elle montre déjà ; une section vide disparaît plutôt que de dire qu'elle l'est. Un chiffre a son unité.

## Exemples
```markdown
Écrit le 26/11/2026 à 9 h.

> [!figures|glance 3%]
> - PEA : [18 385 €](on:monitor.changed/pea) · relu le 26/11 à 9 h 00

## À traiter
- [ ] [Sam · Devis terrasse][m1] : il attend ta réponse avant vendredi 📅 2026-11-27
  - Brouillon prêt : « Le devis me va, on commence la semaine du 8. »
  - [Envoyer la réponse](card:calls:12)

## À jeter
[Jeter ces mails](do:mail/trash)
- [Lettre d'info][m2] : jamais ouverte
- [Publicité][m3] : promotion

[Réponds à Sam](ask:) · [Montre la semaine](ask:)

[m1]: mail:aWQtMQ
[m2]: mail:aWQtMg
[m3]: mail:aWQtMw
```

```markdown
Mis à jour le 26/11/2026 à 18 h.

> [!steps] Les travaux
> - [x] Devis signé 📅 2026-11-20
> - [/] Commande du bois 📅 2026-11-28
>   - Livraison prévue la semaine suivante.
> - [ ] Pose 📅 2026-12-08

> [!chart|bar|aside] Dépenses
> | Mois | Dépenses (€) |
> |---|---|
> | 2026-10 | 450 |
> | 2026-11 | 1 280 |

> [!agenda]
> ### Lundi 8 décembre
> - 08:00–12:00 [Début de la pose][e1] · chez toi

> [!person] Sam
> - Rôle : artisan
> - Téléphone : 01 23 45 67 89
>
> Préfère être appelé le matin.

> [!warning] Bois en retard
> Le fournisseur annonce deux jours de retard.

> Le bois sera là mardi au plus tard.
> — Sam, 24/11

[e1]: calendar:ZXYtMQ
```
