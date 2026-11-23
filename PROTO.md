# Prototype: better blocks and new ones, agenda first (#730)

This branch (`proto/blocks`) is a prototype for #730, part of #700. **It is not meant to be merged.**
It changes the real espace code (catalog, components, CSS, gallery) so you can look at the result
in the browser. Once you have chosen what to keep, the implementation PR adds the grammar, the brain
prompt, `list_rows.go` and the docs.

## How to view it

```sh
cd core/web/espace && npm ci && npx vite --port 5317
```

- <http://localhost:5317/?blocks>: one page per new or changed block, with what it replaces and the
  JSON the brain writes for it (open « Ce qu'Eriphe écrit »).
- <http://localhost:5317/?open=node-day>: the full « Ma journée » page.
- <http://localhost:5317/?open=node-mails>: the « Mes mails » summary.
- <http://localhost:5317/?open=node-invest>: the « Investissements » page.
- <http://localhost:5317/?gallery>: the existing gallery, now with Agenda, Person and Quote.

All data is invented (Sam, Léa, Alex, example.org) and dated from today, so the agenda's now line
falls on the real time of day. The Oui buttons work against the dev server's fake core.
`sh proto-shots/shoot-all.sh` (dev server running) regenerates every screenshot, with the clock
frozen at 11:05.

## Screenshots

Under `core/web/espace/proto-shots/`:

| Page | Desktop light | Desktop dark | Phone light | Phone dark |
|---|---|---|---|---|
| Blocks gallery | `blocks-desktop-light.png` | `blocks-desktop-dark.png` | `blocks-phone-light.png` | |
| Ma journée | `day-desktop-light.png` | `day-desktop-dark.png` | `day-phone-light.png` | `day-phone-dark.png` |
| Mes mails | `mails-desktop-light.png` | `mails-desktop-dark.png` | `mails-phone-light.png` | `mails-phone-dark.png` |
| Investissements | `invest-desktop-light.png` | `invest-desktop-dark.png` | `invest-phone-light.png` | `invest-phone-dark.png` |

`before/` holds the audit shots from `main`: the old gallery (light, dark; in English) and « Mes
mails » (desktop, phone).

## What the audit found (main, before)

- **Day**: events filled with violet (the colour reserved for what waits) and drawn red and amber
  when they overlap, as if they were errors. Times in a monospace font, a « PROPOSÉ » ghost in
  capitals whose title was cut off, and no now line. The brain did not use Day, so « Ma journée »
  ended up as text rows like « Picnic 14:10 needs-action ».
- **Timeline / Progress**: the current step was violet, though nothing waited. Progress was a
  pill followed by a cryptic segment bar and a monospace « 2/5 ».
- **Item / Fold**: every row sat in its own box, and folds were dashed boxes. On a page this
  looked like a stack of cards.
- **Metric**: each figure sat in its own box, with no trend.
- **Chart**: the plot scaled with the page's width, so on a wide page its axis labels grew to
  about 36 px, in monospace.
- **Message / Chart axes**: dates and labels in monospace.
- **Phone**: a row's card text (« Envoyer la réponse à Sam · compte… ») pushed its Oui off the
  screen. The batch line cut its own label down to « 38… ».

## Blocks

### Agenda (new, replaces `Day`)

What it is: anything from a calendar, in one block. The first day is an hour column: today's has a
line at the current time, and past events are greyed. The next day is a compact column (all-day
items, then time and title). Days 3 to 7 are a strip of small columns; when the block is narrow
(phone, small page; this is a container query) the strip becomes a list of days.

- **One sign for « waits »**: a dashed violet ghost, used both for an invitation with no answer
  (status `needs-action`) and for a dated card Eriphe proposes (« proposé par Eriphe »). A ghost
  carries **its card's Oui**, through the existing `InlineYes`. For an invitation, the card is the
  one that waits on the event's item. The page learns it out of band (`useList`, as for a List's
  rows), never from the block's data. For a proposal, the card is the proposal's own.
