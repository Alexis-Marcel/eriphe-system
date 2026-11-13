package main

// The incoming addresses' endpoint (incoming_hooks.go): no session, the
// token in the path is the authorisation. They are made on the Monitors
// page (web_monitors.go).
func (w *web) alertRoutes(mux *webMux) {
	mux.HandleFunc("POST "+hookPath+"{token}", w.handleAlertHook)
}
