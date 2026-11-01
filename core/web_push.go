package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Settings → Notifications: one page for the whole subject. Its first
// section is the notifications of this device (push.go), installing the
// workspace as an app, and the devices told; the router's order
// (web_where.go), Telegram (web_telegram.go), the rules and what was sent
// (web_notify.go) follow. The page subscribes after the user turns the
// switch on; the tabs say whether they are in front, so that nothing
// buzzes while the user is looking.

func (w *web) pushRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/notifications", w.auth(w.handleNotifications))
	mux.Handle("GET /push/key", w.auth(w.handlePushKey))
	mux.Handle("POST /push/subscribe", http.MaxBytesHandler(w.auth(w.handleSubscribe), 8<<10))
	mux.Handle("POST /push/unsubscribe", w.auth(w.handleUnsubscribe))
	mux.Handle("POST /push/test", w.auth(w.handlePushTest))
	mux.Handle("POST /presence", http.MaxBytesHandler(w.auth(w.handlePresence), 1<<10))
	w.notifyRoutes(mux) // web_notify.go
}

// startPush opens the pusher and follows what waits for the user.
func (w *web) startPush() {
	p, err := newPusher(w.c.store)
	if err != nil {
		log.Printf("push: notifications off: %v", err)
		return
	}
	w.c.push = p
}

type pushDevice struct {
	ID, Label string
	Created   time.Time
	Kind      string // computer or phone, for the router (route.go)
	Choice    string // the user's, or empty: from its label
}

type notificationsPage struct {
	Key      string // the VAPID public key, for the browser's subscription
	Devices  []pushDevice
	Off      bool         // no key: notifications cannot work here
	Notify   notifyPage   // what the brain told, its rules (web_notify.go)
	Where    wherePage    // the router's order (web_where.go)
	Channels channelsPage // Telegram (web_telegram.go)
}

func (w *web) notificationsPage(lang string) notificationsPage {
	v := notificationsPage{Notify: w.notifyPage(0), Where: w.wherePage(lang), Channels: w.channelsPage()}
	p := w.c.push
	if p == nil {
		v.Off = true
		return v
	}
	v.Key = p.pub
	prefs := w.c.devicePrefs()
	subs, _ := p.subscriptions()
	for _, s := range subs {
		v.Devices = append(v.Devices, pushDevice{ID: s.ID, Label: s.Label, Created: s.Created,
			Kind: subKind(s, prefs[s.ID]), Choice: prefs[s.ID].Kind})
	}
	return v
}

func (w *web) handleNotifications(rw http.ResponseWriter, r *http.Request, s *session) {
	v := w.notificationsPage(w.lang(r))
	if open, err := strconv.ParseInt(r.URL.Query().Get("n"), 10, 64); err == nil {
		v.Notify = w.notifyPage(open) // opened from a push: that one shows why
		w.c.ackNotification(open, ackOpened)
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.notifications", Nav: "settings", Tab: "notifications", Data: v})
}

func (w *web) pushDevices(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "push-devices", page{Data: w.notificationsPage(w.lang(r))})
}

func (w *web) handlePushKey(rw http.ResponseWriter, _ *http.Request, _ *session) {
	if w.c.push == nil {
		http.Error(rw, "notifications are off", http.StatusServiceUnavailable)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]string{"key": w.c.push.pub})
}

