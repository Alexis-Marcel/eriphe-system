# Prototype: « belles vues »

This branch (`proto/belles-vues`) is a throwaway prototype. **It is not meant to be merged.** It adds
a dev page beside the espace app (`core/web/espace/proto.html`, `src/proto/`). It does not change the
app, eriphe-core or the catalog. The only change to existing code is `export` on a few sample blocks
in `data/sampleviews.ts` and `data/sampledays.ts`, so they can be reused verbatim as the « before »
side.

## The question

Can pages look cleaner and nicer while the LLM writes **less** JSON? The idea is that the renderer
supplies the beauty, not the brain writing more.

## How to view it

```sh
cd core/web/espace && npm ci && npx vite --config vite.proto.config.ts
```

The server runs on port 5318. Its config fakes eriphe-core's `/espace/media`, so photos are local
SVG placeholders and no outside host is contacted.

| URL | What |
|---|---|
| <http://localhost:5318/proto.html> | the shell: a bar to pick the page, variant, theme, width, motion and state; the page in an iframe; « JSON » shows both sides and their size |
| <http://localhost:5318/proto.html?s=mails&v=cote&w=phone&json=1> | before and after side by side on a phone, with the JSON |
| <http://localhost:5318/proto.html?s=tailles> | the size table, for all six pages |
| <http://localhost:5318/proto.html?frame=1&s=invest&v=apres&theme=dark> | one page alone (what the screenshots use) |

The URL parameters are:

| Parameter | Values |
|---|---|
| `s` | `mails`, `invest`, `cuisine`, `garde-robe`, `projet`, `premiere`, or `tailles` for the size table |
| `v` | `avant`, `habille`, `apres`, `cote` |
| `theme` | `light`, `dark` |
| `w` | `phone`, `desktop` |
| `motion` | `1`, or `0` to force reduced motion |
| `state` | `live`, `loading`, `empty`, `error` |
| `json` | open the JSON panel |
| `frame` | the page alone, without the shell |
| `shot` | hide the dock |
| `closed` | start on the node, not the page |

In a frame, the dock at the bottom has these buttons:

- « Fermer vers le nœud » / « Ouvrir depuis le nœud »: the page shrinks into its node and grows
  back out of it (a shared element).
- « Rejouer »: replays the opening.
- One button per page that plays a change: « Un mail arrive », « Le marché bouge », « Étape
  suivante », « Premiers articles ».

In « Nouveaux blocs », the Oui buttons and the gallery chips work.

These scripts need the dev server running:

- `node proto-shots/shoot.mjs` takes 61 screenshots, with reduced motion so the stills are
  deterministic.
- `node proto-shots/check.mjs` plays every motion with motion on and checks where each one ends.
  All 11 checks pass.

## The six pages

All data is invented: Sam, Léa, Alex, `example.org`.

| Page | What it is |
|---|---|
| Mes mails | 124 mails, 4 to handle, a batch of 102 to throw away (2 of them phishing), 18 for info. Today's `mailBlock`, quote and figures, verbatim. |
| Investissements | Today's `invest*` blocks, verbatim: figures with trends, a 30-day curve, allocation against target, the positions table, a quote. |
| Cuisine | The fridge as a Markdown list. « Possible ce soir », a gallery of `node_find` recipes. All recipes in a table, by rating. Two ask buttons. |
| Garde-robe | Tomorrow's outfit (4 photos), three figures, 18 pieces with photos (`node_find`). |
| Terrasse | A subject page: its bilan, « Ce qu'on a décidé », its steps, Sam, notes with `[[links]]`, ask buttons. |
| Mes lectures | A view just created whose source returns nothing yet: the first-time page. |

For the four pages that have no view today, I wrote the « before » JSON as the brain would write it
in today's catalog. Every « before » block passes `a2ui.ValidateSurface`; I checked this with a
throwaway Go test that is not committed. Every « before » page renders through the real `Doc`.

## The variants

- **`avant` (Aujourd'hui)**: today's JSON, drawn by today's renderer (`Doc`, the dev server's
  make-believe core). This is the baseline.
- **`habille` (Habillé, même JSON)**: today's JSON, unchanged, with only renderer polish
  (`.bv-polish` in `proto.css`): a lead paragraph, block titles in ink, figures in a framed band,
  roomier rows, galleries without card boxes, pill buttons, and blocks that come in with a stagger.
  This could ship with **zero prompt change**. It is modest, because the renderer cannot guess
  structure the JSON does not carry.
- **`apres` (Nouveaux blocs)**: the high-level blocks below, one component per block, with keyed
  props and defaults. Today's `nodeGrammar` already accepts a single component as `components` and
  completes it to `root`, so each new block is a single catalog component. Sources, `items` and
  `open` are written exactly as today.

### The blocks in `apres`

