package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// Cloud storage as a source of Folders: Google Drive now (Dropbox and
// OneDrive later), served to tasks by eriphe-files (servers/files), a local
// server like eriphe-mail. eriphe-core runs the OAuth authorization, keeps
// the refresh token in the vault and lends it to the files service on its
// node, like a password: it never enters a task.
//
// Google wants a registered https redirect, and each installation has its
// own address: the Eriphe app, published once, registers
// https://eriphe.com/oauth/callback, and the portal there only bounces the
// browser to the workspace named in the state (the Home Assistant way):
//
//	workspace → Google (PKCE, state = e1.<payload>.<mac>)
//	Google → https://eriphe.com/oauth/callback?code&state
//	portal → <workspace>/oauth/callback?code&state   (nothing else, nothing kept)
//	workspace: checks the state's MAC, the flow and the browser's cookie,
//	           exchanges the code with the client secret and the PKCE verifier
//
// The state's payload is the workspace's origin, a random flow id and an
// expiry, signed with HMAC-SHA256 under a key only this eriphe-core has: a
// state it did not make, or one replayed, is refused here. The portal can
// read the payload but not check the MAC (no shared key could stay secret
// in a public image): it holds the origin to the shape of a workspace
// address instead (bounceOrigin), the same rule as here, and forwards only
// code and state.
//
// "Use my own Google client" skips the portal: the client id and secret
// typed in the workspace, registered at Google with the workspace's own
// https://<workspace>/oauth/callback.

const (
	filesServer    = "fichiers"
	filesImage     = "ghcr.io/alexis-marcel/eriphe-files"
	filesConfigEnv = "FILES_CONFIG"
	driveTokenEnv  = "FILES_GOOGLE_TOKEN"
	driveAccount   = "google"

	defaultBounce   = "https://eriphe.com/oauth/callback"
	googleAppFile   = "google-oauth.json" // next to the database, by default
	stateVersion    = "e1"
	stateKeyName    = "cloud_state_key"
	cloudFlowExpiry = 15 * time.Minute

	scopeDrive         = "https://www.googleapis.com/auth/drive"
	scopeDriveReadonly = "https://www.googleapis.com/auth/drive.readonly"
)

// googleOAuth are Google's endpoints; tests replace them.
var googleOAuth = oauth2.Endpoint{
	AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:  "https://oauth2.googleapis.com/token",
	AuthStyle: oauth2.AuthStyleInParams,
}

// cloudHTTP exchanges codes; tests replace it.
var cloudHTTP = &http.Client{Timeout: 30 * time.Second}

// CloudConfig is where the Eriphe app's OAuth client is. Its secret is
// never in the repository or in an image: the file is given to the
// installation (see core/README.md, "Google Drive").
type CloudConfig struct {
	// GoogleClientFile is the client JSON the Google Cloud console
	// downloads ({"web": {"client_id", "client_secret", ...}}), or
	// vault:<name>. Default google-oauth.json next to the database.
	GoogleClientFile string `json:"google_client_file,omitempty"`
	// BounceURL is the redirect registered for that client, which bounces
	// to the workspace: https://eriphe.com/oauth/callback.
	BounceURL string `json:"bounce_url,omitempty"`
}

type oauthApp struct {
	ID     string `json:"client_id"`
	Secret string `json:"client_secret"`
}

// googleApp reads the Eriphe app's client.
func (c *core) googleApp() (oauthApp, error) {
	file := ""
	if cc := c.base.Cloud; cc != nil {
		file = cc.GoogleClientFile
	}
	if file == "" {
		file = filepath.Join(filepath.Dir(c.base.DB), googleAppFile)
	}
	raw, err := readSecret(file)
	if err != nil {
		return oauthApp{}, err
	}
	var doc struct {
		Web       *oauthApp `json:"web"`
		Installed *oauthApp `json:"installed"`
		oauthApp
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return oauthApp{}, fmt.Errorf("%s: not a client JSON", file)
	}
	app := doc.oauthApp
	if doc.Web != nil {
		app = *doc.Web
	} else if doc.Installed != nil {
		app = *doc.Installed
	}
	if app.ID == "" || app.Secret == "" {
		return oauthApp{}, fmt.Errorf("%s: client_id and client_secret are required", file)
	}
	return app, nil
}

