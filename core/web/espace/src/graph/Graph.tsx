import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, type PointerEvent as PtrEvent } from "react";
import { AnimatePresence, motion, useMotionValueEvent, useSpring, type MotionValue } from "motion/react";
import type { GraphNode } from "../data/api";
import { closePage, depthOf, openPage, step, store, subtree, talkTo, useGraph } from "../data/store";
import { useWords } from "../i18n";
import { Orb } from "../orb/Orb";
import { Page } from "../page/Page";
import { bigBox, bigRoom, boxAt, edgeMark, frameAll, frameAlone, framePages, pageMax, type Box, type Cam } from "./camera";
import { edgePush, edgeStep, extent, Sim, type Pos, type Pt } from "./layout";
import { codeOf, labelBox, plainTitle, radiusOf, titleOf, waits, weight } from "./model";
import { UnaskedMark } from "./Title";

// The graph (#633): every node a bubble, a subject bigger than its own,
// their links; an open node is its page, drawn right where its bubble was,
// the others making room around it (layout.ts). Any number of pages open
// (a ⌘- or shift-click opens one more); the bar talks to one of them.
// The user drags a bubble, or a page by its head: what is linked to it
// follows, it stays where dropped and what is there steps aside, for the
// session: nothing is kept, a reload lays the graph out afresh.
// The camera frames the open pages, else the whole graph; a drag or the
// wheel moves it by hand, a double click gives it back. A node that waits
// for the user says it on its bubble (a ring that breathes, « t'attend »);
// out of view, a small mark at the screen's edge points to it.

const ease = [0.2, 0.7, 0.2, 1] as const;
const PAGE_W = 700, PAGE_W_MANY = 620, PAGE_H = 520, PAGE_MAX = 640;

// sim is the graph's simulation, one for the page's life: it outlives the
// graph's switch to the phone's list and back.
const sim = new Sim();

// sizes: the pages the user gave a size, like windows: enlarged ("big",
// its size worked out from the screen's), or dragged by the corner (its
// width and height, in the graph's units). For the session, like where
// a bubble was dropped: kept beside sim (the switch to the phone's list
// and back keeps it), never saved, a reload gives each page its own.
const sizes = new Map<string, Pt | "big">();
const MIN_W = 420, MIN_H = 240;

type Sizing = { id: string; sx: number; sy: number; w0: number; h0: number; c0: Pt; moved: boolean };

type Grab = { id: string; sx: number; sy: number; px: number; py: number; ox: number; oy: number; moved: boolean; raw?: Pt; edgeSince?: number };

