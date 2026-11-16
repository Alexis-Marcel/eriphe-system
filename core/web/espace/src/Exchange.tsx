import { useLayoutEffect, useRef, useState, type KeyboardEvent, type TouchEvent, type WheelEvent } from "react";
import { Markdown } from "./a2ui/markdown";
import type { Earlier, Exchange, ExchangeLine, Note } from "./data";
import { useWords } from "./i18n";
import { localURL } from "./presence";

// What the workspace shows of the conversation (the stream's "exchange"
// event, web_espace_exchange.go), not a second chat: the left column keeps
// to the last exchange, the user's last message and Eriphe's answer, after
// what the page itself just said (a sample's note, an error). What the user says updates
// a thread or a surface at the centre, so rereading is rare: scrolling up
// the column shows a button that brings the lines before, a few at a time;
// the conversation (/conversation) has them all.
//
// The page places it all as text; Eriphe's text is Markdown, drawn by the
// page's own renderer (a2ui/markdown.tsx), which makes no HTML of it.
//
// The conversation leads (#613): the exchange is the one of the thread
// open, which the column names (« Sur « titre » »), the home's when none
// is. Under Eriphe's latest answer, the replies it suggested, in its own
// words: a click sends the sentence as the user's message, there.

// Sent is a message the user just wrote here, shown before eriphe-core has
// it: after is the exchange's last seq when it was sent, about the surface
// open then (null: none), where it shows until it lands.
export type Sent = { text: string; after: number; about: string | null };

export function lastSeq(x: Exchange | null): number {
  return x?.lines.reduce((m, l) => Math.max(m, l.seq), 0) ?? 0;
}

// landed tells whether the message sent is in the exchange: a message of
// the user's newer than the exchange was when it was sent.
export function landed(x: Exchange | null, sent: Sent | null): boolean {
  return !!sent && !!x?.lines.some((l) => l.role === "user" && l.seq > sent.after);
}

// pendingOf is the message sent, until it lands.
function pendingOf(x: Exchange | null, sent: Sent | null): Sent | null {
  return sent && !landed(x, sent) ? sent : null;
}

export type Item = { kind: "line"; line: ExchangeLine } | { kind: "draft"; text: string };

// present is the last exchange as the column shows it: the message sent,
// alone until it lands (it is the last exchange then), or the event's
// lines; the draft; whether Eriphe is at work; and the replies it
// suggests, only under its answer, the exchange's last line, when nothing
// else is on its way.
export function present(x: Exchange | null, sent: Sent | null): { items: Item[]; writing: boolean; replies: string[] } {
  const p = pendingOf(x, sent);
  const items: Item[] = p ? [{ kind: "line", line: { seq: -1, role: "user", text: p.text, time: "" } }] : (x?.lines ?? []).map((line) => ({ kind: "line", line }));
  const draft = x?.draft ?? "";
  if (draft) items.push({ kind: "draft", text: draft });
  const writing = !draft && (!!p || !!x?.thinking);
  const last = items.at(-1);
  const answered = !p && !draft && !writing && last?.kind === "line" && last.line.role === "brain" && !last.line.external;
  return { items, writing, replies: answered ? (x?.suggestions ?? []).slice(0, 3) : [] };
}

// upward tells whether a key scrolls the column up.
const upward = (key: string) => key === "ArrowUp" || key === "PageUp" || key === "Home";

function Dots() {
  const { t } = useWords();
  return (
    <p className="exchange-dots" role="status" aria-label={t("espace.exchange.writing")}>
      <i />
      <i />
      <i />
    </p>
  );
}

// Said is a line of Eriphe's: its Markdown, marked when written after
// reading external content; an error says Eriphe could not answer.
function Said({ line, className }: { line: ExchangeLine; className: string }) {
  const { t } = useWords();
  if (line.role === "error") {
    return (
      <p className="exchange-error" role="alert">
        {t("espace.exchange.error")}
      </p>
    );
  }
  return (
    <div className={className}>
      <Markdown text={line.text ?? ""} newTab={t("espace.a2.newtab")} />
      {line.external && <p className="exchange-external">{t("espace.exchange.external")}</p>}
    </div>
  );
}

// Line is a line of the conversation: the user's as a small bubble,
// Eriphe's as text, large when it is the newest.
function Line({ line, latest }: { line: ExchangeLine; latest: boolean }) {
  const { t } = useWords();
  if (line.role !== "user") return <Said line={line} className={`present-msg present-brain${latest ? " latest" : ""}`} />;
  return (
    <div className="present-user">
      {line.via && <span className="exchange-who">{t("espace.exchange.via", line.via)}</span>}
      <p className="present-bubble">{line.text}</p>
    </div>
  );
}

// Before is what the column brought before the exchange whose first line
// is head: the lines, whether more come before them, and whether the user
// scrolled up (the button shows).
type Before = { head: number; lines: ExchangeLine[]; more: boolean; asked: boolean };

// Replies are the replies Eriphe suggests under its answer: a click sends
// the sentence, as the user's own.
function Replies({ replies, onReply }: { replies: string[]; onReply: (text: string) => void }) {
  const { t } = useWords();
  if (replies.length === 0) return null;
  return (
    <div className="exchange-replies" role="group" aria-label={t("espace.exchange.replies")}>
      {replies.map((r) => (
        <button key={r} type="button" className="exchange-reply" onClick={() => onReply(r)}>
          {r}
        </button>
      ))}
    </div>
  );
}

