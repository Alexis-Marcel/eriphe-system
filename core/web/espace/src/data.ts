import type { ActionMessage, Adapter, ErrorMessage, Message } from "./a2ui/adapter";
import type { Lang } from "./i18n";
import strings from "./strings.json";
import type { Layout } from "./types";

// The data layer: the one place that talks to eriphe-core. The page reads
// - /espace/boot: its words, the CSRF token, the assistant's name;
// - /events?a2ui=1: the event stream, whose "a2ui" events carry the
//   surfaces' A2UI messages, in order, a snapshot that resets the page
//   first (surfaces_stream.go), its "layout" events where each surface is
//   on the grid, which code decides (workspace_layout.go), and its
//   "proposals" events the refs of the proposals made or decided since
//   (surface_proposals.go), for what shows them to read them again; its
//   "exchange" events the conversation's last exchange (exchangeEvent,
//   web_espace_exchange.go), when it opens and whenever it changes; its
//   "notice" events the notifications routed to this tab (route_send.go).
//   One stream per page, shared, with the page's tab id;
// - POST /presence: whether this tab is in front of the user, and its
//   browser's push subscription (presence.ts, web_push.go);
// - POST /api/espace/action and /api/espace/error: what the user did on a
//   surface, and what went wrong rendering one, as A2UI v1.0 messages. An
//   action is ask (a Button's request to Eriphe), answer (a Proposal's yes
//   or no) or local (open, back, expand, tab), checked by eriphe-core
//   (surface_actions.go); the page's own "open" (a tile or a chip) and
//   "back" place a surface first on the layout, and give the layout back;
// - POST /messages: what the user writes, with the surface open
//   (about, surface:<id>), so Eriphe knows what « ça » is;
// - POST /api/espace/gesture: what left the grid unopened, what the user
//   closed, moved or resized (workspace_gestures.go):
//   the composer chooses again, memory's extraction reads them, and a
//   close, a move or a resize holds for the session;
// - GET /api/proposals/{ref}: a proposal a surface shows (the catalog's
//   Proposal), answered by an "answer" action (answerProposal);
// - GET /api/proposals?day=: the dated proposals that wait that day, which
//   a Day shows as ghosts (dayProposals).
// No control of the page keeps a rule or a preference: the user says it to
// Eriphe, who remembers it.
// A request without a session gets 401: the page goes to the sign-in. With
// no eriphe-core behind (the dev server), it shows a made-up surface
// (a2ui/fixtures.ts), and says so.

export type Boot = { lang: Lang; name: string; csrf: string; strings: Record<string, string>; live: boolean };

const json = { Accept: "application/json" };

let csrf = ""; // from the boot

// This page's tab id: it tells eriphe-core whether it is in front of the
// user (presence.ts), and gets the notifications routed to it, by the
// event stream or the answer to its presence (route.go).
export const tab = Math.random().toString(36).slice(2) + Date.now().toString(36);

class SignedOut extends Error {}

// A session that ended sends the page to the sign-in: a page route redirects
// there, /api/ answers 401.
function signedOut(r: Response) {
  if (r.status === 401 || (r.redirected && new URL(r.url).pathname === "/login")) {
    location.assign("/login");
    throw new SignedOut("signed out");
  }
}

// boot reads what the app needs from eriphe-core. With no boot to read (the
// dev server), the app runs on its fixtures; a boot that cannot be read
// fails: the shell then offers the conversation (main.tsx), never made-up
// data in place of the user's.
export async function boot(url: string | undefined): Promise<Boot> {
  if (url) {
    const r = await fetch(url, { credentials: "same-origin", headers: json });
    signedOut(r);
    if (!r.ok) throw new Error(`${url}: ${r.status}`);
    const b: Boot = { ...(await r.json()), live: true };
    csrf = b.csrf;
    return b;
  }
  const asked = new URLSearchParams(location.search).get("lang");
  const lang: Lang = asked === "en" || (!asked && !navigator.language.startsWith("fr")) ? "en" : "fr";
  return { lang, name: "Eriphe", csrf: "", strings: strings[lang], live: false };
}

async function getJSON<T>(path: string, init?: RequestInit): Promise<T | null> {
  const r = await fetch(path, { credentials: "same-origin", ...init, headers: { ...json, ...init?.headers } });
  signedOut(r);
  if (r.status === 404) return null; // not there yet
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return (await r.json()) as T;
}

// The event stream, one per page, open while something listens. It asks
// for the surfaces once something listens to them; listening to them
// opens it again, so the listener gets the snapshot that starts them.
type Handler = (e: MessageEvent) => void;
const handlers = new Map<string, Set<Handler>>();
let source: EventSource | null = null;

// The stream's health: whether it is open, and since when it is not. The
// browser opens it again by itself after a drop; one it closed for good
// (an error status) is opened again here, once the session is known to
// hold.
export type StreamHealth = { open: boolean; since: number };
let health: StreamHealth = { open: false, since: Date.now() };
let retry: ReturnType<typeof setTimeout> | null = null;

