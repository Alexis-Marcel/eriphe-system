// eriphe-core is Eriphe's control plane: it keeps the state of nodes and
// tasks, schedules tasks on nodes, follows them, lends identities, and gives
// the brain its tools over MCP.
//
//	eriphe-core -data /data                                      # no file: defaults, files in /data
//	eriphe-core -config core.json
//	eriphe-core join-token -config core.json -name pc            # prints how to join a new node
//	eriphe-core join-token -config core.json -name pc -rejoin    # lets node pc join again
//	eriphe-core join-token -data /data -name pc -quiet           # prints the token alone
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // the calendar form checks time zones; the image has no zoneinfo

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time (the image: X.Y.Z for a release,
// X.Y.Z-dev+<commit> for a build of main, see the Dockerfile and the
// README's "Versions").
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "join-token" {
		joinTokenCommand(os.Args[2:])
		return
	}
	path := flag.String("config", "", "configuration file (without one, defaults in -data)")
	data := flag.String("data", envOr("ERIPHE_DATA", defaultData), "where eriphe-core keeps its files without a configuration file (env ERIPHE_DATA)")
	flag.Parse()
	cfg, err := loadOrDefault(*path, *data)
	if err != nil {
		log.Fatal(err)
	}
	// The container runs in UTC: every time shown to me or the brain is in my zone.
	time.Local = cfg.location
	log.Printf("time zone: %s", cfg.TimeZone)
	store, err := openStore(cfg.DB)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	// The vault first: every secret of the configuration may be in it.
	if err := startVault(cfg, store); err != nil {
		log.Printf("vault: %v", err)
	}
	token, err := readSecret(cfg.TokenFile)
	if err != nil {
		log.Fatalf("token: %v", err)
	}
	c := newCore(cfg, store)
	c.token = token
	c.startMachineTLS() // machine_tls.go
	// Eriphe's memory, in its own file: without it, the rest still works.
	if c.mem, err = openMemory(cfg.memoryConfig().DB); err != nil {
		log.Printf("memory: %v", err)
	}
	if err := c.registerServices(c.conf().runSpecs); err != nil {
		log.Fatalf("services: %v", err)
	}
	c.run(context.Background())
	c.pool.run(context.Background())

	server := newMCPServer(c)
	c.self = server
	mux := http.NewServeMux()
	mux.Handle("/mcp", bearer(token, mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true},
	)))
	mux.Handle("POST /v1/messages", bearer(token, http.HandlerFunc(c.handlePostMessage)))
	mux.Handle("GET /v1/messages", bearer(token, http.HandlerFunc(c.handleGetMessages)))
	mux.Handle("POST /v1/voice", http.HandlerFunc(c.handleVoice)) // Home Assistant's own token (voice.go)
	mux.Handle("GET /v1/voice", http.HandlerFunc(c.handleVoiceInfo))
	c.taskRoutes(mux, token)
	c.serverRoutes(mux, token)
	c.catalogRoutes(mux, token)
	c.assistantRoutes(mux, token) // assistants.go
	c.vaultRoutes(mux, token)
	c.folderRoutes(mux, token)     // folders.go
	c.nodeAccessRoutes(mux, token) // node_access.go
	mux.Handle("POST /v1/join-tokens", bearer(token, http.HandlerFunc(c.handleJoinToken)))
	mux.Handle("GET /v1/nodes", bearer(token, http.HandlerFunc(c.handleNodes)))
	mux.Handle("GET /v1/version", bearer(token, http.HandlerFunc(c.handleVersion)))
	mux.Handle("DELETE /v1/nodes/{name}", bearer(token, http.HandlerFunc(c.handleDeleteNode)))
	mux.Handle("GET /v1/nodes/tls", bearer(token, http.HandlerFunc(c.handleNodesTLS)))
	mux.Handle("PUT /v1/nodes/tls", bearer(token, http.HandlerFunc(c.handleNodesTLS)))
	// Nodes authenticate with their join token, then with their own secret.
	mux.HandleFunc("POST /v1/nodes/join", c.handleJoin)
	mux.HandleFunc("GET /v1/nodes/connect", c.handleConnect)
	mux.HandleFunc("GET /v1/node/assignments", c.handleAssignments)
	mux.HandleFunc("POST /v1/node/tasks/{id}/report", c.handleReport)
	mux.Handle("/v1/node/tasks/{id}/mcp/{server}", c.taskMCPHandler())
	mux.HandleFunc("POST /v1/node/tasks/{id}/credential", c.handleTaskCredential) // credentials.go
	mux.HandleFunc("POST /v1/node/services/{name}/report", c.handleServiceReport)
	mux.HandleFunc("POST /v1/node/certificate", c.handleNodeCertificate)
	mux.Handle("GET /v1/services", bearer(token, http.HandlerFunc(c.handleServices)))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	// Remote access over the tailnet (remote.go): the same API, and the workspace.
	c.remote = newRemoteAccess(store, cfg.DB, mux)
	if c.machines != nil {
		c.remote.clientCAs = c.machines.pool
	}
	go c.runAssistants(context.Background(), token)
	go c.runMemory(context.Background())
	go c.updates.run(context.Background())
	if cfg.Web != nil {
		go func() { log.Fatalf("web: %v", c.serveWeb(cfg)) }()
	}

	log.Printf("eriphe-core %s listening on %s", version, cfg.Listen)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(c.apiListener(ln)))
}

