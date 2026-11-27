import { drawnRadius, labelAlpha, LOD, type Item, type View } from "./lod";
import { declutter, nameAlpha, padOf, zoneAlpha, zoneFamilies, zoneShape, type Family, type Shape } from "./zones";

// The canvas of a big graph (lod.ts): every node a disc, every link a
// line, painted again only when something moved (a frame of the layout,
// the camera, a hover, a bubble come or gone), never at rest. What it
// paints is what a bubble looks like, so that a disc becomes its bubble
// without a jump: the same place and radius, the core's opacity (its
// weight, fainter once done), the label in the bubble's font and place,
// in the graph's units. A node that waits keeps its sign at every scale:
// a violet ring and halo (never a word, never a yellow title), at least a
// few pixels wide on screen. A family (a root and what is under it) has a
// soft colour of its own, low in saturation, away from the halo's violet.
// One path per style: the links in one stroke, the discs by colour.

// A Scene is the graph as the canvas paints it, worked out once per graph
// (not per frame), in the layout's order.
export type Scene = {
  items: Item[]; // id, radius, waits: what lod.ts decides and hits on
  depth: Uint8Array;
  alpha: Float32Array; // the core's opacity
  done: Uint8Array;
  run: Uint8Array;
  fam: Int16Array; // the family's colour (-1: none)
  sleep: Uint8Array; // asleep (life.ts): a pale dot
  root: Int32Array; // its root's index (-1: none placed)
  families: Family[]; // the families drawn as zones (zones.ts)
  titles: string[];
  codes: string[]; // a root's short word, inside its disc
  order: Int32Array; // the nodes by radius, the biggest first: the labels' order
  links: Int32Array; // the links, two indices each
  index: Map<string, number>;
};

export type SceneNode = { id: string; r: number; depth: number; light: number; status: string; fam: number; title: string; code: string; root?: string; sleeps?: boolean };

export function scene(nodes: readonly SceneNode[], links: readonly [string, string][]): Scene {
  const n = nodes.length;
  const s: Scene = {
    items: nodes.map((x) => ({ id: x.id, r: x.r, waits: x.status === "waits", sleeps: !!x.sleeps })),
    depth: new Uint8Array(n),
    alpha: new Float32Array(n),
    done: new Uint8Array(n),
    run: new Uint8Array(n),
    fam: new Int16Array(n),
    sleep: new Uint8Array(n),
    root: new Int32Array(n),
    families: [],
    titles: nodes.map((x) => x.title),
    codes: nodes.map((x) => x.code),
    order: new Int32Array(n),
    links: new Int32Array(links.length * 2),
    index: new Map(nodes.map((x, i) => [x.id, i])),
  };
  nodes.forEach((x, i) => {
    s.depth[i] = Math.min(255, x.depth);
    s.done[i] = x.status === "done" ? 1 : 0;
    s.run[i] = x.status === "running" ? 1 : 0;
    s.fam[i] = x.fam;
    s.sleep[i] = x.sleeps ? 1 : 0;
    s.root[i] = x.root === undefined ? -1 : (s.index.get(x.root) ?? -1);
    // as a bubble's core (Graph.tsx, Bubble)
    s.alpha[i] = x.status === "done" ? Math.max(0.35, x.light * 0.8) : Math.max(0.5, x.light);
  });
  const by = nodes.map((_, i) => i).sort((a, b) => nodes[b].r - nodes[a].r || a - b);
  s.order.set(by);
  let j = 0;
  for (const [a, b] of links) {
    const ia = s.index.get(a), ib = s.index.get(b);
    if (ia === undefined || ib === undefined) continue;
    s.links[j++] = ia;
    s.links[j++] = ib;
  }
  s.links = s.links.slice(0, j);
  s.families = zoneFamilies(s.root, s.fam, (i) => s.items[i].waits);
  return s;
}

