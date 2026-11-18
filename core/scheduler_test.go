package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScheduleFilter(t *testing.T) {
	claude := json.RawMessage(`{"runtimes":["claude-code"]}`)
	gpu := json.RawMessage(`{"runtimes":["claude-code"],"labels":["gpu"]}`)
	none := json.RawMessage(`{"runtimes":[]}`)
	devices := []DeviceRow{
		{Name: "nas", Online: true, Info: claude},
		{Name: "pc", Online: true, Info: gpu},
		{Name: "mac", Online: false, Info: claude},
		{Name: "pi", Online: true, Info: none},
		{Name: "tv", Online: false, Info: json.RawMessage(`{"runtimes":["claude-code"],"labels":["tpu"]}`)},
	}
	load := map[string]int{"nas": 0, "pc": 2}
	active := func(n string) (int, error) { return load[n], nil }

	cases := []struct {
		want, runtime, got string
		needs              []string
		fails, waits       bool
	}{
		{"", "claude-code", "nas", nil, false, false},            // least loaded online device
		{"", "claude-code", "pc", []string{"gpu"}, false, false}, // the only one with a GPU
		{"nas", "claude-code", "nas", nil, false, false},
		{"mac", "claude-code", "", nil, true, false},             // offline
		{"pi", "claude-code", "", nil, true, false},              // no such runtime
		{"box", "claude-code", "", nil, true, false},             // unknown
		{"", "opencode", "", nil, true, false},                   // nobody offers it
		{"nas", "claude-code", "", []string{"gpu"}, true, false}, // lacks the label
		{"", "claude-code", "", []string{"tpu"}, true, true},     // only an offline device has it
		{"", "claude-code", "", []string{"gpu", "tpu"}, true, false},
	}
	for _, c := range cases {
		got, err := schedule(devices, c.want, placement{Runtime: c.runtime, Needs: c.needs}, active)
		var waiting *waitingError
		if (err != nil) != c.fails || got != c.got || errors.As(err, &waiting) != c.waits {
			t.Errorf("schedule(%q, %q, %v) = %q, %v", c.want, c.runtime, c.needs, got, err)
		}
	}
}

func TestScheduleScore(t *testing.T) {
	info := func(s string) json.RawMessage { return json.RawMessage(s) }
	devices := []DeviceRow{
		{Name: "nas", Online: true, Info: info(`{"runtimes":["claude-code"],"cpus":4,"memory_bytes":8589934592,
			"image":"worker:1","images":["worker:1"]}`)},
		{Name: "pc", Online: true, Info: info(`{"runtimes":["claude-code"],"cpus":20,"memory_bytes":34359738368,
			"labels":["fast"],"image":"worker:1"}`)},
	}
	load := map[string]int{"pc": 1}
	active := func(n string) (int, error) { return load[n], nil }
	cases := []struct {
		p    placement
		want string
	}{
		{placement{Prefers: []string{"cpu"}}, "pc"}, // 20 CPUs beat 4, even with a task
		{placement{Prefers: []string{"memory"}}, "pc"},
		{placement{Prefers: []string{"fast"}}, "pc"},
		{placement{}, "nas"}, // idle, and has the image
		{placement{Image: "other"}, "nas"},
	}
	for _, c := range cases {
		c.p.Runtime = "claude-code"
		if got, err := schedule(devices, "", c.p, active); err != nil || got != c.want {
			t.Errorf("schedule(%+v) = %q, %v; want %q", c.p, got, err, c.want)
		}
	}

	// Equal devices: the image breaks the tie, then the name does.
	load = map[string]int{}
	devices[1].Info = info(`{"runtimes":["claude-code"],"cpus":4,"images":["ghcr.io/x/worker:latest"]}`)
	devices[0].Info = info(`{"runtimes":["claude-code"],"cpus":4}`)
	if got, _ := schedule(devices, "", placement{Runtime: "claude-code", Image: "ghcr.io/x/worker"}, active); got != "pc" {
		t.Errorf("image tie-break = %q", got)
	}
	if got, _ := schedule(devices, "", placement{Runtime: "claude-code"}, active); got != "nas" {
		t.Errorf("name tie-break = %q", got)
	}
	// A requested device is taken as is, even if another scores better.
	if got, _ := schedule(devices, "nas", placement{Runtime: "claude-code", Image: "ghcr.io/x/worker"}, active); got != "nas" {
		t.Errorf("requested device = %q", got)
	}
}

