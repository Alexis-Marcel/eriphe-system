package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The images and videos of a node's blocks never reach the page from
// outside (A2UI #4045: an address the page loads can carry what a model
// read out to whoever answers it, and it tells the sender the user
// looked). The CSP already forbids it (img-src 'self'); eriphe-core
// rewrites each media address of a block (blockView, web_graph.go) to its
// own endpoint,
//
//	GET /espace/media?u=<address>&s=<signature>
//
// with a session, which fetches it itself: only an address eriphe-core
// signed (not an open fetcher), http or https only, a public address only
// (the same dialer as an access: never its own network, the cloud's
// metadata or the LAN), 3 redirects at most, no cookie nor credential,
// 8 MiB at most, an image (not SVG, which carries scripts) or a video, kept
// 10 minutes by the browser.
//
// A node's media are its own files: a task's images, or a file the user
// attached to a message that the brain kept with the node (node_set, a
// property whose value is the attachment's id: node_attach.go).
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
// media:<name>, which the task writes as an Image's url in a block of its
// node. The bytes never go through a model. On the page, blockView signs
// media:<node>/<name> for the node the block is in, and handleMedia reads
// it from that node's files only.

const (
	mediaScheme    = "media:"
	mediaMax       = 8 << 20
	mediaTimeout   = 20 * time.Second
	mediaRedirects = 3
	mediaPath      = "/espace/media"
)

// mediaClient fetches the media; tests replace it.
var mediaClient = &http.Client{
	Timeout: mediaTimeout,
	Transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, Control: accessControl}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: mediaTimeout,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       time.Minute,
	},
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= mediaRedirects {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
			return errors.New("not http")
		}
		return nil
	},
}

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

// mediaOrDrop is a media address of node id's block as the page gets it:
// eriphe-core's own for one of the node's files (media:<name>) or an
// http(s) address allowed, "" for anything else.
func (c *core) mediaOrDrop(id, raw string, allowed bool) string {
	raw = strings.TrimSpace(raw)
	if name, ok := strings.CutPrefix(raw, mediaScheme); ok {
		if id == "" || !nodestore.ValidFileName(name) {
			return ""
		}
		at := mediaScheme + id + "/" + name
		return mediaPath + "?" + url.Values{"u": {at}, "s": {c.mediaSign(at)}}.Encode()
	}
	u, err := url.Parse(raw)
	if !allowed || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 4096 {
		return ""
	}
	return mediaPath + "?" + url.Values{"u": {raw}, "s": {c.mediaSign(raw)}}.Encode()
}

// nodeFileSrc is the address on eriphe-core of node id's own file a
// value names (media:<name>), "" for a value that names none.
func (c *core) nodeFileSrc(id, v string) string {
	if name, ok := strings.CutPrefix(strings.TrimSpace(v), mediaScheme); !ok || !nodestore.ValidFileName(name) {
		return ""
	}
	return c.mediaOrDrop(id, v, false)
}

// mediaIsImage tells a media:<name> of an image from a file's (a PDF).
func mediaIsImage(v string) bool { return !strings.HasSuffix(strings.TrimSpace(v), ".pdf") }

// handleMedia is GET /espace/media: a block's image or video, fetched by
// eriphe-core, or read from its node's files. 403 for an address it did
// not sign, 502 for one that is not a media it serves, 404 for a node's
// file that is no more.
func (w *web) handleMedia(rw http.ResponseWriter, r *http.Request, _ *session) {
	raw, sig := r.URL.Query().Get("u"), r.URL.Query().Get("s")
	if raw == "" || !hmac.Equal([]byte(sig), []byte(w.c.mediaSign(raw))) {
		http.Error(rw, "not an address of a block", http.StatusForbidden)
		return
	}
	var body []byte
	var kind string
	var err error
	if at, ok := strings.CutPrefix(raw, mediaScheme); ok {
		body, kind, err = w.c.nodeMedia(at)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusNotFound)
			return
		}
	} else if body, kind, err = fetchMedia(r, raw); err != nil {
		http.Error(rw, err.Error(), http.StatusBadGateway)
		return
	}
	h := rw.Header()
	h.Set("Content-Type", kind)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "private, max-age=600")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if strings.HasPrefix(kind, "image/") || strings.HasPrefix(kind, "video/") {
		h.Set("Content-Disposition", "inline")
	} else {
		h.Set("Content-Disposition", "attachment") // a node's PDF: downloaded, never run in the page's origin
	}
	rw.Write(body)
}

// fetchMedia fetches an image or a video, within the limits.
func fetchMedia(r *http.Request, raw string) ([]byte, string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", errors.New("not an http address")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "eriphe-core")
	req.Header.Set("Accept", "image/*, video/*")
	res, err := mediaClient.Do(req)
	if err != nil {
		return nil, "", errors.New("the media could not be fetched")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", errors.New("the media's server answered " + strconv.Itoa(res.StatusCode))
	}
	kind, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || !(strings.HasPrefix(kind, "image/") || strings.HasPrefix(kind, "video/")) || kind == "image/svg+xml" {
		return nil, "", errors.New("not an image or a video")
	}
	if res.ContentLength > mediaMax {
		return nil, "", errors.New("too large")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, mediaMax+1))
	if err != nil {
		return nil, "", errors.New("the media could not be read")
	}
	if len(body) > mediaMax {
		return nil, "", errors.New("too large")
	}
	return body, kind, nil
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
		"note": "Write src as the url of an Image in a block of your node (only there), with a Link to the page's address beside it.",
	})
}
