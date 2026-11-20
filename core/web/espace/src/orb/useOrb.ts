import { useEffect, useRef, useState } from "react";
import type { GraphNode } from "../data/api";
import { useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import type { T } from "../i18n";
import { close, expires, loadClosed, orbShow, saveClosed, type Closed, type Show } from "./model";

// useOrb is what the orb shows now (orbShow), and closing it: the store's
// feed, what the user just sent, what was shown already. The first time a
// thing shows is when its while starts; at its end, or closed, it is
// remembered (Closed), and the next thing shows.

const met = new Map<string, number>();
let closed: Closed | null = null;
const users = new Set<() => void>(); // every component that shows the orb, drawn again when it changes
const bump = () => users.forEach((f) => f());

export function useOrb(): { show: Show; dismiss: () => void } {
  const feed = useGraph((s) => s.orb);
  const sent = useGraph((s) => s.sent);
  const nodes = useGraph((s) => s.nodes);
  const call = useGraph((s) => s.call);
  const talk = useGraph((s) => s.talk);
  const [, tick] = useState(0);
  useEffect(() => {
    const f = () => tick((n) => n + 1);
    users.add(f);
    return () => void users.delete(f);
  }, []);
  closed ??= loadClosed();
  const now = Date.now();
  const show = orbShow(feed, { now, sent, closed, met: (k) => met.get(k), nodes, call });
  if (show.mode !== "rest" && !met.has(show.key)) met.set(show.key, now);

  const dismiss = () => {
    closed = close(closed!, show);
    met.set(show.key, -Infinity); // a call has nothing to keep: its while is over
    saveClosed(closed);
    bump();
  };
  // Its while over, it goes, remembered.
  const ms = expires(show);
  const left = ms === null ? null : ms - (now - (met.get(show.key) ?? now));
  useEffect(() => {
    if (left === null) return;
    const t = setTimeout(() => {
      closed = close(closed!, show);
      saveClosed(closed);
      bump();
    }, Math.max(0, left));
    return () => clearTimeout(t);
  }, [show.key, left === null]); // eslint-disable-line react-hooks/exhaustive-deps
  // Opening a subject moves on from the answer given at the home.
  const was = useRef(talk);
  useEffect(() => {
    if (was.current === null && talk !== null && show.mode === "answer" && show.pinned) dismiss();
    was.current = talk;
  }, [talk]); // eslint-disable-line react-hooks/exhaustive-deps
  return { show, dismiss };
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

// resetOrb forgets what was shown (tests).
export function resetOrb(): void {
  met.clear();
  closed = null;
}
