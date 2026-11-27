import { drawnRadius, labelAlpha, LOD, type Item, type View } from "./lod";

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
  titles: string[];
  codes: string[]; // a root's short word, inside its disc
  order: Int32Array; // the nodes by radius, the biggest first: the labels' order
  links: Int32Array; // the links, two indices each
  index: Map<string, number>;
};

export type SceneNode = { id: string; r: number; depth: number; light: number; status: string; fam: number; title: string; code: string };

export function scene(nodes: readonly SceneNode[], links: readonly [string, string][]): Scene {
  const n = nodes.length;
  const s: Scene = {
    items: nodes.map((x) => ({ id: x.id, r: x.r, waits: x.status === "waits" })),
    depth: new Uint8Array(n),
    alpha: new Float32Array(n),
    done: new Uint8Array(n),
    run: new Uint8Array(n),
    fam: new Int16Array(n),
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
};

export const FADE = 180; // ms: a disc fades as its bubble fades in (espace.css, lod-in)
export const LABELS = 300; // labels at most in a paint

// paintStats: the last paints' cost, for the ?leger page.
export const paintStats = { ms: 0, last: 0, paints: 0, discs: 0, labels: 0, links: 0 };

type Theme = { bg: string; fg: string; fg3: string; ring: string; edge: string; edgeOn: string; accent: string; run: string; ok: string; neutral: string; fills: string[]; dots: string[]; font: string; mono: string };

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
    fills: fams.map((f) => rgb(mix(f, share, card))),
    dots: fams.map((f) => rgb(f)),
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

  constructor(private canvas: HTMLCanvasElement, ctx: CanvasRenderingContext2D) {
    this.ctx = ctx;
    this.theme = readTheme();
    this.halo = this.sprite();
  }

  static on(canvas: HTMLCanvasElement): Painter | null {
    let ctx: CanvasRenderingContext2D | null = null;
    try {
      ctx = canvas.getContext("2d");
    } catch {
      ctx = null;
    }
    return ctx ? new Painter(canvas, ctx) : null;
  }

  // retheme reads the colours again (the theme changed).
  retheme() {
    this.theme = readTheme();
    this.halo = this.sprite();
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
    let fading = false;
    // each node on screen: where, how big, how much of its disc shows (0:
    // its bubble or its page in its place)
    const show = new Float32Array(n);
    for (let i = 0; i < n; i++) {
      const id = s.items[i].id;
      sx[i] = xy[i * 2] * k + v.x;
      sy[i] = xy[i * 2 + 1] * k + v.y;
      sr[i] = discR(s.items[i].r, s.done[i] === 1, k);
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
    // or none, then four steps of opacity)
    const colour = LOD.color;
    const B = (FAMILIES + 1) * 4;
    const count = new Int32Array(B + 1);
    let discs = 0;
    for (let i = 0; i < n; i++) {
      bucket[i] = -1;
      if (show[i] === 0 || !seen(i, sr[i] + 2)) continue;
      const f = colour ? s.fam[i] + 1 : 0;
      const q = Math.min(3, Math.floor(s.alpha[i] * show[i] * 4 - 0.001));
      bucket[i] = f * 4 + Math.max(0, q);
      count[bucket[i] + 1]++;
      discs++;
    }
    for (let b = 0; b < B; b++) count[b + 1] += count[b];
    const sorted = new Int32Array(discs);
    const fill = count.slice(0, B);
    for (let i = 0; i < n; i++) if (bucket[i] >= 0) sorted[fill[bucket[i]]++] = i;
    for (let b = 0; b < B; b++) {
      const from = count[b], to = count[b + 1];
      if (from === to) continue;
      const f = Math.floor(b / 4) - 1, q = b % 4;
      ctx.globalAlpha = (q + 1) / 4;
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
      for (let b = 4; b < B; b++) {
        const from = count[b], to = count[b + 1];
        if (from === to) continue;
        ctx.globalAlpha = 0.55 * (((b % 4) + 1) / 4);
        ctx.fillStyle = T.dots[Math.floor(b / 4) - 1];
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
    for (const [flag, style, lw] of [
      [(i: number) => s.items[i].waits, T.accent, 2],
      [(i: number) => s.run[i] === 1 && sr[i] >= 5, T.run, 1.5],
    ] as const) {
      ctx.strokeStyle = style;
      ctx.lineWidth = lw;
      for (let i = 0; i < n; i++) {
        if (bucket[i] < 0 || !flag(i)) continue;
        ctx.globalAlpha = show[i];
        ctx.beginPath();
        ctx.arc(sx[i], sy[i], Math.max(2.5, sr[i] + lw / 2), 0, Math.PI * 2);
        ctx.stroke();
      }
    }
    ctx.lineWidth = 1;

    // the labels, the biggest nodes first: in the bubble's font and place
    // (espace.css, .gr-label), in the graph's units; a root's short word in
    // its disc
    let labels = 0;
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    ctx.lineJoin = "round";
    let font = "";
    for (let o = 0; o < n && labels < LABELS; o++) {
      const i = s.order[o];
      const r = s.items[i].r;
      const la = labelAlpha(r * k);
      if (la <= 0) break; // the rest are smaller
      if (show[i] === 0) continue;
      const root = s.depth[i] === 0;
      const fs = (root ? 14.5 : 12.5) * k;
      if (fs < 4) continue;
      const R = r + (root ? 12 : 6);
      const y = sy[i] + (R + (root ? 14 : 8)) * k;
      if (!seen(i, sr[i]) && (sx[i] < -300 || sx[i] > W + 300 || y > H || y < -fs * 2)) continue;
      const f = `${root ? 600 : 500} ${fs.toFixed(1)}px ${T.font}`;
      if (f !== font) ctx.font = font = f;
      ctx.globalAlpha = la * show[i];
      ctx.strokeStyle = T.bg;
      ctx.lineWidth = Math.max(2, fs * 0.28);
      ctx.strokeText(s.titles[i], sx[i], y);
      ctx.fillStyle = s.done[i] ? T.fg3 : T.fg;
      ctx.fillText(s.titles[i], sx[i], y);
      if (root && 10.5 * k >= 5 && seen(i, sr[i])) {
        ctx.font = font = `${(10.5 * k).toFixed(1)}px ${T.mono}`;
        ctx.textBaseline = "middle";
        ctx.fillStyle = T.fg3;
        ctx.globalAlpha = show[i] * s.alpha[i];
        ctx.fillText(s.codes[i], sx[i], sy[i]);
        ctx.textBaseline = "top";
      }
      labels++;
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
      ctx.textBaseline = "top";
      ctx.strokeStyle = T.bg;
      ctx.lineWidth = 4;
      const y = sy[hi] + Math.max(3, sr[hi]) + 6;
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
