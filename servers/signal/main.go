// Command eriphe-signal is Eriphe's own Signal account: signal-cli, run in
// JSON-RPC mode, behind setup tools that eriphe-core's Signal channel calls
// to register the number, send and receive.
package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	transport := flag.String("transport", env("MCP_TRANSPORT", "stdio"), "stdio or http")
	addr := flag.String("addr", env("MCP_ADDR", ":8080"), "listen address for http")
	bin := flag.String("signal-cli", env("SIGNAL_CLI", "signal-cli"), "the signal-cli binary")
	data := flag.String("data", env("SIGNAL_DATA", "/data"), "where signal-cli keeps the account and its keys")
	flag.Parse()
	log.SetOutput(os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	box := newInbox()
	c := newCLI(execStarter(*bin, *data), box.add)
	if _, err := exec.LookPath(*bin); err != nil {
		c.missing = fmt.Sprintf("signal-cli is not in this image for %s: eriphe-signal runs on an amd64 machine only", runtime.GOARCH)
	}
	go c.run(ctx)
	server := newServer(&signalServer{cli: c, inbox: box}, version)

	switch *transport {
	case "stdio":
		log.Printf("eriphe-signal %s on stdio", version)
		if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			log.Fatal(err)
		}
	case "http":
		token, err := bearer()
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("eriphe-signal %s on %s", version, *addr)
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
