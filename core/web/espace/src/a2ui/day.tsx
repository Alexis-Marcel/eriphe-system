import type { T } from "../i18n";
import type { Lang } from "../i18n";
import { day, minutesOf, time, wall } from "./format";

// The catalog's Day: a day's events placed by the hour, side by side when
// they overlap, the all-day ones in a row above. Placement is classes on a
// grid of half-hour rows (.a2-r-N starts at row N, .a2-span-N spans N
// rows, .a2-lane-N is the column of an event that overlaps another,
// .a2-lane-all the full width of one that does not), never a style: the
// CSP refuses them.

export type DayEvent = { title: string; start: string; end?: string; place?: string; allDay?: boolean };

const SLOT = 30; // minutes a grid row stands for
const LANES = 3; // columns at most; a fourth overlap shares the last

export function cleanEvents(v: unknown): DayEvent[] {
  if (!Array.isArray(v)) return [];
  return v.filter(
    (e): e is DayEvent => !!e && typeof e === "object" && typeof e.title === "string" && typeof e.start === "string",
  );
}

type Placed = { ev: DayEvent; start: number; end: number; lane: number; overlap: boolean };

// place puts each timed event of the day in a lane: the first free one.
export function place(events: DayEvent[], date: string): { timed: Placed[]; allDay: DayEvent[] } {
  const allDay: DayEvent[] = [];
  const timed: Placed[] = [];
  for (const ev of events) {
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
    timed.push({ ev, start, end: Math.min(end, 24 * 60), lane: 0, overlap: false });
  }
  timed.sort((a, b) => a.start - b.start || b.end - a.end);
  const laneEnds: number[] = [];
  for (const p of timed) {
    let lane = laneEnds.findIndex((end) => end <= p.start);
    if (lane < 0) lane = laneEnds.length;
    laneEnds[lane] = p.end;
    p.lane = lane;
  }
  for (const p of timed) p.overlap = timed.some((q) => q !== p && q.start < p.end && p.start < q.end);
  return { timed, allDay };
}

export function Day({ date, events, lang, t }: { date: string; events: DayEvent[]; lang: Lang; t: T }) {
  const w = wall(date);
  const { timed, allDay } = place(events, date.slice(0, 10));
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

  return (
    <section className="a2-day" aria-label={label}>
      <p className="a2-day-label">{label}</p>
      {allDay.length > 0 && (
        <ul className="a2-day-allday" aria-label={t("espace.a2.allday")}>
          {allDay.map((ev, i) => (
            <li key={i} className="a2-ev a2-ev-allday">
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
            return (
              <div
                key={i}
                role="listitem"
                className={`a2-ev a2-r-${row(p.start)} a2-span-${Math.min(48, rows)} ${p.overlap ? `a2-lane-${Math.min(p.lane, LANES - 1)} a2-ev-overlap` : "a2-lane-all"}${rows === 1 ? " a2-ev-short" : ""}`}
              >
                <span className="a2-ev-title">{p.ev.title}</span>
                <span className="a2-ev-time">{span(p)}</span>
                {p.ev.place && rows > 2 && <span className="a2-ev-place">{p.ev.place}</span>}
                {p.overlap && <span className="sr-only">{t("espace.a2.day.overlap")}</span>}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
