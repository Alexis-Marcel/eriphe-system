// The generic model of the workspace: objects (what exists, named by a ref
// and read from their source by eriphe-core, #384), a composition (what
// Eriphe asks to show, by ref, #386) and rules (what the user said, which
// code applies, #388). No type knows any situation: "a mail and a conflict
// in the agenda" is never written; it comes from the relations between
// objects.

export type Ref = string;

export type Ev = { start: string; end?: string; title: string; where?: string };
export type Step = { title: string; status: "done" | "running" | "waiting"; at?: string; note?: string };

export type Obj =
  | { ref: Ref; type: "mail"; from: string; subject: string; at: string; excerpt: string; source?: string }
  | { ref: Ref; type: "agenda"; date: string; events: Ev[] }
  | { ref: Ref; type: "event"; title: string; start: string; end?: string; where?: string; source?: string }
  | {
      ref: Ref; type: "card"; text: string; tool: string; input: Record<string, string>;
      about?: Ref; since: string; state: "pending" | "yes" | "no";
    }
  | { ref: Ref; type: "session"; title: string; project?: string; goal: string; steps: Step[]; finding?: string }
  | { ref: Ref; type: "choice"; session: Ref; question: string; options: string[]; advice?: string; picked?: string }
  | { ref: Ref; type: "alert"; monitor: string; title: string; severity: string; at: string; count: number; note?: string }
  | { ref: Ref; type: "project"; name: string; due?: string; late?: boolean; next: string; state: string }
  | { ref: Ref; type: "group"; title: string; refs: Ref[] };

export type ObjType = Obj["type"];

// An item of a composition: the object it is about, its weight (1 to 5:
// 5 interrupts, 1 is background), a short why, and what Eriphe says when
// it is opened or presented.
export type Item = { ref: Ref; weight: number; why: string; say?: string };

// A composition, as the composer gives it (#386).
export type Composition = {
  mode: "center" | "side";
  ambient?: "calm" | "urgent" | "decision";
  eriphe: string;
  items: Item[];
  chips?: Ref[];
};

// A rule comes from a remark or a gesture (the ⋯ menu). key is what it
// applies to: a sender's domain, a monitor, a type of object.
export type Rule = { id: string; key: string; delta: number; remark?: string; at: string };
