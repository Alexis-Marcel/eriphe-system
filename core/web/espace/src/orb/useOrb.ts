import { createContext, createElement, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import type { GraphNode } from "../data/api";
import { setClosed, step, store, useGraph } from "../data/store";
import { titleOf } from "../graph/model";
import type { T } from "../i18n";
import { close, expires, leaves, orbShow, replaced, type Show } from "./model";

// What the orb shows now (orbShow), and closing it: the store's feed, what
// the user just sent, what was shown already. The first time a thing shows
// is when its while starts; at its end, or when something else happens
// (newer words, the user writes, opens another subject, closes it), it is
// remembered (the store's closed, which eriphe-core keeps with the view),
// and the next thing shows. One OrbProvider works it out, once, for the
// orb (Orb.tsx), the bar (Bar.tsx) and its line (Line.tsx), which read it
// with useOrb: what it shows changing draws only them again.

export type Orb = { show: Show; dismiss: () => void; unfold: () => void; crowded: boolean; setCrowded: (v: boolean) => void };

const met = new Map<string, number>();
const unfolded = new Set<string>(); // the words the user unfolded (« Voir plus »), by key
let last: Show | null = null; // what showed before, to know what replaced it

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

// forget remembers a thing as over: closed, or its while over.
function forget(s: Show): void {
  met.set(s.key, -Infinity); // its while is over
  step(store, setClosed, close(store.getState().closed, s));
  poke();
}
let poke = () => {}; // draws the provider again

const OrbContext = createContext<Orb | null>(null);

export function useOrb(): Orb {
  const o = useContext(OrbContext);
  if (!o) throw new Error("useOrb outside OrbProvider");
  return o;
}

export function OrbProvider({ children }: { children: ReactNode }) {
  const feed = useGraph((s) => s.orb);
  const sent = useGraph((s) => s.sent);
  const nodes = useGraph((s) => s.nodes);
  const talk = useGraph((s) => s.talk);
  const closed = useGraph((s) => s.closed);
  const [, tick] = useState(0);
  // crowded: no free spot for it out in the space (Orb.tsx): its words
  // then show as a line above the bar, as on a phone.
  const [crowded, setCrowded] = useState(false);
  useEffect(() => {
    // The tab hidden or shown again: the clock stops or goes on.
    const f = () => {
      clock();
      tick((n) => n + 1);
    };
    document.addEventListener("visibilitychange", f);
    poke = () => tick((n) => n + 1);
    return () => {
      document.removeEventListener("visibilitychange", f);
      poke = () => {};
    };
  }, []);
  const now = clock();
  const base = orbShow(feed, { now, sent, closed, met: (k) => met.get(k), nodes });
  const show = base.words?.full && unfolded.has(base.key) ? { ...base, full: true } : base;
  if (show.mode !== "rest" && !met.has(show.key)) met.set(show.key, now);
  const current = useRef(show);
  current.current = show;
  const dismiss = useCallback(() => forget(current.current), []);
  const unfold = useCallback(() => {
    unfolded.add(current.current.key);
    tick((n) => n + 1);
  }, []);

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
  const value: Orb = { show, dismiss, unfold, crowded: crowded && show.mode !== "rest", setCrowded };
  return createElement(OrbContext.Provider, { value }, children);
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
    case "answer":
      return s.words?.text ?? "";
  }
  return "";
}

// resetOrb forgets what was shown, and the clock (tests).
export function resetOrb(): void {
  met.clear();
  unfolded.clear();
  last = null;
  seen = 0;
  from = null;
}
