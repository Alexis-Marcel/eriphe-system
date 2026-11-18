package main

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
)

// A task's images for its node. A task with a node of its own
// (eriphe-nodes among its servers) may hand eriphe-core an image for it (a
// page it captured with eriphe-capture, worker/base): it posts the bytes
// to this device, with its own secret, at ERIPHE_MEDIA_URL,
//
//	POST <callback-url>/v1/task/media?node=<id>   (task secret)
//
// and the device relays them to eriphe-core with its own secret and the
// task's id (/v1/device/tasks/{id}/media), which checks and keeps them. The
// bytes never go through the model.

const (
	mediaPath = "/v1/task/media"
	// mediaMax is a task's image at most; eriphe-core has its own limit.
	mediaMax = 8 << 20
	// nodesServer is the server of a task's own node, Eriphe's.
	nodesServer = "eriphe-nodes"
)

// mediaEnv tells a task with a node where to post its images.
func (n *device) mediaEnv(t *Task) map[string]string {
	if !slices.Contains(t.Spec.Servers, nodesServer) {
		return nil
	}
	return map[string]string{"ERIPHE_MEDIA_URL": n.callbackFor(t) + mediaPath}
}

// mediaHandler serves POST /v1/task/media for the task containers.
func (n *device) mediaHandler(s *state) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(relayTaskKey{}).(*Task)
			core, err := url.Parse(s.base())
			if err != nil {
				core = &url.URL{}
			}
			pr.SetURL(core)
			pr.Out.URL.Path = strings.TrimSuffix(core.Path, "/") + "/v1/device/tasks/" + t.ID + "/media"
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = url.Values{"node": {pr.In.URL.Query().Get("node")}}.Encode()
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("Authorization", "Bearer "+s.Secret)
			pr.Out.Header.Set("Eriphe-Device", s.Name)
		},
		Transport: s.relayTransport(),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := n.taskBySecret(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if t == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if finished(t.state()) {
			http.Error(w, "the task is over", http.StatusGone)
			return
		}
		if !slices.Contains(t.Spec.Servers, nodesServer) {
			http.Error(w, "this task has no node to give an image to", http.StatusForbidden)
			return
		}
		if r.ContentLength > mediaMax {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, mediaMax)
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), relayTaskKey{}, t)))
	})
}
