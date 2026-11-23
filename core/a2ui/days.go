package a2ui

import "time"

// Components that show a source's items as their own rows, and the days
// they draw (a calendar's events, the proposals of its days). Core tells
// such a component's rows to the page, each with the card that waits on
// it, as it does a List's; and a card that waits with a time on one of
// the days it draws shows there, its Oui with it, rather than at the
// page's foot. The catalog declares both, on the component's schema
// (catalog_v1.json): "x-rows", the property bound to a source's items;
// "x-days", that it draws the dated proposals of its days, read from its
// date, days and proposals. Core names no component.

// componentDoc is a component's schema in Eriphe's catalog; nil for a
// name it does not hold.
func componentDoc(componentType string) map[string]any {
	comps, _ := Eriphe().doc["components"].(map[string]any)
	def, _ := comps[componentType].(map[string]any)
	return def
}

// RowsProp is the property of a component bound to a source's items as
// its rows ("" when it has none).
func RowsProp(componentType string) string {
	p, _ := componentDoc(componentType)["x-rows"].(string)
	return p
}

// MaxDays is the most days a component draws.
const MaxDays = 7

// DaysShown are the days a component, as written, draws the dated
// proposals of: from its date (today when it has none) for its days (1 by
// default). ok is false for a component that draws none: one the catalog
// does not say draws days, one that says "proposals": false, or one whose
// date is not written as a date (a binding: core cannot be sure which days
// the page draws).
func DaysShown(comp map[string]any, today time.Time) (from time.Time, days int, ok bool) {
	kind, _ := comp["component"].(string)
	if draws, _ := componentDoc(kind)["x-days"].(bool); !draws || comp["proposals"] == false {
		return time.Time{}, 0, false
	}
	from = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	if d, set := comp["date"]; set {
		s, _ := d.(string)
		if len(s) < len(time.DateOnly) {
			return time.Time{}, 0, false
		}
		t, err := time.ParseInLocation(time.DateOnly, s[:len(time.DateOnly)], today.Location())
		if err != nil {
			return time.Time{}, 0, false
		}
		from = t
	}
	days = 1
	if n, isNum := comp["days"].(float64); isNum {
		days = max(1, min(MaxDays, int(n)))
	}
	return from, days, true
}
