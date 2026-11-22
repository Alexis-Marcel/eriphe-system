package main

import (
	"bytes"
	"cmp"
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
	"strconv"
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
	mux.HandleFunc("GET /e2e/turns", sc.handleTurns)
	mux.HandleFunc("POST /e2e/actions", sc.handleActions)
	mux.HandleFunc("POST /e2e/review", sc.handleReview)
	mux.HandleFunc("POST /e2e/sections", sc.handleSections)
	mux.HandleFunc("POST /e2e/open", sc.handleOpen)
	mux.HandleFunc("POST /e2e/bilan", sc.handleBilan)
	mux.HandleFunc("POST /e2e/purge", sc.handlePurge)
	mux.HandleFunc("POST /e2e/courrier", sc.handleCourrier)
	mux.HandleFunc("POST /e2e/proposals", sc.handleProposals)
	mux.HandleFunc("POST /e2e/catalog", sc.handleCatalog)
	mux.HandleFunc("POST /e2e/conso", sc.handleConso)
	mux.HandleFunc("POST /e2e/view", sc.handleView)
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
	turns  [][]string // the user's lines of each of the brain's turns (waiting.spec.ts)

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
			"veille": {URL: "https://veille.example.org/mcp", Auth: &ServerAuth{None: true}, Use: "the news watch", Access: accessReadOnly},
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
	for _, s := range []string{"maison", "agenda", "mail", "prod", "veille"} {
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
	mu      sync.Mutex
	lit     []string
	sent    []string
	moved   [][]string // ranger: the ids of each call (selection.spec.ts)
	deleted [][]string // supprimer: the ids of each call (sections.spec.ts)
	read    []string   // lire: the id of each call (sources-open.spec.ts)
	trashed []string   // trash: "<account>:<ids>" of each call (view-bilan.spec.ts)
	replied []string   // repondre: the id of each call (view-bilan.spec.ts)
	purged  []string   // purger: the ids it deleted (view-refresh.spec.ts)
	kw      float64    // conso: the house's consumption (view-glance.spec.ts)
	// veille: the news watch's server, whose tool changes after it was
	// added (tools.spec.ts).
	veille *mcp.Server
}

// guetter is the news watch's tool, as it was added and as it changed.
func guetter(desc string) *mcp.Tool {
	return &mcp.Tool{Name: "guetter", Description: desc} // never in a read-only grid: lent to no one, the brain unchanged
}

func guetterRun(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
	return nil, map[string]any{"news": []any{}}, nil
}

// bilan is the mailbox the view as a bilan shows (view-bilan.spec.ts), in two
// accounts: what trash took and what repondre answered leave it.
var bilan = []map[string]any{
	{"id": "a1", "subject": "Devis terrasse", "from": "Sam <sam@example.org>", "account": "perso", "date": "2026-11-20T08:10:00Z",
		"body": "Bonjour,\n\nVoici le devis pour la terrasse : 4 200 €, pose comprise. Peux-tu me dire avant vendredi si on lance la commande ?\n\nSam"},
	{"id": "a2", "subject": "Avenant au bail", "from": "Agence <agence@example.org>", "account": "travail", "date": "2026-11-19T16:40:00Z",
		"body": "Bonjour,\n\nVous trouverez ci-joint l'avenant au bail (3 pages). Rien à signer, il est pour information.\n\nL'agence"},
	{"id": "j1", "subject": "Soldes d'hiver", "from": "promo@example.org", "account": "perso", "date": "2026-11-20T06:00:00Z", "body": "-50 % sur tout."},
	{"id": "j2", "subject": "Lettre du dimanche", "from": "news@example.org", "account": "travail", "date": "2026-11-19T07:00:00Z", "body": "Les nouvelles de la semaine."},
	{"id": "j3", "subject": "Votre colis est bloqué", "from": "colis@example.net", "account": "perso", "date": "2026-11-18T12:00:00Z", "body": "Payez 1,99 € pour le débloquer."},
	{"id": "i1", "subject": "Facture d'électricité", "from": "energie@example.org", "account": "perso", "date": "2026-11-18T09:00:00Z", "body": "62 €, prélevés le 5 décembre."},
	{"id": "i2", "subject": "Entraînement de samedi", "from": "club@example.org", "account": "travail", "date": "2026-11-17T18:00:00Z", "body": "Avancé à 9 h."},
}

// courrier is the mailbox of the view a call to approve lands on
// (view-calls.spec.ts): what trash took leaves it.
var courrier = []map[string]any{
	{"id": "c1", "subject": "Billets pour Disney", "from": "Parc <billets@example.org>"},
	{"id": "c2", "subject": "Votre colis est arrivé", "from": "Relais <relais@example.org>"},
	{"id": "c3", "subject": "Compte rendu du conseil", "from": "Sam <sam@example.org>"},
	{"id": "c4", "subject": "Colis en point relais", "from": "Relais <relais@example.org>"},
}

// bilanNotes are the bilan's items as its skill notes them: group, note.
var bilanNotes = map[string][2]string{
	"a1": {"À traiter", "Répondre à Sam — brouillon prêt, avant vendredi"},
	"a2": {"À traiter", "À lire : l'avenant au bail, rien à signer"},
	"j1": {"À jeter", "Publicité"},
	"j2": {"À jeter", "Newsletter jamais ouverte"},
	"j3": {"À jeter", "Hameçonnage : faux transporteur"},
	"i1": {"Pour info", "62 €, prélevés le 5 décembre"},
	"i2": {"Pour info", "Entraînement avancé à 9 h"},
}

// bilanReply is the reply the bilan's skill drafted for Sam.
const bilanReply = "Bonjour Sam,\n\nMerci pour le devis, il me va : tu peux lancer la commande. Dis-moi quand la pose est possible.\n\nÀ bientôt"

// tri is the mailbox the view in sections shows (sections.spec.ts),
// noted into two groups: what supprimer deleted leaves it.
var tri = []map[string]any{
	{"id": "s1", "subject": "Soldes d'hiver", "from": "promo@example.org"},
	{"id": "s2", "subject": "Compte rendu du comité", "from": "sam@example.org"},
	{"id": "s3", "subject": "Votre colis est en route", "from": "colis@example.org"},
	{"id": "s4", "subject": "Lettre du dimanche", "from": "news@example.org"},
	{"id": "s5", "subject": "Planning de mars", "from": "lou@example.org"},
}

// promos is the mailbox the conversation cleans (view-refresh.spec.ts):
// what purger deleted leaves it.
var promos = []map[string]any{
	{"id": "p1", "subject": "Soldes d'automne", "from": "promo@example.org"},
	{"id": "p2", "subject": "Compte rendu de la réunion", "from": "sam@example.org"},
	{"id": "p3", "subject": "Dernière chance : -70 %", "from": "promo@example.org"},
	{"id": "p4", "subject": "Photos du week-end", "from": "lou@example.org"},
}

// boite is the mailbox the list with actions shows (selection.spec.ts):
// what ranger moved leaves it.
var boite = []map[string]any{
	{"id": "b1", "subject": "Facture d'octobre", "from": "facturation@example.org"},
	{"id": "b2", "subject": "Lettre d'information", "from": "news@example.org"},
	{"id": "b3", "subject": "Réunion de lundi", "from": "sam@example.org"},
	{"id": "b4", "subject": "Promotion de la semaine", "from": "promo@example.org"},
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
		mcp.AddTool(s, &mcp.Tool{Name: "conso", Description: "La consommation de la maison, en kW", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				return nil, map[string]any{"kw": cmp.Or(f.kw, 2)}, nil
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
		mcp.AddTool(s, &mcp.Tool{Name: "recus", Description: "Les mails reçus", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				return nil, map[string]any{"messages": []any{
					map[string]any{"id": "m1", "subject": "Facture d'octobre", "from": "facturation@example.org"},
				}}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "boite", Description: "La boîte de réception", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				gone := map[string]bool{}
				for _, ids := range f.moved {
					for _, id := range ids {
						gone[id] = true
					}
				}
				msgs := []any{}
				for _, m := range boite {
					if !gone[m["id"].(string)] {
						msgs = append(msgs, m)
					}
				}
				return nil, map[string]any{"messages": msgs}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "tri", Description: "Les mails à trier", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				gone := map[string]bool{}
				for _, ids := range f.deleted {
					for _, id := range ids {
						gone[id] = true
					}
				}
				msgs := []any{}
				for _, m := range tri {
					if !gone[m["id"].(string)] {
						msgs = append(msgs, m)
					}
				}
				return nil, map[string]any{"messages": msgs}, nil
			})
		// lus lists two mails, lire reads one in full (sources-open.spec.ts).
		mcp.AddTool(s, &mcp.Tool{Name: "lus", Description: "Les mails à lire", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				return nil, map[string]any{"messages": []any{
					map[string]any{"id": "l1", "subject": "Ordre du jour", "from": "sam@example.org"},
					map[string]any{"id": "l2", "subject": "Photos du week-end", "from": "lou@example.org"},
				}}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "bilan", Description: "Les mails de la boîte, des deux comptes", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				msgs := []any{}
				for _, m := range bilan {
					id := m["id"].(string)
					if !slices.ContainsFunc(f.trashed, func(t string) bool { return slices.Contains(strings.Split(t[strings.Index(t, ":")+1:], ","), id) }) {
						msgs = append(msgs, map[string]any{"id": id, "subject": m["subject"], "from": m["from"], "account": m["account"], "date": m["date"]})
					}
				}
				return nil, map[string]any{"messages": msgs}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "courrier", Description: "Le courrier du compte perso", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				msgs := []any{}
				for _, m := range courrier {
					id := m["id"].(string)
					if !slices.ContainsFunc(f.trashed, func(t string) bool { return slices.Contains(strings.Split(t[strings.Index(t, ":")+1:], ","), id) }) {
						msgs = append(msgs, m)
					}
				}
				return nil, map[string]any{"messages": msgs}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "lettre", Description: "Lit un mail de la boîte en entier", Annotations: read},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				ID string `json:"id"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				for _, m := range bilan {
					if m["id"] == in.ID {
						return nil, map[string]any{"subject": m["subject"], "from": m["from"], "date": m["date"], "body": m["body"]}, nil
					}
				}
				return nil, map[string]any{}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "trash", Description: "Met des mails d'un compte à la corbeille"},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				IDs     []string `json:"ids"`
				Account string   `json:"account"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.trashed = append(f.trashed, in.Account+":"+strings.Join(in.IDs, ","))
				return nil, map[string]any{"trashed": len(in.IDs)}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "repondre", Description: "Répond à un mail", Annotations: &mcp.ToolAnnotations{Title: "Envoyer la réponse"}},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.replied = append(f.replied, in.ID)
				return nil, map[string]any{"ok": true}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "lire", Description: "Lit un mail en entier", Annotations: read},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				ID string `json:"id"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.read = append(f.read, in.ID)
				return nil, map[string]any{"subject": "Ordre du jour", "from": "Sam <sam@example.org>", "date": "2026-11-20T09:00:00Z",
					"body": "Bonjour,\nvoici l'ordre du jour de " + in.ID + "."}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "delete", Description: "Supprime des mails"},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				IDs []string `json:"ids"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.deleted = append(f.deleted, slices.Clone(in.IDs))
				return nil, map[string]any{"deleted": len(in.IDs)}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "move", Description: "Range des mails dans un dossier"},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				IDs []string `json:"ids"`
				To  string   `json:"to"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.moved = append(f.moved, slices.Clone(in.IDs))
				return nil, map[string]any{"moved": len(in.IDs)}, nil
			})
		// promos lists the mailbox the conversation cleans, purger deletes
		// from it (view-refresh.spec.ts).
		mcp.AddTool(s, &mcp.Tool{Name: "promos", Description: "Les mails de la boîte à nettoyer", Annotations: read},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				msgs := []any{}
				for _, m := range promos {
					if !slices.Contains(f.purged, m["id"].(string)) {
						msgs = append(msgs, m)
					}
				}
				return nil, map[string]any{"messages": msgs}, nil
			})
		mcp.AddTool(s, &mcp.Tool{Name: "purger", Description: "Supprime des mails pour de bon"},
			func(_ context.Context, _ *mcp.CallToolRequest, in struct {
				IDs []string `json:"ids"`
			}) (*mcp.CallToolResult, map[string]any, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.purged = append(f.purged, in.IDs...)
				return nil, map[string]any{"deleted": len(in.IDs)}, nil
			})
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
	case "veille":
		mcp.AddTool(s, guetter("Guette les nouvelles du métier."), guetterRun)
		f.mu.Lock()
		f.veille = s
		f.mu.Unlock()
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
	if said != "" {
		var lines []string
		for _, m := range userLineRe.FindAllStringSubmatch(turn, -1) {
			lines = append(lines, m[2])
		}
		sc.mu.Lock()
		sc.turns = append(sc.turns, lines)
		sc.mu.Unlock()
	}
	b := &brainPlay{sc: sc, task: task}
	var answer string
	if said == "" {
		answer = sc.event(b, turn) // no message of the user's: an event woke it
	} else {
		answer = sc.script(b, said, node)
	}
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
	case strings.Contains(said, "Prends ton temps"):
		// Eriphe at work until the page releases it (POST /e2e/finish/hold):
		// what the user says meanwhile waits (waiting.spec.ts).
		sc.waitFinish("hold")
		return "Me voilà."
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
		// A tall page: a long document with a table, as a real one is.
		out := b.self("node_open", map[string]any{"title": "Production", "parent": mail, "body": "Les erreurs de l'app.\n\n" + prodNotes + "```eriphe:block errors\n" +
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
		// Asked at the home: its subject opens, the bar in it, and the view
		// is composed there (the prompt's « Les vues »). What will run alone
		// is settled first: the questions that change it, each with its
		// default, in the subject's document, and the buttons that answer
		// them; the answer is one line.
		out := b.self("node_open", map[string]any{"title": "Garder un œil sur mes mails", "goal": "Une vue des mails qui comptent",
			"body": "## Ce qu'on suit\n\nLes mails qui demandent quelque chose, triés pour toi.\n\n## À décider\n\n" +
				"1. Quelles boîtes ? Par défaut, toutes.\n2. Qui ou quoi compte surtout ? Par défaut, ce qui demande une réponse.\n" +
				"3. Je te préviens quand ? Par défaut au brief, jamais la nuit.\n4. Je prépare un brouillon pour ton oui, et je note le reste.\n\n" +
				askBlock("reponses", "Fais comme tu proposes", "Toutes, et réveille-moi la nuit pour la prod")})
		if id, _ := out["id"].(string); id != "" {
			keep("watch", id)
		}
		return "J'ai écrit mes questions dans le sujet."
	case strings.Contains(said, "Fais comme tu proposes") && node == n("write"):
		// The article's defaults: the questions go, the choices are written.
		_, hash := b.read(node)
		b.self("node_write", map[string]any{"id": node, "expect": hash, "body": articleDoc("## Ce qu'on a décidé\n\n- Pour des salariés, 1 000 mots.\n", "", "Une recherche, puis l'angle.")})
		return "C'est noté : pour des salariés, 1 000 mots."
	case strings.Contains(said, "Fais comme tu proposes"):
		// The card recalls the choices: the view's goal is its why. The whole
		// view, as a real brain composes it: its node, the monitor that looks
		// every 5 minutes and the skill that sorts each mail. Its card is
		// placed in the subject's document, under what was decided.
		out := b.self("propose_view", map[string]any{"title": "Mes mails", "goal": "Toutes les boîtes ; ce qui demande une réponse ; prévenu au brief, jamais la nuit",
			"body":    "Les mails qui demandent une réponse.\n",
			"monitor": map[string]any{"name": "mails-recus", "server": "mail", "tool": "recus", "cond": "new", "path": "messages", "id": "id", "every": "5m"},
			"skill": map[string]any{"name": "mails-trier", "description": "Trie chaque mail reçu et prépare une réponse à ceux qui en demandent une.",
				"when": "monitor.new key=mails-recus", "every": "15m", "model": "small", "tools": []string{"mail"},
				"instructions": "# Trier les mails\n\nPour **chaque** mail reçu :\n\n1. Note-le dans la page.\n2. S'il demande une réponse, ouvre un node « Répondre à … » avec un brouillon.\n\nN'envoie rien : l'envoi attend le `oui`.\n"}})
		card, _ := out["card"].(string)
		_, hash := b.read(node)
		b.self("node_write", map[string]any{"id": node, "expect": hash, "body": "## Ce qu'on suit\n\nLes mails qui demandent quelque chose, triés pour toi.\n\n" +
			"## Ce qu'on a décidé\n\n- Toutes les boîtes ; ce qui demande une réponse.\n- Prévenu au brief, jamais la nuit.\n\n" +
			"```eriphe:block vue\n{\"card\": \"" + card + "\"}\n```\n\n## Ensuite\n\nTon oui sur la carte crée la vue.\n"})
		return "Voici la vue : dis oui sur sa carte et elle s'ouvre."
	case strings.Contains(said, "vue de l'agenda"):
		// Proposed at the home with no subject opened: its card waits
		// above the bar (web_orb.go's unheldCards).
		b.self("propose_view", map[string]any{"title": "Mon agenda", "body": "Les rendez-vous du jour.\n"})
		return "Voici la vue de ton agenda : dis oui sur sa carte."
	case strings.Contains(said, "à part"):
		// On the side: opened in the background, the bar stays.
		b.self("node_open", map[string]any{"title": "Recherche sur les newsletters", "aside": true, "goal": "Les newsletters à garder"})
		return "Je lance la recherche à part."
	case strings.Contains(said, "en retard"):
		// Said in a subject's bar, about something else: the turn answers as
		// at the home (turn_home), and the card its send needs waits above
		// the bar, in front of the user (talk-at-home.spec.ts).
		b.self("turn_home", map[string]any{})
		b.catalog("mail", "envoyer", map[string]any{"to": "lou@example.org", "body": "Je serai en retard."}, false)
		return "Tu me donnes ton accord pour l'envoyer ?"
	case strings.Contains(said, "mes films"):
		// A short answer to a simple question, more than a line: it stays at
		// the home, held whole above the bar (talk-at-home.spec.ts).
		return homeFilms
	case strings.Contains(said, "supprime ces 2"):
		// Asked in the view's bar: the brain calls the tool on the rows'
		// ids; its card lands on those rows (view-calls.spec.ts).
		b.catalog("mail", "trash", map[string]any{"account": "perso", "ids": []any{"c2", "c4"}}, false)
		return "Je les mets à la corbeille dès que tu dis oui."
	case strings.Contains(said, "archiver"):
		return "C'est toi qui archives : « Archiver », en haut de sa page."
	case strings.Contains(said, "Garde un œil sur la consommation"):
		// A glance on the figure the user asked to watch, on a node of its
		// own, on the side (view-glance.spec.ts).
		out := b.self("node_open", map[string]any{"title": "Énergie maison", "aside": true, "body": "La consommation de la maison.\n\n```eriphe:block conso\n" +
			`{"components": [{"id": "root", "component": "Metric", "label": "Consommation (kW)", "value": {"@path": "/conso/kw"}}], "sources": {"conso": {"tool": "maison/conso", "glance": {"label": "Conso", "path": "kw", "threshold": 10}}}}` + "\n```\n"})
		if id, _ := out["id"].(string); id != "" {
			keep("conso", id)
		}
		return "Je garde un œil sur la consommation."
	case strings.Contains(said, "Supprime les deux promos"):
		// From the conversation, a call the user allowed for good: the view
		// that lists the mailbox reads it again by itself
		// (view-refresh.spec.ts).
		b.catalog("mail", "purger", map[string]any{"ids": []string{"p1", "p3"}}, false)
		return "Les deux promos sont supprimées."
	case strings.Contains(said, "Écris-moi un article"):
		// Asked at the home: the main subject opens with its plan and its
		// questions, their buttons below.
		out := b.self("node_open", map[string]any{"title": "Écrire l'article", "goal": "Un article sur le télétravail, publié",
			"body": articleDoc("## À décider\n\n1. Pour qui ? Par défaut, des salariés.\n2. Quelle longueur ? Par défaut, 1 000 mots.\n\n"+
				askBlock("reponses", "Fais comme tu proposes", "Écris-le pour des managers"), "", "Tes réponses, puis une recherche.")})
		if id, _ := out["id"].(string); id != "" {
			keep("write", id)
		}
		return "J'ouvre le sujet de l'article."
	case strings.Contains(said, "recherche sur le côté"):
		// On the side: a child of the subject, in the background, with the
		// task that does the research.
		out := b.self("node_open", map[string]any{"title": "Recherche sur le télétravail", "parent": node, "aside": true, "goal": "Ce que disent les études"})
		if id, _ := out["id"].(string); id != "" {
			keep("wresearch", id)
			b.self("run_task", map[string]any{"title": "Documentation sur le télétravail", "prompt": "Lis les études récentes sur le télétravail et résume-les.", "node": id})
		}
		return "Je lance la recherche sur le côté."
	case strings.Contains(said, "angle des managers"):
		// Chosen in « Choisir l'angle »: the choice is written there, the
		// deliverable gets its first version, and the main subject says
		// where things stand.
		_, hash := b.read(node)
		b.self("node_write", map[string]any{"id": node, "expect": hash, "body": "## Ce qu'on a décidé\n\nL'angle des managers : ce qui change pour eux.\n"})
		out := b.self("node_open", map[string]any{"title": "L'article", "parent": n("write"), "aside": true,
			"body": "# Télétravail : ce qui change pour les managers\n\nTrois études le disent : on travaille autant, autrement.\n"})
		if id, _ := out["id"].(string); id != "" {
			keep("deliverable", id)
		}
		_, hash = b.read(n("write"))
		b.self("node_write", map[string]any{"id": n("write"), "expect": hash, "body": articleDoc(articleDecided+"- L'angle des managers.\n", articleResearch, "Relire [[L'article]], version 1.", articleLinked...)})
		return "J'ai écrit une première version de l'article."
	case strings.Contains(said, "Ajoute une conclusion"):
		body, hash := b.read(node)
		b.self("node_write", map[string]any{"id": node, "expect": hash, "body": body + "\n## Pour conclure\n\nLe bureau devient un lieu de rencontre.\n"})
		_, hash = b.read(n("write"))
		b.self("node_write", map[string]any{"id": n("write"), "expect": hash, "body": articleDoc(articleDecided+"- L'angle des managers.\n", articleResearch, "Relire [[L'article]], version 2, avec sa conclusion.", articleLinked...)})
		return "Conclusion ajoutée."
	}
	return "D'accord."
}

