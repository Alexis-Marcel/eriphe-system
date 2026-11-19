package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	ossignal "os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The epic's scenario, served for Playwright (#633, web/espace/e2e): a
// whole eriphe-core on a port of 127.0.0.1, its workspace signed in with
// testPassword, with faked tools and a scripted brain. Nothing is faked
// inside the core: the brain is a task on a device, and the device is
// fake. It takes its assignments, plays canned calls through its proxy
// (the brain's own tools, the catalog's, a worker's eriphe-nodes) and
// reports as a device does; the engine, the cards, the filing and the
// stream are the real ones. No model, no network.
//
//	ERIPHE_E2E_ADDR=127.0.0.1:8897 go test -run '^TestEspaceScenario$' -timeout 0 .
//
// The page drives it; a few moments of the world outside the user (a
// monitor's mail, a task that finishes) are asked of it at POST
// /e2e/<what>, a route of this test only.
func TestEspaceScenario(t *testing.T) {
	addr := os.Getenv("ERIPHE_E2E_ADDR")
	if addr == "" {
		t.Skip("ERIPHE_E2E_ADDR is not set: the scenario's test core serves only when asked (web/espace/e2e)")
	}
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sc := newScenario(t, ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /e2e/mail", sc.handleMail)
	mux.HandleFunc("POST /e2e/finish/{key}", sc.handleFinish)
	mux.HandleFunc("GET /e2e/tools", sc.handleTools)
	mux.Handle("/", sc.web.w.routes())
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	log.Printf("scenario: serving on http://%s", addr)
	<-ctx.Done()
	srv.Close()
}

// scenario is the world of the scenario: the core, its workspace, the
// faked tools and the device that plays the brain and the workers.
type scenario struct {
	t     *testing.T
	ctx   context.Context
	c     *core
	web   *testWeb
	tools *scenarioTools
	dev   string // the device API's URL

	mu     sync.Mutex
	nodes  map[string]string // the scenario's nodes, by a name of its own
	seqs   map[string]int    // each task's last event
	inbox  map[string]int    // each task's last inbox item handled
	tasks  map[string]string // each worker task's role: style, plan, research, check
	finish map[string]chan struct{}

	// live, for the nodes' bench (nodes_bench_live_test.go): the brain is
	// a real model, started by eriphe-core as on an install, and the
	// workers do the same for any work. Nil: the scripted brain.
	live *liveBrain
}

func newScenario(t *testing.T, ctx context.Context) *scenario {
	return newScenarioWith(t, ctx, nil)
}

// newScenarioWith is the scenario's world, its brain the scripted one, or
// live's.
func newScenarioWith(t *testing.T, ctx context.Context, live *liveBrain) *scenario {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	user := "Sam"
	if live != nil {
		user = "Lou" // not Sam, who writes the urgent mail: a real model reads both
	}
	cfg := &Config{User: user, Listen: "127.0.0.1:8800", DB: filepath.Join(dir, "core.db"), DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: secret}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p", Use: "everything"}},
		Brain:     &BrainConfig{Agent: "small", Tools: []string{"maison", "agenda", "mail", "prod"}},
		Memory:    &MemoryConfig{},
		Servers: map[string]Server{
			"maison": {URL: "https://maison.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "the house", Access: accessFree},
			"agenda": {URL: "https://agenda.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "the calendar", Access: accessReads},
			"mail":   {URL: "https://mail.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "the mail", Access: accessReads},
			"prod":   {URL: "https://prod.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "the production app", Access: accessReads},
		}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	m, _ := testMemory(t)
	m.mirror = &memMirror{dir: cfg.memoirePath()}
	c.useMemory(m)
	openGraph(t, c)
	c.useNodes(c.nodes)
	for _, s := range []string{"maison", "agenda", "prod"} {
		c.setServerTrust(s, true, trustByUser)
	}
	tools := &scenarioTools{}
	c.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := tools.server(u.name).Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	c.pool.run(ctx)
	for _, s := range []string{"maison", "agenda", "mail", "prod"} {
		waitStatus(t, c.pool.get(s), statusReady)
	}
	c.self = newMCPServer(c)

	store.registerDevice("pc", hashSecret("device-secret"))
	store.setDeviceStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	if live == nil {
		// The scripted brain needs no spec: its turns are canned. A live
		// one is started by eriphe-core, its spec the install's.
		store.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", Device: "pc", Agent: "small", Runtime: "claude-code", State: stateIdle, Prompt: "brain"})
		setBrainTask(c, "t-brain")
	}

	tw := newTestWebFor(t, c)
	tw.setup()

	api := http.NewServeMux()
	api.HandleFunc("GET /v1/device/assignments", c.handleAssignments)
	api.HandleFunc("POST /v1/device/tasks/{id}/report", c.handleReport)
	api.Handle("/v1/device/tasks/{id}/mcp/{server}", c.taskMCPHandler())
	dev := httptest.NewServer(api)
	t.Cleanup(dev.Close)

	sc := &scenario{t: t, ctx: ctx, c: c, web: tw, tools: tools, dev: dev.URL,
		nodes: map[string]string{}, seqs: map[string]int{}, inbox: map[string]int{}, tasks: map[string]string{},
		finish: map[string]chan struct{}{}, live: live}
	if live != nil {
		live.serve(sc)
	}
	sc.seed()
	go c.runAssistants(ctx, "tok")
	go c.runNodes(ctx)
	go c.runNodeSources(ctx)
	go sc.device()
	return sc
}

// seed is the graph the scenario starts from: the standing nodes the brain
// opened before (« Ma journée », the subject « Substack », the mails), and
// Eriphe's word for the morning.
func (sc *scenario) seed() {
	c := sc.c
	mk := func(name string, nw nodestore.New) {
		if nw.Author == "" {
			nw.Author = byBrain
		}
		d, err := c.nodes.Create(nw)
		if err != nil {
			sc.t.Fatal(err)
		}
		sc.nodes[name] = d.Node.Meta.ID
		// A second apart: the graph lays out roots in the order they were
		// made, to the second, and the scenario is the same each run.
		time.Sleep(time.Second)
	}
	mk("day", nodestore.New{Title: "Ma journée", Lifetime: nodefile.Standing, Body: "Ce qui se passe aujourd'hui.\n"})
	mk("substack", nodestore.New{Title: "Substack", Lifetime: nodefile.Standing, Goal: "Lancer la newsletter", Body: "La newsletter : des articles sur le métier.\n"})
	mk("mails", nodestore.New{Title: "Mails", Lifetime: nodefile.Standing, Body: "Les mails qui comptent.\n"})
	sc.c.store.addMessage(roleBrain, "Bonjour. Rien ne t'attend ce matin.", true)
}

// scenarioTools are the faked tools: the house's lights, the calendar,
// the mail and the production app's errors.
type scenarioTools struct {
	mu   sync.Mutex
	lit  []string
	sent []string
}

func (f *scenarioTools) server(name string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name, Version: "1"}, nil)
	read := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld}
	switch name {
	case "maison":
		mcp.AddTool(s, &mcp.Tool{Name: "allumer", Description: "Allume une lumière"},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				Piece string `json:"piece"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.lit = append(f.lit, in.Piece)
				return nil, map[string]any{"ok": true, "piece": in.Piece}, nil
			})
	case "agenda":
		mcp.AddTool(s, &mcp.Tool{Name: "aujourdhui", Description: "Les rendez-vous du jour", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				return nil, map[string]any{"events": []any{
					map[string]any{"start": "11:00", "title": "Point produit"},
					map[string]any{"start": "14:30", "title": "Dentiste"},
				}}, nil
			})
	case "mail":
		mcp.AddTool(s, &mcp.Tool{Name: "envoyer", Description: "Envoie un mail"},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				To   string `json:"to"`
				Body string `json:"body"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.sent = append(f.sent, in.To+": "+in.Body)
				return nil, map[string]any{"ok": true}, nil
			})
	case "prod":
		mcp.AddTool(s, &mcp.Tool{Name: "erreurs", Description: "Les erreurs 5xx par minute", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				return nil, map[string]any{"rate": 42, "series": []any{
					map[string]any{"x": "10:00", "y": 3}, map[string]any{"x": "10:05", "y": 18}, map[string]any{"x": "10:10", "y": 42},
				}}, nil
			})
	}
	return s
}

// The device.

// device plays the device "pc": it takes its assignments as a device does,
// and plays each task's part: the brain's turns, the workers' work.
func (sc *scenario) device() {
	rev := int64(-1)
	for sc.ctx.Err() == nil {
		sc.c.store.setDeviceStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
		var got struct {
			Rev   int64              `json:"rev"`
			Tasks []deviceAssignment `json:"tasks"`
		}
		if err := sc.get(fmt.Sprintf("/v1/device/assignments?rev=%d&wait=2", rev), &got); err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		rev = got.Rev
		for _, a := range got.Tasks {
			sc.play(a)
		}
	}
}

// play plays one assignment: a task new to the device starts; a turn in
// its inbox is played once.
func (sc *scenario) play(a deviceAssignment) {
	t, err := sc.c.store.task(a.ID)
	if err != nil || finished(t.State) {
		return
	}
	if a.Desired == desiredStopped {
		sc.report(a.ID, deviceReport{State: stateStopped})
		return
	}
	if t.Kind == "brain" && sc.live != nil {
		sc.live.assign(a)
		return
	}
	if t.Kind == "brain" {
		for _, it := range a.Inbox {
			if it.Kind != "message" {
				continue // a permission's answer: the turn that asked waits for it (waitAnswer)
			}
			sc.mu.Lock()
			done := it.Seq <= sc.inbox[a.ID]
			sc.inbox[a.ID] = max(sc.inbox[a.ID], it.Seq)
			sc.mu.Unlock()
			if done {
				continue
			}
			text, _ := it.Data["text"].(string)
			sc.report(a.ID, deviceReport{State: stateRunning, Delivered: it.Seq})
			go sc.brainTurn(a.ID, text)
		}
		return
	}
	sc.mu.Lock()
	_, known := sc.tasks[a.ID]
	if !known {
		sc.tasks[a.ID] = workerRole(t)
	}
	sc.mu.Unlock()
	if !known {
		sc.report(a.ID, deviceReport{State: stateRunning})
		if sc.live != nil {
			go sc.liveWork(a.ID, t)
		} else {
			go sc.work(a.ID, workerRole(t))
		}
	}
}

func (sc *scenario) get(path string, out any) error {
	req, _ := http.NewRequestWithContext(sc.ctx, "GET", sc.dev+path, nil)
	req.Header.Set("Authorization", "Bearer device-secret")
	req.Header.Set("Eriphe-Device", "pc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// report sends a device's report for a task.
func (sc *scenario) report(id string, r deviceReport) {
	raw, _ := json.Marshal(r)
	req, _ := http.NewRequest("POST", sc.dev+"/v1/device/tasks/"+id+"/report", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer device-secret")
	req.Header.Set("Eriphe-Device", "pc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("scenario: report %s: %v", id, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		log.Printf("scenario: report %s: %s", id, resp.Status)
	}
}

// events are a task's next events, numbered after its last.
func (sc *scenario) events(id string, evs ...EventRow) []EventRow {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.seqs[id] == 0 {
		if t, err := sc.c.store.task(id); err == nil {
			sc.seqs[id] = t.LastSeq
		}
	}
	for i := range evs {
		sc.seqs[id]++
		evs[i].Seq, evs[i].Time = sc.seqs[id], time.Now()
	}
	return evs
}

// call calls a tool of a server as the task's device relays it.
func (sc *scenario) call(task, server, tool string, args map[string]any) (map[string]any, error) {
	hc := &http.Client{Transport: headerTransport{"Authorization": "Bearer device-secret", "Eriphe-Device": "pc"}}
	tr := &mcp.StreamableClientTransport{Endpoint: sc.dev + "/v1/device/tasks/" + task + "/mcp/" + server, HTTPClient: hc, MaxRetries: -1}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "device"}, nil).Connect(sc.ctx, tr, nil)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	res, err := sess.CallTool(sc.ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, fmt.Errorf("%s/%s: %s", server, tool, resultText(res))
	}
	var out map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(raw, &out)
	if out == nil {
		json.Unmarshal([]byte(resultText(res)), &out)
	}
	return out, nil
}

// The brain.

// brainTurn is a turn of the scripted brain: what the user said decides
// the canned calls, then its answer ends the turn, as the agent's would.
func (sc *scenario) brainTurn(task, turn string) {
	said, node := userLine(turn)
	b := &brainPlay{sc: sc, task: task}
	answer := sc.script(b, said, node)
	evs := append(b.evs, EventRow{Type: "text", Data: map[string]any{"text": answer}}, EventRow{Type: "turn_end", Data: map[string]any{"subtype": "success"}})
	sc.report(task, deviceReport{State: stateIdle, Events: sc.events(task, evs...)})
}

// userLine is the user's last line of a turn, and the node it was said to.
var userLineRe = regexp.MustCompile(`(?m)^\[Sam \d\d:\d\d #\d+([^\]]*)\] (.*)$`)

func userLine(turn string) (said, node string) {
	all := userLineRe.FindAllStringSubmatch(turn, -1)
	if len(all) == 0 {
		return "", ""
	}
	last := all[len(all)-1]
	if _, id, ok := strings.Cut(last[1], "nœud "); ok {
		node = strings.Fields(id)[0]
	}
	return last[2], node
}

// brainPlay is what a turn of the scripted brain did: its calls, as the
// events its device reports.
type brainPlay struct {
	sc   *scenario
	task string
	evs  []EventRow
}

// self calls one of eriphe-core's tools.
func (b *brainPlay) self(tool string, args map[string]any) map[string]any {
	b.evs = append(b.evs, EventRow{Type: "tool_use", Data: map[string]any{"name": "mcp__" + selfServer + "__" + tool, "input": args}})
	out, err := b.sc.call(b.task, selfServer, tool, args)
	if err != nil {
		log.Printf("scenario: brain %s: %v", tool, err)
	}
	return out
}

// catalog calls a tool of the catalog. A read the brain reads freely
// runs through the proxy at once; anything else is asked first, as Claude
// Code asks its permission: the device reports it, the policy answers (or
// keeps it on a card, and the brain goes on), and an allowed call runs.
func (b *brainPlay) catalog(server, tool string, args map[string]any, read bool) {
	name := claudeToolName(server, tool)
	b.evs = append(b.evs, EventRow{Type: "tool_use", Data: map[string]any{"name": name, "input": args}})
	if !read {
		pid := fmt.Sprintf("p-%d", time.Now().UnixNano())
		b.sc.report(b.task, deviceReport{State: stateWaitingPermission, Events: b.sc.events(b.task, append(b.evs,
			EventRow{Type: "permission", Data: map[string]any{"id": pid, "tool_name": name, "input": args}})...)})
		b.evs = nil
		if !b.sc.waitAnswer(b.task, pid) {
			return
		}
	}
	if _, err := b.sc.call(b.task, server, tool, args); err != nil {
		log.Printf("scenario: brain %s: %v", name, err)
	}
}

// waitAnswer waits for the permission's answer in the task's inbox: true
// when it is allowed.
func (sc *scenario) waitAnswer(task, pid string) bool {
	for i := 0; i < 200 && sc.ctx.Err() == nil; i++ {
		items, _ := sc.c.store.inbox(task)
		for _, it := range items {
			if it.Kind == "permission" && fmt.Sprint(it.Data["id"]) == pid {
				sc.mu.Lock()
				sc.inbox[task] = max(sc.inbox[task], it.Seq)
				sc.mu.Unlock()
				sc.report(task, deviceReport{State: stateRunning, Delivered: it.Seq})
				allow, _ := it.Data["allow"].(bool)
				return allow
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// read is a node's last version, as node_read gives it: its body and hash.
func (b *brainPlay) read(id string) (body, hash string) {
	out := b.self("node_read", map[string]any{"id": id})
	body, _ = out["body"].(string)
	hash, _ = out["hash"].(string)
	return body, hash
}

// script is the canned part of the brain for each thing the user says.
func (sc *scenario) script(b *brainPlay, said, node string) string {
	n := func(name string) string {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		return sc.nodes[name]
	}
	keep := func(name, id string) {
		sc.mu.Lock()
		sc.nodes[name] = id
		sc.mu.Unlock()
	}
	day := n("day")
	switch {
	case strings.Contains(said, "lumière"):
		// The day is what the turn is about: the call goes into its trace.
		b.read(day)
		b.catalog("maison", "allumer", map[string]any{"piece": "bureau"}, false)
		return "C'est allumé."
	case strings.Contains(said, "style des articles"):
		out := b.self("node_open", map[string]any{"title": "Premier article", "parent": n("substack"),
			"goal": "Un plan du premier article, dans le style de ceux qui marchent",
			"steps": []any{
				map[string]any{"key": "style", "title": "Style des articles", "prompt": "Analyse le style des articles qui marchent."},
				map[string]any{"key": "plan", "title": "Plan du premier article", "after": []any{"style"}, "prompt": "Propose un plan du premier article, dans ce style."},
			}})
		if id, _ := out["id"].(string); id != "" {
			keep("article", id)
		}
		return "Je lance l'analyse du style, puis le plan."
	case strings.Contains(said, "prévu aujourd'hui"):
		b.catalog("agenda", "aujourdhui", map[string]any{}, true)
		_, hash := b.read(day)
		b.self("node_edit", map[string]any{"id": day, "expect": hash, "block": "agenda", "value": map[string]any{
			"components": []any{
				map[string]any{"id": "root", "component": "List", "children": map[string]any{"path": "/today/events", "componentId": "ev"}},
				map[string]any{"id": "ev", "component": "Text", "text": map[string]any{"@call": "formatString", "args": map[string]any{"value": "${start} · ${title}"}}},
			},
			"sources": map[string]any{"today": map[string]any{"tool": "agenda/aujourdhui"}},
		}})
		b.self("node_show", map[string]any{"id": day})
		return "Deux rendez-vous : le point produit à 11 h et le dentiste à 14 h 30."
	case strings.Contains(said, "Réponds que je regarde"):
		// What reads nothing of the mail first: the mail is someone else's
		// words, and what the turn writes after reading it is marked so.
		// The prod has no view: a child of the mail, with a live block,
		// shown beside it (the prompt's « Les vues »).
		mail := node
		out := b.self("node_open", map[string]any{"title": "Production", "parent": mail, "body": "Les erreurs de l'app.\n\n```eriphe:block errors\n" +
			`{"components": [{"id": "root", "component": "Metric", "label": "Erreurs 5xx par minute", "value": {"@path": "/errors/rate"}}], "sources": {"errors": {"tool": "prod/erreurs"}}}` + "\n```\n"})
		if id, _ := out["id"].(string); id != "" {
			keep("prod", id)
			b.self("node_show", map[string]any{"id": id})
		}
		b.self("run_task", map[string]any{"title": "Recherche sur l'erreur", "prompt": "Cherche d'où vient l'erreur 500 de la prod.", "node": mail})
		_, hash := b.read(mail)
		b.self("node_edit", map[string]any{"id": mail, "expect": hash, "old": "À écrire.", "new": "Je regarde dans l'heure."})
		b.catalog("mail", "envoyer", map[string]any{"to": "sam@example.org", "body": "Je regarde dans l'heure."}, false)
		return "Le brouillon est prêt, à toi de l'envoyer. J'ouvre la prod et je lance la recherche."
	case strings.Contains(said, "Plus court"):
		body, hash := b.read(node)
		short := strings.Replace(body, "1. Pourquoi ce métier\n", "", 1)
		b.self("node_write", map[string]any{"id": node, "expect": hash, "body": short})
		return "Plus court, le titre 2 gardé."
	case strings.Contains(said, "plan me va"):
		// On the user's word, the plan waits for them no more.
		b.self("node_set", map[string]any{"id": node, "done": true})
		return "C'est noté, le plan est fait."
	case strings.Contains(said, "Garde un œil sur mes mails"):
		// Asked at the home: its subject opens, the console in it, and the
		// view is composed there (the prompt's « Les vues »). What will run
		// alone is settled first: one message, the questions that change
		// it, each with its default, and replies to answer fast.
		out := b.self("node_open", map[string]any{"title": "Garder un œil sur mes mails", "goal": "Une vue des mails qui comptent",
			"body": "## Ce qu'on suit\n\nLes mails qui demandent quelque chose, triés pour toi.\n\n## À décider\n\n" +
				"- Quelles boîtes : toutes, par défaut.\n- Ce qui compte : ce qui demande une réponse.\n" +
				"- Te prévenir : au brief, jamais la nuit.\n- Préparer : un brouillon pour ton oui, le reste noté.\n"})
		if id, _ := out["id"].(string); id != "" {
			keep("watch", id)
		}
		return "Avant de la mettre en place, quelques questions :\n\n" +
			"1. Quelles boîtes ? Par défaut, toutes.\n2. Qui ou quoi compte surtout ? Par défaut, ce qui demande une réponse.\n" +
			"3. Je te préviens quand ? Par défaut au brief, jamais la nuit.\n4. Je prépare un brouillon pour ton oui, et je note le reste ?\n\n" +
			"<suggestions>\nFais comme tu proposes\nToutes, et réveille-moi la nuit pour la prod\n</suggestions>"
	case strings.Contains(said, "Fais comme tu proposes"):
		// The card recalls the choices: the view's goal is its why.
		b.self("propose_view", map[string]any{"title": "Mes mails", "goal": "Toutes les boîtes ; ce qui demande une réponse ; prévenu au brief, jamais la nuit",
			"body": "Les mails qui demandent une réponse.\n"})
		return "Voici la vue : dis oui sur sa carte et elle s'ouvre."
	case strings.Contains(said, "vue de l'agenda"):
		// Proposed at the home with no subject opened: its card waits in
		// the console (web_console.go's unheldCards).
		b.self("propose_view", map[string]any{"title": "Mon agenda", "body": "Les rendez-vous du jour.\n"})
		return "Voici la vue de ton agenda : dis oui sur sa carte."
	case strings.Contains(said, "à part"):
		// On the side: opened in the background, the console stays.
		b.self("node_open", map[string]any{"title": "Recherche sur les newsletters", "aside": true, "goal": "Les newsletters à garder"})
		return "Je lance la recherche à part."
	case strings.Contains(said, "archiver"):
		return "C'est toi qui archives : « Archiver », en haut de sa page."
	}
	return "D'accord."
}

// The workers.

// workerRole is what a worker task is in the scenario, by its node.
func workerRole(t *TaskRow) string {
	switch {
	case strings.Contains(t.Title, "Style"):
		return "style"
	case strings.Contains(t.Title, "Plan"):
		return "plan"
	case strings.Contains(t.Title, "Recherche"):
		return "research"
	}
	return "check"
}

// work plays a worker: the style says where it is and waits for the page
// to finish it; the plan writes its first version; the research runs on;
// any other (a final check) is done at once.
func (sc *scenario) work(id, role string) {
	switch role {
	case "style":
		sc.call(id, nodesServer, "progress", map[string]any{"text": "3 articles lus"})
		sc.waitFinish("style")
		sc.call(id, nodesServer, "node_append", map[string]any{"text": "Phrases courtes, un exemple par idée, un titre qui promet."})
		sc.done(id, "Le style : phrases courtes, un exemple par idée.")
	case "plan":
		if out, err := sc.call(id, nodesServer, "node_read", map[string]any{}); err == nil {
			hash, _ := out["hash"].(string)
			sc.call(id, nodesServer, "node_write", map[string]any{"expect": hash, "body": "1. Pourquoi ce métier\n2. Ce qui a changé en dix ans\n3. Ce qui vient\n"})
		}
		sc.done(id, "Le plan en trois titres.")
	case "research":
		sc.call(id, nodesServer, "progress", map[string]any{"text": "Journaux de la prod lus"})
	default:
		sc.done(id, "Vérifié.")
	}
}

// done ends a worker's turn with a receipt that says it is done.
func (sc *scenario) done(id, text string) {
	receipt := "\n\n```json\n" + `{"receipt": {"status": "done", "summary": "` + text + `"}}` + "\n```"
	sc.report(id, deviceReport{State: stateIdle, Events: sc.events(id,
		EventRow{Type: "text", Data: map[string]any{"text": text + receipt}},
		EventRow{Type: "turn_end", Data: map[string]any{"subtype": "success"}})})
}

func (sc *scenario) waitFinish(key string) {
	sc.mu.Lock()
	ch, ok := sc.finish[key]
	if !ok {
		ch = make(chan struct{})
		sc.finish[key] = ch
	}
	sc.mu.Unlock()
	select {
	case <-ch:
	case <-sc.ctx.Done():
	}
}

// handleTools is GET /e2e/tools: what the faked tools did, the lights lit
// and the mails sent.
func (sc *scenario) handleTools(rw http.ResponseWriter, r *http.Request) {
	f := sc.tools
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSON(rw, http.StatusOK, map[string]any{"lit": slices.Clone(f.lit), "sent": slices.Clone(f.sent)})
}

// handleFinish is POST /e2e/finish/{key}: the worker so named finishes.
func (sc *scenario) handleFinish(rw http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	sc.mu.Lock()
	ch, ok := sc.finish[key]
	if !ok {
		ch = make(chan struct{})
		sc.finish[key] = ch
	}
	select {
	case <-ch:
	default:
		close(ch)
	}
	sc.mu.Unlock()
	rw.WriteHeader(http.StatusNoContent)
}

// handleMail is POST /e2e/mail: the mails' monitor brings an urgent mail
// from Sam about the production app, and its skill opens a node for it,
// sorted, with a draft, waiting for the user.
func (sc *scenario) handleMail(rw http.ResponseWriter, r *http.Request) {
	id, err := sc.mail()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

// mail is what the mails' skill does with the urgent mail: a node, under
// the mails, sorted, with a draft, waiting for the user.
func (sc *scenario) mail() (string, error) {
	c := sc.c
	d, err := c.nodes.Create(nodestore.New{Title: "Urgent : la prod renvoie des 500", Parent: sc.nodes["mails"], External: true, Author: "skill:mails",
		Body: "**Tri** : urgent. Sam (sam@example.org) écrit que l'app de production renvoie des erreurs 500 depuis 10 h.\n\n**Brouillon** : À écrire.\n"})
	if err != nil {
		return "", err
	}
	sc.mu.Lock()
	sc.nodes["mail"] = d.Node.Meta.ID
	sc.mu.Unlock()
	c.nodes.SetState(d.Node.Meta.ID, nodestore.Waiting)
	return d.Node.Meta.ID, nil
}
