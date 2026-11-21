import { useEffect, useRef, useState } from "react";
import type { GraphNode } from "../data/api";
import { useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import type { T } from "../i18n";
import { close, expires, leaves, loadClosed, orbShow, replaced, saveClosed, type Closed, type Show } from "./model";

// useOrb is what the orb shows now (orbShow), and closing it: the store's
// feed, what the user just sent, what was shown already. The first time a
// thing shows is when its while starts; at its end, or when something else
// happens (newer words, the user writes, opens another subject, closes
// it), it is remembered (Closed), and the next thing shows.

const met = new Map<string, number>();
let closed: Closed | null = null;
let last: Show | null = null; // what showed before, to know what replaced it
const users = new Set<() => void>(); // every component that shows the orb, drawn again when it changes
const bump = () => users.forEach((f) => f());
let crowded = false; // no free spot for it out in the space: it says it from the bar

// The orb's clock counts only the time the page is seen: words do not go
// while the tab is hidden. seen is the time counted until from, when the
// page was last seen visible (null: hidden since).
let seen = 0;
let from: number | null = null;
const visible = () => document.visibilityState !== "hidden";
function clock(): number {
  const now = Date.now();
  if (from !== null) seen += now - from;
  from = visible() ? now : null;
  return seen;
}
// The tab hidden or shown again: the clock stops or goes on.
const onVisibility = () => {
  clock();
  bump();
};

// forget remembers a thing as over: closed, or its while over.
function forget(s: Show): void {
  closed = close(closed!, s);
  met.set(s.key, -Infinity); // a call has nothing to keep: its while is over
  saveClosed(closed);
  bump();
}

// setCrowded says whether the orb found no free spot (Orb.tsx): its
// words then show as a line above the bar, as on a phone.
export function setCrowded(v: boolean): void {
  if (v === crowded) return;
  crowded = v;
  bump();
}

export function useOrb(): { show: Show; dismiss: () => void; crowded: boolean } {
  const feed = useGraph((s) => s.orb);
  const sent = useGraph((s) => s.sent);
  const nodes = useGraph((s) => s.nodes);
  const call = useGraph((s) => s.call);
  const talk = useGraph((s) => s.talk);
  const [, tick] = useState(0);
  useEffect(() => {
    const f = () => tick((n) => n + 1);
    users.add(f);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      users.delete(f);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);
  closed ??= loadClosed();
  const now = clock();
  const show = orbShow(feed, { now, sent, closed, met: (k) => met.get(k), nodes, call });
  if (show.mode !== "rest" && !met.has(show.key)) met.set(show.key, now);
  const dismiss = () => forget(show);

  // Newer words, or the user writing, replace the words shown: over.
  useEffect(() => {
    if (last && replaced(last, show)) forget(last);
    last = show;
  }, [show.key]); // eslint-disable-line react-hooks/exhaustive-deps
  // Else, after their while of the page being seen, they go.
  const ms = expires(show);
  const left = ms === null ? null : ms - (now - (met.get(show.key) ?? now));
  const shown = visible();
  useEffect(() => {
    if (left === null || !shown) return;
    const t = setTimeout(() => forget(show), Math.max(0, left));
    return () => clearTimeout(t);
  }, [show.key, left === null, shown]); // eslint-disable-line react-hooks/exhaustive-deps
  // The user opening a subject moves on from words about another one, and
  // from the answer given at the home.
  const was = useRef(talk);
  useEffect(() => {
    if (talk !== was.current && (leaves(show, talk) || (was.current === null && show.mode === "answer" && show.pinned))) dismiss();
    was.current = talk;
  }, [talk]); // eslint-disable-line react-hooks/exhaustive-deps
  return { show, dismiss, crowded };
}

// orbText is what the orb says, as plain words: for the screen reader
// (aria-live), and the phone's line.
export function orbText(s: Show, t: T, nodes: Record<string, GraphNode>): string {
  const title = (id: string | null) => (id ? titleOf(nodes[id], t("espace.external")) : "");
  switch (s.mode) {
    case "work":
      return s.draft ?? (s.node ? t("espace.orb.working.on", title(s.node)) : t("espace.orb.working"));
    case "failed":
      return t("espace.orb.failed");
    case "wait":
      return s.words?.text ?? t("espace.orb.wait");
    case "answer":
    case "said":
      return s.call ? t("espace.orb.waits", title(s.call)) : (s.words?.text ?? "");
  }
  return "";
}

// resetOrb forgets what was shown, and the clock (tests).
export function resetOrb(): void {
  met.clear();
  closed = null;
  last = null;
  crowded = false;
  seen = 0;
  from = null;
}
