import type { Lang } from "../i18n";

// The data layer's requests: the one place, with stream.ts, that talks to
// eriphe-core. The page reads
// - /espace/boot: its words, the CSRF token, the assistant's name;
// - GET /api/nodes/{id}[?version=n]: a node's page, its body (Markdown)
//   and its blocks ready to draw, its versions, the cards it waits on
//   (web_graph.go);
// - GET /api/nodes/{id}/trace and /md: its trace, its file as it is;
// - GET /api/proposals/{ref}, ?day=: a proposal a block shows, those of a
//   day (web_proposals.go);
// - GET /api/archives: what the user archived;
// and writes
// - POST /messages: what the user says, to the node the bar talks to;
// - POST /cards/{id}/yes|no: a card's answer;
// - POST /brain/retry: once more the turn Eriphe could not answer;
// - the user's gestures (web_gestures.go): PUT /api/nodes/{id}/md (an edit,
//   made on the version it was read at), POST .../ask (a block's button
//   or a page's ask: link, which only asks), .../task (a task ticked, its
//   line written on the last version), .../open (an item read in full), .../archive and
//   .../reopen, PUT /api/espace/view (the pages open, the display settings:
//   the theme, the graph's forces);
// - POST /presence: whether this tab is in front of the user (presence.ts).
// No control of the page keeps a rule or a preference about what Eriphe
// does: the user says it to Eriphe, who remembers it. Only how the page
// looks is set on it (the theme, the graph's forces). A request without a session gets 401: the page
// goes to the sign-in.

// The workspace as the user left it (espaceView, web_gestures.go): the
// pages open, the one the bar talked to, the last of Eriphe's shows
// taken, the theme ("" follows the system), what the user closed of the
// orb's (orb/model.ts: Closed); the graph's forces the user changed in the
// settings (the others are the defaults, graph/layout.ts) and, false,
// that the open node is not the graph's centre (hud/Settings.tsx).
export type Theme = "" | "light" | "dark";
export type OrbClosed = { answer: number; seen: number; failed: number };
export type View = { open: string[]; talk?: string; shown?: number; theme?: Theme; orb?: OrbClosed; forces?: Record<string, number>; centring?: boolean };

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
  // What the bar may attach to one message (attachments.go): how many
  // files, and how large each, in bytes.
  attach: { many: number; size: number };
};

const json = { Accept: "application/json" };

let csrf = ""; // from the boot

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

// boot reads what the app needs from eriphe-core; a boot that cannot be
// read fails: the shell then offers the conversation (main.tsx), never
// made-up data in place of the user's. The dev server has no boot to read:
// its made-up workspace (dev.tsx) gives one.
export async function boot(url: string): Promise<Boot> {
  const r = signedOut(await fetch(url, { credentials: "same-origin", headers: json }));
  if (!r.ok) throw new Error(`${url}: ${r.status}`);
  const b: Boot = { ...(await r.json()), live: true };
  csrf = b.csrf;
  return b;
}

// Reads is how a page reads a node: from eriphe-core, or, on the dev
// server, from its made-up core (dev.tsx, setReads). The one seam between
// the two: nothing else reads a node another way.
export type Reads = {
  page(id: string, version: number): Promise<NodePage | null>;
  trace(id: string): Promise<TraceEntry[]>;
  file(id: string, version: number): Promise<NodeFile | null>;
  open(id: string, what: OpenWhat): Promise<Opened>;
};

let reads: Reads | null = null;

