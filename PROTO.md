# Prototype: « Eriphe coche, tu cliques »

This branch (`proto/eriphe-coche`) is a throwaway prototype. **It is not meant to be merged.** It
changes the real espace code (`core/web/espace`) and the dev server's make-believe core, so the idea
can be clicked in a browser. eriphe-core itself is unchanged. The only Go change is seven
FR/EN strings in `core/web_text_rows.go`.

## The question

Why is there a proposal card when a view could carry the yes itself?

**Today on `main`.** Note that the brief described an older state. #723, merged on 2026-11-22, removed
block `actions`, `picks.tsx`, the checkboxes and the action bar of #672. What `main` has now is one
way to say yes, and it is always a card:

- a card on each row it acts on (`InlineYes`, its sentence and Oui on every row);
- a batch (`Fold` with `list`) that carries a shared card and has its rows ticked, so the user unticks
  what stays.

Cards of the same kind fold together (`call_fold.go`), but never across accounts or other
arguments. So the request « supprime Backblaze, AWS, SNCF, les newsletters et les pubs, archive les
factures déjà payées » gives four new cards, one per action and account. It also leaves the triage
skill's older card apart. Each card's sentence and Oui repeat on every row it covers.

**The proposed model.** When Eriphe proposes an action on items of a view, no card is made. Eriphe
pre-ticks the rows, each with its action and a short reason (« pub », « échec CI remplacé »,
« échange passé »). The page's bar shows one button per action, with its count and accounts:
« Corbeille · 23 · 2 comptes », « Archiver · 5 · 2 comptes ». The user unticks what to keep and
clicks once per action. Behind the click, code runs one call per account. Cards stay only for what
is not a row of a list: sending a mail, accepting an invitation, starting a task with rights,
creating a view.

So this prototype brings back a tick-and-bar mechanism, close to what #723 removed. The difference
is that Eriphe does the ticking, with a reason on each row, and there is no card behind the ticks.

## How to view it

```sh
cd core/web/espace && npm ci && npx vite --port 5317
```

| URL | What |
|---|---|
| <http://localhost:5317/?open=node-mails> | « Mes mails » before the request: only the triage skill's 2 ticks (CI failures a later build replaced) |
| <http://localhost:5317/?open=node-mails&coche=a> | after the request (scenario A) |
| <http://localhost:5317/?open=node-mails&coche=ab> | after the request, then a new mail arrived (B) |
| <http://localhost:5317/?open=node-mails&avant&coche=a> | **before**: the same moment as `main` does it today, with cards |

A panel at the bottom left (« Eriphe coche, tu cliques ») plays the moments by hand:

- « Le dire » says the request (A).
- « B · Un mail arrive » brings in a new mail (B).
- « Voir avant (?avant) » toggles between the prototype and today's version.
- « Recommencer » starts again.

On a phone-sized window (under 560 px) the workspace shows its list layout, and the panel sits at the
top. The ⤢ button enlarges the page, which helps with a long list. All data is invented: Sam, Léa,
Alex, accounts « perso » and « travail », `example.org` senders. The sender names Backblaze, AWS and
SNCF are kept because the owner's request names them; their addresses are `*.example.org`.

`sh proto-shots/shoot-all.sh` regenerates every screenshot while the dev server is running (it uses
Playwright from the dev dependencies).

## Scenarios

### A. The request, ticked

The user said « supprime Backblaze, AWS, SNCF, les newsletters et les pubs, archive les factures
déjà payées ». Here is what happens, step by step:

1. **Eriphe's answer.** It is one line in the orb, in its ordinary « answer » mode: « C'est coché
   dans « Mes mails » : 23 à la corbeille, 5 factures payées à archiver. Décoche ce que tu gardes. »
   There is no wait mode and no « Voir » pointing at a card.
2. **The inbox list.** 28 rows across the two accounts are pre-ticked. Each one shows its action as a
   small word (« corbeille », or « archiver » with a dashed outline) and its reason (« newsletter »,
   « pub », « Backblaze », « AWS », « SNCF · échange passé », « facture payée », « remboursée »,
   « échec CI remplacé »).
   - Rows Eriphe left alone stay plain and aligned, with no box: « Votre facture de novembre »
     (not paid yet), Léa's photos, the CI success, the parcel, the tennis club, the office closure.
