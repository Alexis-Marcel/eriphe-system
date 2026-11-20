import type { Lang } from "../i18n";
import strings from "../strings.json";

// The data layer's requests: the one place, with stream.ts, that talks to
// eriphe-core. The page reads
// - /espace/boot: its words, the CSRF token, the assistant's name;
// - GET /api/nodes/{id}[?version=n]: a node's page, its text and its
//   blocks ready to draw, its versions, the cards it waits on (web_graph.go);
// - GET /api/nodes/{id}/trace and /md: its trace, its file as it is;
// - GET /api/proposals/{ref}, ?day=: a proposal a block shows, those of a
//   day (web_proposals.go);
// - GET /api/archives: what the user archived;
// and writes
// - POST /messages: what the user says, to the node the console talks to;
// - POST /cards/{id}/yes|no: a card's answer;
// - POST /brain/retry: once more the turn Eriphe could not answer;
// - the user's gestures (web_gestures.go): PUT /api/nodes/{id}/md (an edit,
//   made on the version it was read at), POST .../mark and .../ask (a
//   block's Mark and button), .../archive and .../reopen, PUT and DELETE
//   .../place (where a bubble was dropped), PUT /api/espace/view (the pages
//   open, the display settings);
// - POST /api/nodes/{id}/act: an action on the items picked in a list
//   (web_list_actions.go);
// - POST /presence: whether this tab is in front of the user (presence.ts).
// No control of the page keeps a rule or a preference: the user says it to
// Eriphe, who remembers it. A request without a session gets 401: the page
// goes to the sign-in.

// The workspace as the user left it (espaceView, web_gestures.go): the
// pages open, the one the console talked to, the last of Eriphe's shows
// taken, the theme ("" follows the system).
export type Theme = "" | "light" | "dark";
export type View = { open: string[]; talk?: string; shown?: number; theme?: Theme };
export type Place = { x: number; y: number };

export type Boot = {
  lang: Lang;
  name: string;
  csrf: string;
  strings: Record<string, string>;
  live: boolean;
  view?: View;
  places?: Record<string, Place>;
  // What Eriphe lacks to think: no AI yet (its wizard left for later), or
  // no brain on this server; absent once it can.
  setup?: "noai" | "nobrain";
};

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

