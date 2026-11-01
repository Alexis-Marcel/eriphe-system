package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Time in a question, read by code. "Où vivait-elle début septembre ?",
// "qu'a-t-elle fait la semaine dernière ?": recall turns the explicit
// expression into a range and uses it to reorder (memories dated in the
// range first, facts true then), never to filter, as LongMemEval's
// time-aware expansion does, but without a model: only explicit
// expressions, so nothing is invented (an 8B model "often mistakenly
// extracts a time range", LongMemEval E.4). A day is widened by two days
// on each side.

type timeRange struct {
	From, To time.Time
	Month    time.Month // the range is within this month, else 0
}

func (r timeRange) contains(t time.Time) bool { return !t.Before(r.From) && t.Before(r.To) }

// overlaps tells whether [from, to) meets the range; a zero to is open.
func (r timeRange) overlaps(from, to time.Time) bool {
	return from.Before(r.To) && (to.IsZero() || to.After(r.From))
}

var monthNames = map[string]time.Month{
	"janvier": 1, "fevrier": 2, "mars": 3, "avril": 4, "mai": 5, "juin": 6, "juillet": 7, "aout": 8,
	"septembre": 9, "octobre": 10, "novembre": 11, "decembre": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6, "july": 7, "august": 8,
	"september": 9, "october": 10, "november": 11, "december": 12,
}

var frenchMonth = []string{"", "janvier", "fevrier", "mars", "avril", "mai", "juin", "juillet", "aout", "septembre", "octobre", "novembre", "decembre"}

const monthAlt = `janvier|fevrier|mars|avril|mai|juin|juillet|aout|septembre|octobre|novembre|decembre|january|february|march|april|may|june|july|august|september|october|november|december`

var smallNumbers = map[string]int{"un": 1, "une": 1, "deux": 2, "trois": 3, "quatre": 4, "cinq": 5, "six": 6, "sept": 7, "huit": 8,
	"neuf": 9, "dix": 10, "a": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5}

