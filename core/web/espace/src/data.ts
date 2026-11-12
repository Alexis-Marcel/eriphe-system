import { fixtures } from "./fixtures";
import type { Lang } from "./i18n";
import strings from "./strings.json";
import type { Composition, Item, Obj, Ref, Rule } from "./types";

// The data layer: the one place that talks to eriphe-core. The page reads
// - /espace/boot: its words, the CSRF token, the assistant's name;
// - /api/composition (#386): what to show, by ref;
// - /api/attention (#382): the attention queue, laid out by weight while
//   there is no composer;
// - /api/objects (#384): the objects behind the refs;
// - /events: the event stream, whose "attention" and "composition" events
//   say when to read again.
// Until those endpoints exist (404), or with no eriphe-core behind (the dev
// server), it shows made-up data (fixtures.ts) and says so.

export type Boot = { lang: Lang; name: string; csrf: string; strings: Record<string, string>; live: boolean };

export type Snapshot = { comp: Composition; store: Map<Ref, Obj>; sample: boolean };

const json = { Accept: "application/json" };

// A session that ended sends the page to the sign-in.
function signedOut(r: Response) {
  if (r.redirected && new URL(r.url).pathname === "/login") {
    location.assign("/login");
    throw new Error("signed out");
  }
}

export async function boot(url: string | undefined): Promise<Boot> {
  if (url) {
    try {
      const r = await fetch(url, { credentials: "same-origin", headers: json });
      signedOut(r);
      if (r.ok) return { ...(await r.json()), live: true };
    } catch {
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
  if (r.status === 404 || r.status === 405) return null; // not there yet
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return (await r.json()) as T;
}

// The attention queue's items, as #382 gives them.
type Attention = { items?: Item[] } | Item[];

async function composition(): Promise<Composition | null> {
  const comp = await getJSON<Composition>("/api/composition");
  if (comp) return comp;
  const att = await getJSON<Attention>("/api/attention");
  if (!att) return null;
  const items = Array.isArray(att) ? att : (att.items ?? []);
  return { mode: "side", eriphe: "", items: items.map(({ ref, weight, why, say }) => ({ ref, weight, why, say })) };
}

async function objects(refs: Ref[]): Promise<Obj[]> {
  if (!refs.length) return [];
  const got = await getJSON<Obj[] | { objects: Obj[] }>("/api/objects", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: JSON.stringify({ refs }),
  });
  if (!got) return [];
  return Array.isArray(got) ? got : got.objects;
}

let csrf = "";

export async function load(b: Boot): Promise<Snapshot> {
  csrf = b.csrf;
  const comp = b.live ? await composition() : null;
  if (!comp) {
    const f = fixtures(b.lang);
    return { comp: f.comp, store: new Map(f.objects.map((o) => [o.ref, o])), sample: true };
  }
  const refs = [...new Set([...comp.items.map((i) => i.ref), ...(comp.chips ?? [])])];
  const store = new Map<Ref, Obj>();
  for (const o of await objects(refs)) store.set(o.ref, o);
  // A group's members, one level down.
  const members = [...store.values()].flatMap((o) => (o.type === "group" ? o.refs : [])).filter((r) => !store.has(r));
  for (const o of await objects([...new Set(members)])) store.set(o.ref, o);
  return { comp, store, sample: false };
}

// follow calls back when eriphe-core says the attention or the composition
// changed, once a burst is over.
export function follow(onChange: () => void): () => void {
  const es = new EventSource("/events");
  let timer = 0;
  const later = () => {
    clearTimeout(timer);
    timer = window.setTimeout(onChange, 400);
  };
  es.addEventListener("attention", later);
  es.addEventListener("composition", later);
  return () => {
    clearTimeout(timer);
    es.close();
  };
}

async function post(path: string, body: URLSearchParams): Promise<boolean> {
  const r = await fetch(path, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf }, body });
  return r.ok;
}

// answer says yes or no to a card, as its buttons in the conversation do.
export async function answer(ref: Ref, yes: boolean): Promise<boolean> {
  const id = ref.match(/^card:(\d+)$/)?.[1];
  if (!id) return false;
  return post(`/cards/${id}/${yes ? "yes" : "no"}`, new URLSearchParams());
}

// send writes to the brain, in the conversation.
export async function send(text: string): Promise<boolean> {
  return post("/messages", new URLSearchParams({ text }));
}

// The user's remarks (the ⋯ menu): kept in this browser and applied by the
// layout engine until eriphe-core turns them into rules (#388).
const RULES = "eriphe.espace.rules";

export function loadRules(): Rule[] {
  try {
    const raw = JSON.parse(localStorage.getItem(RULES) ?? "[]");
    return Array.isArray(raw) ? raw : [];
  } catch {
    return [];
  }
}

export function saveRules(rules: Rule[]) {
  localStorage.setItem(RULES, JSON.stringify(rules));
}
