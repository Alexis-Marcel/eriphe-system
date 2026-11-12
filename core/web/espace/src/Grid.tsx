import { useSyncExternalStore, type MouseEvent } from "react";
import { AnimatePresence, LayoutGroup, motion } from "motion/react";
import type { Adapter } from "./a2ui/adapter";
import { SurfaceView } from "./a2ui/Surfaces";
import { useWords } from "./i18n";
import type { FoldedChip, Layout } from "./types";

// The workspace's surfaces, where eriphe-core lays them out (workspace_layout.go):
// each tile at its place on the fixed 12 × 8 grid, by class (.at-cN, .at-rN,
// .w-N, .h-N, .size-*), never a style attribute; Motion moves a tile that
// changed place, a tile enters by CSS. What is folded waits as a chip in the
// strip above; a surface the layout does not name yet (it comes right after
// the surface) waits there too, so nothing is unseen. Opening a tile or a
// chip asks eriphe-core to place it first (onOpen).

const spring = { type: "spring", stiffness: 260, damping: 30 } as const;

const interactive = "button,input,select,textarea,a,[role=button],[role=tab]";

export function Grid({ adapter, layout, onOpen }: { adapter: Adapter; layout: Layout; onOpen: (id: string) => void }) {
  const { t } = useWords();
  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  const byID = new Map(surfaces.map((s) => [s.id, s]));
  const tiles = layout.items.filter((i) => byID.has(i.id));
  const named = new Set([...layout.items.map((i) => i.id), ...layout.chips.map((c) => c.id)]);
  const chips: FoldedChip[] = [
    ...layout.chips.filter((c) => byID.has(c.id)),
    ...surfaces.filter((s) => !named.has(s.id)).map((s) => ({ id: s.id, title: s.id })),
  ];
  return (
    <>
      <div className="chips" role="list" aria-label={t("espace.chips")}>
        {chips.map((c) => (
          <div key={c.id} role="listitem" className="chip" data-surface={c.id}>
            <button className="chip-open" onClick={() => onOpen(c.id)} title={t("espace.chip.open", c.title)}>
              <span className="chip-text">{c.title}</span>
              {c.why && <span className="chip-from">{c.why}</span>}
            </button>
          </div>
        ))}
      </div>
      <main className="canvas" aria-label={t("espace.canvas")}>
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
                  className={`mod tile size-${tile.size} at-c${tile.col} at-r${tile.row} w-${tile.w} h-${tile.h}${focus ? " focus" : ""}`}
                  onClick={open}
                >
                  <h2 className="tile-title">{tile.title}</h2>
                  <div className="mod-body">
                    <SurfaceView surface={byID.get(tile.id)!} />
                  </div>
                  {tile.why && tile.size !== "s" && <p className="why">{tile.why}</p>}
                </motion.article>
              );
            })}
          </AnimatePresence>
        </LayoutGroup>
      </main>
    </>
  );
}