// joinTokenCommand asks the running eriphe-core for a join token and prints
// the command that joins a node with it.
func joinTokenCommand(args []string) {
	fs := flag.NewFlagSet("join-token", flag.ExitOnError)
	path := fs.String("config", "", "configuration file (without one, defaults in -data)")
	data := fs.String("data", envOr("ERIPHE_DATA", defaultData), "eriphe-core's data directory, without a configuration file (env ERIPHE_DATA)")
	ttl := fs.Duration("ttl", 24*time.Hour, "how long the token is valid")
	name := fs.String("name", "", "name of the node the token is for (required)")
	rejoin := fs.Bool("rejoin", false, "let the node already registered under -name join again, with a new secret")
	quiet := fs.Bool("quiet", false, "print the token alone, for a script")
	fs.Parse(args)
	if !nodeName.MatchString(*name) {
		log.Fatal("give the node's name: join-token -name pc (lowercase letters, digits, dashes)")
	}
	cfg, err := loadOrDefault(*path, *data)
	if err != nil {
		log.Fatal(err)
	}
	if strings.HasPrefix(cfg.TokenFile, vaultPrefix) {
		store, err := openStore(cfg.DB)
		if err != nil {
			log.Fatalf("store: %v", err)
		}
		if err := startVault(cfg, store); err != nil {
			log.Fatalf("vault: %v", err)
		}
	}
	token, err := readSecret(cfg.TokenFile)
	if err != nil {
		log.Fatalf("token: %v", err)
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+"/v1/join-tokens?"+joinQuery(*name, *rejoin, *ttl), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Token   string    `json:"token"`
		Expires time.Time `json:"expires"`
		CAHash  string    `json:"ca_hash"`
	}
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		log.Fatalf("eriphe-core answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Fatal(err)
	}
	if *quiet {
		fmt.Println(out.Token)
		return
	}
	url := cfg.PublicURL
	if url == "" {
		url = "http://<this machine>:" + port
	}
	where := "On the new machine:"
	if *rejoin {
		where = "On the machine, after deleting node.json in the node's root:"
	}
	pin := ""
	if out.CAHash != "" {
		pin = " -ca-hash " + out.CAHash
	}
	fmt.Printf("Join token for %s, single use, valid until %s:\n\n  %s\n\n%s\n\n  eriphe-node -core %s -join-token %s -name %s%s\n",
		*name, out.Expires.Local().Format("2006-01-02 15:04"), out.Token, where, url, out.Token, *name, pin)
}

func joinQuery(name string, rejoin bool, ttl time.Duration) string {
	q := neturl.Values{"name": {name}, "ttl": {ttl.String()}}
	if rejoin {
		q.Set("rejoin", "1")
	}
	return q.Encode()
}

func bearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