export function Graph({ live }: { live: boolean }) {
  const { t } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const open = useGraph((s) => s.open);
  const talk = useGraph((s) => s.talk);
  const external = t("espace.external");

  const [size, setSize] = useState({ w: innerWidth, h: innerHeight });
  useEffect(() => {
    const on = () => setSize({ w: innerWidth, h: innerHeight });
    addEventListener("resize", on);
    return () => removeEventListener("resize", on);
  }, []);

  // The nodes in a stable order (the order they were made): the layout is
  // the same for the same graph.
  const list = useMemo(() => Object.values(nodes).sort((a, b) => a.created.localeCompare(b.created) || a.id.localeCompare(b.id)), [nodes]);
  const ids = useMemo(() => new Set(list.map((x) => x.id)), [list]);
  const parentOf = (x: GraphNode) => (x.parent && ids.has(x.parent) ? x.parent : undefined);
  const openIds = open.filter((id) => ids.has(id));
  const lit = useMemo(() => new Set(openIds.flatMap((id) => subtree(nodes, id))), [openIds.join(","), nodes]); // eslint-disable-line react-hooks/exhaustive-deps
  const now = Date.now();
  const rOf = (x: GraphNode) => radiusOf(x, nodes, now);

  // The room the bar takes at the foot (its cards, when it shows some).
  const foot = useFoot();
  const band = { ...size, foot };

  // A page takes the height of its content, measured whenever it changes
  // (its document comes after it opened), up to what the screen holds
  // above the bar.
  const [pageH, setPageH] = useState<Record<string, number>>({});
  const pageW = openIds.length > 1 ? PAGE_W_MANY : PAGE_W;
  const maxH = pageMax(band, PAGE_MAX);
  const big = bigBox(band, pageW);
  const [, setSized] = useState(0);
  // A page's box: enlarged, most of the screen; given a size, that size
  // within the screen's; else its width and its content's height.
  const boxOf = (id: string): Pt => {
    const s = sizes.get(id);
    if (s === "big") return big;
    if (s) return [Math.round(Math.min(big[0], Math.max(MIN_W, s[0]))), Math.round(Math.min(big[1], Math.max(MIN_H, s[1])))];
    return [pageW, Math.min(maxH, pageH[id] ?? PAGE_H)];
  };
  // What a page keeps clear in the graph: its box, and for an enlarged one
  // the bar's room too (bigRoom), so that no bubble sits behind the bar.
  const roomOf = (id: string): Pt => (sizes.get(id) === "big" ? bigRoom(big, band) : boxOf(id));
  // The pages are drawn again only when what they show changes
  // (StillPage): their gestures read the boxes of now through this.
  const boxNow = useRef(boxOf);
  boxNow.current = boxOf;
  const sizers = useRef(new Map<string, (el: HTMLDivElement | null) => void>());
  const sizer = (id: string) => {
    let f = sizers.current.get(id);
    if (!f) {
      let ro: ResizeObserver | null = null;
      f = (el) => {
        ro?.disconnect();
        ro = null;
        if (!el) return;
        const read = () => {
          const h = Math.ceil(Math.max(240, el.offsetHeight) / 20) * 20;
          setPageH((c) => (c[id] === h ? c : { ...c, [id]: h }));
        };
        read();
        ro = new ResizeObserver(read);
        ro.observe(el);
      };
      sizers.current.set(id, f);
    }
    return f;
  };

  // The layout: a living force graph (layout.ts), one simulation for the
  // page's life (sim, below), told the graph and what is open whenever
  // they change, ticked a frame at a time while something moves. Nothing
  // of it is kept: a reload lays the graph out afresh.
  const [held, setHeld] = useState<string | null>(null);
  const layoutKey =
    list.map((x) => `${x.id}:${parentOf(x) ?? ""}:${(x.links ?? []).join("+")}:${Math.round(rOf(x))}:${titleOf(x, external).length}:${x.state}`).join(",") +
    "|" + openIds.map((id) => `${id}:${roomOf(id)[0]}x${roomOf(id)[1]}`).join(",");
  const [, setFrame] = useState(0);
  const raf = useRef<number | null>(null);
  // rest: where the bodies were when the graph last came to rest (or
  // what is open changed, and every few frames while it moves, nothing
  // held): the camera frames that, not each frame.
  const rest = useRef<Pos>({});
  const frames = useRef(0);
  const reduced = useMemo(() => matchMedia("(prefers-reduced-motion: reduce)").matches, []);
  const wake = () => {
    if (reduced && !sim.heldId()) {
      sim.settle();
      rest.current = sim.positions();
      setFrame((n) => n + 1);
      return;
    }
    if (raf.current !== null) return;
    const loop = () => {
      if (!sim.asleep()) sim.tick();
      const still = sim.asleep();
      if (still || (!sim.heldId() && ++frames.current % 15 === 0)) rest.current = sim.positions();
      setFrame((n) => n + 1);
      raf.current = still ? null : requestAnimationFrame(loop);
    };
    raf.current = requestAnimationFrame(loop);
  };
  // Unmounted (the switch to the phone's list, a remount): the loop stops
  // and what was held is let go; mounted again, the next change wakes it.
  useEffect(
    () => () => {
      if (raf.current !== null) cancelAnimationFrame(raf.current);
      raf.current = null;
      sim.drop();
    },
    [],
  );
  // A page opened goes beside the one the bar talked to before it.
  const talkWas = useRef(talk);
  useEffect(() => void (talkWas.current = talk), [talk]);
  const bodies = list.map((x) => ({
    id: x.id,
    parent: parentOf(x),
    links: (x.links ?? []).filter((l) => ids.has(l) && l !== x.id),
    ...labelBox(x, titleOf(x, external), depthOf(nodes, x.id), rOf(x)),
    box: openIds.includes(x.id) ? roomOf(x.id) : undefined,
  }));
  useLayoutEffect(() => {
    sim.sync(bodies, talkWas.current);
    rest.current = sim.positions();
    wake();
  }, [layoutKey]); // eslint-disable-line react-hooks/exhaustive-deps
  const pos = sim.positions();
  const at = (id: string): Pt => pos[id] ?? [0, 0];

  /* ---------- the camera ---------- */
  const camX = useSpring(size.w / 2, { stiffness: 70, damping: 20 });
  const camY = useSpring(size.h / 2, { stiffness: 70, damping: 20 });
  const camK = useSpring(0.9, { stiffness: 70, damping: 20 });
  const [manual, setManual] = useState(false);
  const openKey = openIds.join(",");
  useEffect(() => setManual(false), [openKey, talk]);
  // The page in focus: the last one opened (Eriphe's show too), or the
  // one the user turns to; framed whole when the pages do not all fit.
  const [focus, setFocus] = useState<string | null>(talk);
  const wasOpen = useRef<string[]>(openIds);
  useEffect(() => {
    const born = openIds.filter((id) => !wasOpen.current.includes(id));
    wasOpen.current = openIds;
    if (born.length) setFocus(born[born.length - 1]);
  }, [openKey]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (talk) setFocus(talk);
  }, [talk]);
  // A bubble's box: the bubble and its label under it, whole.
  const bubbleBox = (x: GraphNode, at: Pt): Box => {
    const [h, top, bottom] = extent({ id: x.id, links: [], ...labelBox(x, titleOf(x, external), depthOf(nodes, x.id), rOf(x)) }, at);
    return [at[0] - h, top, at[0] + h, bottom];
  };
  const target: Cam = (() => {
    const rp = rest.current;
    const shown = openIds.filter((id) => rp[id]);
    const bubbles = list.filter((x) => rp[x.id] && !shown.includes(x.id));
    const all = bubbles.map((x) => bubbleBox(x, rp[x.id]));
    // An enlarged page in focus is framed alone and whole, at scale 1.
    if (focus && shown.includes(focus) && sizes.get(focus) === "big") return frameAlone(boxAt(rp[focus], boxOf(focus)), band);
    if (shown.length) {
      const pages = shown.map((id) => ({ id, box: boxAt(rp[id], boxOf(id)) }));
      const near = bubbles.filter((x) => lit.has(x.id)).map((x) => bubbleBox(x, rp[x.id]));
      return framePages(pages, focus, near, band, size.w < 1200 ? 0.9 : 1, all);
    }
    return frameAll(all, band);
  })();
  useEffect(() => {
    if (manual) return;
    camK.set(target.k);
    camX.set(target.x);
    camY.set(target.y);
  }, [manual, target.k, target.x, target.y, camK, camX, camY]);

  const vp = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = vp.current;
    if (!el) return;
    // Over a page, the wheel scrolls the page's body, from its head too,
    // and never moves the graph; a pinch there does nothing (the browser
    // would zoom the whole screen).
    const wheel = (e: WheelEvent) => {
      const pg = (e.target as HTMLElement).closest(".pg");
      if (pg) {
        if (e.ctrlKey || e.metaKey) e.preventDefault();
        else if (!(e.target as HTMLElement).closest(".pg-body")) {
          e.preventDefault();
          pg.querySelector(".pg-body")?.scrollBy({ top: e.deltaY, left: e.deltaX });
        }
        return;
      }
      e.preventDefault();
      setManual(true);
      if (e.ctrlKey || e.metaKey) {
        const k0 = camK.get();
        const k1 = Math.max(0.25, Math.min(2.6, k0 * Math.exp(-e.deltaY * 0.006)));
        camX.jump(e.clientX - ((e.clientX - camX.get()) * k1) / k0);
        camY.jump(e.clientY - ((e.clientY - camY.get()) * k1) / k0);
        camK.jump(k1);
      } else {
        camX.jump(camX.get() - e.deltaX);
        camY.jump(camY.get() - e.deltaY);
      }
    };
    el.addEventListener("wheel", wheel, { passive: false });
    return () => el.removeEventListener("wheel", wheel);
  }, [camK, camX, camY]);

  /* ---------- dragging: the view, a bubble, a page by its head ---------- */
  const pan = useRef<{ x: number; y: number; cx: number; cy: number } | null>(null);
  const grab = useRef<Grab | null>(null);
  const sizing = useRef<Sizing | null>(null);
  const [resizing, setResizing] = useState<string | null>(null);
  const edgeRaf = useRef<number | null>(null);
  const dragged = useRef(false); // swallows the click that ends a drag
  const onDown = (e: PtrEvent) => {
    if ((e.target as HTMLElement).closest("button, .pg")) return;
    pan.current = { x: e.clientX, y: e.clientY, cx: camX.get(), cy: camY.get() };
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
  };
  const grabNode = (id: string, e: PtrEvent) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    const [wx, wy] = sim.at(id) ?? [0, 0];
    const k = camK.get();
    grab.current = { id, sx: e.clientX, sy: e.clientY, px: e.clientX, py: e.clientY, ox: wx - (e.clientX - camX.get()) / k, oy: wy - (e.clientY - camY.get()) / k, moved: false };
    try {
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      /* a pointer already gone */
    }
  };
  // The body held follows the pointer, within reach (the simulation's
  // leash): a subject not too far out in the void, a bubble near its
  // parent.
  const placeHeld = () => {
    const g = grab.current;
    if (!g) return;
    const k = camK.get();
    const raw: Pt = [(g.px - camX.get()) / k + g.ox, (g.py - camY.get()) / k + g.oy];
    g.raw = raw;
    sim.hold(g.id, raw);
    if (held !== g.id) setHeld(g.id);
    wake();
  };
  // Near the screen's edge the camera scrolls: it is the bubble reaching
  // the edge that counts, not the pointer.
  const edgeTick = (ms: number) => {
    const g = grab.current, el = vp.current;
    if (!g?.moved || !el) {
      edgeRaf.current = null;
      return;
    }
    const p = sim.at(g.id), k = camK.get();
    const sx = p ? camX.get() + p[0] * k : g.px, sy = p ? camY.get() + p[1] * k : g.py;
    const r = el.getBoundingClientRect();
    const ex = edgePush(sx, r.left, r.right), ey = edgePush(sy, r.top, r.bottom);
    if (ex || ey) {
      g.edgeSince ??= ms;
      const dx = edgeStep(ex, ms, g.edgeSince), dy = edgeStep(ey, ms, g.edgeSince);
      if (dx || dy) {
        setManual(true);
        camX.jump(camX.get() + dx);
        camY.jump(camY.get() + dy);
        placeHeld();
      }
    } else g.edgeSince = undefined;
    edgeRaf.current = requestAnimationFrame(edgeTick);
  };
  // A page's corner dragged: it takes the size the pointer gives (its top
  // left corner stays where it was: the page held, its centre moving by
  // half what it grew), the camera still meanwhile; let go, what is
  // under it steps aside and the camera frames it again.
  const startResize = (id: string, e: PtrEvent) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    e.preventDefault();
    const [w0, h0] = boxNow.current(id);
    sizing.current = { id, sx: e.clientX, sy: e.clientY, w0, h0, c0: sim.at(id) ?? [0, 0], moved: false };
    try {
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      /* a pointer already gone */
    }
  };
  const moveResize = (e: PtrEvent) => {
    const z = sizing.current;
    if (!z) return;
    if (!z.moved) {
      if (Math.abs(e.clientX - z.sx) + Math.abs(e.clientY - z.sy) < 3) return;
      z.moved = true;
      setManual(true);
      setResizing(z.id);
    }
    const k = camK.get();
    const w = Math.min(big[0], Math.max(MIN_W, z.w0 + (e.clientX - z.sx) / k));
    const h = Math.min(big[1], Math.max(MIN_H, z.h0 + (e.clientY - z.sy) / k));
    sizes.set(z.id, [w, h]);
    sim.hold(z.id, [z.c0[0] + (w - z.w0) / 2, z.c0[1] + (h - z.h0) / 2]);
    setSized((n) => n + 1);
    wake();
  };
  const endResize = () => {
    const z = sizing.current;
    sizing.current = null;
    if (!z?.moved) return;
    sim.drop();
    wake();
    setResizing(null);
    setManual(false);
  };
  // From the keyboard: its arrows, a step at a time.
  const resizeBy = (id: string, dw: number, dh: number) => {
    const [w, h] = boxNow.current(id);
    sizes.set(id, [w + dw, h + dh]);
    setSized((n) => n + 1);
    setManual(false);
  };
  const toggleBig = (id: string) => {
    if (sizes.get(id) === "big") sizes.delete(id);
    else sizes.set(id, "big");
    setSized((n) => n + 1);
    setFocus(id);
    setManual(false);
  };

  const onMove = (e: PtrEvent) => {
    if (sizing.current) return moveResize(e);
    const g = grab.current;
    if (g) {
      if (!g.moved && Math.abs(e.clientX - g.sx) + Math.abs(e.clientY - g.sy) < 5) return;
      if (!g.moved) {
        g.moved = true;
        edgeRaf.current = requestAnimationFrame(edgeTick);
      }
      g.px = e.clientX;
      g.py = e.clientY;
      placeHeld();
      return;
    }
    const d = pan.current;
    if (!d) return;
    if (Math.abs(e.clientX - d.x) + Math.abs(e.clientY - d.y) > 3) setManual(true);
    camX.jump(d.cx + e.clientX - d.x);
    camY.jump(d.cy + e.clientY - d.y);
  };
  const onUp = () => {
    if (sizing.current) return endResize();
    const g = grab.current;
    grab.current = null;
    pan.current = null;
    if (edgeRaf.current) cancelAnimationFrame(edgeRaf.current);
    edgeRaf.current = null;
    if (!g?.moved || !g.raw) return;
    // Dropped: it stays there (nothing remembers it), what is under it
    // steps aside, what followed it settles.
    sim.drop();
    wake();
    setHeld(null);
    dragged.current = true;
    setTimeout(() => (dragged.current = false), 0);
  };

  const openNode = (id: string, beside: boolean) => {
    if (dragged.current) return;
    setManual(false);
    step(store, openPage, id, beside);
  };

  return (
    <div
      className={`gr-viewport${held ? " holding" : ""}${resizing ? " resizing" : ""}`}
      ref={vp}
      role="application"
      aria-label={t("espace.graph")}
      onDoubleClick={(e) => !(e.target as HTMLElement).closest(".pg") && setManual(false)}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onPointerCancel={onUp}
    >
      <motion.div className="gr-world" style={{ x: camX, y: camY, scale: camK }}>
        <svg className="gr-links" aria-hidden="true">
          {list.map((x) => {
            const out = [];
            const par = parentOf(x);
            if (par) {
              const [x1, y1] = at(par), [x2, y2] = at(x.id);
              const on = lit.has(x.id) && lit.has(par);
              out.push(<line key={`p-${x.id}`} x1={x1} y1={y1} x2={x2} y2={y2} className={`gr-link${on ? " on" : ""}${openIds.length && !on ? " far" : ""}`} />);
            }
            for (const l of x.links ?? []) {
              if (!ids.has(l)) continue;
              const [x1, y1] = at(x.id), [x2, y2] = at(l);
              const far = openIds.length > 0 && !(lit.has(x.id) || lit.has(l));
              out.push(
                <path
                  key={`l-${x.id}-${l}`}
                  d={`M${x1},${y1} Q${(x1 + x2) / 2 - (y2 - y1) * 0.18},${(y1 + y2) / 2 + (x2 - x1) * 0.18} ${x2},${y2}`}
                  className={`gr-link rel${far ? " far" : ""}`}
                />,
              );
            }
            return out;
          })}
        </svg>

        {list.map((x) => {
          const d = depthOf(nodes, x.id);
          return (
            <Bubble
              key={x.id}
              x={x}
              title={plainTitle(x, external)}
              said={titleOf(x, external)}
              d={d}
              r={rOf(x)}
              at={at(x.id)}
              open={openIds.includes(x.id)}
              focus={x.id === talk}
              dim={openIds.length > 0 && !lit.has(x.id)}
              held={held === x.id}
              light={0.3 + 0.7 * Math.min(1, weight(x, nodes, now) / 0.85)}
              waitsWord={t("espace.waits")}
              standingWord={t("espace.standing.short")}
              onGrab={(e) => grabNode(x.id, e)}
              onOpen={(beside) => openNode(x.id, beside)}
            />
          );
        })}

        <AnimatePresence>
          {openIds
            .filter((id) => pos[id])
            .map((id) => {
              const [cx, cy] = at(id);
              const sized = sizes.has(id);
              const [bw, bh] = boxOf(id);
              return (
                <motion.div
                  key={`page-${id}`}
                  className={`gr-page${sized ? " sized" : ""}${sizes.get(id) === "big" ? " big" : ""}${resizing === id ? " sizing" : ""}`}
                  data-page={id}
                  style={{ left: cx, top: cy, width: bw, height: sized ? bh : undefined, maxHeight: sized ? bh : maxH, zIndex: held === id || resizing === id ? 5 : undefined }}
                  ref={sizer(id)}
                  initial={{ opacity: 0, scale: 0.2 }}
                  animate={{ opacity: held === id && resizing !== id ? 0.5 : 1, scale: 1 }}
                  exit={{ opacity: 0, scale: 0.2 }}
                  transition={{ type: "spring", stiffness: 200, damping: 26 }}
                >
                  <StillPage
                    id={id}
                    live={live}
                    talking={openIds.length > 1 && id === talk}
                    onTalk={() => step(store, talkTo, id)}
                    onClose={() => step(store, closePage, id)}
                    onGrab={(e) => grabNode(id, e)}
                    onOpen={(p) => openNode(p, true)}
                    big={sizes.get(id) === "big"}
                    onBig={() => toggleBig(id)}
                    onResize={(e) => startResize(id, e)}
                    onResizeBy={(dw, dh) => resizeBy(id, dw, dh)}
                  />
                </motion.div>
              );
            })}
        </AnimatePresence>
      </motion.div>
      <Edges
        cam={[camX, camY, camK]}
        size={size}
        marks={list.filter((x) => waits(x) && !openIds.includes(x.id) && pos[x.id]).map((x) => ({ id: x.id, title: titleOf(x, external), at: at(x.id) }))}
        onOpen={(id) => openNode(id, false)}
      />
      <Orb bodies={bodies} at={pos} cam={[camX, camY, camK]} onOpen={(id) => openNode(id, true)} />
    </div>
  );
}

