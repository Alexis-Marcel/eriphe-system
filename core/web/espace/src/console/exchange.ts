import type { Exchange, ExchangeLine } from "../data/api";

// What the console shows of the conversation (the stream's "exchange",
// web_console.go): not a second chat, the last exchange of the node it
// talks to, the home's when none. What the user just said shows at once,
// until eriphe-core has it.

// Sent is a message the user just wrote: after is the exchange's last seq
// when it was sent, to the node the console talked to then.
export type Sent = { text: string; after: number; to: string | null };

export function lastSeq(x: Exchange | null): number {
  return x?.lines.reduce((m, l) => Math.max(m, l.seq), 0) ?? 0;
}

// landed tells whether the message sent is in the exchange: a message of
// the user's newer than the exchange was when it was sent.
export function landed(x: Exchange | null, sent: Sent | null): boolean {
  return !!sent && !!x?.lines.some((l) => l.role === "user" && l.seq > sent.after);
}

export type Item = { kind: "line"; line: ExchangeLine } | { kind: "draft"; text: string };

// present is the last exchange as the console shows it: the message sent,
// alone until it lands, or the event's lines; the draft; whether Eriphe is
// at work; the replies it suggests, under its answer only, when nothing
// else is on its way; and Eriphe's line to show by the orb (aside): its
// newest, or what it is writing.
export function present(x: Exchange | null, sent: Sent | null): { items: Item[]; writing: boolean; replies: string[]; aside: string | null } {
  const p = sent && !landed(x, sent) ? sent : null;
  const items: Item[] = p ? [{ kind: "line", line: { seq: -1, role: "user", text: p.text, time: "" } }] : (x?.lines ?? []).map((line) => ({ kind: "line", line }));
  const draft = x?.draft ?? "";
  if (draft) items.push({ kind: "draft", text: draft });
  const writing = !draft && (!!p || !!x?.thinking);
  const last = items.at(-1);
  const answered = !p && !draft && !writing && last?.kind === "line" && last.line.role === "brain" && !last.line.external;
  const aside = draft || (!p && last?.kind === "line" && last.line.role === "brain" ? (last.line.text ?? null) : null);
  return { items, writing, replies: answered ? (x?.suggestions ?? []).slice(0, 3) : [], aside };
}
