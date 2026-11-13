import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { AnimatePresence, LayoutGroup, motion } from "motion/react";
import type { Adapter } from "./a2ui/adapter";
import { SurfaceView } from "./a2ui/Surfaces";
import { chipLabel, shownChips, surfaceTitle } from "./chips";
import { useWords } from "./i18n";
import { COLS, DIMS, ROWS, SIZES, foldOut, needRows } from "./place";
import type { FoldedChip, Layout, Tile, TileSize } from "./types";

// The workspace's surfaces, where eriphe-core lays them out (workspace_layout.go):
// each tile at its place on the fixed 12 × 8 grid, by class (.at-cN, .at-rN,
// .w-N, .h-N, .size-*), never a style attribute; Motion moves a tile that
// changed place, a tile enters by CSS. What is folded waits as a chip in the
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
// On a phone (narrow) the grid does not hold: one column, the tiles in the
// composer's order, the chips after them, only the cross on a tile; a
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
// composer's order (the layout's items, never their place on the grid),
// the chips after them; with a surface open, that surface alone, on the
// whole screen.
export function stacked(layout: Layout, have: Set<string>): { tiles: Tile[]; chips: FoldedChip[]; full: boolean } {
  const { tiles, chips } = drawn(layout, have);
  const open = layout.focus ? tiles.find((i) => i.id === layout.focus) : undefined;
  return open ? { tiles: [open], chips: [], full: true } : { tiles, chips, full: false };
}

type Props = {
  adapter: Adapter;
  layout: Layout;
  narrow?: boolean; // a phone: one column (stacked)
  onOpen: (id: string) => void;
  onClose?: (id: string) => void;
  onPlace?: (id: string, what: "moved" | "resized", to: Spot) => void;
  onNeed?: (id: string, rows: number) => void; // a tile's content needs this many rows
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

export function Grid({ adapter, layout, narrow = false, onOpen, onClose = () => {}, onPlace = () => {}, onNeed = () => {} }: Props) {
  const { t } = useWords();
  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  const byID = new Map(surfaces.map((s) => [s.id, s]));
  const have = new Set(byID.keys());
  const { tiles, chips, full } = narrow ? stacked(layout, have) : { ...drawn(layout, have), full: false };

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
      {shownChips(chips, (id) => surfaceTitle(byID.get(id)), t("espace.chip.untitled")).map((c) => (
        <div key={c.id} role="listitem" className={`chip${c.closed ? " closed" : ""}`} data-surface={c.id}>
          <button className="chip-open" onClick={() => onOpen(c.id)} title={t("espace.chip.open", chipLabel(c))}>
            <span className="chip-text">{chipLabel(c)}</span>
            {c.closed ? <span className="chip-from">{t("espace.chip.closed")}</span> : c.why && <span className="chip-from">{c.why}</span>}
          </button>
        </div>
      ))}
    </div>
  );
  const board = (
    <main className={`canvas${narrow ? " column" : ""}${full ? " full" : ""}`} aria-label={t("espace.canvas")} ref={canvas}>
      {/* A surface open on a phone takes the screen at once: the others go, not fading out. */}
      <LayoutGroup key={full ? "full" : "grid"}>
        {/* Not mode="popLayout": it injects a <style> element, which the CSP refuses. */}
        <AnimatePresence>
          {tiles.map((tile) => {
            const focus = layout.focus === tile.id;
            const open = (e: MouseEvent) => {
              if (!focus && !(e.target as HTMLElement).closest(interactive)) onOpen(tile.id);
            };
            return (
              <motion.article
                key={tile.id}
                layout
                transition={spring}
                exit={{ opacity: 0 }}
                data-surface={tile.id}
                aria-label={tile.title}
                className={`mod tile size-${tile.size} at-c${tile.col} at-r${tile.row} w-${tile.w} h-${tile.h}${focus ? " focus" : ""}${tile.related && !focus ? " related" : ""}${tile.pinned ? " pinned" : ""}${drag?.id === tile.id ? " dragging" : ""}`}
                onClick={open}
              >
                <div className="tile-head">
                  <h2 className="tile-title">{tile.title}</h2>
                  {!focus && <span className="tile-more">{t("espace.tile.more")}</span>}
                  <div className="tile-tools">
                    {!narrow && (<>
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
                    <SurfaceView surface={byID.get(tile.id)!} />
                  </div>
                </div>
                {tile.size !== "s" && (tile.why || tile.related) && <p className="why">{tile.why || t("espace.related")}</p>}
              </motion.article>
            );
          })}
        </AnimatePresence>
      </LayoutGroup>
      {drag && ghost && <div className={`tile-ghost at-c${drag.col} at-r${drag.row} w-${ghost.w} h-${ghost.h}`} aria-hidden="true" />}
    </main>
  );
  // On a phone the chips come after the tiles: what is folded waits below.
  return narrow ? <>{board}{strip}</> : <>{strip}{board}</>;
}