// handleSubscribe keeps the subscription the browser gave (PushSubscription.toJSON()).
func (w *web) handleSubscribe(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.push == nil {
		http.Error(rw, "notifications are off", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		App bool `json:"app"` // the installed app, not a browser tab
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(rw, "bad subscription", http.StatusBadRequest)
		return
	}
	label := deviceLabel(r.UserAgent(), w.lang(r))
	if in.App {
		label += " · " + tr(w.lang(r), "push.app")
	}
	_, err := w.c.push.subscribe(pushSub{Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth,
		Lang: w.lang(r), Label: label, UAKind: uaKind(r.UserAgent())})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	w.pushDevices(rw, r, s)
}

func (w *web) handleUnsubscribe(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.push != nil {
		w.c.push.unsubscribe(r.PostFormValue("id"))
	}
	w.pushDevices(rw, r, s)
}

// handlePushTest sends a notification to one device, even with this tab in front.
func (w *web) handlePushTest(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	id := r.PostFormValue("id")
	if w.c.push == nil || id == "" {
		w.toast(rw, tr(lang, "push.test.none"), false)
		return
	}
	w.c.tell(pushNote{OnlyFor: id, Anyway: true, URL: "/settings/notifications", Tag: "test",
		Body: map[string]string{"fr": tr("fr", "push.test.body"), "en": tr("en", "push.test.body")}})
	w.toast(rw, tr(lang, "push.test.sent"), true)
}

// handlePresence: a tab says whether it is in front of the user, and on
// which device: its browser's push subscription, if it has one, and its
// kind. It gets back what waits to be shown in it (route_send.go).
func (w *web) handlePresence(rw http.ResponseWriter, r *http.Request, _ *session) {
	tab := r.PostFormValue("tab")
	if w.c.push != nil && tab != "" && len(tab) <= 64 {
		front := r.PostFormValue("front") == "1"
		sub := r.PostFormValue("sub")
		if !isPushID(sub) {
			sub = ""
		}
		ua := r.UserAgent()
		w.c.push.seenOn(tab, front, tabSeen{sub: sub, kind: uaKind(ua), label: deviceLabel(ua, w.lang(r))})
		if front {
			w.c.seenPresence(time.Now()) // a signal for the quiet hours (notify_quiet.go)
			if notes := w.c.showInApp(tab); len(notes) > 0 {
				writeJSON(rw, http.StatusOK, map[string]any{"notes": notes})
				return
			}
		}
	}
	rw.WriteHeader(http.StatusNoContent)
}

// isPushID: 32 hexadecimal digits, as pushID makes them.
func isPushID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// deviceLabel names a device from its browser: "iPhone · Safari".
func deviceLabel(ua, lang string) string {
	device := ""
	switch {
	case strings.Contains(ua, "iPhone"):
		device = "iPhone"
	case strings.Contains(ua, "iPad"):
		device = "iPad"
	case strings.Contains(ua, "Android"):
		device = "Android"
	case strings.Contains(ua, "Macintosh"):
		device = "Mac"
	case strings.Contains(ua, "Windows"):
		device = "Windows"
	case strings.Contains(ua, "Linux"), strings.Contains(ua, "CrOS"):
		device = "Linux"
	}
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"), strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "SamsungBrowser/"):
		browser = "Samsung Internet"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	switch {
	case device != "" && browser != "":
		return device + " · " + browser
	case device != "" || browser != "":
		return device + browser
	}
	return tr(lang, "push.device")
}

// The watch on what waits for the user: a permission only they can give
// (or any, while the assistant is away), and a question the assistant
// ended its message with. Each is told once.

func (w *web) followWaiting(stop <-chan struct{}) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		w.c.telegramReplies() // first: it says whether replies go to Telegram (telegram.go)
		w.pushWaiting()
		select {
		case <-stop:
			return
		case <-tick.C:
		case <-w.waker():
		}
	}
}

func (w *web) pushWaiting() {
	if !w.c.anyChannel() {
		return
	}
	st := w.c.store
	_, perms, err := w.tasksAndPerms()
	if err == nil {
		away := w.brainState() != "" && w.brainState() != "thinking"
		for _, pv := range perms {
			if pv.UserOnly == "" && !away {
				continue // the assistant decides, or asks
			}
			key := "push_perm:" + pv.Task.ID + ":" + pv.ID
			if st.get(key) != "" {
				continue
			}
			st.set(key, "1")
			short := strings.TrimSpace(pv.Tool + " " + truncate(strings.Join(strings.Fields(pv.Subject), " "), 100))
			w.c.tell(waitingNote(short, "/#perm-"+pv.Task.ID+"-"+pv.ID, "perm-"+pv.Task.ID+"-"+pv.ID))
		}
	}
	seen, err := strconv.Atoi(st.get("push_msg_seen"))
	if err != nil {
		st.set("push_msg_seen", strconv.Itoa(st.lastMessageSeq())) // from now on
		return
	}
	// A brain message being written is marked first (notifyWake, brainNotify).
	w.c.relayMu.Lock()
	defer w.c.relayMu.Unlock()
	msgs, err := st.messagesAfter(seen, 50)
	if err != nil || len(msgs) == 0 {
		return
	}
	for _, m := range msgs {
		if m.Role != roleBrain || st.get(notifiedKey(m.Seq)) != "" {
			continue // a notification went through notify, and was decided there
		}
		if q, ok := asksUser(m.Text); ok {
			note := waitingNote(q, fmt.Sprintf("/#m%d", m.Seq), "ask")
			note.Waiting = true // on Telegram, a reply there may carry it already (telegram.go)
			w.c.tell(note)
		}
	}
	st.set("push_msg_seen", strconv.Itoa(msgs[len(msgs)-1].Seq))
}

// taskLink sends a task's address, opened by the browser as a page (a
// link, a bookmark) rather than by htmx for the drawer, to the
// conversation with that task's drawer open.
func taskLink(rw http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("HX-Request") != "" || r.Header.Get("Sec-Fetch-Mode") != "navigate" {
		return false
	}
	http.Redirect(rw, r, "/?task="+url.QueryEscape(r.PathValue("id")), http.StatusSeeOther)
	return true
}