func (c *core) bounceURL() string {
	if cc := c.base.Cloud; cc != nil && cc.BounceURL != "" {
		return cc.BounceURL
	}
	return defaultBounce
}

// The state.

type statePayload struct {
	Origin  string `json:"w"` // the workspace, scheme://host[:port]
	Nonce   string `json:"n"` // the flow
	Expires int64  `json:"x"` // unix seconds
}

// stateKey is this installation's key for the states, made once.
func (c *core) stateKey() []byte {
	c.cloud.mu.Lock()
	defer c.cloud.mu.Unlock()
	if k, err := hex.DecodeString(c.store.get(stateKeyName)); err == nil && len(k) == 32 {
		return k
	}
	k := make([]byte, 32)
	rand.Read(k)
	c.store.set(stateKeyName, hex.EncodeToString(k))
	return k
}

func signState(key []byte, p statePayload) string {
	raw, _ := json.Marshal(p)
	body := stateVersion + "." + base64.RawURLEncoding.EncodeToString(raw)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

var errBadState = errors.New("this authorization is not valid any more: connect again")

// openState checks a state's MAC and expiry and reads it.
func openState(key []byte, s string, now time.Time) (statePayload, error) {
	parts := strings.Split(s, ".")
	if len(s) > 1024 || len(parts) != 3 || parts[0] != stateVersion {
		return statePayload{}, errBadState
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[2])
	m := hmac.New(sha256.New, key)
	m.Write([]byte(parts[0] + "." + parts[1]))
	if err != nil || !hmac.Equal(mac, m.Sum(nil)) {
		return statePayload{}, errBadState
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var p statePayload
	if json.Unmarshal(raw, &p) != nil || now.Unix() > p.Expires {
		return statePayload{}, errBadState
	}
	return p, nil
}

// isCloudState tells a state of ours from a state of the go-sdk's flows.
func isCloudState(s string) bool { return strings.HasPrefix(s, stateVersion+".") }

// bounceOrigin is the rule the portal applies to the address it bounces
// to (eriphe/src/lib/server/oauth.ts, the same rule): the origin of a
// workspace, which is on the home network or the tailnet. https to a
// private, loopback or Tailscale address, or to a local name (one label,
// .local, .lan, .home, .internal, .home.arpa, .localdomain, .localhost) or a
// tailnet name (.ts.net); http only to a loopback or private address.
func bounceOrigin(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		len(s) > 255 || s != strings.ToLower(s) {
		return false
	}
	host := u.Hostname()
	if host == "" || (u.Port() != "" && !portRe.MatchString(u.Port())) || u.Port() == map[string]string{"https": "443", "http": "80"}[u.Scheme] {
		return false
	}
	ip, err := netip.ParseAddr(host)
	isIP := err == nil
	private := isIP && (ip.Unmap().IsLoopback() || ip.Unmap().IsPrivate() || ip.Unmap().IsLinkLocalUnicast() || cgnat.Contains(ip.Unmap()))
	switch u.Scheme {
	case "http":
		return private || host == "localhost"
	case "https":
		if isIP {
			return private
		}
		if !hostRe.MatchString(host) || numericHost.MatchString(host) {
			return false
		}
		if !strings.Contains(host, ".") {
			return true
		}
		for _, suffix := range []string{".local", ".lan", ".home", ".internal", ".home.arpa", ".localdomain", ".localhost", ".ts.net"} {
			if strings.HasSuffix(host, suffix) {
				return true
			}
		}
	}
	return false
}

var (
	portRe = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)
	hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	// A name a browser reads as an IPv4 address (2130706433, 0x7f000001).
	numericHost = regexp.MustCompile(`^(0x[0-9a-f]*|[0-9]+)$`)
)

// The flows.

type cloudFlow struct {
	origin   string
	verifier string
	binding  string // hash of the browser's flow cookie
	config   *oauth2.Config
	own      bool // the user's own client, stored with the token
	lang     string
	expires  time.Time
}

type cloudFlows struct {
	mu    sync.Mutex
	flows map[string]*cloudFlow
}

func (f *cloudFlows) put(id string, fl *cloudFlow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flows == nil {
		f.flows = map[string]*cloudFlow{}
	}
	for k, old := range f.flows {
		if time.Now().After(old.expires) {
			delete(f.flows, k)
		}
	}
	f.flows[id] = fl
}

// take hands a flow out once.
func (f *cloudFlows) take(id string) *cloudFlow {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.flows[id]
	delete(f.flows, id)
	if fl == nil || time.Now().After(fl.expires) {
		return nil
	}
	return fl
}

// driveConnect is what the workspace's Connect asks.
type driveConnect struct {
	Origin  string    // the workspace, as the browser reaches it
	Binding string    // hash of the browser's flow cookie
	Write   bool      // ask for the scope that also creates files
	Own     *oauthApp // the user's own client, or nil for the Eriphe app
	Lang    string    // the words of the tool's use, if it is created
}

// startDrive starts a Google authorization and returns where to send the
// browser.
func (c *core) startDrive(in driveConnect) (string, error) {
	app, redirect := oauthApp{}, ""
	if in.Own != nil {
		app, redirect = *in.Own, in.Origin+"/oauth/callback"
		if app.ID == "" || app.Secret == "" {
			return "", &catalogError{http.StatusBadRequest, "drive.ownmissing", "", errors.New("the client id and secret are required")}
		}
	} else {
		var err error
		if app, err = c.googleApp(); err != nil {
			return "", &catalogError{http.StatusServiceUnavailable, "drive.noapp", "", err}
		}
		if !bounceOrigin(in.Origin) {
			return "", &catalogError{http.StatusBadRequest, "drive.badorigin", in.Origin, errors.New("eriphe.com only bounces to a workspace on the home network or the tailnet")}
		}
		redirect = c.bounceURL()
	}
	scope := scopeDriveReadonly
	if in.Write {
		scope = scopeDrive
	}
	cfg := &oauth2.Config{ClientID: app.ID, ClientSecret: app.Secret, Endpoint: googleOAuth, RedirectURL: redirect, Scopes: []string{scope}}
	nonce := randomToken(18)
	fl := &cloudFlow{origin: in.Origin, verifier: oauth2.GenerateVerifier(), binding: in.Binding, config: cfg,
		own: in.Own != nil, lang: in.Lang, expires: time.Now().Add(cloudFlowExpiry)}
	c.cloud.put(nonce, fl)
	state := signState(c.stateKey(), statePayload{Origin: in.Origin, Nonce: nonce, Expires: fl.expires.Unix()})
	// prompt=consent: Google gives a refresh token at every consent, not only the first.
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(fl.verifier),
		oauth2.SetAuthURLParam("prompt", "consent")), nil
}

