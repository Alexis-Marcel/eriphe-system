import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { AnimatePresence, LayoutGroup, motion } from "motion/react";
import type { Adapter } from "./a2ui/adapter";
import { OnStage } from "./a2ui/code";
import { SurfaceView } from "./a2ui/Surfaces";
import { chipLabel, laidOut, needsUser, shownChips, surfaceNew, surfaceState, surfaceSummary, surfaceTitle, surfaceUpdated, type TileState } from "./chips";
import { useWords } from "./i18n";
import { COLS, DIMS, ROWS, SIZES, foldOut, needRows } from "./place";
import type { FoldedChip, Layout, Tile, TileSize } from "./types";

// The workspace's surfaces, where eriphe-core lays them out (workspace_layout.go):
// each tile at its place on the fixed 12 × 8 grid, by class (.at-cN, .at-rN,
// .w-N, .h-N, .size-*), never a style attribute; Motion moves a tile that
// changed place, a tile enters by CSS. A small tile (s) of a surface that
// has a one-line summary (a thread's) shows that line, a chip shows it
// beside its title. What is folded waits as a chip in the
// strip above; a surface the layout does not name yet (it comes right after
// the surface) waits there too, so nothing is unseen. Opening a tile or a
// chip asks eriphe-core to place it first (onOpen).
//
// The user arranges the tiles with the three small buttons of each
// (workspace_gestures.go): the grip moves it (dragged, or with the arrow
// keys), the size button changes its size (a click, + or −, the arrow
// keys), the cross closes it for the session. eriphe-core keeps a move or a
// resize as the tile's place for the session (onPlace) and a close as not
// shown again (onClose); the page shows it at once, core's next layout
// follows.
//
// A tile on the grid never scrolls and never clips a sentence: the page
// measures each one's content at its width (onNeed, in rows) and lays it
// out taller where there is room (place.ts, fitted); one that still has
// too little room shows its first blocks whole and says the rest is
// there, open (foldOut). Only the surface open scrolls: it is the
// workspace within the workspace.
//
// A surface open re-lays the workspace out (focus mode, on a wide screen):
// it large in the centre, a working area that scrolls; everything else in
// a column at the side (sideOf), folded: a state chip, its title, its one
// line, whatever its state; still live, and a click on one opens it
// instead. No dim, no blur: the others are smaller, not hidden. What needs
// the user (urgent: a card a task waits on, an alert) comes first in the
// column, its state saying so; what arrives or changes while the focus
// lasts says « nouveau », and pulses when it needs the user. It never takes the centre: only the user (or Eriphe, asked)
// moves the focus. Back (Esc, the panel's button, the centre's arrow)
// gives the grid again. Each surface keeps its Motion layoutId from the
// grid to the centre or the column and back: the re-layout is animated.
// Core's places on the grid around the focus are not used on a wide screen.
//
// On a phone (narrow) the grid does not hold: one column, the tiles in
// eriphe-core's order, the chips after them, only the cross on a tile; a
// surface open is drawn alone, on the whole screen (stacked).

const spring = { type: "spring", stiffness: 260, damping: 30 } as const;

const interactive = "button,input,select,textarea,a,[role=button],[role=tab]";

export { COLS, DIMS, ROWS, SIZES };

// A tile's place as the user gives it: a size at a column and a row.
export type Spot = { size: TileSize; col: number; row: number };

// fit keeps a spot on the grid: the tile moves back in, at its size.
export function fit(size: TileSize, col: number, row: number): Spot {
  const d = DIMS[size];
  return { size, col: Math.min(Math.max(col, 0), COLS - d.w), row: Math.min(Math.max(row, 0), ROWS - d.h) };
}

// moved is a tile moved by a number of cells; null when it cannot move so.
export function moved(t: Tile, dc: number, dr: number): Spot | null {
  const s = fit(t.size, t.col + dc, t.row + dr);
  return s.col === t.col && s.row === t.row ? null : s;
}

// resized is a tile a size bigger (1) or smaller (-1), at its place, moved
// back in when it no longer fits; wrap goes from xl back to s (a click).
export function resized(t: Tile, by: 1 | -1, wrap = false): Spot | null {
  let i = SIZES.indexOf(t.size) + by;
  if (wrap && i >= SIZES.length) i = 0;
  if (i < 0 || i >= SIZES.length) return null;
  return fit(SIZES[i], t.col, t.row);
}

