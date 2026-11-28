package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node's media are its own files: a task's images, or a file the user
// attached to a message that the brain kept with the node (node_set, a
// property whose value is the attachment's id: node_attach.go). The page
// gets each as an address of eriphe-core's own, signed for that node,
//
//	GET /espace/media?u=media:<node>/<name>&s=<signature>
//
// with a session: handleMedia reads it from that node's files only, an
// image (not SVG, which carries scripts) shown inline, a PDF downloaded:
// the page fetches a PDF's bytes and draws it itself (PdfView.tsx, with a
// bundled pdf.js), so a PDF never runs in the workspace's origin.
// Nothing is ever fetched from outside.
//
// A task's images. Most sites refuse being framed:
// to show a page, a task with a node captures it in its container
// (eriphe-capture, worker/base) and posts the image to its device, which
// relays it here with the device's secret and the task's id:
//
//	POST /v1/device/tasks/{id}/media?node=<id>   (device secret, the image as body)
//
// for the task's own node only. A PNG or a JPEG, sniffed from its bytes,
// 5 MiB at most, a few a node: it is kept in files/<id>/ of the graph's
// repository, one commit (nodestore.PutFile), and its address answered,
// media:<name>, which the task names as a property of its node.

const (
	mediaScheme = "media:"
	mediaPath   = "/espace/media"
)

// mediaKey signs the media addresses: made once a run, so an address
// lasts as long as eriphe-core.
var mediaKey = sync.OnceValue(func() []byte {
	k := make([]byte, 32)
	rand.Read(k)
	return k
})

func (c *core) mediaSign(u string) string {
	m := hmac.New(sha256.New, mediaKey())
	m.Write([]byte(u))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// nodeFileSrc is the address on eriphe-core of node id's own file a
// value names (media:<name>), "" for a value that names none.
func (c *core) nodeFileSrc(id, v string) string {
	name, ok := strings.CutPrefix(strings.TrimSpace(v), mediaScheme)
	if !ok || id == "" || !nodestore.ValidFileName(name) {
		return ""
	}
	at := mediaScheme + id + "/" + name
	return mediaPath + "?" + url.Values{"u": {at}, "s": {c.mediaSign(at)}}.Encode()
}

// mediaIsImage tells a media:<name> of an image from a file's (a PDF).
func mediaIsImage(v string) bool { return !strings.HasSuffix(strings.TrimSpace(v), ".pdf") }

// handleMedia is GET /espace/media: one of a node's files. 403 for an
// address eriphe-core did not sign, 404 for a node's file that is no
// more.
func (w *web) handleMedia(rw http.ResponseWriter, r *http.Request, _ *session) {
	raw, sig := r.URL.Query().Get("u"), r.URL.Query().Get("s")
	if raw == "" || !hmac.Equal([]byte(sig), []byte(w.c.mediaSign(raw))) {
		http.Error(rw, "not an address of a node's file", http.StatusForbidden)
		return
	}
	at, _ := strings.CutPrefix(raw, mediaScheme)
	body, kind, err := w.c.nodeMedia(at)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	}
	h := rw.Header()
	h.Set("Content-Type", kind)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "private, max-age=600")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if strings.HasPrefix(kind, "image/") {
		h.Set("Content-Disposition", "inline")
	} else {
		h.Set("Content-Disposition", "attachment") // a node's PDF: downloaded, never run in the page's origin
	}
	rw.Write(body)
}

// nodeMedia is one of a node's files, from <node>/<name>, with its type.
func (c *core) nodeMedia(at string) ([]byte, string, error) {
	id, name, _ := strings.Cut(at, "/")
	if c.nodes == nil {
		return nil, "", errors.New("no such media")
	}
	body, err := c.nodes.File(id, name)
	if err != nil {
		return nil, "", errors.New("no such media")
	}
	kind, _, ok := nodeFileType(body)
	if !ok {
		return nil, "", errors.New("no such media")
	}
	return body, kind, nil
}

// nodeFileTypes are the types a node's file may be, sniffed from its
// bytes, and the extension each is kept under: never SVG nor HTML, which
// carry scripts.
var nodeFileTypes = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "application/pdf": "pdf"}

// nodeFileType tells a node's file's type and extension from its bytes.
func nodeFileType(data []byte) (kind, ext string, ok bool) {
	kind = http.DetectContentType(data)
	ext, ok = nodeFileTypes[kind]
	return kind, ext, ok
}

// imageType tells a PNG or a JPEG from its bytes; anything else is
// refused, whatever its sender called it.
func imageType(data []byte) (string, bool) {
	switch t := http.DetectContentType(data); t {
	case "image/png", "image/jpeg":
		return t, true
	}
	return "", false
}

// handleTaskMedia is POST /v1/device/tasks/{id}/media, for devices: an
// image a task gives its own node. It answers the image's address,
// media:<name>, for an Image of a block of that node.
func (c *core) handleTaskMedia(w http.ResponseWriter, r *http.Request) {
	device, ok := c.deviceFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t, err := c.store.task(r.PathValue("id"))
	if err != nil || t.Device != device || finished(t.State) {
		http.Error(w, "no such task on this device", http.StatusNotFound)
		return
	}
	node := c.taskNode(t.ID)
	if node == "" || c.nodes == nil {
		http.Error(w, "this task has no node to keep an image in", http.StatusForbidden)
		return
	}
	if asked := strings.TrimSpace(r.URL.Query().Get("node")); asked != node {
		http.Error(w, "an image goes to the task's own node, "+node+", only", http.StatusForbidden)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, nodestore.MaxFile+1))
	if err != nil {
		http.Error(w, "the image could not be read", http.StatusBadRequest)
		return
	}
	if len(data) > nodestore.MaxFile {
		http.Error(w, "too large: "+humanSize(nodestore.MaxFile)+" at most", http.StatusRequestEntityTooLarge)
		return
	}
	kind, ok := imageType(data)
	if !ok {
		http.Error(w, "a PNG or a JPEG only", http.StatusUnsupportedMediaType)
		return
	}
	ext := "png"
	if kind == "image/jpeg" {
		ext = "jpg"
	}
	name, err := c.nodes.PutFile(node, c.taskAuthor(t.ID), ext, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	log.Printf("node %s: image %s (%s) from task %s", node, name, humanSize(int64(len(data))), t.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"src": mediaScheme + name, "type": kind, "size": len(data),
		"note": "Kept with your node as src; a page does not show an image yet: link the page's address in your node.",
	})
}
