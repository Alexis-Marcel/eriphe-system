# The node file format

Eriphe's workspace is a graph of **nodes**. A node is one piece of work and its result: a subject, a
research, an incident, a reply to write, a page kept up by a skill like the mails. Each node is one
Markdown file with YAML front matter; its body is a **Markdown page** that holds everything the page
shows. This page describes that file, for the brain, for tasks, and for anyone who reads or reuses
the files.

The Go package [`core/nodefile`](../core/nodefile) reads and writes this format; everything else in
Eriphe goes through it. The brain and every task with a node read how to write a page from the
`page_format` tool ([`core/page-prompt.md`](../core/page-prompt.md), in French), whose examples are
pages the check below finds nothing in.

## A full example

````markdown
---
format: 4
id: k3v9q2m7
title: Mes mails
goal: Tableau de bord des mails à traiter, tenu par le skill mails ; on n'y écrit pas de réponses.
created: 2026-11-17T09:00:00+01:00
---
Écrit le 26/11/2026 à 9 h.

Sam attend ta réponse avant vendredi.

## À traiter
- [ ] [Sam · Devis terrasse][m1] : il attend ta réponse avant vendredi 📅 2026-11-27
  - Brouillon prêt : « Le devis me va, on commence la semaine du 8. »
  - [Envoyer la réponse](card:calls:12)