// StillPage is a page drawn again only when what it shows changes, not at
// each frame the graph moves: its handlers name their node, and read the
// graph when they run.
const StillPage = memo(Page, (a, b) => a.id === b.id && a.live === b.live && a.talking === b.talking && a.big === b.big);

// useFoot is the room the bar takes at the screen's foot, from its top to
// the bottom, with a margin: more than the usual when it shows cards.
export function useFoot(): number {
  const [foot, setFoot] = useState(0);
  useEffect(() => {
    const el = document.querySelector<HTMLElement>(".cs");
    if (!el) return;
    const read = () => setFoot(Math.round(innerHeight - el.getBoundingClientRect().top + 12));
    read();
    const ro = new ResizeObserver(read);
    ro.observe(el);
    addEventListener("resize", read);
    return () => {
      ro.disconnect();
      removeEventListener("resize", read);
    };
  }, []);
  return foot;
}

// Edges marks the nodes that wait for the user out of view: a small arrow
// at the screen's edge, toward each; a click opens it.
function Edges({ cam, size, marks, onOpen }: { cam: [MotionValue<number>, MotionValue<number>, MotionValue<number>]; size: { w: number; h: number }; marks: { id: string; title: string; at: Pt }[]; onOpen: (id: string) => void }) {
  const { t } = useWords();
  const [x, y, k] = cam;
  const [, setTick] = useState(0);
  const raf = useRef<number | null>(null);
  const again = () => {
    if (raf.current === null) raf.current = requestAnimationFrame(() => ((raf.current = null), setTick((n) => n + 1)));
  };
  useMotionValueEvent(x, "change", again);
  useMotionValueEvent(y, "change", again);
  useMotionValueEvent(k, "change", again);
  useEffect(() => () => void (raf.current !== null && cancelAnimationFrame(raf.current)), []);
  const out = marks.flatMap((m) => {
    const e = edgeMark(x.get() + m.at[0] * k.get(), y.get() + m.at[1] * k.get(), size);
    return e ? [{ ...m, cx: e.x, cy: e.y, turn: e.turn }] : [];
  });
  return (
    <>
      {out.map((m) => (
        <button key={m.id} type="button" className="gr-edge" style={{ left: m.cx, top: m.cy }} aria-label={t("espace.edge", m.title)} title={t("espace.edge", m.title)} onClick={() => onOpen(m.id)}>
          <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" style={{ rotate: `${m.turn}rad` }}>
            <path d="M3 8h9M8.5 4.5 12 8l-3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
      ))}
    </>
  );
}

