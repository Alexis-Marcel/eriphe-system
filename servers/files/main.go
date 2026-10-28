// Command eriphe-files is an MCP server for the folders the user gives to
// Eriphe in their cloud storage: Google Drive now, Dropbox and OneDrive
// later behind the same tools. It reaches only the folders it is
// configured with and what is inside them, reads files (Google documents
// exported to text), and adds new files to a writable folder's Eriphe/
// subfolder; it never modifies or deletes the user's files. Its OAuth
// token is lent by eriphe-core, which ran the authorization.
package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	transport := flag.String("transport", env("MCP_TRANSPORT", "stdio"), "stdio or http")
	addr := flag.String("addr", env("MCP_ADDR", ":8080"), "listen address for http")
	configFile := flag.String("config", os.Getenv("FILES_CONFIG_FILE"), "configuration file (JSON); FILES_CONFIG holds it inline")
	flag.Parse()
	log.SetOutput(os.Stderr)

	cfg, err := LoadConfig(os.Getenv("FILES_CONFIG"), *configFile)
	if err != nil {
		log.Fatal(err)
	}
	f, err := NewFiles(cfg)
	if err != nil {
		log.Fatal(err)
	}
	server := newServer(f, version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch *transport {
	case "stdio":
		log.Printf("eriphe-files %s on stdio, %d account(s), %d folder(s)", version, len(f.accounts), len(f.folders))
		if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			log.Fatal(err)
		}
	case "http":
		token, err := bearer()
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("eriphe-files %s on %s, %d account(s), %d folder(s)", version, *addr, len(f.accounts), len(f.folders))
		if err := serveHTTP(ctx, *addr, token, server); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown transport %q: stdio or http", *transport)
	}
}

// bearer is the optional token required on HTTP: MCP_TOKEN or MCP_TOKEN_FILE.
func bearer() (string, error) {
	if f := os.Getenv("MCP_TOKEN_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return strings.TrimSpace(string(b)), err
	}
	return os.Getenv("MCP_TOKEN"), nil
}

func serveHTTP(ctx context.Context, addr, token string, server *mcp.Server) error {
	mux := http.NewServeMux()
	mux.Handle("/mcp", auth(token, mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true},
	)))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// auth requires the bearer token when one is set.
func auth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
