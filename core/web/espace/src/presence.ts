import type { Note } from "./data";

// Whether the user is looking, as the conversation's page tells it
// (static/app.js): the tab tells eriphe-core when it comes in front or
// leaves, and every 20 s while it is in front, with its browser's push
// subscription, so that eriphe-core knows on which device the user is
// (route.go), feeds their activity (notify_quiet.go: the quiet hours, a
// return after an absence) and routes a notification here rather than
// pushing it. The answer may carry the notifications routed to this tab.
// The page also registers Eriphe's service worker (static/sw.js), which
// makes it installable and shows the pushed notifications.

export const PRESENCE_EVERY = 20_000;

// pushID is a push subscription's id: its endpoint's hash, as eriphe-core
// computes it (pushID, push.go).
export async function pushID(endpoint: string): Promise<string> {
  const h = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(endpoint)));
  return Array.from(h.slice(0, 16), (b) => b.toString(16).padStart(2, "0")).join("");
}

// localURL tells whether a notification's url is a path of this site: not
// protocol-relative, even as a browser reads it (a backslash for a slash,
// tabs and newlines dropped), and still on this origin once resolved.
export function localURL(url: string | undefined): url is string {
  if (typeof url !== "string" || !url.startsWith("/") || url.startsWith("//") || /[\\\u0000-\u001f\u007f]/.test(url)) {
    return false;
  }
  try {
    return new URL(url, location.origin).origin === location.origin;
  } catch {
    return false;
  }
}

export type PresenceOptions = {
  tab: string;
  csrf: () => string;
  onNotes: (n: Note[]) => void;
};

// startPresence tells the tab's presence until the returned function is
// called.
export function startPresence({ tab, csrf, onNotes }: PresenceOptions): () => void {
  let last: boolean | null = null;
  let at = 0;
  let sub = "";
  let stopped = false;
  const front = () => document.visibilityState === "visible" && document.hasFocus();
  const tell = (force: boolean) => {
    if (stopped) return;
    const f = front();
    if (!force && f === last && (!f || Date.now() - at < PRESENCE_EVERY)) return;
    last = f;
    at = Date.now();
    const body = new URLSearchParams({ tab, front: f ? "1" : "0", sub, csrf: csrf() });
    fetch("/presence", { method: "POST", body, credentials: "same-origin", keepalive: true, headers: { "X-CSRF-Token": csrf() } })
      .then((r) => (r.status === 200 ? r.json() : null))
      .then((j: { notes?: Note[] } | null) => {
        if (j && Array.isArray(j.notes) && j.notes.length > 0) onNotes(j.notes);
      })
      .catch(() => undefined);
  };
  const onChange = () => tell(false);
  const onHide = () => {
    if (navigator.sendBeacon) navigator.sendBeacon("/presence", new URLSearchParams({ tab, front: "0", csrf: csrf() }));
  };
  // A notification clicked while this page is open, and not yet controlled
  // by the worker: it asks the page to go there.
  const onWorker = (e: MessageEvent) => {
    const d = e.data as { type?: string; url?: string } | null;
    if (d && d.type === "open" && typeof d.url === "string" && new URL(d.url, location.href).origin === location.origin) location.href = d.url;
  };
  const worker = window.isSecureContext && "serviceWorker" in navigator ? navigator.serviceWorker : null;
  if (worker) {
    worker.register("/sw.js").catch(() => null);
    worker.addEventListener("message", onWorker);
    worker
      .getRegistration()
      .then((reg) => reg?.pushManager?.getSubscription() ?? null)
      .then((s) => (s ? pushID(s.endpoint) : ""))
      .then((id) => {
        sub = id;
        if (id && front()) tell(true);
      })
      .catch(() => undefined);
  }
  for (const ev of ["focus", "blur", "pageshow"]) window.addEventListener(ev, onChange);
  document.addEventListener("visibilitychange", onChange);
  window.addEventListener("pagehide", onHide);
  const timer = setInterval(() => {
    if (front()) tell(true);
  }, PRESENCE_EVERY);
  tell(true);
  return () => {
    stopped = true;
    clearInterval(timer);
    for (const ev of ["focus", "blur", "pageshow"]) window.removeEventListener(ev, onChange);
    document.removeEventListener("visibilitychange", onChange);
    window.removeEventListener("pagehide", onHide);
    worker?.removeEventListener("message", onWorker);
  };
}
