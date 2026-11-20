import { useEffect, useState } from "react";
import type { BarLine, Exchange, Waiting } from "../data/api";

// What the bar shows (the stream's "exchange", web_console.go): not a
// chat, the user's last message and one line of Eriphe's, of the node it
// talks to, the home's when none; at the home, Eriphe's whole answer when
// it is more than a line, held until the user moves on. What the user just said shows at once,
// until eriphe-core has it. Above them, the user's messages that wait for
// Eriphe, each with a clock (messages_waiting.go): the last question is
// the last one Eriphe read.

// Sent is a message the user just wrote: after is the exchange's last seq
// when it was sent, to the node the bar talked to then.
export type Sent = { text: string; after: number; to: string | null };

export function lastSeq(x: Exchange | null): number {
  return Math.max(x?.asked?.seq ?? 0, x?.said?.seq ?? 0, ...(x?.waiting ?? []).map((w) => w.seq));
}

// landed tells whether the message sent is in the exchange: a message of
// the user's newer than the exchange was when it was sent, read by Eriphe
// or waiting for it.
export function landed(x: Exchange | null, sent: Sent | null): boolean {
  return !!sent && ((x?.asked?.seq ?? 0) > sent.after || (x?.waiting ?? []).some((w) => w.seq > sent.after));
}

// WAITING_SHOWN is how many waiting messages the bar shows, the newest:
// the others are counted.
export const WAITING_SHOWN = 3;

// waitingShown is what the bar shows of the messages that wait, but those
// the user withdrew (gone): the newest WAITING_SHOWN, and how many more.
export function waitingShown(x: Exchange | null, gone: ReadonlySet<number>): { shown: Waiting[]; more: number } {
  const all = (x?.waiting ?? []).filter((w) => !gone.has(w.seq));
  const shown = all.slice(-WAITING_SHOWN);
  return { shown, more: all.length - shown.length };
}

// present is the exchange as the bar shows it: the user's last question
// (asked: the one just sent at once, else the exchange's); Eriphe's
// whole answer given at the home (answer), held, and its line (said),
// which goes, none while a message waits for it; the first line of what
// it is writing (line); whether it is at work; whether it could not
// answer.
export function present(
  x: Exchange | null,
  sent: Sent | null,
): { asked: string | null; answer: BarLine | null; said: BarLine | null; line: string | null; writing: boolean; failed: boolean } {
  const p = sent && !landed(x, sent) ? sent : null;
  const line = x?.draft || null;
  return {
    asked: p ? p.text : (x?.asked?.text ?? null),
    answer: p ? null : (x?.answer ?? null),
    said: p ? null : (x?.said ?? null),
    line: p ? null : line,
    writing: !line && (!!p || !!x?.thinking),
    failed: !p && !!x?.failed,
  };
}

// The answers given at the home that the user closed (✕): the last one's
// seq, kept in the browser so that a reload does not bring it back. Seqs
// only grow: every answer up to it is closed.
const CLOSED = "eriphe.espace.answer.closed";

export function closedAnswer(): number {
  try {
    return Number(localStorage.getItem(CLOSED)) || 0;
  } catch {
    return 0;
  }
}

export function closeAnswer(seq: number): void {
  try {
    if (seq > closedAnswer()) localStorage.setItem(CLOSED, String(seq));
  } catch {
    // no storage: closed for this page only
  }
}

// ACK_MS is how long Eriphe's line stays in the bar (espace.css fades it
// out at its end: cs-ack-out).
export const ACK_MS = 6000;

// met is when the bar first met each line of Eriphe's: the same one sent
// again, or met again on a page the bar talked to before, goes on fading
// from then, and once gone is not shown again.
const met = new Map<string, number>();

// useAck tells whether Eriphe's line (key: the node and its seq) shows:
// for ACK_MS from when the bar first met it.
export function useAck(key: string | null): boolean {
  const [, tick] = useState(0);
  if (key && !met.has(key)) met.set(key, Date.now());
  const left = key ? ACK_MS - (Date.now() - (met.get(key) ?? 0)) : 0;
  useEffect(() => {
    if (left <= 0) return;
    const t = setTimeout(() => tick((n) => n + 1), left);
    return () => clearTimeout(t);
  }, [key, left > 0]);
  return left > 0;
}
