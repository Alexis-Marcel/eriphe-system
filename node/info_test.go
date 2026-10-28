package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The node tells eriphe-core its version, and how it was installed, with
// what it offers, Docker or not.
func TestNodeReportsVersion(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "sha-1234567"
	n := newNode(options{docker: filepath.Join(t.TempDir(), "no-docker"), install: "macos"})
	rec := httptest.NewRecorder()
	n.handleNode(rec, httptest.NewRequest(http.MethodGet, "/v1/node", nil))
	var info struct {
		Version  string `json:"version"`
		Install  string `json:"install"`
		Services bool   `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "sha-1234567" || info.Install != "macos" || info.Services {
		t.Fatalf("GET /v1/node: %s", rec.Body)
	}
}
