package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// fakeDrive is an in-process Google Drive (the few calls of google.go) and
// its token endpoint.
type fakeDrive struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	files     map[string]*fakeFile
	next      int
	tokens    map[string]bool // valid access tokens
	refreshes int
	refreshed []string // refresh tokens presented
	expiresIn int
	revoked   bool
	writes    []string // every write call: method and path
}

type fakeFile struct {
	driveFile
	content  map[string]string // by mime type: the media, or an export
	uploaded string            // the last new version: its type and content
	shared   bool              // in Shared with me
}

const fakeRefresh = "refresh-1"

func newFakeDrive(t *testing.T) *fakeDrive {
	d := &fakeDrive{t: t, files: map[string]*fakeFile{}, tokens: map[string]bool{}, expiresIn: 3600}
	d.srv = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDrive) add(id, name, mimeType, parent string, content map[string]string) *fakeFile {
	f := &fakeFile{driveFile: driveFile{ID: id, Name: name, MimeType: mimeType}, content: content}
	if parent != "" {
		f.Parents = []string{parent}
	}
	for _, c := range content {
		f.Size = fmt.Sprint(len(c))
	}
	d.files[id] = f
	return f
}

func (d *fakeDrive) account(scopes ...string) AccountConfig {
	return AccountConfig{Name: "drive", Provider: "google", Auth: AuthConfig{TokenEnv: "FILES_TEST_TOKEN"}, Scopes: scopes,
		APIURL: d.srv.URL + "/drive/v3", UploadURL: d.srv.URL + "/upload/drive/v3", TokenURL: d.srv.URL + "/token"}
}

