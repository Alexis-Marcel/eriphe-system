import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import { AnimatePresence, LayoutGroup, MotionConfig, motion, useReducedMotion } from "motion/react";
import { createAdapter } from "./a2ui/adapter";
import { SAMPLE_SURFACE, galleryFixtures, gallerySurfaces, sampleLayout, sampleProposals, surfaceFixtures } from "./a2ui/fixtures";
import { coreProposals, ProposalsContext } from "./a2ui/proposals";
import { Surfaces } from "./a2ui/Surfaces";
import * as data from "./data";
import { place, type Placed } from "./engine";
import { DIMS, Grid, type Spot } from "./Grid";
import { useWords } from "./i18n";
import type { Composition, Layout, Obj, Ref } from "./types";
import { View, YesNo, lineOf, sourceOf, titleOf, type Act } from "./views";

const EMPTY: Composition = { mode: "side", eriphe: "", items: [] };

const spring = { type: "spring", stiffness: 260, damping: 30 } as const;

// The page has no control that keeps a rule or a preference: a module is
// opened by a click, and what the user wants of it is said to Eriphe in
// words, with the open module's ref (data.send). The walkthrough is
// something the user asks Eriphe for (workspace_walkthrough). The one fixed
// control is a card's Oui / Non.

// tourOf is what a walkthrough goes through when Eriphe names nothing: the
// modules on the screen that matter, the most important first.
export function tourOf(placed: Placed[]): Ref[] {
  return placed.filter((p) => p.weight >= 2 || p.size !== "s").map((p) => p.obj.ref);
}

// leftUnopened is what was in front and has left the composition (neither
// module nor chip now) without the user opening it: a gesture memory's
// extraction may read.
export function leftUnopened(before: Ref[], comp: Composition, opened: Set<Ref>): Ref[] {
  const now = new Set([...comp.items.map((i) => i.ref), ...(comp.chips ?? [])]);
  return before.filter((r) => !now.has(r) && !opened.has(r));
}