3. **The bar.** It sticks to the bottom of the page and shows « Corbeille · 23 · 2 comptes » and
   « Archiver · 5 · 2 comptes ». The tooltip shows the split, for example « Un appel par compte :
   perso : 15 · travail : 8 ».
4. **Unticking.** The user unticks two rows: a garden newsletter they read, and the Backblaze receipt
   of November. The bar now reads « 2 décoché(s), ils restent » and « Corbeille · 21 ».
5. **First click.** « Corbeille · 21 » runs. The rows leave and the bar says « ✓ 21 à la corbeille ».
   The two unticked rows stay unticked: they are kept by node outside the page's surfaces, so a
   re-read does not reset them.
6. **Second click.** « Archiver · 5 » runs and the bar says « ✓ 5 archivé(s) ».
7. **The bilan.** A moment later the view's skill rewrites its bilan with what is left.

The action word on a row is a button. Clicking it moves the row to the list's other action
(corbeille ↔ archiver). This is one possible answer to the question of two actions on one row (see
the open questions).

### B. A new mail arrives

While the page is open, a new advertisement arrives on the « perso » account. The triage skill
pre-ticks it (« corbeille · pub »). It comes in through the same path as eriphe-core's `source`
event: the page changes in place and is not read again.

- The new row is tinted for a few seconds.
- The bar goes from 23 to 24.
- Nothing else moves: no card, no words in the orb. The node keeps its halo (it already waits, for
  Sam's card).

In `?avant`, the same mail folds into the oldest untouched « perso » trash card, which becomes
« Mettre 16 mails à la corbeille · compte perso ». Its sentence repeats on 16 rows.

### C. What stays a card

« Répondre à Sam — brouillon prêt » is not a row action. It sends a mail with a drafted body, so it
stays a card, with its sentence and Oui on the row of « À traiter », exactly as on `main`. The same
holds for Alex's reply and for adding the dentist appointment to the calendar. These rows show the
contrast on one page: a card with a Oui on top, ticks and the bar below.

### D. Before and after

`?avant` plays the same moments with `main`'s mechanism: real `InlineYes` row cards and the orb's
« wait » mode with « Voir ». In the inbox:

- every pub and newsletter row of « perso » carries « Mettre 15 mails à la corbeille · compte
  perso » and a large black « Mettre 15 mails à la corbeille »;
- the « travail » rows carry their own card;
- the CI rows carry the skill's older card;
- the invoices carry two archive cards.

That makes five cards for one request plus one skill. The orb says « J'ai préparé 4 propositions…
Voir ». In the prototype the same moment is one line, ticks and two buttons.

## Screenshots

Under `core/web/espace/proto-shots/`, each in `desktop`/`phone` × `light`/`dark`
(`<name>-<size>-<theme>.png`):

| Name | What it shows |
|---|---|
| `a1-asked` | A: right after the request: the orb's line, the rows ticked, the bar (desktop: the page enlarged) |
| `a2-unticked` | A: two rows unticked (struck reason, dimmed), « Corbeille · 21 », « 2 décoché(s), ils restent » |
| `a3-trash` | A: after « Corbeille »: « ✓ 21 à la corbeille », only « Archiver · 5 » left |
| `a4-archive` | A: after « Archiver »: « ✓ 5 archivé(s) », the inbox's remaining rows |
| `a-page` | A: the whole page, top to bottom (C's cards at the top, then the inbox) |
| `b-arrived` | B: the new mail at the top, tinted, the bar at 24, nothing else changed |
| `avant` | D: today's version of the same moment (row cards, orb in wait mode) |
| `avant-page` | D: today's whole page |

On a phone the bar sits above the writing bar, with its buttons full width. Each reason goes under
its row's text, so it stays readable. Eriphe's line in the orb covers the bar until it is closed:
the phone shots after `a1` close it first. That overlap is open question 9.

## Changed files

**Front:**
- `a2ui/ticks.ts` (new): the user's unticks and action changes, by node. It also holds the runner,
  one call per account.
- `a2ui/lists.tsx`: `TickRows`, for a List some of whose rows carry a `tick`.
- `doc/TickBar.tsx` (new): the bar.
- `doc/Doc.tsx`: draws the bar and gives the Lists the page's node.
- `espace.css`: the `tk-*` rules, and `.ls-page .pg { overflow: clip }` so that the bar sticks on a
  phone.

**Types:** `data/api.ts`. `ListRow.tick = {action, reason, account}` is code's, like `card`, and is
never in the block's data. `SourceList.actions` names each action and says what it says once done.

**Dev core:**
- `data/samplemails.ts` (new): « Mes mails » with two accounts and the scenarios. `?avant` turns
  the same ticks into `main`'s cards, one per action and account.
- `data/samplecore.ts`: ticks on rows, `pushSources` (as a `source` event) and `sampleRun`.
- `data/sampleviews.ts`: the old « Mes mails » is replaced.
- `dev.tsx` and `dev-coche.tsx`: the panel and `?coche=`.

**Strings:** `espace.tick.*` in `core/web_text_rows.go` (FR and EN), with `strings.json` regenerated.

**Checks:** `npx tsc --noEmit` and `npx vitest run` (291 tests) pass. In `core/`, `go vet`,
`go test` and `gofmt` are clean. There are no new tests: this is a throwaway prototype.

## For an implementation (not done here)

- **Tool:** the brain gets a tool, `tick_items(node, list, [{item, action, reason}])`, or a field
  of its answer, instead of a calls card. Code checks that each action is one the view declares and
  that each item is one of the source's.
- **Where actions come from:** someone has to declare the view's actions (corbeille → which tool and
  arguments, by account), and that declaration has to be approved once. This is what #672's block
  `actions` were, and #723 removed them. The difference here is that Eriphe ticks, not the user.
  See question 1.
- **Server side:** `list_rows.go` tells `tick` on rows the way it tells `card`. A new route runs an
  action over the ticked items: one call per account, each recorded in the decisions.
- **What goes away or changes:** `call_fold.go` and batch cards are no longer needed for row
  actions. The `one_yes_test.go` guard needs a second entry point, or the click becomes a card
  decided at once.

## Open questions for the owner

1. **Who declares the actions?** Without cards, the yes is the bar's click on an action the view
   declares. Should that declaration be approved once, when the view is created (a card « Cette vue
   peut mettre à la corbeille et archiver »), or should any tool with a « write » policy be offered
   per click?
2. **Two actions on one row.** The prototype shows one action per row, and clicking the word cycles
   through the list's actions. Is that enough, or should a row be able to carry two actions (archive
   *and* label)? Should the user be able to change Eriphe's action at all, rather than just untick
   the row?
