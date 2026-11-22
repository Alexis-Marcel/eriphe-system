import { putView, type Theme } from "./api";
import { viewOf, type State, type Store } from "./store";

// What eriphe-core keeps of the workspace as the user leaves it (#633,
// web_gestures.go): the pages open, the one the console talks to, the
// theme. keep sends them a moment after they change, the last only, and at
// once if the page leaves before; the boot gives them back on any of the
// user's screens. A page opened on a node shown as its glance's tile is
// sent at once: eriphe-core marks the glance seen (node_glance.go), even
// if the page closes within the moment.

export const KEEP_AFTER = 600;

export function keep(store: Store): () => void {
  let last = JSON.stringify(viewOf(store.getState()));
  let timer: ReturnType<typeof setTimeout> | null = null;
  const send = (leaving: boolean) => {
    if (timer) clearTimeout(timer);
    timer = null;
    void putView(JSON.parse(last), leaving).catch(() => undefined);
  };
  const unsub = store.subscribe((s, was) => {
    const now = JSON.stringify(viewOf(s));
    if (now === last) return;
    last = now;
    if (opensGlance(s, was)) return send(false);
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => send(false), KEEP_AFTER);
  });
  const leave = () => timer && send(true);
  addEventListener("pagehide", leave);
  return () => {
    unsub();
    removeEventListener("pagehide", leave);
    if (timer) clearTimeout(timer);
  };
}

// opensGlance tells whether a page just opened on a node whose glance is
// past its threshold and not seen.
export function opensGlance(s: State, was: State): boolean {
  return s.open.some((id) => !was.open.includes(id) && !!s.nodes[id]?.glance?.shown && !s.nodes[id]?.glance?.seen);
}

// applyTheme sets the theme on the page: chosen, or the system's ("").
// The shell carries the one kept, before the app draws.
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme) root.dataset.theme = theme;
  else delete root.dataset.theme;
  document.querySelector('meta[name="color-scheme"]')?.setAttribute("content", theme || "light dark");
}
