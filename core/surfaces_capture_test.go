package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

var (
	testPNG  = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{1}, 64)...)
	testJPEG = append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), bytes.Repeat([]byte{2}, 64)...)
)

// postTaskMedia sends what a device relays of a task's image.
func postTaskMedia(c *core, device, secret, taskID, surfaceID string, data []byte) (int, string) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/device/tasks/{id}/media", c.handleTaskMedia)
	req := httptest.NewRequest("POST", "/v1/device/tasks/"+taskID+"/media?"+url.Values{"surface": {surfaceID}}.Encode(), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Eriphe-Device", device)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// A task without the workspace gives no image.
func TestTaskMediaNeedsTheWorkspace(t *testing.T) {
	c, _ := surfaceCore(t)
	c.store.registerDevice("pc", hashSecret("device-secret"))
	task := &TaskRow{ID: "t-plain", Prompt: "x", State: stateRunning, Device: "pc", Tools: "ledger"}
	if err := c.store.insertTask(task); err != nil {
		t.Fatal(err)
	}
	if code, body := postTaskMedia(c, "pc", "device-secret", task.ID, "thread-1", testPNG); code != http.StatusForbidden {
		t.Fatalf("%d %s", code, body)
	}
}

// A task lent the workspace gives no image either: a thread has no
// surface to show it any more.
func TestTaskMediaRefused(t *testing.T) {
	c, _ := surfaceCore(t)
	c.store.registerDevice("pc", hashSecret("device-secret"))
	task := &TaskRow{ID: "t-shot", Prompt: "x", State: stateRunning, Device: "pc", Tools: workspaceServer}
	if err := c.store.insertTask(task); err != nil {
		t.Fatal(err)
	}
	if code, body := postTaskMedia(c, "pc", "device-secret", task.ID, "thread-1", testPNG); code != http.StatusGone && code != http.StatusForbidden {
		t.Fatalf("%d %s", code, body)
	}
	if len(c.surfaceMediaOf("thread-1")) != 0 {
		t.Fatal("an image kept")
	}
}