export function setReads(r: Reads | null): void {
  reads = r;
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
// shown as it is, always marked (graph/Title.tsx). status: computed by
// eriphe-core (node_status.go), what waits for the user first. line: while
// it runs, its task's last word of where it is.
export type GraphNode = {
  id: string;
  title?: string;
  titleExternal?: boolean;
  // unasked: a skill or an event opened it, not the user (a sign says so)
  unasked?: boolean;
  parent?: string;
  links?: string[];
  // maintained: the skills that keep it up, approved and on (eriphe-core computes it): its satellite, its ∞
  maintained?: string[];
  // weight: how much it weighs, computed once by eriphe-core (node_weight.go): its bubble's size and light
  weight: number;
  status: "idle" | "running" | "waits" | "done";
  created: string;
  updated: string;
  done?: string;
  external?: boolean;
  version: number;
  line?: string;
  // glance: a figure of one of its sources, shown as a tile while it is
  // past its threshold and not seen (node_glance.go, graph/glance.ts)
  glance?: Glance;
};

// Glance is a node's glance as eriphe-core reads it (graphGlance,
// node_glance.go): its label (the author's words; labelExternal when its
// block may hold someone else's), its last value, the reference and the
// variation from it (in percent of it, or in its units: amount), its
// threshold, its recent values; shown: past its threshold; seen: opened
// since it crossed.
export type Glance = {
  label: string;
  labelExternal?: boolean;
  unit: "percent" | "amount";
  value: number;
  ref: number;
  delta: number;
  threshold: number;
  series: number[];
  shown?: boolean;
  seen?: boolean;
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
  // body: its Markdown as its file holds it, the page drawn (page/Body.tsx);
  // parts: its legacy blocks, ready to draw where their fences are
  body: string;
  parts: PagePart[];
  // items: its references to catalog servers' items, each where it is
  // written, with the card that waits on it; placed: the cards its lines
  // carry (on an item's row, or a card: link), not at its foot
  // (page_items.go)
  items?: PageItem[];
  placed?: string[];
  // cards: those that still ask, at its foot; settled: those decided, for the « ⋯ » menu
  cards: ProposalView[];
  settled?: ProposalView[];
  refs?: Record<string, PageRef>;
  alive?: PageAlive;
  // origin: who wanted it, as eriphe-core recorded it
  origin?: PageOrigin;
  // tags and props: its tags and its other properties, as its version says them
  tags?: string[];
  props?: PageProp[];
};
// A reference of a page to a catalog server's item ([text][m1], with
// [m1]: mail:<id>), on its line (pageItem, page_items.go): the item's row
// that carries its card and opens it (0: none, its card at the page's
// foot), its id as written, whether its server opens it; the card that
// waits on it, the item as its server read it (external: someone else's
// words), misleading when the line's words do not say it, unread when it
// could not be read (the line's words not checked).
export type PageItem = {
  line: number;
  label: string;
  text: string;
  row?: number;
  id: string;
  open?: boolean;
  card?: string;
  title?: string;
  detail?: string;
  external?: boolean;
  misleading?: boolean;
  unread?: boolean;
};
// A property of a node (pageProp, web_graph.go): its name, its value or
// list of them, and its kind (text, number, date); media: the node's own
// files it names (a photo kept with it), as eriphe-core serves them.
export type PageProp = { key: string; value: unknown; kind: "text" | "number" | "date"; media?: PageMedia[] };
// One of a node's files: its address on eriphe-core, an image or a file to download (a PDF).
export type PageMedia = { src: string; image: boolean };
// What keeps a node a skill maintains alive (pageAlive, node_alive.go): its
// sources, monitors and skills, each named, what it does in code's words.
export type AliveLine = { name: string; what: string };
export type PageOrigin = { unasked?: boolean; text: string };
export type PageAlive = { sources?: AliveLine[]; monitors?: AliveLine[]; skills?: AliveLine[]; updated: string };
// The node a [[link]] of a page's text opens, by the name as written
// (pageRef, web_graph.go): its title, titleExternal when it may be someone
// else's words (shown marked); archived, it opens nothing. A name no node
// has is not there.
export type PageRef = { id: string; title?: string; titleExternal?: boolean; external?: boolean; archived?: boolean };
export type PageVersion = { n: number; author: string; at: string };
export type PagePart = { text?: string; block?: PageBlock };
// A block ready to draw: its components and its data, the sources' last
// values at /<name>; or what is wrong with it.
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
  // filter them, whose rows open, that a batch folds, or on whose rows a
  // card waits (list_rows.go, list_sections.go)
  lists?: SourceList[];
};

