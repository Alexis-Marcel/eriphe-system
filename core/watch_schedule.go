package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The schedule of a watch of source horaire: days and times of day, in the
// user's zone. Written as the user would say it, in English or French:
//
//	daily 07:00                 tous les jours 07:00
//	weekdays 07:00              semaine 07:00
//	weekend 09:30               week-end 09:30
//	sunday 18:00                dimanche 18:00
//	monday,thursday 09:00       lundi,jeudi 09:00
//	every 2h 08:00-20:00        toutes les 2h 08:00-20:00
//	weekdays 08:00,12:30        (several times a day)
//
// Every fire is a wall-clock time of a day, computed day by day: a daylight
// saving change neither skips a day nor fires twice.

// recurrence is a parsed schedule: the days it fires on, and its times of
// day in minutes after midnight, sorted.
type recurrence struct {
	Days  [7]bool // by time.Weekday
	Times []int
}

const (
	maxScheduleTimes = 24
	maxEveryHours    = 12
)

var (
	weekdayWords = map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
		"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
		"dimanche": time.Sunday, "lundi": time.Monday, "mardi": time.Tuesday, "mercredi": time.Wednesday,
		"jeudi": time.Thursday, "vendredi": time.Friday, "samedi": time.Saturday,
	}
	allDaysWords   = []string{"daily", "everyday", "jours", "day", "days"}
	weekdaysWords  = []string{"weekdays", "semaine", "ouvrés", "ouvres"}
	weekendWords   = []string{"weekend", "week-end", "weekends"}
	scheduleNoise  = []string{"at", "à", "a", "on", "the", "le", "les", "tous", "chaque", "each", "every", "toutes", "de", "du", "from", "et", "and"}
	clockWordRe    = regexp.MustCompile(`^\d{1,2}[:h]\d{2}$`)
	clockWindowRe  = regexp.MustCompile(`^(\d{1,2}[:h]\d{2})-(\d{1,2}[:h]\d{2})$`)
	everyHoursRe   = regexp.MustCompile(`^(\d{1,2})h$`)
	errNoSchedTime = errors.New("a schedule needs a time of day (07:00) or an interval (every 2h)")
)

// parseSchedule reads a schedule; see above.
func parseSchedule(s string) (recurrence, error) {
	var r recurrence
	words := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(s)), func(c rune) bool { return c == ' ' || c == ',' || c == ';' })
	if len(words) == 0 {
		return r, errNoSchedTime
	}
	anyDay, every := false, 0
	from, to := 0, 23*60+59
	window := false
	for i, w := range words {
		switch {
		case containsStr(allDaysWords, w):
			anyDay = true
			r.Days = [7]bool{true, true, true, true, true, true, true}
		case containsStr(weekdaysWords, w):
			anyDay = true
			for d := time.Monday; d <= time.Friday; d++ {
				r.Days[d] = true
			}
		case containsStr(weekendWords, w):
			anyDay = true
			r.Days[time.Saturday], r.Days[time.Sunday] = true, true
		case isWeekdayWord(w):
			anyDay = true
			r.Days[weekdayWords[w]] = true
		case everyHoursRe.MatchString(w) && i > 0 && containsStr([]string{"every", "les", "toutes"}, words[i-1]):
			every, _ = strconv.Atoi(everyHoursRe.FindStringSubmatch(w)[1])
			if every < 1 || every > maxEveryHours {
				return r, fmt.Errorf("every is 1h to %dh", maxEveryHours)
			}
		case clockWindowRe.MatchString(w):
			m := clockWindowRe.FindStringSubmatch(w)
			var err1, err2 error
			from, err1 = parseClock(m[1])
			to, err2 = parseClock(m[2])
			if err1 != nil || err2 != nil || from >= to {
				return r, fmt.Errorf("%q is not a window HH:MM-HH:MM within a day", w)
			}
			window = true
		case clockWordRe.MatchString(w):
			m, err := parseClock(w)
			if err != nil {
				return r, err
			}
			r.Times = append(r.Times, m)
		case containsStr(scheduleNoise, w):
		default:
			return r, fmt.Errorf("%q is not understood in a schedule: say daily, weekdays, weekend or a day, then HH:MM, or every Nh HH:MM-HH:MM", w)
		}
	}
	if !anyDay {
		r.Days = [7]bool{true, true, true, true, true, true, true}
	}
	switch {
	case every > 0 && len(r.Times) > 0:
		return r, errors.New("give either times of day or an interval, not both")
	case every > 0:
		for m := from; m <= to; m += every * 60 {
			r.Times = append(r.Times, m)
		}
	case window:
		return r, errors.New("a window HH:MM-HH:MM goes with an interval (every 2h)")
	}
	if len(r.Times) == 0 {
		return r, errNoSchedTime
	}
	slices.Sort(r.Times)
	r.Times = slices.Compact(r.Times)
	if len(r.Times) > maxScheduleTimes {
		return r, fmt.Errorf("at most %d times a day", maxScheduleTimes)
	}
	return r, nil
}

func isWeekdayWord(w string) bool {
	_, ok := weekdayWords[w]
	return ok
}

// next is the first fire strictly after after, in loc: day by day, each
// time of the day built as a wall-clock time of that day.
func (r recurrence) next(after time.Time, loc *time.Location) time.Time {
	after = after.In(loc)
	y, m, d := after.Date()
	for i := 0; i <= 8; i++ {
		day := time.Date(y, m, d+i, 12, 0, 0, 0, loc)
		if !r.Days[day.Weekday()] {
			continue
		}
		var best time.Time
		for _, min := range r.Times {
			t := time.Date(y, m, d+i, min/60, min%60, 0, 0, loc)
			if t.After(after) && (best.IsZero() || t.Before(best)) {
				best = t
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{} // no day at all: parseSchedule never gives one
}
