import { useId, type ReactNode } from "react";
import { useWords, type Lang } from "../i18n";
import { time, wall, when } from "./format";
import { Icon as IconView } from "./icons";

// The views a page's callouts and quotes are drawn with (page/Body.tsx,
// page/callouts.tsx): a person, a quote, an alert. Classes only, under
// the workspace's CSP; their fixed words are the app's (web_text_espace.go).

// linkTo is the address a quote's link may open, parsed once: http(s), a host, no
// user or password, nothing a reader could misread; null otherwise.
// The href and the host it shows come from this one parse.
export function linkTo(raw: string): URL | null {
  if (!raw || raw.length > 2048 || /[\s\\\u0000-\u001f\u007f-\u009f]/.test(raw)) return null;
  try {
    const u = new URL(raw);
    if ((u.protocol !== "http:" && u.protocol !== "https:") || u.username || u.password || !u.hostname) return null;
    return u;
  } catch {
    return null;
  }
}

// A sender as people read it: "Sam <sam@example.org>" is Sam.
export const nameOf = (from: string) => from.split(" <")[0].replace(/^"|"$/g, "").trim() || from;

// When a message came: its time today, else its date (and time).
function sent(lang: Lang, iso: string, now = new Date()): string {
  const w = wall(iso);
  if (!w) return "";
  const today = [now.getFullYear(), now.getMonth() + 1, now.getDate()].map((n) => String(n).padStart(2, "0")).join("-");
  return w.time && iso.slice(0, 10) === today ? time(lang, w) : when(lang, w, now);
}

// initials: what a person's circle shows, one or two letters of their name.
export function initials(name: string): string {
  const words = nameOf(name).split(/[\s.\-_]+/).filter((w) => /\p{L}/u.test(w));
  return (words.length > 1 ? words[0][0] + words[words.length - 1][0] : (words[0] ?? "?").slice(0, 1)).toUpperCase();
}

const MAIL = /^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,24}$/;
const PHONE = /^\+?[0-9][0-9 .()-]{4,22}$/;

// factLink is where a fact opens on a click, built by the page from its
// value, never written: a value that is an e-mail address, whole, mails it
// (mailto:, the address alone); one that is a phone number, whole, calls
// it (tel:, its digits). Anything else is text: no other scheme, nothing
// added to the address.
export function factLink(value: string): string | null {
  const v = value.trim();
  if (MAIL.test(v)) return `mailto:${v}`;
  const digits = v.replace(/[^0-9]/g, "");
  if (PHONE.test(v) && digits.length >= 6 && digits.length <= 15) return `tel:${v.startsWith("+") ? "+" : ""}${digits}`;
  return null;
}

// A fact of a Person: its words (a page's keep their bold and links), and
// where it opens (factLink), when it does.
export type PersonFact = { label: ReactNode; value: ReactNode; href?: string | null };

// PersonView draws a page's [!person].
export function PersonView({ name: raw, detail, note, facts }: { name: string; detail?: ReactNode; note?: ReactNode; facts: PersonFact[] }) {
  const id = useId();
  const name = nameOf(raw);
  return (
    <section className="a2-person" aria-labelledby={id}>
      <span className="a2-person-face" aria-hidden="true">{initials(name)}</span>
      <div className="a2-person-main">
        <p className="a2-person-head">
          <span id={id} className="a2-person-name">{name}</span>
          {detail && <span className="a2-person-detail">{detail}</span>}
        </p>
        {note && <p className="a2-person-note">{note}</p>}
        {facts.length > 0 && (
          <dl className="a2-person-facts">
            {facts.slice(0, 8).map((f, i) => (
              <div key={i}>
                <dt>{f.label}</dt>
                <dd>{f.href ? <a href={f.href} rel="noopener noreferrer">{f.value}</a> : f.value}</dd>
              </div>
            ))}
          </dl>
        )}
      </div>
    </section>
  );
}

// QuoteView draws a page's quote (its words
// keep their bold and links). date is ISO; written, a date as its author
// wrote it, shown as is when it reads as no day.
export function QuoteView({ text, from: by, date = "", written, url: u, whole }: { text: ReactNode; from?: string; date?: string; written?: string; url?: URL | null; whole?: boolean }) {
  const { t, lang } = useWords();
  const from = nameOf(by ?? "");
  const day = date && wall(date) ? date : "";
  return (
    <figure className="a2-quote">
      <blockquote className="a2-quote-text" cite={u?.href}>
        {text}
      </blockquote>
      {(from || day || written || u) && (
        <figcaption className="a2-quote-from">
          {from && <span className="a2-quote-who">{from}</span>}
          {day ? (
            <time className="a2-quote-when" dateTime={day}>
              {sent(lang, day)}
            </time>
          ) : (
            written && <span className="a2-quote-when">{written}</span>
          )}
          {u && (
            <a className="a2-link a2-quote-link" href={u.href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer" title={u.href}>
              <IconView name="link" />
              <span className="a2-link-host">{whole ? u.href : u.host}</span>
              <span className="sr-only"> {t("espace.a2.newtab")}</span>
            </a>
          )}
        </figcaption>
      )}
    </figure>
  );
}

const ALERT_ICON = { info: "info", success: "check", warning: "warning", error: "error" } as const;

// AlertView draws a page's [!warning],
// [!info] or [!success] (its words keep their bold and links; its other
// blocks under them, children).
export function AlertView({ level = "info", title, text, children }: { level?: keyof typeof ALERT_ICON; title?: ReactNode; text?: ReactNode; children?: ReactNode }) {
  const { t } = useWords();
  const urgent = level === "warning" || level === "error";
  return (
    <div className={`a2-alert a2-alert-${level}`} role={urgent ? "alert" : "status"}>
      <IconView name={ALERT_ICON[level]} />
      <div>
        <span className="sr-only">{t(`espace.a2.level.${level}`)}, </span>
        {title && <p className="a2-alert-title">{title}</p>}
        {text && <p className="a2-alert-text">{text}</p>}
        {children}
      </div>
    </div>
  );
}
