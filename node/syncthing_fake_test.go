package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeSyncthing stands for Syncthing's REST API, the calls the node makes,
// in memory: never the real binary.
type fakeSyncthing struct {
	*httptest.Server
	key string

	mu       sync.Mutex
	options  map[string]any
	devices  map[string]stDevice
	folders  map[string]stFolder
	ignores  map[string][]string
	status   map[string]stFolderStatus
	complete map[string]float64 // folder/device → %
	errs     map[string][]stFileError
	calls    []string
}

const fakeDeviceID = "AAAAAAA-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH"

func newFakeSyncthing(t *testing.T) *fakeSyncthing {
	f := &fakeSyncthing{key: "fake-api-key-0123456789", options: map[string]any{}, devices: map[string]stDevice{},
		folders: map[string]stFolder{}, ignores: map[string][]string{}, status: map[string]stFolderStatus{},
		complete: map[string]float64{}, errs: map[string][]stFileError{}}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /rest/system/status", func(w http.ResponseWriter, r *http.Request) { j(w, map[string]string{"myID": fakeDeviceID}) })
	mux.HandleFunc("PATCH /rest/config/options", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		for k, v := range in {
			f.options[k] = v
		}
		f.mu.Unlock()
	})
	mux.HandleFunc("GET /rest/config/devices", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		out := []stDevice{{DeviceID: fakeDeviceID, Name: "self"}}
		for _, d := range f.devices {
			out = append(out, d)
		}
		f.mu.Unlock()
		j(w, out)
	})
	mux.HandleFunc("PUT /rest/config/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		var d stDevice
		json.NewDecoder(r.Body).Decode(&d)
		if d.DeviceID != r.PathValue("id") {
			http.Error(w, "id", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.devices[d.DeviceID] = d
		f.mu.Unlock()
	})
	mux.HandleFunc("DELETE /rest/config/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		delete(f.devices, r.PathValue("id"))
		f.mu.Unlock()
	})
	mux.HandleFunc("GET /rest/config/folders", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		out := []stFolder{}
		for _, fo := range f.folders {
			out = append(out, fo)
		}
		f.mu.Unlock()
		j(w, out)
	})
	mux.HandleFunc("PUT /rest/config/folders/{id}", func(w http.ResponseWriter, r *http.Request) {
		var fo stFolder
		json.NewDecoder(r.Body).Decode(&fo)
		if fo.ID != r.PathValue("id") {
			http.Error(w, "id", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.folders[fo.ID] = fo
		f.mu.Unlock()
	})
	mux.HandleFunc("DELETE /rest/config/folders/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		delete(f.folders, r.PathValue("id"))
		delete(f.ignores, r.PathValue("id"))
		f.mu.Unlock()
	})
	mux.HandleFunc("POST /rest/db/ignores", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Ignore []string `json:"ignore"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.ignores[r.URL.Query().Get("folder")] = in.Ignore
		f.mu.Unlock()
	})
	// A scan: a folder never scanned is idle afterwards, unless the test
	// set its state.
	mux.HandleFunc("POST /rest/db/scan", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.URL.Query().Get("folder")
		if _, ok := f.folders[id]; !ok {
			http.Error(w, "no such folder", http.StatusNotFound)
			return
		}
		if _, ok := f.status[id]; !ok {
			f.status[id] = stFolderStatus{State: "idle"}
		}
	})
	mux.HandleFunc("GET /rest/db/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		s, ok := f.status[r.URL.Query().Get("folder")]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "no such folder", http.StatusNotFound)
			return
		}
		j(w, s)
	})
	mux.HandleFunc("GET /rest/db/completion", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		c := f.complete[r.URL.Query().Get("folder")+"/"+r.URL.Query().Get("device")]
		f.mu.Unlock()
		j(w, map[string]any{"completion": c, "needBytes": int64(100 - c)})
	})
	mux.HandleFunc("GET /rest/folder/errors", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		e := f.errs[r.URL.Query().Get("folder")]
		f.mu.Unlock()
		j(w, map[string]any{"errors": e})
	})
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != f.key {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

// client is a node's client of the fake.
func (f *fakeSyncthing) client() *syncthing {
	return newSyncthing(f.URL, f.key, "127.0.0.1:22100")
}

// configXML is a config.xml for the fake, as the installers write one.
func (f *fakeSyncthing) configXML() string {
	raw, _ := os.ReadFile(filepath.Join("testdata", "syncthing-config.xml"))
	s := strings.ReplaceAll(string(raw), "@KEY@", f.key)
	return strings.ReplaceAll(s, "127.0.0.1:18384", strings.TrimPrefix(f.URL, "http://"))
}
