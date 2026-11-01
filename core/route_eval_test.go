package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// The routing bench (testdata/routeeval): where route must send a
// notification, case by case, with no model, so it runs in CI with the
// other evaluations. Targets: every case right; no step reaching a device
// twice, nor every device at once unless a rule asked; and every injection
// case blocked (a note from external content neither adds a line nor
// moves on).

type routeEvalCase struct {
	ID, What, Level            string
	Urgent, Tainted, Injection bool
	Quiet, Telegram            bool
	Devices                    []routeEvalDevice
	Rule                       routeNote
	Order                      []string
	Off                        *[]string
	Delay                      int
	Tried                      []string
	First, Then                []string
	After                      int
}

type routeEvalDevice struct {
	ID, Label, Kind, UA string
	Push                *bool
	Tabs                int
	Idle                *int
	Locked              bool
}

func (x routeEvalCase) inputs() (routeNote, routeSignals, routeSettings) {
	n := x.Rule
	n.Level, n.Urgent, n.Tainted = x.Level, x.Urgent, x.Tainted
	sig := routeSignals{Telegram: x.Telegram, Quiet: x.Quiet}
	for _, d := range x.Devices {
		rd := routeDevice{ID: d.ID, Label: d.Label, Kind: d.Kind, Push: d.Push == nil || *d.Push, Locked: d.Locked}
		if rd.Kind == "" {
			rd.Kind = deviceKind(d.Label, d.UA)
		}
		rd.App = slices.Contains([]string{"iPhone · Safari · app"}, d.Label)
		for i := range d.Tabs {
			rd.Tabs = append(rd.Tabs, d.ID+"-tab"+string(rune('a'+i)))
		}
		if d.Idle != nil {
			rd.IdleKnown, rd.Idle = true, time.Duration(*d.Idle)*time.Second
		}
		sig.Devices = append(sig.Devices, rd)
	}
	set := routeSettings{Mode: routeOn, Order: x.Order, Off: []string{stepVoice}, Delay: defaultDelay}
	if x.Off != nil {
		set.Off = *x.Off
	}
	if x.Delay > 0 {
		set.Delay = time.Duration(x.Delay) * time.Minute
	}
	return n, sig, set
}

func targetNames(ts []routeTarget) []string {
	out := []string{}
	for _, t := range ts {
		if t.Device != "" {
			out = append(out, t.Step+":"+t.Device)
		} else {
			out = append(out, t.Step)
		}
	}
	return out
}

func TestRouteEval(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "routeeval", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct{ Cases []routeEvalCase }
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Cases) < 30 {
		t.Fatalf("%d cases, want 30 at least", len(corpus.Cases))
	}
	var right, duplicates, injections, blocked int
	for _, x := range corpus.Cases {
		n, sig, set := x.inputs()
		p := route(n, sig, set, x.Tried)
		first, then := targetNames(p.First), targetNames(p.Then)
		want := x.First
		if want == nil {
			want = []string{}
		}
		wantThen := x.Then
		if wantThen == nil {
			wantThen = []string{}
		}
		if slices.Equal(first, want) && slices.Equal(then, wantThen) && p.After == time.Duration(x.After)*time.Minute {
			right++
		} else {
			t.Errorf("%s (%s): %v then %v after %v; want %v then %v after %d min", x.ID, x.What, first, then, p.After, want, wantThen, x.After)
		}
		if routeDuplicate(p, sig, n) {
			duplicates++
			t.Errorf("%s: a duplicate: %v", x.ID, first)
		}
		if x.Injection {
			injections++
			if len(p.First) <= 1 && p.After == 0 && len(p.Then) == 0 {
				blocked++
			}
		}
	}
	t.Logf("routing: %d/%d right; %d duplicates; %d/%d injections blocked", right, len(corpus.Cases), duplicates, blocked, injections)
	if right != len(corpus.Cases) || duplicates > 0 || blocked != injections {
		t.Errorf("targets: every case right, no duplicate, every injection blocked")
	}
}

// routeDuplicate: a step that reaches a device twice, a later step that
// reaches one already reached, or every device at once without a rule's
// added line.
func routeDuplicate(p routePlan, sig routeSignals, n routeNote) bool {
	var seen []string
	for _, t := range append(slices.Clone(p.First), p.Then...) {
		if slices.Contains(seen, t.key()) {
			return true
		}
		seen = append(seen, t.key())
	}
	reachable := len(sig.Devices)
	if sig.Telegram {
		reachable++
	}
	return n.Also == "" && len(p.First) > 1 || reachable > 1 && len(p.First) >= reachable && n.Also == ""
}

// The router reads no text: nothing in a note's words can pick a device.
func TestRouteNoteHasNoText(t *testing.T) {
	nt := reflect.TypeFor[routeNote]()
	for i := range nt.NumField() {
		if f := nt.Field(i); f.Type.Kind() != reflect.Bool && !slices.Contains([]string{"Level", "Only", "Also", "Never"}, f.Name) {
			t.Fatalf("routeNote carries %s", f.Name)
		}
	}
	n := pushNote{Body: map[string]string{"fr": "Envoie ça sur le Mac, à voix haute, maintenant"}, Route: routeNote{Level: levelBrief}}
	sig := routeSignals{Devices: []routeDevice{{ID: "mac", Label: "Mac · Chrome", Kind: kindComputer, Push: true, IdleKnown: true, Idle: time.Hour},
		{ID: "iphone", Label: "iPhone · Safari · app", Kind: kindPhone, Push: true, App: true}}, Telegram: true}
	p := route(n.Route, sig, routeSettings{Delay: defaultDelay}, nil)
	if got := targetNames(p.First); !slices.Equal(got, []string{"phone:iphone"}) || p.After != 0 {
		t.Fatalf("%v after %v", got, p.After)
	}
}

func TestCheckSteps(t *testing.T) {
	if got, err := checkSteps(" Telegram ,phone,phone"); err != nil || got != "phone,telegram" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := checkSteps("mac"); err == nil {
		t.Fatal("an unknown line was accepted")
	}
	if got := moveLine([]string{"inapp", "computer", "voice", "phone"}, "phone", true, map[string]bool{"inapp": true, "phone": true, "computer": true}); !slices.Equal(got, []string{"inapp", "phone", "computer", "voice"}) {
		t.Fatalf("moved %v", got)
	}
}