// event is the scripted brain's turn on an event: the research of the
// article done, it updates the research's node and the main subject, and
// opens « Choisir l'angle » on the side, its options and their buttons.
// The catalog changed: a line. Any other event: nothing to say.
func (sc *scenario) event(b *brainPlay, turn string) string {
	if strings.Contains(turn, catalogEvent) {
		return "Un outil de plus : films."
	}
	if strings.Contains(turn, "a dit oui") && strings.Contains(turn, "trash") {
		return "C'est fait : les 2 mails sont à la corbeille."
	}
	if !strings.Contains(turn, "« Documentation sur le télétravail »") || !strings.Contains(turn, "a fini son tour") {
		return "[rien]"
	}
	sc.mu.Lock()
	research, write := sc.nodes["wresearch"], sc.nodes["write"]
	sc.mu.Unlock()
	_, hash := b.read(research)
	b.self("node_write", map[string]any{"id": research, "expect": hash, "body": "## Ce qu'on a trouvé\n\n" + articleSummary + "\n"})
	_, hash = b.read(write)
	b.self("node_write", map[string]any{"id": write, "expect": hash, "body": articleDoc(articleDecided, articleResearch, "[[Choisir l'angle]].", articleLinked[:2]...)})
	out := b.self("node_open", map[string]any{"title": "Choisir l'angle", "parent": write, "aside": true,
		"body": "Deux angles tiennent, d'après la recherche.\n\n- Les managers : ce qui change pour eux.\n- Les salariés : comment s'organiser.\n\n" +
			askBlock("angles", "L'angle des managers", "L'angle des salariés")})
	if id, _ := out["id"].(string); id != "" {
		sc.mu.Lock()
		sc.nodes["angle"] = id
		sc.mu.Unlock()
	}
	return "La recherche est faite : choisis l'angle."
}