// cellAt is the grid's cell under a point of the canvas.
export function cellAt(r: { left: number; top: number; width: number; height: number }, x: number, y: number) {
  return {
    col: Math.min(Math.max(Math.floor(((x - r.left) / Math.max(r.width, 1)) * COLS), 0), COLS - 1),
    row: Math.min(Math.max(Math.floor(((y - r.top) / Math.max(r.height, 1)) * ROWS), 0), ROWS - 1),
  };
}

// A narrow screen (a phone): the page scrolls, the grid does not hold. The
// same width as espace.css's.
export const NARROW = "(max-width: 900px)";

function narrowQuery(): MediaQueryList | null {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" ? window.matchMedia(NARROW) : null;
}

// useNarrow tells whether the screen is narrow, and follows it.
export function useNarrow(): boolean {
  return useSyncExternalStore(
    (on) => {
      const q = narrowQuery();
      q?.addEventListener("change", on);
      return () => q?.removeEventListener("change", on);
    },
    () => narrowQuery()?.matches ?? false,
  );
}

// What the grid draws: the tiles whose surface is there, and the chips:
// what the layout folds, then the surfaces it does not name yet.
export function drawn(layout: Layout, have: Set<string>): { tiles: Tile[]; chips: FoldedChip[] } {
  const named = new Set([...layout.items.map((i) => i.id), ...layout.chips.map((c) => c.id)]);
  return {
    tiles: layout.items.filter((i) => have.has(i.id)),
    chips: [...layout.chips.filter((c) => have.has(c.id)), ...[...have].filter((id) => !named.has(id)).map((id) => ({ id, title: id }))],
  };
}

// stacked is the workspace on a phone: one column, the tiles in the
// eriphe-core's order (the layout's items, never their place on the grid),
// the chips after them; with a surface open, that surface alone, on the
// whole screen.
export function stacked(layout: Layout, have: Set<string>): { tiles: Tile[]; chips: FoldedChip[]; full: boolean } {
  const { tiles, chips } = drawn(layout, have);
  const open = layout.focus ? tiles.find((i) => i.id === layout.focus) : undefined;
  return open ? { tiles: [open], chips: [], full: true } : { tiles, chips, full: false };
}

// A surface in the focus mode's column. needs: it needs the user (urgent,
// by the layout or by its weight): first in the column, one line as all.
export type SideItem = { id: string; title: string; why?: string; needs: boolean; related?: boolean; closed?: boolean };

// sideOf is the column beside the surface open: what the grid would draw
// but it, the tiles in core's order then the chips, the closed last; what
// needs the user first of all.
export function sideOf(layout: Layout, have: Set<string>, state: (id: string) => TileState = () => "quiet"): SideItem[] {
  const { tiles, chips } = drawn(layout, have);
  const all: SideItem[] = [
    ...tiles
      .filter((i) => i.id !== layout.focus)
      .map((i) => ({ id: i.id, title: i.title, why: i.why, needs: !!i.urgent || needsUser(state(i.id)), related: i.related })),
    ...chips
      .filter((c) => c.id !== layout.focus)
      .map((c) => ({ id: c.id, title: c.title, why: c.why, needs: !c.closed && needsUser(state(c.id)), closed: c.closed })),
  ];
  return [...all.filter((i) => i.needs), ...all.filter((i) => !i.needs && !i.closed), ...all.filter((i) => i.closed)];
}

type Props = {
  adapter: Adapter;
  layout: Layout;
  narrow?: boolean; // a phone: one column (stacked)
  onOpen: (id: string) => void;
  onClose?: (id: string) => void;
  onPlace?: (id: string, what: "moved" | "resized", to: Spot) => void;
  onNeed?: (id: string, rows: number) => void; // a tile's content needs this many rows
  onBack?: () => void; // back to the overview, from the surface open
};

