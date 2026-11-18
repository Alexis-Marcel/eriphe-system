import { putView, type Theme } from "./api";
import { viewOf, type Store } from "./store";

// What eriphe-core keeps of the workspace as the user leaves it (#633,
// web_gestures.go): the pages open, the one the console talks to, the
// theme. keep sends them a moment after they change, the last only; the
// boot gives them back on any of the user's screens.

export const KEEP_AFTER = 600;

export function keep(store: Store): () => void {
  let last = JSON.stringify(viewOf(store.getState()));
  let timer: ReturnType<typeof setTimeout> | null = null;
  const unsub = store.subscribe((s) => {
    const now = JSON.stringify(viewOf(s));
    if (now === last) return;
    last = now;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => void putView(JSON.parse(now)).catch(() => undefined), KEEP_AFTER);
  });
  return () => {
    unsub();
    if (timer) clearTimeout(timer);
  };
}

// applyTheme sets the theme on the page: chosen, or the system's ("").
// The shell carries the one kept, before the app draws.
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme) root.dataset.theme = theme;
  else delete root.dataset.theme;
  document.querySelector('meta[name="color-scheme"]')?.setAttribute("content", theme || "light dark");
}