// Bubble is one node: its core (a click opens its page; with ⌘ or shift,
// beside the others), a ring for what runs or waits, its label. One sign
// per meaning: a node that waits for the user says it by its violet halo
// and ring alone (and to a screen reader, its core's words); its title is
// drawn as any other, someone else's words or not: the sign that it may
// be is its page's (« extérieur »), and its core's words (⚠).
function Bubble(p: {
  x: GraphNode; title: string; said: string; d: number; r: number; at: Pt; open: boolean; focus: boolean; dim: boolean; held: boolean; light: number; waitsWord: string; standingWord: string;
  onGrab: (e: PtrEvent) => void; onOpen: (beside: boolean) => void;
}) {
  const { x, d, r } = p;
  const yours = waits(x);
  const run = x.state === "running";
  const R = r + (d === 0 ? 12 : 6);
  const opacity = p.open ? 0 : p.dim ? (d === 0 ? 0.42 : 0.28) : x.state === "done" ? Math.max(0.35, p.light * 0.8) : Math.max(0.5, p.light);
  const ring = R + (d === 0 ? 9 : 7);
  const cls = ["gr-node", `d${Math.min(d, 2)}`, x.state, yours && "yours", p.focus && "focus", p.held && "held", x.lifetime === "standing" && "standing", x.external && "external", x.unasked && "unasked"]
    .filter(Boolean)
    .join(" ");
  return (
    <motion.div
      className={cls}
      style={{ left: p.at[0], top: p.at[1], pointerEvents: p.open ? "none" : undefined, zIndex: p.held ? 6 : undefined }}
      initial={{ opacity: 0, scale: 0.3 }}
      animate={{ opacity: p.held ? 0.5 : opacity, scale: p.held ? 1.08 : 1 }}
      transition={{ duration: p.held ? 0.15 : 0.6, ease }}
      data-node={x.id}
    >
      {yours && <span className="gr-halo" style={{ width: r * 5, height: r * 5 }} />}
      {/* A permanent subject: a small satellite orbits it, slowly (still
          when the user asks for less motion). */}
      {x.lifetime === "standing" && (
        <span className="gr-sat" style={{ width: (R + 3) * 2, height: (R + 3) * 2 }} aria-hidden="true">
          <i />
        </span>
      )}
      <svg className="gr-ring" width={ring * 2 + 16} height={ring * 2 + 16} viewBox={`${-ring - 8} ${-ring - 8} ${ring * 2 + 16} ${ring * 2 + 16}`} aria-hidden="true">
        {d === 0 && <circle r={R + 7} className="gr-orbit" />}
        {run && <circle r={R} className="gr-ring-run" />}
        {!run && yours && <circle r={R} className="gr-ring-yours" />}
      </svg>
      <button
        type="button"
        className="gr-core"
        style={{ width: r * 2, height: r * 2 }}
        onPointerDown={p.onGrab}
        onClick={(e) => p.onOpen(e.metaKey || e.ctrlKey || e.shiftKey)}
        title={x.lifetime === "standing" ? p.standingWord : undefined}
        aria-label={[x.line && run ? `${p.said} : ${x.line}` : p.said, yours && p.waitsWord].filter(Boolean).join(", ")}
      >
        {d === 0 ? <span className="gr-code">{codeOf(p.title)}</span> : x.state === "done" ? <span className="gr-glyph">✓</span> : null}
      </button>
      <span className="gr-label" style={{ marginTop: R + (d === 0 ? 14 : 8) }}>
        <b>
          {p.title}
          {x.unasked && <UnaskedMark />}
        </b>
        {x.line && run && !p.dim && <em className="runs">{x.line}</em>}
      </span>
    </motion.div>
  );
}
