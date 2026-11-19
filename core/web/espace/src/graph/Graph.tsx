import { useEffect, useMemo, useRef, useState, type PointerEvent as PtrEvent } from "react";
import { AnimatePresence, motion, useMotionValueEvent, useSpring, type MotionValue } from "motion/react";
import { place, type GraphNode } from "../data/api";
import { closePage, depthOf, openPage, placeAt, step, store, subtree, talkTo, useGraph } from "../data/store";
import { useWords } from "../i18n";
import { Page } from "../page/Page";
import { BOTTOM, boxAt, edgeMark, frameAll, framePages, pageMax, TOP, type Box, type Cam } from "./camera";
import { bornBeside, constellation, edgePush, edgeStep, leash, simulate, tether, TETHER, type Pin, type Pos, type Pt } from "./layout";
import { codeOf, labelBox, radiusOf, titleOf, waits, weight } from "./model";

// The graph (#633): every node a bubble, a subject bigger than its own,
// their links; an open node is its page, drawn right where its bubble was,
// the others making room around it (layout.ts). Any number of pages open
// (a ⌘- or shift-click opens one more); the console talks to one of them.
// The user drags a bubble, or a page by its head: it stays where dropped,
// and eriphe-core keeps the place (a subject's spot, a bubble's offset
// from its parent) for every screen.
// The camera frames the open pages, else the whole graph; a drag or the
// wheel moves it by hand, a double click gives it back. A node that waits
// for the user says it on its bubble (a ring that breathes, « t'attend »);
// out of view, a small mark at the screen's edge points to it.

