---
id: m4ilx7q2
title: "Reply to Sam: the production app"
parent: k3v9q2m7
links: [p8d2x4c1, a1b2c3d4]
lifetime: work
goal: |-
  Answer Sam within the hour.
  Find why the app fails.
created: 2026-11-17T10:02:00+01:00
done: 2026-11-17T11:30:00+01:00
archived: 2026-11-18T08:00:00+01:00
external: true
# a comment, kept
color: amber
tags:
  - mail
  - prod
---
# The production app

Sam wrote at 10:02: the app answers 500 since the deploy.

---

A rule above, then a block:

```eriphe:block errors
{"components": [{"id": "root", "component": "Card", "title": "5xx per minute", "child": "c"},
                {"id": "c", "component": "Chart", "kind": "line", "series": {"@path": "/errors_5xx/series"}}],
 "data": {},
 "sources": {"errors_5xx": {"tool": "grafana.query", "args": {"q": "errors_5xx"}, "every": 1}},
 "snapshot": {"at": "2026-11-17T10:14", "errors_5xx": {"series": []}},
 "marks": {},
 "external": false}
```

Between the blocks.

```eriphe:block draft
{"components": [{"id": "root", "component": "Markdown", "text": {"@path": "/text"}}],
 "data": {"text": "I am looking at it within the hour."},
 "marks": {"lines": {"l1": "done"}},
 "external": true}
```