// What changes between paints without changing the scene.
export type State = {
  mounted: ReadonlyMap<string, unknown>; // the nodes drawn as bubbles now
  swapped: ReadonlyMap<string, number>; // when each became a bubble: its disc fades as its bubble comes
  open: ReadonlySet<string>; // covered by their page
  hover?: string | null; // the node hovered: too small to be a bubble, its label is painted readable
  near: ReadonlySet<string> | null; // a hover: these stay sharp, the rest fades
  nearLinks: ReadonlySet<number> | null; // ... and these links, by index
  moved?: number; // changes as the layout moves: the zones are drawn again (at most every ZONES ms)
};

export const EASE = 160; // ms: a disc's radius eases to a new one (as a bubble's core, espace.css --morph)
export const ZONES = 120; // ms: the zones drawn again at most this often while the layout moves
const TEX = 420; // the zones' picture's longer side, in px
const PAD = 40; // a zone's margin round each of its nodes, in the graph's units

export const FADE = 180; // ms: a disc fades as its bubble fades in (espace.css, lod-in)
export const LABELS = 300; // labels at most in a paint
const STEPS = 16; // the discs' steps of opacity

// paintStats: the last paints' cost, for the ?leger page.
export const paintStats = { ms: 0, last: 0, paints: 0, discs: 0, labels: 0, links: 0, zones: 0, zoneBuilds: 0 };

type Theme = { bg: string; fg: string; fg3: string; ring: string; edge: string; edgeOn: string; accent: string; run: string; ok: string; neutral: string; sleep: string; fills: string[]; dots: string[]; zones: string[]; names: string[]; zoneA: number; font: string; mono: string };

const hex = (s: string): [number, number, number] | null => {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(s.trim());
  if (!m) return null;
  const h = m[1].length === 3 ? [...m[1]].map((c) => c + c).join("") : m[1];
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
};
const rgb = (c: [number, number, number], a = 1) => `rgba(${c[0]}, ${c[1]}, ${c[2]}, ${a})`;
// mix is a in share of b, as CSS's color-mix(in srgb, a share, b).
const mix = (a: [number, number, number], share: number, b: [number, number, number]): [number, number, number] => [0, 1, 2].map((i) => Math.round(a[i] * share + b[i] * (1 - share))) as [number, number, number];

export const FAMILIES = 8; // the palette's colours (espace.css, --fam0 to --fam7)

// readTheme reads the colours from the page's custom properties
// (espace.css), as the bubbles use them: light or dark.
function readTheme(): Theme {
  const cs = getComputedStyle(document.documentElement);
  const v = (k: string, d: string) => cs.getPropertyValue(k).trim() || d;
  const c = (k: string, d: string) => hex(v(k, d)) ?? hex(d)!;
  const bg = c("--bg", "#f7f7f8"), card = c("--card", "#ffffff"), fg = c("--fg", "#18181b"), fill = c("--fill-strong", "#e9e9ec");
  const share = (parseFloat(v("--fam-mix", "38%")) || 38) / 100;
  const fams = Array.from({ length: FAMILIES }, (_, i) => c(`--fam${i}`, "#7b828c"));
  return {
    bg: rgb(bg),
    fg: rgb(fg),
    fg3: rgb(c("--fg3", "#6b6b74")),
    ring: rgb(c("--line-strong", "#d4d4d8")),
    edge: rgb(fg, 0.24),
    edgeOn: rgb(fg, 0.55),
    accent: rgb(c("--accent", "#5b5bd6")),
    run: rgb(c("--run", "#3b6ea8")),
    ok: rgb(c("--ok", "#30a46c")),
    neutral: rgb(mix(card, 0.5, fill)),
    sleep: rgb(mix(fg, 0.16, bg)),
    fills: fams.map((f) => rgb(mix(f, share, card))),
    dots: fams.map((f) => rgb(f)),
    // a zone: its family's hue, greyed toward the page (low in saturation)
    zones: fams.map((f) => rgb(mix(f, 0.75, mix(fg, 0.5, bg)))),
    names: fams.map((f) => rgb(mix(f, 0.45, fg))),
    zoneA: 0.2,
    font: v("--font", "ui-sans-serif, system-ui, sans-serif"),
    mono: v("--mono", "ui-monospace, monospace"),
  };
}

// discR is a disc's radius on screen: the size knob's, a done node's a
// little smaller, both eased to its radius as it nears the threshold.
function discR(r: number, done: boolean, k: number): number {
  let rr = drawnRadius(r, k) * k;
  if (done) rr *= 0.82 + 0.18 * Math.min(1, (r * k) / LOD.bubbleAt);
  return rr;
}