- **Statuses are always words**: `peut-être` (outline), `annulé` / `refusé` (struck through), `à
  répondre` (ghost). Accepted spellings are confirmed, accepted, tentative, needs-action,
  needsAction, NEEDS-ACTION, declined, cancelled and canceled. Any other value shows nothing, never
  the raw string.
- An overlap is placed side by side, with no alarm colour. A ghost says in words what it overlaps
  (« chevauche « Déjeuner avec Alex » »).
- Placement uses only classes on a grid of half-hour rows, and the now line is placed to 10
  minutes; nothing is set through a style attribute. It works in light and dark, and the line
  moves every minute.

What the brain writes:

```eriphe:block agenda
{"components": [
  {"id":"root","component":"Agenda","days":7,"events":{"@path":"/agenda/events"}}
],
 "sources": {"agenda":{"tool":"calendar/list_events","args":{"days":7},"items":{"path":"events","id":"id","title":"title"}}}}
```

`date` (default: today), `days` 1–7 (default 1), `proposals` (default true). Event fields: `title`,
`start`, `end`, `place`, `allDay`, `status`.

### Figure row (Metric `trend`, Row of Metrics)

What it replaces: boxed Metrics. A Row of Metrics becomes one band with hairlines between the
figures (two per line on a phone). The new optional `trend` (a list of numbers, or a binding)
draws a small line under the figure, with a dot on the last value; the dot stays round however
much the line is stretched.

```json
{"id":"pea","component":"Metric","label":"PEA","value":48230,"unit":"€","delta":852,"deltaLabel":"cette semaine","trend":[46120,46380,"…",48230]}
```

### Person (new)

What it replaces: an Item or bold paragraphs used to say who waits for what. It shows a circle
with initials, the name, what the person is to the user, one sentence, and a few facts as lines
(phone, next meeting). Nothing in it opens a link or places a call.

```json
{"id":"sam","component":"Person","name":"Sam Martin","detail":"Artisan, la terrasse","note":"Attend ta réponse sur le devis depuis mardi.","facts":[{"label":"Téléphone","value":"06 00 00 00 00"}]}
```

### Quote (new)

What it replaces: a Text or Markdown that copied someone else's words. It shows the passage as
written, who wrote it, when, and the page it comes from, in clear. The `url` follows the same rule
as a Link's: `a2ui.IsLink` now covers `Quote.url`, with a Go test. Inside a block marked external,
the quote drops its own rule, because the block's amber edge is already that sign.

```json
{"id":"q","component":"Quote","text":"Tu peux me dire avant vendredi si ça te va ?","from":"Sam <sam@example.org>","date":"2026-11-22T18:12:00+01:00"}
```

### Timeline (revised): what happened

When every step has a date, the dates move into a left margin, showing the time alone for today's
steps (« Pendant la nuit »). The current step is now drawn in ink rather than violet. There is no
new property.

### Progress (revised): progress of a work