const ease = [0.2, 0.7, 0.2, 1] as const;
const PAGE_W = 700, PAGE_W_MANY = 620, PAGE_H = 520, PAGE_MAX = 640;

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

  // The nodes in the order they were made: a new subject takes its slot
  // in the constellation after the others.
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
  const boxOf = (id: string): Pt => [pageW, Math.min(maxH, pageH[id] ?? PAGE_H)];
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

  // The layout: the subjects in a constellation around the centre, shaped
  // like the screen, where the user dropped what, the node held; laid out
  // again when the graph or what is open changes, and live while a node is
  // dragged.
  const prev = useRef<Pos>({});
  // Laid out again only when a place or what it is relative to changes,
  // not at each event of the stream.
  const places = useGraph((s) => s.places);
  const pinKey = Object.entries(places)
    .map(([id, p]) => (nodes[id] ? `${id}:${parentOf(nodes[id]) ?? ""}:${p.x}:${p.y}` : ""))
    .join(",");
  const pins = useMemo(() => {
    const out: Record<string, Pin> = {};
    for (const [id, p] of Object.entries(places)) {
      const x = nodes[id];
      if (x) out[id] = parentOf(x) ? { rel: [p.x, p.y] } : { abs: [p.x, p.y] };
    }
    return out;
  }, [pinKey]); // eslint-disable-line react-hooks/exhaustive-deps
  const [held, setHeld] = useState<{ id: string; at: Pt } | null>(null);
  const layoutKey =
    list.map((x) => `${x.id}:${parentOf(x) ?? ""}:${(x.links ?? []).join("+")}:${Math.round(rOf(x))}:${titleOf(x, external).length}:${x.state}`).join(",") +
    "|" + openIds.map((id) => `${id}:${boxOf(id)[1]}`).join(",");
  // the free band's shape, by tenths (not the bar's cards, which come and
  // go): the constellation is as wide
  const aspect = Math.round((size.w / Math.max(200, size.h - TOP - BOTTOM)) * 10) / 10;
  // The pages open at the last layout: one opened since is born beside
  // the page the console talks to (or the last one opened), not over it.
  const laidOpen = useRef<string[]>([]);
  const pos = useMemo(() => {
    const lnodes = list.map((x) => ({
      id: x.id,
      parent: parentOf(x),
      links: (x.links ?? []).filter((l) => ids.has(l)),
      ...labelBox(x, titleOf(x, external), depthOf(nodes, x.id), rOf(x)),
      box: openIds.includes(x.id) ? boxOf(x.id) : undefined,
      opened: openIds.includes(x.id) ? openIds.indexOf(x.id) : undefined,
    }));
    const spots = constellation(lnodes, aspect);
    for (const id of openIds) {
      const par = nodes[id] && parentOf(nodes[id]);
      const at = prev.current[id] ?? spots[id] ?? (par ? prev.current[par] : undefined);
      if (laidOpen.current.includes(id) || !at) continue;
      const before = laidOpen.current.filter((o) => openIds.includes(o) && prev.current[o]);
      const from = talk && before.includes(talk) ? talk : before.at(-1);
      if (from) prev.current[id] = bornBeside(at, boxOf(id), prev.current[from], boxOf(from));
    }
    laidOpen.current = openIds;
    const p = simulate(lnodes, prev.current, spots, pins, held ?? undefined, held ? 24 : undefined);
    prev.current = { ...prev.current, ...p };
    return p;
  }, [layoutKey, pins, held, aspect]); // eslint-disable-line react-hooks/exhaustive-deps

  // Positions ease towards the layout's, frame by frame.
  const smooth = useRef<Pos>({});
  const [, setTick] = useState(0);
  useEffect(() => {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      smooth.current = { ...pos };
      setTick((n) => n + 1);
      return;
    }
    let raf = 0, last = 0, still = 0;
    const loop = (ms: number) => {
      const a = Math.min(1, ((ms - (last || ms - 16)) / 1000) * 6);
      last = ms;
      let moving = false;
      for (const [k, [tx, ty]] of Object.entries(pos)) {
        const c = smooth.current[k] ?? [tx, ty];
        const n: Pt = [c[0] + (tx - c[0]) * a, c[1] + (ty - c[1]) * a];
        if (Math.abs(n[0] - tx) + Math.abs(n[1] - ty) > 0.3) moving = true;
        smooth.current[k] = n;
      }
      setTick((n) => n + 1);
      still = moving ? 0 : still + 1;
      if (still < 3) raf = requestAnimationFrame(loop);
    };
    // A new node grows out of its parent.
    for (const x of list) if (!smooth.current[x.id] && x.parent && smooth.current[x.parent]) smooth.current[x.id] = [...smooth.current[x.parent]];
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [pos]); // eslint-disable-line react-hooks/exhaustive-deps
  if (held) smooth.current[held.id] = held.at;
  const at = (id: string): Pt => smooth.current[id] ?? pos[id] ?? [0, 0];

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
  const target: Cam = (() => {
    const shown = openIds.filter((id) => pos[id]);
    if (shown.length) {
      const pages = shown.map((id) => ({ id, box: boxAt(pos[id], boxOf(id)) }));
      const near: Box[] = list.filter((x) => lit.has(x.id) && !shown.includes(x.id) && pos[x.id]).map((x) => [pos[x.id][0] - 110, pos[x.id][1] - 40, pos[x.id][0] + 110, pos[x.id][1] + 70]);
      return framePages(pages, focus, near, band, size.w < 1200 ? 0.9 : 1);
    }
    return frameAll(list.map((x) => pos[x.id]).filter(Boolean), band);
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
    const wheel = (e: WheelEvent) => {
      if ((e.target as HTMLElement).closest(".pg-body")) return;
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
    const [wx, wy] = at(id);
    const k = camK.get();
    grab.current = { id, sx: e.clientX, sy: e.clientY, px: e.clientX, py: e.clientY, ox: wx - (e.clientX - camX.get()) / k, oy: wy - (e.clientY - camY.get()) / k, moved: false };
    try {
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      /* a pointer already gone */
    }
  };
  // A bubble stays near its parent; a subject within a step of the nearest
  // other subject (an open page counts with its size).
  const parentAt = (id: string) => {
    const x = nodes[id];
    return x && parentOf(x) ? at(x.parent!) : null;
  };
  const keepNear = (id: string, raw: Pt, dropped: boolean): Pt => {
    const par = parentAt(id);
    if (par) return tether(raw, par, [TETHER, TETHER], dropped);
    const own = new Set(subtree(nodes, id));
    const others = list.filter((x) => !own.has(x.id) && !parentOf(x)).map((x) => ({ at: at(x.id), box: openIds.includes(x.id) ? boxOf(x.id) : undefined }));
    return leash(raw, others, openIds.includes(id) ? boxOf(id) : undefined, dropped);
  };
  const placeHeld = () => {
    const g = grab.current;
    if (!g) return;
    const k = camK.get();
    const raw: Pt = [(g.px - camX.get()) / k + g.ox, (g.py - camY.get()) / k + g.oy];
    g.raw = raw;
    const p = keepNear(g.id, raw, false);
    smooth.current[g.id] = p;
    setHeld({ id: g.id, at: p });
  };
  // Near the screen's edge the camera scrolls: it is the bubble reaching
  // the edge that counts, not the pointer.
  const edgeTick = (ms: number) => {
    const g = grab.current, el = vp.current;
    if (!g?.moved || !el) {
      edgeRaf.current = null;
      return;
    }
    const p = smooth.current[g.id], k = camK.get();
    const sx = p ? camX.get() + p[0] * k : g.px, sy = p ? camY.get() + p[1] * k : g.py;
    const r = el.getBoundingClientRect();
    const ex = edgePush(sx, r.left, r.right), ey = edgePush(sy, r.top, r.bottom);
    if (ex || ey) {
      g.edgeSince ??= ms;
      const dx = edgeStep(ex, ms, g.edgeSince), dy = edgeStep(ey, ms, g.edgeSince);
      if (dx || dy) {
        camX.jump(camX.get() + dx);
        camY.jump(camY.get() + dy);
        placeHeld();
      }
    } else g.edgeSince = undefined;
    edgeRaf.current = requestAnimationFrame(edgeTick);
  };
  const onMove = (e: PtrEvent) => {
    const g = grab.current;
    if (g) {
      if (!g.moved && Math.abs(e.clientX - g.sx) + Math.abs(e.clientY - g.sy) < 5) return;
      if (!g.moved) {
        g.moved = true;
        setManual(true);
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
    const g = grab.current;
    grab.current = null;
    pan.current = null;
    if (edgeRaf.current) cancelAnimationFrame(edgeRaf.current);
    edgeRaf.current = null;
    if (!g?.moved || !g.raw) return;
    // Dropped: it keeps this place, a subject as a spot, a bubble as an
    // offset from its parent.
    const p = keepNear(g.id, g.raw, true);
    const par = parentAt(g.id);
    prev.current[g.id] = p;
    const kept = par ? { x: p[0] - par[0], y: p[1] - par[1] } : { x: p[0], y: p[1] };
    step(store, placeAt, g.id, kept);
    if (live) void place(g.id, kept).catch(() => undefined);
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
      className={`gr-viewport${held ? " holding" : ""}`}
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
              title={titleOf(x, external)}
              d={d}
              r={rOf(x)}
              at={at(x.id)}
              open={openIds.includes(x.id)}
              focus={x.id === talk}
              dim={openIds.length > 0 && !lit.has(x.id)}
              held={held?.id === x.id}
              light={0.3 + 0.7 * Math.min(1, weight(x, nodes, now) / 0.85)}
              waitsWord={t("espace.waits")}
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
              return (
                <motion.div
                  key={`page-${id}`}
                  className="gr-page"
                  style={{ left: cx, top: cy, width: pageW, maxHeight: maxH }}
                  ref={sizer(id)}
                  initial={{ opacity: 0, scale: 0.2 }}
                  animate={{ opacity: 1, scale: 1 }}
                  exit={{ opacity: 0, scale: 0.2 }}
                  transition={{ type: "spring", stiffness: 200, damping: 26 }}
                >
                  <Page
                    id={id}
                    live={live}
                    talking={openIds.length > 1 && id === talk}
                    onTalk={() => step(store, talkTo, id)}
                    onClose={() => step(store, closePage, id)}
                    onGrab={(e) => grabNode(id, e)}
                    onOpen={(p) => openNode(p, true)}
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
    </div>
  );
}

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
// beside the others), a ring for what runs or waits, its label.
function Bubble(p: {
  x: GraphNode; title: string; d: number; r: number; at: Pt; open: boolean; focus: boolean; dim: boolean; held: boolean; light: number; waitsWord: string;
  onGrab: (e: PtrEvent) => void; onOpen: (beside: boolean) => void;
}) {
  const { x, d, r } = p;
  const yours = waits(x);
  const run = x.state === "running";
  const R = r + (d === 0 ? 12 : 6);
  const opacity = p.open ? 0 : p.dim ? (d === 0 ? 0.42 : 0.28) : x.state === "done" ? Math.max(0.35, p.light * 0.8) : Math.max(0.5, p.light);
  const ring = R + (d === 0 ? 9 : 7);
  const cls = ["gr-node", `d${Math.min(d, 2)}`, x.state, yours && "yours", p.focus && "focus", p.held && "held", x.lifetime === "standing" && "standing", x.external && "external"]
    .filter(Boolean)
    .join(" ");
  return (
    <motion.div
      className={cls}
      style={{ left: p.at[0], top: p.at[1], pointerEvents: p.open ? "none" : undefined, zIndex: p.held ? 5 : undefined }}
      initial={{ opacity: 0, scale: 0.3 }}
      animate={{ opacity, scale: p.held ? 1.08 : 1 }}
      transition={{ duration: 0.6, ease }}
      data-node={x.id}
    >
      {yours && <span className="gr-halo" style={{ width: r * 5, height: r * 5 }} />}
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
        aria-label={x.line && run ? `${p.title} : ${x.line}` : p.title}
      >
        {d === 0 ? <span className="gr-code">{codeOf(p.title)}</span> : x.state === "done" ? <span className="gr-glyph">✓</span> : null}
      </button>
      <span className="gr-label" style={{ marginTop: R + (d === 0 ? 14 : 8) }}>
        <b>{p.title}</b>
        {x.line && run && !p.dim && <em className="runs">{x.line}</em>}
        {yours && !run && <em className="yours">{p.waitsWord}</em>}
      </span>
    </motion.div>
  );
}
