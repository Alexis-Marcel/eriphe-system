import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Markdown } from "./a2ui/markdown";
import type { Exchange, ExchangeLine, Note, Talk } from "./data";
import { useWords, type Lang } from "./i18n";
import { localURL } from "./presence";

// What the workspace shows of the conversation (the stream's "exchange"
// event, web_espace_exchange.go), not a second chat:
//
//   - the left column is the present of the visit (Present): the message
//     Eriphe composed for the arrival, first, then the visit's lines;
//     Eriphe's last line large while nothing follows it, the older ones
//     smaller, the user's as small bubbles;
//   - what the user says about a thread is filed under it (thread_talk.go):
//     with its card open, the exchange shows in the card (CardTalk), the
//     left column keeps one line of it; once over (a newer exchange, or
//     TALK_OVER without a word), it folds into one line of the card, which
//     unfolds on a click.
//
// The page places it all as text; Eriphe's text is Markdown, drawn by the
// page's own renderer (a2ui/markdown.tsx), which makes no HTML of it.

// Sent is a message the user just wrote here, shown before eriphe-core has
// it: after is the exchange's last seq when it was sent, about the surface
// open then.
export type Sent = { text: string; after: number; about?: string };

// TALK_OVER is how long an exchange about a thread stays open in its card
// without a word.
export const TALK_OVER = 10 * 60_000;

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

// talkingAbout is the surface the exchange going on is filed under: the
// message sent's, or the last of the user's.
export function talkingAbout(x: Exchange | null, sent: Sent | null): string | undefined {
  const p = pendingOf(x, sent);
  if (p) return p.about;
  return [...(x?.lines ?? [])].reverse().find((l) => l.role === "user")?.thread;
}

export type Item =
  | { kind: "line"; line: ExchangeLine }
  | { kind: "trace"; key: string; thread: string }
  | { kind: "draft"; text: string };

// present is what the left column shows after the arrival message, with
// the surface open (focus): the visit's lines, the exchange about the open
// thread as one line; the message sent, the draft; and whether Eriphe is
// at work there.
export function present(x: Exchange | null, sent: Sent | null, focus: string | null): { items: Item[]; writing: boolean } {
  const items: Item[] = [];
  const inCard = (thread?: string) => !!focus && thread === focus;
  for (const l of x?.lines ?? []) {
    if (!inCard(l.thread)) items.push({ kind: "line", line: l });
    else if (l.role === "user") items.push({ kind: "trace", key: "t" + l.seq, thread: focus! });
  }
  const p = pendingOf(x, sent);
  if (p) items.push(inCard(p.about) ? { kind: "trace", key: "sent", thread: focus! } : { kind: "line", line: { seq: -1, role: "user", text: p.text, time: "" } });
  const here = !inCard(talkingAbout(x, sent));
  const draft = x?.draft && !inCard(x.draftThread) ? x.draft : "";
  if (draft) items.push({ kind: "draft", text: draft });
  return { items, writing: here && !draft && (!!p || !!x?.thinking) };
}

// cardTalk is what the card of a surface shows of the exchanges filed
// under it at now: the one going on (open), the others folded, oldest
// first; and whether Eriphe is at work on it.
export function cardTalk(x: Exchange | null, sent: Sent | null, id: string, now: number): { open: ExchangeLine[]; folded: Talk[]; draft: string; writing: boolean } {
  const talks = x?.threads?.[id] ?? [];
  const p = pendingOf(x, sent);
  const draft = x?.draft && x.draftThread === id ? x.draft : "";
  const about = talkingAbout(x, sent) === id;
  const writing = about && !draft && (!!p || !!x?.thinking);
  if (p && p.about === id) return { open: [{ seq: -1, role: "user", text: p.text, time: "" }], folded: talks, draft, writing };
  const last = talks.at(-1);
  if (!last) return { open: [], folded: [], draft, writing: false };
  // Over: a newer message of the user's, or a while without a word.
  const mine = last.lines[0]?.seq ?? 0;
  const newer = !!p || !!x?.lines.some((l) => l.role === "user" && l.seq > mine);
  const at = Math.max(...last.lines.map((l) => Date.parse(l.time) || 0));
  const going = !newer && (about && (!!draft || !!x?.thinking) || now - at < TALK_OVER);
  return going ? { open: last.lines, folded: talks.slice(0, -1), draft, writing } : { open: [], folded: talks, draft: "", writing: false };
}

// clock words a moment as the thread's header does: « 16 h 10 » today,
// « 13 nov. 16 h 10 » before.
export function clock(time: string, lang: Lang, now = new Date()): string {
  const d = new Date(time);
  if (isNaN(d.getTime())) return "";
  const hm = lang === "en" ? `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}` : `${d.getHours()} h ${String(d.getMinutes()).padStart(2, "0")}`;
  if (d.toDateString() === now.toDateString()) return hm;
  return `${d.toLocaleDateString(lang, { day: "numeric", month: "short" })} ${hm}`;
}

