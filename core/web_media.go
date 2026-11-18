package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
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
// A task's own images (a device's capture) are not taken: the upload is
// refused (handleTaskMedia) until a node keeps its media (files/<id>/ of
// the graph's repository).

const (
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

// mediaOrDrop is a media address as the page gets it: eriphe-core's own
// for an http(s) address allowed, "" for anything else.
func (c *core) mediaOrDrop(raw string, allowed bool) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if !allowed || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 4096 {
		return ""
	}
	return mediaPath + "?" + url.Values{"u": {raw}, "s": {c.mediaSign(raw)}}.Encode()
}

// handleMedia is GET /espace/media: a block's image or video, fetched by
// eriphe-core. 403 for an address it did not sign, 502 for one that is not
// a media it serves.
func (w *web) handleMedia(rw http.ResponseWriter, r *http.Request, _ *session) {
	raw, sig := r.URL.Query().Get("u"), r.URL.Query().Get("s")
	if raw == "" || !hmac.Equal([]byte(sig), []byte(w.c.mediaSign(raw))) {
		http.Error(rw, "not an address of a block", http.StatusForbidden)
		return
	}
	body, kind, err := fetchMedia(r, raw)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadGateway)
		return
	}
	h := rw.Header()
	h.Set("Content-Type", kind)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "private, max-age=600")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
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

// handleTaskMedia is POST /v1/device/tasks/{id}/media, for devices: an
// image a task captured. No node keeps one yet: it is refused, once the
// device and its task are known.
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
	http.Error(w, "a task's images are not kept", http.StatusGone)
}
