import { signedOut, tab } from "./api";
import {
  applyDoc, applyExchange, applyMoved, applyNodes, applyNotes, applyProgress, applyShow, applySource, step, type Store,
} from "./store";

// The workspace's event stream, /events?a2ui=1 (web_sse.go): one per page,
// with the page's tab id and the node the console talks to (node). Its
// events feed the store: "node", "doc", "progress", "source", "show",
// "exchange", "proposals" and "notice". When the console talks to another
// node, the stream opens again for it: eriphe-core sends that node's
// exchange, and the graph again, whole.

export function streamURL(talk: string | null): string {
  return `/events?a2ui=1&tab=${tab}${talk ? `&node=${encodeURIComponent(talk)}` : ""}`;
}

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
// called; it opens it again when the console talks to another node.
export function connect(store: Store): () => void {
  let es: EventSource | null = null;
  let talk = store.getState().talk;
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
    const me = new EventSource(streamURL(talk));
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
    on("exchange", (v) => step(store, applyExchange, v));
    on("proposals", (v) => step(store, applyMoved, v));
    on("notice", (v) => step(store, applyNotes, v));
  };

  const unsub = store.subscribe((s) => {
    if (s.talk === talk) return;
    talk = s.talk;
    open();
  });
  open();
  return () => {
    stopped = true;
    unsub();
    es?.close();
    es = null;
    if (retry) clearTimeout(retry);
  };
}
