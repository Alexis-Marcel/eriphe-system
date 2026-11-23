import { useEffect, useState, type ReactNode } from "react";
import type { Lang, T } from "../i18n";
import { InlineYes } from "./inline";
import { day, locale, minutesOf, time, wall } from "./format";

// The catalog's Agenda: anything from a calendar, one block. Its first day
// is an hour column (today's with a line at the time it is now), the next
// one a compact column, the days after a strip of small columns (a list of
// days when the block is narrow). Placement is classes on a grid of
// half-hour rows (.a2-r-N starts at row N, .a2-span-N spans N rows,
// .a2-lane-N is the column of an event that overlaps another, .a2-lane-all
// the full width of one that does not), never a style: the CSP refuses
// them.
//
// What waits for the user is drawn one way, a ghost (a dashed violet
// outline), whatever it is: an invitation the user has not answered (an
// event whose status is "needs-action"), or a time Eriphe proposes (a
// dated card, read from eriphe-core). A ghost carries its card's Oui when
// it has one: an invitation's is the card that waits on its item (told out
// of band, as a List's rows are: never from the block's data), a
// proposal's its own. A status is always said in the user's words, never
// as the source wrote it; one the page does not know says nothing.

export type AgendaEvent = { title: string; start: string; end?: string; place?: string; allDay?: boolean; status?: string };

// A ghost: a proposal's time; ref is the proposal's card.
export type Ghost = AgendaEvent & { ref: string; tainted?: boolean };

// The statuses the page says, and how sources write them.
export type Status = "" | "tentative" | "needs-action" | "declined" | "cancelled";
const STATUS: Record<string, Status> = {
  confirmed: "", accepted: "", busy: "",
  tentative: "tentative", maybe: "tentative",
  "needs-action": "needs-action", needsaction: "needs-action", needs_action: "needs-action", invited: "needs-action",
  declined: "declined", refused: "declined",
  cancelled: "cancelled", canceled: "cancelled",
};
export const statusOf = (raw: unknown): Status => (typeof raw === "string" ? (STATUS[raw.trim().toLowerCase()] ?? "") : "");

const SLOT = 30; // minutes a grid row stands for
const LANES = 3; // columns at most; a fourth overlap shares the last
export const AGENDA_DAYS = 7; // days at most

export function cleanEvents(v: unknown): AgendaEvent[] {
  if (!Array.isArray(v)) return [];
  return v.filter(
    (e): e is AgendaEvent => !!e && typeof e === "object" && typeof e.title === "string" && typeof e.start === "string",
  );
}

// An entry of a day: an event (n, its place in the block's events) or a
// proposal (ref); waits when it asks the user's answer.
export type Entry = AgendaEvent & { n?: number; ghost?: string; tainted?: boolean; st: Status; waits: boolean };
// clash: for an entry that waits, the events it overlaps; for an event,
// whether one that waits overlaps it.
type Placed = { ev: Entry; start: number; end: number; lane: number; overlap: boolean; clash: string[] };

export function entries(events: AgendaEvent[], ghosts: Ghost[] = []): Entry[] {
  return [
    ...events.map((e, n) => {
      const st = statusOf(e.status);
      return { ...e, n, st, waits: st === "needs-action" };
    }),
    ...ghosts.map((g) => ({ ...g, ghost: g.ref, st: "" as Status, waits: true })),
  ];
}