// The article's subject, as the scripted brain writes it again at each
// step: its plan, what was decided, what the research found, what comes
// next, and its subjects linked in its text ([[title]]), as the prompt
// asks: the graph draws them, the page opens them.
const (
	articleSummary  = "Trois études récentes : on travaille autant, autrement ; les managers changent le plus."
	articleDecided  = "## Ce qu'on a décidé\n\n- Pour des salariés, 1 000 mots.\n"
	articleResearch = "## La recherche\n\n" + articleSummary + "\n"
)

var articleLinked = []string{"[[Recherche sur le télétravail|La recherche]]", "[[Choisir l'angle]]", "[[L'article]]"}

func articleDoc(decided, research, next string, linked ...string) string {
	related := ""
	if len(linked) > 0 {
		related = "## Les sujets liés\n\n- " + strings.Join(linked, "\n- ") + "\n"
	}
	parts := []string{"## Le plan\n\n1. Ce qui a changé\n2. Ce qui marche\n3. Ce qui reste à inventer\n", decided, research, "## Ensuite\n\n" + next + "\n", related}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, strings.TrimRight(p, "\n")+"\n")
		}
	}
	return strings.Join(out, "\n")
}

// askBlock is a block of buttons that answer a question: each asks
// Eriphe its own words, as the user's message in the node.
func askBlock(name string, answers ...string) string {
	comps := []any{map[string]any{"id": "root", "component": "Row", "children": func() []string {
		var ids []string
		for i := range answers {
			ids = append(ids, fmt.Sprintf("r%d", i))
		}
		return ids
	}()}}
	for i, a := range answers {
		c := map[string]any{"id": fmt.Sprintf("r%d", i), "component": "Button", "label": a, "action": map[string]any{"event": map[string]any{"name": "ask", "context": map[string]any{"text": a}}}}
		if i == 0 {
			c["variant"] = "primary"
		}
		comps = append(comps, c)
	}
	raw, _ := json.Marshal(map[string]any{"components": comps})
	return "```eriphe:block " + name + "\n" + string(raw) + "\n```\n"
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
	case strings.Contains(t.Title, "Documentation"):
		return "docs"
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
	case "docs":
		sc.call(id, nodesServer, "progress", map[string]any{"text": "3 études lues"})
		sc.waitFinish("docs")
		sc.call(id, nodesServer, "node_append", map[string]any{"text": articleSummary})
		sc.done(id, "Trois études lues et résumées.")
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
	writeJSON(rw, http.StatusOK, map[string]any{"lit": jsonList(f.lit), "sent": jsonList(f.sent), "moved": jsonList(f.moved), "deleted": jsonList(f.deleted), "read": jsonList(f.read), "trashed": jsonList(f.trashed), "replied": jsonList(f.replied), "purged": jsonList(f.purged)})
}

