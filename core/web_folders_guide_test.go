package main

import "testing"

// One guide per system, each naming the user's devices of that system.
func TestShareGuides(t *testing.T) {
	ms := []folderMachine{
		{Name: "mac", System: "mac"},
		{Name: "nas", System: "linux", NAS: true},
		{Name: "old-mac", System: "mac", Lima: true},
	}
	gs := shareGuides(ms)
	if len(gs) != 3 || gs[0].System != "mac" || gs[1].System != "windows" || gs[2].System != "linux" {
		t.Fatalf("guides = %+v", gs)
	}
	if gs[0].Machines != "mac, old-mac" || !gs[0].Lima || gs[1].Machines != "" || gs[2].Machines != "nas" || !gs[2].NAS {
		t.Fatalf("guides = %+v", gs)
	}
	for _, c := range []struct{ install, goos, want string }{
		{"macos", "darwin", "mac"}, {"macos", "linux", "mac"}, {"", "windows", "windows"}, {"docker", "linux", "linux"}, {"", "linux", "linux"},
	} {
		if got, _, _ := shareSystem(c.install, c.goos); got != c.want {
			t.Errorf("%s/%s = %s, want %s", c.install, c.goos, got, c.want)
		}
	}
}
