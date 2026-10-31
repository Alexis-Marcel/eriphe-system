package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	testMAC     = "00:00:5e:00:53:01" // RFC 7042's documentation range
	pcWakeInfo  = `{"runtimes":["claude-code"],"os":"linux","install":"windows","shares":[{"name":"data","writable":true}],"wake":{"macs":["00:00:5e:00:53:01"],"broadcast":"192.0.2.255"}}`
	nasWakeInfo = `{"runtimes":["claude-code"],"always_on":true}`
)

// fakeWakeHere records what eriphe-core sends from its own network.
func fakeWakeHere(t *testing.T) *[]wakeInfo {
	t.Helper()
	var mu sync.Mutex
	var sent []wakeInfo
	old := sendWakeHere
	sendWakeHere = func(w wakeInfo) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, w)
		return nil
	}
	t.Cleanup(func() { sendWakeHere = old })
	return &sent
}

// wakeCore is a core with "pc", off, which reported its card, and "nas",
// the machine that stays on, a fake node on a tunnel that records the
// wake requests it relays.
func wakeCore(t *testing.T) (*core, *Store, func() []wakeInfo) {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{User: "Sam", DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: "/nonexistent"}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p"}}}, store)
	store.registerNode("pc", hashSecret("x"))
	store.setNodeStatus("pc", true, []byte(pcWakeInfo))
	store.setNodeStatus("pc", false, nil)
	store.registerNode("nas", hashSecret("y"))

	var mu sync.Mutex
	var relayed []wakeInfo
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(nasWakeInfo)) })
	api.HandleFunc("POST /v1/wake", func(w http.ResponseWriter, r *http.Request) {
		var in wakeInfo
		json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		relayed = append(relayed, in)
		mu.Unlock()
		writeJSON(w, 200, map[string]any{"sent": []string{"host"}})
	})
	coreEnd, nodeEnd := net.Pipe()
	nodeSession, _ := yamux.Server(nodeEnd, tunnelConfig())
	go http.Serve(nodeSession, api)
	coreSession, _ := yamux.Client(coreEnd, tunnelConfig())
	c.attach("nas", coreSession, false)
	t.Cleanup(func() { coreSession.Close() })
	waitUntil(t, func() bool {
		n, _ := store.nodes()
		for _, row := range n {
			if row.Name == "nas" && row.Online {
				return true
			}
		}
		return false
	})
	return c, store, func() []wakeInfo {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(relayed)
	}
}

func TestMagicPacketCore(t *testing.T) {
	p, err := magicPacket("00-00-5E-00-53-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 102 || !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) || !bytes.Equal(p[96:], []byte{0, 0, 0x5e, 0, 0x53, 1}) {
		t.Fatalf("% x", p)
	}
	if got := wakeTargets("192.0.2.255"); !slices.Equal(got, []string{"255.255.255.255:9", "255.255.255.255:7", "192.0.2.255:9", "192.0.2.255:7"}) {
		t.Fatalf("targets: %v", got)
	}
	if got := wakeTargets("not an address"); len(got) != 2 {
		t.Fatalf("targets: %v", got)
	}
}

// Waking is off by default, for a device the user allowed, and only while it is off.
func TestWake(t *testing.T) {
	c, store, relayed := wakeCore(t)
	here := fakeWakeHere(t)
	ctx := context.Background()
	if _, err := c.wake(ctx, "pc"); !errors.Is(err, errWakeNotAllowed) {
		t.Fatalf("not allowed: %v", err)
	}
	if err := store.setWakeAllowed("ghost", true); !errors.Is(err, errNoNode) {
		t.Fatalf("unknown device: %v", err)
	}
	if err := store.setWakeAllowed("pc", true); err != nil || !store.wakeAllowed("pc") {
		t.Fatalf("allow: %v", err)
	}
	via, err := c.wake(ctx, "pc")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(via)
	if !slices.Equal(via, []string{"eriphe-core", "nas"}) {
		t.Fatalf("sent through %v", via)
	}
	want := wakeInfo{MACs: []string{testMAC}, Broadcast: "192.0.2.255"}
	if len(*here) != 1 || !slices.Equal((*here)[0].MACs, want.MACs) || (*here)[0].Broadcast != want.Broadcast {
		t.Fatalf("from eriphe-core: %+v", *here)
	}
	if r := relayed(); len(r) != 1 || !slices.Equal(r[0].MACs, want.MACs) || r[0].Broadcast != want.Broadcast {
		t.Fatalf("through the NAS: %+v", r)
	}
	// The machine that stays on is never woken, nor a device that is on.
	store.setWakeAllowed("nas", true)
	if _, err := c.wake(ctx, "nas"); !errors.Is(err, errWakeOnline) {
		t.Fatalf("a device that is on: %v", err)
	}
	// A device whose card is unknown (an older eriphe-node).
	store.registerNode("old", hashSecret("z"))
	store.setWakeAllowed("old", true)
	if _, err := c.wake(ctx, "old"); !errors.Is(err, errWakeNoMAC) {
		t.Fatalf("no MAC: %v", err)
	}
	// Cut access: nothing.
	store.setNodeSuspended("pc", true)
	if _, err := c.wake(ctx, "pc"); !errors.Is(err, errNodeSuspended) {
		t.Fatalf("access cut: %v", err)
	}
	store.setNodeSuspended("pc", false)
	// Forgotten, the device's choice goes with it.
	store.deleteNode("pc")
	store.registerNode("pc", hashSecret("x"))
	if store.wakeAllowed("pc") {
		t.Fatal("a forgotten device may still be woken")
	}
}