// jsonList is a copy of s, empty rather than nil: a spec reads [] before the
// first call, never null.
func jsonList[T any](s []T) []T {
	return append([]T{}, s...)
}

// handleCatalog is POST /e2e/catalog: the tools' catalog changed, an
// event of the home's, as catalog.go writes it; the brain's turn on it
// says a line (talk-at-home.spec.ts).
func (sc *scenario) handleCatalog(rw http.ResponseWriter, r *http.Request) {
	if _, err := sc.c.store.addMessage(roleEvent, catalogEvent, false); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	sc.c.ping()
	rw.WriteHeader(http.StatusNoContent)
}

// homeFilms is the scripted brain's answer to « tu as accès à mes films ? ».
const homeFilms = "Oui, je suis branché sur **Seerr** : je peux chercher un film, voir ce qui est déjà demandé et en demander un pour toi. " +
	"Pour l'instant, chaque recherche te demande ton accord avant de partir. " +
	"Tu peux mettre la recherche et la consultation en accès libre dans Réglages → Outils → films."

// catalogEvent is the event of /e2e/catalog.
const catalogEvent = "Le catalogue d'outils a changé : un outil « films » de plus."

// handleTurns is GET /e2e/turns: the user's lines of each of the brain's
// turns, in order.
func (sc *scenario) handleTurns(rw http.ResponseWriter, r *http.Request) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	writeJSON(rw, http.StatusOK, map[string]any{"turns": slices.Clone(sc.turns)})
}

