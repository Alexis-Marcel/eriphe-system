# The node file format

Eriphe's workspace is a graph of **nodes**. A node is one piece of work and its result: a subject, a
research, an incident, a reply to write, a page kept up by a skill like the mails. Each node is one markdown
file with YAML front matter and typed **blocks**. This page describes that file, for the brain, for
tasks, and for anyone who reads or reuses the files.

The Go package [`core/nodefile`](../core/nodefile) reads and writes this format; everything else in
Eriphe goes through it.

## A full example

````markdown
---
format: 2
id: k3v9q2m7
title: Substack
links: [p8d2x4c1]
goal: Grow the newsletter. Write the first article with Sam's notes.
created: 2026-11-17T09:00:00+01:00
---
# Substack

The newsletter and its first article. The articles that work are short and
open on a question.

```eriphe:block readers
{"components": [{"id":"root","component":"Chart","kind":"line","title":"Subscribers","unit":"readers","series":{"@path":"/subscribers/series"}}],
 "sources": {"subscribers":{"tool":"substack/stats","args":{"metric":"subscribers"},"every":60}}}
```

The titles proposed for the first article:

```eriphe:block titles
{"components": [{"id":"root","component":"Table","rows":{"@path":"/titles"}}],
 "data": {"titles":[{"id":"t1","title":"Why I write by hand","why":"opens on a question"},{"id":"t2","title":"Ten years of notes","why":"short"}]}}
```
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

- **Where:** each file says its own format, in its front matter's `format`. The format is 2 today;
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

## The page (format 3)

In format 3 a node's body is a **Markdown page**, and only that: everything the page shows is
written in the file, so that a reader without Eriphe (Obsidian, another agent, the search) finds it
all. Eriphe draws it better, never otherwise. Format 3 is described here ahead of its conversion:
until the conversion of the blocks lands, files stay in format 2 and a body may still hold blocks
(below), which the page reader reads as code.

[`core/nodefile`](../core/nodefile) reads a page (`ReadPage`, `md.go`) into a tree, checks it
(`Check`, `check.go`) and changes one line of it (`lines.go`). Go's reading is the reference; the
workspace reads the same pages the same way, both held to the fixture pages of
[`core/nodefile/testdata/pages`](../core/nodefile/testdata/pages) (each page with its tree and its
problems as JSON). Lines are counted from 1 in the body, what follows the front matter.

**What a page holds.** Plain Markdown, with Obsidian's conventions where they exist:

- **Headings** `#` to `######`, paragraphs, tables, `---`, fenced code (`` ```diff `` for a diff),
  `[[Title]]` links and `![[Title]]` alone on a line to embed another node.
- **Lists.** An item is one line; the lines indented under it (spaces or tabs, a tab counting four)
  are its **detail**, read as blocks of their own. A task is `- [ ]` (open), `- [x]` (done), `- [/]`
  (in progress) or `- [-]` (dropped). `📅 2026-11-27` in an item's line is its date.
- **When it was written.** A first line of text that starts with « Écrit le », « Mis à jour le »
  (or « Written on », « Updated on ») says when the page was written; it is not one of the blocks.
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
  what Eriphe shows of the thing comes from the thing.
- **Eriphe's links**, valid Markdown links whose scheme is one of four:
  - `[Jeter ces mails](do:mail/trash)`, on its own line, the first under a heading: an action on
    the references of that heading's section, down to the next heading of its level or a higher
    one. Its author must have the tool. Anywhere else it acts on nothing.
  - `[Envoyer la réponse](card:<ref>)` places a card already proposed on that line.
  - `[Montre la semaine](ask:)` is a probable reply, sent to Eriphe as a message.
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

**In the workspace.** The page draws a node's body from its Markdown (`core/web/espace/src/page`):
a task is a row whose box the user ticks (one line rewritten by eriphe-core, a version of theirs),
an item's detail folds under it, a callout is drawn by the workspace's component of its type
(figures, a chart, steps, an agenda, a person, an alert), a reference shows its words with its
target, and a line of `ask:` links is a row of replies. A day without its year takes the year of
the page's « Écrit le » line, else it is shown as written: never a guessed year. Blocks still in
the body are drawn as before, at their place.

**Changing a line.** A page has several authors (a skill, code, the user's gestures), so an author
changes a line, never the whole page. `node_edit` replaces a line, or a piece of text, found once
(`old`, `new`), or moves an item with its detail (`move`, `after`: after a heading it becomes the
section's first item, under its `do:` line; after another item, it goes after that item's detail,
at its indent and in its callout). A line edit needs no version: it is made on the last one, and is
refused once its line is gone (the author reads the page again). A whole body (`node_write`) and a
block still name the version they were made on (`expect`).

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

## Body and blocks

The body is markdown. The page shows the node's title above it: a heading that only repeats the
title at the top of an author's body is dropped when it is written. A **block** is a fenced
section that starts with a line `` ```eriphe:block <name> `` and ends at the next line `` ``` ``:

- Its name is lowercase letters, digits and `_`, 32 at most, starting with a letter or a digit. A
  node has one block of each name.
- A fence `` ```eriphe:block `` inside another fenced code block (as in the example above, inside
  a four-backtick fence) is text, not a block.
- Between its fences, a block is one JSON object:

| Key | Who writes it | What |
|---|---|---|
| `components` | the author | One component of Eriphe's catalog (`core/a2ui/catalog_v1.json`), written alone (code gives it the id `root`), or a flat list of them, one with id `root`. |
| `data` | the author | An object: the values its components read. |
| `sources` | the author | Live sources read by code, by name: each one's value is at `/<name>`. |
| `external` | code | `true` when the block was written after reading external content, or binds a source of a server that is not trusted. |

A key that is not one of these is an error. The empty ones may be left out. The values a block's
sources read are code's, never in the file: they are kept in `core.db`, and the page draws the block
from them.

### Components and bindings

Each block is checked alone, as an A2UI surface of its own:

- against the catalog: component types, properties, ids, a `root`, children that exist;
- links: a `Link` url, literal or bound to the block's data, is an `http(s)` address;
- bindings (`{"@path": "/…"}`, and a template's `path`): each one reads the block's own `data`, one
  of its `sources` (`/<name>/…`, checked when code reads it). A binding to anything else is an
  error that names the block.
- a source named: a property the catalog marks `x-source` (`Sections`' and `Gallery`'s `items`,
  `Table`'s `rows`) may name one of the block's sources instead of a binding (`"items": "mails"`).
  It reads that source's rows: its items, where its `items` path says (`/mails/messages`), or
  node_find's nodes (`/<name>/nodes`). The file keeps the name; code and the page read the binding
  (`Block.Bound`). A name that is not one of the block's sources, a `Sections` over a source with no
  `items`, or a source whose items' path is not plain fields, is an error.

The catalog the brain reads is the one a model writes: a few blocks that each say what they show
(`Sections`, `Figures`, `Gallery`, `Table`, `Chart`, `Timeline`, `Asks`, `Agenda`, …), the page
supplying the rest (groups, counts, units, columns, layout on a phone, empty and reading states).
The components it wrote before (`List`, `Item`, `Fold`, `Grid`, `Card`, `Button`, `Metric`,
`Progress`, `Table`'s `emptyText`) are marked `x-legacy` in the catalog: no longer offered, still
checked and drawn, so that every node written with them reads as it did.

Where a card shows follows from the components, never from a name: a component whose catalog
schema says `x-rows` (the Agenda's `events`) bound to a source's items draws their cards on its
rows; one that says `x-days` (the Agenda) draws the dated proposals of its days, each with its
Yes, and a dated card of the node whose day it draws (a written or absent `date`) is not repeated
at the page's foot. An Agenda's events are in the shape the calendar server returns: `title`,
`start`, `end`, `place`, `allDay`, `status`.

### Sources

A source is one of:

- a tool: `{"tool": "<server>/<tool>", "args": {…}, "every": <minutes>}`;
- a resource: `{"resource": "<server>", "uri": "…", "every": <minutes>}`;
- a monitor the user approved: `{"monitor": "<name>"}`;
- nodes of the graph, as Eriphe's `node_find` finds them: `{"tool": "eriphe-core/node_find", "args":
  {"tags": ["etude"], "where": [{"key": "statut", "value": "en attente"}], "sort": "rendu",
  "columns": ["rendu"]}}`. Its value is `{"nodes": [{"id", "title", "status", "changed", … and each
  column}]}`, read again as soon as a node changes. A title written after an external read, and an
  external node's properties, are left out unless `include_external` is said: its block is then
  external. A `Table` whose rows read `/<name>/nodes` shows them, each row opening its node.

`every` is 1 to 1440 minutes (code's default when absent). A tool or a resource that lists things
may say where its items are: `"items": {"path": "messages", "id": "id", "title": "subject",
"detail": "from"}`, and how one is read in full (`open`, [Reading an item](#reading-an-item)). A source's name is lowercase letters, digits, `_` and `-`, 40 at most; it is not
also a key of `data`. A node waits for the user (its bubble's violet halo and ring) only while a
card waits on it: a source's items never make it wait by themselves.

A source's `glance` (below) is still read, so that the files that hold one keep reading, but no
longer shown: a figure at a glance is a page's line now ([A figure at a glance](#a-figure-at-a-glance)),
and the conversion to format 3 moves it there. It was a figure the source's value holds, one per
node:

```json
"sources": {"pea": {"tool": "bourse/portefeuille", "glance": {"label": "PEA", "path": "total", "threshold": 2}}}
```

- `label`: the figure's name on the tile, one line, 24 characters at most.
- `path`: where the figure is in the source's value, fields joined by dots, a list's index as a
  number (`rates.0.v`), six steps at most. Only a JSON number there counts: words never reach the
  tile.
- `threshold`: how far it must move to show, more than 0.
- `unit`: `percent` (by default: the variation in percent of the reference) or `amount` (in the
  figure's own units).
- `compare`: the reference, `day` (by default: its first value of the user's day), `previous` (its
  value before it last changed) or `fixed`, with `base` a value given (not 0 in percent).


Code reads the sources without a model, as the brain's free reads are: a source new or changed at
once, then every `every` minutes, a resource at its update, a monitor at each of its looks. A
source is also read again a second after any call that may change its server (any tool not marked
read-only, from the conversation, a card or a run), after its server says it changed,
and when its page is opened or the user comes back if it was read more than a minute ago. A read
that fails keeps the last good value. The values are kept in `core.db` only: a read makes no
version, however often it runs, and every version of the page, an archived node's too, shows the
last ones. A block that reads a server that is not trusted is marked `external`, in a version by
`eriphe`. An author's write made on a version older than one of code's (`external`) is not
stale: it applies over it, code's fields kept.

### No yes in a block

A block draws, reads and asks; it never acts. There are no block actions, no marks and no boxes to
pick: a button only asks Eriphe (`ask`, the words its author wrote, sent as the user's message; from
an `external` block, sent marked as not the user's own words), and
whatever asks the user's yes is a **card**, drawn by code: on an item's row when it waits on that
item (its `_card`, below), at the foot of the page otherwise, or where a `card` block places it (a
ref alone, so never `external`: a turn that read external content places one too). A
key `actions` or `marks` is an unknown field, refused like any other.

A card is one thing proposed: the user says yes, or ✕. Its yes button says its verb, never a bare
« Oui » (« Envoyer », « Archiver », « Jeter les 102 »): the tool's own (its `says` declares a
`verb`), else the first word of what the card says, else its kind's (« Garder », « Autoriser »).
Calls that do the same thing to several items of a node (the same tool and arguments but for the
ids) wait as one card: on each of their rows, the same card; in a `Sections` group whose rows all
share it, the batch carries its yes on its folded line, and unfolded the user unticks the rows that
should not go. Every answer, from a card, a word in the bar, a channel
or a page in Réglages, goes through the same code, which keeps the card in step.

### Sections

A page shows a source's items in sections (« À traiter », « À jeter », « Pour info »): its author
notes each item with a `group` (`item_note`), a short label it chooses, and a state, and writes one
`Sections` that names the source:

```json
{"components": {"component": "Sections", "items": "mails"}}
```

- The page groups the rows by `_group`, in the source's order: first the groups with something to
  do (a state `act` or `look`, or a card waiting), a row each, Eriphe's note first, then who and
  what, six shown then « n de plus »; a group whose rows all share one card is a batch, folded, its
  card's yes on its line; the other groups folded. Rows noted `done` leave. With nothing to do it
  says so; empty, and while its source is read, it says that too.
- Pages written before keep their `List`s, one per section, whose template's path filters the
  source's items by a field (`[_group=À supprimer]`, `[_state!=done]`, all must hold; on a `List`
  only, checked when the block is written; a field the item has not reads as empty), their `Fold`
  with `list` (a batch) and their `Metric` with `list` (a count): still checked and drawn.
- Each item of a source with `items` carries, in the block's data (never in its file), its note's
  fields: `_note`, `_state` and `_group` (empty when it has none), `_card`, the ref of the card that
  waits on it while it still asks something (its yes to give, or a yes that runs or failed; empty
  otherwise), and `_external: true` when the note was written after reading external content.
  Code writes them over whatever the source gave. A component may show them (`{"@path":
  "_note"}`); they follow the notes and the cards live. The page is also told each row's card out
  of band (its Lists' rows), never from the block's data.
- An item is done only by its note: a card on an item, once its yes ran, notes the item `done`
  (code's, its note and group kept, until a skill notes it again), and the source is read again: a
  List that keeps `[_state!=done]` lets it go at once. Said yes or no, the skills that work in the
  node are woken (a journal event `node.gesture`): the skill that maintains it writes its bilan again.
- Code filters, never the page: the page draws a List bound to the source's items as a whole and
  shows only the rows code kept, in the source's order.
- A page that lasts says its facts from its sources, never in frozen text: a `Figures` value bound
  to a source's field (`{"@path": "/pea/total"}`); one written by hand is said back to its author
  as a problem (the render report).

### Setup buttons

An ask of an `Asks` that sets something up says what with `until` (`{"text": "Branche l'agenda",
"until": {"tool": "calendar"}}`), one of `{"tool": "<server>"}` (a tool in the catalog that waits
for no authorization), `{"monitor": "<name>"}` (a monitor that runs) or `{"skill": "<name>"}` (a
skill approved and on). Code hides it from the page once that holds, reading its own records each
time the page is drawn: its author need not take it away. The page names an ask by its component
and its place (`root#1`), so a hidden ask leaves its place empty; an `Asks` left with none goes. A
`Button` with `until`, on pages written before, goes the same way (and a `Row` or `Column` it leaves
empty, a `Card` or `Fold` whose child it was).

A skill's run whose call acts on an item without naming it (an event made from a mail) puts it on
that item with `item_note`'s `call` (the reference the run was told when the call was kept): its
card then waits on the item's row, its words naming the item.

A group is one line of 40 characters at most, without `[ ] / = !`.

### Reading an item

A source's `items` may say how one item is read in full: `"open": {"tool": "<server>/<tool>", "id":
"<param>"}`, a read tool of the source's own server and its parameter that takes an item's id
(nothing else required). Code checks it against the catalog when an author writes it.

A row of a `Sections` (or a `List`) of that source opens below itself on a click (or its disclosure button; a
click on its card, a button or a link of the row does not), and closes the same way or by its ✕.
Code reads the open again in the block as stored, checks that the id is one the source lists now,
and calls the tool with it, as the source gave it, under the tool's access policy: a free read that
does not reach the open web runs; any other is refused, with a line saying to ask Eriphe, who will
propose it on a card. The click that opens is never a yes. The page shows
what the tool said as text, never as HTML, as a letter: who (a name, its address quieter) and when
(as people say it) on one line, its title or subject, its body; the fields as the read tool
declares them in its `_meta` (`"eriphe/open": {"title": "subject", "from": "from", "date": "date",
"body": "body"}`), else a title and a sender found as a row finds them and a text result as the
body, the rest only in « détails »; the item's
note when its row does not already say it (« Classé « À supprimer » : … »); then the card that
waits on the item, if any (« Sur Oui » and what its Oui does, a long field such as the reply
itself shown whole, and the same Oui as the row's); and one « détails » with the exact call and the
whole item as JSON. Someone else's words are marked once, by the page's head and the block's edge;
a block not so marked shows a small amber sign when the server is not trusted. Nothing is
written: no version, no trace, nothing the brain reads; the page keeps it for its session.

### Code's fields

A block's `external`, a callout's `external` option ([Lines that follow events](#lines-that-follow-events)),
and the front matter's `external`, `title_trusted` and `opened_by`, are written by code only:

- An author (the brain, a task, the user in Eriphe's editor) may leave them out: code keeps them as
  the version written over has them.
- Written alike, they are accepted. Written otherwise, the write is refused.
- A new block has none of them.

### The short form

An author may give a block's `components` as a single component, an object or a list of one, with
no `root`: Eriphe completes it with the id `root`. The file always holds the full form.

```json
{"components": {"component": "Text", "text": "Waiting for Sam's reply."}}
```

### Canonical form

A block written by code looks like this: its fence, then one key a line, each component on its own
line, the empty keys left out:

````markdown
```eriphe:block status
{"components": [
  {"id":"root","component":"Text","text":"Waiting for Sam's reply."}
],
 "data": {"since":"2026-11-17"}}
```
````
