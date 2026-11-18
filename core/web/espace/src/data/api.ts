import type { Lang } from "../i18n";
import strings from "../strings.json";

// The data layer's requests: the one place, with stream.ts, that talks to
// eriphe-core. The page reads
// - /espace/boot: its words, the CSRF token, the assistant's name;
// - GET /api/nodes/{id}[?version=n]: a node's page, its text and its
//   blocks ready to draw, its versions, the cards it waits on (web_graph.go);
// - GET /api/nodes/{id}/trace and /md: its trace, its file as it is;
// - GET /api/earlier?before=&node=: a node's (or the home's) lines before
//   the last exchange, a few at a time (web_console.go);
// - GET /api/proposals/{ref}, ?day=: a proposal a block shows, those of a
//   day (web_proposals.go);
// and writes
// - POST /messages: what the user says, to the node the console talks to;
// - POST /cards/{id}/yes|no: a card's answer, as in the conversation;
// - POST /presence: whether this tab is in front of the user (presence.ts).
// No control of the page keeps a rule or a preference: the user says it to
// Eriphe, who remembers it. A request without a session gets 401: the page
// goes to the sign-in.

export type Boot = { lang: Lang; name: string; csrf: string; strings: Record<string, string>; live: boolean };

const json = { Accept: "application/json" };

let csrf = ""; // from the boot

export const csrfToken = () => csrf;

// This page's tab id: it tells eriphe-core whether it is in front of the
// user (presence.ts), and gets the notifications routed to it.
export const tab = Math.random().toString(36).slice(2) + Date.now().toString(36);

class SignedOut extends Error {}

// A session that ended sends the page to the sign-in: a page route redirects
// there, /api/ answers 401.
export function signedOut(r: Response): Response {
  if (r.status === 401 || (r.redirected && new URL(r.url).pathname === "/login")) {
    location.assign("/login");
    throw new SignedOut("signed out");
  }
  return r;
}

// boot reads what the app needs from eriphe-core. With no boot to read (the
// dev server), the app runs on its made-up graph (sample.ts); a boot that
// cannot be read fails: the shell then offers the conversation (main.tsx),
// never made-up data in place of the user's.
export async function boot(url: string | undefined): Promise<Boot> {
  if (url) {
    const r = signedOut(await fetch(url, { credentials: "same-origin", headers: json }));
    if (!r.ok) throw new Error(`${url}: ${r.status}`);
    const b: Boot = { ...(await r.json()), live: true };
    csrf = b.csrf;
    return b;
  }
  const asked = new URLSearchParams(location.search).get("lang");
  const lang: Lang = asked === "en" || (!asked && !navigator.language.startsWith("fr")) ? "en" : "fr";
  return { lang, name: "Eriphe", csrf: "", strings: strings[lang], live: false };
}

async function getJSON<T>(path: string): Promise<T | null> {
  const r = signedOut(await fetch(path, { credentials: "same-origin", headers: json }));
  if (r.status === 404) return null; // not there, or no longer
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return (await r.json()) as T;
}

async function post(path: string, body?: URLSearchParams): Promise<boolean> {
  const r = signedOut(await fetch(path, { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf }, body }));
  return r.ok;
}

// A node as the graph shows it (graphNode, web_graph.go). title is empty
// when it may be someone else's words (an external node's): the page names
// it by what it is, never by them. line: while it runs, its task's last
// word of where it is.
export type GraphNode = {
  id: string;
  title?: string;
  parent?: string;
  links?: string[];
  lifetime: "work" | "standing";
  state: "idle" | "running" | "waiting" | "done";
  created: string;
  updated: string;
  done?: string;
  external?: boolean;
  version: number;
  waits?: boolean;
  line?: string;
};

// A node's page (nodePage, web_graph.go): the version shown, its text and
// its blocks in order, its versions, the cards it waits on.
export type NodePage = {
  node: GraphNode;
  version: number;
  archived?: string;
  versions: PageVersion[];
  parts: PagePart[];
  cards: ProposalView[];
};
export type PageVersion = { n: number; author: string; at: string };
export type PagePart = { text?: string; block?: PageBlock };
// A block ready to draw: its components and its data, the sources' last
// values at /<name>, the user's marks at /marks; or what is wrong with it.
export type PageBlock = { name: string; external?: boolean; components: unknown[] | null; data: Record<string, unknown> | null; error?: string };