// leftGrid is what was a tile and no longer is (folded or gone), neither
// opened nor closed by the user: on the surfaces' grid, ignored.
export function leftGrid(before: string[], now: Layout, opened: Set<string>, closed: Set<string>): string[] {
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
  const [snap, setSnap] = useState<data.Snapshot | null>(null);
  const [failed, setFailed] = useState(false);
  const [focus, setFocus] = useState<Ref | null>(null);
  const [decided, setDecided] = useState<Record<Ref, string>>({}); // cards answered here
  const [said, setSaid] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [tour, setTour] = useState<{ refs: Ref[]; i: number } | null>(null);
  const [wide, setWide] = useState(false);

  const reload = useCallback(() => {
    data.load(boot).then((s) => { setSnap(s); setFailed(false); }, () => setFailed(true));
  }, [boot]);
  useEffect(reload, [reload]);
  // A walkthrough Eriphe starts reaches the page on the event stream; the
  // handler is the latest render's.
  const onWalk = useRef<(w: data.Walk) => void>(() => {});
  useEffect(() => (snap && !snap.sample ? data.follow(reload, (w) => onWalk.current(w)) : undefined), [snap?.sample, reload]);

  const store = useMemo(() => {
    const m = new Map<Ref, Obj>();
    for (const [ref, o] of snap?.store ?? []) m.set(ref, o.card && decided[ref] ? { ...o, card: { ...o.card, state: decided[ref] } } : o);
    return m;
  }, [snap, decided]);
  const all = useMemo(() => [...store.values()], [store]);

  const comp = snap?.comp ?? EMPTY;
  const { placed, chips } = useMemo(
    () => place(comp, store, focus),
    [comp, store, focus],
  );
  const shownChips = chips.filter((c) => !(c.obj.card && c.obj.card.state !== "pending"));

  const fp = focus ? placed.find((p) => p.focus) : undefined;
  const focusSay = fp ? fp.say ?? `${t("espace.say.lead", titleOf(t, lang, fp.obj))}${fp.why ? ` ${fp.why}.` : ""}` : null;

  const say = (m: string) => {
    setToast(m);
    setTimeout(() => setToast((x) => (x === m ? null : x)), 2600);
  };
  const live = !!snap && !snap.sample && boot.live;

  // The A2UI surfaces and their layout, streamed by eriphe-core, which
  // decides where each goes (workspace_layout.go); with none behind (the
  // dev server), a made-up one shows what the path does; with ?gallery,
  // every component of the catalog, one under the other.
  const [surfaces] = useState(() => createAdapter(lang));
  const [layout, setLayout] = useState<Layout | null>(null);
  const [gallery] = useState(() => new URLSearchParams(location.search).has("gallery"));
  const proposals = useMemo(() => (snap?.sample ? sampleProposals(lang) : coreProposals), [snap?.sample, lang]);
  useEffect(() => {
    if (boot.live && !gallery) return data.stream(boot, surfaces, setLayout);
    surfaces.applyMessages(gallery ? galleryFixtures(lang) : surfaceFixtures(lang));
    if (!gallery) setLayout(sampleLayout(lang));
    const off = surfaces.onAction(() => say(t("espace.sent.sample")));
    return () => {
      off();
      const ids = gallery ? gallerySurfaces(lang) : [SAMPLE_SURFACE];
      surfaces.applyMessages(ids.map((surfaceId) => ({ version: "1.0" as const, deleteSurface: { surfaceId } })));
    };
  }, [boot, surfaces, lang, gallery]);

  // The surfaces are the workspace once there are any: the per-kind canvas
  // below stays only for the composition by ref, until it goes (#424).
  const pool = useSyncExternalStore(surfaces.subscribe, surfaces.surfaces);
  const onSurfaces = !gallery && !!layout && (pool.length > 0 || layout.items.length + layout.chips.length > 0);
  const sfocus = onSurfaces ? layout?.focus ?? null : null;
  const sfocusTitle = sfocus ? layout?.items.find((i) => i.id === sfocus)?.title ?? sfocus : null;
  const openSurface = (id: string) => {
    setSaid(null);
    sopened.current.add(id);
    sclosed.current.delete(id);
    if (boot.live) data.place("open", id).catch(() => false);
    else setLayout((l) => l && { ...l, focus: id });
  };
  // The user arranges the grid: shown at once, kept by eriphe-core for the
  // session (workspace_gestures.go).
  const closeSurface = (id: string) => {
    sclosed.current.add(id);
    setLayout((l) => l && closedIn(l, id));
    if (boot.live) data.gesture("closed", "surface:" + id, "").catch(() => false);
  };
  const placeSurface = (id: string, what: "moved" | "resized", to: Spot) => {
    setLayout((l) => l && placedAt(l, id, to));
    if (boot.live) data.gesture(what, "surface:" + id, "", to).catch(() => false);
  };
  // A tile that left the grid unopened, while the page was seen, is
  // ignored: a gesture for the composer and memory.
  const sopened = useRef(new Set<string>());
  const sclosed = useRef(new Set<string>());
  const stiles = useRef<string[]>([]);
  useEffect(() => {
    if (!live || !onSurfaces || !layout) return;
    for (const id of leftGrid(stiles.current, layout, sopened.current, sclosed.current)) {
      data.gesture("ignored", "surface:" + id, "").catch(() => false);
    }
    stiles.current = document.visibilityState === "visible" ? layout.items.map((i) => i.id) : [];
  }, [layout, live, onSurfaces]);
  const backSurface = () => {
    if (!sfocus) return;
    if (boot.live) data.place("back", sfocus).catch(() => false);
    else setLayout((l) => l && { ...l, focus: undefined });
  };
  const nothing = !onSurfaces && placed.length === 0 && shownChips.length === 0;
  const line = onSurfaces ? layout!.eriphe : comp.eriphe;
  const text = useTyped(failed ? t("espace.error") : !snap ? "" : said ?? focusSay ?? line);

  // What the user opens, and what left the screen unopened, are gestures
  // eriphe-core keeps for memory's extraction: no counter here.
  const opened = useRef(new Set<Ref>());
  const front = useRef<Ref[]>([]);
  useEffect(() => {
    if (!live || !snap || onSurfaces) return; // the old canvas's only: a surface's opening is its open action
    for (const ref of leftUnopened(front.current, snap.comp, opened.current)) {
      const o = snap.store.get(ref);
      data.gesture("ignored", ref, o?.title ?? "").catch(() => false);
    }
    // Only what was in front while the page was seen counts.
    front.current = document.visibilityState === "visible" ? snap.comp.items.map((i) => i.ref) : [];
  }, [snap, live, onSurfaces]);

  // open focuses a module; byUser says the user did (not a walkthrough).
  const open = (ref: Ref | null, byUser = true) => {
    setFocus(ref);
    setSaid(null);
    if (ref && byUser && live && !opened.current.has(ref)) {
      opened.current.add(ref);
      data.gesture("opened", ref, store.get(ref)?.title ?? "").catch(() => false);
    }
  };

  // A walkthrough: Eriphe goes through refs one module at a time, as the
  // user asked it (workspace_walkthrough).
  const startTour = (refs: Ref[], has: (r: Ref) => boolean) => {
    const go = refs.filter(has);
    if (!go.length) return;
    setTour({ refs: go, i: 0 });
    open(go[0], false);
  };
  onWalk.current = async (w) => {
    if (onSurfaces) return; // a walkthrough goes through the old canvas's modules, hidden behind the surfaces
    const refs = w.refs ?? [];
    if (!refs.length) return startTour(tourOf(placed), () => true);
    const missing = refs.filter((r) => !store.has(r));
    let has = (r: Ref) => store.has(r);
    if (missing.length) {
      const s = await data.load(boot, missing).catch(() => null);
      if (s) {
        setSnap(s);
        has = (r) => s.store.has(r);
      }
    }
    startTour(refs, has);
  };
  const step = (d: number) => {
    if (!tour) return;
    const i = tour.i + d;
    if (i < 0) return;
    if (i >= tour.refs.length) {
      setTour(null);
      open(null);
      setSaid(t("espace.tour.end"));
      return;
    }
    setTour({ ...tour, i });
    open(tour.refs[i], false);
  };
  const stopTour = () => {
    setTour(null);
    open(null);
  };

  // A message goes to Eriphe with the module open, if any: « montre-m'en
  // moins », « lance une recherche là-dessus ».
  const send = async (msg: string, done: string) => {
    if (!live) return say(t("espace.sent.sample"));
    say((await data.send(msg, sfocus ? "surface:" + sfocus : focus).catch(() => false)) ? done : t("espace.error"));
  };

  const act: Act = {
    open,
    answer: async (ref, yes) => {
      if (!snap?.sample && !(await data.answer(ref, yes).catch(() => false))) return say(t("espace.error"));
      setDecided((d) => ({ ...d, [ref]: yes ? "approved" : "rejected" }));
      say(t(yes ? "espace.card.did.yes" : "espace.card.did.no"));
    },
    ask: (msg) => send(msg, t("espace.ask.sent")),
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (["INPUT", "TEXTAREA", "SELECT"].includes((e.target as HTMLElement).tagName)) return;
      if (e.key === "Escape") {
        if (tour) stopTour();
        else if (sfocus) backSurface();
        else open(null);
      }
      if (tour && e.key === "ArrowRight") step(1);
      if (tour && e.key === "ArrowLeft") step(-1);
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
    send(v, t("espace.sent"));
  };

  const fo = focus ? store.get(focus) : undefined;
  const hint = sfocusTitle ? t("espace.input.about", sfocusTitle) : fo ? t("espace.input.about", titleOf(t, lang, fo)) : t("espace.input");
  const mode = focus || onSurfaces ? "side" : comp.mode;
  const ambient = (onSurfaces ? layout!.ambient : comp.ambient) ?? "calm";

  return (
    <MotionConfig reducedMotion="user">
      <div className={`espace ambient-${ambient} mode-${mode} ${wide ? "wide" : ""}`}>
        <aside className="eriphe-panel" aria-live="polite">
          <p className="eriphe-name">{boot.name}</p>
          <div className="eriphe-text">{text && <p>{text}</p>}</div>
          {snap && !snap.sample && nothing && <p className="eriphe-sample quiet">{t("espace.empty")}</p>}
          {snap?.sample && <p className="eriphe-sample">{t("espace.sample")}</p>}
          <div className="eriphe-actions">
            {tour ? (
              <>
                <button className="btn ghost" onClick={() => step(1)}>
                  {tour.i + 1 < tour.refs.length ? t("espace.next", tour.i + 1, tour.refs.length) : t("espace.finish")}
                </button>
                <button className="btn ghost" onClick={stopTour}>{t("espace.stop")}</button>
              </>
            ) : sfocus ? (
              <button className="btn ghost" onClick={backSurface}>{t("espace.back")}</button>
            ) : focus ? (
              <button className="btn ghost" onClick={() => open(null)}>{t("espace.back")}</button>
            ) : null}
          </div>
          <form className="eriphe-input" onSubmit={onSubmit}>
            <input name="text" autoComplete="off" placeholder={hint} aria-label={hint} />
          </form>
          <div className="eriphe-links">
            <a href="/">{t("espace.convo")}</a>
            <button className="linkish" onClick={() => setWide(!wide)} aria-pressed={wide}>{t(wide ? "espace.narrow" : "espace.wide")}</button>
          </div>
        </aside>

        <div className="stage">
          <ProposalsContext.Provider value={proposals}>
            {onSurfaces ? (
              <Grid adapter={surfaces} layout={layout!} onOpen={openSurface} onClose={closeSurface} onPlace={placeSurface} />
            ) : (
              <>
              <div className="chips" role="list" aria-label={t("espace.chips")}>
                <AnimatePresence>
                  {shownChips.map((c) => (
                    <motion.div key={c.obj.ref} layout role="listitem" className={`chip from-${c.from}`}
                      initial={{ opacity: 0, y: -12 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -12 }} transition={spring}>
                      <button className="chip-open" onClick={() => open(c.obj.ref)}>
                        <span className="chip-text">{lineOf(c.obj)}</span>
                        <span className="chip-from">{[sourceOf(c.obj), t(c.from === "overflow" ? "espace.chip.overflow" : "espace.chip.composition")].filter(Boolean).join(", ")}</span>
                      </button>
                      {c.obj.card?.state === "pending" && <YesNo ref_={c.obj.ref} act={act} />}
                    </motion.div>
                  ))}
                </AnimatePresence>
              </div>

                {gallery && (
                  <div className="surfaces">
                    <Surfaces adapter={surfaces} />
                  </div>
                )}

              <main className="canvas" aria-label={t("espace.canvas")}>
                <LayoutGroup>
                  {/* Not mode="popLayout": it injects a <style> element, which the CSP refuses.
                      A module leaving keeps its grid cells while it fades, so nothing jumps. */}
                  <AnimatePresence>
                    {placed.map((p) => (
                      <motion.article
                        key={p.obj.ref}
                        layout
                        transition={spring}
                        initial={{ opacity: 0, scale: 0.94 }}
                        animate={{ opacity: 1, scale: 1 }}
                        exit={{ opacity: 0, scale: 0.92 }}
                        className={`mod size-${p.size} at-c${p.col} at-r${p.row} w-${p.w} h-${p.h} type-${p.obj.kind}${p.focus ? " focus" : ""}${p.related ? " related" : ""}`}
                        onClick={(e) => {
                          if (!p.focus && !(e.target as HTMLElement).closest("button,input,select,a")) open(p.obj.ref);
                        }}
                      >
                        <div className="mod-body">
                          <View o={p.obj} size={p.size} full={p.focus} all={all} act={act} />
                        </div>
                        {p.size !== "s" && <p className="why">{p.why || (p.focus ? t("espace.opened") : p.related ? t("espace.related") : "")}</p>}
                      </motion.article>
                    ))}
                  </AnimatePresence>
                </LayoutGroup>
              </main>
              </>
            )}
          </ProposalsContext.Provider>
        </div>

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

