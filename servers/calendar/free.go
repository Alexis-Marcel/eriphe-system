package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

type span struct{ start, end time.Time }

// freeTime finds free slots within working hours.
func (c *Calendars) freeTime(ctx context.Context, in freeIn) (freeOut, error) {
	loc, err := c.zone(in.Timezone)
	if err != nil {
		return freeOut{}, err
	}
	from := time.Now().In(loc).Truncate(time.Minute)
	if in.From != "" {
		if from, _, err = parseWhen(in.From, loc); err != nil {
			return freeOut{}, err
		}
	}
	to := from.AddDate(0, 0, 7)
	if in.To != "" {
		if to, _, err = parseWhen(in.To, loc); err != nil {
			return freeOut{}, err
		}
	}
	if !to.After(from) {
		return freeOut{}, errors.New("to must be after from")
	}
	if to.Sub(from) > 62*24*time.Hour {
		return freeOut{}, errors.New("range too long (max 62 days)")
	}
	dur := time.Duration(in.Duration) * time.Minute
	if dur <= 0 {
		dur = 30 * time.Minute
	}
	dayStart, err := clock(in.DayStart, "09:00")
	if err != nil {
		return freeOut{}, err
	}
	dayEnd, err := clock(in.DayEnd, "18:00")
	if err != nil {
		return freeOut{}, err
	}
	if dayEnd <= dayStart {
		return freeOut{}, errors.New("day_end must be after day_start")
	}
	max := in.MaxSlots
	if max <= 0 {
		max = 10
	}
	max = min(max, 50)
	buffer := time.Duration(max0(in.MinGapMinutes)) * time.Minute

	evs, probs, err := c.events(ctx, in.Account, in.Calendar, from, to, loc)
	if err != nil {
		return freeOut{}, err
	}
	var busy []span
	for _, e := range evs {
		if e.Free || (e.AllDay && !in.AllDayBlocks) || (in.ExcludeEventID != "" && e.ID == in.ExcludeEventID) {
			continue
		}
		s, en, err := bounds(e, loc)
		if err != nil {
			continue
		}
		busy = append(busy, span{s.Add(-buffer), en.Add(buffer)})
	}
	busy = merge(busy)

	o := freeOut{Slots: []slot{}, Timezone: loc.String(), Errors: probs}
	for d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc); d.Before(to) && len(o.Slots) < max; d = d.AddDate(0, 0, 1) {
		if !in.Weekends && (d.Weekday() == time.Saturday || d.Weekday() == time.Sunday) {
			continue
		}
		// Wall-clock hours, right across DST changes.
		ws := time.Date(d.Year(), d.Month(), d.Day(), int(dayStart.Hours()), int(dayStart.Minutes())%60, 0, 0, loc)
		we := time.Date(d.Year(), d.Month(), d.Day(), int(dayEnd.Hours()), int(dayEnd.Minutes())%60, 0, 0, loc)
		if ws.Before(from) {
			ws = from
		}
		if we.After(to) {
			we = to
		}
		for _, f := range subtract(span{ws, we}, busy) {
			if f.end.Sub(f.start) >= dur && len(o.Slots) < max {
				o.Slots = append(o.Slots, slot{f.start.Format(time.RFC3339), f.end.Format(time.RFC3339), int(f.end.Sub(f.start).Minutes())})
			}
		}
	}
	return o, nil
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// clock reads HH:MM as an offset from midnight.
func clock(s, def string) (time.Duration, error) {
	if s == "" {
		s = def
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q: use HH:MM", s)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

func bounds(e Event, loc *time.Location) (time.Time, time.Time, error) {
	if e.AllDay {
		s, err := time.ParseInLocation(time.DateOnly, e.Start, loc)
		if err != nil {
			return s, s, err
		}
		en, err := time.ParseInLocation(time.DateOnly, e.End, loc)
		return s, en.AddDate(0, 0, 1), err
	}
	s, err := time.Parse(time.RFC3339, e.Start)
	if err != nil {
		return s, s, err
	}
	en, err := time.Parse(time.RFC3339, e.End)
	return s, en, err
}

func merge(in []span) []span {
	sort.Slice(in, func(i, j int) bool { return in[i].start.Before(in[j].start) })
	var out []span
	for _, s := range in {
		if n := len(out); n > 0 && !s.start.After(out[n-1].end) {
			if s.end.After(out[n-1].end) {
				out[n-1].end = s.end
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// subtract removes the busy spans (sorted, merged) from w.
func subtract(w span, busy []span) []span {
	var out []span
	cur := w.start
	for _, b := range busy {
		if !b.end.After(cur) {
			continue
		}
		if !b.start.Before(w.end) {
			break
		}
		if b.start.After(cur) {
			out = append(out, span{cur, b.start})
		}
		if b.end.After(cur) {
			cur = b.end
		}
	}
	if cur.Before(w.end) {
		out = append(out, span{cur, w.end})
	}
	return out
}
