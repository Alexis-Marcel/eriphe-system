import type { T } from "../i18n";
import type { Lang } from "../i18n";
import { day, minutesOf, time, wall } from "./format";

// The catalog's Day: a day's events placed by the hour, side by side when
// they overlap, the all-day ones in a row above. Placement is classes on a
// grid of half-hour rows (.a2-r-N starts at row N, .a2-span-N spans N
// rows, .a2-lane-N is the column of an event that overlaps another,
// .a2-lane-all the full width of one that does not), never a style: the
// CSP refuses them.
//
// Ghosts are what Eriphe proposes that day and waits for the user's yes
// (a dated proposal, read from eriphe-core, never written by the author):
// placed with the events, drawn apart, and saying which events they would
// overlap. They show, they do not answer: the proposal's card or a
// Proposal component does.

export type DayEvent = { title: string; start: string; end?: string; place?: string; allDay?: boolean };

// A ghost: a proposal's time that day; ref is the proposal's.
export type Ghost = DayEvent & { ref: string; tainted?: boolean };

const SLOT = 30; // minutes a grid row stands for
const LANES = 3; // columns at most; a fourth overlap shares the last

export function cleanEvents(v: unknown): DayEvent[] {
  if (!Array.isArray(v)) return [];
  return v.filter(
    (e): e is DayEvent => !!e && typeof e === "object" && typeof e.title === "string" && typeof e.start === "string",
  );
}

type Item = DayEvent & { ghost?: string; tainted?: boolean };
// clash: for a ghost, the events it overlaps; for an event, whether a ghost
// overlaps it.
type Placed = { ev: Item; start: number; end: number; lane: number; overlap: boolean; clash: string[] };

// place puts each timed event of the day in a lane: the first free one.
// Ghosts take lanes like events, and know which events they overlap.
export function place(events: DayEvent[], date: string, ghosts: Ghost[] = []): { timed: Placed[]; allDay: Item[] } {
  const allDay: Item[] = [];
  const timed: Placed[] = [];
  const items: Item[] = [...events, ...ghosts.map((g) => ({ ...g, ghost: g.ref }))];
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
  const meets = (p: Placed, q: Placed) => q !== p && q.start < p.end && p.start < q.end;
  for (const p of timed) {
    p.overlap = timed.some((q) => meets(p, q));
    p.clash = timed.filter((q) => meets(p, q) && Boolean(p.ev.ghost) !== Boolean(q.ev.ghost)).map((q) => q.ev.title);
  }
  return { timed, allDay };
}

export function Day({ date, events, ghosts = [], lang, t }: { date: string; events: DayEvent[]; ghosts?: Ghost[]; lang: Lang; t: T }) {
  const w = wall(date);
  const { timed, allDay } = place(events, date.slice(0, 10), ghosts);
  const label = w ? day(lang, w) : date;
  // The hours shown: from the first event's to the last's, four at least.
  let from = timed.length ? Math.floor(Math.min(...timed.map((p) => p.start)) / 60) : 8;
  let to = timed.length ? Math.ceil(Math.max(...timed.map((p) => p.end)) / 60) : 12;
  if (to - from < 4) to = Math.min(24, from + 4);
  if (to - from < 4) from = Math.max(0, to - 4);
  const lanes = Math.min(LANES, Math.max(1, ...timed.map((p) => p.lane + 1)));
  const row = (min: number) => Math.max(0, Math.floor((min - from * 60) / SLOT));
  const hours = Array.from({ length: to - from }, (_, i) => from + i);
  const span = (p: Placed) => `${time(lang, wall(p.ev.start)!)}–${p.ev.end && wall(p.ev.end)?.time ? time(lang, wall(p.ev.end)!) : ""}`.replace(/–$/, "");
  const kicker = (ev: Item) => ev.ghost && <span className="a2-ev-kicker">{t(ev.tainted ? "espace.a2.day.ghost.tainted" : "espace.a2.day.ghost")}</span>;

  return (
    <section className="a2-day" aria-label={label}>
      <p className="a2-day-label">{label}</p>
      {allDay.length > 0 && (
        <ul className="a2-day-allday" aria-label={t("espace.a2.allday")}>
          {allDay.map((ev, i) => (
            <li key={i} className={`a2-ev a2-ev-allday${ev.ghost ? " a2-ev-ghost" : ""}`} data-proposal={ev.ghost}>
              {kicker(ev)}
              <span className="a2-ev-title">{ev.title}</span>
              {ev.place && <span className="a2-ev-place">{ev.place}</span>}
            </li>
          ))}
        </ul>
      )}
      {!timed.length && !allDay.length && <p className="a2-empty">{t("espace.a2.day.none")}</p>}
      {timed.length > 0 && (
        <div className={`a2-day-grid a2-lanes-${lanes}`} role="list">
          {hours.map((h) => (
            <span key={h} className={`a2-hour a2-r-${(h - from) * 2}`} aria-hidden="true">
              {String(h).padStart(2, "0")}
            </span>
          ))}
          {timed.map((p, i) => {
            const rows = Math.max(1, Math.ceil((p.end - p.start) / SLOT));
            const ghost = Boolean(p.ev.ghost);
            const classes = [
              "a2-ev",
              `a2-r-${row(p.start)}`,
              `a2-span-${Math.min(48, rows)}`,
              p.overlap ? `a2-lane-${Math.min(p.lane, LANES - 1)}` : "a2-lane-all",
              p.overlap && !ghost ? "a2-ev-overlap" : "",
              ghost ? "a2-ev-ghost" : "",
              p.clash.length ? (ghost ? "a2-ev-clash" : "a2-ev-clashed") : "",
              rows === 1 ? "a2-ev-short" : "",
            ];
            return (
              <div key={i} role="listitem" className={classes.filter(Boolean).join(" ")} data-proposal={p.ev.ghost}>
                {kicker(p.ev)}
                <span className="a2-ev-title">{p.ev.title}</span>
                <span className="a2-ev-time">{span(p)}</span>
                {p.ev.place && rows > 2 && <span className="a2-ev-place">{p.ev.place}</span>}
                {ghost && p.clash.length > 0 && <span className="a2-ev-clash-what">{t("espace.a2.day.clash", p.clash.join(", "))}</span>}
                {p.overlap && !(ghost && p.clash.length) && <span className="sr-only">{t("espace.a2.day.overlap")}</span>}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
