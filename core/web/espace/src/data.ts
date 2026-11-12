import type { ActionMessage, Adapter, ErrorMessage, Message } from "./a2ui/adapter";
import { fixtures } from "./fixtures";
import type { Lang } from "./i18n";
import strings from "./strings.json";
import type { Composition, Layout, Obj, Ref } from "./types";

// The data layer: the one place that talks to eriphe-core. The page reads
// - /espace/boot: its words, the CSRF token, the assistant's name;
// - /api/composition (#386): what to show, by ref;
// - /api/attention (attention.go): the attention queue, laid out by weight
//   while there is no composition;
// - POST /api/objects (web_objects.go): the objects behind the refs, and
//   the ones they relate to;
// - /events: the event stream, whose "attention" and "composition" events
//   say when to read again, and "walkthrough" when Eriphe walks the user
//   through objects (workspace_walkthrough, workspace.go); asked with
//   a2ui=1, its "a2ui" events carry the surfaces' A2UI messages, in order,
//   a snapshot that resets the page first (surfaces_stream.go), and its
//   "layout" events where each surface is on the grid, which code decides
//   (workspace_layout.go). One stream per page, shared;
// - POST /api/espace/action and /api/espace/error: what the user did on a
//   surface, and what went wrong rendering one, as A2UI v1.0 messages. An
//   action is ask (a Button's request to Eriphe), answer (a Proposal's yes
//   or no) or local (open, back, expand, tab), checked by eriphe-core
//   (surface_actions.go); the page's own "open" (a tile or a chip) and
//   "back" place a surface first on the layout, and give the layout back;
// - POST /messages: what the user writes, with the ref of the module open
//   (about), so Eriphe knows what « ça » is;
// - POST /api/espace/gesture: what the user opened, or what left the screen
//   unopened, which memory's extraction reads (workspace.go);
// - GET /api/proposals/{ref}: a proposal a surface shows (the catalog's
//   Proposal), answered by an "answer" action (answerProposal).
// No control of the page keeps a rule or a preference: the user says it to
// Eriphe, who remembers it.
// A request without a session gets 401: the page goes to the sign-in. Only
// when neither the composition nor the attention queue is there (404), or
// with no eriphe-core behind (the dev server), it shows made-up data
// (fixtures.ts), and says so.

export type Boot = { lang: Lang; name: string; csrf: string; strings: Record<string, string>; live: boolean };

export type Snapshot = { comp: Composition; store: Map<Ref, Obj>; sample: boolean };

const json = { Accept: "application/json" };

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
      if (r.ok) return { ...(await r.json()), live: true };
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

// The attention queue (attentionItem in attention.go).
type Attention = { items: { ref: Ref; weight: number; why: string; say?: string; source: string; time: string }[] };

async function composition(): Promise<Composition | null> {
  const comp = await getJSON<Composition>("/api/composition");
  if (comp) return comp;
  const att = await getJSON<Attention>("/api/attention");
  if (!att) return null;
  return { mode: "side", eriphe: "", items: (att.items ?? []).map(({ ref, weight, why, say }) => ({ ref, weight, why, say })) };
}

// One entry per ref asked, in order: the object, or why it could not be read.
type Read = { ref: Ref; object?: Obj; error?: string; status?: number };

const BATCH = 50; // POST /api/objects reads 50 refs at most

// objects reads refs; one that names nothing, or whose source fails, is left
// out, and the layout engine drops it.
async function objects(refs: Ref[]): Promise<Obj[]> {
  const out: Obj[] = [];
  for (let i = 0; i < refs.length; i += BATCH) {
    const got = await getJSON<{ objects: Read[] }>("/api/objects", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify({ refs: refs.slice(i, i + BATCH) }),
    });
    for (const r of got?.objects ?? []) if (r.object) out.push(r.object);
  }
  return out;
}

let csrf = "";

// load reads the composition and its objects; extra are refs to read too
// (a walkthrough's, when they are not on the screen).
export async function load(b: Boot, extra: Ref[] = []): Promise<Snapshot> {
  csrf = b.csrf;
  const comp = b.live ? await composition() : null;
  if (!comp) {
    const f = fixtures(b.lang);
    return { comp: f.comp, store: new Map(f.objects.map((o) => [o.ref, o])), sample: true };
  }
  const refs = [...new Set([...comp.items.map((i) => i.ref), ...(comp.chips ?? []), ...extra])];
  const store = new Map<Ref, Obj>();
  for (const o of await objects(refs)) store.set(o.ref, o);
  // What they relate to, one level down, so a focus can bring it beside.
  const more = [...store.values()].flatMap((o) => [...(o.related ?? []), ...(o.group?.refs ?? [])]).filter((r) => !store.has(r));
  for (const o of await objects([...new Set(more)])) store.set(o.ref, o);
  return { comp, store, sample: false };
}

// A walkthrough Eriphe asked for (workspace_walkthrough): its refs, in
// order; none for what the page shows.
export type Walk = { id: number; refs: Ref[] | null };

// follow calls back when eriphe-core says the attention or the composition
// changed, once a burst is over, and when Eriphe starts a walkthrough.
export function follow(onChange: () => void, onWalk: (w: Walk) => void = () => {}): () => void {
  let timer = 0;
  const later = () => {
    clearTimeout(timer);
    timer = window.setTimeout(onChange, 400);
  };
  const offs = [
    listen("attention", later),
    listen("composition", later),
    listen("walkthrough", (e) => {
      try {
        onWalk(JSON.parse(e.data) as Walk);
      } catch {
        /* not a walkthrough */
      }
    }),
  ];
  return () => {
    clearTimeout(timer);
    for (const off of offs) off();
  };
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

// answer says yes or no to a card, as its buttons in the conversation do.
export async function answer(ref: Ref, yes: boolean): Promise<boolean> {
  const id = ref.match(/^card:(\d+)$/)?.[1];
  if (!id) return false;
  return post(`/cards/${id}/${yes ? "yes" : "no"}`, new URLSearchParams());
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
};

// proposal reads a proposal by its own ref (kind:id); null when no card
// carries it.
export async function proposal(ref: string): Promise<ProposalView | null> {
  return getJSON<ProposalView>(`/api/proposals/${encodeURIComponent(ref)}`);
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

// send writes to the brain, in the conversation; about is the ref of the
// module open while it was written.
export async function send(text: string, about?: Ref | null): Promise<boolean> {
  const body = new URLSearchParams({ text });
  if (about) body.set("about", about);
  return post("/messages", body);
}

// gesture tells eriphe-core what the user did: opened a module, or let one
// leave the screen unopened. Nothing waits on it.
export function gesture(what: "opened" | "ignored", ref: Ref, title: string): Promise<boolean> {
  return postJSON("/api/espace/gesture", { what, ref, title });
}