| Block | The LLM writes | The renderer does by itself |
|---|---|---|
| `Sections` | `{"component":"Sections","items":"mails"}` | Groups by `_group`. The group with something to do (`_state` act/look) opens first, one rich row per item: avatar, Eriphe's note as the title, sender · subject, time, the card's text and its Oui. A group whose items share one card becomes a folded batch with « Oui, pour tous ». Other groups fold, with their count. Shows 6 rows, then « n de plus ». Says « Rien à traiter » when nothing is open. Empty, loading and error states. Replaces Column + Text + List + Item ×3 + Fold ×2 and the filter paths. |
| `Figures` | `{"component":"Figures","items":[{label,value,unit,delta,deltaLabel,trend}]}` | A band with hairlines. The first block of a page is the hero (bigger numbers). Two per line on a phone, with the first one full width. The value rolls to a new value. Replaces Row + Metric ×n with their ids. |
| `Gallery` | `{"component":"Gallery","items":"pieces","by":"categorie","units":{"portee":"fois"}}` | Guesses the image field (`photo`, `image`, …, or any `/espace/media` URL) and the title. Filter chips with counts come from `by`. Shows 8 tiles, then « Voir les n autres ». Four or fewer items get large tiles. Replaces Grid + Card + Column + Image + Text ×2 + formatString. |
| `Table` (extended) | `{"component":"Table","rows":"recettes","units":{"note":"★","temps":"min"}}` | Infers the columns from the first row, using the key as the header. Numbers align right. `units` gives €, %, ±% (signed, in ink), ★ (a rating as 5 marks) or a word. On a narrow page, rows become cards; with three columns or fewer, a row stays a compact line. |
| `Chart` (extended) | `"series":{"PEA":[46120,…]},"end":"2026-11-24"` or `{"Actions":{"Aujourd'hui":64,"Cible":60}}` | Daily values up to `end`, so no `{x,y}` per point. The line draws itself in. Shows high and low values and the change over the period. |
| `Timeline` (extended) | the same `items` | Says its own progress: « 2 sur 6 · Verser l'acompte », with a segmented bar. `Progress` is no longer needed next to it. |
| `Asks` | `{"component":"Asks","items":["Fais la liste de courses","Propose le menu"]}` | Shows the buttons. The first one is the default. Stacked full width on a phone. Replaces Row + Button ×n, each with `action.event.name/context.text`. |
| `Quote`, `Person` | as today, without `id` | A calmer look. |

Page-level rules come from the order and kind of blocks; the LLM never writes them:

- The first paragraph is the lead.
- « **Pour info** · » is quieter.
- `## X` is a section head.
- A Markdown list of 4 or more short items becomes chips (the fridge).
- Two small blocks in a row (Chart, Quote, Person, Timeline) sit side by side when the page is at
  least 620 px wide.
- The layout follows the page's width (container queries), not the window's. A page in the graph is
  narrower than the screen.

### Automatic states (`?state=`)

The LLM writes none of these:

- **Loading**: a skeleton shaped like each block (rows, figure band, tiles).
- **Empty**: a calm box, « Rien ici pour l'instant · Lu à l'instant · relu toutes les heures ». It
  uses the source's `every`.
- **Error with a last value**: the last value stays, with one quiet line: « Lecture échouée il y a
  3 min : voici la dernière. »
- **Error with no value**: « Je n'arrive pas à lire « lectures » pour l'instant », and that a retry
  happens by itself.

Today, an empty view shows whatever `emptyText` the brain remembered to write. A Fold without `list`
stays even when it is empty: see `premiere-avant-*`, where « Les articles pour info » shows over
nothing.

### Motion

