import { signedOut, tab } from "./api";
import {
  applyDoc, applyMoved, applyNodes, applyNotes, applyOrb, applyProgress, applyShow, applySource, step, type Store,
} from "./store";

// The workspace's event stream, /events (web_sse.go): one per page, with
// the page's tab id. Its events feed the store: "node", "doc",
// "progress", "source", "show", "orb", "proposals" and "notice".

export const streamURL = () => `/events?tab=${tab}`;

// STREAM_GRACE is how long the stream may stay down before the page says
// so: a restart of eriphe-core or a phone waking takes less.
export const STREAM_GRACE = 20_000;

type Parse<T> = (data: string) => T | null;
const parse: Parse<never> = (d) => {
  try {
    return JSON.parse(d);
  } catch {
    return null;
  }
};

// connect follows the stream into the store until the returned function is
// called.
export function connect(store: Store): () => void {
  let es: EventSource | null = null;
  let retry: ReturnType<typeof setTimeout> | null = null;
  let stopped = false;

  const on = (type: string, f: (data: never) => void) =>
    es!.addEventListener(type, (e) => {
      const v = parse((e as MessageEvent).data);
      if (v !== null) f(v);
    });

  const open = () => {
    es?.close();
    if (retry) clearTimeout(retry);
    retry = null;
    const me = new EventSource(streamURL());
    es = me;
    me.onopen = () => store.setState({ live: true });
    me.onerror = () => {
      store.setState({ live: false });
      if (me.readyState !== 2 || es !== me || stopped) return; // 2: CLOSED; else the browser tries again
      retry = setTimeout(() => {
        fetch("/espace/boot", { credentials: "same-origin" })
          .then(signedOut)
          .catch(() => undefined)
          .finally(() => es === me && !stopped && open());
      }, 3000);
    };
    on("node", (v) => step(store, applyNodes, v));
    on("doc", (v) => step(store, applyDoc, v));
    on("progress", (v) => step(store, applyProgress, v));
    on("source", (v) => step(store, applySource, v));
    on("show", (v) => step(store, applyShow, v, Date.now()));
    on("orb", (v) => step(store, applyOrb, v));
    on("proposals", (v) => step(store, applyMoved, v));
    on("notice", (v) => step(store, applyNotes, v));
  };

  open();
  return () => {
    stopped = true;
    es?.close();
    es = null;
    if (retry) clearTimeout(retry);
  };
}