// plain is a text cut to n characters, its Markdown marks left out: a
// folded line is a glimpse, the unfolded one has it whole.
export function plain(text: string, n: number): string {
  const s = text.replace(/[*_`#>~]|\[([^\]]*)\]\([^)]*\)/g, (_, label) => label ?? "").replace(/\s+/g, " ").trim();
  return s.length > n ? s.slice(0, n - 1).trimEnd() + "…" : s;
}

function useNow(every: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(timer);
  }, [every]);
  return now;
}

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
      {line.external && <p className="talk-external">{t("espace.talk.external")}</p>}
    </div>
  );
}

// Present is the left column's conversation: the arrival message, then
// the visit (present), the newest of Eriphe's large.
export function Present({ arrival, x, sent, focus, titleOf, onOpen }: {
  arrival: string;
  x: Exchange | null;
  sent: Sent | null;
  focus: string | null;
  titleOf: (id: string) => string | undefined;
  onOpen: (id: string) => void;
}) {
  const { t } = useWords();
  const { items, writing } = present(x, sent, focus);
  const lastItem = items.at(-1);
  const latest = writing ? null : lastItem ? (lastItem.kind === "draft" || (lastItem.kind === "line" && lastItem.line.role === "brain") ? lastItem : null) : "arrival";
  const box = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const b = box.current;
    if (b && typeof b.scrollTo === "function") b.scrollTo({ top: b.scrollHeight });
  }, [items.length, writing, x?.draft]);
  return (
    <div className="present" ref={box} aria-label={t("espace.exchange")}>
      {arrival && (
        <div className={`present-msg present-arrival${latest === "arrival" ? " latest" : ""}`}>
          <p className="present-said">{arrival}</p>
        </div>
      )}
      {items.map((it) => {
        if (it.kind === "trace") {
          return (
            <p key={it.key} className="present-trace">
              {t("espace.talk.trace", titleOf(it.thread) ?? t("espace.chip.untitled"))}
            </p>
          );
        }
        if (it.kind === "draft") {
          return <Said key="draft" line={{ seq: 0, role: "brain", text: it.text, time: "" }} className={`present-msg present-brain draft${latest === it ? " latest" : ""}`} />;
        }
        const l = it.line;
        if (l.role === "user") {
          const title = l.thread ? titleOf(l.thread) : undefined;
          return (
            <div key={l.seq} className="present-user">
              {l.via && <span className="exchange-who">{t("espace.exchange.via", l.via)}</span>}
              <p className="present-bubble">{l.text}</p>
              {l.thread && title && (
                <button type="button" className="present-about" onClick={() => onOpen(l.thread!)}>
                  {t("espace.talk.about", title)}
                </button>
              )}
            </div>
          );
        }
        return <Said key={l.seq} line={l} className={`present-msg present-brain${latest === it ? " latest" : ""}`} />;
      })}
      {writing && <Dots />}
    </div>
  );
}

// CardTalk is what a surface's card shows of the exchanges filed under
// it: the folded ones, a line each that unfolds, then the one going on.
export function CardTalk({ id, x, sent }: { id: string; x: Exchange | null; sent: Sent | null }) {
  const { t, lang } = useWords();
  const now = useNow(30_000);
  const [unfolded, setUnfolded] = useState<Record<number, boolean>>({});
  const v = cardTalk(x, sent, id, now);
  if (v.open.length === 0 && v.folded.length === 0 && !v.draft && !v.writing) return null;
  const said = (l: ExchangeLine) =>
    l.role === "user" ? (
      <p key={l.seq} className="talk-user">
        <span className="talk-bubble">{l.text}</span>
      </p>
    ) : (
      <Said key={l.seq} line={l} className="talk-brain" />
    );
  return (
    <section className="talk" aria-label={t("espace.talk")}>
      {v.folded.length > 0 && (
        <ol className="talk-folded">
          {v.folded.map((x) => {
            const user = x.lines[0];
            const reply = [...x.lines].reverse().find((l) => l.role === "brain");
            const open = !!unfolded[user.seq];
            const when = clock(user.time, lang);
            const line = reply ? t("espace.talk.line", when, plain(user.text ?? "", 60), plain(reply.text ?? "", 90)) : t("espace.talk.line.alone", when, plain(user.text ?? "", 60));
            return (
              <li key={user.seq} className={`talk-fold${open ? " open" : ""}`}>
                <button type="button" className="talk-fold-line" aria-expanded={open} onClick={() => setUnfolded((u) => ({ ...u, [user.seq]: !open }))}>
                  {line}
                </button>
                {open && <div className="talk-fold-body">{x.lines.map(said)}</div>}
              </li>
            );
          })}
        </ol>
      )}
      {(v.open.length > 0 || v.draft || v.writing) && (
        <div className="talk-open" aria-live="polite">
          <p className="talk-title">{t("espace.talk")}</p>
          {v.open.map(said)}
          {v.draft && <Said line={{ seq: 0, role: "brain", text: v.draft, time: "" }} className="talk-brain draft" />}
          {v.writing && <Dots />}
        </div>
      )}
    </section>
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
