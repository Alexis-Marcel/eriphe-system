---
format: 2
id: a9g3n1d2
title: Ma semaine
created: 2026-11-20T08:00:00+01:00
---
```eriphe:block semaine
{"components": [
  {"id":"root","component":"Column","children":["t","ag"]},
  {"id":"t","component":"Text","text":"Les rendez-vous de la semaine","variant":"caption"},
  {"id":"ag","component":"Agenda","days":7,"events":{"@path":"/agenda/events"}}
],
 "sources": {"agenda": {"tool":"agenda/list_events","args":{"days":7},"items":{"path":"events","id":"id","title":"title","open":{"tool":"agenda/get_event","id":"event_id"}}}}}
```

```eriphe:block demain
{"components": [{"id":"root","component":"Agenda","date":"2026-11-27","events":[
  {"title":"Dentiste","start":"2026-11-27T09:30:00+01:00","end":"2026-11-27T10:00:00+01:00","place":"Centre"},
  {"title":"Anniversaire de Sam","start":"2026-11-28","allDay":true}
]}]}
```
