// Eriphe's service worker: it makes the workspace installable, keeps its
// static files at hand, and shows the notifications eriphe-core pushes
// (push.go). Pages, actions and the event stream always go to the network
// and are never cached: they are private and live.
//
// eriphe-core serves this file with the version of the static files in
// place of the placeholder below (web.go): a new version installs a new worker,
// which drops the old cache.
const VERSION = "__VERSION__";
const CACHE = "eriphe-static-" + VERSION;
const SHELL = ["/static/app.css", "/static/app.js", "/static/htmx.min.js", "/static/icon.svg",
  "/static/icon-192.png", "/static/badge-96.png", "/static/offline.html"];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith("eriphe-") && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (e) => {
  const req = e.request;
  const url = new URL(req.url);
  if (req.method !== "GET" || url.origin !== location.origin) return;
  if (req.mode === "navigate") {
    // A page: always the network; offline, a small page that says so.
    e.respondWith(fetch(req).catch(() => caches.match("/static/offline.html")));
    return;
  }
  if (!url.pathname.startsWith("/static/")) return; // never anything else
  if (url.searchParams.has("v")) {
    // A versioned file never changes: the cache first.
    e.respondWith(caches.match(req).then((hit) => hit || fetch(req).then((r) => {
      if (r.ok) {
        const copy = r.clone();
        caches.open(CACHE).then((c) => c.put(req, copy));
      }
      return r;
    })));
    return;
  }
  // Network first, so a new version shows at once; the cache when offline.
  e.respondWith(
    fetch(req)
      .then((r) => {
        if (r.ok) {
          const copy = r.clone();
          caches.open(CACHE).then((c) => c.put(req, copy));
        }
        return r;
      })
      .catch(() => caches.match(req, { ignoreSearch: true }))
  );
});

// A push from eriphe-core: {title, body, url, tag}, and for what the
// assistant told on its own, its buttons (actions) and where they answer.
self.addEventListener("push", (e) => {
  let data = {};
  try { data = e.data ? e.data.json() : {}; } catch (_) { data = { body: e.data ? e.data.text() : "" }; }
  const title = data.title || "Eriphe";
  e.waitUntil(self.registration.showNotification(title, {
    body: data.body || "",
    tag: data.tag || undefined,
    renotify: !!data.tag,
    icon: "/static/icon-192.png",
    badge: "/static/badge-96.png",
    data: { url: data.url || "/", react: data.react || "" },
    actions: Array.isArray(data.actions) ? data.actions.slice(0, 3) : [],
  }));
});

// A click opens the workspace where the notification points: in a window
// already open if there is one, else a new one. Only this origin's paths.
self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  const react = e.notification.data && e.notification.data.react;
  if (e.action && react) {
    // A button: 👍, 👎 or « pas maintenant », answered with the push's own
    // token; nothing opens. Only this origin's paths.
    const to = new URL(react, self.registration.scope);
    if (to.origin !== location.origin) return;
    e.waitUntil(fetch(to.href, { method: "POST", credentials: "omit",
      body: new URLSearchParams({ r: e.action }) }).catch(() => {}));
    return;
  }
  let target = new URL((e.notification.data && e.notification.data.url) || "/", self.registration.scope);
  if (target.origin !== location.origin) target = new URL("/", self.registration.scope);
  e.waitUntil((async () => {
    const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    const win = wins.find((w) => new URL(w.url).origin === location.origin);
    if (win) {
      await win.focus();
      if ("navigate" in win) {
        try { await win.navigate(target.href); return; } catch (_) { /* not controlled yet */ }
      }
      win.postMessage({ type: "open", url: target.href });
      return;
    }
    await self.clients.openWindow(target.href);
  })());
});
