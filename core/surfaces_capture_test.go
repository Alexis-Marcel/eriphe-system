package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	testPNG  = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{1}, 64)...)
	testJPEG = append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), bytes.Repeat([]byte{2}, 64)...)
)

// postTaskMedia sends what a node relays of a task's image.
func postTaskMedia(c *core, node, secret, taskID, surfaceID string, data []byte) (int, string) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/node/tasks/{id}/media", c.handleTaskMedia)
	req := httptest.NewRequest("POST", "/v1/node/tasks/"+taskID+"/media?"+url.Values{"surface": {surfaceID}}.Encode(), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Eriphe-Node", node)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// captureTask is a task the brain started to fill the surface id, running
// on the node pc, as a task capturing a page is.
func captureTask(t *testing.T, c *core, id string) string {
	t.Helper()
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	brain := connectServer(t, newMCPServer(c))
	placeholder := []map[string]any{{"id": "root", "component": "Placeholder", "text": "En préparation"}}
	if _, msg := callOn(t, brain, "surface_write", map[string]any{"id": id, "title": "Le site", "components": placeholder}); msg != "" {
		t.Fatal(msg)
	}
	got, msg := callOn(t, brain, "run_task", map[string]any{"prompt": "Montre la page d'accueil de example.org.", "surface": id})
	if msg != "" {
		t.Fatalf("run_task: %s", msg)
	}
	task := got["id"].(string)
	c.store.updateTask(task, map[string]any{"node": "pc", "state": stateRunning})
	return task
}

// A task gives the image of a page it captured for its surface: through
// its node, a PNG or a JPEG, within the limit, for its own surface only.
func TestTaskMediaUpload(t *testing.T) {
	c, _ := surfaceCore(t)
	task := captureTask(t, c, "page-site")

	code, body := postTaskMedia(c, "pc", "node-secret", task, "page-site", testPNG)
	var got struct{ Src, Type string }
	json.Unmarshal([]byte(body), &got)
	if code != 200 || surfaceMediaRef(got.Src) == "" || got.Type != "image/png" {
		t.Fatalf("%d %s", code, body)
	}
	if data, err := os.ReadFile(c.store.surfaceMediaPath(surfaceMediaRef(got.Src))); err != nil || !bytes.Equal(data, testPNG) {
		t.Fatalf("kept %q %v", data, err)
	}
	if code, body := postTaskMedia(c, "pc", "node-secret", task, "page-site", testJPEG); code != 200 || !strings.Contains(body, "image/jpeg") {
		t.Fatalf("jpeg: %d %s", code, body)
	}

	for name, tc := range map[string]struct {
		node, secret, surface string
		data                  []byte
		want                  int
	}{
		"wrong secret":   {"pc", "wrong", "page-site", testPNG, http.StatusUnauthorized},
		"another node":   {"nas", "node-secret", "page-site", testPNG, http.StatusUnauthorized},
		"another surf":   {"pc", "node-secret", "autre", testPNG, http.StatusForbidden},
		"no surface":     {"pc", "node-secret", "", testPNG, http.StatusBadRequest},
		"a gif":          {"pc", "node-secret", "page-site", []byte("GIF89a\x01\x00\x01\x00"), http.StatusUnsupportedMediaType},
		"an svg":         {"pc", "node-secret", "page-site", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), http.StatusUnsupportedMediaType},
		"html":           {"pc", "node-secret", "page-site", []byte("<html><body>x"), http.StatusUnsupportedMediaType},
		"empty":          {"pc", "node-secret", "page-site", nil, http.StatusBadRequest},
		"too large":      {"pc", "node-secret", "page-site", append(testPNG, make([]byte, surfaceMediaMax)...), http.StatusRequestEntityTooLarge},
		"unknown task":   {"pc", "node-secret", "page-site", testPNG, http.StatusNotFound},
		"invalid surfid": {"pc", "node-secret", "../x", testPNG, http.StatusBadRequest},
	} {
		id := task
		if name == "unknown task" {
			id = "t-nope"
		}
		if code, body := postTaskMedia(c, tc.node, tc.secret, id, tc.surface, tc.data); code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, code, body, tc.want)
		}
	}
	if n := len(c.surfaceMediaOf("page-site")); n != 2 {
		t.Fatalf("kept %d images", n)
	}
	// A few per surface.
	for range surfaceMediaPerSurface - 2 {
		postTaskMedia(c, "pc", "node-secret", task, "page-site", testPNG)
	}
	if code, _ := postTaskMedia(c, "pc", "node-secret", task, "page-site", testPNG); code != http.StatusTooManyRequests {
		t.Fatalf("over the count: %d", code)
	}
	// A task over gives nothing more.
	c.store.updateTask(task, map[string]any{"state": stateStopped})
	if code, _ := postTaskMedia(c, "pc", "node-secret", task, "page-site", testPNG); code != http.StatusNotFound {
		t.Fatalf("finished task: %d", code)
	}
}

