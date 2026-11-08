package main

import (
	"slices"
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
	if got := sendChannels(); !slices.Equal(got, []string{chanPush, chanTelegram}) {
		t.Errorf("send channels: %v", got)
	}
	voice, ok := channelKindOf(chanVoice)
	if !ok || voice.Speaker != speakerRoom || voice.Carries != carrySentence || voice.sends() {
		t.Errorf("voice: %+v", voice)
	}
	if ws, _ := channelKindOf(chanWorkspace); ws.Speaker != speakerUser || ws.out() {
		t.Errorf("workspace: %+v", ws)
	}
	if tg, _ := channelKindOf(chanTelegram); tg.Dir != dirBoth || tg.Speaker != speakerAccount {
		t.Errorf("telegram: %+v", tg)
	}
}

// Only the channels a note is broadcast on may be named by a rule or the
// default: never the voice, which only the router's line reaches.
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