var (
	reISODate  = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	reDayMonth = regexp.MustCompile(`\b(?:le |du |au |the |on )?(1er|\d{1,2})(?:st|nd|rd|th)? (` + monthAlt + `)(?: (\d{4}))?\b`)
	reMonthDay = regexp.MustCompile(`\b(` + monthAlt + `) (\d{1,2})(?:st|nd|rd|th)?(?:,? (\d{4}))?\b`)
	reMonthQ   = regexp.MustCompile(`\b(debut|fin|mi|en|in|early|late|mid|courant)[- ](` + monthAlt + `)(?: (\d{4}))?\b`)
	reAgo      = regexp.MustCompile(`\bil y a (\d+|un|une|deux|trois|quatre|cinq|six|sept|huit|neuf|dix) (jours?|semaines?|mois|ans?)\b|\b(\d+|a|one|two|three|four|five|six) (days?|weeks?|months?|years?) ago\b`)
	reRelative = []struct {
		re   *regexp.Regexp
		span func(now time.Time) (time.Time, time.Time)
	}{
		{regexp.MustCompile(`\bavant[- ]hier\b|\bthe day before yesterday\b`), func(n time.Time) (time.Time, time.Time) {
			d := dayStart(n).AddDate(0, 0, -2)
			return d, d.AddDate(0, 0, 1)
		}},
		{regexp.MustCompile(`\bhier\b|\byesterday\b`), func(n time.Time) (time.Time, time.Time) {
			d := dayStart(n).AddDate(0, 0, -1)
			return d, d.AddDate(0, 0, 1)
		}},
		{regexp.MustCompile(`\baujourd ?hui\b|\btoday\b`), func(n time.Time) (time.Time, time.Time) { return dayStart(n), n.Add(time.Minute) }},
		{regexp.MustCompile(`\b(la )?semaine (derniere|passee)\b|\blast week\b`), func(n time.Time) (time.Time, time.Time) { w := weekStart(n); return w.AddDate(0, 0, -7), w }},
		{regexp.MustCompile(`\bcette semaine\b|\bthis week\b`), func(n time.Time) (time.Time, time.Time) { return weekStart(n), n.Add(time.Minute) }},
		{regexp.MustCompile(`\b(le )?mois (dernier|passe)\b|\blast month\b`), func(n time.Time) (time.Time, time.Time) {
			m := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, n.Location())
			return m.AddDate(0, -1, 0), m
		}},
		{regexp.MustCompile(`\bce mois[- ]?(ci)?\b|\bthis month\b`), func(n time.Time) (time.Time, time.Time) {
			return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, n.Location()), n.Add(time.Minute)
		}},
		{regexp.MustCompile(`\bl ?(an|annee) (dernier|derniere|passee?)\b|\blast year\b`), func(n time.Time) (time.Time, time.Time) {
			return time.Date(n.Year()-1, 1, 1, 0, 0, 0, 0, n.Location()), time.Date(n.Year(), 1, 1, 0, 0, 0, 0, n.Location())
		}},
	}
)

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// weekStart is the Monday of t's week.
func weekStart(t time.Time) time.Time {
	d := dayStart(t)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// nearestYear is the year that puts month/day closest to now.
func nearestYear(now time.Time, m time.Month, d int) int {
	best, bestGap := now.Year(), time.Duration(1<<62)
	for _, y := range []int{now.Year() - 1, now.Year(), now.Year() + 1} {
		gap := time.Date(y, m, d, 12, 0, 0, 0, now.Location()).Sub(now).Abs()
		if gap < bestGap {
			best, bestGap = y, gap
		}
	}
	return best
}

const dayMargin = 2 * 24 * time.Hour // a day said is widened by two days on each side

func dayRange(y int, m time.Month, d int, loc *time.Location) timeRange {
	day := time.Date(y, m, d, 0, 0, 0, 0, loc)
	return timeRange{From: day.Add(-dayMargin), To: day.AddDate(0, 0, 1).Add(dayMargin), Month: m}
}

// queryTime reads the first explicit time expression of a question; it
// returns the range and the question without it.
func queryTime(q string, now time.Time) (timeRange, string, bool) {
	s := " " + strings.Join(strings.Fields(strings.NewReplacer("'", " ", "’", " ").Replace(foldAccents(q))), " ") + " "
	loc := now.Location()
	cut := func(loc []int) string { return strings.Join(strings.Fields(s[:loc[0]]+" "+s[loc[1]:]), " ") }
	if m := reISODate.FindStringSubmatchIndex(s); m != nil {
		y, _ := strconv.Atoi(s[m[2]:m[3]])
		mo, _ := strconv.Atoi(s[m[4]:m[5]])
		d, _ := strconv.Atoi(s[m[6]:m[7]])
		if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			return dayRange(y, time.Month(mo), d, loc), cut(m[:2]), true
		}
	}
	if m := reMonthQ.FindStringSubmatchIndex(s); m != nil {
		mo := monthNames[s[m[4]:m[5]]]
		y := nearestYear(now, mo, 15)
		if m[6] >= 0 {
			y, _ = strconv.Atoi(s[m[6]:m[7]])
		}
		start := time.Date(y, mo, 1, 0, 0, 0, 0, loc)
		end := start.AddDate(0, 1, 0)
		switch s[m[2]:m[3]] {
		case "debut", "early":
			end = start.AddDate(0, 0, 11)
		case "mi", "mid":
			start, end = start.AddDate(0, 0, 9), start.AddDate(0, 0, 21)
		case "fin", "late":
			start = start.AddDate(0, 0, 19)
		}
		return timeRange{From: start, To: end, Month: mo}, cut(m[:2]), true
	}
	if m := reDayMonth.FindStringSubmatchIndex(s); m != nil {
		d, _ := strconv.Atoi(strings.TrimSuffix(s[m[2]:m[3]], "er"))
		mo := monthNames[s[m[4]:m[5]]]
		y := nearestYear(now, mo, d)
		if m[6] >= 0 {
			y, _ = strconv.Atoi(s[m[6]:m[7]])
		}
		if d >= 1 && d <= 31 {
			return dayRange(y, mo, d, loc), cut(m[:2]), true
		}
	}
	if m := reMonthDay.FindStringSubmatchIndex(s); m != nil {
		mo := monthNames[s[m[2]:m[3]]]
		d, _ := strconv.Atoi(s[m[4]:m[5]])
		y := nearestYear(now, mo, d)
		if m[6] >= 0 {
			y, _ = strconv.Atoi(s[m[6]:m[7]])
		}
		if d >= 1 && d <= 31 {
			return dayRange(y, mo, d, loc), cut(m[:2]), true
		}
	}
	if m := reAgo.FindStringSubmatchIndex(s); m != nil {
		num, unit := "", ""
		if m[2] >= 0 {
			num, unit = s[m[2]:m[3]], s[m[4]:m[5]]
		} else {
			num, unit = s[m[6]:m[7]], s[m[8]:m[9]]
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			n = smallNumbers[num]
		}
		if n > 0 {
			at, margin := dayStart(now), 24*time.Hour
			switch {
			case strings.HasPrefix(unit, "jour"), strings.HasPrefix(unit, "day"):
				at = at.AddDate(0, 0, -n)
			case strings.HasPrefix(unit, "semaine"), strings.HasPrefix(unit, "week"):
				at, margin = at.AddDate(0, 0, -7*n), 3*24*time.Hour
			case unit == "mois", strings.HasPrefix(unit, "month"):
				at, margin = at.AddDate(0, -n, 0), 10*24*time.Hour
			default:
				at, margin = at.AddDate(-n, 0, 0), 60*24*time.Hour
			}
			return timeRange{From: at.Add(-margin), To: at.AddDate(0, 0, 1).Add(margin)}, cut(m[:2]), true
		}
	}
	for _, r := range reRelative {
		if m := r.re.FindStringIndex(s); m != nil {
			from, to := r.span(now)
			tr := timeRange{From: from, To: to}
			if from.Month() == to.Add(-time.Second).Month() {
				tr.Month = from.Month()
			}
			return tr, cut(m), true
		}
	}
	return timeRange{}, q, false
}
