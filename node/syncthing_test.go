package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The installers' config.xml gives the node the API's address and key and
// the listening address; one that leaves the loopback is refused.
func TestReadSyncthingConfig(t *testing.T) {
	f := newFakeSyncthing(t)
	home := t.TempDir()
	write := func(s string) {
		os.WriteFile(filepath.Join(home, "config.xml"), []byte(s), 0o600)
	}
	write(f.configXML())
	api, key, listen, err := readSyncthingConfig(home)
	if err != nil || api != f.URL || key != f.key || listen != "127.0.0.1:22100" {
		t.Fatalf("%q %q %q %v", api, key, listen, err)
	}
	for name, change := range map[string][2]string{
		"API on every address": {strings.TrimPrefix(f.URL, "http://"), "0.0.0.0:8384"},
		"API over TLS":         {`tls="false"`, `tls="true"`},
		"no key":               {f.key, ""},
		"listening anywhere":   {"tcp://127.0.0.1:22100", "default"},
		"listening on the LAN": {"tcp://127.0.0.1:22100", "tcp://0.0.0.0:22000"},
		"QUIC":                 {"tcp://127.0.0.1:22100", "quic://127.0.0.1:22100"},
		"two addresses": {"<listenAddress>tcp://127.0.0.1:22100</listenAddress>",
			"<listenAddress>tcp://127.0.0.1:22100</listenAddress><listenAddress>tcp://0.0.0.0:22000</listenAddress>"},
	} {
		write(strings.Replace(f.configXML(), change[0], change[1], 1))
		if _, _, _, err := readSyncthingConfig(home); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, _, err := readSyncthingConfig(t.TempDir()); err == nil {
		t.Error("no config.xml: accepted")
	}
}

// At start the node turns off, again, all that would let Syncthing reach
// anyone by itself, learns its device ID and gives its listening address to
// the way between machines; GET /v1/node then tells eriphe-core the ID.
func TestStartSyncthing(t *testing.T) {
	f := newFakeSyncthing(t)
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.xml"), []byte(f.configXML()), 0o600)
	n := newNode(options{root: t.TempDir()})
	n.syncNet = newSyncNet(n.opt.root, "", 0, nil)
	if n.syncthingInfo(context.Background()) != nil {
		t.Fatal("a device ID before Syncthing answered")
	}
	done := make(chan struct{})
	go func() { n.startSyncthing(home); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("startSyncthing did not end")
	}
	if info := n.syncthingInfo(context.Background()); info == nil || info.DeviceID != fakeDeviceID {
		t.Fatalf("info %+v", info)
	}
	if n.syncNet.local != "127.0.0.1:22100" {
		t.Fatalf("local %q", n.syncNet.local)
	}
	want := map[string]any{"globalAnnounceEnabled": false, "localAnnounceEnabled": false, "relaysEnabled": false,
		"natEnabled": false, "announceLANAddresses": false, "urAccepted": float64(-1), "crashReportingEnabled": false,
		"autoUpgradeIntervalH": float64(0), "startBrowser": false}
	for k, v := range want {
		if f.options[k] != v {
			t.Errorf("option %s = %v, want %v", k, f.options[k], v)
		}
	}
	if l, _ := f.options["listenAddresses"].([]any); len(l) != 1 || l[0] != "tcp://127.0.0.1:22100" {
		t.Errorf("listenAddresses %v", f.options["listenAddresses"])
	}
}

// Every call carries the API key; without it Syncthing refuses.
func TestSyncthingKey(t *testing.T) {
	f := newFakeSyncthing(t)
	ctx := context.Background()
	if id, err := f.client().deviceID(ctx); err != nil || id != fakeDeviceID {
		t.Fatalf("%q %v", id, err)
	}
	wrong := newSyncthing(f.URL, "another-key-0123456789", "127.0.0.1:22100")
	if _, err := wrong.deviceID(ctx); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestValidDeviceID(t *testing.T) {
	for id, ok := range map[string]bool{
		fakeDeviceID: true,
		"MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYY-LTMRWAD": true,
		"": false,
		"aaaaaaa-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH": false,
		"AAAAAAA-BBBBBBB": false,
		"AAAAAA1-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH": false,
	} {
		if validDeviceID(id) != ok {
			t.Errorf("%q: %v", id, !ok)
		}
	}
}
