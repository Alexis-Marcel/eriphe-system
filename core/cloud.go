package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Cloud storage as a source of places: eriphe-files (servers/files), a
// local server like eriphe-mail, serves an account's folders to tasks.
// eriphe-core runs the sign-in (oauth_client.go), keeps the refresh token in
// the vault and lends it to the files service on its node, like a password:
// it never enters a task. The app the sign-in uses is the user's own, kept
// in the vault (connection_apps.go); its places are picked on the tool's
// page (places.go, web_places.go).

const (
	filesServer    = "fichiers"
	filesImage     = "ghcr.io/alexis-marcel/eriphe-files"
	filesConfigEnv = "FILES_CONFIG"
)

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
			return &catalogError{http.StatusConflict, "conn.taken", filesServer, errors.New("a tool named " + filesServer + " already exists")}
		}
		spec, _ = decodeRun(srv.Run)
	}
	change(&fc)
	if len(fc.Accounts) == 0 {
		if exists {
			return c.deleteServer(filesServer)
		}
		return errors.New("the drive is not connected")
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
		p, _ := connectionPreset(fc.Accounts[0].signInProvider(), toolDrive)
		srv = Server{Use: tr(lang, "preset."+p.ID+".use")}.withLevel(accessReads)
	}
	srv.Run = marshalRun(spec)
	return c.putServer(mode, filesServer, srv, nil)
}

// putDriveAccount gives a Google account to the files server, reading the
// token kept in the vault as secret, or updates its scopes. Without the
// scope that writes, the account's folders fall back to read.
func (c *core) putDriveAccount(lang, provider, email string, scopes []string, write bool, secret string) error {
	tokenEnv := driveTokenEnvOf(email)
	env := map[string]EnvValue{tokenEnv: {File: vaultPrefix + secret}}
	return c.putDrive(lang, func(fc *filesConfig) {
		i := slices.IndexFunc(fc.Accounts, func(a filesAccount) bool { return strings.EqualFold(a.Name, email) })
		if i < 0 {
			fc.Accounts = append(fc.Accounts, filesAccount{Name: email, Provider: provider})
			i = len(fc.Accounts) - 1
		}
		fc.Accounts[i].Scopes = scopes
		fc.Accounts[i].Auth.TokenEnv = tokenEnv
		if !write {
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
// task and not the brain ever get it; only browsePlaces calls it.
func setupTool(t *mcp.Tool) bool { return t.Meta["eriphe/setup"] == true }
