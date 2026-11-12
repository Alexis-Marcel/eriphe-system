// The generic model of the workspace: objects (what exists, named by a ref
// and read from their source by eriphe-core: objects.go, GET/POST
// /api/objects) and a composition (what Eriphe asks to show, by ref, #386).
// No type knows any situation: "a mail and a conflict in the agenda" is never written; it comes
// from the relations between objects.
//
// Obj follows eriphe-core's Object (objects.go): the JSON schema is owned by
// Go, the front follows it.

export type Ref = string;

export type MailObject = {
  id: string; account: string; folder: string; from: string; to?: string[]; cc?: string[];
  subject: string; date: string; unread: boolean; flagged?: boolean;
  attachments?: { filename?: string; type: string; size: number }[];
  body: string; truncated?: boolean; notice?: string;
};

export type EventObject = {
  id: string; account: string; calendar: string; title: string; start: string; end: string;
  all_day?: boolean; location?: string; description?: string; recurring?: boolean; free?: boolean;
};

export type AgendaObject = { date: string; timezone?: string; events: EventObject[] };

export type SessionStep = { key: string; title: string; kind: string; status: string; task?: string };

export type SessionObject = {
  id: number; title: string; goal: string; done_when?: string; state: string; held?: string;
  summary?: string; lead?: string; created: string; started?: string; ended?: string; steps: SessionStep[];
};

export type CardObject = {
  id: number; kind: string; state: string; text: string; why?: string; tainted?: boolean;
  fields?: { name: string; value: string }[]; created: string; decided?: string; proposal: string;
};

export type TaskObject = { id: string; title: string; agent?: string; kind?: string; state: string; node?: string; created: string; updated: string };

export type ProjectObject = { name: string; state: string; text: string; updated: string };

export type Obj = {
  ref: Ref;
  kind: string; // mail, agenda, event, session, card, task, project; group is the workspace's own
  title: string;
  mail?: MailObject;
  agenda?: AgendaObject;
  event?: EventObject;
  session?: SessionObject;
  card?: CardObject;
  task?: TaskObject;
  project?: ProjectObject;
  group?: { refs: Ref[] }; // a composition's group of objects (#386), not read from a source
  related?: Ref[];
};

// An item of a composition: the object it is about, its weight (1 to 5:
// 5 interrupts, 1 is background), a short why, and what Eriphe says when
// it is opened or presented.
export type Item = { ref: Ref; weight: number; why: string; say?: string };

// The layout of the surfaces, owned by eriphe-core (workspaceLayout in
// workspace_layout.go): the event stream's "layout". A tile is a surface at
// its place on the 12 × 8 grid (col and row from 0, w and h in cells); a
// chip, a surface folded in the strip above it.
export type TileSize = "s" | "m" | "l" | "xl";
export type Tile = { id: string; title: string; size: TileSize; col: number; row: number; w: number; h: number; why?: string };
export type FoldedChip = { id: string; title: string; why?: string };
export type Layout = {
  eriphe: string;
  by: string;
  ambient: "calm" | "decision" | "urgent";
  focus?: string;
  items: Tile[];
  chips: FoldedChip[];
  moment?: string;
  at?: string;
  missing?: string[];
};

// A composition, as the composer gives it (#386).
export type Composition = {
  mode: "center" | "side";
  ambient?: "calm" | "urgent" | "decision";
  eriphe: string;
  items: Item[];
  chips?: Ref[];
};