// measureTiles measures every tile of the canvas but the one open: the rows
// its content needs (onNeed), and, when it has too little room for now,
// which blocks it hides (foldOut, the tile marked .cut; .clip when one
// block alone is still too tall). What it hid
// before is shown again first, so the content is measured whole.
export function measureTiles(canvas: HTMLElement, onNeed: (id: string, rows: number) => void) {
  const style = getComputedStyle(canvas);
  const gap = parseFloat(style.rowGap) || 0;
  const cell = (canvas.clientHeight - gap * (ROWS - 1)) / ROWS;
  for (const tile of Array.from(canvas.querySelectorAll<HTMLElement>("article.tile"))) {
    const body = tile.querySelector<HTMLElement>(".mod-body");
    const fit = tile.querySelector<HTMLElement>(".mod-fit");
    const id = tile.dataset.surface;
    if (!body || !fit || !id) continue;
    for (const el of Array.from(fit.querySelectorAll(".fit-out"))) el.classList.remove("fit-out");
    if (tile.classList.contains("focus")) {
      tile.classList.remove("cut", "clip");
      continue;
    }
    const natural = fit.offsetHeight;
    if (cell > 0 && natural > 0) onNeed(id, needRows(tile.clientHeight, body.clientHeight, natural, cell, gap));
    tile.classList.toggle("cut", natural > body.clientHeight + 1 && foldOut(fit, body.clientHeight));
    // One block alone taller than the tile (a day, a chart): it fades out at the foot.
    tile.classList.toggle("clip", fit.offsetHeight > body.clientHeight + 1);
  }
}