// handleProposals is POST /e2e/proposals: the view « Mes mails »'s
// skill, woken once per mail, proposed to archive three, one card each,
// as an install made them before they folded: the next sync folds them
// into one (call_fold.go).
func (sc *scenario) handleProposals(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	found, _ := c.nodes.Find("Mes mails", false, 10)
	node := ""
	for _, s := range found {
		if s.Title == "Mes mails" {
			node = s.ID
		}
	}
	if node == "" {
		http.Error(rw, "no view « Mes mails »", http.StatusNotFound)
		return
	}
	for _, m := range []itemLabel{
		{ID: "n1", Title: "Offre : Data Engineer", Detail: "emplois@example.org"},
		{ID: "n2", Title: "La lettre de la semaine", Detail: "news@example.org"},
		{ID: "n3", Title: "Soldes d'hiver", Detail: "promo@example.org"},
	} {
		raw, _ := json.Marshal(map[string]any{"ids": []string{m.ID}, "to": "archive"})
		labels, _ := json.Marshal([]itemLabel{m})
		res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, server, category, call, input, tainted, created, node, item, labels)
			VALUES ('t-brain', 'mail', 'change', 'mcp__mail__move []', ?, 'external:mail', ?, ?, ?, ?)`, string(raw), time.Now().Unix(), node, m.ID, string(labels))
		id, err := rowsID(res, err)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, id, id)
		c.cardNow(cardCalls, id, "", refNode+":"+node)
	}
	c.syncCards()
	c.bump()
	rw.WriteHeader(http.StatusNoContent)
}

// handleActions is POST /e2e/actions: a node whose block lists the
// mailbox with an action that archives what the user picks
// (selection.spec.ts), written by the brain as any author, read once, and
// shown.
func (sc *scenario) handleActions(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	d, err := c.nodes.Create(nodestore.New{Title: "Tri de la boîte", Lifetime: nodefile.Standing, Author: byBrain, Body: "La boîte, à trier.\n"})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	id := d.Node.Meta.ID
	body := "La boîte, à trier.\n\n```eriphe:block boite\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/boite/messages", "componentId": "row"}},
  {"id": "row", "component": "Item", "title": {"@path": "subject"}, "detail": {"@path": "from"}}],
 "sources": {"boite": {"tool": "mail/boite", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}}},
 "actions": {"archive": {"label": "Ranger", "tool": "mail/move", "args": {"to": "archive"}, "items": "ids", "source": "boite"}}}` + "\n```\n"
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	c.readNode(sc.ctx, id, nil, time.Now())
	c.showNode(id, byBrain, false)
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

