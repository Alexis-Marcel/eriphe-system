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
//   (surface_proposals.go), for what shows them to read them again. One
//   stream per page, shared;
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

class SignedOut extends Error {}

// A session that ended sends the page to the sign-in: a page route redirects
// there, /api/ answers 401.
function signedOut(r: Response) {
  if (r.status === 401 || (r.redirected && new URL(r.url).pathname === "/login")) {
    location.assign("/login");
    throw new SignedOut("signed out");
  }
}

export async function boot(url: string | undefined): Promise<Boot> {
  if (url) {
    try {
      const r = await fetch(url, { credentials: "same-origin", headers: json });
      signedOut(r);
      if (r.ok) {
        const b: Boot = { ...(await r.json()), live: true };
        csrf = b.csrf;
        return b;
      }
    } catch (e) {
      if (e instanceof SignedOut) throw e;
      /* no eriphe-core: the dev server */
    }
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

function open() {
  source?.close();
  source = new EventSource(handlers.has("a2ui") ? "/events?a2ui=1" : "/events");
  for (const [type, set] of handlers) for (const h of set) source.addEventListener(type, h as EventListener);
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
    }
  };
}

// The data of an "a2ui" event (a2uiEvent in surfaces_stream.go): the A2UI
// messages of the surfaces' changes, in order; seq is the last one's.
// reset: the surfaces the page has are dropped first.
export type SurfaceEvent = { seq: number; reset?: boolean; messages: Message[] };

// stream feeds the adapter the surfaces eriphe-core sends, and onLayout
// where they go; it sends back what the user did on them and what went
// wrong rendering them.
export function stream(b: Boot, adapter: Adapter, onLayout: (l: Layout) => void = () => {}): () => void {
  csrf = b.csrf;
  let seq = -1;
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
  state: string; // pending, approved, rejected, gone
  text: string;
  why?: string;
  tainted?: boolean;
  fields?: { name: string; value: string }[];
  // When it takes place, if it has a time: times with the user's offset, a
  // date for a whole day.
  when?: Span[];
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

// send writes to the brain, in the conversation; about is the surface open
// while it was written (surface:<id>).
export async function send(text: string, about?: string | null): Promise<boolean> {
  const body = new URLSearchParams({ text });
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