export function Grid({ adapter, layout, narrow = false, onOpen, onClose = () => {}, onPlace = () => {}, onNeed = () => {}, onBack = () => {} }: Props) {
  const { t } = useWords();
  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  const byID = new Map(surfaces.map((s) => [s.id, s]));
  // A surface that opens inside another is never a tile nor a chip: only in it, open.
  const have = laidOut(surfaces);
  const { tiles, chips, full } = narrow ? stacked(layout, have) : { ...drawn(layout, have), full: false };
  // The focus mode: a wide screen, and the surface open is drawn.
  const center = !narrow && layout.focus ? tiles.find((i) => i.id === layout.focus) : undefined;

  // What arrived or changed since the focus began: each surface as it was
  // then (surfaceUpdated), the one open aside. A new focus has seen all.
  const since = useRef<{ focus?: string; was: Map<string, string>; none?: boolean }>({ was: new Map(), none: true });
  // Taken again once the surfaces come, when the layout came first.
  if (since.current.focus !== layout.focus || (since.current.none && surfaces.length > 0)) {
    since.current = {
      focus: layout.focus,
      was: new Map(surfaces.filter((s) => s.id !== layout.focus).map((s) => [s.id, surfaceUpdated(s)])),
      none: surfaces.length === 0,
    };
  }
  const fresh = (id: string) => since.current.was.get(id) !== surfaceUpdated(byID.get(id));

  // A drag of a tile's grip: the cell it would go to, shown by a ghost.
  const canvas = useRef<HTMLElement>(null);
  const [drag, setDrag] = useState<({ id: string } & Spot) | null>(null);
  const dragOff = useRef<(() => void) | null>(null);
  useEffect(() => () => dragOff.current?.(), []);
  const startDrag = (tile: Tile) => (e: PointerEvent<HTMLButtonElement>) => {
    const box = canvas.current?.getBoundingClientRect();
    if (!box || e.button !== 0) return;
    e.preventDefault();
    const grab = cellAt(box, e.clientX, e.clientY);
    const off = { col: grab.col - tile.col, row: grab.row - tile.row };
    let to: Spot = { size: tile.size, col: tile.col, row: tile.row };
    const move = (ev: globalThis.MouseEvent) => {
      const at = cellAt(box, ev.clientX, ev.clientY);
      to = fit(tile.size, at.col - off.col, at.row - off.row);
      setDrag({ id: tile.id, ...to });
    };
    const up = () => {
      stop();
      if (to.col !== tile.col || to.row !== tile.row) onPlace(tile.id, "moved", to);
    };
    const stop = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", stop);
      dragOff.current = null;
      setDrag(null);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", stop);
    dragOff.current = stop;
    setDrag({ id: tile.id, ...to });
  };
  const moveKeys = (tile: Tile) => (e: KeyboardEvent) => {
    const by = ({ ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] } as Record<string, [number, number]>)[e.key];
    if (!by) return;
    e.preventDefault();
    e.stopPropagation();
    const to = moved(tile, by[0], by[1]);
    if (to) onPlace(tile.id, "moved", to);
  };
  const sizeKeys = (tile: Tile) => (e: KeyboardEvent) => {
    const by = ({ "+": 1, "=": 1, ArrowUp: 1, ArrowRight: 1, "-": -1, ArrowDown: -1, ArrowLeft: -1 } as Record<string, 1 | -1>)[e.key];
    if (!by) return;
    e.preventDefault();
    e.stopPropagation();
    const to = resized(tile, by);
    if (to) onPlace(tile.id, "resized", to);
  };
  const ghost = drag && DIMS[drag.size];

  // Measured when the tiles drawn change, and again when a tile's content
  // or the canvas changes size (not at each drawing: Eriphe's line types at
  // 60 frames a second); not on a phone, where a tile is as tall as it is.
  const need = useRef(onNeed);
  need.current = onNeed;
  const measure = () => {
    if (!narrow && canvas.current) measureTiles(canvas.current, (id, rows) => need.current(id, rows));
  };
  const drawnKey = JSON.stringify([narrow, layout.focus, tiles]);
  useLayoutEffect(measure, [drawnKey]);
  useEffect(() => {
    if (narrow || !canvas.current || typeof ResizeObserver === "undefined") return;
    let frame = 0;
    const ro = new ResizeObserver(() => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(measure);
    });
    ro.observe(canvas.current);
    for (const el of Array.from(canvas.current.querySelectorAll(".mod-fit"))) ro.observe(el);
    return () => {
      cancelAnimationFrame(frame);
      ro.disconnect();
    };
  }, [drawnKey]);

  const strip = (
    <div className={`chips${narrow ? " stacked" : ""}`} role="list" aria-label={t("espace.chips")}>
      {shownChips(chips, (id) => surfaceTitle(byID.get(id)), t("espace.chip.untitled")).map((c) => {
        // A surface's one-line summary, when it has one, says more than why it is there.
        const line = c.ids.length === 1 ? surfaceSummary(byID.get(c.id)) : undefined;
        const st = c.closed ? "quiet" : surfaceState(byID.get(c.id));
        const n = c.closed ? 0 : surfaceNew(byID.get(c.id));
        return (
          <motion.div key={c.id} layoutId={narrow ? undefined : "surface-" + c.id} transition={spring} role="listitem" className={`chip ts-${st}${c.closed ? " closed" : ""}`} data-surface={c.id}>
            <button className="chip-open" onClick={() => onOpen(c.id)} title={t("espace.chip.open", chipLabel(c))}>
              <span className="chip-text">
                {chipLabel(c)}
                {needsUser(st) && <span className={`tile-badge b-${st}`}>{t("espace.state." + st)}</span>}
                {!needsUser(st) && st !== "done" && n > 0 && <span className="tile-badge b-new">{t("espace.tile.new", n)}</span>}
              </span>
              {c.closed ? <span className="chip-from">{t("espace.chip.closed")}</span> : (line || c.why) && <span className="chip-from">{line || c.why}</span>}
            </button>
          </motion.div>
        );
      })}
    </div>
  );
  // A surface as a tile: on the grid at its place, or open in the centre
  // (focus mode), where it has no place, only the way back and its cross.
  const tileView = (tile: Tile, centre = false) => {
    const focus = layout.focus === tile.id;
    // On stage with another open inside it (a child thread in its parent): that one shows, under its own title.
    const inner = focus && layout.inside ? byID.get(layout.inside) : undefined;
    const title = (inner && surfaceTitle(inner)) || tile.title;
    // A small tile shows a surface's one-line summary, when it has one, rather than a slice of it.
    const line = tile.size === "s" && !focus ? surfaceSummary(byID.get(tile.id)) : undefined;
    // Its state, code's: a coloured edge, and a badge for what needs the user or is new.
    const st = surfaceState(byID.get(tile.id));
    const n = focus ? 0 : surfaceNew(byID.get(tile.id));
    const open = (e: MouseEvent) => {
      if (!focus && !(e.target as HTMLElement).closest(interactive)) onOpen(tile.id);
    };
    const place = centre ? " centre" : ` at-c${tile.col} at-r${tile.row} w-${tile.w} h-${tile.h}`;
    return (
      <motion.article
        key={tile.id}
        layout
        layoutId={narrow ? undefined : "surface-" + tile.id}
        transition={spring}
        exit={{ opacity: 0 }}
        data-surface={tile.id}
        aria-label={tile.title}
        className={`mod tile ts-${st} size-${tile.size}${place}${focus ? " focus" : ""}${tile.pinned ? " pinned" : ""}${drag?.id === tile.id ? " dragging" : ""}`}
        onClick={open}
      >
        <div className="tile-head">
          <h2 className="tile-title">{title}</h2>
          {needsUser(st) && !focus && <span className={`tile-badge b-${st}`}>{t("espace.state." + st)}</span>}
          {!needsUser(st) && st !== "done" && n > 0 && <span className="tile-badge b-new">{t("espace.tile.new", n)}</span>}
          {!focus && <span className="tile-more">{t("espace.tile.more")}</span>}
          <div className="tile-tools">
            {centre && (
              <button type="button" className="tile-tool tile-back" aria-label={t("espace.back")} title={t("espace.back")} onClick={onBack}>
                <span aria-hidden="true">←</span>
              </button>
            )}
            {!narrow && !centre && (<>
              <button type="button" className="tile-tool tile-move" aria-label={t("espace.tile.move", tile.title)} title={t("espace.tile.move", tile.title)}
                onPointerDown={startDrag(tile)} onKeyDown={moveKeys(tile)}>
                <span aria-hidden="true">⠿</span>
              </button>
              <button type="button" className="tile-tool tile-size" aria-label={t("espace.tile.size", tile.title, tile.size.toUpperCase())} title={t("espace.tile.size", tile.title, tile.size.toUpperCase())}
                onClick={() => { const to = resized(tile, 1, true); if (to) onPlace(tile.id, "resized", to); }} onKeyDown={sizeKeys(tile)}>
                <span aria-hidden="true">⤡</span>
              </button>
            </>)}
            <button type="button" className="tile-tool tile-close" aria-label={t("espace.tile.close", tile.title)} title={t("espace.tile.close", tile.title)}
              onClick={() => onClose(tile.id)}>
              <span aria-hidden="true">×</span>
            </button>
          </div>
        </div>
        <div className="mod-body">
          <div className="mod-fit">
            {line ? (
              <p className="tile-line">{line}</p>
            ) : (
              <OnStage.Provider value={focus}>
                <SurfaceView key={inner?.id ?? tile.id} surface={inner ?? byID.get(tile.id)!} />
              </OnStage.Provider>
            )}
          </div>
        </div>
        {tile.size !== "s" && !centre && tile.why && <p className="why">{tile.why}</p>}
      </motion.article>
    );
  };

  const board = (
    <main className={`canvas${narrow ? " column" : ""}${full ? " full" : ""}`} aria-label={t("espace.canvas")} ref={canvas}>
      {/* A surface open on a phone takes the screen at once: the others go, not fading out. */}
      <LayoutGroup key={full ? "full" : "grid"}>
        {/* Not mode="popLayout": it injects a <style> element, which the CSP refuses. */}
        <AnimatePresence>{tiles.map((tile) => tileView(tile))}</AnimatePresence>
      </LayoutGroup>
      {drag && ghost && <div className={`tile-ghost at-c${drag.col} at-r${drag.row} w-${ghost.w} h-${ghost.h}`} aria-hidden="true" />}
    </main>
  );

  // The focus mode: the surface open in the centre, the others in the
  // column beside it, each compact and live; what needs the user whole.
  if (center) {
    const side = sideOf(layout, have, (id) => surfaceState(byID.get(id)));
    return (
      <main className="canvas focus-mode" aria-label={t("espace.canvas")} ref={canvas}>
        {tileView(center, true)}
        <aside className="side" aria-label={t("espace.side")}>
          {side.map((item) => {
            const s = byID.get(item.id);
            const title = (item.title && item.title !== item.id ? item.title : undefined) ?? surfaceTitle(s) ?? t("espace.chip.untitled");
            const line = surfaceSummary(s) || item.why;
            const state = item.needs ? (surfaceState(s) === "urgent" ? "urgent" : "yours") : item.closed ? "closed" : item.related ? "related" : undefined;
            const isNew = fresh(item.id);
            const open = (e: MouseEvent) => {
              if (!(e.target as HTMLElement).closest(interactive)) onOpen(item.id);
            };
            return (
              <motion.article
                key={item.id}
                layout
                layoutId={"surface-" + item.id}
                transition={spring}
                data-surface={item.id}
                aria-label={title}
                className={`mod side-item${item.needs ? " needs" : ""}${item.closed ? " closed" : ""}${isNew ? " fresh" : ""}`}
                onClick={open}
              >
                <button type="button" className="side-open" onClick={() => onOpen(item.id)} title={t("espace.chip.open", title)}>
                  {(state || isNew) && (
                    <span className="side-head">
                      {state && <span className={`side-state st-${state}`}>{t("espace.state." + state)}</span>}
                      {isNew && <span className="side-new">{t("espace.side.new")}</span>}
                    </span>
                  )}
                  <span className="side-title">{title}</span>
                  {line && <span className="side-line">{line}</span>}
                </button>
              </motion.article>
            );
          })}
        </aside>
      </main>
    );
  }

  // On a phone the chips come after the tiles: what is folded waits below.
  return narrow ? <>{board}{strip}</> : <>{strip}{board}</>;
}