3. **A tick nobody clicks.** Does it stay forever? Does it expire (for example at the next run of the
   triage skill, which ticks again from scratch)? Does it fade after N days? In the prototype it
   stays until clicked or until the item leaves the source.
4. **Halo.** Should waiting ticks make the node's halo show, the same way a card does? Or do ticks
   stay quiet, and only cards (Sam's reply) make the node wait? In B the halo is already on because
   of Sam's card, so the prototype cannot tell the two apart.
5. **Ticking a row Eriphe didn't.** Can the user tick a plain row (the unpaid invoice), and with
   which action? The prototype gives plain rows no box.
6. **Confirming destructive actions.** #672 showed the exact call before a destructive one. Is
   « Corbeille » (recoverable) truly one click? Would « Supprimer définitivement » need a second
   step showing the calls?
7. **Sam's reply.** It concerns a row but is not a tick, so it keeps its card on the row as on `main`.
   Should a card that is about a row stay on that row, or move to the page's foot so that rows only
   ever carry ticks?
8. **Blocks marked external.** The mail block is someone else's words (its amber edge). The ticks and
   reasons are code's, and Eriphe writes them from those mails. Should a reason drawn from a mail's
   content (« facture payée ») carry the external sign?
9. **Phone.** The bar sits above the writing bar, and Eriphe's line in the orb covers it until it is
   closed or fades. Should the bar take the orb's place while ticks wait, or should the orb's line
   move above the bar?
10. **The result line.** « ✓ 21 à la corbeille » stays in the bar until the next action. Should it
    fade, offer « Annuler » (trash is recoverable), or move into the page's « Déjà décidé » menu as
    a settled card does today?
11. **Rows spread through the list.** The ticked rows stay in the inbox's order, mixed with the rows
    that stay. Would it be better to group them (« Coché par Eriphe » at the top), or to sort them
    by action?