// A task no online device can run waits without a device, out of every device's
// assignments, and is placed when a fitting device comes online.
func TestPendingTaskWaitsForDevice(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers: map[string]Provider{"anthropic": {Kind: "claude-subscription", Secret: secret}},
		Agents: map[string]Agent{
			"sonnet": {Runtime: "claude-code", Provider: "anthropic", Needs: []string{"gpu"}},
			"tpu":    {Runtime: "claude-code", Provider: "anthropic", Needs: []string{"tpu"}},
		},
		DefaultAgent: "sonnet",
	}
	c := newCore(cfg, store)
	gpu := []byte(`{"runtimes":["claude-code"],"labels":["gpu"]}`)
	store.registerDevice("nas", hashSecret("a"))
	store.setDeviceStatus("nas", true, []byte(`{"runtimes":["claude-code"]}`))
	store.registerDevice("pc", hashSecret("b"))
	store.setDeviceStatus("pc", true, gpu) // known from an earlier visit...
	store.setDeviceStatus("pc", false, nil)

	ctx := context.Background()
	task, err := c.runTask(ctx, runRequest{Prompt: "train"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Device != "" || task.State != statePending || task.PendingReason == "" {
		t.Fatalf("task = %+v", task)
	}
	stopped, _ := c.runTask(ctx, runRequest{Prompt: "never mind"})
	if err := c.stop(ctx, stopped.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.task(stopped.ID); got.State != stateStopped {
		t.Fatalf("stopped task = %+v", got)
	}
	if _, err := c.runTask(ctx, runRequest{Prompt: "x", Agent: "tpu"}); err == nil {
		t.Fatal("no device could ever run it: want an error")
	}
	for _, n := range []string{"nas", "pc", ""} {
		if a, _ := store.assignments(n); len(a) != 0 {
			t.Fatalf("assignments(%q) = %+v", n, a)
		}
	}

	c.placePending() // nothing changed: still waiting
	if got, _ := store.task(task.ID); got.Device != "" {
		t.Fatalf("placed too early: %+v", got)
	}
	rev := c.rev
	store.setDeviceStatus("pc", true, gpu) // ...and back
	c.placePending()
	got, _ := store.task(task.ID)
	if got.Device != "pc" || got.PendingReason != "" || got.State != statePending {
		t.Fatalf("task = %+v", got)
	}
	if c.rev == rev {
		t.Fatal("the device's watch was not woken")
	}
	a, _ := store.assignments("pc")
	if len(a) != 1 || a[0].ID != task.ID {
		t.Fatalf("assignments = %+v", a)
	}
	if got, _ := store.task(stopped.ID); got.Device != "" {
		t.Fatalf("a stopped task was placed: %+v", got)
	}
}

// A service whose image runs on one architecture goes to a device of it.
func TestScheduleArch(t *testing.T) {
	devices := []DeviceRow{
		{Name: "mac", Online: true, Info: json.RawMessage(`{"services":true,"arch":"arm64"}`)},
		{Name: "nas", Online: true, Info: json.RawMessage(`{"services":true,"arch":"amd64"}`)},
	}
	active := func(string) (int, error) { return 0, nil }
	if got, err := schedule(devices, "", placement{Service: true, Arch: "amd64"}, active); err != nil || got != "nas" {
		t.Fatalf("amd64: %q %v", got, err)
	}
	if got, err := schedule(devices, "mac", placement{Service: true, Arch: "amd64"}, active); err == nil {
		t.Fatalf("pinned to an arm64 device: %q", got)
	}
	if _, err := (RunSpec{Image: "x", Transport: transportStdio, Arch: "riscv"}).normalize(); err == nil {
		t.Fatal("an unknown arch")
	}
}
