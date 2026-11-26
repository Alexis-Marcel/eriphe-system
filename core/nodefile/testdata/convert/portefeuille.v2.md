---
format: 2
id: p4f8t2l6
title: Portefeuille
created: 2026-11-05T10:00:00+01:00
---
```eriphe:block resume
{"components": [
  {"id":"root","component":"Column","children":["fig","prog","alloc","movers"]},
  {"id":"fig","component":"Figures","title":"Aujourd'hui","items":[{"label":"PEA","value":18385,"unit":"€","delta":1.8,"deltaLabel":"cette semaine"},{"label":"Lignes","value":6}]},
  {"id":"prog","component":"Progress","label":"Rééquilibrage","value":2,"total":5,"step":"Vendre les obligations"},
  {"id":"alloc","component":"Table","title":"Allocation","rows":"allocation","units":{"part":"%"}},
  {"id":"movers","component":"List","children":{"componentId":"mv","path":"/movers/items"},"emptyText":"Rien ne bouge"},
  {"id":"mv","component":"Item","title":{"@path":"name"},"detail":{"@path":"ticker"},"note":{"@path":"change"}}
],
 "sources": {
   "allocation": {"tool":"investissement/get_allocation","items":{"path":"lines","id":"ticker","title":"name"}},
   "movers": {"tool":"investissement/get_movers"}
 }}
```