// finishDrive takes Google's answer, as the workspace's callback received
// it: it checks the state, the flow and the browser, exchanges the code and
// gives the token to the files server.
func (c *core) finishDrive(ctx context.Context, origin, state, code, errCode, binding string) error {
	p, err := openState(c.stateKey(), state, time.Now())
	if err != nil {
		return err
	}
	fl := c.cloud.take(p.Nonce)
	if fl == nil || fl.origin != p.Origin || p.Origin != origin ||
		subtle.ConstantTimeCompare([]byte(fl.binding), []byte(binding)) != 1 {
		return errBadState
	}
	if errCode != "" {
		if errCode == "access_denied" {
			return &catalogError{http.StatusBadRequest, "drive.denied", "", errors.New("access denied")}
		}
		return fmt.Errorf("Google refused: %s", errCode)
	}
	if code == "" {
		return errors.New("no code in the redirect")
	}
	tok, err := fl.config.Exchange(context.WithValue(ctx, oauth2.HTTPClient, cloudHTTP), code, oauth2.VerifierOption(fl.verifier))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return fmt.Errorf("Google refused the code: %s %s", re.ErrorCode, re.ErrorDescription)
		}
		return fmt.Errorf("exchanging the code: %w", err)
	}
	if tok.RefreshToken == "" {
		return errors.New("Google gave no refresh token: remove Eriphe's access at myaccount.google.com/connections, then connect again")
	}
	scopes := strings.Fields(fmt.Sprint(tok.Extra("scope")))
	if !slices.Contains(scopes, scopeDrive) && !slices.Contains(scopes, scopeDriveReadonly) {
		return &catalogError{http.StatusBadRequest, "drive.noscope", "", errors.New("the access to Google Drive was not granted")}
	}
	secret, _ := json.Marshal(map[string]string{"client_id": fl.config.ClientID, "client_secret": fl.config.ClientSecret,
		"refresh_token": tok.RefreshToken})
	return c.putDrive(fl.lang, func(fc *filesConfig) {
		fc.Accounts = []filesAccount{{Name: driveAccount, Provider: "google", Scopes: scopes}}
		fc.Accounts[0].Auth.TokenEnv = driveTokenEnv
		if !slices.Contains(scopes, scopeDrive) {
			for i := range fc.Folders {
				fc.Folders[i].Access = "read"
			}
		}
	}, credentials{"env:" + driveTokenEnv: string(secret)})
}