## À jeter
[Jeter ces mails](do:mail/trash)
- [Lettre d'info][m2] : jamais ouverte

[Montre la semaine](ask:)

[m1]: mail:aWQtMQ
[m2]: mail:aWQtMg
````

## The file

- **Name:** `<slug>-<id>.md`, flat in the graph's repository. The name is frozen when the node is
  created: renaming a node changes its title, not its file.
- **Its trace** (what happened in it: a task's progress, the calls made from it, the user's
  decisions) is not in the file nor in the repository: a plain file, `<id>.trace.md`, in the folder
  beside it (`nodes-trace/`), each entry appended at its end (`## <time> · <author>`, then its text).
  Past 2 MiB it becomes its older part, `<id>.trace.1.md` (the one before dropped), and a new file
  begins. The night's backup holds both (`nodes-<day>.trace.tar`).
- **Encoding:** UTF-8. The file starts with its front matter, between two `---` lines; the body
  follows.
- **Round trip:** reading a file and writing it again gives the same bytes. When code changes one
  field or one block, only that part is written again, in its canonical form; the rest of the file
  keeps its bytes, comments and layout included.

## Front matter

| Field | Type | Meaning |
|---|---|---|
| `format` | whole number | Written by code only: the format the file is written in ([Format version](#format-version)). Absent: 1, and never written while it is 1. |
| `id` | text | The node's id: lowercase letters and digits, 32 at most, no dash. Required. |
| `title` | text | Its title for the user: one line, 120 characters at most. Required. |
| `parent` | id | The node it belongs to; none for a node at the top. Code writes it: at creation, and when Eriphe moves the node (`node_set parent`), never under itself or a node below it. |
| `links` | list of ids | Written by code only: the nodes it links to; no duplicate, never itself. An author's write keeps them. An author links nodes in the text instead ([Links between nodes](#links-between-nodes)). |
| `goal` | text | What the node is for, and what is written in it, in one line (« tableau de bord des mails à traiter, tenu par le skill mails ; on n'y écrit pas de réponses »). Every node Eriphe opens has one (`node_open` requires it); a file without one still reads. Not shown on its page; `node_find` and Eriphe's list of nodes give it. Whether a skill maintains the node is never in its file: code computes it from the skills that name it as their home. |
| `created` | timestamp | When it was created (RFC 3339). Required. |
| `done` | timestamp | Written by code (Eriphe's `node_set` `done`, the user's gestures): when it was marked done; absent while not done. An author's write keeps it. |
| `archived` | timestamp | When the user archived it; absent while not archived. |
| `external` | `true` | Written by code only: the node holds someone else's words. Absent otherwise. |
| `title_trusted` | `true` | Written by code only, on an external node: its title is not someone else's words (written before the node was external, or by a writer that vouched for it: the user approving it on a card, an author that read nothing external). Absent otherwise: the title of an external node is then shown marked, in amber with a ⚠ (« Titre écrit après une lecture extérieure »), never replaced; code never acts on a title. |
| `opened_by` | a mapping | Written by code only, when it makes the node: who wanted it. `by` is `user` (a message of theirs, `msg` its number; or their yes on a card, `card`), `skill` (a skill's run: `skill` its name, `trigger` what started it: `monitor:<name>`, `alert:<hook>` or `schedule`) `event` (a turn of Eriphe's on events only: a wake, an intention, a monitor's event) or `eriphe` (one of Eriphe's own places, made by code: « Outils à revoir »). A node a task or a step opened says who wanted that work, with the `task` and the `node` it was opened for. Absent on a node made before it was recorded: its origin is unknown. The workspace says it in the page's ⋯ menu, and marks a node the user did not ask for (`skill`, `event`) with « ↯ ». |

Any other field is a **property** of the node, as in Obsidian, kept with its value: a text, a
number, a date (`2026-10-01`, quoted or not, with a time or not), a `"[[link]]"`, or a list of
them. `tags` is the list of its tags, nested with `/` (`cuisine/recette`), a leading `#` left out. A
node is classed by its tags and properties only: there is no `type`, and nothing in code knows what
a property means. Eriphe's `node_set` writes them (`props`: a value, or `null` to remove one); its
name is matched without case, never a field of the table nor one starting with `_`. The index
(`node_props`) keeps each value typed, so that `node_find` compares a number as a number and a date
as a date, and a tag matches its parents (`cuisine` finds `cuisine/recette`). A mapping, or an
empty field, is kept but not indexed. The workspace shows them under the node's title.

An author may also write them as Obsidian does, in a front matter of the page's own at its top (a
`---` line, a YAML mapping, a `---` line, after blank lines at most), in `node_open`, `node_write`,
`node_edit` or `node_append`: its fields are set as `node_set` sets them (a field written wins
over the property's value, a field without a value is skipped) and the block leaves the page, so
that the file keeps one front matter. All of the block or nothing: a block one of whose fields is
not a property (a field of the table above, a name starting with `_`, a mapping as a value, a tag
that is not one) stays in the page as text, nothing lost, and the check says so (`front-block`).
A block the page already held before the write is not written by it: it stays as it is. A `---`
rule followed by text that is not a YAML mapping is the page's own.

Any other field is kept with its value. While the front matter does not change, it keeps its bytes,
comments included. When code writes it again (a new title, say), the known fields come first in the
order of the table, the empty ones left out, then the other fields in their order; YAML comments are
not kept. A field given twice, or YAML that does not parse, is an error.

The node's status (`running`, `waits`, `done`, `idle`) is in no file nor index: eriphe-core
computes it from its cards, its tasks and steps, and this file's `done`.

## Format version

A node is the user's note: it outlives every change of Eriphe. No code wipes a node (one is only
archived, by the user), and a change of this format converts the files instead of leaving them
unread.

- **Where:** each file says its own format, in its front matter's `format`. The format is 4 today;
  a file that does not say it is in format 1. It is in the file rather than once for the
  repository because the file is the unit: a file
  copied in from elsewhere, and each old version in the history, says the format it was written
  in, so it is read as it was meant (a version read from the history is converted in memory).
  Whatever else changes, the front matter stays YAML between two `---` lines, with `format` in it.
- **A change of the format** comes with its conversion, from the format before it (a function of
  the file's bytes, in [`core/nodefile`](../core/nodefile)), and a test that reads a file of the old
  format. The new format is the one after the last conversion; files are written in it, `format`
  then in their front matter. A conversion keeps every byte it does not change: `format: N` is
  written as the front matter's first line (or its `format` line replaced), in the file's own line
  ends, nothing else rewritten.
- **The conversions so far:** 1 → 2 drops `lifetime` (`work` or `standing`): there are only
  nodes, and a node a skill maintains is told by the skill (its `node:`), never by its file.
- 2 → 3 writes each block of a page (a fence ```` ```eriphe:block ````, A2UI components with their
  data and the sources code read live) as the page's own Markdown, at its place, the text around
  it kept: lists, items with their detail folded under them, callouts (`figures`, `steps`, `chart`,
  `agenda`, `person`, `warning`, `info`, `success`), tables, quotes, code, `ask:` links (a button
  asks what it sent), links as written, and `card:` for a card block. A block that read sources
  becomes a snapshot under « Écrit le … » (the time of the last read): the last values code kept
  (`node_reads`) and the notes of its items (`node_items`), an item whose server and id are known
  as a reference (`[m1]: mail:<id>`), its note the line's comment, its group a heading, its state
  the box; a figure bound to a monitor's value becomes an `on:monitor.changed/<name>` line. Words
  from the data, the reads and the notes are escaped so that none reads as a link, a heading or a
  cell; a block that may show someone else's words makes the node `external` (its title still
  trusted). Code has no tool: no `do:` line is written. What cannot be written (an input field, a
  gallery, a computed value) stays whole in a ```` ```json ```` fence, readable. A version of the
  history is converted without the values (they are core.db's, kept for the last version only).
  Blocks, the sources code read live for them and their items' notes are gone with this format:
  once every node converted (no file left broken), the tables that kept the values and the notes
  are dropped from core.db.
- 3 → 4 folds a front matter an author wrote at the top of a page, kept there as text until format
  4, into the node's properties, as a write does now ([Front matter](#front-matter)); a property
  the front matter has already keeps its value (`node_set` set it after the page was written). The
  front matter is then written again in its canonical form. A page without such a block, or with
  one that does not fold, keeps its bytes.
- **At start**, a file of an older format is converted to today's, through each conversion in
  turn: all of them in one commit by `convert`, each node a version of its own, the old one kept in
  the history. A file changed by hand meanwhile is first committed as it is (`outside`). A file
  whose conversion fails, or gives a file that does not read as a node (one broken by hand), is
  reported and left alone like any file that does not read, its node's last good version still
  readable; it is converted at a later start, once mended.
- **A file of a newer format** than this Eriphe knows stops the graph at start, with an error that
  names it; nothing is written, the file is left as it is. Update Eriphe.
- **The index** (`nodes.db`) holds nothing the files do not: it may be rebuilt from them at any
  time, and is never converted: one of an older schema is made again from the files at start.

## Links between nodes

A node's text links other nodes by their title, as in Obsidian: `[[Title]]`, or `[[Title|shown
words]]` to show other words (`\|` works like `|`, which a table row needs). Inside a code span or
a fenced block it is only text, and `\[[` is only brackets; a block's words hold no link. The page
draws each one as a link that opens that node beside it; a name no node has is greyed, an archived
node shows as archived and opens nothing. Whatever words it shows, its tooltip and accessible name
say the real target's title (or that it is someone else's words, for an external one).

A name matches a title whatever its case, accents, typographic apostrophes or runs of spaces; a
node not archived comes first, then the oldest. The index resolves a name once and keeps the node
it found: the link holds when that node's title changes, and a node that takes the old title later
does not take it. A name no node has yet links as soon as a node gets that title. Documents are
never rewritten on a rename (that would make versions of other nodes, stale every `expect` on them,
and could not touch archived ones). The limit: the index is the only place that keeps a resolution,
so an index rebuilt from the files resolves the names again by the titles of the day, and a link
written with a title since changed then waits.

A node's links, the graph's edges, are its text's links (resolved) and its front matter's `links`
(code's), together.

## The page

A node's body is a **Markdown page**: everything the page shows is written in the file, so that a
reader without Eriphe (Obsidian, another agent, the search) finds it all. Eriphe draws it better,
never otherwise, and reads nothing from elsewhere to draw it: when something changes, whoever keeps
the page writes its line again, or a line follows the event journal
([Lines that follow events](#lines-that-follow-events)).

[`core/nodefile`](../core/nodefile) reads a page (`ReadPage`, `md.go`) into a tree, checks it
(`Check`, `check.go`) and changes one line of it (`lines.go`). Go's reading is the reference; the
workspace reads the same pages the same way, both held to the fixture pages of
[`core/nodefile/testdata/pages`](../core/nodefile/testdata/pages) (each page with its tree and its
problems as JSON). Lines are counted from 1 in the body, what follows the front matter.

**What a page holds.** Plain Markdown, with Obsidian's conventions where they exist:

- **Headings** `#` to `######`, paragraphs, tables, `---`, fenced code (`` ```diff `` for a diff),
  `[[Title]]` links and `![[Title]]` alone on a line to embed another node (Obsidian embeds it;
  Eriphe draws it as a link to it).
- **Lists.** An item is one line; the lines indented under it (spaces or tabs, a tab counting four)
  are its **detail**, read as blocks of their own. A task is `- [ ]` (open), `- [x]` (done), `- [/]`
  (in progress) or `- [-]` (dropped). `📅 2026-11-27` in an item's line is its date.
  The words before « : » on an item's line are what it is (a short title), the words after it a
  comment by whoever keeps the page; another voice names itself (« Tâche « Trier » : … »).
- **When it was written.** A first line of text that starts with « Écrit le », « Mis à jour le »
  (or « Written on », « Updated on ») says when the page was written; it is not one of the blocks.
  A page that follows something starts with it. No `#` heading repeats the node's title: the page
  shows it above the body, and one at the top of an author's body is dropped when it is written.
- **Callouts.** A quote whose first line is `> [!type|option|option]- Title`: `-` after the
  brackets folds it, `+` shows it open. The types Eriphe draws are `figures`, `chart`, `steps`,
  `agenda`, `person`, `warning`, `info`, `success` and `note`. Any type takes `aside` (the page's
  side column) and `glance <threshold>` ([A figure at a glance](#a-figure-at-a-glance)); `external`
  is code's ([Lines that follow events](#lines-that-follow-events)); `chart` takes `line`, `bar` or
  `stack` (line when none), and holds a table whose
  first column is the x and each other column a series, its cells numbers (`18 385`, `1,8`, `−2`),
  the unit in the header (`Dépenses (€)`). A plain quote whose last line is `— Sam, 24/11` says who
  said it and when.
- **References.** A thing that lives elsewhere is a reference link, `[words][m1]`, with its target
  on a line of its own, at the bottom: `[m1]: mail:<id>`. The target's scheme is a catalog server's
  name (or `https:` for a page), its id everything after the first colon, as the tool gave it: never
  made up. A label is unique in the page, matched without case. The link's words are the author's;
  what Eriphe shows of the thing comes from the thing ([Cards on referenced items](#cards-on-referenced-items)).
- **Eriphe's links**, valid Markdown links whose scheme is one of four:
  - `[Jeter ces mails](do:mail/trash)`, on its own line, the first under a heading: an action on
    the references of that heading's section, down to the next heading of its level or a higher
    one. Its author must have the tool. Anywhere else it acts on nothing
    ([Actions on a heading's items](#actions-on-a-headings-items)).
  - `[Envoyer la réponse](card:<ref>)` places a card already proposed on that line: one of the
    node's own, or one the line's author proposed, which then waits in this node only (the node
    where its subject is discussed, say). Never one someone else proposed in another node (drawn
    as its words). Alone on its line, the whole card; in a sentence, its Oui. Not in a folded callout or a chart, which no reader sees as written. A card
    no line places waits at the foot of the page; code writes nothing in
    the page for it.
  - `[Montre la semaine](ask:)` is a probable reply: on a line of its own at the end of the page
    (or of an item), several separated by ` · `, the first the default. A click sends its words, as
    the stored page writes them, to Eriphe as the user's message in the node (marked as not the
    user's own words when the node is external).
  - `[18 385 €](on:monitor.changed/pea)` is a value an event of the journal rewrites
    ([Lines that follow events](#lines-that-follow-events)).

**The check.** After each write of an author (`node_open`, `node_write`, `node_edit`,
`node_append`), code reads the page and the write's result lists what is wrong in it
(`problems`: a line, a code and words), the write kept all the same:

| Code | What |
|---|---|
| `ref-undefined` | a reference whose label has no target |
| `ref-unused` | a target no reference uses |
| `ref-duplicate` | a label defined twice |
| `callout-type`, `callout-option` | a callout type, or an option, Eriphe does not draw |
| `date-impossible`, `date-malformed` | `📅` followed by a day that does not exist, or by no `YYYY-MM-DD` |
| `do-place` | a `do:` link not alone on the first line under a heading |
| `do-target`, `do-tool` | a `do:` link that names no `server/tool`, or a tool its author does not have |
| `link-target` | a `card:` or `on:` link that names nothing |
| `on-target` | an `on:` link that names no event (`on:<kind>/<key>#<field>`) |
| `chart` | a chart without a table, or a cell of a series that is not a number |
| `fence-open` | a code fence never closed |
| `front-block` | a front matter at the top of the page that stayed text: one of its fields is not a property ([Front matter](#front-matter)) |

**In the workspace.** The page draws a node's body from its Markdown (`core/web/espace/src/page`):
a task is a row whose box the user ticks (one line rewritten by eriphe-core, a version of theirs),
an item's detail folds under it, a callout is drawn by the workspace's component of its type
(figures, a chart, steps, an agenda, a person, an alert), a reference shows its words with its
target (with a warning when its words do not say the item: below), and a line of `ask:` links is a
row of replies. A day without its year takes the year of
the page's « Écrit le » line, else it is shown as written: never a guessed year.

**Changing a line.** A page has several authors (a skill, code, the user's gestures), so an author
changes a line, never the whole page. `node_edit` replaces a line, or a piece of text, found once
(`old`, `new`), or moves an item with its detail (`move`, `after`: after a heading it becomes the
section's first item, under its `do:` line; after another item, it goes after that item's detail,
at its indent and in its callout). A line edit needs no version: it is made on the last one, and is
refused once its line is gone (the author reads the page again). A whole body (`node_write`) names
the version it was made on (`expect`). Each item's line is unique in the page (its reference or its
words), so that an edit finds it; adding or removing a reference adds or removes its target too.
A ticked line stays, struck through, until whoever keeps the page removes it. No sentence repeats
what the page shows, and a figure says its unit.

### Cards on referenced items

A reference whose target's scheme is a catalog server's name names an **item** of that server
(`[m1]: mail:<id>`; core/page_items.go). A call kept for the user's yes whose server is the
reference's, and whose input names the reference's id (as a value, or in a list of them), acts on
that item: its card waits on the line that references it, on the item's row the reader sees (the
outermost item holding the line: a reference in a detail puts the card on its row; in a callout,
only an open one drawing its lists as rows, a note or an alert). A reference to
another server's id is not the item, whatever its id. Calls that do the same thing to other items of
the node fold into one card, shown on each of their rows. A reference in a paragraph, a table, a folded
callout or one drawn its own way (figures, steps, agenda, person, chart) carries no card: it waits
at the page's foot.

The card's words come from the item as its server reads it, never from the link's words: when the
call is kept, code reads each item with the tool the server declares (below), a free read only, and
keeps its title and sender with the call. An item it cannot read freely is counted, never named.
Where a reference's words say nothing of the item read (none of their words of three letters or
more is in its title or sender), the page shows them with a warning and the item as read beside
them: a line « Pub Maison Lune » pointing at a mail of Sam's shows « Les clés pour samedi » · Sam.
A reference whose item could not be read (no free read, a failure) says « non vérifié »: its words
are not checked.

The page's JSON (`GET /api/nodes/{id}`) carries this beside its body: `items` (each reference of an
item where it is written: its line, label, words, row, id, whether it opens, the card that waits on
it and the item as read, `misleading`, `unread`) and `placed` (the cards its lines carry), Go's placement,
which the workspace draws where it says.

### Actions on a heading's items

A `do:` link, alone on the first line under a heading, is an action the page places; code composes
it (core/page_acts.go):

```markdown
## À jeter
[Jeter ces mails](do:mail/trash)

- [Pub Maison Lune][m1] : publicité
- [x] [Lettre d'info][m2]
```

- **What it acts on**: the references under the heading, down to the next heading of its level or
  a higher one, to items of the tool's server, each id once, on a row the reader sees (not in a
  paragraph, a table, a folded callout or one drawn its own way); not those whose item is ticked
  (`[x]`, or dropped `[-]`), nor those a call of the same tool already names in the node (waiting
  on its card, or said yes to in the last seven days and not failed).
- **Its words are code's**: what the tool does to how many (the card's sentence and verb), each item
  as its server reads it (a free read, as for a card on an item), with the same warning where a
  reference's words do not say it. The link's words are never shown on its card.
- **Its calls come from the tool's input schema**: the argument that takes the items' ids (the one
  argument the schema requires, or, with none required, the one named for ids: `id`, `ids`,
  `…_id`, `…_ids`). A list of strings takes all the items in one call; a string takes one, in a call
  for each item. A tool that requires anything else has no `do:`: code fills in nothing else.
- **Its author must have the tool**: whoever wrote its line, by the node's versions (the oldest of
  the last versions that all hold it under the same heading: a copy elsewhere is its copier's). The user has every tool; the brain, those of the catalog; a
  task, those its grant lends; a skill, those its last run was lent; code, none. A tick of the user
  since does not change who wrote it.
- **The access grid** applies to every call: a tool it never allows (or one that must name its
  account) makes the action act on nothing, and a call refused at its run fails as any card's.
- **Nothing is kept before the yes.** The page is its state: the workspace draws its line from the
  page as stored (`POST /api/nodes/{id}/do` with the line and its text: the items, read, and what
  Oui says for 1, 2… of them), each row it acts on with a box, ticked, and the user unticks what
  stays. Oui (`POST /api/nodes/{id}/do/yes` with the ids ticked) reads the page again: only the
  items ticked that are still under the heading run, never one added since it was drawn. The calls
  are then kept as one calls card on the node, said yes to at once by that yes: its run, its
  outcome, its record (« Déjà décidé ») and the node's skill woken to write the page again are a
  card's. A do: that acts on nothing is drawn as its words, why in its tooltip.

### Lines that follow events

A value that moves is a line of the page that follows the event journal, rewritten by code at each
event, without a model:

```markdown
> [!figures|glance 3%]
> - PEA : [18 385 €](on:monitor.changed/pea) · relu le 26/11 à 10 h 05
```

- **The target** is `on:<kind>/<key>#<field>`: the event's kind (`monitor.changed`, `task.ended`…),
  its key (a monitor's name; none follows the events of any key), and where the value is in the
  event's payload, fields joined by dots (`value` when there is no `#`; `#value.total` for a field
  of a monitor's value). A key holds no space nor `)`, as a Markdown link's target does not. A
  monitor is one source of events among others; its first look publishes nothing: the first value
  is the one the author wrote, copied from the tool.
- **The value.** A number replaces the number of the label, written as the author wrote it (its
  decimals, its separators, `−` or `+`), what surrounds it kept (the unit): `18 385 €` becomes
  `18 412 €`. A text replaces the whole label: one line, 80 characters at most, without brackets,
  parentheses, backticks, `<`, `>`, `|` or `\` (nothing in it reads as a link, code or HTML). A list
  or an object is not a line's value: a skill woken by the same event writes those.
- **The date** after the link, ` · relu le 26/11 à 10 h 05` (` · read on Nov 26 at 10:05` in
  English), is written again, added when there is none.
- **No version per value.** The file is written; the value enters the history with the node's next
  version, whoever makes it, or with one version a day, author `readings`, when nobody writes. An
  author's write made on the version read keeps the values code wrote since, unless it changed one
  itself.
- **Someone else's events.** An event written by someone else (a monitor of a server not trusted)
  marks the callout that holds its line `external`: `> [!figures|external]`. The option is code's:
  an author's write that takes it off while the line stays in the page, or marks a callout code did
  not, is refused; no event takes it off (words around a number may still be someone else's). A
  node with a callout so marked taints whoever reads it, as an external node does. Outside a
  callout, an event written by someone else only writes numbers.

### A figure at a glance

A page may show one of its figures at a glance: the node's bubble becomes a small tile when the
figure moved past a threshold, until the user opens the node. The figure is the first number
written by an `on:` line in the callout marked `glance <threshold>`, else, when the node's property
`glance` says the threshold (`glance: 3%`), in the whole page; its label is the words before it on
its line (`PEA`), or the node's title.

- `glance 3%`: the variation in percent of the reference; `glance 500`: in the figure's own units.
- `previous` after it (`glance 3% previous`) compares with the value before the last change;
  otherwise the reference is the figure's first value of the user's day.

Code reads the figure at each change of the file (a value an event wrote, an author's write), with
no model, and keeps in `core.db` what the variation needs (its first value of the day, the previous
one, its last ones for the tile's line). The node is past its threshold once the variation reaches
it, and stays so until it falls under 0.85 of it. Opened while past it, the node is **seen**: a
circle again, on every screen and after a reload, until the variation falls under 0.85 of the
threshold and crosses it again. A glance is information, never a request: a node that waits for the
user keeps its circle and its halo. In a callout marked `external`, or a node that is, the label may
be someone else's words, and the tile says so.

### Opening an item

A server declares, in the `_meta` of one of its read tools, that the tool reads one of its items by
id: `"eriphe/open": {"id": "id", "title": "subject", "from": "from", "date": "date", "body":
"body"}` (the parameter that takes the id, nothing else required, and which field of what it says is
the title, the sender, the date, the body; the mail and calendar servers declare theirs). A row of
a page that references an item of that server opens it below itself on a click: code finds the
reference in the page as stored, by its label (`POST /api/nodes/{id}/open` with `{"ref": "m1"}`),
never an id from the click, and calls the tool with the id as the page writes it, under the tool's
access policy: a free read that does not reach the open web runs; any other is refused, with a line
saying to ask Eriphe, who will propose it on a card. It says when the page's words for the item are
misleading. Only on the page's last version. The click that opens is never a yes.

The page shows what the tool said as text, never as HTML, as a letter: who (a name, its address
quieter) and when (as people say it) on one line, its title or subject, its body, the fields as the
read tool declares them; then the card that waits on the item, if any; and one « détails » with the
exact call and the whole item as JSON. Someone else's words are marked. Nothing is written: no
version, no trace, nothing the brain reads; the page keeps it for its session.

### Code's fields

A callout's `external` option ([Lines that follow events](#lines-that-follow-events)), and the front
matter's `external`, `title_trusted` and `opened_by`, are written by code only:

- An author (the brain, a task, the user in Eriphe's editor) may leave them out: code keeps them as
  the version written over has them.
- Written alike, they are accepted. Written otherwise, the write is refused.