// handleReview is POST /e2e/review: the news watch's server changes its
// tool after the user added it; it is held, and « Outils à revoir » (made
// now, as at a start) lists the change and waits for the user
// (tools.spec.ts).
func (sc *scenario) handleReview(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	sc.tools.mu.Lock()
	s := sc.tools.veille
	sc.tools.mu.Unlock()
	s.RemoveTools("guetter")
	mcp.AddTool(s, guetter("Guette les nouvelles du métier. Nouveau : en envoie aussi un résumé chaque matin."), guetterRun)
	u := c.pool.get("veille")
	for deadline := time.Now().Add(10 * time.Second); !slices.Contains(u.heldTools(), "guetter"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			http.Error(rw, "the changed tool is not held", http.StatusInternalServerError)
			return
		}
	}
	id, err := c.ensureToolReview()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	c.live.wg.Wait()
	c.readNode(sc.ctx, id, []string{reviewSource}, time.Now())
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

// handleSections is POST /e2e/sections: a view of the mails to sort in
// two sections, its items noted into two groups as a skill would, with an
// action that deletes on the first (sections.spec.ts), written by the
// brain as any author, read once, and shown.
func (sc *scenario) handleSections(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	d, err := c.nodes.Create(nodestore.New{Title: "Mails en sections", Lifetime: nodefile.Standing, Author: byBrain, Body: "Les mails, triés.\n"})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	id := d.Node.Meta.ID
	body := "Les mails, triés.\n\n```eriphe:block tri\n" + `{"components": [
  {"id": "root", "component": "Column", "children": ["c1", "c2"]},
  {"id": "c1", "component": "Card", "title": "À supprimer", "child": "drop"},
  {"id": "drop", "component": "List", "children": {"path": "/tri/messages[_group=À supprimer]", "componentId": "row"}, "emptyText": "Rien à supprimer"},
  {"id": "c2", "component": "Card", "title": "Pour info", "child": "info"},
  {"id": "info", "component": "List", "children": {"path": "/tri/messages[_group=Pour info]", "componentId": "row"}},
  {"id": "row", "component": "Item", "title": {"@path": "subject"}, "detail": {"@path": "from"}}],
 "sources": {"tri": {"tool": "mail/tri", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}}},
 "actions": {"drop": {"tool": "mail/delete", "items": "ids", "source": "tri", "list": "drop"}}}` + "\n```\n"
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	for item, group := range map[string]string{"s1": "À supprimer", "s2": "Pour info", "s3": "À supprimer", "s4": "À supprimer", "s5": "Pour info"} {
		if err := c.noteItem(id, itemNote{Source: "tri", Item: item, Group: group, Author: byBrain}); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	c.readNode(sc.ctx, id, nil, time.Now())
	c.showNode(id, byBrain, false)
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

// handleOpen is POST /e2e/open: a view of two mails whose rows open in
// place, each read in full by mail/lire (sources-open.spec.ts), l1 noted in a
// group; written by the brain as any author, read once, and shown.
func (sc *scenario) handleOpen(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	d, err := c.nodes.Create(nodestore.New{Title: "Mails à lire", Lifetime: nodefile.Standing, Author: byBrain, Body: "Les mails.\n"})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	id := d.Node.Meta.ID
	body := "Les mails.\n\n```eriphe:block lus\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/lus/messages", "componentId": "row"}},
  {"id": "row", "component": "Item", "title": {"@path": "subject"}, "detail": {"@path": "from"}}],
 "sources": {"lus": {"tool": "mail/lus", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from", "open": {"tool": "mail/lire", "id": "id"}}}}}` + "\n```\n"
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.noteItem(id, itemNote{Source: "lus", Item: "l1", Group: "À traiter", Note: "Sam attend ton avis", Author: byBrain}); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	c.readNode(sc.ctx, id, nil, time.Now())
	c.showNode(id, byBrain, false)
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

// handleBilan is POST /e2e/bilan: a view of the mailbox as a bilan
// (view-bilan.spec.ts), as its skill leaves it: the bilan on top, one block
// that reads the mails of two accounts once, « À traiter » with a reply
// drafted for Sam waiting on its row, « À jeter » as a batch whose trash
// takes each mail's account, « Pour info » folded. Written after an
// external read, as a skill that read mails writes it.
func (sc *scenario) handleBilan(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	lead := "1 réponse attendue : Sam, avant vendredi. 1 hameçonnage dans le lot à jeter.\n\n**Pour info** · La facture d'électricité est arrivée (62 €, prélevés le 5 décembre). Le club avance l'entraînement de samedi à 9 h.\n"
	d, err := c.nodes.Create(nodestore.New{Title: "Mes mails du jour", Lifetime: nodefile.Standing, Author: byBrain, Body: lead})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	id := d.Node.Meta.ID
	body := lead + "\n```eriphe:block boite\n" + `{"components": [
  {"id": "root", "component": "Column", "children": ["todo_h", "todo", "trash", "info"]},
  {"id": "todo_h", "component": "Text", "variant": "subtitle", "text": "À traiter"},
  {"id": "todo", "component": "List", "children": {"path": "/mails/messages[_group=À traiter][_state!=done]", "componentId": "todo_row"}, "emptyText": "Rien ne t'attend."},
  {"id": "todo_row", "component": "Row", "align": "center", "children": ["todo_item", "todo_yes"]},
  {"id": "todo_item", "component": "Item", "title": {"@path": "_note"}, "detail": {"@path": "from"}, "note": {"@path": "subject"}},
  {"id": "todo_yes", "component": "Proposal", "inline": true, "proposalId": {"@path": "_card"}},
  {"id": "trash", "component": "Fold", "list": "jeter", "label": "{n} mails inutiles à jeter", "child": "jeter"},
  {"id": "jeter", "component": "List", "children": {"path": "/mails/messages[_group=À jeter]", "componentId": "jeter_row"}},
  {"id": "jeter_row", "component": "Item", "level": "low", "title": {"@path": "subject"}, "detail": {"@path": "from"}, "note": {"@path": "_note"}},
  {"id": "info", "component": "Fold", "list": "infos", "label": "Les {n} mails pour info", "child": "infos"},
  {"id": "infos", "component": "List", "children": {"path": "/mails/messages[_group=Pour info]", "componentId": "info_row"}},
  {"id": "info_row", "component": "Item", "level": "low", "title": {"@path": "subject"}, "detail": {"@path": "from"}, "note": {"@path": "_note"}}],
 "sources": {"mails": {"tool": "mail/bilan", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from", "open": {"tool": "mail/lettre", "id": "id"}}}},
 "actions": {"jeter": {"label": "Jeter", "tool": "mail/trash", "args": {"account": {"@item": "account"}}, "items": "ids", "source": "mails", "list": "jeter"}}}` + "\n```\n"
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, true, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	for item, n := range bilanNotes {
		if err := c.noteItem(id, itemNote{Source: "mails", Item: item, Group: n[0], Note: n[1], State: "act", Author: byBrain}); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// The reply its skill drafted for Sam: a card on the mail's row.
	raw, _ := json.Marshal(map[string]any{"id": "a1", "body": bilanReply})
	labels, _ := json.Marshal([]itemLabel{{ID: "a1", Title: "Devis terrasse", Detail: "Sam"}})
	res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, server, category, call, input, tainted, created, node, source, item, labels)
		VALUES ('t-brain', 'mail', 'send', 'mcp__mail__repondre []', ?, 'external:mail', ?, ?, 'mails', 'a1', ?)`, string(raw), time.Now().Unix(), id, string(labels))
	ask, err := rowsID(res, err)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, ask, ask)
	c.cardNow(cardCalls, ask, "", refNode+":"+id)
	c.syncCards()
	c.readNode(sc.ctx, id, nil, time.Now())
	c.showNode(id, byBrain, false)
	c.bump()
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

// handleView is POST /e2e/view: the workspace as a spec begins it, whatever
// the one before left open (its pages stay open in the view kept here, the
// next spec's beside them): no page open, the bar at the home, Eriphe's
// last show already seen, so that no fresh show opens its page again.
func (sc *scenario) handleView(rw http.ResponseWriter, r *http.Request) {
	last, _ := sc.c.lastShown()
	raw, _ := json.Marshal(espaceView{Open: []string{}, Shown: int(last.Seq)})
	if err := sc.c.store.set(espaceViewKey, string(raw)); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// handlePurge is POST /e2e/purge: a view of a mailbox (mail/promos), read
// once, every five minutes after, and shown; purger allowed for good, as
// the user would have (view-refresh.spec.ts).
func (sc *scenario) handlePurge(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	d, err := c.nodes.Create(nodestore.New{Title: "Boîte à nettoyer", Lifetime: nodefile.Standing, Author: byBrain, Body: "La boîte.\n"})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	id := d.Node.Meta.ID
	body := "La boîte.\n\n```eriphe:block promos\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/promos/messages", "componentId": "row"}},
  {"id": "row", "component": "Item", "title": {"@path": "subject"}, "detail": {"@path": "from"}}],
 "sources": {"promos": {"tool": "mail/promos", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}}}}` + "\n```\n"
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, false, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.store.allowAction("mail", claudeToolName("mail", "purger"), ""); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	c.readNode(sc.ctx, id, nil, time.Now())
	c.showNode(id, byBrain, false)
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