// The files server's configuration, as servers/files reads it.

type filesConfig struct {
	Accounts []filesAccount `json:"accounts"`
	Folders  []DriveFolder  `json:"folders,omitempty"`
}

type filesAccount struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Auth     struct {
		TokenEnv string `json:"token_env"`
	} `json:"auth"`
	Scopes []string `json:"scopes,omitempty"`
}

// DriveFolder is a folder of the cloud given to Eriphe: the Folder object's
// cloud source (eriphe-files enforces it).
type DriveFolder struct {
	Name    string `json:"name"`
	Account string `json:"account"`
	ID      string `json:"id"`
	Access  string `json:"access"` // read, or drop: also new files in its Eriphe/ subfolder
}

// driveState is the files server as the workspace shows it.
type driveState struct {
	Exists   bool // the server is in the catalog
	Mine     bool // … and it is the workspace's eriphe-files
	CanWrite bool // the account granted the scope that creates files
	Folders  []DriveFolder
}

func (c *core) driveState() driveState {
	srv, ok := c.conf().Servers[filesServer]
	if !ok {
		return driveState{}
	}
	fc, ok := filesConfigOf(srv)
	st := driveState{Exists: true, Mine: ok && c.source(kindServer, filesServer) == sourceWorkspace, Folders: fc.Folders}
	for _, a := range fc.Accounts {
		st.CanWrite = st.CanWrite || slices.Contains(a.Scopes, scopeDrive)
	}
	return st
}

func filesConfigOf(srv Server) (filesConfig, bool) {
	if !srv.local() {
		return filesConfig{}, false
	}
	spec, err := decodeRun(srv.Run)
	if err != nil || !strings.HasPrefix(spec.Image, filesImage) {
		return filesConfig{}, false
	}
	var fc filesConfig
	if json.Unmarshal([]byte(spec.Env[filesConfigEnv].Value), &fc) != nil {
		return filesConfig{}, false
	}
	return fc, true
}

// cloudFolders are the cloud Folders, for the Folder object.
func (c *core) cloudFolders() []DriveFolder { return c.driveState().Folders }

