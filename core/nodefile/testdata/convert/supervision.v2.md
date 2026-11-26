---
format: 2
id: s7p2v4x9
title: Supervision
created: 2026-11-10T10:00:00+01:00
---
Les serveurs de la maison.
```eriphe:block charge
{"components": [
  {"id":"root","component":"Card","title":"Charge","child":"row"},
  {"id":"row","component":"Row","children":["cpu","mem","disque"]},
  {"id":"cpu","component":"Metric","label":"CPU","value":{"@path":"/cpu/data/result/0/value/1"},"unit":"%"},
  {"id":"mem","component":"Metric","label":"Mémoire","value":{"@path":"/mem/data/result/0/value/1"},"unit":"Go"},
  {"id":"disque","component":"Metric","label":"Disque","value":{"@path":"/disque/value"},"unit":"%","delta":-2,"deltaLabel":"depuis hier"}
],
 "sources": {
   "cpu": {"tool":"grafana-maison/query_prometheus","args":{"expr":"avg(cpu)"}},
   "mem": {"tool":"grafana-maison/query_prometheus","args":{"expr":"avg(mem)"}},
   "disque": {"monitor":"disque"}
 }}
```
Fin.
