package main

import (
	"context"
	"testing"
)

func TestParseHIDIdle(t *testing.T) {
	out := []byte(`+-o IOHIDSystem  <class IOHIDSystem>
    {
      "HIDIdleTime" = 13744299375
    }`)
	if s, ok := parseHIDIdle(out); !ok || s != 13 {
		t.Fatalf("%d %v", s, ok)
	}
	if _, ok := parseHIDIdle([]byte("nothing")); ok {
		t.Fatal("read an idle time from nothing")
	}
	if !parseLocked([]byte(`"IOConsoleUsers" = ({"CGSSessionScreenIsLocked"=Yes,"kCGSSessionOnConsoleKey"=Yes})`)) {
		t.Fatal("locked not read")
	}
	if parseLocked([]byte(`"IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes})`)) {
		t.Fatal("unlocked read as locked")
	}
}

// Unknown is all-zero: eriphe-core reads nothing from it.
func TestMachineIdleShape(t *testing.T) {
	s := machineIdle(context.Background())
	if !s.Known && (s.Seconds != 0 || s.Locked) {
		t.Fatalf("unknown with values: %+v", s)
	}
}
