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
// - POST /messages: what the user says, to the node the bar talks to;
// - POST /cards/{id}/yes|no: a card's answer;
// - POST /brain/retry: once more the turn Eriphe could not answer;
// - the user's gestures (web_gestures.go): PUT /api/nodes/{id}/md (an edit,
//   made on the version it was read at), POST .../mark and .../ask (a
//   block's Mark and button), .../archive and .../reopen, PUT /api/espace/view (the pages open, the
//   display settings);
// - POST /api/nodes/{id}/act: an action on the items picked in a list
//   (web_list_actions.go);
// - POST /presence: whether this tab is in front of the user (presence.ts).
// No control of the page keeps a rule or a preference: the user says it to
// Eriphe, who remembers it. A request without a session gets 401: the page
// goes to the sign-in.

// The workspace as the user left it (espaceView, web_gestures.go): the
// pages open, the one the bar talked to, the last of Eriphe's shows
// taken, the theme ("" follows the system).
export type Theme = "" | "light" | "dark";
export type View = { open: string[]; talk?: string; shown?: number; theme?: Theme };

export type Boot = {
  lang: Lang;
  name: string;
  csrf: string;
  strings: Record<string, string>;
  live: boolean;
  view?: View;
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

// A node as the graph shows it (graphNode, web_graph.go). titleExternal:
// its title may be someone else's words (written after an external read):
// shown as it is, always marked (graph/Title.tsx). line: while it runs,
// its task's last word of where it is.
export type GraphNode = {
  id: string;
  title?: string;
  titleExternal?: boolean;
  // unasked: a skill or an event opened it, not the user (a sign says so)
  unasked?: boolean;
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
  // cards: those that still ask, at its foot; settled: those decided, for the « ⋯ » menu
  cards: ProposalView[];
  settled?: ProposalView[];
  refs?: Record<string, PageRef>;
  alive?: PageAlive;
  // origin: who wanted it, as eriphe-core recorded it
  origin?: PageOrigin;
};
// What keeps a permanent subject alive (pageAlive, node_alive.go): its
// sources, monitors and skills, each named, what it does in code's words.
export type AliveLine = { name: string; what: string };
export type PageOrigin = { unasked?: boolean; text: string };
// decided: the goal of the view the user approved (« Ce qu'on a décidé »), decidedExternal when written after an external read.
export type PageAlive = { decided?: string; decidedExternal?: boolean; sources?: AliveLine[]; monitors?: AliveLine[]; skills?: AliveLine[]; updated: string };
// The node a [[link]] of a page's text opens, by the name as written
// (pageRef, web_graph.go): its title, titleExternal when it may be someone
// else's words (shown marked); archived, it opens nothing. A name no node
// has is not there.
export type PageRef = { id: string; title?: string; titleExternal?: boolean; external?: boolean; archived?: boolean };
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
  // the Lists bound to a source's items the page must know: those that
  // filter them, those the user may pick items in, and the actions on
  // them (list_actions.go, list_sections.go)
  lists?: PickList[];
};

// A row of such a List: its place in its source's list (n), its id, what
// it shows. An action's words are code's: one for one item, many with
// {n}, all with {n} for all the rows of the list; hint is the author's
// label, hintExternal on an external block (shown marked: someone else's
// words may have written it); confirm: a tool that throws away,
// asked once more with its exact call. A filtered List shows its rows
// only, never the source's others.
// card: the ref of the card that waits on this item (item_cards.go), shown
// when the row is opened: what its Oui does, in code's words.
export type PickItem = { n: number; id: string; title?: string; detail?: string; card?: string };
export type PickAction = { name: string; one: string; many: string; all?: string; tool: string; args?: string[]; hint?: string; hintExternal?: boolean; confirm?: boolean };
export type PickList = { component: string; source: string; filtered?: boolean; open?: boolean; items: PickItem[]; actions: PickAction[] };

// What opening an item read (item_open.go): its common fields and the
// whole as JSON, external when its server is not trusted, with the item's
// note; or the call it asks a yes for (ask); or why not (error).
export type Opened = {
  ask?: { tool: string; args: string[] };
  title?: string;
  from?: string;
  date?: string;
  body?: string;
  raw?: string;
  external?: boolean;
  note?: { group?: string; note?: string; external?: boolean };
  error?: string;
};

// openItem reads an item of a block's source in full, the user's gesture;
// yes once they saw the call it asked for, with that call's tool.
export async function openItem(id: string, block: string, source: string, item: string, yes: boolean, tool?: string): Promise<Opened> {
  const r = await sendJSON<Opened>("POST", `${nodePath(id)}/open`, { block, source, item, yes, tool: tool ?? "" });
  if (r.status === 200 && r.data) return r.data;
  return { error: r.data?.error ?? String(r.status) };
}

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

// act runs one of a block's actions on the items picked in one of its
// Lists (its component id), or all those it showed: the user's own
// gesture (web_list_actions.go). ok, with the line that says what was
// done, or why not.
export async function act(id: string, block: string, list: string, action: string, version: number, ids: string[]): Promise<{ ok: boolean; text: string }> {
  const r = await sendJSON<{ text?: string; error?: string }>("POST", `${nodePath(id)}/act`, { block, list, action, version, ids });
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

// putView keeps what is open; keepalive, as the page leaves.
export async function putView(v: View, keepalive = false): Promise<boolean> {
  return (await sendJSON("PUT", "/api/espace/view", v, keepalive)).status === 204;
}

// A message of the user's that waits for Eriphe (waitingLine,
// messages_waiting.go): sent, not yet read; node: the node it was said
// to, with its title; none: the home.
export type Waiting = { seq: number; text: string; node?: string; title?: string };

// Eriphe's words where they belong (orbWords, web_orb.go): node, none for
// the home; a few plain lines (more: the subject's page says the rest),
// or at the home an answer of more than a line, whole, as Markdown
// (whole); external: written after reading external content.
export type OrbWords = { seq: number; node?: string; text: string; more?: boolean; whole?: boolean; external?: boolean };

// The "orb" event (orbEvent, web_orb.go): what Eriphe's orb shows,
// whatever page the bar talks to. asked: the seq of the user's last
// message Eriphe read; work: Eriphe is at work, on that node (none: the
// home), and what it is writing; answer: its answer to that message;
// said: its later words that answer none (an event's); failed: it could
// not answer; wait: the newest card that waits for the user's yes in a
// node; cards: those no node holds, shown above the bar.
export type OrbFeed = {
  asked?: number;
  work?: { node?: string; draft?: string };
  answer?: OrbWords;
  said?: OrbWords;
  failed?: { seq: number; node?: string };
  wait?: { card: number; node: string };
  cards?: string[];
  // The user's messages that wait for Eriphe, whatever node they were
  // said to, oldest first.
  waiting?: Waiting[];
};

// send writes to Eriphe, said to a node (it belongs to it) or to the home.
export function send(text: string, node?: string | null): Promise<boolean> {
  const body = new URLSearchParams({ text, from: "workspace" });
  if (node) body.set("node", node);
  return post("/messages", body);
}

// withdraw takes back a message that waits for Eriphe: taken when Eriphe
// had read it already.
export async function withdraw(seq: number): Promise<"ok" | "taken" | "error"> {
  const r = await sendJSON("DELETE", `/messages/${seq}`).catch(() => null);
  if (r?.status === 204 || r?.status === 404) return "ok"; // 404: gone already
  return r?.status === 409 ? "taken" : "error";
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
  fromExternal?: boolean; // from quotes words that may be someone else's, marked
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
