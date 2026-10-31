package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The workspace's pages and actions. They call the same methods as the API
// and the brain's tools: the workspace is one more surface, not a new path.

const shownMessages = 150

// homeData is the conversation page.
type homeData struct {
	Msgs    []Message
	LastSeq int
	More    bool // older messages are not shown
	Pending pendingData
	Tasks   tasksData
}

// pendingData is what waits for me at the end of the conversation: the
// permissions the tasks ask for, and whether the brain is at work.
type pendingData struct {
	Perms    []permView
	Brain    string // thinking, none, off or ""
	Problems []string
}

type permView struct {
	Task     taskCard
	ID       string
	Tool     string
	Subject  string
	UserOnly string
	Where    string // an id prefix: the same request shows in the conversation and in its task
	Always   bool   // "always allow this action" is offered: a catalog tool's (access.go)
}

type tasksData struct {
	Active []taskCard
	Done   []taskCard
}

// taskCard is a task as the side panel shows it.
type taskCard struct {
	ID, Title, Prompt    string
	Node, Agent          string
	Repo, Branch         string
	State, PendingReason string
	Link, LinkLabel      string
	Updated, Created     time.Time
	NeedsYou, Finished   bool
	LastText             string
}

// split separates a task's place, owner/repo@branch, into its repository and branch.
func splitDir(dir string) (repo, branch string) {
	repo = dir
	if at := strings.LastIndex(dir, "@"); at > strings.LastIndex(dir, "/") && at > 0 {
		repo, branch = dir[:at], dir[at+1:]
	}
	repo = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(repo, "https://"), "github.com/"), ".git")
	return repo, branch
}

var prRe = regexp.MustCompile(`/(pull|merge_requests)/(\d+)`)

// findLink returns the last link an agent mentioned, a pull request first.
func findLink(events []EventRow) (link, label string) {
	var other string
	for _, e := range events {
		if e.Type != "text" && e.Type != "turn_end" {
			continue
		}
		text, _ := e.Data["text"].(string)
		if text == "" {
			text, _ = e.Data["result"].(string)
		}
		for _, m := range urlRe.FindAllString(text, -1) {
			u, _ := trimURL(m)
			if p := prRe.FindStringSubmatch(u); p != nil {
				link, label = u, "PR #"+p[2]
			} else {
				other = u
			}
		}
	}
	if link == "" && other != "" {
		if p, err := url.Parse(other); err == nil {
			link, label = other, strings.TrimPrefix(p.Hostname(), "www.")
		}
	}
	return link, label
}

func (w *web) card(st *taskStatus) taskCard {
	t := st.Task
	repo, branch := splitDir(t.Dir)
	title := strings.TrimSpace(t.Prompt)
	if i := strings.IndexByte(title, '\n'); i > 0 {
		title = title[:i]
	}
	link, label := findLink(st.Recent)
	return taskCard{ID: t.ID, Title: truncate(title, 110), Prompt: t.Prompt, Node: t.Node, Agent: t.Agent,
		Repo: repo, Branch: branch, State: t.State, PendingReason: t.PendingReason, Link: link, LinkLabel: label,
		Updated: t.Updated, Created: t.Created, Finished: finished(t.State), LastText: st.LastText,
		NeedsYou: w.needsUser(st)}
}

// tasksAndPerms reads the worker tasks for the side panel, and the
// permissions they wait for.
func (w *web) tasksAndPerms() (tasksData, []permView, error) {
	var out tasksData
	var perms []permView
	tasks, err := w.c.store.tasks(false, 30)
	if err != nil {
		return out, nil, err
	}
	for _, t := range tasks {
		if finished(t.State) && len(out.Done) >= 6 {
			continue
		}
		st, err := w.c.status(t.ID, 200)
		if err != nil {
			continue
		}
		card := w.card(st)
		if card.Finished {
			out.Done = append(out.Done, card)
			continue
		}
		out.Active = append(out.Active, card)
		if p := w.pendingPermission(st); p != nil {
			input, _ := p["input"].(map[string]any)
			tool := fmt.Sprint(p["tool_name"])
			pid := fmt.Sprint(p["id"])
			perms = append(perms, permView{Task: card, ID: pid, Tool: tool, Subject: subject(tool, input),
				UserOnly: w.c.store.userOnly(t.ID, pid), Always: w.c.rememberable(tool)})
		}
	}
	// What needs me first, then what works, then what waits.
	sort.SliceStable(out.Active, func(i, j int) bool { return cardRank(out.Active[i]) < cardRank(out.Active[j]) })
	return out, perms, nil
}

