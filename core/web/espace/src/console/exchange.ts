import { useEffect, useState } from "react";
import type { BarLine, Exchange } from "../data/api";

// What the bar shows (the stream's "exchange", web_console.go): not a
// chat, the user's last message and one line of Eriphe's, of the node it
// talks to, the home's when none. What the user just said shows at once,
// until eriphe-core has it.

// Sent is a message the user just wrote: after is the exchange's last seq
// when it was sent, to the node the bar talked to then.
export type Sent = { text: string; after: number; to: string | null };

export function lastSeq(x: Exchange | null): number {
  return Math.max(x?.asked?.seq ?? 0, x?.said?.seq ?? 0);
}

// landed tells whether the message sent is in the exchange: a message of
// the user's newer than the exchange was when it was sent.
export function landed(x: Exchange | null, sent: Sent | null): boolean {
  return !!sent && (x?.asked?.seq ?? 0) > sent.after;
}

// present is the exchange as the bar shows it: the user's last question
// (asked: the one just sent at once, else the exchange's); Eriphe's
// answer (said), none while a message waits for it; the first line of
// what it is writing (line); whether it is at work; whether it could not
// answer.
export function present(
  x: Exchange | null,
  sent: Sent | null,
): { asked: string | null; said: BarLine | null; line: string | null; writing: boolean; failed: boolean } {
  const p = sent && !landed(x, sent) ? sent : null;
  const line = x?.draft || null;
  return {
    asked: p ? p.text : (x?.asked?.text ?? null),
    said: p ? null : (x?.said ?? null),
    line: p ? null : line,
    writing: !line && (!!p || !!x?.thinking),
    failed: !p && !!x?.failed,
  };
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
