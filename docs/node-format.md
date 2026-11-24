# The node file format

Eriphe's workspace is a graph of **nodes**. A node is one piece of work and its result: a subject, a
research, an incident, a reply to write, a standing view like the mails. Each node is one markdown
file with YAML front matter and typed **blocks**. This page describes that file, for the brain, for
tasks, and for anyone who reads or reuses the files.

The Go package [`core/nodefile`](../core/nodefile) reads and writes this format; everything else in
Eriphe goes through it.

## A full example

````markdown
---
id: k3v9q2m7
title: Substack
links: [p8d2x4c1]
lifetime: standing
goal: Grow the newsletter. Write the first article with Sam's notes.
created: 2026-11-17T09:00:00+01:00
---
# Substack

The newsletter and its first article. The articles that work are short and
open on a question.

```eriphe:block readers
{"components": [
  {"id":"root","component":"Card","title":"Subscribers","child":"chart"},
  {"id":"chart","component":"Chart","kind":"line","unit":"readers","series":{"@path":"/subscribers/series"}}
],
 "sources": {"subscribers":{"tool":"substack/stats","args":{"metric":"subscribers"},"every":60}}}
```

The titles proposed for the first article:

```eriphe:block titles
{"components": [
  {"id":"root","component":"List","children":{"path":"/titles","componentId":"row"}},
  {"id":"row","component":"Item","title":{"@path":"label"},"detail":{"@path":"why"}}
],
 "data": {"titles":[{"id":"t1","label":"Why I write by hand","why":"opens on a question"},{"id":"t2","label":"Ten years of notes","why":"short"}]}}
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
| `links` | list of ids | Written by code only (a view's draft links the subject it was proposed from): the nodes it links to; no duplicate, never itself. An author's write keeps them. An author links nodes in the text instead ([Links between nodes](#links-between-nodes)). |
| `lifetime` | `work` or `standing` | Work ends when it is done; a standing node (the day, the mails) stays. Required. A standing node is a view: only `propose_view` makes one, its draft a work node until the user's yes turns it standing; `node_open` opens work only. |
| `goal` | text | What the node is for. Never shown to the user. |
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

- **Where:** each file says its own format, in its front matter's `format`. The format is 1 today,
  the only one, and a file that does not say it is in format 1: no file is rewritten to say it.
  It is in the file rather than once for the repository because the file is the unit: a file
  copied in from elsewhere, and each old version in the history, says the format it was written
  in, so it is read as it was meant (a version read from the history is converted in memory).
  Whatever else changes, the front matter stays YAML between two `---` lines, with `format` in it.
- **A change of the format** comes with its conversion, from the format before it (a function of
  the file's bytes, in [`core/nodefile`](../core/nodefile)), and a test that reads a file of the old
  format. The new format is the one after the last conversion; files are written in it, `format`
  then in their front matter.
- **At start**, a file of an older format is converted to today's, through each conversion in
  turn: all of them in one commit by `convert`, each node a version of its own, the old one kept in
  the history. A file changed by hand meanwhile is first committed as it is (`outside`). A file
  whose conversion fails, or gives a file that does not read as a node (one broken by hand), is
  reported and left alone like any file that does not read, its node's last good version still
  readable; it is converted at a later start, once mended.
- **A file of a newer format** than this Eriphe knows stops the graph at start, with an error that
  names it; nothing is written, the file is left as it is. Update Eriphe.
- **The index** (`nodes.db`) holds nothing the files do not: it may be rebuilt from them at any
  time, and is never converted.

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
| `components` | the author | A flat list of components of Eriphe's catalog (`core/a2ui/catalog_v1.json`), one with id `root`. |
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

A source may also give its node a **glance**: a figure its value holds, that the node's bubble shows
as a small tile when the figure moved past a threshold, until the user opens the node. One per node.

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

Code reads the figure at each read of its source, with no model, and keeps in `core.db` what the
variation needs (its first value of the day, the previous one, its last ones for the tile's line). The
node is past its threshold once the variation reaches it, and stays so until it falls under 0.85 of
it. Opened while past it, the node is **seen**: a circle again, on every screen and after a reload,
until the variation falls under 0.85 of the threshold and crosses it again. A glance is information,
never a request: a node that waits for the user keeps its circle and its halo.

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

A card is one thing proposed: the user says Oui, or ✕. Calls that do the same thing to several
items of a node (the same tool and arguments but for the ids) wait as one card: on each of their
rows, the same card; folded in a `Fold` with `list`, the batch carries its Oui, and unfolded the
user unticks the rows that should not go. Every answer, from a card, a word in the bar, a channel
or a page in Réglages, goes through the same code, which keeps the card in step.

### Sections

A view may show a source's items in sections (« À traiter », « À supprimer », « Pour info »): its
author notes each item with a `group` (`item_note`), a short label it chooses, and writes one `List`
per section whose template's path filters the source's items by a field:

```json
{"id": "drop", "component": "List", "children": {"path": "/mails/messages[_group=À supprimer]", "componentId": "row"}}
```

- A filter is `[field=value]` or `[field!=value]`, one or more after the source's items as their
  `items` path says them (all must hold: `[_group=À traiter][_state!=done]`), on a `List` only;
  checked when an author writes the block. A field the item has not reads as empty: `[_group=]`
  keeps the items in no group.
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
  node are woken (a journal event `node.gesture`): the view's skill writes its bilan again.
- Code filters, never the page: the page draws the List bound to the source's items as a whole and
  shows only the rows code kept, in the source's order; a List whose filter keeps nothing shows its
  `emptyText`.
- A `Fold` with `list` names a `List` of the block bound to a source's items: its line counts that
  List's rows and carries the Oui of the card they share, if any.
- A `Metric` with `list` (in place of `value`) names such a `List` too: its figure is the number of
  rows code keeps, its filters applied, and follows each read of the source. A page that lasts says
  its facts so, or with a `Metric` bound to a source's field, never in frozen text.

### Setup buttons

A `Button` that sets something up says what with `until`, one of `{"tool": "<server>"}` (a tool in
the catalog that waits for no authorization), `{"monitor": "<name>"}` (a monitor that runs) or
`{"skill": "<name>"}` (a skill approved and on). Code hides it from the page once that holds (and a
`Row` or `Column` it leaves empty, a `Card` or `Fold` whose child it was), reading its own records
each time the page is drawn: its author need not take it away.

A skill's run whose call acts on an item without naming it (an event made from a mail) puts it on
that item with `item_note`'s `call` (the reference the run was told when the call was kept): its
card then waits on the item's row, its words naming the item.

A group is one line of 40 characters at most, without `[ ] / = !`.

### Reading an item

A source's `items` may say how one item is read in full: `"open": {"tool": "<server>/<tool>", "id":
"<param>"}`, a read tool of the source's own server and its parameter that takes an item's id
(nothing else required). Code checks it against the catalog when an author writes it.

A row of a `List` bound to that source opens below itself on a click (or its disclosure button; a
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

A block's `external`, and the front matter's `external`, `title_trusted` and `opened_by`, are written by code only:

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