func cardRank(c taskCard) int {
	switch {
	case c.NeedsYou:
		return 0
	case c.State == stateWaitingPermission:
		return 1
	case c.State == stateRunning:
		return 2
	case c.State == stateIdle:
		return 3
	}
	return 4
}

// needsUser tells whether one of a task's pending permissions is the user's.
func (w *web) needsUser(st *taskStatus) bool {
	for _, p := range st.PendingPermissions {
		if w.c.store.userOnly(st.Task.ID, fmt.Sprint(p["id"])) != "" {
			return true
		}
	}
	return false
}

// hasPending tells whether a task still waits for that permission.
func hasPending(st *taskStatus, pid string) bool {
	for _, p := range st.PendingPermissions {
		if fmt.Sprint(p["id"]) == pid {
			return true
		}
	}
	return false
}

// pendingPermission is the permission a task waits for, unless an answer
// is already on its way to the node.
func (w *web) pendingPermission(st *taskStatus) map[string]any {
	p := st.PendingPermission
	if p == nil || w.c.store.answered(st.Task.ID, fmt.Sprint(p["id"])) {
		return nil
	}
	return p
}

// brainState tells whether the brain is set up, at work, or away.
func (w *web) brainState() string {
	b := w.c.conf().brain()
	if b == nil {
		return "none"
	}
	if b.Agent == "" {
		return "noai" // the wizard was left for later
	}
	id := w.c.brainTaskID()
	if id == "" {
		return "off"
	}
	t, err := w.c.store.task(id)
	switch {
	case err != nil || finished(t.State) || t.State == statePending:
		return "off"
	case t.State == stateRunning && w.c.brainErrorOpen() == 0:
		// After a failure, its error card says it all: no endless "thinking".
		return "thinking"
	}
	return ""
}

func (w *web) pending(perms []permView) pendingData {
	return pendingData{Perms: perms, Brain: w.brainState()}
}

func (w *web) handleHome(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.needsWelcome() {
		http.Redirect(rw, r, "/welcome", http.StatusSeeOther)
		return
	}
	w.c.firstRun() // first_run.go: once, after the wizard
	last := w.c.store.lastMessageSeq()
	msgs, err := w.c.store.messagesAfter(max(0, last-shownMessages), shownMessages)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	w.dress(msgs)
	tasks, perms, err := w.tasksAndPerms()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	w.render(rw, r, s, "home", "", page{Title: "nav.chat", Nav: "chat", Data: homeData{
		Msgs: msgs, LastSeq: last, More: last > shownMessages, Pending: w.pending(perms), Tasks: tasks}})
}

