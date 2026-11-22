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
 "sources": {"subscribers":{"tool":"substack/stats","args":{"metric":"subscribers"},"every":60}},
 "snapshot": {"at":"2026-11-17T10:14:00+01:00","subscribers":{"series":[{"name":"Subscribers","points":[{"x":"mon.","y":120},{"x":"tue.","y":126}]}]}}}
```

The titles proposed for the first article:

```eriphe:block titles
{"components": [
  {"id":"root","component":"List","children":{"path":"/titles","componentId":"row"}},
  {"id":"row","component":"Row","children":["name","keep"]},
  {"id":"name","component":"Text","text":{"@path":"label"}},
  {"id":"keep","component":"Mark","list":"titles","item":{"@path":"id"},"marks":["keep","drop"]}
],
 "data": {"titles":[{"id":"t1","label":"Why I write by hand"},{"id":"t2","label":"Ten years of notes"}]},
 "marks": {"titles":{"t1":"drop","t2":"keep"}}}
```
````

## The file

- **Name:** `<slug>-<id>.md`, flat in the graph's repository. The name is frozen when the node is
  created: renaming a node changes its title, not its file. Its trace is `<slug>-<id>.trace.md`.
- **Encoding:** UTF-8. The file starts with its front matter, between two `---` lines; the body
  follows.
- **Round trip:** reading a file and writing it again gives the same bytes. When code changes one
  field or one block, only that part is written again, in its canonical form; the rest of the file
  keeps its bytes, comments and layout included.

## Front matter

| Field | Type | Meaning |
|---|---|---|
| `id` | text | The node's id: lowercase letters and digits, 32 at most, no dash. Required. |
| `title` | text | Its title for the user: one line, 120 characters at most. Required. |
| `parent` | id | The node it belongs to; none for a node at the top. |
| `links` | list of ids | Written by code only (a view created from a card links the subjects the card was raised in): the nodes it links to; no duplicate, never itself. An author's write keeps them. An author links nodes in the text instead ([Links between nodes](#links-between-nodes)). |
| `after` | list of ids | Written by code only, at a step's creation: the sibling steps it waits for (no duplicate, never itself). Absent for a node that is no step. |
| `lifetime` | `work` or `standing` | Work ends when it is done; a standing node (the day, the mails) stays. Required. |
| `goal` | text | What the node is for. Never shown to the user. |
| `created` | timestamp | When it was created (RFC 3339). Required. |
| `done` | timestamp | Written by code (Eriphe's `node_set` `done`, the user's gestures): when it was marked done; absent while not done. An author's write keeps it. |
| `archived` | timestamp | When the user archived it; absent while not archived. |
| `external` | `true` | Written by code only: the node holds someone else's words. Absent otherwise. |
| `title_trusted` | `true` | Written by code only, on an external node: its title is not someone else's words (written before the node was external, or by a writer that vouched for it: the user approving it on a card, an author that read nothing external). Absent otherwise: the title of an external node is then shown marked, in amber with a ⚠ (« Titre écrit après une lecture extérieure »), never replaced; code never acts on a title. |
| `opened_by` | a mapping | Written by code only, when it makes the node: who wanted it. `by` is `user` (a message of theirs, `msg` its number; or their yes on a card, `card`), `skill` (a skill's run: `skill` its name, `trigger` what started it: `monitor:<name>`, `alert:<hook>` or `schedule`) `event` (a turn of Eriphe's on events only: a wake, an intention, a monitor's event) or `eriphe` (one of Eriphe's own places, made by code: « Outils à revoir »). A node a task or a step opened says who wanted that work, with the `task` and the `node` it was opened for. Absent on a node made before it was recorded: its origin is unknown. The workspace says it in the page's ⋯ menu, and marks a node the user did not ask for (`skill`, `event`) with « ↯ ». |

Any other field is kept with its value. While the front matter does not change, it keeps its bytes,
comments included. When code writes it again (a new title, say), the known fields come first in the
order of the table, the empty ones left out, then the other fields in their order; YAML comments are
not kept. A field given twice, or YAML that does not parse, is an error.

The node's state (`running`, `waiting`, `idle`, `done`) is not in the file: it is code's, in the
graph's index.

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

The body is markdown. A **block** is a fenced section that starts with a line
`` ```eriphe:block <name> `` and ends at the next line `` ``` ``:

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
| `actions` | the author | What the user may run on the items of a source they pick in a list, by name ([Actions](#actions)). |
| `snapshot` | code | The sources' last good values by name, with `at` (when they were read), so that the file reads well offline. |
| `marks` | code | The user's marks: by list, then by item, `keep`, `drop` or `done`. |
| `external` | code | `true` when the block was written after reading external content, or binds a source of a server that is not trusted. |

A key that is not one of these is an error. The empty ones may be left out.

### Components and bindings

Each block is checked alone, as an A2UI surface of its own:

- against the catalog: component types, properties, ids, a `root`, children that exist;
- links: a `Link` url, literal or bound to the block's data, is an `http(s)` address;
- bindings (`{"@path": "/…"}`, and a template's `path`): each one reads the block's own `data`, one
  of its `sources` (`/<name>/…`, checked when code reads it), or the user's marks
  (`/marks/<list>/<item>`). A binding to anything else is an error that names the block.

### Sources

A source is one of:

- a tool: `{"tool": "<server>/<tool>", "args": {…}, "every": <minutes>}`;
- a resource: `{"resource": "<server>", "uri": "…", "every": <minutes>}`;
- a monitor the user approved: `{"monitor": "<name>"}`.

`every` is 1 to 1440 minutes (code's default when absent). A tool or a resource that lists things
may say where its items are: `"items": {"path": "messages", "id": "id", "title": "subject",
"detail": "from"}`, and how one is read in full (`open`, [Reading an item](#reading-an-item)). A source's name is lowercase letters, digits, `_` and `-`, 40 at most; it is not
also a key of `data`, and `marks` is no source's or data key's name, nor `at` a source's (the
snapshot's time). A source with `items` may say `"attention": true`: while it lists items, its
node waits for the user (its bubble's violet halo and ring say so, as for a card); when it lists none, the
node rests.

Code reads the sources without a model, as the brain's free reads are: a source new or changed at
once, then every `every` minutes, a resource at its update, a monitor at each of its looks. A
source is also read again a second after any call that may change its server (any tool not marked
read-only, from the conversation, a card, an action or a run), after its server says it changed,
and when its page is opened or the user comes back if it was read more than a minute ago; the
items such a call named show done until that read says them gone, or back. A read
that fails keeps the last good value. A value that changed is written into the block's `snapshot`
with `at`, in a version by `eriphe`, once a quarter of an hour at most per block; a block that
reads a server that is not trusted is marked `external`. An author's write made on a version older
than code's snapshot is not stale: it applies over it, the snapshot kept.

### Actions

A block may declare what the user can run on the items of one of its sources, by name:

```json
"actions": {"archive": {"label": "Ranger", "tool": "mail/move", "args": {"to": "archive"},
                        "items": "ids", "source": "inbox"}}