// sendJSON sends a gesture's JSON: its status and what came back.
async function sendJSON<T>(method: string, path: string, body?: unknown, keepalive = false): Promise<{ status: number; data: T | null }> {
  const r = signedOut(
    await fetch(path, {
      method,
      keepalive,
      credentials: "same-origin",
      headers: { "X-CSRF-Token": csrf, "Content-Type": "application/json", ...json },
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
  );
  const data = r.status === 204 ? null : ((await r.json().catch(() => null)) as T | null);
  return { status: r.status, data };
}

const nodePath = (id: string) => `/api/nodes/${encodeURIComponent(id)}`;

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
// its blocks in order, its versions, the cards it waits on. shift: 1 when
// its creation was empty, the user's v1 being its version 2 (label).
export type NodePage = {
  node: GraphNode;
  version: number;
  archived?: string;
  versions: PageVersion[];
  shift?: number;
  parts: PagePart[];
  cards: ProposalView[];
  refs?: Record<string, PageRef>;
};
// The node a [[link]] of a page's text opens, by the name as written
// (pageRef, web_graph.go): its title, none when it may be someone else's
// words; archived, it opens nothing. A name no node has is not there.
export type PageRef = { id: string; title?: string; external?: boolean; archived?: boolean };
export type PageVersion = { n: number; author: string; at: string };
export type PagePart = { text?: string; block?: PageBlock };
// A block ready to draw: its components and its data, the sources' last
// values at /<name>, the user's marks at /marks; or what is wrong with it.
// A card block names one of the node's cards (card), with the card as
// eriphe-core's records say it (proposal); none when it is not the node's.
export type PageBlock = {
  name: string;
  external?: boolean;
  components: unknown[] | null;
  data: Record<string, unknown> | null;
  error?: string;
  card?: string;
  proposal?: ProposalView;
  // the Lists the user may pick items in, and the actions on them
  // (list_actions.go)
  lists?: PickList[];
};

// A row the user may pick: its place in its source's list (n), its id,
// what it shows. An action's words are code's: one for one item, many
// with {n}; hint is the author's label, never on an external block;
// confirm: a tool that throws away, asked once more with its exact call.
export type PickItem = { n: number; id: string; title?: string; detail?: string };
export type PickAction = { name: string; one: string; many: string; tool: string; args?: string[]; hint?: string; confirm?: boolean };
export type PickList = { component: string; source: string; items: PickItem[]; actions: PickAction[] };

export type TraceEntry = { at: string; author: string; text: string };
// A node's file as it is (md), its body as the user edits it (body), the
// version and its hash, and the last version.
export type NodeFile = { md: string; body: string; version: number; hash: string; last: number };

export function nodePage(id: string, version = 0): Promise<NodePage | null> {
  return getJSON<NodePage>(`${nodePath(id)}${version ? `?version=${version}` : ""}`);
}

export async function nodeTrace(id: string): Promise<TraceEntry[]> {
  return (await getJSON<{ entries: TraceEntry[] }>(`${nodePath(id)}/trace`))?.entries ?? [];
}

export function nodeFile(id: string, version = 0): Promise<NodeFile | null> {
  return getJSON<NodeFile>(`${nodePath(id)}/md${version ? `?version=${version}` : ""}`);
}

// What a save of the .md tab came to: saved (its version and hash), a
// conflict (a newer version came: its body and hash, nothing written),
// archived, or refused (why: a block that does not read, a secret).
export type Saved =
  | { ok: true; version: number; hash: string }
  | { ok: false; conflict: { version: number; hash: string; body: string } }
  | { ok: false; why: string };

export async function saveFile(id: string, body: string, expect: string): Promise<Saved> {
  const { status, data } = await sendJSON<{ version: number; hash: string; body: string; error: string }>("PUT", `${nodePath(id)}/md`, { body, expect });
  if (status === 200 && data) return { ok: true, version: data.version, hash: data.hash };
  if (status === 409 && data) return { ok: false, conflict: { version: data.version, hash: data.hash, body: data.body } };
  return { ok: false, why: data?.error ?? String(status) };
}

// mark sets the user's mark on an item a block shows ("" takes it away).
export async function mark(id: string, block: string, list: string, item: string, m: string): Promise<boolean> {
  return (await sendJSON("POST", `${nodePath(id)}/mark`, { block, list, item, mark: m })).status === 204;
}

// ask sends a block's button: eriphe-core reads its words in the block.
export async function ask(id: string, block: string, component: string): Promise<boolean> {
  return (await sendJSON("POST", `${nodePath(id)}/ask`, { block, component })).status === 204;
}

// act runs one of a block's actions on the items picked: the user's own
// gesture (web_list_actions.go). ok, with the line that says what was
// done, or why not.
export async function act(id: string, block: string, action: string, version: number, ids: string[]): Promise<{ ok: boolean; text: string }> {
  const r = await sendJSON<{ text?: string; error?: string }>("POST", `${nodePath(id)}/act`, { block, action, version, ids });
  return { ok: r.status === 200, text: r.data?.text ?? r.data?.error ?? "" };
}

export async function archive(id: string): Promise<boolean> {
  return (await sendJSON("POST", `${nodePath(id)}/archive`)).status === 204;
}

// label is a version as the user numbers it.
export const label = (n: number, shift = 0) => Math.max(1, n - shift);

export async function reopen(id: string): Promise<boolean> {
  return (await sendJSON("POST", `${nodePath(id)}/reopen`)).status === 204;
}

// An archived node, the top of what one archive took (archivedNode).
export type ArchivedNode = GraphNode & { archived: string; with: number };

export async function archives(): Promise<ArchivedNode[]> {
  return (await getJSON<{ archives: ArchivedNode[] }>("/api/archives"))?.archives ?? [];
}

// place keeps where a bubble was dropped (null: the layout places it).
export async function place(id: string, p: Place | null): Promise<boolean> {
  const r = await sendJSON(p ? "PUT" : "DELETE", `${nodePath(id)}/place`, p ?? undefined);
  return r.status === 204;
}

// putView keeps what is open; keepalive, as the page leaves.
export async function putView(v: View, keepalive = false): Promise<boolean> {
  return (await sendJSON("PUT", "/api/espace/view", v, keepalive)).status === 204;
}

// A line of the bar (barLine, web_console.go): its message's seq and its
// text, plain; external: Eriphe's, written after reading external content.
export type BarLine = { seq: number; text: string; external?: boolean };

// The "exchange" event: what the bar shows of the node it talks to (node),
// or of the home: the user's last message (asked), Eriphe's line (said:
// the first line of its last answer, the rest in the subject), whether it
// could not answer (failed), the first line of what it is writing, whether
// it is at work, the cards no node holds.
export type Exchange = {
  node?: string;
  title?: string;
  asked?: BarLine;
  said?: BarLine;
  failed?: boolean;
  draft?: string;
  thinking?: boolean;
  // The cards that wait and no node holds (their refs): the bar shows
  // them, wherever it talks, so that none waits out of sight.
  cards?: string[];
};

// send writes to Eriphe, said to a node (it belongs to it) or to the home.
export function send(text: string, node?: string | null): Promise<boolean> {
  const body = new URLSearchParams({ text, from: "workspace" });
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
  // summary: what a yes makes, in plain sentences of code's (a view's);
  // fields: its technical words, a detail one under Détails, a Markdown
  // one (a skill's instructions) drawn as such.
  summary?: string[];
  fields?: { name: string; value: string; detail?: boolean; markdown?: boolean }[];
  // Where it comes from, in words, with its node; what its Oui does: facts
  // of eriphe-core's, never the model's words.
  node?: string;
  from?: string;
  yes?: string;
  when?: Span[];
  exec?: "running" | "done" | "failed";
  execWhy?: string;
  // A card of several calls: each one the user may leave out (its id,
  // what it acts on, how many items, where it stands), and what Oui says
  // for 1, 2… items chosen (call_fold.go).
  choices?: Choice[];
  acts?: string[];
};

export type Choice = { id: number; text: string; n: number; state: string };

export type Span = { start: string; end?: string; allDay?: boolean };

export function proposal(ref: string): Promise<ProposalView | null> {
  return getJSON<ProposalView>(`/api/proposals/${encodeURIComponent(ref)}`);
}

export async function dayProposals(date: string): Promise<ProposalView[]> {
  return (await getJSON<{ proposals: ProposalView[] }>(`/api/proposals?day=${encodeURIComponent(date)}`))?.proposals ?? [];
}

// What a Proposal component answers: the proposal it shows. Its card's
// Oui or Non decides it.
// A Oui with a choice names the calls ticked (only) and those unticked
// (left): only those are decided.
export type Answer = { surfaceId: string; componentId: string; ref: string; yes: boolean; at?: string; only?: number[]; left?: number[] };

// retryBrain gives Eriphe the turn that failed once more (brain_errors.go).
export function retryBrain(): Promise<boolean> {
  return post("/brain/retry");
}

// answerCard says yes or no to a card; a yes with a choice, to the
// calls ticked (only), the others (left) refused.
export function answerCard(card: number, yes: boolean, only?: number[], left?: number[]): Promise<boolean> {
  if (!yes || !only) return post(`/cards/${card}/${yes ? "yes" : "no"}`);
  return post(`/cards/${card}/yes`, new URLSearchParams({ only: only.join(","), left: (left ?? []).join(",") }));
}

// answerProposal answers the card of the proposal a block shows, read again
// first: a proposal no card carries, or decided since, is not answered.
export async function answerProposal(a: Answer): Promise<boolean> {
  const p = await proposal(a.ref);
  if (!p || p.state !== "pending" || !p.card) return false;
  return answerCard(p.card, a.yes, a.only, a.left);
}

// What moved: the refs of the proposals made or decided, or all of them.
export type Moved = { refs: string[]; all?: boolean };