// A row of such a List: its place in its source's list (n), its id, what
// it shows, and card, the ref of the card that waits on this item
// (item_cards.go): its Oui is drawn on the row, by the page, never by the
// block. A filtered List shows its rows only, never the source's others.
export type ListRow = { n: number; id: string; title?: string; detail?: string; card?: string };
export type SourceList = { component: string; source: string; filtered?: boolean; open?: boolean; items: ListRow[] };

// What opening an item read (item_open.go): its common fields and the
// whole as JSON, external when its server is not trusted, with the item's
// note; or why not (error: a read that asks a yes is not opened).
export type Opened = {
  title?: string;
  from?: string;
  date?: string;
  body?: string;
  raw?: string;
  external?: boolean;
  // misleading: the page's words for it do not say it (page_items.go)
  misleading?: boolean;
  note?: { group?: string; note?: string; external?: boolean };
  error?: string;
};

// What a click opens: a reference of the page, by its label; or a legacy
// List's row, by its block, its source and the item's id.
export type OpenWhat = { ref: string } | { block: string; source: string; item: string };

// openItem reads in full an item the page references, or a block's
// source lists: the user's gesture (never a yes). eriphe-core finds the
// item in the page as stored.
export async function openItem(id: string, what: OpenWhat): Promise<Opened> {
  if (reads) return reads.open(id, what);
  const r = await sendJSON<Opened>("POST", `${nodePath(id)}/open`, what);
  if (r.status === 200 && r.data) return r.data;
  return { error: r.data?.error ?? String(r.status) };
}

export type TraceEntry = { at: string; author: string; text: string };
// A node's file as it is (md), its body as the user edits it (body), the
// version and its hash, and the last version.
export type NodeFile = { md: string; body: string; version: number; hash: string; last: number };

export function nodePage(id: string, version = 0): Promise<NodePage | null> {
  if (reads) return reads.page(id, version);
  return getJSON<NodePage>(`${nodePath(id)}${version ? `?version=${version}` : ""}`);
}

export async function nodeTrace(id: string): Promise<TraceEntry[]> {
  if (reads) return reads.trace(id);
  return (await getJSON<{ entries: TraceEntry[] }>(`${nodePath(id)}/trace`))?.entries ?? [];
}