Every motion is shorter than 250 ms, none blocks, and all go to their end state at once under
`prefers-reduced-motion` (Motion's `reducedMotion="user"`, or `?motion=0`).

- **Open from the node**: a shared element (`layoutId`). The node pill grows into the page and back,
  over 240 ms. The real graph already springs pages open, so this is the delta to keep: content
  fades in after the frame lands.
- **Blocks in a soft stagger**: 160 ms each. The step is capped so the last block starts by 90 ms.
  Rows inside a list are never staggered.
- **A row handled**: its Oui turns into a drawn check, the row dims, then its height folds away
  (200 ms) and the rows below slide up.
- **A new row**: grows in at its place.
- **A batch answered**: the whole fold leaves.
- **A count or figure that changes**: it rolls to the new value (240 ms) with a faint flash.
- **A timeline step**: the dot fills and the bar segment changes.
- **Gallery filter**: tiles re-flow with `layout`, and `popLayout` for exits.

## Size: what the LLM writes

Counted as the blocks are written in the node's file: fences included, compact JSON, exact
characters. The page's text is the same on both sides. Tokens ≈ characters / 3.5, which is an
estimate.

| Page | Today | Prototype | Change | Components |
|---|---|---|---|---|
| Mes mails | 2 294 car. (≈ 655 tk) | 931 (≈ 266) | **−59 %** | 17 → 3 |
| Investissements | 3 789 (≈ 1 083) | 2 310 (≈ 660) | **−39 %** | 15 → 5 |
| Cuisine | 1 819 (≈ 520) | 754 (≈ 215) | **−59 %** | 14 → 3 |
| Garde-robe | 2 612 (≈ 746) | 919 (≈ 263) | **−65 %** | 31 → 3 |
| Terrasse | 1 454 (≈ 415) | 958 (≈ 274) | **−34 %** | 7 → 3 |
| Mes lectures | 1 027 (≈ 293) | 300 (≈ 86) | **−71 %** | 7 → 1 |
| **Total** | **12 995** | **6 172** | **−53 %** | **91 → 18** |

For the whole node body, text included: 14 423 → 7 600 characters (−47 %).

Investissements gains least because its literal data, the 30-day trends, stays. Terrasse gains
least among the rest because its timeline items are data too. What disappears is the structure:
ids, `children`, Column + Text titles, Row + Button + `action.event.context`, Card / Column / Image
templates, `@path` bindings and filter paths.

**The grammar it costs.** Today `catalog_read` returns 20 477 runes: 15 661 for the components and
4 815 for `nodeGrammar`. The draft text for the new blocks (`src/proto/grammar.fr.md`) is 1 527
runes. It would let these parts go, or shrink to a line:

- `Progress`: 657 runes.
- Most of the « Éléments / Sections » grammar, which explains filter paths, `Fold` `list` and
  batches: about 1 170 runes.
- The 600-rune mails example.

The estimate is roughly **−900 runes net**. Even keeping everything, +1 527 runes paid once per
`catalog_read` is repaid by the first page written, which saves 300 to 1 700 characters, and again
at every rewrite of that node.

## Recommendation

**Keep:**

1. **`Sections`**. It is the biggest win, in characters, clumsiness and look at once. Code already
   writes `_group`, `_state` and `_card`, so the renderer can lay out « À traiter / lot / pour info »
   without the brain describing it. It also makes the brain-prompt paragraph on sections and the
   grammar's filter syntax mostly unnecessary.
2. **`Figures`, `Asks`, `Gallery`**. Each one is a common shape the brain now spells out as 5 to 30
   components.
3. **`Table` with `units` and inferred columns, `rows` as a source name.** The `★` and `±%` units
   carry most of the « nice » for little grammar.
4. **Automatic empty, loading and error states**, and the « Rien à traiter » line. Drop `emptyText`
   from the grammar.
5. **Page rules from order and kind**: lead paragraph, quiet « Pour info », chips for short lists,
   pairs of small blocks, container queries. These are pure renderer work and apply to today's JSON
   too.
6. **Motion**: the row that leaves, the rolling count and figure, the short block stagger. They are
   calm and cheap. Open-from-node only as the content fade after the existing spring.
7. **`habille` now, separately.** The polish CSS needs no prompt change and can ship first.

**Drop, or not worth it:**

- Pairing blocks side by side by kind. It only helps on wide pages and can surprise; at least keep
  it to the four kinds here.
- The chart's high/low labels: today's axis grid reads better. Keep the curve-draw motion and the
  period change in the head.
- Inferring an image field from any `/espace/media` URL: keep only the named keys plus a one-key
  override.
- Colour on signed changes (I tried green/red). It is back to ink, as the house rule says.

## Open questions for the owner

1. **Replace or add?** Should `Sections`, `Figures`, `Gallery` and `Asks` replace their low-level
   spellings in the brain's grammar, so List / Item / Fold / Grid / Card / Button are kept only for
   rare cases and drop out of the prompt's examples? Or should they sit beside them?
2. **Who decides the sections' order and folding?** The prototype uses `_state`: act/look open,
   a shared card folds into a batch, the rest folds. Is that rule right for every view, or does a
   view sometimes need to say « this group stays open »? If so, add one optional key (`open: [...]`).
3. **Row content**: the prototype shows Eriphe's note as the title, then sender · subject. Is that
   the reading order you want for every source, or only for mails?
4. **`units` vocabulary**: are `€ % ±% ★` plus free words enough? Dates and durations are the obvious
   next ones.
5. **Chart `series` shorthand** (`{name: [numbers]}` with `end`): fine to add, or should charts
   always bind to a source and never carry literal points?
6. **Chips for short Markdown lists**: is a renderer rewriting the brain's Markdown acceptable, or
   should it be an explicit block?
7. **The `habille` CSS**: ship it on its own first, before any grammar change?

## Files

- `core/web/espace/proto.html`, `vite.proto.config.ts`: the dev page, port 5318, and the fake
  `/espace/media`.
- `core/web/espace/src/proto/scenarios.ts`: the six pages, before and after.
- `src/proto/blocks.tsx`, `After.tsx`, `proto.css`: the new blocks, the page rules, the states,
  the motion, the `habille` polish.
- `src/proto/Before.tsx`: today's page through the real `Doc`.
- `src/proto/size.ts`, `main.tsx`: counting, and the shell.
- `src/proto/grammar.fr.md`: the draft grammar for the new blocks, as `catalog_read` would give it.
- `proto-shots/`: 61 PNGs, named `<page>-<avant|apres|habille>-<phone|desktop>-<light|dark>`, plus
  `*-state-*`, `tailles.png` and `shell-mails-cote.png`; `shoot.mjs`, `check.mjs`.
