package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Cloud storage as a source of Folders: Google Drive now (Dropbox and
// OneDrive later), served to tasks by eriphe-files (servers/files), a local
// server like eriphe-mail. eriphe-core runs the OAuth authorization, keeps
// the refresh token in the vault and lends it to the files service on its
// node, like a password: it never enters a task.
//
// Each user brings their own Google app, as with Home Assistant: an OAuth
// client "Web application" they create once in their Google Cloud project,
// with the workspace's own https://<workspace>/oauth/callback as its
// redirect. The page guides them through it (web_drive.go,
// docs/google.md); the client id and secret are kept in the vault
// (googleClientVault), never shown back. The sign-in itself is the neutral
// flow of oauth_client.go.

const (
	filesServer    = "fichiers"
	filesImage     = "ghcr.io/alexis-marcel/eriphe-files"
	filesConfigEnv = "FILES_CONFIG"
	driveTokenEnv  = "FILES_GOOGLE_TOKEN" // an account's is FILES_GOOGLE_TOKEN_<hash> (driveTokenEnvOf)

	googleClientVault = "google-drive-client" // the user's own client, in the vault

	scopeDrive         = "https://www.googleapis.com/auth/drive"
	scopeDriveReadonly = "https://www.googleapis.com/auth/drive.readonly"
)

// CloudConfig is what an older configuration may still carry, from when
// Eriphe had a shared Google app behind a sign-in relay: both fields are
// read and ignored (legacyCloud says so at start). A Drive connected
// through that app keeps working until its client is deleted; connecting
// again takes the user's own app.
type CloudConfig struct {
	GoogleClientFile string `json:"google_client_file,omitempty"`
	BounceURL        string `json:"bounce_url,omitempty"`
}

// legacyCloud is the log line for a configuration that still names the
// shared app, or "".
func (c *Config) legacyCloud() string {
	if c.Cloud == nil || (c.Cloud.GoogleClientFile == "" && c.Cloud.BounceURL == "") {
		return ""
	}
	return "cloud: google_client_file and bounce_url are not used any more: connect Google Drive with your own Google app (Settings → Tools → Google Drive)"
}

var errNoGoogleClient = errors.New("no Google client")

// googleClient reads the user's Google app from the vault.
func googleClient() (oauthApp, error) {
	v := currentVault()
	if v == nil {
		return oauthApp{}, errNoVault
	}
	raw, err := v.get(googleClientVault)
	if errors.Is(err, errNoSecret) {
		return oauthApp{}, errNoGoogleClient
	}
	if err != nil {
		return oauthApp{}, err
	}
	var app oauthApp
	if json.Unmarshal([]byte(raw), &app) != nil || app.ID == "" || app.Secret == "" {
		return oauthApp{}, errNoGoogleClient
	}
	return app, nil
}

func storeGoogleClient(app oauthApp) error {
	v := currentVault()
	if v == nil {
		return errNoVault
	}
	raw, _ := json.Marshal(app)
	return v.set(googleClientVault, string(raw))
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
	CanWrite bool // an account granted the scope that creates files
	Accounts []driveAccountView
	Folders  []DriveFolder
}

// driveAccountView is one account of the files server.
type driveAccountView struct {
	Name     string // the Google address; "google" for a Drive connected before accounts
	CanWrite bool
}

func (c *core) driveState() driveState {
	srv, ok := c.conf().Servers[filesServer]
	if !ok {
		return driveState{}
	}
	fc, ok := filesConfigOf(srv)
	st := driveState{Exists: true, Mine: ok && c.source(kindServer, filesServer) == sourceWorkspace, Folders: fc.Folders}
	for _, a := range fc.Accounts {
		w := slices.Contains(a.Scopes, scopeDrive)
		st.Accounts = append(st.Accounts, driveAccountView{Name: a.Name, CanWrite: w})
		st.CanWrite = st.CanWrite || w
	}
	return st
}

// canWrite tells whether an account of the files server may create files.
func (st driveState) canWrite(account string) bool {
	for _, a := range st.Accounts {
		if strings.EqualFold(a.Name, account) {
			return a.CanWrite
		}
	}
	return false
}

// driveAccountName finds the account a folder is taken from: the one
// named, or the only one.
func (st driveState) driveAccountName(account string) (string, bool) {
	for _, a := range st.Accounts {
		if strings.EqualFold(a.Name, account) || account == "" && len(st.Accounts) == 1 {
			return a.Name, true
		}
	}
	return "", false
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

// driveTokenEnvOf is the environment variable of an account's token in the
// files server.
func driveTokenEnvOf(email string) string {
	h := sha256.Sum256([]byte(strings.ToLower(email)))
	return driveTokenEnv + "_" + strings.ToUpper(hex.EncodeToString(h[:4]))
}

// putDrive changes the files server's configuration, creating the server
// the first time and removing it when no account is left. env holds the
// token references of accounts added; the others keep theirs.
func (c *core) putDrive(lang string, change func(*filesConfig), env map[string]EnvValue) error {
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
		if exists {
			return c.deleteServer(filesServer)
		}
		return errors.New("Google Drive is not connected")
	}
	raw, _ := json.Marshal(fc)
	next := map[string]EnvValue{filesConfigEnv: {Value: string(raw)}}
	for _, a := range fc.Accounts {
		if v, ok := env[a.Auth.TokenEnv]; ok {
			next[a.Auth.TokenEnv] = v
		} else if v, ok := spec.Env[a.Auth.TokenEnv]; ok {
			next[a.Auth.TokenEnv] = v
		}
	}
	spec.Env = next
	mode := putUpdate
	if !exists {
		mode = putCreate
		srv = Server{Use: tr(lang, "preset.gdrive.use")}.withLevel(accessReads)
	}
	srv.Run = marshalRun(spec)
	return c.putServer(mode, filesServer, srv, nil)
}

// putDriveAccount gives a Google account to the files server, or updates
// its scopes. A Drive connected before accounts existed (its token in
// FILES_GOOGLE_TOKEN) is taken over by the first account connected: its
// folders stay. Without the scope that writes, the account's folders fall
// back to read.
func (c *core) putDriveAccount(lang, email string, scopes []string) error {
	tokenEnv := driveTokenEnvOf(email)
	env := map[string]EnvValue{tokenEnv: {File: vaultPrefix + googleSecret(email)}}
	return c.putDrive(lang, func(fc *filesConfig) {
		i := slices.IndexFunc(fc.Accounts, func(a filesAccount) bool { return strings.EqualFold(a.Name, email) })
		if i < 0 {
			if j := slices.IndexFunc(fc.Accounts, func(a filesAccount) bool { return a.Auth.TokenEnv == driveTokenEnv }); j >= 0 {
				old := fc.Accounts[j].Name
				fc.Accounts = slices.Delete(fc.Accounts, j, j+1)
				for k := range fc.Folders {
					if fc.Folders[k].Account == old || fc.Folders[k].Account == "" {
						fc.Folders[k].Account = email
					}
				}
			}
			fc.Accounts = append(fc.Accounts, filesAccount{Name: email, Provider: "google"})
			i = len(fc.Accounts) - 1
		}
		fc.Accounts[i].Scopes = scopes
		fc.Accounts[i].Auth.TokenEnv = tokenEnv
		if !slices.Contains(scopes, scopeDrive) {
			for k := range fc.Folders {
				if strings.EqualFold(fc.Folders[k].Account, email) {
					fc.Folders[k].Access = "read"
				}
			}
		}
	}, env)
}

// dropDriveAccount takes an account and its folders out of the files server.
func (c *core) dropDriveAccount(email string) error {
	return c.putDrive("", func(fc *filesConfig) {
		fc.Accounts = slices.DeleteFunc(fc.Accounts, func(a filesAccount) bool { return strings.EqualFold(a.Name, email) })
		fc.Folders = slices.DeleteFunc(fc.Folders, func(f DriveFolder) bool { return strings.EqualFold(f.Account, email) })
	}, nil)
}

// addDriveFolder gives a folder of an account to Eriphe, or changes its
// access. account may be empty with one account.
func (c *core) addDriveFolder(account, id, name, access string) error {
	id, name = strings.TrimSpace(id), strings.Join(strings.Fields(name), " ")
	if id == "" || name == "" || len(name) > 120 || (access != "read" && access != "drop") {
		return &catalogError{http.StatusBadRequest, "drive.badfolder", "", errors.New("a folder needs an id, a name and an access")}
	}
	st := c.driveState()
	if st.Exists && !st.Mine {
		return &catalogError{http.StatusConflict, "drive.taken", filesServer, errors.New("a tool named " + filesServer + " already exists")}
	}
	for _, f := range st.Folders {
		if f.ID == id {
			account = f.Account
		}
	}
	account, ok := st.driveAccountName(account)
	if !ok {
		return &catalogError{http.StatusBadRequest, "drive.badfolder", "", errors.New("no such Google Drive account")}
	}
	if access == "drop" && !st.canWrite(account) {
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
		fc.Folders = append(fc.Folders, DriveFolder{Name: final, Account: account, ID: id, Access: access})
	}, nil)
}

func (c *core) removeDriveFolder(id string) error {
	return c.putDrive("", func(fc *filesConfig) {
		fc.Folders = slices.DeleteFunc(fc.Folders, func(f DriveFolder) bool { return f.ID == id })
	}, nil)
}

// browseDrive lists an account's folders through the files server's setup
// tool, which tasks never get.
func (c *core) browseDrive(ctx context.Context, account, parent, page string) ([]driveItem, string, error) {
	u := c.pool.get(filesServer)
	if u == nil {
		return nil, "", errors.New("Google Drive is not connected")
	}
	in := map[string]string{"parent": parent}
	if account != "" {
		in["account"] = account
	}
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