// handleSend adds my message, and the files attached to it, to the
// conversation; the event stream shows it.
func (w *web) handleSend(rw http.ResponseWriter, r *http.Request, s *session) {
	files, err := w.messageFiles(r)
	if err != nil {
		w.sendError(rw, r, err)
		return
	}
	text := strings.TrimSpace(r.PostFormValue("text"))
	if text == "" && len(files) == 0 {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	if len(text) > 20000 {
		http.Error(rw, "message too long", http.StatusRequestEntityTooLarge)
		return
	}
	atts, err := w.c.store.saveAttachments(files)
	if err != nil {
		w.sendError(rw, r, err)
		return
	}
	if _, err := w.c.store.addUserMessage(text, atts); err != nil {
		w.c.store.removeAttachments(atts)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	w.c.ping()
	w.notify()
	rw.WriteHeader(http.StatusNoContent)
}

// handleBrainRetry is an error card's "Try again" (brain_errors.go).
func (w *web) handleBrainRetry(rw http.ResponseWriter, r *http.Request, s *session) {
	w.c.retryBrain()
	w.notify()
	w.toast(rw, tr(w.lang(r), "chat.error.retried"), true)
}

// taskDetail is a task's drawer.
type taskDetail struct {
	Card        taskCard
	Events      []EventRow
	Credentials []CredentialEntry // what it asked for (credentials.go)
	Perm        *permView
	Flash       string
	OK          bool
}

func (w *web) detail(id string) (*taskDetail, error) {
	st, err := w.c.status(id, 80)
	if err != nil {
		return nil, err
	}
	d := &taskDetail{Card: w.card(st)}
	d.Credentials, _ = w.c.store.credentialLog(id, 20)
	for _, e := range st.Recent {
		switch e.Type {
		case "text", "tool_use", "permission", "permission_answer", "error":
			d.Events = append(d.Events, e)
		case "turn_end":
			if s, _ := e.Data["subtype"].(string); s != "" && s != "success" {
				d.Events = append(d.Events, e)
			}
		}
	}
	if p := w.pendingPermission(st); p != nil {
		input, _ := p["input"].(map[string]any)
		tool, pid := fmt.Sprint(p["tool_name"]), fmt.Sprint(p["id"])
		d.Perm = &permView{Task: d.Card, ID: pid, Tool: tool, Subject: subject(tool, input), UserOnly: w.c.store.userOnly(id, pid), Where: "d-",
			Always: w.c.rememberable(tool)}
	}
	return d, nil
}

func (w *web) handleTask(rw http.ResponseWriter, r *http.Request, s *session) {
	if taskLink(rw, r) {
		return
	}
	d, err := w.detail(r.PathValue("id"))
	if err != nil {
		taskError(rw, err)
		return
	}
	block := "drawer"
	if r.URL.Query().Get("part") == "body" {
		block = "drawer-body"
	}
	w.render(rw, r, s, "home", block, page{Data: d})
}

// handleAnswer allows or denies the pending permission shown, as me.
func (w *web) handleAnswer(rw http.ResponseWriter, r *http.Request, s *session) {
	id := r.PathValue("id")
	always := r.PostFormValue("allow") == "always" // and its action without asking from now on
	allow := always || r.PostFormValue("allow") == "yes"
	st, err := w.c.status(id, 0)
	if err != nil {
		taskError(rw, err)
		return
	}
	lang := w.lang(r)
	pid := r.PostFormValue("permission")
	if !hasPending(st, pid) {
		w.toast(rw, tr(lang, "perm.changed"), false)
		return
	}
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if always {
		_, err = w.c.answerAlways(r.Context(), id, pid, reason)
	} else {
		_, err = w.c.answer(r.Context(), id, pid, reason, &allow, byUser)
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.notify()
	msg := tr(lang, "perm.denied")
	switch {
	case always:
		msg = tr(lang, "perm.remembered")
	case allow:
		msg = tr(lang, "perm.allowed")
	}
	w.toast(rw, msg, true)
}

// handleReply answers the agent of an idle task, as me.
func (w *web) handleReply(rw http.ResponseWriter, r *http.Request, s *session) {
	text := strings.TrimSpace(r.PostFormValue("text"))
	if text == "" {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := w.c.answer(r.Context(), r.PathValue("id"), "", text, nil, byUser); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.notify()
	w.toast(rw, tr(w.lang(r), "task.sent"), true)
}

func (w *web) handleStop(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := w.c.stop(r.Context(), r.PathValue("id")); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.notify()
	w.toast(rw, tr(w.lang(r), "task.stopping"), true)
}

// toast answers an htmx action with a short message the page shows.
func (w *web) toast(rw http.ResponseWriter, msg string, ok bool) {
	rw.Header().Set("HX-Trigger", hxTrigger(msg, ok, false)) // web_memory.go
	rw.Header().Set("HX-Reswap", "none")
	rw.WriteHeader(http.StatusOK)
}

// Settings.

type machineView struct {
	NodeRow
	CPUs     int
	Memory   int64
	Runtimes []string
	Labels   []string
	Active   int
	AlwaysOn bool   // the tools run here
	Model    bool   // it lends a model server (model_relay.go)
	Version  string // its eriphe-node's
	System   string // its system in words, a key of web_text.go (system.*)
	Arch     string
	Update   updateRow // where its update stands (web_updates.go)
	NoTLS    bool      // it connects without its certificate yet, while machines have TLS (machine_tls.go)
	// What Eriphe sees there: the folders opened to it on this device, and
	// whether it shares any root to open one from.
	Folders []*Folder
	Shares  bool
	// What it can do there: put agents to work, if the node runs tasks.
	Workers bool
	// Whether Eriphe may wake it (web_wake.go).
	Wake wakeView
}

// deviceViews are the devices' cards, in the order of nodes, with the
// updates' rows they come from.
func (w *web) deviceViews(nodes []NodeRow, updates updatesData) ([]machineView, error) {
	folders, err := w.c.store.folders()
	if err != nil {
		return nil, err
	}
	tls := w.c.caHash() != ""
	var out []machineView
	for i, n := range nodes {
		info := parseInfo(n)
		n.Asleep = w.c.asleep(n)
		active, _ := w.c.store.activeCount(n.Name)
		m := machineView{NodeRow: n, CPUs: info.CPUs, Memory: info.Memory, Runtimes: info.Runtimes,
			Labels: info.Labels, Active: active, AlwaysOn: info.AlwaysOn, Model: info.ModelServer, Version: info.Version,
			System: systemKey(nodeInstall(n, info), n.OS), Arch: n.Arch, Update: updates.Rows[i+1],
			Shares: len(info.Shares) > 0, Workers: len(info.Runtimes) > 0, NoTLS: tls && n.CertExpires.IsZero()}
		m.Wake = w.wakeView(n, info)
		for _, f := range folders {
			if f.Node == n.Name {
				m.Folders = append(m.Folders, f)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func (w *web) handleMachines(rw http.ResponseWriter, r *http.Request, s *session) {
	nodes, err := w.c.store.nodes()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	updates := w.updatesData(nodes)
	out, err := w.deviceViews(nodes, updates)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	var plain []string
	for _, n := range nodes {
		if n.CertExpires.IsZero() {
			plain = append(plain, n.Name)
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.machines", Nav: "settings", Tab: "devices",
		Data: machinesData{Nodes: out, Remote: strings.Join(w.awayCoreURLs(), ", "), CAHash: w.c.caHash(), TLSOnly: w.c.nodesTLSOnly(),
			Plain: strings.Join(plain, ", "), Updates: updates}})
}

// systemKey names a machine's system in words, from how it was installed
// and the system its eriphe-node runs on: on a Mac, the node runs on macOS
// itself with Apple's container, or in Lima's Linux VM; on Windows, in WSL;
// installed by hand on Linux, it is a compose project of one's own (a NAS).
// Empty while unknown (a machine that never reported it).
func systemKey(install, goos string) string {
	switch {
	case install == "macos" && goos == "darwin":
		return "system.macos-container"
	case install == "macos":
		return "system.macos-lima"
	case install == "windows":
		return "system.windows"
	case install == "linux":
		return "system.linux"
	case install == "docker":
		return "system.docker"
	case goos == "linux":
		return "system.compose"
	case goos == "darwin":
		return "system.darwin"
	case goos == "windows":
		return "system.windows-native"
	}
	return ""
}

type machinesData struct {
	Nodes  []machineView
	Remote string // eriphe-core's addresses away from home: the second one, the tailnet's
	// TLS for machines (machine_tls.go): the CA's hash, whether the plain
	// path is closed, and the machines still without their certificate.
	CAHash  string
	TLSOnly bool
	Plain   string
	Updates updatesData // web_updates.go
}

type joinView struct {
	Error   string
	Name    string
	Token   string
	Core    string
	Labels  string
	Rejoin  bool // for a registered machine, to join again
	Local   bool // the address is this computer's own: another one cannot use it
	Expires time.Time
	CAHash  string // the machines' CA, which the new machine pins (machine_tls.go)
	Wait    joinWait
	// The assistant's name, which the installers print ("Connecting this Mac
	// to Jarvis"); empty for the default one, which they know.
	Assistant string
}

// plainName is a display name the commands can give in single quotes, in sh
// and in PowerShell: no apostrophe (PowerShell takes ’ for one too).
var plainName = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{M}\p{N} -]*$`)

// joinAssistant is the assistant's name for the join commands, or nothing:
// the installers say Eriphe then.
func joinAssistant() string {
	n := assistantName()
	if n == defaultDisplayName || !plainName.MatchString(n) {
		return ""
	}
	return n
}

// loopbackURL tells whether an address names this computer only.
func loopbackURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
}

// coreURL is how a new machine reaches eriphe-core: public_url, or this
// host on eriphe-core's port, or on the one its container is published on
// (ERIPHE_API_PORT, which the Eriphe container's launcher sets).
func (w *web) coreURL(r *http.Request) string {
	if w.c.conf().PublicURL != "" {
		return strings.TrimSuffix(w.c.conf().PublicURL, "/")
	}
	_, port, _ := net.SplitHostPort(w.c.conf().Listen)
	if p := publishedAPIPort(); p != "" {
		port = p
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port
}

// publishedAPIPort is ERIPHE_API_PORT, when it is a port: the host's port
// eriphe-core's container is published on, when it is not its own.
func publishedAPIPort() string {
	p := os.Getenv("ERIPHE_API_PORT")
	if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
		return ""
	}
	return p
}

var labelsRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(,[a-z0-9][a-z0-9-]*)*$`)

// handleJoinCommand makes a join token for a new machine and shows how to
// use it. A name already taken needs the machine's own "join again" action.
func (w *web) handleJoinCommand(rw http.ResponseWriter, r *http.Request, s *session) {
	name := strings.TrimSpace(strings.ToLower(r.PostFormValue("name")))
	labels := strings.ReplaceAll(strings.ToLower(r.PostFormValue("labels")), " ", "")
	v := joinView{Name: name, Labels: labels}
	if !nodeName.MatchString(name) || (labels != "" && !labelsRe.MatchString(labels)) {
		v.Error = "machines.badname"
		w.render(rw, r, s, "settings", "join-result", page{Data: v})
		return
	}
	w.joinResult(rw, r, s, v)
}

// handleRejoinCommand makes a token that lets a registered machine join
// again under its name, with a new secret (a reinstalled machine).
func (w *web) handleRejoinCommand(rw http.ResponseWriter, r *http.Request, s *session) {
	w.joinResult(rw, r, s, joinView{Name: r.PathValue("name"), Rejoin: true})
}

func (w *web) joinResult(rw http.ResponseWriter, r *http.Request, s *session, v joinView) {
	token, expires, err := w.c.newJoinToken(v.Name, v.Rejoin, 24*time.Hour)
	switch {
	case errors.Is(err, errBadNodeName):
		v.Error = "machines.badname"
	case errors.Is(err, errNodeExists):
		v.Error = "machines.exists"
	case errors.Is(err, errNoNode):
		v.Error = "machines.nonode"
	case err != nil:
		v.Error = "err.internal"
	}
	if v.Error == "" {
		v.Token, v.Core, v.Expires, v.CAHash = token, w.joinCoreURL(r, r.PostFormValue("via")), expires, w.c.caHash()
		v.Local = loopbackURL(strings.Split(v.Core, ",")[0])
		v.Wait = joinWait{Name: v.Name, ID: strings.SplitN(token, ".", 2)[0]}
		v.Assistant = joinAssistant()
		log.Printf("web: join token for %s (rejoin: %t)", v.Name, v.Rejoin)
	}
	w.render(rw, r, s, "settings", "join-result", page{Data: v})
}

func (w *web) handleForget(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.store.deleteNode(name); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.c.disconnect(name)
	log.Printf("web: node %s forgotten", name)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

// handleImportSecret copies a secret file of the configuration into the
// vault. Only the files the configuration names can be read this way.
func (w *web) handleImportSecret(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	v := currentVault()
	if v == nil {
		w.secretResult(rw, r, s, tr(lang, "accounts.novault"), false)
		return
	}
	field := r.PostFormValue("field")
	var ref *secretRef
	for _, x := range w.c.base.secretRefs() {
		if x.Field == field && !x.InVault() {
			ref = &x
		}
	}
	if ref == nil {
		w.secretResult(rw, r, s, tr(lang, "err.internal"), false)
		return
	}
	if err := v.importFile(ref.Name, ref.Ref); err != nil {
		w.secretResult(rw, r, s, err.Error(), false)
		return
	}
	log.Printf("web: %s imported into the vault as %s", ref.Field, ref.Name)
	w.secretResult(rw, r, s, fmt.Sprintf(tr(lang, "accounts.imported"), ref.Name, vaultPrefix+ref.Name), true)
}

func (w *web) handleDeleteSecret(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	v := currentVault()
	if v == nil {
		w.secretResult(rw, r, s, tr(lang, "accounts.novault"), false)
		return
	}
	name := r.PathValue("name")
	if err := v.remove(name); err != nil {
		w.secretResult(rw, r, s, err.Error(), false)
		return
	}
	log.Printf("web: secret %s deleted", name)
	w.secretResult(rw, r, s, tr(lang, "accounts.deleted"), true)
}

type decisionRow struct {
	Time                                time.Time
	TaskID, Tool, Subject, Decision, By string
	Reason                              string
}

type journalView struct {
	Rows []decisionRow
	Next int // the offset of the next page, 0 if none
}

const journalPage = 100

func (s *Store) decisions(offset, limit int) ([]decisionRow, error) {
	rows, err := s.db.Query(`SELECT time, task_id, tool, subject, decision, by, reason FROM decisions
		ORDER BY time DESC, rowid DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []decisionRow
	for rows.Next() {
		var d decisionRow
		var t int64
		if err := rows.Scan(&t, &d.TaskID, &d.Tool, &d.Subject, &d.Decision, &d.By, &d.Reason); err != nil {
			return nil, err
		}
		d.Time = time.Unix(t, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (w *web) handleJournal(rw http.ResponseWriter, r *http.Request, s *session) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	offset = max(offset, 0)
	rows, err := w.c.store.decisions(offset, journalPage+1)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	v := journalView{Rows: rows}
	if len(rows) > journalPage {
		v.Rows, v.Next = rows[:journalPage], offset+journalPage
	}
	block := ""
	if offset > 0 {
		block = "journal-rows"
	}
	w.render(rw, r, s, "settings", block, page{Title: "settings.journal", Nav: "settings", Tab: "journal", Data: v})
}

// accountPage is "Mon espace": the sign-in, and the keys and passwords the
// vault holds, to replace or delete (each is typed in its own tool's page).
type accountPage struct {
	signinView
	Address addressView // web_address.go
	Secrets secretsView
}

func (w *web) handleAccount(rw http.ResponseWriter, r *http.Request, s *session) {
	v := accountPage{signinView: w.signinView(r), Address: w.addressView(r), Secrets: w.secrets("", false)}
	w.render(rw, r, s, "settings", "", page{Title: "settings.account", Nav: "settings", Tab: "account", Data: v})
}

// handleLang keeps the language in a cookie and reloads the page.
func (w *web) handleLang(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := r.PostFormValue("lang")
	if lang != "fr" && lang != "en" {
		http.Error(rw, "fr or en", http.StatusBadRequest)
		return
	}
	http.SetCookie(rw, &http.Cookie{Name: "lang", Value: lang, Path: "/", MaxAge: 400 * 24 * 3600,
		Secure: true, SameSite: http.SameSiteStrictMode})
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}