// Present is the left column's conversation: the last exchange, Eriphe's
// newest line large, the replies it suggests under it (onReply sends one).
// Scrolling up shows the button that brings the lines before it (load);
// they stay until the next exchange.
export function Present({ x, sent, load, onReply }: {
  x: Exchange | null;
  sent: Sent | null;
  load: (before: number) => Promise<Earlier>;
  onReply: (text: string) => void;
}) {
  const { t } = useWords();
  const { items, writing, replies } = present(x, sent);
  const pending = !!pendingOf(x, sent);
  const head = pending ? -1 : (x?.lines[0]?.seq ?? 0);
  const [state, setState] = useState<Before>({ head, lines: [], more: false, asked: false });
  const before: Before = state.head === head ? state : { head, lines: [], more: false, asked: false };
  const more = before.lines.length > 0 ? before.more : !pending && head > 0 && !!x?.earlier;
  const [busy, setBusy] = useState(false);

  const lastItem = items.at(-1);
  const latest = writing ? null : lastItem && (lastItem.kind === "draft" || lastItem.line.role === "brain") ? lastItem : null;

  const box = useRef<HTMLDivElement>(null);
  const top = useRef({ top: 0, height: 0, client: 0 });
  const touch = useRef(0);
  const fromBottom = useRef<number | null>(null);
  useLayoutEffect(() => {
    const b = box.current;
    if (b && typeof b.scrollTo === "function") b.scrollTo({ top: b.scrollHeight });
  }, [head, items.length, writing, x?.draft]);
  // Lines brought before: what was shown stays where it was.
  useLayoutEffect(() => {
    const b = box.current;
    if (b && fromBottom.current !== null) b.scrollTop = b.scrollHeight - fromBottom.current;
    fromBottom.current = null;
  }, [before.lines.length]);

  const ask = () => {
    if (!before.asked) setState({ ...before, asked: true });
  };
  // A scroll up by the user, not the column's content or size changing
  // under a view kept at its end.
  const onScroll = () => {
    const b = box.current;
    if (!b) return;
    const was = top.current;
    if (b.scrollTop < was.top && b.scrollHeight === was.height && b.clientHeight === was.client) ask();
    top.current = { top: b.scrollTop, height: b.scrollHeight, client: b.clientHeight };
  };
  const bring = async () => {
    const first = before.lines[0]?.seq ?? x?.lines[0]?.seq;
    if (!first || busy) return;
    setBusy(true);
    try {
      const got = await load(first);
      const b = box.current;
      if (b) fromBottom.current = b.scrollHeight - b.scrollTop;
      setState((cur) => {
        const now = cur.head === head ? cur : before;
        return { head, lines: [...got.lines, ...now.lines], more: got.more && got.lines.length > 0, asked: true };
      });
    } catch {
      /* the conversation has them */
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="present"
      ref={box}
      tabIndex={0}
      aria-label={t("espace.exchange")}
      onScroll={onScroll}
      onWheel={(e: WheelEvent) => e.deltaY < 0 && ask()}
      onTouchStart={(e: TouchEvent) => void (touch.current = e.touches[0]?.clientY ?? 0)}
      onTouchMove={(e: TouchEvent) => (e.touches[0]?.clientY ?? 0) > touch.current && ask()}
      onKeyDown={(e: KeyboardEvent) => upward(e.key) && ask()}
    >
      {before.asked && more && (
        <button type="button" className="present-earlier" onClick={bring} disabled={busy}>
          {t("espace.exchange.earlier")}
        </button>
      )}
      {before.lines.map((l) => (
        <Line key={l.seq} line={l} latest={false} />
      ))}
      {items.map((it) =>
        it.kind === "draft" ? (
          <Said key="draft" line={{ seq: 0, role: "brain", text: it.text, time: "" }} className={`present-msg present-brain draft${latest === it ? " latest" : ""}`} />
        ) : (
          <Line key={it.line.seq} line={it.line} latest={latest === it} />
        ),
      )}
      {writing && <Dots />}
      <Replies replies={replies} onReply={onReply} />
    </div>
  );
}

// The notifications routed to this tab: a notice in the page, which goes
// where the notification says when clicked, and leaves by itself.
export function Notices({ notes, onClose }: { notes: Note[]; onClose: (id: number) => void }) {
  const { t, lang } = useWords();
  if (notes.length === 0) return null;
  return (
    <div className="notices" role="region" aria-label={t("espace.notices")}>
      {notes.map((n) => {
        const text = n.body?.[lang] || n.body?.fr || "";
        return (
          <div key={n.id} className="notice" role="alert">
            {localURL(n.url) ? (
              <a className="notice-text" href={n.url}>
                {text}
              </a>
            ) : (
              <span className="notice-text">{text}</span>
            )}
            <button type="button" className="notice-close" aria-label={t("espace.notice.close")} onClick={() => onClose(n.id)}>
              ×
            </button>
          </div>
        );
      })}
    </div>
  );
}
