package main

import (
	"slices"
	"strings"
	"testing"
)

// Every channel says what it is, consistently with its direction.
func TestChannelKinds(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range channelKinds {
		if k.ID == "" || seen[k.ID] {
			t.Fatalf("channel %q: empty or twice", k.ID)
		}
		seen[k.ID] = true
		if k.in() != (k.Speaker != "") {
			t.Errorf("%s: inbound iff it says who speaks (%q)", k.ID, k.Speaker)
		}
		if k.out() != (k.Carries != "") {
			t.Errorf("%s: outbound iff it says what it carries (%q)", k.ID, k.Carries)
		}
		if (k.ready == nil) != (k.deliver == nil) {
			t.Errorf("%s: ready and deliver go together", k.ID)
		}
		if k.deliver != nil && !k.out() {
			t.Errorf("%s: delivers but does not notify", k.ID)
		}
		if k.sends() && k.Carries != carryText {
			t.Errorf("%s: a channel a note is broadcast on carries its text", k.ID)
		}
	}
	if got := sendChannels(); !slices.Equal(got, []string{chanPush, dcChannel, sgChannel, tgChannel}) { // the bot channels by their files' order
		t.Errorf("send channels: %v", got)
	}
	if ws, _ := channelKindOf(chanWorkspace); ws.Speaker != speakerUser || ws.out() {
		t.Errorf("workspace: %+v", ws)
	}
	if tg, _ := channelKindOf(tgChannel); tg.Dir != dirBoth || tg.Speaker != speakerAccount {
		t.Errorf("telegram: %+v", tg)
	}
}

// Only the channels a note is broadcast on may be named by a rule or the
// default: never a channel the user added, which only the router's voice
// line reaches.
func TestCheckChannelsOnlySendChannels(t *testing.T) {
	for _, in := range []string{"voice", "workspace", "push,voice"} {
		if _, err := checkChannels(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// Nothing set up: nobody can be reached, and a broadcast reaches nothing.
func TestChannelsNothingSetUp(t *testing.T) {
	c := &core{}
	if c.anyChannel() {
		t.Error("reachable without a channel")
	}
	for _, k := range channelKinds {
		if k.sends() && (k.ready(c) || len(k.deliver(c, pushNote{})) > 0) {
			t.Errorf("%s: reached without being set up", k.ID)
		}
	}
}

// A channel registered with a line of its own is a line like the others,
// known by nothing but its registration: the router picks it, a rule and
// the settings name it, the where page shows it by its name.
func TestRegisteredChannelIsALine(t *testing.T) {
	saved := slices.Clone(channelKinds)
	t.Cleanup(func() { channelKinds = saved })
	registerChannel(channelKind{ID: "chat", Label: "Chat", Dir: dirBoth, Speaker: speakerAccount, Carries: carryText, Line: true,
		ready: func(*core) bool { return true }, deliver: func(*core, pushNote) []routeTarget { return nil }})
	if lines := routeLines(); lines[len(lines)-1] != "chat" || !slices.Contains(lines, tgChannel) {
		t.Fatalf("lines %v", lines)
	}
	if got, err := checkSteps("chat,phone"); err != nil || got != "phone,chat" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := checkChannels("chat,push"); err != nil || got != "push,chat" {
		t.Fatalf("%q %v", got, err)
	}
	if v, err := checkSetting(setDetail, "Chat  alert"); err != nil || v != "chat alert" {
		t.Fatalf("%q %v", v, err)
	}
	sig := routeSignals{Channels: map[string]bool{"chat": true}}
	p := route(routeNote{Level: outcomeNow, First: "chat"}, sig, routeSettings{}, nil)
	if len(p.First) != 1 || p.First[0].Step != "chat" || p.First[0].Label != "Chat" || describeTarget("fr", p.First[0]) != "Chat" {
		t.Fatalf("plan %+v", p)
	}
	if stepChannel("chat") != "chat" || channelNames("fr", "push,chat") != "Appareils et Chat" {
		t.Fatalf("%q %q", stepChannel("chat"), channelNames("fr", "push,chat"))
	}
	if _, err := checkChannelName("chat"); err == nil {
		t.Fatal("an added channel took a registered channel's name")
	}
	if !strings.Contains(channelWords(), "|chat") {
		t.Fatal("a channel's name is not wording about being told")
	}
}
