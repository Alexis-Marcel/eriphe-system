package a2ui

import (
	"encoding/json"
	"testing"
)

// BoundPaths gives each binding and template list absolute, through the
// templates a component is rendered in, with where it is written.
func TestBoundPaths(t *testing.T) {
	s := surfaceOf(
		`{"id":"root","component":"Column","children":["sales","mails","pic"]}`,
		`{"id":"sales","component":"Metric","label":"Total","value":{"@path":"/sales/total"}}`,
		`{"id":"mails","component":"List","children":{"path":"/threads","componentId":"thread"}}`,
		`{"id":"thread","component":"Column","children":["head","replies"]}`,
		`{"id":"head","component":"Text","text":{"@path":"subject"}}`,
		`{"id":"replies","component":"List","children":{"path":"messages","componentId":"reply"}}`,
		`{"id":"reply","component":"Message","from":{"@path":"from"},"date":{"@call":"formatRelative","args":{"value":{"@path":"date"}}}}`,
		`{"id":"pic","component":"Image","url":{"@path":"/photo"}}`,
	)
	if errs := ValidateSurface(s); len(errs) > 0 {
		t.Fatal(errs)
	}
	got := map[string]Bound{}
	for _, b := range Eriphe().BoundPaths(s.Components) {
		got[b.Path] = b
	}
	for path, want := range map[string]Bound{
		"/sales/total":               {Component: "sales", Type: "Metric", Prop: "value", At: "/1/value/@path"},
		"/threads":                   {List: true, Component: "mails", Prop: "children", At: "/2/children/path"},
		"/threads/*/subject":         {Component: "head", Prop: "text"},
		"/threads/*/messages":        {List: true, Component: "replies"},
		"/threads/*/messages/*/from": {Component: "reply", Prop: "from"},
		"/threads/*/messages/*/date": {Component: "reply", Prop: "date", At: "/6/date/args/value/@path"},
		"/photo":                     {Component: "pic", Media: true},
	} {
		b, ok := got[path]
		if !ok {
			t.Errorf("%s not found in %v", path, got)
			continue
		}
		if b.List != want.List || b.Component != want.Component || b.Media != want.Media ||
			(want.Prop != "" && b.Prop != want.Prop) || (want.At != "" && b.At != want.At) || (want.Type != "" && b.Type != want.Type) {
			t.Errorf("%s: %+v, want %+v", path, b, want)
		}
	}
	if len(got) != 7 {
		t.Errorf("paths %v", got)
	}
	if Eriphe().BoundPaths(json.RawMessage(`{"not":"a list"}`)) != nil {
		t.Error("an object gave paths")
	}
	if !IsMedia("Image", "url") || IsMedia("Image", "alt") || IsMedia("Text", "url") {
		t.Error("IsMedia")
	}
}