export function streamHealth(): StreamHealth {
  return health;
}

// STREAM_GRACE is how long the stream may stay down before the page offers
// the conversation: a restart of eriphe-core or a phone waking takes less.
export const STREAM_GRACE = 20_000;

// streamDown tells whether the stream has been down too long.
export function streamDown(h: StreamHealth, now: number): boolean {
  return !h.open && now - h.since > STREAM_GRACE;
}

export function streamURL(surfaces: boolean): string {
  return `/events?${surfaces ? "a2ui=1&" : ""}tab=${tab}`;
}

function open() {
  source?.close();
  if (retry) clearTimeout(retry);
  retry = null;
  const es = new EventSource(streamURL(handlers.has("a2ui")));
  source = es;
  es.onopen = () => {
    health = { open: true, since: Date.now() };
  };
  es.onerror = () => {
    if (health.open) health = { open: false, since: Date.now() };
    if (es.readyState !== 2 || source !== es) return; // 2: CLOSED; else the browser tries again
    retry = setTimeout(() => {
      fetch("/espace/boot", { credentials: "same-origin", headers: json })
        .then(signedOut)
        .catch(() => undefined)
        .finally(() => source === es && open());
    }, 3000);
  };
  for (const [type, set] of handlers) for (const h of set) es.addEventListener(type, h as EventListener);
}

function listen(type: string, h: Handler): () => void {
  if (!handlers.has(type)) handlers.set(type, new Set());
  handlers.get(type)!.add(h);
  if (!source || type === "a2ui") open();
  else source.addEventListener(type, h as EventListener);
  return () => {
    const set = handlers.get(type);
    if (!set?.delete(h)) return;
    if (set.size === 0) handlers.delete(type);
    source?.removeEventListener(type, h as EventListener);
    if (handlers.size === 0) {
      source?.close();
      source = null;
      if (retry) clearTimeout(retry);
      retry = null;
    }
  };
}

// The data of an "a2ui" event (a2uiEvent in surfaces_stream.go): the A2UI
// messages of the surfaces' changes, in order; seq is the last one's.
// reset: the surfaces the page has are dropped first.
export type SurfaceEvent = { seq: number; reset?: boolean; messages: Message[] };

// A line of the conversation's last exchange (exchangeLine,
// web_espace_exchange.go): the user's (via: where they said it, when not
// here or in the conversation), Eriphe's, or an error, without its text.
export type ExchangeLine = { seq: number; role: "user" | "brain" | "error"; text?: string; via?: string; time: string };

// The "exchange" event: the user's last message and Eriphe's lines after
// it, the message Eriphe is writing, and whether it is at work.
export type Exchange = { lines: ExchangeLine[]; draft?: string; thinking?: boolean };

// A notification routed to this tab (inApp, route_send.go): shown in the
// page, no system notification; its url opens it.
export type Note = { id: number; body: Record<string, string>; url: string };

// What the stream also feeds, besides the surfaces: the exchange, and the
// notifications routed to this tab.
export type Follow = { onExchange?: (x: Exchange) => void; onNotes?: (n: Note[]) => void };

// stream feeds the adapter the surfaces eriphe-core sends, and onLayout
// where they go; it sends back what the user did on them and what went
// wrong rendering them.
export function stream(b: Boot, adapter: Adapter, onLayout: (l: Layout) => void = () => {}, follow: Follow = {}): () => void {
  csrf = b.csrf;
  let seq = -1;
  const offs: (() => void)[] = [];
  if (follow.onExchange) {
    const on = follow.onExchange;
    offs.push(
      listen("exchange", (e) => {
        try {
          const x = JSON.parse(e.data) as Exchange;
          on({ ...x, lines: Array.isArray(x.lines) ? x.lines : [] });
        } catch {
          /* not an exchange */
        }
      }),
    );
  }
  if (follow.onNotes) {
    const on = follow.onNotes;
    offs.push(
      listen("notice", (e) => {
        try {
          const n = JSON.parse(e.data) as Note[];
          if (Array.isArray(n)) on(n);
        } catch {
          /* not notes */
        }
      }),
    );
  }
  // Listened to first: the stream opens with the surfaces, the layout right after.
  const offLayout = listen("layout", (e) => {
    try {
      onLayout(JSON.parse(e.data) as Layout);
    } catch {
      /* not a layout */
    }
  });
  const off = listen("a2ui", (e) => {
    let ev: SurfaceEvent;
    try {
      ev = JSON.parse(e.data) as SurfaceEvent;
    } catch {
      return; /* not an a2ui event */
    }
    if (ev.reset) {
      adapter.applyMessages(adapter.surfaces().map((s): Message => ({ version: "1.0", deleteSurface: { surfaceId: s.id } })));
    } else if (ev.seq <= seq) {
      return; // already applied
    }
    seq = ev.seq;
    adapter.applyMessages(ev.messages ?? []);
  });
  const offAction = adapter.onAction((m) => void action(m).catch(() => false));
  const offError = adapter.onError((m) => void renderError(m).catch(() => false));
  return () => {
    off();
    offLayout();
    offAction();
    offError();
    for (const o of offs) o();
  };
}