export function nodeFile(id: string, version = 0): Promise<NodeFile | null> {
  if (reads) return reads.file(id, version);
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

// ask sends a block's button: eriphe-core reads its words in the block.
export async function ask(id: string, block: string, component: string): Promise<boolean> {
  return (await sendJSON("POST", `${nodePath(id)}/ask`, { block, component })).status === 204;
}

// askLine sends a page's ask: link, the nth of its line: eriphe-core
// reads its words in the page.
export async function askLine(id: string, line: number, n: number): Promise<boolean> {
  return (await sendJSON("POST", `${nodePath(id)}/ask`, { line, n })).status === 204;
}

// tick writes a task's state into its line (text, the line as the page
// read it; line, its number, a hint), on the node's last version.
export async function tick(id: string, line: number, text: string, state: string): Promise<boolean> {
  return (await sendJSON("POST", `${nodePath(id)}/task`, { line, text, state })).status === 200;
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
// messages_waiting.go): sent, not yet read; files: the names of the files
// attached to it; node: the node it was said to, with its title; none:
// the home.
export type Waiting = { seq: number; text: string; files?: string[]; node?: string; title?: string };

// Eriphe's words where they belong (orbWords, web_orb.go): node, none for
// the home; a few plain lines (more: the subject's page says the rest),
// or at the home an answer of more than a line, whole, as Markdown
// (whole); external: written after reading external content.
export type OrbWords = { seq: number; node?: string; text: string; more?: boolean; full?: string; whole?: boolean; external?: boolean };

// The "orb" event (orbEvent, web_orb.go): what Eriphe's orb shows,
// whatever page the bar talks to. asked: the seq of the user's last
// message Eriphe read; work: Eriphe is at work, on that node (none: the
// home), and what it is writing; answer: its answer to that message;
// failed: it could not answer it; cards: the cards that wait for the
// user's yes that no node holds, shown above the bar (one a node holds
// waits in its page: the graph shows it, not the orb). Nothing Eriphe says or does unasked is the orb's.
export type OrbFeed = {
  asked?: number;
  work?: { node?: string; draft?: string };
  answer?: OrbWords;
  failed?: { seq: number; node?: string };
  cards?: string[];
  // The user's messages that wait for Eriphe, whatever node they were
  // said to, oldest first.
  waiting?: Waiting[];
};

// Sending is what came of a message sent: ok, or why it could not go:
// refused, eriphe-core's own words (a file it does not take, too large:
// sendError, web_files.go); else none, it could not be reached.
export type Sending = { ok: true } | { ok: false; refused?: string };

// send writes to Eriphe, said to a node (it belongs to it) or to the home,
// with the files attached to it (attachments.go): as a form when it has
// none, as multipart when it has some.
export async function send(text: string, node?: string | null, files: readonly File[] = []): Promise<Sending> {
  const fields: Record<string, string> = { text, from: "workspace" };
  if (node) fields.node = node;
  let body: URLSearchParams | FormData = new URLSearchParams(fields);
  if (files.length) {
    const form = new FormData();
    for (const [k, v] of Object.entries(fields)) form.set(k, v);
    for (const f of files) form.append("files", f, f.name);
    body = form;
  }
  const r = signedOut(await fetch("/messages", { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf }, body }));
  if (r.ok) return { ok: true };
  const refused = r.status === 400 || r.status === 413 ? (await r.text().catch(() => "")).trim() : "";
  return refused ? { ok: false, refused } : { ok: false };
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
  context?: string; // where it acts, said quietly beside its text (« compte Gmail »)
  why?: string;
  tainted?: boolean;
  // summary: what a yes makes, in plain sentences of code's (a view's);
  // fields: its technical words, a detail one under Détails, a Markdown
  // one (a skill's instructions) drawn as such.
  summary?: string[];
  // A raw one is a call's exact call, one click further than Détails.
  fields?: { name: string; value: string; detail?: boolean; markdown?: boolean; raw?: boolean }[];
  // Where it comes from, in words, with its node; what its Oui does: facts
  // of eriphe-core's, never the model's words.
  node?: string;
  from?: string;
  fromExternal?: boolean; // from quotes words that may be someone else's, marked
  yes?: string;
  when?: Span[];
  exec?: "running" | "done" | "failed";
  execWhy?: string;
  // Failed: why, in the tool's own words (external: marked).
  execError?: string;
  execErrorExternal?: boolean;
  // A card of several calls: each one the user may leave out (its id,
  // what it acts on, how many items, where it stands), and what Oui says
  // for 1, 2… items chosen (call_fold.go).
  choices?: Choice[];
  acts?: string[];
  // What its yes button says: always a verb, never a bare « Oui »
  // (card_verb.go: « Envoyer », « Jeter les 102 »).
  verb: string;
};

// stillAsks tells a card that still asks something of the user: it waits,
// or its yes runs. Done, failed, declined or withdrawn, it is decided:
// it leaves the page (its ⋯ menu lists it), and the orb says how it went.
export function stillAsks(p: Pick<ProposalView, "state" | "exec">): boolean {
  return p.state === "pending" || (p.state === "approved" && p.exec === "running");
}

// items: the items a choice acts on, by id: a batch's rows unticked leave
// out the choices all of whose items they are (call_fold.go).
export type Choice = { id: number; text: string; n: number; state: string; items?: string[] };

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
// calls ticked (only), the others (left) refused; a no naming some of its
// calls (left, a row's ✕), to those alone: the card waits on.
export function answerCard(card: number, yes: boolean, only?: number[], left?: number[]): Promise<boolean> {
  if (!yes && left?.length) return post(`/cards/${card}/no`, new URLSearchParams({ left: left.join(",") }));
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

// What moved: the refs of the proposals made or decided, or all of them;
// nodes, the nodes their cards are linked to (card_moves.go).
export type Moved = { refs: string[]; nodes?: string[]; all?: boolean };
