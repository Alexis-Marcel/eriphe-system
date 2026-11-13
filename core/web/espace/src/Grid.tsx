import { useEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { AnimatePresence, LayoutGroup, motion } from "motion/react";
import type { Adapter } from "./a2ui/adapter";
import { SurfaceView } from "./a2ui/Surfaces";
import { useWords } from "./i18n";
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

const spring = { type: "spring", stiffness: 260, damping: 30 } as const;

const interactive = "button,input,select,textarea,a,[role=button],[role=tab]";

export const COLS = 12;
export const ROWS = 8;
export const SIZES: TileSize[] = ["s", "m", "l", "xl"];
export const DIMS: Record<TileSize, { w: number; h: number }> = { s: { w: 3, h: 1 }, m: { w: 3, h: 2 }, l: { w: 4, h: 3 }, xl: { w: 6, h: 5 } };

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

type Props = {
  adapter: Adapter;
  layout: Layout;
  onOpen: (id: string) => void;
  onClose?: (id: string) => void;
  onPlace?: (id: string, what: "moved" | "resized", to: Spot) => void;
};

export function Grid({ adapter, layout, onOpen, onClose = () => {}, onPlace = () => {} }: Props) {
  const { t } = useWords();
  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  const byID = new Map(surfaces.map((s) => [s.id, s]));
  const tiles = layout.items.filter((i) => byID.has(i.id));
  const named = new Set([...layout.items.map((i) => i.id), ...layout.chips.map((c) => c.id)]);
  const chips: FoldedChip[] = [
    ...layout.chips.filter((c) => byID.has(c.id)),
    ...surfaces.filter((s) => !named.has(s.id)).map((s) => ({ id: s.id, title: s.id })),
  ];

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

  return (
    <>
      <div className="chips" role="list" aria-label={t("espace.chips")}>
        {chips.map((c) => (
          <div key={c.id} role="listitem" className={`chip${c.closed ? " closed" : ""}`} data-surface={c.id}>
            <button className="chip-open" onClick={() => onOpen(c.id)} title={t("espace.chip.open", c.title)}>
              <span className="chip-text">{c.title}</span>
              {c.closed ? <span className="chip-from">{t("espace.chip.closed")}</span> : c.why && <span className="chip-from">{c.why}</span>}
            </button>
          </div>
        ))}
      </div>
      <main className="canvas" aria-label={t("espace.canvas")} ref={canvas}>
        <LayoutGroup>
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
                  className={`mod tile size-${tile.size} at-c${tile.col} at-r${tile.row} w-${tile.w} h-${tile.h}${focus ? " focus" : ""}${tile.pinned ? " pinned" : ""}${drag?.id === tile.id ? " dragging" : ""}`}
                  onClick={open}
                >
                  <div className="tile-head">
                    <h2 className="tile-title">{tile.title}</h2>
                    <div className="tile-tools">
                      <button type="button" className="tile-tool tile-move" aria-label={t("espace.tile.move", tile.title)} title={t("espace.tile.move", tile.title)}
                        onPointerDown={startDrag(tile)} onKeyDown={moveKeys(tile)}>
                        <span aria-hidden="true">⠿</span>
                      </button>
                      <button type="button" className="tile-tool tile-size" aria-label={t("espace.tile.size", tile.title, tile.size.toUpperCase())} title={t("espace.tile.size", tile.title, tile.size.toUpperCase())}
                        onClick={() => { const to = resized(tile, 1, true); if (to) onPlace(tile.id, "resized", to); }} onKeyDown={sizeKeys(tile)}>
                        <span aria-hidden="true">⤡</span>
                      </button>
                      <button type="button" className="tile-tool tile-close" aria-label={t("espace.tile.close", tile.title)} title={t("espace.tile.close", tile.title)}
                        onClick={() => onClose(tile.id)}>
                        <span aria-hidden="true">×</span>
                      </button>
                    </div>
                  </div>
                  <div className="mod-body">
                    <SurfaceView surface={byID.get(tile.id)!} />
                  </div>
                  {tile.why && tile.size !== "s" && <p className="why">{tile.why}</p>}
                </motion.article>
              );
            })}
          </AnimatePresence>
        </LayoutGroup>
        {drag && ghost && <div className={`tile-ghost at-c${drag.col} at-r${drag.row} w-${ghost.w} h-${ghost.h}`} aria-hidden="true" />}
      </main>
    </>
  );
}