// place puts each timed entry of a day in a lane: the first free one.
export function place(items: Entry[], date: string): { timed: Placed[]; allDay: Entry[] } {
  const allDay: Entry[] = [];
  const timed: Placed[] = [];
  for (const ev of items) {
    const s = wall(ev.start);
    if (!s) continue;
    // An event of another day is not this day's; one across midnight
    // shows its part of it.
    const e = wall(ev.end);
    const startDay = ev.start.slice(0, 10), endDay = e ? ev.end!.slice(0, 10) : startDay;
    if (startDay > date || endDay < date) continue;
    if (ev.allDay || !s.time) {
      allDay.push(ev);
      continue;
    }
    const start = startDay === date ? minutesOf(s) : 0;
    let end = !e ? start + SLOT : endDay === date ? minutesOf(e) : 24 * 60;
    if (end <= start) end = start + SLOT;
    timed.push({ ev, start, end: Math.min(end, 24 * 60), lane: 0, overlap: false, clash: [] });
  }
  timed.sort((a, b) => a.start - b.start || b.end - a.end);
  const laneEnds: number[] = [];
  for (const p of timed) {
    let lane = laneEnds.findIndex((end) => end <= p.start);
    if (lane < 0) lane = laneEnds.length;
    laneEnds[lane] = p.end;
    p.lane = lane;
  }
  const off = (q: Placed) => q.ev.st === "declined" || q.ev.st === "cancelled";
  const meets = (p: Placed, q: Placed) => q !== p && q.start < p.end && p.start < q.end;
  for (const p of timed) {
    p.overlap = timed.some((q) => meets(p, q));
    p.clash = timed.filter((q) => meets(p, q) && !off(q) && !off(p) && p.ev.waits !== q.ev.waits).map((q) => q.ev.title);
  }
  return { timed, allDay };
}

// The date n days after an ISO date, as an ISO date.
export function addDays(date: string, n: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

// The user's date and minutes now, on their clock.
export function today(now: Date): { date: string; min: number } {
  const p = (n: number) => String(n).padStart(2, "0");
  return { date: `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`, min: now.getHours() * 60 + now.getMinutes() };
}

// useNow is the time now, again each minute.
export function useNow(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(id);
  }, []);
  return now;
}

// Where an Agenda is answered from, and the card each of its events
// carries (eriphe-core's, out of band; none outside a page).
export type AgendaCards = { where: { surfaceId: string; componentId: string }; card: (n: number) => string | undefined };

type Props = { date: string; days: number; events: AgendaEvent[]; ghosts: Ghost[]; cards?: AgendaCards; lang: Lang; t: T; now: Date };

export function Agenda({ date, days, events, ghosts, cards, lang, t, now }: Props) {
  const all = entries(events, ghosts);
  const n = Math.max(1, Math.min(AGENDA_DAYS, Math.round(days)));
  const dates = Array.from({ length: n }, (_, i) => addDays(date, i));
  const label = dayName(lang, t, date, now);
  return (
    <section className={`a2-agenda a2-agenda-n${Math.min(n, 3)}`} aria-label={label.join(", ")}>
      <div className="a2-agenda-main">
        <HourDay date={date} items={all} cards={cards} lang={lang} t={t} now={now} />
        {n > 1 && <ListDay date={dates[1]} items={all} cards={cards} lang={lang} t={t} now={now} />}
      </div>
      {n > 2 && (
        <ol className="a2-agenda-week">
          {dates.slice(2).map((d) => (
            <ListDay key={d} date={d} items={all} cards={cards} lang={lang} t={t} now={now} small />
          ))}
        </ol>
      )}
    </section>
  );
}

// A day's name: « Aujourd'hui » and its date, « Demain », else its date.
function dayName(lang: Lang, t: T, date: string, now: Date, small = false): [string, string] {
  const w = wall(date)!;
  const d = today(now).date;
  const long = day(lang, w);
  const short = new Intl.DateTimeFormat(locale(lang), { weekday: "short", day: "numeric", timeZone: "UTC" }).format(w.at);
  if (date === d) return [t("espace.a2.days.today"), long];
  if (date === addDays(d, 1)) return [t("espace.a2.days.tomorrow"), small ? short : long];
  return small ? [short, ""] : [long.charAt(0).toUpperCase() + long.slice(1), ""];
}

function Head({ name }: { name: [string, string] }) {
  return (
    <p className="a2-agenda-head">
      <span className="a2-agenda-name">{name[0]}</span>
      {name[1] && <span className="a2-agenda-date">{name[1]}</span>}
    </p>
  );
}