// A task without the workspace gives no image.
func TestTaskMediaNeedsTheWorkspace(t *testing.T) {
	c, _ := surfaceCore(t)
	c.store.registerNode("pc", hashSecret("node-secret"))
	task := &TaskRow{ID: "t-plain", Prompt: "x", State: stateRunning, Node: "pc", Tools: "ledger"}
	if err := c.store.insertTask(task); err != nil {
		t.Fatal(err)
	}
	if code, body := postTaskMedia(c, "pc", "node-secret", task.ID, "page-site", testPNG); code != http.StatusForbidden {
		t.Fatalf("%d %s", code, body)
	}
}

// The surface shows the image through eriphe-core, signed, even written by
// a task that read the web (tainted), and only on its own surface; the
// page's CSP stays as it was; the image goes with its surface.
func TestTaskMediaShown(t *testing.T) {
	c, _ := surfaceCore(t)
	task := captureTask(t, c, "page-site")
	_, body := postTaskMedia(c, "pc", "node-secret", task, "page-site", testPNG)
	var got struct{ Src string }
	json.Unmarshal([]byte(body), &got)

	comps := json.RawMessage(`[
		{"id":"root","component":"Column","children":["shot","addr"]},
		{"id":"shot","component":"Image","url":"` + got.Src + `","alt":"La page d'accueil"},
		{"id":"addr","component":"Link","label":"Ouvrir le site","url":"https://www.example.org/"}]`)
	s, errs, err := c.surfaceWrite("brain", surfaceIn{ID: "page-site", Title: "Le site", Components: comps, Tainted: []string{"mcp__web__fetch"}})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	view := c.surfaceView(s)
	var src, link string
	for _, comp := range view.components {
		if comp.ID == "shot" {
			json.Unmarshal(comp.Props["url"], &src)
		}
		if comp.ID == "addr" {
			json.Unmarshal(comp.Props["url"], &link)
		}
	}
	if !strings.HasPrefix(src, mediaPath+"?") || strings.Contains(src, "example.org") || link != "https://www.example.org/" {
		t.Fatalf("the image's address %q, the link %q", src, link)
	}
	// Another surface cannot show it, nor anyone's made-up address.
	other, _, _ := c.surfaceWrite("brain", surfaceIn{ID: "autre", Title: "x", Components: json.RawMessage(`[
		{"id":"root","component":"Image","url":"` + got.Src + `"}]`)})
	if raw, _ := json.Marshal(c.surfaceView(other).components); strings.Contains(string(raw), mediaPath) {
		t.Fatalf("shown on another surface: %s", raw)
	}
	if v := c.viewMedia("page-site", "media:med_00000000000000000000000000000000", true); v != "" {
		t.Fatalf("an unknown image: %q", v)
	}

	tw := newTestWebFor(t, c)
	tw.setup()
	code, served, h := tw.do("GET", src, nil, nil)
	if code != 200 || served != string(testPNG) || h.Get("Content-Type") != "image/png" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Fatalf("%d %v", code, h)
	}
	forged := mediaPath + "?" + url.Values{"u": {got.Src}, "s": {"forged"}}.Encode()
	if code, _, _ := tw.do("GET", forged, nil, nil); code != http.StatusForbidden {
		t.Fatalf("forged: %d", code)
	}
	// The page's own CSP is unchanged: images from itself only.
	_, _, h = tw.do("GET", "/espace", nil, nil)
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data: blob:;") || strings.Contains(csp, "http") {
		t.Fatalf("CSP %q", csp)
	}

	// Gone with its surface.
	id := surfaceMediaRef(got.Src)
	if err := c.surfaceRemove("page-site", surfaceByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.store.surfaceMediaPath(id)); !os.IsNotExist(err) {
		t.Fatalf("its file stayed: %v", err)
	}
	if code, _, _ := tw.do("GET", src, nil, nil); code != http.StatusBadGateway {
		t.Fatalf("after removal: %d", code)
	}
}

// An image its surface no longer shows, or whose surface never came, goes
// after an hour; one it shows stays.
func TestTaskMediaSweep(t *testing.T) {
	c, _ := surfaceCore(t)
	task := captureTask(t, c, "page-site")
	put := func() string {
		_, body := postTaskMedia(c, "pc", "node-secret", task, "page-site", testPNG)
		var got struct{ Src string }
		json.Unmarshal([]byte(body), &got)
		return got.Src
	}
	shown, unused := put(), put()
	comps := json.RawMessage(`[{"id":"root","component":"Image","url":"` + shown + `"}]`)
	if _, errs, err := c.surfaceWrite("brain", surfaceIn{ID: "page-site", Title: "Le site", Components: comps}); err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	all, _ := c.surfaces()
	c.sweepSurfaceMedia(all, time.Now())
	if len(c.surfaceMediaOf("page-site")) != 2 {
		t.Fatal("swept too early")
	}
	c.sweepSurfaceMedia(all, time.Now().Add(surfaceMediaUnused+time.Minute))
	left := c.surfaceMediaOf("page-site")
	if len(left) != 1 || surfaceMediaScheme+left[0].ID != shown {
		t.Fatalf("left %+v (shown %s, unused %s)", left, shown, unused)
	}
}