export type TraceEntry = { at: string; author: string; text: string };
export type NodeFile = { md: string; version: number; hash: string; last: number };

export function nodePage(id: string, version = 0): Promise<NodePage | null> {
  return getJSON<NodePage>(`/api/nodes/${encodeURIComponent(id)}${version ? `?version=${version}` : ""}`);
}

export async function nodeTrace(id: string): Promise<TraceEntry[]> {
  return (await getJSON<{ entries: TraceEntry[] }>(`/api/nodes/${encodeURIComponent(id)}/trace`))?.entries ?? [];
}

export function nodeFile(id: string, version = 0): Promise<NodeFile | null> {
  return getJSON<NodeFile>(`/api/nodes/${encodeURIComponent(id)}/md${version ? `?version=${version}` : ""}`);
}

// A line of the conversation (exchangeLine, web_console.go): the user's
// (via: where they said it, when not here), Eriphe's (external: written
// after reading external content; filed: the node code filed it in), or an
// error, without its text.
export type ExchangeLine = { seq: number; role: "user" | "brain" | "error"; text?: string; via?: string; time: string; external?: boolean; filed?: string };

// The "exchange" event: the last exchange of the node the console talks to
// (node), or the home's; whether lines come before it, the message Eriphe
// is writing, whether it is at work, the replies its last line suggests.
export type Exchange = {
  node?: string;
  title?: string;
  lines: ExchangeLine[];
  earlier?: boolean;
  draft?: string;
  thinking?: boolean;
  suggestions?: string[];
};

export type Earlier = { lines: ExchangeLine[]; more: boolean };

// earlier reads the lines of a node's exchange (none: the home's) before
// the line seq.
export async function earlier(before: number, node?: string): Promise<Earlier> {
  const of = node ? `&node=${encodeURIComponent(node)}` : "";
  return (await getJSON<Earlier>(`/api/earlier?before=${before}${of}`)) ?? { lines: [], more: false };
}

// send writes to Eriphe, said to a node (it belongs to it) or to the home,
// typed in the workspace or a reply it suggested, chosen (from
// "suggestion": never a card's answer).
export function send(text: string, node?: string | null, from: "workspace" | "suggestion" = "workspace"): Promise<boolean> {
  const body = new URLSearchParams({ text, from });
  if (node) body.set("node", node);
  return post("/messages", body);
}

// A notification routed to this tab (inApp, route_send.go): shown in the
// page; its url opens it.
export type Note = { id: number; body: Record<string, string>; url: string };

// A proposal as a page or a block shows it (proposalView, web_proposals.go).
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
  // Where it comes from, in words, with its node; what its Oui does: facts
  // of eriphe-core's, never the model's words.
  node?: string;
  from?: string;
  yes?: string;
  when?: Span[];
  exec?: "running" | "done" | "failed";
  execWhy?: string;
};

export type Span = { start: string; end?: string; allDay?: boolean };

export function proposal(ref: string): Promise<ProposalView | null> {
  return getJSON<ProposalView>(`/api/proposals/${encodeURIComponent(ref)}`);
}

export async function dayProposals(date: string): Promise<ProposalView[]> {
  return (await getJSON<{ proposals: ProposalView[] }>(`/api/proposals?day=${encodeURIComponent(date)}`))?.proposals ?? [];
}

// What a Proposal component answers: the proposal it shows. Its card's
// Oui or Non decides it, as in the conversation.
export type Answer = { surfaceId: string; componentId: string; ref: string; yes: boolean; at?: string };

// answerCard says yes or no to a card.
export function answerCard(card: number, yes: boolean): Promise<boolean> {
  return post(`/cards/${card}/${yes ? "yes" : "no"}`);
}

// answerProposal answers the card of the proposal a block shows, read again
// first: a proposal no card carries, or decided since, is not answered.
export async function answerProposal(a: Answer): Promise<boolean> {
  const p = await proposal(a.ref);
  if (!p || p.state !== "pending" || !p.card) return false;
  return answerCard(p.card, a.yes);
}

// What moved: the refs of the proposals made or decided, or all of them.
export type Moved = { refs: string[]; all?: boolean };