// What an entry says of itself besides its title: « à répondre », « peut-être »,
// « Proposé »…
function said(t: T, ev: Entry): string {
  if (ev.ghost) return t(ev.tainted ? "espace.a2.days.proposed.tainted" : "espace.a2.days.proposed");
  return ev.st ? t(`espace.a2.days.status.${ev.st}`) : "";
}

const stClass = (ev: Entry) => [ev.waits ? "a2-ev-waits" : "", ev.st ? `a2-ev-${ev.st}` : ""].filter(Boolean).join(" ");

// yes is the card an entry carries: an invitation's, told by eriphe-core;
// a proposal's, its own.
function yes(ev: Entry, cards: AgendaCards | undefined): ReactNode {
  const ref = ev.ghost ?? (ev.n !== undefined ? cards?.card(ev.n) : undefined);
  if (!ref || !ev.waits) return null;
  return <InlineYes refText={ref} where={cards?.where ?? { surfaceId: "", componentId: "" }} />;
}

function span(lang: Lang, ev: Entry, compact = false): string {
  const s = wall(ev.start), e = wall(ev.end);
  if (!s?.time) return "";
  return e?.time ? `${time(lang, s, compact)} – ${time(lang, e, compact)}` : time(lang, s, compact);
}

function HourDay({ date, items, cards, lang, t, now }: { date: string; items: Entry[]; cards?: AgendaCards; lang: Lang; t: T; now: Date }) {
  const { timed, allDay } = place(items, date);
  const here = today(now);
  const isToday = date === here.date;
  // The hours shown: from the first entry's (or now) to the last's (or a
  // little after now), four at least.
  const starts = timed.map((p) => p.start), ends = timed.map((p) => p.end);
  if (isToday) starts.push(here.min), ends.push(here.min + SLOT);
  let from = starts.length ? Math.floor(Math.min(...starts) / 60) : 8;
  let to = ends.length ? Math.ceil(Math.max(...ends) / 60) : 12;
  if (to - from < 4) to = Math.min(24, from + 4);
  if (to - from < 4) from = Math.max(0, to - 4);
  const lanes = Math.min(LANES, Math.max(1, ...timed.map((p) => p.lane + 1)));
  const row = (min: number) => Math.max(0, Math.floor((min - from * 60) / SLOT));
  const hours = Array.from({ length: to - from }, (_, i) => from + i);
  const nowAt = here.min - from * 60;
  const showNow = isToday && nowAt >= 0 && here.min < to * 60;
  const nowW = { at: new Date(Date.UTC(2000, 0, 1, Math.floor(here.min / 60), here.min % 60)), time: true };
  return (
    <div className="a2-agenda-day a2-agenda-hours">
      <Head name={dayName(lang, t, date, now)} />
      {allDay.length > 0 && (
        <ul className="a2-agenda-allday" aria-label={t("espace.a2.allday")}>
          {allDay.map((ev, i) => (
            <li key={i} className={`a2-ev a2-ev-allday ${stClass(ev)}`} data-proposal={ev.ghost}>
              <span className="a2-ev-title">{ev.title}</span>
              {said(t, ev) && <span className="a2-ev-said">{said(t, ev)}</span>}
              {yes(ev, cards)}
            </li>
          ))}
        </ul>
      )}
      <div className={`a2-agenda-grid a2-lanes-${lanes}`} role="list">
        {hours.map((h) => (
          <span key={h} className={`a2-hour a2-r-${(h - from) * 2}`} aria-hidden="true">
            {String(h).padStart(2, "0")}
          </span>
        ))}
        {!timed.length && !allDay.length && <p className="a2-empty a2-agenda-none a2-r-0">{t("espace.a2.days.none")}</p>}
        {timed.map((p, i) => {
          const rows = Math.max(1, Math.ceil((p.end - p.start) / SLOT));
          const past = isToday && p.end <= here.min;
          const word = said(t, p.ev);
          const classes = [
            "a2-ev",
            `a2-r-${row(p.start)}`,
            `a2-span-${Math.min(48, rows)}`,
            p.overlap ? `a2-lane-${Math.min(p.lane, LANES - 1)}` : "a2-lane-all",
            stClass(p.ev),
            past ? "a2-ev-past" : "",
            rows === 1 ? "a2-ev-short" : rows > 2 ? "a2-ev-tall" : "",
          ];
          return (
            <div key={i} role="listitem" className={classes.filter(Boolean).join(" ")} data-proposal={p.ev.ghost}>
              {/* Its title first; then, on a line of their own when it is
                  tall enough, its word (« à répondre »), its time, its place
                  and what it overlaps. */}
              <span className="a2-ev-line">
                <span className="a2-ev-title">{p.ev.title}</span>
                {rows === 1 && word && <span className="a2-ev-said">{word}</span>}
                {rows === 1 && <span className="a2-ev-time">{span(lang, p.ev)}</span>}
              </span>
              {rows > 1 && (
                <span className="a2-ev-line a2-ev-sub">
                  {word && <span className="a2-ev-said">{word}</span>}
                  <span className="a2-ev-time">{span(lang, p.ev)}</span>
                  {p.ev.place && <span className="a2-ev-place">{p.ev.place}</span>}
                </span>
              )}
              {rows > 3 && p.ev.waits && p.clash.length > 0 && (
                <span className="a2-ev-line a2-ev-sub">
                  <span className="a2-ev-clash">{t("espace.a2.days.clash", p.clash.join(", "))}</span>
                </span>
              )}
              {yes(p.ev, cards)}
              {p.ev.waits && p.clash.length > 0 && rows <= 3 && <span className="sr-only">{t("espace.a2.days.clash", p.clash.join(", "))}</span>}
              {p.overlap && !(p.ev.waits && p.clash.length) && <span className="sr-only">{t("espace.a2.days.overlap")}</span>}
            </div>
          );
        })}
        {showNow && (
          <>
            <span className={`a2-now-time a2-r-${row(here.min)} a2-now-${Math.floor((nowAt % SLOT) / 10)}`} aria-hidden="true">
              {time(lang, nowW, true)}
            </span>
            <span className={`a2-now a2-r-${row(here.min)} a2-now-${Math.floor((nowAt % SLOT) / 10)}`} role="presentation">
              <span className="sr-only">{t("espace.a2.days.now", time(lang, nowW))}</span>
            </span>
          </>
        )}
      </div>
    </div>
  );
}

