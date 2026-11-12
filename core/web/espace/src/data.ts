import { fixtures } from "./fixtures";
import type { Lang } from "./i18n";
import strings from "./strings.json";
import type { Composition, Obj, Ref } from "./types";

// The data layer: the one place that talks to eriphe-core. The page reads
// - /espace/boot: its words, the CSRF token, the assistant's name;
// - /api/composition (#386): what to show, by ref;
// - /api/attention (attention.go): the attention queue, laid out by weight
//   while there is no composition;
// - POST /api/objects (web_objects.go): the objects behind the refs, and
//   the ones they relate to;
// - /events: the event stream, whose "attention" and "composition" events
//   say when to read again, and "walkthrough" when Eriphe walks the user
//   through objects (workspace_walkthrough, workspace.go);
// - POST /messages: what the user writes, with the ref of the module open
//   (about), so Eriphe knows what « ça » is;
// - POST /api/espace/gesture: what the user opened, or what left the screen
//   unopened, which memory's extraction reads (workspace.go);
// - GET /api/proposals/{ref}: a proposal a surface shows (the catalog's
//   Proposal), and the card whose POST /cards/{id}/yes|no answers it.
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
  const es = new EventSource("/events");
  let timer = 0;
  const later = () => {
    clearTimeout(timer);
    timer = window.setTimeout(onChange, 400);
  };
  es.addEventListener("attention", later);
  es.addEventListener("composition", later);
  es.addEventListener("walkthrough", (e) => {
    try {
      onWalk(JSON.parse((e as MessageEvent).data) as Walk);
    } catch {
      /* not a walkthrough */
    }
  });
  return () => {
    clearTimeout(timer);
    es.close();
  };
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

// answerCard says yes or no to the card of a proposal.
export async function answerCard(card: number, yes: boolean): Promise<boolean> {
  return post(`/cards/${card}/${yes ? "yes" : "no"}`, new URLSearchParams());
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
export async function gesture(what: "opened" | "ignored", ref: Ref, title: string): Promise<boolean> {
  const r = await fetch("/api/espace/gesture", {
    method: "POST",
    credentials: "same-origin",
    headers: { ...json, "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: JSON.stringify({ what, ref, title }),
  });
  signedOut(r);
  return r.ok;
}
