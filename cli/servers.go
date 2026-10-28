package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

// Server is a catalog server as eriphe-core lists it.
type Server struct {
	Name        string `json:"name"`
	Use         string `json:"use"`
	Kind        string `json:"kind"`
	Source      string `json:"source,omitempty"` // config (the file) or workspace
	URL         string `json:"url,omitempty"`
	Auth        string `json:"auth"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	Connect     string `json:"connect,omitempty"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	ToolCount   int    `json:"tool_count"`
}

// Servers lists the tool catalog.
func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	var out []Server
	err := c.call(ctx, http.MethodGet, "/v1/servers", shortTimeout, nil, &out)
	return out, err
}

// connectStart is eriphe-core's answer to a connect: a flow and the URL to
// open, or a status if the server needed no authorization.
type connectStart struct {
	Flow    string `json:"flow"`
	AuthURL string `json:"auth_url"`
	Status  string `json:"status"`
}

// StartConnect asks eriphe-core for an authorization flow, redirected to
// redirect.
func (c *Client) StartConnect(ctx context.Context, name, redirect string) (*connectStart, error) {
	var out connectStart
	err := c.call(ctx, http.MethodPost, "/v1/servers/"+url.PathEscape(name)+"/connect", 2*time.Minute,
		map[string]string{"redirect_uri": redirect}, &out)
	return &out, err
}

// connectResult is how the server is once the redirect is handed over.
type connectResult struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Tools  int    `json:"tools"`
}

// FinishConnect hands the redirect's parameters over to eriphe-core.
func (c *Client) FinishConnect(ctx context.Context, name, flow string, q url.Values) (*connectResult, error) {
	in := map[string]string{"flow": flow}
	for _, k := range []string{"code", "state", "iss", "error", "error_description"} {
		if v := q.Get(k); v != "" {
			in[k] = v
		}
	}
	var out connectResult
	err := c.call(ctx, http.MethodPost, "/v1/servers/"+url.PathEscape(name)+"/callback", 3*time.Minute, in, &out)
	return &out, err
}

func serversCommand(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: eriphe servers")
	}
	c, err := client()
	if err != nil {
		return err
	}
	servers, err := c.Servers(ctx)
	if err != nil {
		return err
	}
	printServers(os.Stdout, servers)
	return nil
}

// printServers writes the catalog as an aligned table, then what needs the user.
func printServers(w io.Writer, servers []Server) {
	if len(servers) == 0 {
		fmt.Fprintln(w, "No servers in the catalog.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tAUTH\tTOOLS\tSOURCE\tUSE")
	for _, s := range servers {
		source := s.Source
		if source == "" {
			source = "config" // an eriphe-core without a workspace catalog
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", s.Name, s.Status, s.Auth, s.ToolCount, source, truncate(oneLine(s.Use), 50))
	}
	tw.Flush()
	for _, s := range servers {
		switch {
		case s.Connect != "":
			fmt.Fprintf(w, "\n%s needs an authorization (%s): run `%s`.", s.Name, s.Error, s.Connect)
		case s.Status == "error":
			fmt.Fprintf(w, "\n%s: %s", s.Name, s.Error)
		}
	}
	fmt.Fprintln(w)
}

func connectCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "print the URL without opening a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: eriphe connect [-no-browser] <server>")
	}
	c, err := client()
	if err != nil {
		return err
	}
	open := openBrowser
	if *noBrowser {
		open = func(string) error { return errors.New("not opened") }
	}
	return connect(ctx, c, os.Stdout, fs.Arg(0), open, os.Stdin)
}

// connect authorizes eriphe-core to reach an OAuth server: it receives the
// redirect on a loopback port (allowed for native apps), or, if the browser
// is on another machine, the redirected URL pasted on stdin.
func connect(ctx context.Context, c *Client, w io.Writer, name string, open func(string) error, stdin io.Reader) error {
	w = &lockedWriter{w: w} // the paste reader writes too
	servers, err := c.Servers(ctx)
	if err != nil {
		return err
	}
	var srv *Server
	for i := range servers {
		if servers[i].Name == name {
			srv = &servers[i]
		}
	}
	if srv == nil {
		return fmt.Errorf("no server %q in the catalog (see eriphe servers)", name)
	}
	if srv.Auth != "oauth" {
		return fmt.Errorf("%s does not use OAuth (%s): nothing to connect", name, srv.Auth)
	}
	// The port of the registered redirect if there is one: the client is
	// registered for it. Otherwise any free port.
	addr := "127.0.0.1:0"
	if u, err := url.Parse(srv.RedirectURI); err == nil && u.Port() != "" {
		addr = "127.0.0.1:" + u.Port()
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil && addr != "127.0.0.1:0" {
		fmt.Fprintf(w, "Port %s is busy: using another one (the server may register Eriphe again).\n", addr)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return err
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	start, err := c.StartConnect(ctx, name, redirect)
	if err != nil {
		return err
	}
	if start.AuthURL == "" {
		fmt.Fprintf(w, "%s is %s: no authorization needed.\n", name, start.Status)
		return nil
	}

	got := make(chan url.Values, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code") == "" && q.Get("error") == "" {
			http.Error(rw, "no authorization code", http.StatusBadRequest)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(rw, "<!doctype html><title>Eriphe</title><p>Eriphe has the authorization. You can close this tab.</p>")
		select {
		case got <- q:
		default:
		}
	})
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go hs.Serve(ln)
	defer hs.Close()
	go func() {
		// The fallback: the URL the browser was sent to, pasted here.
		sc := bufio.NewScanner(stdin)
		for sc.Scan() {
			if q, ok := redirectParams(sc.Text()); ok {
				got <- q
				return
			}
			if strings.TrimSpace(sc.Text()) != "" {
				fmt.Fprintln(w, "That is not the redirected URL (it holds code= and state=).")
			}
		}
	}()

	fmt.Fprintf(w, "Authorize Eriphe to use %s in your browser:\n\n  %s\n\n", name, start.AuthURL)
	if err := open(start.AuthURL); err == nil {
		fmt.Fprintln(w, "(Opened in your browser.)")
	}
	fmt.Fprintf(w, "Waiting for the redirect to %s.\nIf your browser is on another machine, paste here the URL it ended on.\n", redirect)

	var q url.Values
	select {
	case q = <-got:
	case <-time.After(10 * time.Minute):
		return errors.New("no authorization within 10 minutes")
	case <-ctx.Done():
		return ctx.Err()
	}
	res, err := c.FinishConnect(ctx, name, start.Flow, q)
	if err != nil {
		return err
	}
	if res.Status != "ready" {
		return fmt.Errorf("%s is %s: %s", name, res.Status, res.Error)
	}
	fmt.Fprintf(w, "%s is connected: %d tools.\n", name, res.Tools)
	return nil
}

// redirectParams reads a pasted redirect URL.
func redirectParams(s string) (url.Values, bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return nil, false
	}
	q := u.Query()
	return q, (q.Get("code") != "" && q.Get("state") != "") || q.Get("error") != ""
}

func openBrowser(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		return exec.Command("xdg-open", u).Start()
	}
}

// lockedWriter serializes writes from several goroutines.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
