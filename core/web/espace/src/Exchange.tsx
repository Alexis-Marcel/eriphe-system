import { Markdown } from "./a2ui/markdown";
import type { Exchange, ExchangeLine, Note } from "./data";
import { CONVO } from "./Fallback";
import { useWords } from "./i18n";
import { localURL } from "./presence";

// What the workspace shows of the conversation: not a second chat, the
// last exchange only, near the input (the stream's "exchange" event,
// web_espace_exchange.go): the user's last message and what Eriphe said
// after it, as it writes it; folded by a click, with the way to the whole
// conversation. Eriphe's text is Markdown, drawn by the page's own
// renderer (a2ui/markdown.tsx), which makes no HTML of it.

// Sent is a message the user just wrote here, shown before eriphe-core has
// it: after is the exchange's last seq when it was sent.
export type Sent = { text: string; after: number };

export function lastSeq(x: Exchange | null): number {
  return x?.lines.reduce((m, l) => Math.max(m, l.seq), 0) ?? 0;
}

// landed tells whether the message sent is in the exchange: a message of
// the user's newer than the exchange was when it was sent.
export function landed(x: Exchange | null, sent: Sent | null): boolean {
  return !!sent && !!x?.lines.some((l) => l.role === "user" && l.seq > sent.after);
}

// shown is what the page draws: the exchange, or the message sent until it
// lands, and whether Eriphe is at work.
export function shown(x: Exchange | null, sent: Sent | null): { lines: ExchangeLine[]; draft: string; writing: boolean } {
  if (sent && !landed(x, sent)) {
    return { lines: [{ seq: -1, role: "user", text: sent.text, time: "" }], draft: "", writing: true };
  }
  const lines = x?.lines ?? [];
  const draft = x?.draft ?? "";
  return { lines, draft, writing: !!draft || !!x?.thinking };
}

export function ExchangeView({ x, sent, open, onToggle }: { x: Exchange | null; sent: Sent | null; open: boolean; onToggle: () => void }) {
  const { t } = useWords();
  const v = shown(x, sent);
  if (v.lines.length === 0 && !v.writing) return null;
  const newTab = t("espace.a2.newtab");
  return (
    <section className={`exchange${open ? " open" : ""}`} aria-label={t("espace.exchange")}>
      <button type="button" className="exchange-toggle" aria-expanded={open} onClick={onToggle}>
        {v.writing ? t("espace.exchange.writing") : t("espace.exchange")}
      </button>
      {open && (
        <div className="exchange-body" aria-live="polite">
          {v.lines.map((l) =>
            l.role === "user" ? (
              <p key={l.seq} className="exchange-user">
                <span className="exchange-who">{l.via ? t("espace.exchange.via", l.via) : t("espace.exchange.you")}</span>
                {l.text}
              </p>
            ) : l.role === "error" ? (
              <p key={l.seq} className="exchange-error" role="alert">
                {t("espace.exchange.error")}
              </p>
            ) : (
              <div key={l.seq} className="exchange-brain">
                <Markdown text={l.text ?? ""} newTab={newTab} />
              </div>
            ),
          )}
          {v.draft ? (
            <div className="exchange-brain draft">
              <Markdown text={v.draft} newTab={newTab} />
            </div>
          ) : (
            v.writing && (
              <p className="exchange-dots" aria-hidden="true">
                <i />
                <i />
                <i />
              </p>
            )
          )}
          <a className="exchange-all" href={CONVO}>
            {t("espace.exchange.all")}
          </a>
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
