import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import { AnimatePresence, MotionConfig, motion, useReducedMotion } from "motion/react";
import { createAdapter } from "./a2ui/adapter";
import { Icon } from "./a2ui/icons";
import { galleryFixtures, gallerySurfaces, sampleProposals, workspaceFixtures } from "./a2ui/fixtures";
import { coreProposals, ProposalsContext } from "./a2ui/proposals";
import { Surfaces } from "./a2ui/Surfaces";
import * as data from "./data";
import { ExchangeView, landed, lastSeq, Notices, type Sent } from "./Exchange";
import { CONVO } from "./Fallback";
import { DIMS, Grid, stacked, useNarrow, type Spot } from "./Grid";
import { useWords } from "./i18n";
import { fitted, focusedIn, unfocused } from "./place";
import { startPresence } from "./presence";
import type { Layout } from "./types";

// NOTE_FOR is how long a notification routed here stays on the page.
export const NOTE_FOR = 15_000;

// The page has no control that keeps a rule or a preference: a surface is
// opened by a click, and what the user wants of it is said to Eriphe in
// words, with the open surface (data.send). The one fixed control is a
// proposal's Oui / Non.

// leftGrid is what was a tile and no longer is (folded or gone), neither
// opened nor closed by the user: on the surfaces' grid, ignored. Not while
// a surface is open, nor as it closes (wasFocus): the workspace folds what
// does not relate to it, the user did not leave those aside.
export function leftGrid(before: string[], now: Layout, opened: Set<string>, closed: Set<string>, wasFocus?: string): string[] {
  if (now.focus || wasFocus) return [];
  const tiles = new Set(now.items.map((i) => i.id));
  return before.filter((id) => !tiles.has(id) && !opened.has(id) && !closed.has(id));
}

// placedAt is a layout with a tile the user moved or resized at its new
// place, at once; eriphe-core's next layout places the others around it.
export function placedAt(l: Layout, id: string, to: Spot): Layout {
  const d = DIMS[to.size];
  return { ...l, items: l.items.map((i) => (i.id === id ? { ...i, ...to, w: d.w, h: d.h, pinned: true } : i)) };
}

// closedIn is a layout with a surface the user closed folded last, at once.
export function closedIn(l: Layout, id: string): Layout {
  const tile = l.items.find((i) => i.id === id);
  const chip = l.chips.find((c) => c.id === id);
  const title = tile?.title ?? chip?.title ?? id;
  return {
    ...l,
    focus: l.focus === id ? undefined : l.focus,
    items: l.items.filter((i) => i.id !== id),
    chips: [...l.chips.filter((c) => c.id !== id), { id, title, closed: true }],
  };
}

export { focusedIn, unfocused };

// A click answers at once: opening a surface lays it out large at once
// (focusedIn), going back gives the layout before it (unfocused), while
// eriphe-core's own layout is on its way. Until core's layout agrees, or
// for INTENT_FOR at most, the page keeps what the user asked over each
// layout core sends (shownLayout): a layout already on its way does not
// undo the click.
export const INTENT_FOR = 6_000;
export type Intent = { open: string; at: number } | { back: Layout | null; at: number };

// shownLayout is core's layout with what the user asked and core has not
// done yet; null once core agrees (or the wait is over): the intent ends.
export function shownLayout(core: Layout, intent: Intent | null, now: number): Layout | null {
  if (!intent || now - intent.at > INTENT_FOR) return null;
  if ("open" in intent) return core.focus === intent.open ? null : focusedIn(core, intent.open);
  return core.focus ? unfocused(core, intent.back) : null;
}

// layoutLine is Eriphe's line above the grid: the brain's about what is
// open, once it wrote one, else the composer's.
export function layoutLine(l: Layout): string {
  return (l.focus && l.say) || l.eriphe;
}

