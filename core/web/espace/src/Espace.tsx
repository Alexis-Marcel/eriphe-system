import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import { AnimatePresence, MotionConfig, motion, useReducedMotion } from "motion/react";
import { createAdapter } from "./a2ui/adapter";
import { SAMPLE_SURFACE, galleryFixtures, gallerySurfaces, sampleLayout, sampleProposals, surfaceFixtures } from "./a2ui/fixtures";
import { coreProposals, ProposalsContext } from "./a2ui/proposals";
import { Surfaces } from "./a2ui/Surfaces";
import * as data from "./data";
import { CONVO } from "./Fallback";
import { DIMS, Grid, useNarrow, type Spot } from "./Grid";
import { useWords } from "./i18n";
import type { Layout } from "./types";

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

// focusedIn is a layout with a surface opened, at once; eriphe-core then
// recomposes the workspace around it (workspace_focus.go).
export function focusedIn(l: Layout, id: string): Layout {
  return { ...l, focus: id, say: undefined };
}

// unfocused is the layout when the focus ends: the one before it when the
// page kept it (no eriphe-core behind), else the same with nothing open
// until eriphe-core gives the one before back.
export function unfocused(l: Layout, before: Layout | null): Layout {
  return before ?? { ...l, focus: undefined, say: undefined, items: l.items.map(({ related: _, ...i }) => i) };
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
  const [layout, setLayout] = useState<Layout | null>(null);
  const [gallery] = useState(() => new URLSearchParams(location.search).has("gallery"));
  const proposals = useMemo(() => (live ? coreProposals : sampleProposals(lang)), [live, lang]);
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

  const pool = useSyncExternalStore(surfaces.subscribe, surfaces.surfaces);
  const onSurfaces = !gallery && !!layout && (pool.length > 0 || layout.items.length + layout.chips.length > 0);
  const sfocus = onSurfaces ? layout?.focus ?? null : null;
  const sfocusTitle = sfocus ? layout?.items.find((i) => i.id === sfocus)?.title ?? sfocus : null;
  const sbefore = useRef<Layout | null>(null); // the layout before the focus, with no eriphe-core behind
  const openSurface = (id: string) => {
    setSaid(null);
    sopened.current.add(id);
    sclosed.current.delete(id);
    if (boot.live) data.place("open", id).catch(() => false);
    else sbefore.current ??= layout;
    setLayout((l) => l && focusedIn(l, id));
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
    if (boot.live) data.place("back", sfocus).catch(() => false);
    else {
      const before = sbefore.current;
      sbefore.current = null;
      setLayout((l) => l && unfocused(l, before));
    }
  };
  // Nothing to show: Eriphe says so, never an empty grid without a word.
  const nothing = !gallery && !!layout && !onSurfaces;
  const text = useTyped(said ?? (layout ? layoutLine(layout) : ""));

  // A message goes to Eriphe with the surface open, if any: « montre-m'en
  // moins », « lance une recherche là-dessus ».
  const send = async (msg: string, done: string) => {
    if (!live) return say(t("espace.sent.sample"));
    say((await data.send(msg, sfocus ? "surface:" + sfocus : null).catch(() => false)) ? done : t("espace.error"));
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
    send(v, t("espace.sent"));
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
  const ambient = (onSurfaces ? layout!.ambient : undefined) ?? "calm";

  return (
    <MotionConfig reducedMotion="user">
      <div className={`espace ambient-${ambient}${wide ? " wide" : ""}${narrow ? " narrow" : ""}${narrow && sfocus ? " focused" : ""}`}>
        <aside className="eriphe-panel" aria-live="polite">
          <p className="eriphe-name">{boot.name}</p>
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
          <div className="eriphe-links">
            <a href={CONVO}>{t("espace.convo")}</a>
            {!narrow && <button className="linkish" onClick={() => setWide(!wide)} aria-pressed={wide}>{t(wide ? "espace.narrow" : "espace.wide")}</button>}
          </div>
        </aside>

        <div className="stage">
          <ProposalsContext.Provider value={proposals}>
            {onSurfaces ? (
              <Grid adapter={surfaces} layout={layout!} narrow={narrow} onOpen={openSurface} onClose={closeSurface} onPlace={placeSurface} />
            ) : gallery ? (
              <div className="surfaces">
                <Surfaces adapter={surfaces} />
              </div>
            ) : null}
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