async function postJSON(path: string, body: unknown): Promise<boolean> {
  const r = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: { ...json, "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
  signedOut(r);
  return r.ok;
}

// action sends what the user did on a surface (an A2UI action).
export function action(m: ActionMessage): Promise<boolean> {
  return postJSON("/api/espace/action", m);
}

// place says the user opened a surface (a tile or a folded chip): code
// places it first. back gives the layout back.
export function place(what: "open" | "back", surfaceId: string): Promise<boolean> {
  return action({ version: "1.0", action: { name: what, surfaceId, sourceComponentId: "root", timestamp: new Date().toISOString(), context: {} } });
}

// renderError sends what went wrong rendering a surface, for its author.
export function renderError(m: ErrorMessage): Promise<boolean> {
  return postJSON("/api/espace/error", m);
}

async function post(path: string, body: URLSearchParams): Promise<boolean> {
  const r = await fetch(path, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf }, body });
  signedOut(r);
  return r.ok;
}

// A proposal as a surface shows it (proposalView, web_espace_proposal.go).
export type ProposalView = {
  ref: string;
  card: number;
  // kind is what the proposal is (memory, intention, access…): what its
  // card asks depends on it.
  kind?: string;
  state: string; // pending, approved, rejected, gone
  text: string;
  why?: string;
  tainted?: boolean;
  fields?: { name: string; value: string }[];
  // When it takes place, if it has a time: times with the user's offset, a
  // date for a whole day.
  when?: Span[];
  // Approved, its call: running, then done or failed (execWhy: notkept,
  // gone, notallowed or error).
  exec?: "running" | "done" | "failed";
  execWhy?: string;
};

export type Span = { start: string; end?: string; allDay?: boolean };

// proposal reads a proposal by its own ref (kind:id); null when no card
// carries it.
export async function proposal(ref: string): Promise<ProposalView | null> {
  return getJSON<ProposalView>(`/api/proposals/${encodeURIComponent(ref)}`);
}

// dayProposals reads the proposals that wait and take place on a day
// (YYYY-MM-DD), each with its times that day.
export async function dayProposals(date: string): Promise<ProposalView[]> {
  const got = await getJSON<{ proposals: ProposalView[] }>(`/api/proposals?day=${encodeURIComponent(date)}`);
  return got?.proposals ?? [];
}

// What moved: the refs of the proposals made or decided, or all of them
// when the stream fell behind (proposalsEvent, surface_proposals.go).
export type Moved = { refs: string[]; all?: boolean };

// followProposals calls back when proposals moved. It listens on the
// workspace's stream, which the surfaces keep open.
export function followProposals(on: (m: Moved) => void): () => void {
  return listen("proposals", (e) => {
    try {
      const m = JSON.parse(e.data) as Moved;
      on({ refs: Array.isArray(m.refs) ? m.refs : [], all: Boolean(m.all) });
    } catch {
      /* not a proposals event */
    }
  });
}

// answerProposal says yes or no to the proposal a Proposal component
// shows, as an A2UI "answer" action (POST /api/espace/action): eriphe-core
// binds it to that one proposal, checks the surface shows it and that it
// still waits, and decides it as its card's Oui or Non would.
export function answerProposal(a: Answer): Promise<boolean> {
  return action(answerMessage(a));
}

// What a Proposal component answers: the proposal it shows, where.
export type Answer = { surfaceId: string; componentId: string; ref: string; yes: boolean };

export function answerMessage(a: Answer): ActionMessage {
  return {
    version: "1.0",
    action: {
      name: "answer",
      surfaceId: a.surfaceId,
      sourceComponentId: a.componentId,
      timestamp: new Date().toISOString(),
      context: { proposal: a.ref, answer: a.yes ? "yes" : "no" },
    },
  };
}

// send writes to the brain, in the conversation, marked as typed in the
// workspace (so Eriphe may answer with a surface on stage); about is the
// surface open while it was written (surface:<id>).
export async function send(text: string, about?: string | null): Promise<boolean> {
  const body = new URLSearchParams({ text, from: "workspace" });
  if (about) body.set("about", about);
  return post("/messages", body);
}

// gesture tells eriphe-core what the user did to a surface (surface:<id>):
// let it leave the grid unopened; closed it (not shown again this session),
// moved it or changed its size (to: kept as its place for the session).
// Nothing waits on it.
export type GestureKind = "ignored" | "closed" | "moved" | "resized";
export function gesture(what: GestureKind, ref: string, to?: { size: string; col: number; row: number }): Promise<boolean> {
  return postJSON("/api/espace/gesture", { what, ref, ...to });
}