func (d *fakeDrive) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.URL.Path == "/token" {
		d.token(w, r)
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !d.tokens[tok] {
		http.Error(w, `{"error":{"message":"invalid credentials"}}`, http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		d.writes = append(d.writes, r.Method+" "+r.URL.Path)
	}
	q := r.URL.Query()
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/drive/v3/files":
		d.list(w, q.Get("q"))
	case r.Method == http.MethodGet && path == "/drive/v3/drives":
		json.NewEncoder(w).Encode(map[string]any{"drives": []map[string]string{{"id": "team", "name": "Team"}}})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/export"):
		f := d.file(strings.TrimSuffix(strings.TrimPrefix(path, "/drive/v3/files/"), "/export"))
		c, ok := "", f != nil
		if ok {
			c, ok = f.content[q.Get("mimeType")]
		}
		if !ok {
			http.Error(w, `{"error":{"message":"export not supported"}}`, http.StatusBadRequest)
			return
		}
		io.WriteString(w, c)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/drive/v3/files/"):
		f := d.file(strings.TrimPrefix(path, "/drive/v3/files/"))
		if f == nil {
			http.Error(w, `{"error":{"message":"File not found"}}`, http.StatusNotFound)
			return
		}
		if q.Get("alt") == "media" {
			io.WriteString(w, f.content[f.MimeType])
			return
		}
		json.NewEncoder(w).Encode(f.driveFile)
	case r.Method == http.MethodPost && path == "/upload/drive/v3/files" && q.Get("uploadType") == "multipart":
		d.upload(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/upload/drive/v3/files/") && q.Get("uploadType") == "media":
		f := d.file(strings.TrimPrefix(path, "/upload/drive/v3/files/"))
		body, _ := io.ReadAll(r.Body)
		f.uploaded = r.Header.Get("Content-Type") + " " + string(body)
		json.NewEncoder(w).Encode(f.driveFile)
	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/drive/v3/files/"):
		f := d.file(strings.TrimPrefix(path, "/drive/v3/files/"))
		var meta struct {
			Trashed bool `json:"trashed"`
		}
		json.NewDecoder(r.Body).Decode(&meta)
		f.Trashed = meta.Trashed
		json.NewEncoder(w).Encode(map[string]string{"id": f.ID})
	default:
		d.t.Errorf("fake drive: unexpected %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusBadRequest)
	}
}

func (d *fakeDrive) file(id string) *fakeFile {
	if id == "root" {
		id = "root-id"
	}
	return d.files[id]
}

func (d *fakeDrive) token(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != "client-1" || r.Form.Get("client_secret") != "secret-1" {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	d.refreshed = append(d.refreshed, r.Form.Get("refresh_token"))
	w.Header().Set("Content-Type", "application/json")
	if d.revoked || r.Form.Get("refresh_token") != fakeRefresh {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
		return
	}
	d.refreshes++
	at := fmt.Sprintf("access-%d", d.refreshes)
	d.tokens[at] = true
	json.NewEncoder(w).Encode(map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": d.expiresIn})
}

var (
	inParents = regexp.MustCompile(`^'([^']*)' in parents and trashed = false( and mimeType = '([^']*)')?$`)
	contains  = regexp.MustCompile(`^\(name contains '((?:[^'\\]|\\.)*)' or fullText contains '(?:[^'\\]|\\.)*'\) and trashed = false$`)
)

func (d *fakeDrive) list(w http.ResponseWriter, q string) {
	var out []driveFile
	switch m := inParents.FindStringSubmatch(q); {
	case q == "sharedWithMe and trashed = false":
		for _, f := range d.files {
			if f.shared && !f.Trashed {
				out = append(out, f.driveFile)
			}
		}
	case m != nil:
		for _, f := range d.files {
			if len(f.Parents) > 0 && f.Parents[0] == m[1] && (m[3] == "" || f.MimeType == m[3]) && !f.Trashed {
				out = append(out, f.driveFile)
			}
		}
	case contains.MatchString(q):
		text := strings.ReplaceAll(contains.FindStringSubmatch(q)[1], `\'`, `'`)
		for _, f := range d.files {
			hit := strings.Contains(f.Name, text)
			for _, c := range f.content {
				hit = hit || strings.Contains(c, text)
			}
			if hit && !f.Trashed {
				out = append(out, f.driveFile)
			}
		}
	default:
		d.t.Errorf("fake drive: query %q", q)
	}
	json.NewEncoder(w).Encode(map[string]any{"files": out})
}

func (d *fakeDrive) upload(w http.ResponseWriter, r *http.Request) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		d.t.Errorf("upload: %v", err)
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	var meta struct {
		driveFile
	}
	part, _ := mr.NextPart()
	json.NewDecoder(part).Decode(&meta)
	part, _ = mr.NextPart()
	body, _ := io.ReadAll(part)
	mimeType := part.Header.Get("Content-Type")
	stored := mimeType
	if meta.MimeType != "" {
		stored = meta.MimeType // converted
	}
	d.next++
	f := d.add(fmt.Sprintf("new-%d", d.next), meta.Name, stored, meta.Parents[0], map[string]string{mimeType: string(body)})
	json.NewEncoder(w).Encode(f.driveFile)
}

// tree is a small Drive:
//
//	My Drive
//	├── Compta (given)
//	│   ├── facture.txt
//	│   ├── Notes (a Google Doc)
//	│   ├── Budget (a Google Sheet)
//	│   ├── sub/deep.txt
//	│   └── link (a shortcut to Perso/secret.txt)
//	├── Perso (not given)
//	│   └── secret.txt
//	└── Engineering (given in the nested tests)
//	    ├── plan.txt
//	    ├── link-api (a shortcut to api.txt)
//	    └── teams
//	        └── Backend (given in the nested tests, inside Engineering)
//	            ├── api.txt
//	            └── srv/main.txt
//
// and shared.txt, shared with the user from a folder they cannot see.
func (d *fakeDrive) tree() {
	d.add("root-id", "My Drive", mimeFolder, "", nil)
	d.add("compta", "Compta", mimeFolder, "root-id", nil)
	d.add("facture", "facture.txt", "text/plain", "compta", map[string]string{"text/plain": "facture 42, budget"})
	d.add("notes", "Notes", mimeDoc, "compta", map[string]string{"text/markdown": "# Notes\n\nbudget 2026", "text/plain": "Notes budget 2026", "application/pdf": "%PDF-1.7 notes"})
	d.add("sheet", "Budget", mimeSheet, "compta", map[string]string{"text/csv": "a,b\n1,2"})
	d.add("sub", "sub", mimeFolder, "compta", nil)
	d.add("deep", "deep.txt", "text/plain", "sub", map[string]string{"text/plain": "deep budget"})
	d.add("perso", "Perso", mimeFolder, "root-id", nil)
	d.add("secret", "secret.txt", "text/plain", "perso", map[string]string{"text/plain": "secret budget"})
	link := d.add("link", "link", mimeShortcut, "compta", nil)
	link.ShortcutDetails = &struct {
		TargetID string `json:"targetId"`
	}{"secret"}

	d.add("eng", "Engineering", mimeFolder, "root-id", nil)
	d.add("plan", "plan.txt", "text/plain", "eng", map[string]string{"text/plain": "roadmap plan"})
	d.add("teams", "teams", mimeFolder, "eng", nil)
	d.add("backend", "Backend", mimeFolder, "teams", nil)
	d.add("api", "api.txt", "text/plain", "backend", map[string]string{"text/plain": "roadmap api"})
	d.add("srv", "srv", mimeFolder, "backend", nil)
	d.add("main", "main.txt", "text/plain", "srv", map[string]string{"text/plain": "roadmap main"})
	short := d.add("link-api", "link-api", mimeShortcut, "eng", nil)
	short.ShortcutDetails = &struct {
		TargetID string `json:"targetId"`
	}{"api"}
	d.add("shared", "shared.txt", "text/plain", "hidden-folder", map[string]string{"text/plain": "roadmap shared"}).shared = true
}
