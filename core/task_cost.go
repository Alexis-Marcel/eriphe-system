package main

import (
	"sort"
	"strings"
	"time"
)

// What the tasks cost, from their turns' tokens at their model's list
// price. A turn_end's cost_usd is no use for a sum: Claude Code's is the
// session's total so far, carried over a resumed session. The amounts are
// list-price equivalents: with a subscription, they are quota.

// modelPrice is a model's list price, in US dollars per million tokens.
// Cache writes are at Claude Code's one-hour cache price, twice the input.
type modelPrice struct{ In, Out, CacheRead, CacheWrite float64 }

// modelPrices are the prices by model family, found in the model's name:
// an alias such as "opus" or a full name such as "claude-opus-5-5".
var modelPrices = []struct {
	family string
	price  modelPrice
}{
	{"opus", modelPrice{4, 20, 0.20, 8}},
	{"sonnet", modelPrice{2, 10, 0.10, 4}},
	{"haiku", modelPrice{1, 5, 0.10, 2}},
}

// priceOf is the price of model, if eriphe-core knows it.
func priceOf(model string) (modelPrice, bool) {
	m := strings.ToLower(model)
	for _, p := range modelPrices {
		if strings.Contains(m, p.family) {
			return p.price, true
		}
	}
	return modelPrice{}, false
}

// turnTokens are tokens of turns, by kind.
type turnTokens struct{ In, Out, CacheRead, CacheWrite int64 }

// cost is what the tokens cost at p.
func (t turnTokens) cost(p modelPrice) float64 {
	return (float64(t.In)*p.In + float64(t.Out)*p.Out + float64(t.CacheRead)*p.CacheRead + float64(t.CacheWrite)*p.CacheWrite) / 1e6
}

// tokenSums are the sums of the usage of the turn_end events e of a query,
// in turnTokens' order.
const tokenSums = `COALESCE(SUM(json_extract(e.data, '$.usage.input_tokens')), 0),
	COALESCE(SUM(json_extract(e.data, '$.usage.output_tokens')), 0),
	COALESCE(SUM(json_extract(e.data, '$.usage.cache_read_tokens')), 0),
	COALESCE(SUM(json_extract(e.data, '$.usage.cache_write_tokens')), 0)`

// spendDays is the window of the cost by day and role.
const spendDays = 7

// daySpend is what the tasks cost on a day, by role: the task's kind
// (worker, brain or an assistant's role).
type daySpend struct {
	Day    string             // YYYY-MM-DD, local time
	ByRole map[string]float64 // US dollars
	Total  float64
}

// Cost is the day's cost of role, for the template.
func (d daySpend) Cost(role string) float64 { return d.ByRole[role] }

// spend is the cost of the turns since since, by day (the latest first)
// and role, with the roles that cost something, sorted.
func (s *Store) spend(since time.Time) (days []daySpend, roles []string) {
	rows, err := s.db.Query(`SELECT date(e.time, 'unixepoch', 'localtime'), t.kind, t.model, `+tokenSums+`
		FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE e.type = 'turn_end' AND e.time >= ?
		GROUP BY 1, 2, 3`, since.Unix())
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	byDay := map[string]*daySpend{}
	seen := map[string]bool{}
	for rows.Next() {
		var day, kind, model string
		var t turnTokens
		if rows.Scan(&day, &kind, &model, &t.In, &t.Out, &t.CacheRead, &t.CacheWrite) != nil {
			continue
		}
		p, ok := priceOf(model)
		if !ok {
			continue
		}
		cost := t.cost(p)
		if cost <= 0 {
			continue
		}
		d := byDay[day]
		if d == nil {
			d = &daySpend{Day: day, ByRole: map[string]float64{}}
			byDay[day] = d
		}
		d.ByRole[kind] += cost
		d.Total += cost
		if !seen[kind] {
			seen[kind] = true
			roles = append(roles, kind)
		}
	}
	for _, d := range byDay {
		days = append(days, *d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Day > days[j].Day })
	sort.Strings(roles)
	return days, roles
}
