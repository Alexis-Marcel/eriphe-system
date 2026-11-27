import { useEffect, useRef, useState } from "react";
import type { GraphNode } from "./data/api";
import { applyNodes, step, store } from "./data/store";
import "./dev.css";

// The dev server's ?leger page: the graph at any size, to feel and measure
// how smoothly it draws. Fictitious nodes (clusters with their children,
// links across, nodes alone, varied weights, some waiting with their halo,
// some done), a live overlay (frames per second, the worst frame of the
// last two seconds, the bubbles in the page, the time the layout took to
// first come to rest), and buttons that change the graph live. Only on the
// dev server (dev.tsx), never in the built app.
// The measuring script (scripts/perf-graph.mjs, npm run perf:graph) drives
// it through window.leger.

export const SIZES = [25, 100, 250, 500, 1000, 2000, 3000];

// rng is a small seeded generator: the same size gives the same graph, so
// two runs (before and after a change) measure the same thing.
function rng(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const WORDS = ["Voyage", "Maison", "Factures", "Lectures", "Projet", "Jardin", "Santé", "Banque", "Photos", "Recettes", "Vélo", "Cours", "Impôts", "Musique", "Famille", "Travaux"];
const THINGS = ["devis", "billets", "plan", "liste", "relance", "rendez-vous", "notes", "budget", "contrat", "idées", "suivi", "brouillon"];

// legerGraph is n fictitious nodes: about one in seven a subject with its
// children (some with children of their own), one in twelve alone, a link
// across now and then; one in thirty waits for the user, one in six is done.
export function legerGraph(n: number, seed = 7): GraphNode[] {
  const r = rng(seed + n);
  const pick = <T,>(xs: T[]) => xs[Math.floor(r() * xs.length)];
  const t0 = Date.parse("2026-11-01T08:00:00Z");
  const out: GraphNode[] = [];
  const subjects: string[] = [];
  const made = (i: number) => new Date(t0 + i * 60_000).toISOString();
  for (let i = 0; i < n; i++) {
    const id = `lg${i}`;
    const roll = r();
    const status: GraphNode["status"] = roll < 0.033 ? "waits" : roll < 0.2 ? "done" : roll < 0.25 ? "running" : "idle";
    const x: GraphNode = { id, title: "", weight: 0.15 + r() * 0.8, status, created: made(i), updated: made(i), version: 1 };
    const kind = r();
    if (!subjects.length || kind < 0.14) {
      subjects.push(id);
      x.title = `${pick(WORDS)} ${subjects.length}`;
      x.weight = 0.5 + r() * 0.5;
    } else if (kind < 0.22) {
      x.title = `${pick(THINGS)} seul ${i}`;
    } else {
      // A child of a recent subject, or of one of its children.
      const near = out.slice(-12).filter((y) => y.parent || subjects.includes(y.id));
      const parent = near.length && r() < 0.3 ? pick(near).id : subjects[Math.max(0, subjects.length - 1 - Math.floor(r() * 3))];
      x.parent = parent;
      x.title = `${pick(THINGS)} ${i}`;
    }
    if (status === "running") x.line = "en cours…";
    if (r() < 0.06 && out.length) x.links = [out[Math.floor(r() * out.length)].id];
    if (r() < 0.02) x.maintained = ["suivi"];
    out.push(x);
  }
  return out;
}

type Stats = { fps: number; worst: number; p95: number; bubbles: number; stable: number | null; drawn: number | null; nodes: number; k: number };

// The frames as the page sees them: their times, for the overlay and the
// script.
const frames: number[] = [];
let loadedAt = 0;
let stableAt: number | null = null;
let stableIn: number | null = null;
// drawnIn: from the graph given to the first frame with bubbles in it.
let drawnIn: number | null = null;

function viewport() {
  return document.querySelector(".gr-viewport");
}

function stats(): Stats {
  const now = performance.now();
  const last = frames.filter((t) => t > now - 2000);
  const gaps = last.slice(1).map((t, i) => t - last[i]).sort((a, b) => a - b);
  return {
    fps: frames.filter((t) => t > now - 1000).length,
    worst: gaps.length ? gaps[gaps.length - 1] : 0,
    p95: gaps.length ? gaps[Math.floor(gaps.length * 0.95)] : 0,
    bubbles: document.querySelectorAll(".gr-node").length,
    stable: stableIn,
    drawn: drawnIn,
    nodes: Object.keys(store.getState().nodes).length,
    k: Math.round(scale() * 100) / 100,
  };
}

function load(n: number) {
  step(store, applyNodes, { reset: true, nodes: legerGraph(n) });
  restart();
}

function restart() {
  loadedAt = performance.now();
  stableAt = null;
  stableIn = null;
}

function add(k: number) {
  const nodes = Object.values(store.getState().nodes);
  const more = legerGraph(nodes.length + k, 11).slice(nodes.length).map((x) => ({ ...x, id: `${x.id}m`, parent: x.parent && store.getState().nodes[x.parent] ? x.parent : undefined, links: undefined }));
  step(store, applyNodes, { nodes: more });
  restart();
}

function remove(k: number) {
  const nodes = Object.values(store.getState().nodes).sort((a, b) => a.created.localeCompare(b.created) || a.id.localeCompare(b.id));
  const keep = nodes.slice(0, Math.max(0, nodes.length - k));
  step(store, applyNodes, { reset: true, nodes: keep });
  restart();
}

// resize changes the weight, so the size, of every subject: the layout warms up.
let grown = false;
function resize() {
  grown = !grown;
  const nodes = Object.values(store.getState().nodes).filter((x) => !x.parent);
  step(store, applyNodes, { nodes: nodes.map((x) => ({ ...x, weight: grown ? Math.min(1.6, x.weight + 0.5) : Math.max(0.2, x.weight - 0.5) })) });
  restart();
}

// sample records the frames and the long tasks for ms milliseconds, while
// move (if any) is called at each frame: what the script measures.
function sample(ms: number, move?: (t: number) => void): Promise<{ fps: number; worst: number; p95: number; longTasks: number; longMs: number; frames: number }> {
  return new Promise((done) => {
    const ts: number[] = [];
    const long: number[] = [];
    let po: PerformanceObserver | null = null;
    try {
      po = new PerformanceObserver((l) => {
        for (const e of l.getEntries()) long.push(e.duration);
      });
      po.observe({ type: "longtask" });
    } catch {
      po = null;
    }
    const start = performance.now();
    const tick = (t: number) => {
      ts.push(t);
      if (t - start < ms) {
        move?.(t - start);
        requestAnimationFrame(tick);
        return;
      }
      po?.disconnect();
      const gaps = ts.slice(1).map((x, i) => x - ts[i]).sort((a, b) => a - b);
      done({
        fps: Math.round(((ts.length - 1) * 1000) / (ts[ts.length - 1] - ts[0])),
        worst: Math.round(gaps[gaps.length - 1] ?? 0),
        p95: Math.round(gaps[Math.floor(gaps.length * 0.95)] ?? 0),
        longTasks: long.length,
        longMs: Math.round(long.reduce((a, b) => a + b, 0)),
        frames: ts.length,
      });
    };
    requestAnimationFrame(tick);
  });
}

// pan moves the camera across the graph through the wheel, steadily, out
// for 1.5 s and back: what comes into view is drawn as it comes.
function pan(t: number) {
  const v = viewport();
  if (!v) return;
  const dir = t < 1500 ? 1 : -1;
  v.dispatchEvent(new WheelEvent("wheel", { deltaX: 12 * dir, deltaY: 4 * Math.sin(t / 400), clientX: innerWidth / 2, clientY: innerHeight / 2, bubbles: true, cancelable: true }));
}

// zoomTo sets the camera's scale about the screen's middle, through the
// wheel (ctrl), as a pinch would: the script measures a pan at scale 1.
function zoomTo(k: number) {
  const v = viewport();
  const now = scale();
  if (!v || !now) return;
  v.dispatchEvent(new WheelEvent("wheel", { deltaY: -Math.log(k / now) / 0.006, ctrlKey: true, clientX: innerWidth / 2, clientY: innerHeight / 2, bubbles: true, cancelable: true }));
}

// scale is the camera's scale now, as drawn.
function scale(): number {
  const w = document.querySelector(".gr-world");
  return w ? new DOMMatrix(getComputedStyle(w).transform).a : 0;
}

// panZoom moves the camera as a hand would, through the wheel: a pan, then
// a zoom in, a pan, a zoom out.
function panZoom(t: number) {
  const v = viewport();
  if (!v) return;
  const phase = Math.floor(t / 500) % 2;
  const out = Math.floor(t / 1000) % 2 ? 1 : -1;
  const cx = innerWidth / 2, cy = innerHeight / 2;
  const ev = phase === 0
    ? new WheelEvent("wheel", { deltaX: 14 * Math.cos(t / 300), deltaY: 10 * Math.sin(t / 300), clientX: cx, clientY: cy, bubbles: true, cancelable: true })
    : new WheelEvent("wheel", { deltaY: out * 3, ctrlKey: true, clientX: cx, clientY: cy, bubbles: true, cancelable: true });
  v.dispatchEvent(ev);
}

declare global {
  interface Window {
    leger?: { load(n: number): void; add(k: number): void; remove(k: number): void; resize(): void; stats(): Stats; sample: typeof sample; pan: typeof pan; panZoom: typeof panZoom; zoomTo: typeof zoomTo };
  }
}

export function startLeger() {
  const asked = Number(new URLSearchParams(location.search).get("n"));
  window.leger = { load, add, remove, resize, stats, sample, pan, panZoom, zoomTo };
  load(asked > 0 ? asked : 100);
  let raf = 0;
  const loop = (t: number) => {
    frames.push(t);
    if (drawnIn === null && document.querySelector(".gr-node")) drawnIn = Math.round(t - loadedAt);
    while (frames.length && frames[0] < t - 2500) frames.shift();
    if (stableAt === null && t - loadedAt > 50 && viewport()?.hasAttribute("data-still")) {
      stableAt = t;
      stableIn = Math.round(t - loadedAt);
    }
    raf = requestAnimationFrame(loop);
  };
  raf = requestAnimationFrame(loop);
  return () => {
    cancelAnimationFrame(raf);
    delete window.leger;
  };
}

// LegerPanel is the overlay: the figures, a few times a second (written
// straight on their elements, not drawn again), and the controls.
export function LegerPanel() {
  const out = useRef<HTMLPreElement>(null);
  const [n, setN] = useState(() => Number(new URLSearchParams(location.search).get("n")) || 100);
  const [shut, setShut] = useState(false);
  useEffect(() => {
    const id = setInterval(() => {
      const s = stats();
      if (out.current)
        out.current.textContent = [
          `nœuds    ${s.nodes}`,
          `bulles   ${s.bubbles}`,
          `i/s      ${s.fps}`,
          `pire     ${s.worst.toFixed(1)} ms (2 s)`,
          `p95      ${s.p95.toFixed(1)} ms`,
          `au repos ${s.stable === null ? "…" : `${s.stable} ms`}`,
          `échelle  ${s.k}`,
        ].join("\n");
    }, 250);
    return () => clearInterval(id);
  }, []);
  const go = (k: number) => {
    setN(k);
    load(k);
  };
  const idx = Math.max(0, SIZES.findIndex((s) => s >= n));
  return (
    <aside className={`dev-phys dev-leger${shut ? " shut" : ""}`} aria-label="Fluidité">
      <header>
        <b>?leger — nœuds fictifs</b>
        <button type="button" onClick={() => setShut(!shut)} aria-expanded={!shut}>
          {shut ? "▸" : "▾"}
        </button>
      </header>
      <pre ref={out} className="dev-leger-out" />
      {!shut && (
        <>
          <input type="range" min={0} max={SIZES.length - 1} step={1} value={idx} onChange={(e) => go(SIZES[Number(e.target.value)])} aria-label="Taille" />
          <div className="dev-phys-row">
            {SIZES.map((k) => (
              <button key={k} type="button" onClick={() => go(k)} aria-pressed={k === n}>
                {k}
              </button>
            ))}
          </div>
          <div className="dev-phys-row">
            <button type="button" onClick={() => add(50)}>
              +50
            </button>
            <button type="button" onClick={() => remove(50)}>
              −50
            </button>
            <button type="button" onClick={resize}>
              Changer une taille
            </button>
          </div>
        </>
      )}
    </aside>
  );
}
