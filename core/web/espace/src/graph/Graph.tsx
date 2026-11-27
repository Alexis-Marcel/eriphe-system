import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type PointerEvent as PtrEvent, type ReactNode } from "react";
import { AnimatePresence, frame, motion, useMotionValueEvent, useReducedMotion, useSpring, type MotionValue } from "motion/react";
import type { GraphNode } from "../data/api";
import { closePage, depthOf, openPage, step, store, subtree, talkTo, useGraph } from "../data/store";
import { useWords, type Lang } from "../i18n";
import { Orb } from "../orb/Orb";
import { Page } from "../page/Page";
import { onReads, readsUnderWay } from "../page/usePage";
import { bigBox, bigRoom, boxAt, edgeMark, frameAll, frameAlone, frameAround, framePages, pageMax, type Box, type Cam } from "./camera";
import { decide, families, hit, LOD, lodVersion, NEAR, onLod, same, type View } from "./lod";
import { FAMILIES, Painter, scene, type Scene } from "./paint";
import { CENTRING, edgePush, edgeStep, extent, FORCES, onForces, pairs, Sim, SLICE, type LNode, type Pos, type Pt } from "./layout";
import { coreShape, pickTiles, TILE, variation } from "./glance";
import { codeOf, labelBox, plainTitle, radiusOf, titleOf, waits } from "./model";
import { TileFace } from "./Tile";
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
// for the user says it on its bubble by its halo alone, no word (the word
// is for screen readers only); out of view, a small mark at the screen's
// edge points to it.
// React draws the graph when what it shows changes (a node, a page, a
// drag begun); each frame of the simulation only moves what is drawn:
// the bubbles, the pages and the links are placed straight on their
// elements (place), by a transform (no layout), the orb and the edge
// marks told (onFrame). A big graph (lod.ts) paints every node as a disc
// and every link as a line on a canvas (paint.ts), and draws as bubbles
// only the nodes big on the screen, open, held, hovered or waiting near
// the camera: what is drawn is a small store of its own (Shown), so that
// the camera moving draws the bubbles that come, not the whole graph. A
// disc is hit as a bubble is: hovered, pressed, dragged, clicked.

const ease = [0.2, 0.7, 0.2, 1] as const;
const PAGE_W = 700, PAGE_W_MANY = 620, PAGE_H = 520, PAGE_MAX = 640;

// sim is the graph's simulation, one for the page's life: it outlives the
// graph's switch to the phone's list and back. A frame takes a slice of
// the main thread at most: a big graph is laid out over a few.
const sim = new Sim(FORCES, { slice: SLICE });
const positions = () => sim.positions();

// What moves with the simulation's frames, told after each (the orb, the
// edge marks).
const framed = new Set<() => void>();
const onFrame = (f: () => void) => {
  framed.add(f);
  return () => void framed.delete(f);
};

// sizes: the pages the user gave a size, like windows: enlarged ("big",
// its size worked out from the screen's), or dragged by the corner (its
// width and height, in the graph's units). For the session, like where
// a bubble was dropped: kept beside sim (the switch to the phone's list
// and back keeps it), never saved, a reload gives each page its own.
const sizes = new Map<string, Pt | "big">();
const MIN_W = 420, MIN_H = 240;

type Sizing = { id: string; sx: number; sy: number; w0: number; h0: number; c0: Pt; moved: boolean };

type Grab = { id: string; sx: number; sy: number; px: number; py: number; ox: number; oy: number; moved: boolean; raw?: Pt; edgeSince?: number; disc?: boolean };

// A link drawn: one per pair of nodes linked (a bubble and its parent,
// two nodes a text links, one way or both), a straight line.
type Link = { key: string; a: string; b: string };

// putNode and putLink place a bubble and a link where the simulation has
// them: a bubble by its transform, which moves it without a layout (a 2D
// one: a 3D one gives each bubble a layer of its own, measured no faster).
const putNode = (el: HTMLElement, p: Pt) => void (el.style.transform = `translate(${p[0]}px, ${p[1]}px)`);
const putLink = (el: Element, a: Pt, b: Pt) => {
  el.setAttribute("x1", String(a[0]));
  el.setAttribute("y1", String(a[1]));
  el.setAttribute("x2", String(b[0]));
  el.setAttribute("y2", String(b[1]));
};

// lightOf is how bright a node's core is, by its weight.
const lightOf = (x: GraphNode) => 0.3 + 0.7 * Math.min(1, x.weight / 0.85);

// Drawn draws items, those of a big graph the camera sees only (Shown):
// it alone is drawn again when they change, not the graph.
function Drawn<T>({ shown, all, items, on, draw }: { shown: Shown; all: boolean; items: T[]; on: (x: T, s: Set<string>) => boolean; draw: (x: T) => ReactNode }) {
  const s = useSyncExternalStore(shown.on, shown.get);
  if (all) return <>{items.map(draw)}</>;
  return <>{s ? items.filter((x) => on(x, s)).map(draw) : null}</>;
}