The pill is gone. The block shows the word, « 3 sur 5 », and the new optional `step` (the current
step's name), with a bar of the steps underneath. The tone colours only the word, and nothing is
violet.

```json
{"id":"article","component":"Progress","label":"Article « Le bureau en graphe »","value":3,"current":1,"total":5,"step":"Relecture par Léa"}
```

### Table: `change` columns

A new column kind `change` writes a signed percentage (« +2,6 % », « −0,3 % »), right-aligned,
green when up and red when down.

### Quieter existing pieces

- **Item**: no box; the list's hairlines separate the rows. A due date is plain text, not a pill.
- **Fold / batch**: a quiet line with the chevron first and a hairline above, instead of a dashed
  box. On a phone the batch's Oui moves under the label.
- **Row card**: on a narrow page the card's words go into the Oui's tooltip, so the Oui stays
  visible. A batch whose words repeat its Oui no longer says them twice.
- **Chart**: the plot is measured with `ResizeObserver` and its viewBox follows the real width, so
  its words and height keep their size. Axis labels now use Geist with tabular figures.
- **Message**: the date is in Geist with tabular figures, not monospace.

## Changed files

- Catalog: `core/a2ui/catalog_v1.json` (Agenda replaces Day; Person, Quote, Metric.trend,
  Progress.step, Table `change`), `core/a2ui/links.go` (+ test), `catalog_test.go`,
  `testdata/catalog_prompt.md`.
- Front: `a2ui/agenda.tsx` (was `day.tsx`), `a2ui/spark.tsx`, `a2ui/catalog.tsx`, `a2ui/chart.tsx`,
  `a2ui/inline.tsx`, `espace.css`.
- Words FR/EN: `core/web_text_espace.go` → `strings.json` (`espace.a2.agenda.*`,
  `espace.a2.progress.of`).
- Dev data: `data/sampledays.ts` (Ma journée, Investissements, gallery sections),
  `data/sampleviews.ts` (Mes mails: Quote, week figures), `data/samplecore.ts` (an Agenda's rows
  and dated cards), `doc/BlocksGallery.tsx` (`?blocks`), `a2ui/gallery.json`.
- Tests: `components.test.tsx` (agenda placement and now line, statuses in words, next days,
  ghosts with their Oui, Person, Quote, trend, step, change, dated timeline), `catalog.test.ts`.
  `npx tsc --noEmit` and `npx vitest run` (287 tests) pass, as do the Go `a2ui` tests,
  `TestEspaceStrings` and `TestEspaceGalleryIsValid`.

## For the implementation PR (not done here)

- `list_rows.go`: tell an Agenda's rows (its `events` bound to a source's `items`), with each
  event's card, as for a List. The dev core does this in `samplecore.ts` `block()`.
- Where a dated card shows: the prototype draws it in its own page's Agenda and leaves it out of
  the page foot (`samplecore.ts`). Core would need that rule (see question 3).
- Brain prompt (`brain-prompt.md`) and catalog instructions: « une journée : Agenda », and when to
  use Person, Quote and a figure row. Also `docs/node-format.md`, `docs/workspace.md`, and the core
  README's catalog list.
- The e2e specs that mention Day (if any) and the rebuilt bundle.

## Open questions for you

1. **Agenda's shape**: is « first day by the hour, next day as lines, the rest as a strip » right,
   or should `days: 7` give seven equal columns (a classic week view)? Should a non-today `date`
   also be drawn by the hour?
2. **Field mapping**: Agenda reads `title / start / end / place / allDay / status`. A calendar tool
   that returns `summary` or `start.dateTime` cannot be bound directly. Should the source's `items`
   gain a mapping (as it already has `title`, `detail`), or should the calendar MCP return this
   shape?
3. **A dated card in two places**: Eriphe's proposed slot shows as a ghost with its Oui in the
   Agenda. Should it also stay at the page foot (today's rule), or should the Agenda be its place,
   like a row is for an item's card? And a dated card that belongs to another node: should it show
   its Oui in « Ma journée » too, or only the ghost?
4. **Invitations from outside**: a calendar invitation is someone else's words. Should its ghost
   carry the small amber sign (as an external card does), or is the block's external edge enough?
5. **Person facts**: should a phone or e-mail stay plain text (as now, nothing opens), or become a
   `tel:`/`mailto:` link? Today Link refuses anything that is not http(s).
6. **Colour of changes**: green and red stay for good and bad news (Metric, Table `change`). Would
   you rather have ink and a sign only, keeping colour for states?
7. **Keep `Day`?** The prototype replaces it outright (one mechanism per need). Is anything outside
   the espace still writing `Day`?
8. **Other candidates not built**: a media gallery (Image already exists in a Grid), a map-free
   place card, and a checklist that is not buttons. Is any of them worth the next pass?