// putDrive changes the files server's configuration (and its token, with
// creds), creating the server the first time.
func (c *core) putDrive(lang string, change func(*filesConfig), creds credentials) error {
	srv, exists := c.conf().Servers[filesServer]
	var fc filesConfig
	spec := RunSpec{Image: filesImage, Transport: transportStdio}
	if exists {
		var ok bool
		if fc, ok = filesConfigOf(srv); !ok || c.source(kindServer, filesServer) != sourceWorkspace {
			return &catalogError{http.StatusConflict, "drive.taken", filesServer, errors.New("a tool named " + filesServer + " already exists")}
		}
		spec, _ = decodeRun(srv.Run)
	}
	change(&fc)
	if len(fc.Accounts) == 0 {
		return errors.New("Google Drive is not connected")
	}
	raw, _ := json.Marshal(fc)
	env := map[string]EnvValue{filesConfigEnv: {Value: string(raw)}}
	if v, ok := spec.Env[driveTokenEnv]; ok {
		env[driveTokenEnv] = v
	}
	spec.Env = env
	mode := putUpdate
	if !exists {
		mode = putCreate
		srv = Server{Use: tr(lang, "preset.gdrive.use"), TrustAnnotations: true}
	}
	srv.Run = marshalRun(spec)
	return c.putServer(mode, filesServer, srv, creds)
}

// addDriveFolder gives a folder to Eriphe, or changes its access.
func (c *core) addDriveFolder(id, name, access string) error {
	id, name = strings.TrimSpace(id), strings.Join(strings.Fields(name), " ")
	if id == "" || name == "" || len(name) > 120 || (access != "read" && access != "drop") {
		return &catalogError{http.StatusBadRequest, "drive.badfolder", "", errors.New("a folder needs an id, a name and an access")}
	}
	if access == "drop" && !c.driveState().CanWrite {
		return &catalogError{http.StatusBadRequest, "drive.readonly", "", errors.New("Google Drive was connected for reading only")}
	}
	return c.putDrive("", func(fc *filesConfig) {
		for i, f := range fc.Folders {
			if f.ID == id {
				fc.Folders[i].Access = access
				return
			}
		}
		final := name
		for n := 2; slices.ContainsFunc(fc.Folders, func(f DriveFolder) bool { return strings.EqualFold(f.Name, final) }); n++ {
			final = fmt.Sprintf("%s (%d)", name, n)
		}
		fc.Folders = append(fc.Folders, DriveFolder{Name: final, Account: driveAccount, ID: id, Access: access})
	}, nil)
}

func (c *core) removeDriveFolder(id string) error {
	return c.putDrive("", func(fc *filesConfig) {
		fc.Folders = slices.DeleteFunc(fc.Folders, func(f DriveFolder) bool { return f.ID == id })
	}, nil)
}

// browseDrive lists an account's folders through the files server's setup
// tool, which tasks never get.
func (c *core) browseDrive(ctx context.Context, parent, page string) ([]driveItem, string, error) {
	u := c.pool.get(filesServer)
	if u == nil {
		return nil, "", errors.New("Google Drive is not connected")
	}
	in := map[string]string{"parent": parent}
	if page != "" {
		in["page_token"] = page
	}
	args, _ := json.Marshal(in)
	res, err := u.call(ctx, "browse_folders", args)
	if err != nil {
		return nil, "", err
	}
	if res.IsError {
		return nil, "", errors.New(resultText(res))
	}
	var out struct {
		Folders []driveItem `json:"folders"`
		Next    string      `json:"next_page_token"`
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	return out.Folders, out.Next, nil
}

func resultText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// setupTool tells a tool a server keeps for eriphe-core's workspace
// (eriphe-files' browse_folders, which sees every folder name of the
// account): the pool leaves it out of the server's tools, so no grant, no
// task and not the brain ever get it; only browseDrive calls it.
func setupTool(t *mcp.Tool) bool { return t.Meta["eriphe/setup"] == true }

type driveItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"`
}