```

- `tool`: a catalog tool, `<server>/<tool>`, as a source's; `args`: its fixed arguments, or one
  taken from each item, `{"@item": "<field>"}` (plain fields joined by dots): one call per value
  the picked items have, each with its own items (`"args": {"account": {"@item": "account"}}`
  trashes the mails of each account with that account named);
- `items`: the tool's parameter that takes the ids the user picks, a list; never among `args`;
- `source`: one of the block's sources, with an `items` descriptor (the ids are its items' `id`);
- `label`: the author's name for it, one line, 60 characters at most: only a hint;
- `list` (optional): the id of the one `List` of the block it shows on, a List of that source's
  items; without it, it shows on every List of them.

A name is lowercase letters, digits, `_` and `-`, 40 at most. When an author writes it, code checks
it against the catalog: the tool is there, `items` is one of its parameters that takes a list, an
arg taken from each item is one of its parameters that takes one value, and the fixed `args` hold
against its input schema with those parameters left out.

A `List` bound as a whole to that source's items (its template's `path` is `/<source>/<items.path>`,
a path of plain fields, with or without a [filter](#sections)) lets the user pick rows: a box on
each, « tout sélectionner », a button per action on all its rows (« Tout supprimer », up to 200
rows), and, once something is picked, the actions over the picked rows. What a button says is
code's, built from the tool and its fixed args (« Déplacer les 2 → archive »), never with the tool
(the confirm and the button's tooltip say it); the `label` shows beside it, never on an `external`
block. A tool that looks destructive (it says so, or `trash`, `delete`, `remove` in its name or
description) shows its exact call (the tool, its args, an arg taken from each item by its values,
« account : perso, travail (un appel par account) », and the very items it will take, title ·
detail) before it runs.

The click is the user's own gesture, on the ids the page showed (frozen when the button is pressed,
whatever the list shows meanwhile): code reads the action and the List the click was made in again
in the block as stored (each the same as at the version the page showed), checks that every id is
one the source lists now and that the List's filters keep now, and runs one call on those ids, never
more (a mail come since the page showed the list is not in it; one id that is not enough for nothing
to run), or, with an arg taken from each item, one call per value read from the source's items
(an item without it, and nothing runs), with the tool's access policy for each call, all checked
before any runs (never: refused, but for a tool only the user's gesture runs, such as
`eriphe-catalog`'s `catalog_accept` and `catalog_refuse`, never to any agent and run by the click
alone; on approval: the click is the yes, logged as such; free: runs; a call that must name its
account and names none is refused). The node's trace says each call in code's words. The source is
then read again, and the skills that work in the node are woken (a journal event `node.gesture`,
once the user's gestures pause ten seconds): the view's skill writes its bilan again.

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
- A card on an item, once its yes ran, notes the item `done` (code's, its note and group kept,
  until a skill notes it again): a List that keeps `[_state!=done]` lets it go at once. Said yes or
  no, the skills that work in the node are woken, as after an action.
- Code filters, never the page: the page draws the List bound to the source's items as a whole and
  shows only the rows code kept, in the source's order; a List whose filter keeps nothing shows its
  `emptyText`. An action with `list` shows on its section only.

A skill's run whose call acts on an item without naming it (an event made from a mail) puts it on
that item with `item_note`'s `call` (the reference the run was told when the call was kept): its
card then waits on the item's row, its words naming the item.
- Code filters, never the page: the page draws the List bound to the source's items as a whole and
  shows only the rows code kept, in the source's order; a List whose filter keeps nothing shows its
  `emptyText`. An action with `list` shows on its section only.

A group is one line of 40 characters at most, without `[ ] / = !`.

### Reading an item

A source's `items` may say how one item is read in full: `"open": {"tool": "<server>/<tool>", "id":
"<param>"}`, a read tool of the source's own server and its parameter that takes an item's id
(nothing else required). Code checks it against the catalog when an author writes it.

A row of a `List` bound to that source opens below itself on a click (or its disclosure button; a
click on its box, a button or a link of the row does not), and closes the same way or by its ✕.
Code reads the open again in the block as stored, checks that the id is one the source lists now,
and calls the tool with it, as the source gave it, under the tool's access policy: never is
refused; a free read that does not reach the open web runs; any other shows the call first, inline,
and runs on the user's yes, given on the tool shown (logged as such). The click that opens is not the yes. The page shows
what the tool said as text, never as HTML, as a letter: who (a name, its address quieter) and when
(as people say it) on one line, its title or subject, its body (body, text or content); the item's
note when its row does not already say it (« Classé « À supprimer » : … »); then the card that
waits on the item, if any (« Sur Oui » and what its Oui does, a long field such as the reply
itself shown whole, and the same Oui as the row's); and one « détails » with the exact call and the
whole item as JSON. Someone else's words are marked once, by the page's head and the block's edge;
a block not so marked shows a small amber sign when the server is not trusted. Nothing is
written: no version, no trace, nothing the brain reads; the page keeps it for its session.

### Code's fields

`snapshot`, `marks` and `external`, and the front matter's `after`, `external`, `title_trusted` and `opened_by`, are written by code only:

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