export class Painter {
  private ctx: CanvasRenderingContext2D;
  private dpr = 1;
  private w = 0;
  private h = 0;
  private theme: Theme;
  private halo: HTMLCanvasElement;
  private sx = new Float32Array(0);
  private sy = new Float32Array(0);
  private sr = new Float32Array(0);
  private bucket = new Int32Array(0);
  // the font's ascent and descent, per px of its size: a label's baseline
  // is where the page puts it (a line 1.5 times its size, the text in the
  // middle of it)
  private asc = 0.9;
  private desc = 0.22;
  // each disc's radius as drawn, easing to its node's (cur, for the scene curOf)
  private cur = new Float32Array(0);
  private curOf: Scene | null = null;
  private lastT = 0;
  // the zones as last drawn, in the graph's units (zones.ts)
  private zone: { tex: HTMLCanvasElement; breath: HTMLCanvasElement | null; x0: number; y0: number; t: number; m: number; shapes: Shape[]; scene: Scene; theme: Theme; moved?: number; at: number } | null = null;
  private union: HTMLCanvasElement | null = null;
  private breathCtx: CanvasRenderingContext2D | null = null;
  private breathing = true;
  private widths = new Map<string, number>(); // the family names' half widths, by size and name

  // breath: a canvas under this one, for the zones of the families where
  // something waits, its opacity breathing in CSS (espace.css): no paint
  // at rest for it.
  constructor(private canvas: HTMLCanvasElement, ctx: CanvasRenderingContext2D, private breath: HTMLCanvasElement | null = null) {
    this.ctx = ctx;
    try {
      this.breathCtx = breath?.getContext("2d") ?? null;
    } catch {
      this.breathCtx = null;
    }
    this.theme = readTheme();
    this.halo = this.sprite();
    this.metrics();
  }

  private metrics() {
    this.ctx.font = `600 100px ${this.theme.font}`;
    const m = this.ctx.measureText?.("Hg");
    if (m?.fontBoundingBoxAscent) {
      this.asc = m.fontBoundingBoxAscent / 100;
      this.desc = m.fontBoundingBoxDescent / 100;
    }
  }

  static on(canvas: HTMLCanvasElement, breath: HTMLCanvasElement | null = null): Painter | null {
    let ctx: CanvasRenderingContext2D | null = null;
    try {
      ctx = canvas.getContext("2d");
    } catch {
      ctx = null;
    }
    return ctx ? new Painter(canvas, ctx, breath) : null;
  }

  // retheme reads the colours again (the theme changed).
  retheme() {
    this.theme = readTheme();
    this.widths.clear();
    this.halo = this.sprite();
    this.metrics();
  }

  // The halo, painted once: the bubble's (espace.css, .gr-halo), a violet
  // glow fading out at 62 % of its radius.
  private sprite(): HTMLCanvasElement {
    const c = document.createElement("canvas");
    c.width = c.height = 64;
    const g = c.getContext("2d");
    if (!g) return c;
    const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
    const a = hex(getComputedStyle(document.documentElement).getPropertyValue("--accent").trim()) ?? [91, 91, 214];
    grad.addColorStop(0, rgb(a, 0.34));
    grad.addColorStop(0.62, rgb(a, 0));
    g.fillStyle = grad;
    g.fillRect(0, 0, 64, 64);
    return c;
  }

  resize(w: number, h: number) {
    const dpr = Math.min(2, globalThis.devicePixelRatio || 1);
    if (w === this.w && h === this.h && dpr === this.dpr) return;
    this.w = w;
    this.h = h;
    this.dpr = dpr;
    this.canvas.width = Math.round(w * dpr);
    this.canvas.height = Math.round(h * dpr);
    if (this.breath) {
      this.breath.width = this.canvas.width;
      this.breath.height = this.canvas.height;
    }
  }

