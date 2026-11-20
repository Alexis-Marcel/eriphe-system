import { useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { useMotionValueEvent, type MotionValue } from "motion/react";
import { useGraph } from "../data/store";
import { TOP } from "../graph/camera";
import { extent, type LNode, type Pos, type Pt } from "../graph/layout";
import { titleOf } from "../graph/model";
import { useWords } from "../i18n";
import { glide, homePoint, placeBeside, placeHome, type Placed, type Rect, type Side } from "./model";
import { useOrb } from "./useOrb";
import { Words } from "./Words";

// Orb is Eriphe in the graph: a small blue orb. It rests in the bar (its
// dock); when it has something to say or works, it glides beside the
// subject it is about (a bubble, or its page when open), on the side where
// it covers nothing: no bubble, no title, no page, not the bar; at the
// home, to a free spot near the middle of the screen. It is no body of the
// simulation: it reads where the bodies are and gives way to all of them,
// following its subject when it moves. Its words open away from the
// subject; it shows them once there. With less motion asked, it goes at
// once. Each frame is written straight to its elements: no drawing again.

const ARRIVED = 8; // px from its place: there, its words show

export function Orb({ bodies, at, cam, onOpen }: { bodies: LNode[]; at: Pos; cam: [MotionValue<number>, MotionValue<number>, MotionValue<number>]; onOpen: (id: string) => void }) {
  const { t } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const { show, dismiss } = useOrb();
  const reduced = useMemo(() => matchMedia("(prefers-reduced-motion: reduce)").matches, []);
  const orb = useRef<HTMLButtonElement>(null);
  const say = useRef<HTMLDivElement>(null);
  const st = useRef<{ cur: Pt | null; side?: Side | "home"; home?: Pt; key: string; raf: number | null; still: number }>({ cur: null, key: "", raf: null, still: 0 });
  const latest = useRef({ show, bodies, at });
  latest.current = { show, bodies, at };

  const frame = () => {
    const s = st.current;
    const { show, bodies, at } = latest.current;
    const o = orb.current, w = say.current;
    if (!o || !w) return true;
    const [cx, cy, k] = [cam[0].get(), cam[1].get(), cam[2].get()];
    // An open page as drawn (its height is its content's, not the room
    // the layout keeps for it); a bubble and its title as the layout has them.
    const pages = new Map<string, DOMRect>();
    for (const e of document.querySelectorAll<HTMLElement>("[data-page]")) pages.set(e.dataset.page!, e.getBoundingClientRect());
    const rect = (b: LNode, p: Pt): Rect => {
      const d = b.box ? pages.get(b.id) : undefined;
      if (d && d.width > 0) return { l: d.left, t: d.top, r: d.right, b: d.bottom };
      const [h, top, bot] = extent(b, p);
      return { l: cx + (p[0] - h) * k, t: cy + top * k, r: cx + (p[0] + h) * k, b: cy + bot * k };
    };
    const dock = document.querySelector(".cs-dock")?.getBoundingClientRect();
    const docked = show.mode === "rest";
    let to: Pt;
    let placed: Placed | null = null;
    if (docked) {
      to = dock ? [dock.left + dock.width / 2, dock.top + dock.height / 2] : [innerWidth / 2, innerHeight - 40];
      s.side = undefined;
      s.home = undefined;
    } else {
      const obstacles: Rect[] = bodies.filter((b) => at[b.id]).map((b) => rect(b, at[b.id]));
      for (const sel of [".cs", ".hud-corner", ".notices"]) {
        const r = document.querySelector(sel)?.getBoundingClientRect();
        if (r && r.width > 0) obstacles.push({ l: r.left, t: r.top, r: r.right, b: r.bottom });
      }
      const screen: Rect = { l: 8, t: TOP, r: innerWidth - 8, b: innerHeight - 8 };
      const words: [number, number] = w.childElementCount ? [Math.max(1, w.offsetWidth), w.offsetHeight] : [0, 0];
      if (s.key !== show.key) {
        s.key = show.key;
        s.side = undefined;
        s.home = undefined;
      }
      const target = show.node ? bodies.find((b) => b.id === show.node && at[b.id]) : undefined;
      if (target) {
        // A card waits in its open page: the orb points at its height.
        let anchor: number | undefined;
        const card = show.mode === "wait" && target.box ? document.querySelector(`[data-page="${CSS.escape(target.id)}"] .a2-proposal-pending`)?.getBoundingClientRect() : undefined;
        if (card && card.height > 0) anchor = card.top + Math.min(card.height, 60) / 2;
        placed = placeBeside(rect(target, at[target.id]), words, obstacles, screen, s.side, anchor);
      } else {
        placed = placeHome(words, obstacles, screen, s.home);
        s.home = homePoint(placed, words);
      }
      s.side = placed.side;
      to = placed.orb;
    }
    const cur = s.cur ? glide(s.cur, to, reduced) : to;
    const moved = !s.cur || cur[0] !== s.cur[0] || cur[1] !== s.cur[1];
    s.cur = cur;
    o.style.transform = `translate(${cur[0]}px, ${cur[1]}px)`;
    o.classList.toggle("docked", docked);
    const there = !docked && Math.hypot(to[0] - cur[0], to[1] - cur[1]) < ARRIVED;
    if (placed?.words) w.style.transform = `translate(${placed.words.l + cur[0] - to[0]}px, ${placed.words.t + cur[1] - to[1]}px)`;
    w.classList.toggle("on", there && !!placed?.words);
    w.classList.toggle("left", placed?.dir === -1);
    s.still = moved ? 0 : s.still + 1;
    return s.still > 30;
  };
  const kick = () => {
    const s = st.current;
    s.still = 0;
    if (s.raf !== null) return;
    const loop = () => {
      const done = frame();
      st.current.raf = done ? null : requestAnimationFrame(loop);
    };
    s.raf = requestAnimationFrame(loop);
  };
  // Whatever changed (the graph moved, what it says), it looks again; the
  // camera moving too.
  useLayoutEffect(() => {
    frame();
    kick();
  });
  useMotionValueEvent(cam[0], "change", kick);
  useMotionValueEvent(cam[1], "change", kick);
  useMotionValueEvent(cam[2], "change", kick);
  useEffect(() => {
    addEventListener("resize", kick);
    return () => {
      removeEventListener("resize", kick);
      if (st.current.raf !== null) cancelAnimationFrame(st.current.raf);
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const title = show.node ? titleOf(nodes[show.node], t("espace.external")) : "";
  const label =
    show.mode === "work" ? (show.node ? t("espace.orb.working.on", title) : t("espace.orb.working")) : show.node ? t("espace.orb.beside", title) : t("espace.orb.words");
  const open = (id: string) => onOpen(id);
  return (
    <>
      <button
        ref={orb}
        type="button"
        className={`orb ${show.mode}${show.mode === "rest" ? " docked" : ""}${show.mode === "work" ? " working" : ""}`}
        aria-label={label}
        title={show.mode === "rest" ? undefined : label}
        tabIndex={show.mode === "rest" ? -1 : 0}
        onClick={() => show.node && open(show.node)}
      />
      <div ref={say} className={`orb-say ${show.mode}${show.pinned ? " pinned" : ""}`} data-orb={show.key}>
        <Words show={show} dismiss={dismiss} open={open} />
      </div>
    </>
  );
}
