package main

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
)

// A task's images for the workspace. A task lent eriphe-workspace may hand
// eriphe-core an image for one of its surfaces (a page it captured with
// eriphe-capture, worker/base): it posts the bytes to this node, with its
// own secret, at ERIPHE_MEDIA_URL,
//
//	POST <callback-url>/v1/task/media?surface=<id>   (task secret)
//
// and the node relays them to eriphe-core with its own secret and the
// task's id (/v1/node/tasks/{id}/media), which checks and keeps them. The
// bytes never go through the model.

const (
	mediaPath = "/v1/task/media"
	// mediaMax is a task's image at most; eriphe-core has its own limit.
	mediaMax = 8 << 20
	// workspaceServer is the server Eriphe provides for the workspace.
	workspaceServer = "eriphe-workspace"
)

// mediaEnv tells a task lent the workspace where to post its images.
func (n *node) mediaEnv(t *Task) map[string]string {
	if !slices.Contains(t.Spec.Servers, workspaceServer) {
		return nil
	}
	return map[string]string{"ERIPHE_MEDIA_URL": n.callbackFor(t) + mediaPath}
}

// mediaHandler serves POST /v1/task/media for the task containers.
func (n *node) mediaHandler(s *state) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(relayTaskKey{}).(*Task)
			core, err := url.Parse(s.base())
			if err != nil {
				core = &url.URL{}
			}
			pr.SetURL(core)
			pr.Out.URL.Path = strings.TrimSuffix(core.Path, "/") + "/v1/node/tasks/" + t.ID + "/media"
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = url.Values{"surface": {pr.In.URL.Query().Get("surface")}}.Encode()
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("Authorization", "Bearer "+s.Secret)
			pr.Out.Header.Set("Eriphe-Node", s.Name)
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
		if !slices.Contains(t.Spec.Servers, workspaceServer) {
			http.Error(w, "this task has no access to the workspace", http.StatusForbidden)
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