function useTyped(text: string) {
  const reduced = useReducedMotion();
  const [n, setN] = useState(0);
  useEffect(() => {
    if (reduced) {
      setN(text.length);
      return;
    }
    setN(0);
    const timer = setInterval(() => setN((v) => (v >= text.length ? (clearInterval(timer), v) : v + 3)), 16);
    return () => clearInterval(timer);
  }, [text, reduced]);
  return text.slice(0, n);
}

export function Espace({ boot }: { boot: data.Boot }) {
  const { t, lang } = useWords();
  const [said, setSaid] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [wide, setWide] = useState(false);
  const narrow = useNarrow();

  const say = (m: string) => {
    setToast(m);
    setTimeout(() => setToast((x) => (x === m ? null : x)), 2600);
  };
  const live = boot.live;

  // The A2UI surfaces and their layout, streamed by eriphe-core, which
  // decides where each goes (workspace_layout.go); with none behind (the
  // dev server), a made-up one shows what the path does; with ?gallery,
  // every component of the catalog, one under the other.
  const [surfaces] = useState(() => createAdapter(lang));
  // layout is core's (or the sample's); what is drawn is it with the
  // user's intent over it, fitted to the tiles' content (place.ts).
  const [layout, setLayout] = useState<Layout | null>(null);
  const [intent, setIntent] = useState<Intent | null>(null);
  const [needs, setNeeds] = useState<Record<string, number>>({});
  const onNeed = useCallback((id: string, rows: number) => setNeeds((n) => (n[id] === rows ? n : { ...n, [id]: rows })), []);
  const [gallery] = useState(() => new URLSearchParams(location.search).has("gallery"));
  const proposals = useMemo(() => (live ? coreProposals : sampleProposals(lang)), [live, lang]);

  // The conversation's last exchange (Exchange.tsx): folded when the page
  // opens, unfolded when the user writes and when Eriphe answers.
  const [exchange, setExchange] = useState<data.Exchange | null>(null);
  const [sent, setSent] = useState<Sent | null>(null);
  const [exOpen, setExOpen] = useState(false);
  const exSeen = useRef<number | null>(null); // the exchange's last seq, once the first one came
  const onExchange = useCallback((x: data.Exchange) => {
    setExchange(x);
    const seq = lastSeq(x);
    if (exSeen.current !== null && (seq > exSeen.current || x.draft)) setExOpen(true);
    exSeen.current = Math.max(exSeen.current ?? 0, seq);
  }, []);
  useEffect(() => {
    if (landed(exchange, sent)) setSent(null);
  }, [exchange, sent]);

  // The notifications routed to this tab, by the stream or the answer to
  // its presence: a notice each, for a while.
  const [notes, setNotes] = useState<data.Note[]>([]);
  const dropNote = useCallback((id: number) => setNotes((ns) => ns.filter((n) => n.id !== id)), []);
  const addNotes = useCallback(
    (ns: data.Note[]) => {
      setNotes((old) => [...old, ...ns.filter((n) => !old.some((o) => o.id === n.id))].slice(-4));
      for (const n of ns) setTimeout(() => dropNote(n.id), NOTE_FOR);
    },
    [dropNote],
  );
  useEffect(() => {
    if (!boot.live || gallery) return;
    return startPresence({ tab: data.tab, csrf: () => boot.csrf, onNotes: addNotes });
  }, [boot, gallery, addNotes]);

  useEffect(() => {
    if (boot.live && !gallery) return data.stream(boot, surfaces, setLayout, { onExchange, onNotes: addNotes });
    const sample = workspaceFixtures(lang);
    surfaces.applyMessages(gallery ? galleryFixtures(lang) : sample.messages);
    if (!gallery) setLayout(sample.layout);
    const off = surfaces.onAction(() => say(t("espace.sent.sample")));
    return () => {
      off();
      const ids = gallery ? gallerySurfaces(lang) : sample.ids;
      surfaces.applyMessages(ids.map((surfaceId) => ({ version: "1.0" as const, deleteSurface: { surfaceId } })));
    };
  }, [boot, surfaces, lang, gallery, onExchange, addNotes]);

  const pool = useSyncExternalStore(surfaces.subscribe, surfaces.surfaces);
  // The intent ends once core's layout does what it asked, or after a while.
  const over = layout && intent ? shownLayout(layout, intent, Date.now()) : null;
  useEffect(() => {
    if (intent && layout && !shownLayout(layout, intent, Date.now())) setIntent(null);
  }, [layout, intent]);
  useEffect(() => {
    if (!intent) return;
    const timer = setTimeout(() => setIntent((i) => (i === intent ? null : i)), INTENT_FOR + 50);
    return () => clearTimeout(timer);
  }, [intent]);
  const placed = over ?? layout;
  const shown = useMemo(() => (placed && !narrow ? fitted(placed, needs) : placed), [placed, needs, narrow]);
  const onSurfaces = !gallery && !!shown && (pool.length > 0 || shown.items.length + shown.chips.length > 0);
  const sfocus = onSurfaces ? shown?.focus ?? null : null;
  const sfocusTitle = sfocus ? shown?.items.find((i) => i.id === sfocus)?.title ?? sfocus : null;
  // On a phone the screen is the open surface's once it is drawn alone (stacked), not before:
  // a chip opened waits in the column until core places it.
  const full = narrow && onSurfaces && stacked(shown!, new Set(pool.map((s) => s.id))).full;
  const sbefore = useRef<Layout | null>(null); // the layout before the focus
  const openSurface = (id: string) => {
    setSaid(null);
    sopened.current.add(id);
    sclosed.current.delete(id);
    if (!placed?.focus) sbefore.current = placed;
    if (!boot.live) return setLayout((l) => l && focusedIn(l, id));
    const mine: Intent = { open: id, at: Date.now() };
    setIntent(mine);
    data.place("open", id).then(
      (ok) => ok || failed(mine),
      () => failed(mine),
    );
  };
  // What core refused or never got: the page goes back to core's layout, and says so.
  const failed = (mine: Intent) => {
    setIntent((i) => (i === mine ? null : i));
    say(t("espace.error"));
  };
  // The user arranges the grid: shown at once, kept by eriphe-core for the
  // session (workspace_gestures.go).
  const closeSurface = (id: string) => {
    sclosed.current.add(id);
    setLayout((l) => l && closedIn(l, id));
    if (boot.live) data.gesture("closed", "surface:" + id).catch(() => false);
  };
  const placeSurface = (id: string, what: "moved" | "resized", to: Spot) => {
    setLayout((l) => l && placedAt(l, id, to));
    if (boot.live) data.gesture(what, "surface:" + id, to).catch(() => false);
  };
  // A tile that left the grid unopened, while the page was seen, is
  // ignored: a gesture for the composer and memory.
  const sopened = useRef(new Set<string>());
  const sclosed = useRef(new Set<string>());
  const stiles = useRef<string[]>([]);
  const sfocusWas = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!live || !onSurfaces || !layout) return;
    for (const id of leftGrid(stiles.current, layout, sopened.current, sclosed.current, sfocusWas.current)) {
      data.gesture("ignored", "surface:" + id).catch(() => false);
    }
    stiles.current = document.visibilityState === "visible" ? layout.items.map((i) => i.id) : [];
    sfocusWas.current = layout.focus;
  }, [layout, live, onSurfaces]);
  const backSurface = () => {
    if (!sfocus) return;
    const before = sbefore.current;
    sbefore.current = null;
    if (!boot.live) return setLayout((l) => l && unfocused(l, before));
    const mine: Intent = { back: before, at: Date.now() };
    setIntent(mine);
    data.place("back", sfocus).then(
      (ok) => ok || failed(mine),
      () => failed(mine),
    );
  };
  // Nothing to show: Eriphe says so, never an empty grid without a word.
  const nothing = !gallery && !!shown && !onSurfaces;
  const text = useTyped(said ?? (shown ? layoutLine(shown) : ""));

  // A message goes to Eriphe with the surface open, if any: « montre-m'en
  // moins », « lance une recherche là-dessus ». It shows in the exchange at
  // once, Eriphe's answer under it as it comes.
  const send = async (msg: string) => {
    if (!live) return say(t("espace.sent.sample"));
    const mine: Sent = { text: msg, after: lastSeq(exchange) };
    setSent(mine);
    setExOpen(true);
    if (!(await data.send(msg, sfocus ? "surface:" + sfocus : null).catch(() => false))) {
      setSent((s) => (s === mine ? null : s));
      say(t("espace.error"));
    }
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (["INPUT", "TEXTAREA", "SELECT"].includes((e.target as HTMLElement).tagName)) return;
      if (e.key === "Escape") backSurface();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const onSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const input = e.currentTarget.elements.namedItem("text") as HTMLInputElement;
    const v = input.value.trim();
    if (!v) return;
    input.value = "";
    send(v);
  };

  // The event stream down too long: the conversation is offered, the
  // workspace stays as it was.
  const [down, setDown] = useState(false);
  useEffect(() => {
    if (!live || gallery) return;
    const tick = setInterval(() => setDown(data.streamDown(data.streamHealth(), Date.now())), 5000);
    return () => clearInterval(tick);
  }, [live, gallery]);

  const hint = sfocusTitle ? t("espace.input.about", sfocusTitle) : t("espace.input");
  const ambient = (onSurfaces ? shown!.ambient : undefined) ?? "calm";

  return (
    <MotionConfig reducedMotion="user">
      <div className={`espace ambient-${ambient}${wide ? " wide" : ""}${narrow ? " narrow" : ""}${full ? " focused" : ""}`}>
        <aside className="eriphe-panel" aria-live="polite">
          <div className="eriphe-head">
            <p className="eriphe-name">{boot.name}</p>
            <a className="eriphe-settings" href="/settings" title={t("espace.settings")} aria-label={t("espace.settings")}>
              <Icon name="settings" />
            </a>
          </div>
          <div className="eriphe-text">{text && <p>{text}</p>}</div>
          {live && nothing && <p className="eriphe-sample quiet">{t("espace.empty")}</p>}
          {!live && <p className="eriphe-sample">{t("espace.sample")}</p>}
          {down && (
            <p className="eriphe-sample eriphe-offline" role="alert">
              {t("espace.offline")} <a href={CONVO}>{t("espace.convo")}</a>
            </p>
          )}
          <div className="eriphe-actions">
            {sfocus && <button className="btn ghost" onClick={backSurface}>{t("espace.back")}</button>}
          </div>
          <form className="eriphe-input" onSubmit={onSubmit}>
            <input name="text" autoComplete="off" placeholder={hint} aria-label={hint} />
          </form>
          <ExchangeView x={exchange} sent={sent} open={exOpen} onToggle={() => setExOpen(!exOpen)} />
          <div className="eriphe-links">
            <a href={CONVO}>{t("espace.convo")}</a>
            {!narrow && <button className="linkish" onClick={() => setWide(!wide)} aria-pressed={wide}>{t(wide ? "espace.narrow" : "espace.wide")}</button>}
          </div>
        </aside>

        <div className="stage">
          <ProposalsContext.Provider value={proposals}>
            {onSurfaces ? (
              <Grid adapter={surfaces} layout={shown!} narrow={narrow} onOpen={openSurface} onClose={closeSurface} onPlace={placeSurface} onNeed={onNeed} />
            ) : gallery ? (
              <div className="surfaces">
                <Surfaces adapter={surfaces} />
              </div>
            ) : null}
          </ProposalsContext.Provider>
        </div>

        <Notices notes={notes} onClose={dropNote} />
        <AnimatePresence>
          {toast && (
            <motion.p key={toast} className="toast" role="status" initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}>
              {toast}
            </motion.p>
          )}
        </AnimatePresence>
      </div>
    </MotionConfig>
  );
}