// The scheduler wakes the device a waiting task needs, once per back-off.
func TestWakeForWaitingTask(t *testing.T) {
	c, store, relayed := wakeCore(t)
	here := fakeWakeHere(t)
	store.addFolder(&Folder{Name: "Clients", Node: "pc", Share: "data", Path: "Clients", Access: folderRead})
	task := &TaskRow{ID: "t-wait", Agent: "small", Runtime: "claude-code", Prompt: "Sort the invoices", State: statePending, Folders: "Clients"}
	store.insertTask(task)

	c.wakeFor(task)
	time.Sleep(50 * time.Millisecond)
	if len(*here) != 0 {
		t.Fatal("woken without the user's leave")
	}
	store.setWakeAllowed("pc", true)
	c.placePending() // the task waits for pc: it is woken
	waitUntil(t, func() bool { return len(relayed()) == 1 })
	c.placePending() // within the back-off: not again
	c.wakeFor(task)
	time.Sleep(100 * time.Millisecond)
	if len(relayed()) != 1 {
		t.Fatalf("woken again within the back-off: %d", len(relayed()))
	}
	// After the back-off, once more.
	store.db.Exec(`UPDATE node_wake SET last_auto = ? WHERE name = 'pc'`, time.Now().Add(-wakeBackoff-time.Minute).Unix())
	c.wakeFor(task)
	waitUntil(t, func() bool { return len(relayed()) == 2 })
	// A task that needs no device of its own wakes nothing.
	store.db.Exec(`UPDATE node_wake SET last_auto = 0 WHERE name = 'pc'`)
	c.wakeFor(&TaskRow{ID: "t-any", Agent: "small"})
	time.Sleep(100 * time.Millisecond)
	if len(relayed()) != 2 {
		t.Fatal("a task without folders woke a device")
	}
}

// Settings → Devices → pc: the switch, "Wake now" and whether it came back.
func TestWebWake(t *testing.T) {
	c, store, relayed := wakeCore(t)
	fakeWakeHere(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()

	_, body, _ := tw.do("GET", "/settings/devices/pc", nil, nil)
	if !strings.Contains(body, `hx-post="/settings/devices/pc/wake/allow"`) || strings.Contains(body, `hx-post="/settings/devices/pc/wake/allow" hx-trigger="change" checked`) {
		t.Fatalf("device page: no switch, or on by default")
	}
	if strings.Contains(body, "Réveiller maintenant") || strings.Contains(body, "Préparer l&#39;appareil") {
		t.Fatal("wake now or the guide while waking is off")
	}
	if _, body, _ = tw.do("GET", "/settings/devices/nas", nil, nil); strings.Contains(body, "/wake/allow") {
		t.Fatal("a switch on the machine that stays on")
	}
	// Waking is refused while off.
	if _, _, h := tw.post("/settings/devices/pc/wake", url.Values{}, csrf); !strings.Contains(h.Get("HX-Trigger"), "Active d") {
		t.Fatalf("wake while not allowed: %v", h)
	}
	if code, _, h := tw.post("/settings/devices/pc/wake/allow", url.Values{"allow": {"1"}}, csrf); code != 200 || h.Get("HX-Refresh") != "true" || !store.wakeAllowed("pc") {
		t.Fatalf("allow: %d %v", code, h)
	}
	_, body, _ = tw.do("GET", "/settings/devices/pc", nil, nil)
	for _, want := range []string{"Réveiller maintenant", "Préparer l&#39;appareil", "Wake on Magic Packet", "Réactiver pour l&#39;accès au réseau", testMAC} {
		if !strings.Contains(body, want) {
			t.Errorf("device page without %q", want)
		}
	}
	since := time.Now().Unix()
	code, body, _ := tw.post("/settings/devices/pc/wake", url.Values{}, csrf)
	if code != 200 || !strings.Contains(body, "En attente de pc") || !strings.Contains(body, `hx-trigger="every 5s"`) || len(relayed()) != 1 {
		t.Fatalf("wake now: %d %s", code, body)
	}
	state := func(since int64) string {
		_, body, _ := tw.do("GET", "/settings/devices/pc/wake?since="+strconv.FormatInt(since, 10), nil, nil)
		return body
	}
	if b := state(since); !strings.Contains(b, "En attente de pc") {
		t.Fatalf("still asleep: %s", b)
	}
	if b := state(time.Now().Add(-wakeWait - time.Second).Unix()); !strings.Contains(b, "pc n&#39;a pas répondu en 2 minutes") || strings.Contains(b, "every 5s") {
		t.Fatalf("not back in time: %s", b)
	}
	store.setNodeStatus("pc", true, []byte(pcWakeInfo))
	if b := state(since); !strings.Contains(b, "pc est de retour") {
		t.Fatalf("back: %s", b)
	}
	// Once on, there is nothing to wake.
	if _, _, h := tw.post("/settings/devices/pc/wake", url.Values{}, csrf); !strings.Contains(h.Get("HX-Trigger"), "Cet appareil est d") {
		t.Fatalf("wake a device that is on: %v", h)
	}
	if code, _, _ := tw.post("/settings/devices/pc/wake/allow", url.Values{}, csrf); code != 200 || store.wakeAllowed("pc") {
		t.Fatal("turning it off")
	}
	// The card says whether Eriphe may wake it.
	if _, body, _ = tw.do("GET", "/settings/devices", nil, nil); !strings.Contains(body, "Réveiller l&#39;appareil") {
		t.Fatal("cards without the fact")
	}
}