  // ease moves each disc's radius toward its node's, at now; it tells
  // whether some still move.
  private ease(s: Scene, now: number): boolean {
    const n = s.items.length;
    if (this.curOf !== s) {
      const old = this.curOf, oc = this.cur, nc = new Float32Array(n);
      for (let i = 0; i < n; i++) {
        const j = old?.index.get(s.items[i].id);
        nc[i] = j !== undefined && old ? oc[j] : s.items[i].r;
      }
      this.cur = nc;
      this.curOf = s;
    }
    const dt = this.lastT ? Math.max(0, Math.min(100, now - this.lastT)) : 0;
    this.lastT = now;
    const e = 1 - Math.exp(-dt / EASE);
    let moving = false;
    const cur = this.cur;
    for (let i = 0; i < n; i++) {
      const to = s.items[i].r;
      let c = cur[i];
      if (c === to) continue;
      c += (to - c) * e;
      if (Math.abs(to - c) < 0.05) c = to;
      else moving = true;
      cur[i] = c;
    }
    return moving;
  }

  // zones draws the families' zones again when the layout moved (at most
  // every ZONES ms), the scene or the theme changed: the union of their
  // nodes' discs widened by PAD, of their colour, blurred, in a picture of
  // TEX px in the graph's units. It tells whether a drawing waits.
  private zones(s: Scene, xy: ArrayLike<number>, st: State, now: number): boolean {
    if (!LOD.zones || !s.families.length) {
      this.zone = null;
      return false;
    }
    const z = this.zone;
    const same = z && z.scene === s && z.theme === this.theme;
    if (same && z.moved === st.moved) return false;
    if (same && now - z.at < ZONES) return true;
    const t0 = performance.now();
    const shapes = s.families.map((f) => zoneShape(f, xy));
    const cur = this.cur;
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    const pads = shapes.map((sh) => padOf(sh, PAD));
    shapes.forEach((sh, j) => {
      for (const i of sh.keep) {
        const R = cur[i] + pads[j], x = xy[i * 2], y = xy[i * 2 + 1];
        if (x - R < x0) x0 = x - R;
        if (y - R < y0) y0 = y - R;
        if (x + R > x1) x1 = x + R;
        if (y + R > y1) y1 = y + R;
      }
    });
    if (!(x1 > x0 && y1 > y0)) {
      this.zone = null;
      return false;
    }
    const t = TEX / Math.max(x1 - x0, y1 - y0);
    const blur = Math.max(2, (pads.reduce((a, b) => a + b, 0) / pads.length) * t * 0.6);
    const m = Math.ceil(blur * 2.5);
    const W = Math.ceil((x1 - x0) * t) + m * 2, H = Math.ceil((y1 - y0) * t) + m * 2;
    const U = (this.union ??= document.createElement("canvas"));
    const draw = (only: (sh: Shape, f: Family) => boolean, into: HTMLCanvasElement | null): HTMLCanvasElement | null => {
      if (U.width !== W || U.height !== H) {
        U.width = W;
        U.height = H;
      }
      const u = U.getContext("2d");
      if (!u) return null;
      u.clearRect(0, 0, W, H);
      let any = false;
      shapes.forEach((sh, j) => {
        const f = s.families[j];
        if (!only(sh, f) || !sh.keep.length) return;
        any = true;
        u.fillStyle = this.theme.zones[f.fam % FAMILIES];
        u.beginPath();
        for (const i of sh.keep) {
          const px = (xy[i * 2] - x0) * t + m, py = (xy[i * 2 + 1] - y0) * t + m, r = (cur[i] + pads[j]) * t;
          u.moveTo(px + r, py);
          u.arc(px, py, r, 0, Math.PI * 2);
        }
        u.fill();
      });
      if (!any) return null;
      const out = into ?? document.createElement("canvas");
      out.width = W;
      out.height = H;
      const o: CanvasRenderingContext2D | null = out.getContext("2d");
      if (!o) return null;
      if (typeof (o as { filter?: unknown }).filter === "string") {
        o.filter = `blur(${blur.toFixed(1)}px)`;
        o.drawImage(U, 0, 0);
        o.filter = "none";
      } else {
        // no filter: drawn small and back, smoothed
        o.imageSmoothingEnabled = true;
        o.drawImage(U, 0, 0, W / 4, H / 4);
        o.globalCompositeOperation = "copy";
        o.drawImage(out, 0, 0, W / 4, H / 4, 0, 0, W, H);
        o.globalCompositeOperation = "source-over";
      }
      return out;
    };
    const tex = draw(() => true, z?.tex ?? null);
    if (!tex) {
      this.zone = null;
      return false;
    }
    const breath = this.breathCtx ? draw((_, f) => f.waits, z?.breath ?? null) : null;
    this.zone = { tex, breath, x0, y0, t, m, shapes, scene: s, theme: this.theme, moved: st.moved, at: now };
    paintStats.zones = performance.now() - t0;
    paintStats.zoneBuilds++;
    return false;
  }