// ListDay is a day as lines: its all-day entries, then its times in order.
function ListDay({ date, items, cards, lang, t, now, small = false }: { date: string; items: Entry[]; cards?: AgendaCards; lang: Lang; t: T; now: Date; small?: boolean }) {
  const { timed, allDay } = place(items, date);
  const w = wall(date)!;
  const weekend = [0, 6].includes(w.at.getUTCDay());
  const lines = [...allDay.map((ev) => ({ ev, at: "" })), ...timed.map((p) => ({ ev: p.ev, at: time(lang, wall(p.ev.start)!, true) }))];
  const Tag = small ? "li" : "div";
  return (
    <Tag className={`a2-agenda-day a2-agenda-list${small ? " a2-agenda-small" : ""}${weekend ? " a2-agenda-weekend" : ""}`}>
      <Head name={dayName(lang, t, date, now, small)} />
      {lines.length ? (
        <ul className="a2-agenda-lines">
          {lines.map(({ ev, at }, i) => {
            const word = said(t, ev);
            return (
              <li key={i} className={`a2-agenda-line ${stClass(ev)}`} data-proposal={ev.ghost}>
                <span className="a2-agenda-at">{at || t("espace.a2.days.allday")}</span>
                <span className="a2-agenda-what">
                  <span className="a2-ev-title">{ev.title}</span>
                  {!small && ev.place && <span className="a2-ev-place">{ev.place}</span>}
                  {word && <span className="a2-ev-said">{word}</span>}
                </span>
                {yes(ev, cards)}
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="a2-empty a2-agenda-free">{t("espace.a2.days.free")}</p>
      )}
    </Tag>
  );
}