// Shown is the ids drawn (null: all of them), a small store the bubbles
// and the links read, set at most once a frame, when it changes.
type Shown = { get(): Set<string> | null; set(s: Set<string> | null): void; on(f: () => void): () => void };
function shownStore(): Shown {
  let now: Set<string> | null = null;
  const fs = new Set<() => void>();
  return {
    get: () => now,
    set(s) {
      if (same(s, now)) return;
      now = s;
      for (const f of fs) f();
    },
    on(f) {
      fs.add(f);
      return () => void fs.delete(f);
    },
  };
}

export function Graph({ live }: { live: boolean }) {
  const { t, lang } = useWords();
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
  // The knobs of the level of detail (lod.ts), tuned live on ?leger.
  const lodV = useSyncExternalStore(onLod, lodVersion);
  const lod = list.length >= LOD.from;
  const ids = useMemo(() => new Set(list.map((x) => x.id)), [list]);
  const parentOf = (x: GraphNode) => (x.parent && ids.has(x.parent) ? x.parent : undefined);
  const openKey = open.filter((id) => ids.has(id)).join(",");
  const openIds = useMemo(() => (openKey ? openKey.split(",") : []), [openKey]);
  const lit = useMemo(() => new Set(openIds.flatMap((id) => subtree(nodes, id))), [openIds, nodes]);
  const rOf = (x: GraphNode) => radiusOf(x, nodes);
  // The nodes drawn as tiles (graph/glance.ts): a tile's body is its
  // rounded rectangle, for the layout and the camera alike.
  const tilesWas = useRef<string[]>([]);
  const tiles = useMemo(() => pickTiles(list, openIds, tilesWas.current), [list, openIds]);
  useEffect(() => void (tilesWas.current = tiles), [tiles]);
  const tileOf = (x: GraphNode) => (tiles.includes(x.id) ? TILE : undefined);
  const bodyOf = (x: GraphNode) => {
    const b = labelBox(x, titleOf(x, external), depthOf(nodes, x.id), rOf(x));
    const tile = tileOf(x);
    return tile ? { ...b, r: tile[0] / 2, tile } : b;
  };

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
  const [sized, setSized] = useState(0);
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
        if (el) els.current.pages.set(id, el);
        else els.current.pages.delete(id);
        if (!el) return;
        place();
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
  // page's life (sim, above), told the graph and what is open whenever
  // they change (layoutKey), ticked a frame at a time while something
  // moves. Nothing of it is kept: a reload lays the graph out afresh.
  const [held, setHeld] = useState<string | null>(null);
  const layoutKey = useMemo(
    () =>
      list.map((x) => `${x.id}:${parentOf(x) ?? ""}:${(x.links ?? []).join("+")}:${Math.round(rOf(x))}:${titleOf(x, external).length}:${x.status}:${tileOf(x) ? "tile" : ""}`).join(",") +
      "|" +
      openIds.map((id) => `${id}:${roomOf(id)[0]}x${roomOf(id)[1]}`).join(","),
    [list, openIds, tiles, pageH, sized, size.w, size.h, foot, external], // eslint-disable-line react-hooks/exhaustive-deps
  );
  // The bodies as the simulation has them, the same while the layout is.
  const bodies: LNode[] = useMemo(
    () =>
      list.map((x) => ({
        id: x.id,
        parent: parentOf(x),
        links: (x.links ?? []).filter((l) => ids.has(l) && l !== x.id),
        ...bodyOf(x),
        box: openIds.includes(x.id) ? roomOf(x.id) : undefined,
      })),
    [layoutKey], // eslint-disable-line react-hooks/exhaustive-deps
  );
  const links: Link[] = useMemo(() => pairs(bodies).map(([a, b]) => ({ key: `${a}-${b}`, a, b })), [bodies]);
  const linkBy = useMemo(() => new Map(links.map((l) => [l.key, l])), [links]);
  // A big graph's canvas: its scene (paint.ts), each node's family's
  // colour, worked out once per graph; its nodes are born already, as
  // discs (a bubble come is not born again).
  const famOf = useMemo(() => families(list.map((x) => ({ id: x.id, parent: parentOf(x) })), FAMILIES), [list]); // eslint-disable-line react-hooks/exhaustive-deps
  const sceneOf: Scene | null = useMemo(() => {
    if (!lod) return null;
    return scene(
      list.map((x, i) => {
        const title = plainTitle(x, external), d = depthOf(nodes, x.id);
        born.add(x.id);
        return { id: x.id, r: bodies[i]?.id === x.id ? bodies[i].r : rOf(x), depth: d, light: lightOf(x), status: x.status, fam: famOf.get(x.id) ?? -1, title, code: d === 0 ? codeOf(title) : "" };
      }),
      links.map((l) => [l.a, l.b]),
    );
  }, [lod, bodies, list, famOf]); // eslint-disable-line react-hooks/exhaustive-deps
  const sceneNow = useRef(sceneOf);
  sceneNow.current = sceneOf;
  const lodNow = useRef(lod);
  lodNow.current = lod;

  // What is drawn, by node: placed straight on its element at each frame,
  // by a transform: the writes move it without a layout of the page. Only
  // what is drawn is placed.
  const els = useRef({ nodes: new Map<string, HTMLElement>(), pages: new Map<string, HTMLElement>(), links: new Map<string, SVGElement>() });
  const linksNow = useRef(links);
  linksNow.current = links;
  const placed = useRef(0);
  const linkByNow = useRef(linkBy);
  linkByNow.current = linkBy;
  const place = () => {
    const E = els.current;
    for (const [id, el] of E.nodes) {
      const p = sim.at(id);
      if (p) putNode(el, p);
    }
    // A page is centred on its point; its transform is its spring's.
    for (const [id, el] of E.pages) {
      const p = sim.at(id);
      if (p) el.style.translate = `calc(${p[0]}px - 50%) calc(${p[1]}px - 50%)`;
    }
    for (const [key, el] of E.links) {
      const l = linkByNow.current.get(key);
      const a = l && sim.at(l.a), b = l && sim.at(l.b);
      if (a && b) putLink(el, a, b);
    }
    // The canvas, in the same frame as the bubbles.
    if (lodNow.current) paint();
    // The bodies moving (the loop), they are looked at every few frames.
    if (raf.current === null || ++placed.current % 6 === 0) pick(true);
    for (const f of framed) f();
  };
  const regs = useRef(new Map<string, (el: Element | null) => void>());
  const reg = (kind: "nodes" | "links", key: string) => {
    const k = `${kind}:${key}`;
    let f = regs.current.get(k);
    if (!f) {
      // Drawn (come into view, or new), it is placed at once: before the
      // screen shows it, not at the next frame.
      f = (el) => {
        const m = els.current[kind] as Map<string, Element>;
        // A bubble come in a big graph fades in as its disc fades out
        // (paint.ts); gone, its disc is painted at once.
        if (kind === "nodes" && lodNow.current) {
          if (el) swaps.current.set(key, performance.now());
          else swaps.current.delete(key);
          paintSoon();
        }
        if (!el) return void m.delete(key);
        m.set(key, el);
        if (kind === "nodes") {
          const p = sim.at(key);
          if (p) putNode(el as HTMLElement, p);
          return;
        }
        const l = linkByNow.current.get(key);
        const a = l && sim.at(l.a), b = l && sim.at(l.b);
        if (a && b) putLink(el, a, b);
      };
      regs.current.set(k, f);
    }
    return f;
  };

  const raf = useRef<number | null>(null);
  // rest: where the bodies were when the graph last came to rest (or
  // what is open changed, and every few frames while it moves, nothing
  // held): the camera frames that, not each frame. The camera is told
  // straight (aim), the graph is not drawn again for it.
  const rest = useRef<Pos>({});
  const rested = () => {
    rest.current = sim.positions();
    aimNow.current();
  };
  const frames = useRef(0);
  const reduced = useMemo(() => matchMedia("(prefers-reduced-motion: reduce)").matches, []);
  const wake = () => {
    if (reduced && !sim.heldId() && sim.hurry()) {
      place();
      rested();
      markStill();
      return;
    }
    if (raf.current !== null) return;
    const loop = () => {
      if (!sim.asleep()) sim.frame();
      const still = sim.asleep();
      // laid out at once (hurry): shown once at rest, not on the way
      const shown = still || !sim.hurrying();
      if (shown) place();
      if (still || (shown && !sim.heldId() && ++frames.current % 15 === 0)) rested();
      raf.current = still ? null : requestAnimationFrame(loop);
      markStill();
    };
    raf.current = requestAnimationFrame(loop);
    markStill();
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
  // A page is drawn once the simulation has its node: one that had none
  // when React drew (a node come with its page open) is drawn now.
  const [, setSynced] = useState(0);
  const unplaced = openIds.some((id) => !sim.at(id));
  useLayoutEffect(() => {
    sim.sync(bodies);
    rest.current = sim.positions();
    wake();
    if (unplaced && openIds.some((id) => sim.at(id))) setSynced((n) => n + 1);
  }, [bodies]); // eslint-disable-line react-hooks/exhaustive-deps
  // The forces tuned, or the centring turned on or off (the settings,
  // hud/Settings.tsx): the graph, warmed, moves.
  const wakeNow = useRef(wake);
  wakeNow.current = wake;
  const [centring, setCentring] = useState(CENTRING.focus);
  useEffect(
    () =>
      onForces(() => {
        setCentring(CENTRING.focus);
        wakeNow.current();
      }),
    [],
  );
  // The node in focus, the open page the bar writes to, is the graph's
  // centre (layout.ts, focus): the graph settles round it, the camera
  // frames it in the middle with the graph around. Its page closed, the
  // bar writes to the last one open (store.ts, closePage), which becomes
  // the centre. A press in a page makes it the one the bar writes to
  // (Page, onTalk): it becomes the centre once the pointer is let go, so
  // that what was pressed (a Oui, a link) is still under it when the click
  // lands.
  const wanted = centring && talk && openIds.includes(talk) ? talk : null;
  const [centre, setCentre] = useState(wanted);
  const wantedNow = useRef(wanted);
  wantedNow.current = wanted;
  const pressing = useRef(false);
  useEffect(() => {
    if (!pressing.current) setCentre(wanted);
  }, [wanted]);
  useEffect(() => {
    const down = () => void (pressing.current = true);
    const up = () => {
      if (!pressing.current) return;
      pressing.current = false;
      setTimeout(() => !pressing.current && setCentre(wantedNow.current), 0);
    };
    addEventListener("pointerdown", down, true);
    addEventListener("pointerup", up, true);
    addEventListener("pointercancel", up, true);
    return () => {
      removeEventListener("pointerdown", down, true);
      removeEventListener("pointerup", up, true);
      removeEventListener("pointercancel", up, true);
    };
  }, []);
  useLayoutEffect(() => {
    sim.focus(centre);
    wake();
  }, [centre, bodies]); // eslint-disable-line react-hooks/exhaustive-deps
  // Whatever React drew (a bubble come, a page opened) goes where the
  // simulation has it, before the screen shows it.
  useLayoutEffect(place);

  /* ---------- the camera ---------- */
  const camX = useSpring(size.w / 2, { stiffness: 70, damping: 20 });
  const camY = useSpring(size.h / 2, { stiffness: 70, damping: 20 });
  const camK = useSpring(0.9, { stiffness: 70, damping: 20 });

  // What a big graph draws as bubbles (lod.ts): those big on the screen,
  // and the one held, focused or hovered; looked at again as the graph
  // moves (place) and as the camera does, once a frame.
  const [shown] = useState(shownStore);
  const hovered = useRef<string | null>(null);
  const sizeNow = useRef(size);
  sizeNow.current = size;
  const view = (): View => ({ x: camX.get(), y: camY.get(), k: camK.get(), ...sizeNow.current });
  // Where the bodies are, x then y in the scene's order: one buffer, read
  // again for each paint and each pick.
  const xyBuf = useRef<Float32Array>(new Float32Array(0));
  const xyNow = () => (xyBuf.current = sim.xy(xyBuf.current));
  // A camera that barely moved since the last look changes nothing; the
  // bodies moving, they are looked at every few frames; some bubbles left
  // for the next frame (the budget), it comes.
  const pickedFor = useRef<View | null>(null);
  const pick = (force = false) => {
    const S = sceneNow.current;
    if (!lodNow.current || !S) return shown.set(null);
    const v = view(), was = pickedFor.current;
    if (!force && was && Math.abs(v.k / was.k - 1) < 0.03 && Math.abs(v.x - was.x) < NEAR / 3 && Math.abs(v.y - was.y) < NEAR / 3) return;
    const xy = xyNow();
    if (xy.length !== S.items.length * 2) return;
    pickedFor.current = v;
    const focused = document.activeElement?.closest?.(".gr-node")?.getAttribute("data-node");
    const { ids, more } = decide(S.items, xy, v, shown.get(), [sim.heldId(), focused, hovered.current]);
    shown.set(ids);
    if (more) pickSoon(true);
  };
  const pickRaf = useRef<number | null>(null);
  const pickForce = useRef(false);
  const pickSoon = (force = false) => {
    pickForce.current ||= force;
    if (pickRaf.current === null)
      pickRaf.current = requestAnimationFrame(() => {
        pickRaf.current = null;
        const f = pickForce.current;
        pickForce.current = false;
        pick(f);
      });
  };

  // The canvas (paint.ts): painted in the frame the camera or the graph
  // moves, with the world's transform (motion's render step), at most once.
  const canvas = useRef<HTMLCanvasElement | null>(null);
  const painter = useRef<Painter | null>(null);
  const swaps = useRef(new Map<string, number>());
  const nearBy = useRef<{ ids: Set<string>; links: Set<number> } | null>(null);
  const openSet = useMemo(() => new Set(openIds), [openIds]);
  const openSetNow = useRef(openSet);
  openSetNow.current = openSet;
  const paint = () => {
    const P = painter.current, S = sceneNow.current;
    if (!P || !S || !lodNow.current) return;
    const xy = xyNow();
    if (xy.length !== S.items.length * 2) return;
    P.resize(sizeNow.current.w, sizeNow.current.h);
    const fading = P.paint(S, xy, view(), { mounted: els.current.nodes, swapped: swaps.current, open: openSetNow.current, near: nearBy.current?.ids ?? null, nearLinks: nearBy.current?.links ?? null }, performance.now());
    if (fading) paintSoon();
  };
  const paintNow = useRef(paint);
  paintNow.current = paint;
  const [paintSoon] = useState(() => {
    const go = () => paintNow.current();
    return () => void frame.render(go);
  });
  const onCanvas = (el: HTMLCanvasElement | null) => {
    canvas.current = el;
    painter.current = el ? Painter.on(el) : null;
    if (el) paintSoon();
  };
  // The theme changed (the user's, or the system's), or the fonts came:
  // painted again in its colours, its font.
  useEffect(() => {
    if (!lod) return;
    const again = () => {
      painter.current?.retheme();
      paintSoon();
    };
    const mo = new MutationObserver(again);
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    const mq = matchMedia("(prefers-color-scheme: dark)");
    mq.addEventListener?.("change", again);
    void document.fonts?.ready.then(again);
    return () => {
      mo.disconnect();
      mq.removeEventListener?.("change", again);
    };
  }, [lod]); // eslint-disable-line react-hooks/exhaustive-deps
  // The knobs turned, the screen resized, the graph changed: looked at again.
  useEffect(() => {
    pick(true);
    paintSoon();
  }, [lodV, size.w, size.h, sceneOf, openSet]); // eslint-disable-line react-hooks/exhaustive-deps

  const onCam = () => {
    pickSoon();
    if (lodNow.current) paintSoon();
  };
  useMotionValueEvent(camX, "change", onCam);
  useMotionValueEvent(camY, "change", onCam);
  useMotionValueEvent(camK, "change", onCam);
  useEffect(() => () => void (pickRaf.current !== null && cancelAnimationFrame(pickRaf.current)), []);
  const [manual, setManual] = useState(false);
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
    const [h, top, bottom] = extent({ id: x.id, links: [], ...bodyOf(x) }, at);
    return [at[0] - h, top, at[0] + h, bottom];
  };
  const aimAt = (): Cam => {
    const rp = rest.current;
    const shown = openIds.filter((id) => rp[id]);
    const bubbles = list.filter((x) => rp[x.id] && !shown.includes(x.id));
    const all = bubbles.map((x) => bubbleBox(x, rp[x.id]));
    // An enlarged page in focus is framed alone and whole, at scale 1.
    if (focus && shown.includes(focus) && sizes.get(focus) === "big") return frameAlone(boxAt(rp[focus], boxOf(focus)), band);
    // The page in focus as the centre: framed readable, in the middle.
    if (centre && shown.includes(centre)) return frameAround(boxAt(rp[centre], boxOf(centre)), band);
    if (shown.length) {
      const pages = shown.map((id) => ({ id, box: boxAt(rp[id], boxOf(id)) }));
      const near = bubbles.filter((x) => lit.has(x.id)).map((x) => bubbleBox(x, rp[x.id]));
      return framePages(pages, focus, near, band, size.w < 1200 ? 0.9 : 1, all);
    }
    return frameAll(all, band);
  };
  // Asked for less motion, the camera goes there at once, as the graph
  // settles at once (wake): nothing glides.
  const aim = (to: Cam) => {
    const go = reduced ? "jump" : "set";
    camK[go](to.k);
    camX[go](to.x);
    camY[go](to.y);
    markStill();
  };
  // Aimed again when what React draws changes (a page, the focus, the
  // screen), and when the graph rests or moves on (rested), unless the
  // user moved the camera by hand. Each aim looks at where the graph rests
  // now: one drawn before the graph came to rest must not take the camera
  // back to where it was then.
  const target = aimAt();
  const aimNow = useRef(() => {});
  aimNow.current = () => void (!manual && aim(aimAt()));
  useEffect(() => {
    aimNow.current();
  }, [manual, reduced, target.k, target.x, target.y, camK, camX, camY]);

  const vp = useRef<HTMLDivElement>(null);
  // The pages springing open or shut (their motion), by node.
  const springing = useRef(new Set<string>());
  // At rest: the graph asleep, nothing held, the camera still, no page
  // springing open or read. Said on the viewport (data-still) for what
  // must measure the graph once it settled (the browser tests), looked at
  // again after each frame, when the camera starts or stops, a page's
  // spring starts or ends, a page's read starts or ends.
  const markStill = () => {
    vp.current?.toggleAttribute("data-still", sim.asleep() && !sim.heldId() && ![camX, camY, camK].some((v) => v.isAnimating()) && springing.current.size === 0 && readsUnderWay() === 0);
  };
  useEffect(markStill);
  useEffect(() => onReads(markStill), []); // eslint-disable-line react-hooks/exhaustive-deps
  useMotionValueEvent(camX, "animationStart", markStill);
  useMotionValueEvent(camY, "animationStart", markStill);
  useMotionValueEvent(camK, "animationStart", markStill);
  useMotionValueEvent(camX, "animationComplete", markStill);
  useMotionValueEvent(camY, "animationComplete", markStill);
  useMotionValueEvent(camK, "animationComplete", markStill);
  // (One shutting is not waited for: it is gone.)
  const openNow = useRef(openIds);
  openNow.current = openIds;
  const spring = (id: string, on: boolean) => {
    if (on && openNow.current.includes(id)) springing.current.add(id);
    else springing.current.delete(id);
    markStill();
  };
  useEffect(() => {
    for (const id of springing.current) if (!openIds.includes(id)) springing.current.delete(id);
    markStill();
  }, [openIds]); // eslint-disable-line react-hooks/exhaustive-deps
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
  // discAt is the disc under a pointer on the canvas of a big graph (not
  // on a bubble or a page), if any: a finger reaches a little wider.
  const discAt = (e: PtrEvent): string | null => {
    const S = sceneNow.current, el = vp.current;
    if (!lodNow.current || !S || !el || (e.target as HTMLElement).closest(".gr-node, .pg, button")) return null;
    const xy = xyNow();
    if (xy.length !== S.items.length * 2) return null;
    const r = el.getBoundingClientRect();
    return hit(S.items, xy, view(), e.clientX - r.left, e.clientY - r.top, e.pointerType === "touch" ? 12 : 3, (id) => openSetNow.current.has(id));
  };
  const onDown = (e: PtrEvent) => {
    if ((e.target as HTMLElement).closest("button, .pg")) return;
    // A disc pressed: held as its bubble would be; let go where it was, it
    // opens.
    const d = discAt(e);
    if (d) {
      grabNode(d, e);
      if (grab.current) grab.current.disc = true;
      return;
    }
    pan.current = { x: e.clientX, y: e.clientY, cx: camX.get(), cy: camY.get() };
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
  };
  const grabNode = (id: string, e: PtrEvent) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    hoverOn(null);
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
    if (!d) {
      // Over the canvas, a disc hovered as its bubble is.
      if (lodNow.current && !sizing.current) {
        const h = discAt(e);
        vp.current?.classList.toggle("over-disc", !!h);
        if (h !== hovered.current && (h || !(e.target as HTMLElement).closest(".gr-node, .pg"))) hoverOn(h);
      }
      return;
    }
    if (Math.abs(e.clientX - d.x) + Math.abs(e.clientY - d.y) > 3) setManual(true);
    camX.jump(d.cx + e.clientX - d.x);
    camY.jump(d.cy + e.clientY - d.y);
  };
  const onUp = (e?: PtrEvent) => {
    if (sizing.current) return endResize();
    const g = grab.current;
    grab.current = null;
    pan.current = null;
    if (edgeRaf.current) cancelAnimationFrame(edgeRaf.current);
    edgeRaf.current = null;
    // A disc pressed and let go without a move: a click on it.
    if (g?.disc && !g.moved && e?.type === "pointerup") return openNode(g.id, e.metaKey || e.ctrlKey || e.shiftKey);
    if (!g?.moved || !g.raw) return;
    // Dropped: it stays there (nothing remembers it), what is under it
    // steps aside, what followed it settles.
    sim.drop();
    wake();
    setHeld(null);
    dragged.current = true;
    setTimeout(() => (dragged.current = false), 0);
  };

  // Hovering a bubble or a page's head: it, the nodes linked to it and
  // their links stay sharp, the rest fades (not while a body is held).
  // Told straight to the elements, as the frames are.
  const hoverOn = (id: string | null) => {
    const v = vp.current;
    if (!v) return;
    for (const el of v.querySelectorAll(".near")) el.classList.remove("near");
    const was = hovered.current;
    hovered.current = id;
    // A big graph: the hovered node becomes a bubble; the canvas fades the
    // rest as the page does.
    if (lodNow.current) {
      nearBy.current = null;
      if (id && !grab.current && !sizing.current) {
        const S = sceneNow.current, i = S?.index.get(id);
        const ids = new Set([id]), ls = new Set<number>();
        if (S && i !== undefined)
          for (let j = 0; j < S.links.length; j += 2)
            if (S.links[j] === i || S.links[j + 1] === i) {
              ls.add(j >> 1);
              ids.add(S.items[S.links[j] === i ? S.links[j + 1] : S.links[j]].id);
            }
        nearBy.current = { ids, links: ls };
      }
      if (was !== id) pickSoon(true);
      paintSoon();
    }
    if (!id || grab.current || sizing.current) {
      v.classList.remove("hovering");
      return;
    }
    const E = els.current, near = new Set([id]);
    for (const l of linksNow.current)
      if (l.a === id || l.b === id) {
        near.add(l.a === id ? l.b : l.a);
        E.links.get(l.key)?.classList.add("near");
      }
    for (const n of near) {
      E.nodes.get(n)?.classList.add("near");
      E.pages.get(n)?.classList.add("near");
    }
    v.classList.add("hovering");
  };

  const openNode = (id: string, beside: boolean) => {
    if (dragged.current) return;
    setManual(false);
    step(store, openPage, id, beside);
  };
  // The bubbles' gestures, the same functions for the page's life: a
  // bubble is drawn again only when what it shows changes (memo).
  const gestures = useRef({ grabNode, openNode, hoverOn });
  gestures.current = { grabNode, openNode, hoverOn };
  const [onGrab] = useState(() => (id: string, e: PtrEvent) => gestures.current.grabNode(id, e));
  const [onOpen] = useState(() => (id: string, beside: boolean) => gestures.current.openNode(id, beside));
  const [onHover] = useState(() => (id: string | null) => gestures.current.hoverOn(id));

  return (
    <div
      className={`gr-viewport${lod ? " lod" : ""}${held ? " holding" : ""}${resizing ? " resizing" : ""}`}
      ref={vp}
      onDoubleClick={(e) => !(e.target as HTMLElement).closest(".pg") && setManual(false)}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onPointerCancel={onUp}
      onPointerLeave={() => lod && !grab.current && hovered.current && !openIds.includes(hovered.current) && hoverOn(null)}
    >
      {lod && <canvas className="gr-canvas" ref={onCanvas} aria-hidden="true" />}
      <motion.div className="gr-world" style={{ x: camX, y: camY, scale: camK }}>
        {!lod && (
          <svg className="gr-links" aria-hidden="true">
            {links.map((l) => (
              <line key={l.key} ref={reg("links", l.key)} className="gr-link" />
            ))}
          </svg>
        )}

        {/* The bubbles: the graph as the keyboard and a screen reader
            reach it (the pages are documents of their own, beside it); in
            a big graph, those the camera sees and near (cull.ts). */}
        <div role="application" aria-label={t("espace.graph")}>
          <Drawn
            shown={shown}
            all={!lod}
            items={list}
            on={(x, s) => s.has(x.id)}
            draw={(x) => (
            <Bubble
              key={x.id}
              place={reg("nodes", x.id)}
              x={x}
              title={plainTitle(x, external)}
              said={titleOf(x, external)}
              d={depthOf(nodes, x.id)}
              r={rOf(x)}
              tile={tileOf(x)}
              lang={lang}
              glanceWord={x.glance ? glanceSaid(x.glance, lang, t) : ""}
              open={openIds.includes(x.id)}
              held={held === x.id}
              light={lightOf(x)}
              fam={lod && LOD.color && (famOf.get(x.id) ?? -1) >= 0 ? `var(--fam${famOf.get(x.id)})` : undefined}
              waitsWord={t("espace.waits")}
              maintainedWord={x.maintained?.length ? t("espace.maintained", x.maintained.join(", ")) : ""}
              onGrab={onGrab}
              onOpen={onOpen}
              onHover={onHover}
            />
            )}
          />
        </div>

        <AnimatePresence>
          {openIds
            .filter((id) => sim.at(id))
            .map((id) => {
              const sized = sizes.has(id);
              const [bw, bh] = boxOf(id);
              return (
                <motion.div
                  key={`page-${id}`}
                  className={`gr-page${sized ? " sized" : ""}${sizes.get(id) === "big" ? " big" : ""}${resizing === id ? " sizing" : ""}`}
                  data-page={id}
                  style={{ width: bw, height: sized ? bh : undefined, maxHeight: sized ? bh : maxH, zIndex: held === id || resizing === id ? 5 : undefined }}
                  ref={sizer(id)}
                  initial={{ opacity: 0, scale: 0.2 }}
                  animate={{ opacity: held === id && resizing !== id ? 0.5 : 1, scale: 1 }}
                  exit={{ opacity: 0, scale: 0.2 }}
                  transition={{ type: "spring", stiffness: 200, damping: 26 }}
                  onPointerOver={(e) => hoverOn((e.target as HTMLElement).closest(".pg-head") ? id : null)}
                  onPointerLeave={() => hoverOn(null)}
                  onAnimationStart={() => spring(id, true)}
                  onAnimationComplete={() => spring(id, false)}
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
        marks={list.filter((x) => waits(x) && !openIds.includes(x.id)).map((x) => ({ id: x.id, title: titleOf(x, external) }))}
        onOpen={(id) => openNode(id, false)}
      />
      <Orb bodies={bodies} positions={positions} pageRect={(id) => els.current.pages.get(id)?.getBoundingClientRect()} onFrame={onFrame} cam={[camX, camY, camK]} onOpen={(id) => openNode(id, true)} />
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
// at the screen's edge, toward each; a click opens it. It looks again as
// the camera or the graph moves, at most once a frame.
function Edges({ cam, size, marks, onOpen }: { cam: [MotionValue<number>, MotionValue<number>, MotionValue<number>]; size: { w: number; h: number }; marks: { id: string; title: string }[]; onOpen: (id: string) => void }) {
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
  useEffect(() => (marks.length ? onFrame(again) : undefined), [marks.length]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => () => void (raf.current !== null && cancelAnimationFrame(raf.current)), []);
  const out = marks.flatMap((m) => {
    const p = sim.at(m.id);
    const e = p && edgeMark(x.get() + p[0] * k.get(), y.get() + p[1] * k.get(), size);
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
// beside the others), a ring for what runs, its label. One sign per
// meaning: a node that waits for the user says it by its violet halo
// alone (and to a screen reader, its core's words); its title is drawn as
// any other, someone else's words or not: the sign that it may be is its
// page's ⚠, and its core's words (⚠).
// A node whose glance is a tile (graph/glance.ts): its core morphs from
// the circle to a rounded rectangle that shows the figure, and back; its
// orbit and satellite fade, its label slides under it; with less motion,
// the two only cross-fade.
// Drawn again only when what it shows changes (memo): where it is, the
// graph writes on its element at each frame (place), its transform; its
// motion (its birth, held, its page open) is on the element inside, which
// the graph never writes. An open node's bubble is its page: hidden, out
// of the keyboard's reach.
// Its core fades with its weight, and more once done; its label does not:
// a done node's label is in the faint grey that still reads (--fg3, AA).
// A bubble of a big graph come back into view (cull.ts) is drawn where it
// is, at once: only a node new to the page is born.
const born = new Set<string>();
const Bubble = memo(function Bubble(p: {
  place: (el: HTMLElement | null) => void;
  x: GraphNode; title: string; said: string; d: number; r: number; tile?: [number, number]; lang: Lang; glanceWord: string;
  open: boolean; held: boolean; light: number; fam?: string; waitsWord: string; maintainedWord: string;
  onGrab: (id: string, e: PtrEvent) => void; onOpen: (id: string, beside: boolean) => void; onHover: (id: string | null) => void;
}) {
  const { x, d, r, tile } = p;
  const yours = waits(x);
  const run = x.status === "running";
  const R = r + (d === 0 ? 12 : 6);
  const still = useReducedMotion();
  const shape = coreShape(tile, r, !!still);
  const morph = shape.move;
  const [fresh] = useState(() => !born.has(x.id));
  useEffect(() => void born.add(x.id), [x.id]);
  // A tile is shown whole: a figure that deserves a look.
  // the same with pages open or not: only a hover fades (Graph, hoverOn)
  const light = tile ? 1 : x.status === "done" ? Math.max(0.35, p.light * 0.8) : Math.max(0.5, p.light);
  const ring = R + (d === 0 ? 9 : 7);
  const cls = ["gr-node", `d${Math.min(d, 2)}`, x.status, yours && "yours", p.held && "held", p.open && "open", p.maintainedWord && "maintained", x.external && "external", x.unasked && "unasked", tile && "tiled", p.fam && "fam"]
    .filter(Boolean)
    .join(" ");
  const hide = tile ? " off" : "";
  return (
    <div ref={p.place} className={cls} style={{ pointerEvents: p.open ? "none" : undefined, zIndex: p.held ? 6 : undefined, ...(p.fam ? { "--f": p.fam } : null) }} data-node={x.id} aria-hidden={p.open || undefined}>
      <motion.div
        className="gr-bub"
        initial={fresh ? { opacity: 0, scale: 0.3 } : false}
        animate={{ opacity: p.open ? 0 : p.held ? 0.5 : 1, scale: p.held ? 1.08 : 1 }}
        transition={{ duration: p.held ? 0.15 : 0.6, ease }}
      >
        {yours && <span className="gr-halo" style={{ width: r * 5, height: r * 5 }} />}
        {/* A node a skill maintains: a small satellite orbits it, slowly (still
            when the user asks for less motion). */}
        {p.maintainedWord && (
          <span className={`gr-sat${hide}`} style={{ width: (R + 3) * 2, height: (R + 3) * 2 }} aria-hidden="true">
            <i />
          </span>
        )}
        <svg className={`gr-ring${hide}`} width={ring * 2 + 16} height={ring * 2 + 16} viewBox={`${-ring - 8} ${-ring - 8} ${ring * 2 + 16} ${ring * 2 + 16}`} aria-hidden="true">
          {d === 0 && <circle r={R + 7} className="gr-orbit" />}
          {run && <circle r={R} className="gr-ring-run" />}
        </svg>
        {tile && <span className="gl-ping" style={{ width: tile[0], height: tile[1] }} aria-hidden="true" />}
        <motion.button
          type="button"
          className={`gr-core${tile ? " tile" : ""}`}
          style={{ opacity: light }}
          initial={false}
          animate={shape.to}
          transition={morph}
          tabIndex={p.open ? -1 : undefined}
          onPointerDown={(e) => p.onGrab(x.id, e)}
          onPointerEnter={() => p.onHover(x.id)}
          onPointerLeave={() => p.onHover(null)}
          onClick={(e) => p.onOpen(x.id, e.metaKey || e.ctrlKey || e.shiftKey)}
          title={p.maintainedWord || undefined}
          aria-label={[x.line && run ? `${p.said} : ${x.line}` : p.said, tile && p.glanceWord, yours && p.waitsWord].filter(Boolean).join(", ")}
        >
          <AnimatePresence initial={false}>
            {tile && x.glance ? (
              <motion.span key="tile" className="gl-in" initial={{ opacity: 0 }} animate={{ opacity: 1, transition: { delay: still ? 0 : 0.12, duration: 0.25 } }} exit={{ opacity: 0, transition: { duration: 0.1 } }}>
                <TileFace g={x.glance} lang={p.lang} />
              </motion.span>
            ) : (
              <motion.span key="code" className="gl-in" initial={{ opacity: 0 }} animate={{ opacity: 1, transition: { delay: still ? 0 : 0.15, duration: 0.25 } }} exit={{ opacity: 0, transition: { duration: 0.1 } }}>
                {d === 0 ? <span className="gr-code">{codeOf(p.title)}</span> : x.status === "done" ? <span className="gr-glyph">✓</span> : null}
              </motion.span>
            )}
          </AnimatePresence>
        </motion.button>
        {/* Its label slides under a tile and back (CSS, as the core morphs). */}
        <span className="gr-label" style={{ marginTop: tile ? tile[1] / 2 + 10 : R + (d === 0 ? 14 : 8) }}>
          <b>
            {p.title}
            {x.unasked && <UnaskedMark />}
          </b>
          {x.line && run && <em className="runs">{x.line}</em>}
        </span>
      </motion.div>
    </div>
  );
});

// glanceSaid is a glance's words for a screen reader: its label and its
// variation, and that its label may be someone else's words.
function glanceSaid(g: NonNullable<GraphNode["glance"]>, lang: Lang, t: (key: string, ...args: (string | number)[]) => string): string {
  const { up, text } = variation(g, lang);
  return [t(up ? "espace.glance.up" : "espace.glance.down", g.label, text), g.labelExternal && `⚠ ${t("espace.glance.external")}`].filter(Boolean).join(", ");
}