// handleConso is POST /e2e/conso?kw=…: the house's consumption is now kw,
// and the node that watches it reads it again (view-glance.spec.ts), as its
// period or its server's change would.
func (sc *scenario) handleConso(rw http.ResponseWriter, r *http.Request) {
	kw, err := strconv.ParseFloat(r.URL.Query().Get("kw"), 64)
	if err != nil {
		http.Error(rw, "kw", http.StatusBadRequest)
		return
	}
	sc.tools.mu.Lock()
	sc.tools.kw = kw
	sc.tools.mu.Unlock()
	sc.mu.Lock()
	id := sc.nodes["conso"]
	sc.mu.Unlock()
	if id == "" {
		http.Error(rw, "no glance yet", http.StatusConflict)
		return
	}
	sc.c.readNode(sc.ctx, id, []string{"conso"}, time.Now()) // named: read now, due or not
	writeJSON(rw, http.StatusOK, map[string]any{"id": id, "glance": sc.c.nodeGlance(id)})
}

// handleCourrier is POST /e2e/courrier: a view of the mailbox, a plain
// List of its mails (view-calls.spec.ts), shown; the brain's call on two
// of its rows waits on them.
func (sc *scenario) handleCourrier(rw http.ResponseWriter, r *http.Request) {
	c := sc.c
	d, err := c.nodes.Create(nodestore.New{Title: "Courrier", Lifetime: nodefile.Standing, Author: byBrain, Body: "Le courrier du jour.\n"})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	id := d.Node.Meta.ID
	body := "Le courrier du jour.\n\n```eriphe:block courrier\n" + `{"components": [
  {"id": "root", "component": "List", "children": {"path": "/mails/messages", "componentId": "row"}},
  {"id": "row", "component": "Item", "title": {"@path": "subject"}, "detail": {"@path": "from"}}],
 "sources": {"mails": {"tool": "mail/courrier", "items": {"path": "messages", "id": "id", "title": "subject", "detail": "from"}}}}` + "\n```\n"
	if _, err := c.authorWrite(id, d.Version.Hash, byBrain, true, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(body) }); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	c.readNode(sc.ctx, id, nil, time.Now())
	c.showNode(id, byBrain, false)
	c.bump()
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
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
// the mails, sorted, with a draft, waiting for the user; opened by the
// skill's run, as its task's node_open records it (taskOrigin).
func (sc *scenario) mail() (string, error) {
	c := sc.c
	d, err := c.nodes.Create(nodestore.New{Title: "Urgent : la prod renvoie des 500", Parent: sc.nodes["mails"], External: true, Author: "skill:mails",
		OpenedBy: &nodefile.Origin{By: nodefile.OpenedSkill, Skill: "mails-trier", On: "monitor:mails-recus", Node: sc.nodes["mails"]},
		Body:     "**Tri** : urgent. Sam (sam@example.org) écrit que l'app de production renvoie des erreurs 500 depuis 10 h.\n\n**Brouillon** : À écrire.\n"})
	if err != nil {
		return "", err
	}
	sc.mu.Lock()
	sc.nodes["mail"] = d.Node.Meta.ID
	sc.mu.Unlock()
	if err := fakeStatus(c, d.Node.Meta.ID, nodeWaits); err != nil {
		return "", err
	}
	return d.Node.Meta.ID, nil
}

// nodeAsks tells a node whose document has a button that asks Eriphe
// (a block's ask): its questions come with their answers.
func nodeAsks(c *core, id string) bool {
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return false
	}
	for _, b := range d.Node.Blocks() {
		var list []struct {
			Action struct {
				Event struct {
					Name string `json:"name"`
				} `json:"event"`
			} `json:"action"`
		}
		json.Unmarshal(b.Components, &list)
		for _, comp := range list {
			if comp.Action.Event.Name == "ask" {
				return true
			}
		}
	}
	return false
}

// prodNotes is the Production page's long document: paragraphs and a
// table, so that it is taller than the screen holds.
var prodNotes = strings.Repeat("Le service répond lentement depuis la mise à jour de ce matin ; les erreurs viennent surtout de la file des paiements, qui sature aux heures de pointe.\n\n", 6) +
	"| Service | Erreurs | Latence |\n|---|---|---|\n| Paiements | 31 | 900 ms |\n| Comptes | 7 | 300 ms |\n| Recherche | 4 | 250 ms |\n| Notifications | 0 | 120 ms |\n\n" +
	strings.Repeat("À suivre : la file des paiements, la base des comptes et le cache de la recherche, un par un, en notant ce qui change.\n\n", 6)
