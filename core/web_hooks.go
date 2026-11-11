package main

import (
	"net/http"
)

// The incoming addresses' endpoint (incoming_hooks.go): no session, the
// token in the path is the authorisation. They are made on the Monitors
// page (web_monitors.go).
func (w *web) alertRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+hookPath+"{token}", w.handleAlertHook)
}
