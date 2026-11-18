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
| `links` | list of ids | The nodes it links to; no duplicate, never itself. |
| `lifetime` | `work` or `standing` | Work ends when it is done; a standing node (the day, the mails) stays. Required. |
| `goal` | text | What the node is for. Never shown to the user. |
| `created` | timestamp | When it was created (RFC 3339). Required. |
| `done` | timestamp | When it was marked done; absent while not done. |
| `archived` | timestamp | When the user archived it; absent while not archived. |
| `external` | `true` | Written by code only: the node holds someone else's words. Absent otherwise. |

Any other field is kept with its value. While the front matter does not change, it keeps its bytes,
comments included. When code writes it again (a new title, say), the known fields come first in the
order of the table, the empty ones left out, then the other fields in their order; YAML comments are
not kept. A field given twice, or YAML that does not parse, is an error.

The node's state (`running`, `waiting`, `idle`, `done`) is not in the file: it is code's, in the
graph's index.

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
| `snapshot` | code | The sources' last good values, with `at`, so that the file reads well offline. |
| `marks` | code | The user's marks: by list, then by item, `keep`, `drop` or `done`. |
| `external` | code | `true` when the block was written after reading external content, or binds a source of a server that is not trusted. |

A key that is not one of these is an error. The empty ones may be left out.

### Components and bindings

Each block is checked alone, like a surface of its own:

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
"detail": "from"}`. A source's name is lowercase letters, digits, `_` and `-`, 40 at most; it is not
also a key of `data`, and `marks` is no source's or data key's name.

### Code's fields

`snapshot`, `marks` and `external`, and the front matter's `external`, are written by code only:

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