  // paint paints the scene for the camera v, the nodes where xy has them;
  // it tells whether a fade goes on (paint again next frame).
  paint(s: Scene, xy: ArrayLike<number>, v: View, st: State, now: number): boolean {
    const t0 = performance.now();
    const { ctx, theme: T } = this;
    const n = s.items.length;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    if (xy.length < n * 2) return false;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    if (this.sx.length !== n) {
      this.sx = new Float32Array(n);
      this.sy = new Float32Array(n);
      this.sr = new Float32Array(n);
      this.bucket = new Int32Array(n);
    }
    const { sx, sy, sr, bucket } = this;
    const k = v.k, W = this.w, H = this.h;
    let fading = this.ease(s, now);
    const cur = this.cur;
    if (this.zones(s, xy, st, now)) fading = true;
    // each node on screen: where, how big, how much of its disc shows (0:
    // its bubble or its page in its place)
    const show = new Float32Array(n);
    for (let i = 0; i < n; i++) {
      const id = s.items[i].id;
      sx[i] = xy[i * 2] * k + v.x;
      sy[i] = xy[i * 2 + 1] * k + v.y;
      sr[i] = discR(cur[i], s.done[i] === 1, k);
      if (st.open.has(id)) continue;
      let a = 1;
      if (st.mounted.has(id)) {
        const t = now - (st.swapped.get(id) ?? -Infinity);
        if (t >= FADE) continue;
        a = 1 - t / FADE;
        fading = true;
      }
      if (st.near && !st.near.has(id)) a *= 0.2;
      show[i] = a;
    }
    const seen = (i: number, m: number) => sx[i] + m >= 0 && sx[i] - m <= W && sy[i] + m >= 0 && sy[i] - m <= H;

    // the zones, under all: their picture, moved and scaled by the camera
    const Z = this.zone, za = Z ? T.zoneA * zoneAlpha(k) : 0;
    // the breathing layer shows only while it has a zone to breathe: hidden,
    // the page does not compose it at each frame
    const bc = this.breathCtx, breathes = !!(bc && Z?.breath && za > 0.01);
    if (bc && (breathes || this.breathing)) {
      bc.setTransform(1, 0, 0, 1, 0, 0);
      bc.clearRect(0, 0, this.canvas.width, this.canvas.height);
    }
    if (this.breath && breathes !== this.breathing) {
      this.breath.style.visibility = breathes ? "" : "hidden";
      this.breathing = breathes;
    }
    if (Z && za > 0.01) {
      const zx = (Z.x0 - Z.m / Z.t) * k + v.x, zy = (Z.y0 - Z.m / Z.t) * k + v.y, zw = (Z.tex.width / Z.t) * k, zh = (Z.tex.height / Z.t) * k;
      ctx.imageSmoothingEnabled = true;
      ctx.globalAlpha = za * (st.near ? 0.5 : 1);
      ctx.drawImage(Z.tex, zx, zy, zw, zh);
      if (bc && breathes && Z.breath) {
        bc.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
        bc.globalAlpha = Math.min(1, za * 1.6);
        bc.drawImage(Z.breath, zx, zy, zw, zh);
      }
    }

    // the links: one stroke; those near a hover in a second
    let nl = 0;
    const L = s.links;
    ctx.lineWidth = 1;
    ctx.strokeStyle = T.edge;
    // far out, the links fade (a thousand lines are a grey cloud): a
    // third of their ink at the smallest scale, all of it from scale 1
    ctx.globalAlpha = (st.near ? 0.12 : 1) * Math.max(0.35, Math.min(1, 0.35 + (0.65 * (k - 0.25)) / 0.75));
    ctx.beginPath();
    for (let j = 0; j < L.length; j += 2) {
      const a = L[j], b = L[j + 1];
      const ax = sx[a], ay = sy[a], bx = sx[b], by = sy[b];
      if ((ax < 0 && bx < 0) || (ax > W && bx > W) || (ay < 0 && by < 0) || (ay > H && by > H)) continue;
      if (st.nearLinks?.has(j >> 1)) continue;
      ctx.moveTo(ax, ay);
      ctx.lineTo(bx, by);
      nl++;
    }
    ctx.stroke();
    if (st.nearLinks?.size) {
      ctx.globalAlpha = 1;
      ctx.strokeStyle = T.edgeOn;
      ctx.beginPath();
      for (const j of st.nearLinks) {
        const a = L[j * 2], b = L[j * 2 + 1];
        if (a === undefined) continue;
        ctx.moveTo(sx[a], sy[a]);
        ctx.lineTo(sx[b], sy[b]);
      }
      ctx.stroke();
    }

    // the halos, under the discs: 2.5 times the radius, as a bubble's, and
    // never less than 9 px on screen
    for (let i = 0; i < n; i++) {
      if (!s.items[i].waits || show[i] === 0) continue;
      const hr = Math.max(9, sr[i] * 2.5);
      if (!seen(i, hr)) continue;
      ctx.globalAlpha = show[i];
      ctx.drawImage(this.halo, sx[i] - hr, sy[i] - hr, hr * 2, hr * 2);
    }

    // the discs, by colour and opacity: a bucket each (the family's colour,
    // or none, then STEPS steps of opacity: close enough to the bubble's
    // own that the swap does not show)
    const colour = LOD.color;
    const B = (FAMILIES + 1) * STEPS;
    const count = new Int32Array(B + 1);
    let discs = 0;
    let sleeping = 0;
    for (let i = 0; i < n; i++) {
      bucket[i] = -1;
      if (show[i] === 0 || !seen(i, sr[i] + 2)) continue;
      if (s.sleep[i]) {
        sleeping++;
        continue;
      }
      const f = colour ? s.fam[i] + 1 : 0;
      const q = Math.min(STEPS - 1, Math.round(s.alpha[i] * show[i] * STEPS) - 1);
      bucket[i] = f * STEPS + Math.max(0, q);
      count[bucket[i] + 1]++;
      discs++;
    }
    for (let b = 0; b < B; b++) count[b + 1] += count[b];
    // the nodes asleep: small pale dots, under the others, one path
    if (sleeping) {
      ctx.globalAlpha = st.near ? 0.25 : 0.9;
      ctx.fillStyle = T.sleep;
      ctx.beginPath();
      for (let i = 0; i < n; i++) {
        if (!s.sleep[i] || show[i] === 0 || !seen(i, sr[i] + 2)) continue;
        const r = Math.max(0.9, sr[i]);
        if (r < 2) ctx.rect(sx[i] - r, sy[i] - r, r * 2, r * 2);
        else {
          ctx.moveTo(sx[i] + r, sy[i]);
          ctx.arc(sx[i], sy[i], r, 0, Math.PI * 2);
        }
      }
      ctx.fill();
      discs += sleeping;
    }
    const sorted = new Int32Array(discs - sleeping);
    const fill = count.slice(0, B);
    for (let i = 0; i < n; i++) if (bucket[i] >= 0) sorted[fill[bucket[i]]++] = i;
    for (let b = 0; b < B; b++) {
      const from = count[b], to = count[b + 1];
      if (from === to) continue;
      const f = Math.floor(b / STEPS) - 1, q = b % STEPS;
      ctx.globalAlpha = (q + 1) / STEPS;
      ctx.beginPath();
      let rings = false;
      for (let j = from; j < to; j++) {
        const i = sorted[j], r = sr[i];
        if (r < 1.5) {
          ctx.rect(sx[i] - 1.5, sy[i] - 1.5, 3, 3);
          continue;
        }
        ctx.moveTo(sx[i] + r, sy[i]);
        ctx.arc(sx[i], sy[i], r, 0, Math.PI * 2);
        if (r > 3) rings = true;
      }
      // a tiny disc is a dot of its family's colour, full; a bigger one
      // its soft fill, ringed as a bubble
      ctx.fillStyle = f >= 0 ? T.fills[f] : T.neutral;
      ctx.fill();
      if (rings) {
        ctx.strokeStyle = T.ring;
        ctx.stroke();
      }
    }
    // the dots of tiny discs: their family's colour, so that a family reads
    // from far
    if (colour && k < 0.6) {
      for (let b = STEPS; b < B; b++) {
        const from = count[b], to = count[b + 1];
        if (from === to) continue;
        ctx.globalAlpha = 0.55 * (((b % STEPS) + 1) / STEPS);
        ctx.fillStyle = T.dots[Math.floor(b / STEPS) - 1];
        ctx.beginPath();
        for (let j = from; j < to; j++) {
          const i = sorted[j], r = sr[i];
          if (r >= 4) continue;
          const d = Math.max(1, r * 0.7);
          ctx.rect(sx[i] - d, sy[i] - d, d * 2, d * 2);
        }
        ctx.fill();
      }
    }
    // the rings: violet for what waits (2 px, as its bubble's core), at
    // every scale; the run colour for what runs, once big enough not to
    // be taken for the violet
    // (dashed, as its bubble's: never taken for the violet ring, however big
    // its life makes it)
    for (const [flag, style, lw, dash] of [
      [(i: number) => s.items[i].waits, T.accent, 2, false],
      [(i: number) => s.run[i] === 1 && sr[i] >= 5, T.run, 1.5, true],
    ] as const) {
      ctx.strokeStyle = style;
      ctx.lineWidth = lw;
      ctx.setLineDash?.(dash ? [Math.max(2, 6 * k), Math.max(2, 5 * k)] : []);
      for (let i = 0; i < n; i++) {
        if (bucket[i] < 0 || !flag(i)) continue;
        ctx.globalAlpha = show[i];
        ctx.beginPath();
        ctx.arc(sx[i], sy[i], Math.max(2.5, sr[i] + lw / 2), 0, Math.PI * 2);
        ctx.stroke();
      }
    }
    ctx.lineWidth = 1;
    ctx.setLineDash?.([]);

    // the labels, the biggest nodes first: in the bubble's font and place
    // (espace.css, .gr-label), in the graph's units; a root's short word in
    // its disc
    let labels = 0;
    ctx.textAlign = "center";
    ctx.textBaseline = "alphabetic";
    const base = (1.5 - this.asc - this.desc) / 2 + this.asc; // the baseline under the label's top, per px of its size
    ctx.lineJoin = "round";
    let font = "";
    for (let o = 0; o < n && labels < LABELS; o++) {
      const i = s.order[o];
      const r = cur[i];
      const la = labelAlpha(r * k) * (s.sleep[i] ? 0.6 : 1);
      if (la <= 0) {
        if (s.sleep[i]) continue;
        break; // the rest are smaller
      }
      if (show[i] === 0) continue;
      const root = s.depth[i] === 0;
      const fs = (root ? 14.5 : 12.5) * k;
      if (fs < 4) continue;
      const R = r + (root ? 12 : 6);
      const y = sy[i] + (R + (root ? 14 : 8)) * k + fs * base;
      if (!seen(i, sr[i]) && (sx[i] < -300 || sx[i] > W + 300 || y > H + fs || y < -fs)) continue;
      const f = `${root ? 600 : 500} ${fs.toFixed(1)}px ${T.font}`;
      if (f !== font) ctx.font = font = f;
      ctx.globalAlpha = la * show[i];
      ctx.strokeStyle = T.bg;
      ctx.lineWidth = Math.max(2, fs * 0.28);
      ctx.strokeText(s.titles[i], sx[i], y);
      ctx.fillStyle = s.done[i] || s.sleep[i] ? T.fg3 : T.fg;
      ctx.fillText(s.titles[i], sx[i], y);
      if (root && sr[i] >= 10 && seen(i, sr[i])) {
        ctx.font = font = `${(10.5 * k).toFixed(1)}px ${T.mono}`;
        if ("letterSpacing" in ctx) ctx.letterSpacing = `${(10.5 * 0.14 * k).toFixed(2)}px`; // as .gr-code
        ctx.textBaseline = "middle";
        ctx.fillStyle = T.fg3;
        ctx.globalAlpha = show[i] * s.alpha[i];
        ctx.fillText(s.codes[i], sx[i], sy[i]);
        if ("letterSpacing" in ctx) ctx.letterSpacing = "0px";
        ctx.textBaseline = "alphabetic";
      }
      labels++;
    }
    // far out, each zone's family name, large in its middle; it fades as
    // its root's own label comes (they cross-fade)
    if (Z) {
      const most = Z.shapes.reduce((a, sh) => Math.max(a, sh.keep.length), 1);
      ctx.textBaseline = "middle";
      const names: { j: number; px: number; py: number; fs: number; na: number }[] = [];
      const boxes: [number, number, number, number][] = [];
      for (let j = 0; j < Z.shapes.length; j++) {
        const sh = Z.shapes[j], f = s.families[j];
        if (sh.keep.length < 2) continue;
        const na = nameAlpha(labelAlpha(cur[f.root] * k));
        if (na <= 0.02) continue;
        const px = sh.cx * k + v.x, py = sh.cy * k + v.y;
        const fs = Math.round(12 + 9 * Math.sqrt(sh.keep.length / most));
        if (px < -300 || px > W + 300 || py < -fs || py > H + fs) continue;
        const fnt = `600 ${fs}px ${T.font}`;
        if (fnt !== font) ctx.font = font = fnt;
        const key = `${fs}|${s.titles[f.root]}`;
        let w = this.widths.get(key);
        if (w === undefined) this.widths.set(key, (w = ctx.measureText(s.titles[f.root]).width / 2));
        names.push({ j, px, py, fs, na });
        boxes.push([px - w, py - fs * 0.6, px + w, py + fs * 0.6]);
      }
      const drawn = declutter(boxes);
      for (let q = 0; q < names.length; q++) {
        if (!drawn[q]) continue;
        const { j, px, py, fs, na } = names[q], f = s.families[j];
        const fnt = `600 ${fs}px ${T.font}`;
        if (fnt !== font) ctx.font = font = fnt;
        ctx.globalAlpha = na * 0.9 * (st.near ? 0.3 : 1);
        ctx.strokeStyle = T.bg;
        ctx.lineWidth = 4;
        const title = s.titles[f.root];
        ctx.strokeText(title, px, py);
        ctx.fillStyle = T.names[f.fam % FAMILIES];
        ctx.fillText(title, px, py);
      }
      ctx.textBaseline = "alphabetic";
    }
    // the node hovered, a disc still: ringed, its label readable
    const hi = st.hover ? s.index.get(st.hover) : undefined;
    if (hi !== undefined && show[hi] > 0) {
      ctx.globalAlpha = 1;
      ctx.strokeStyle = T.fg;
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(sx[hi], sy[hi], Math.max(3, sr[hi]) + 2, 0, Math.PI * 2);
      ctx.stroke();
      const root = s.depth[hi] === 0;
      const fs = Math.max(12, (root ? 14.5 : 12.5) * k);
      ctx.font = font = `${root ? 600 : 500} ${fs}px ${T.font}`;
      ctx.strokeStyle = T.bg;
      ctx.lineWidth = 4;
      const y = sy[hi] + Math.max(3, sr[hi]) + 6 + fs * this.asc;
      ctx.strokeText(s.titles[hi], sx[hi], y);
      ctx.fillStyle = T.fg;
      ctx.fillText(s.titles[hi], sx[hi], y);
      ctx.lineWidth = 1;
    }
    ctx.globalAlpha = 1;

    const ms = performance.now() - t0;
    paintStats.last = ms;
    paintStats.ms = paintStats.paints ? paintStats.ms * 0.9 + ms * 0.1 : ms;
    paintStats.paints++;
    paintStats.discs = discs;
    paintStats.labels = labels;
    paintStats.links = nl;
    return fading;
  }
}
