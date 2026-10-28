package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Config is the server configuration, read as JSON from FILES_CONFIG or from
// the file named by FILES_CONFIG_FILE (or -config).
type Config struct {
	Accounts []AccountConfig `json:"accounts"`
	// Folders are the only places the tools reach: each one and what is
	// inside it, nothing else of the account.
	Folders []FolderConfig `json:"folders,omitempty"`
	// Setup serves browse_folders, which lists an account's folders (names
	// only) so that eriphe-core's workspace can offer them. eriphe-core never
	// lends it to a task. Default on; false leaves it out.
	Setup *bool `json:"setup,omitempty"`
	// MaxChars caps the text read_file returns (default 50000).
	MaxChars int `json:"max_chars,omitempty"`
	// MaxBytes caps what is downloaded or written per file (default 20 MB).
	MaxBytes int64 `json:"max_bytes,omitempty"`
	// TimeoutSeconds bounds each call to the provider (default 60).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// AccountConfig is one cloud account.
type AccountConfig struct {
	Name     string     `json:"name"`
	Provider string     `json:"provider"` // google; dropbox and onedrive later
	Auth     AuthConfig `json:"auth"`
	// Scopes the user granted, as the token response said: dropping files
	// needs a scope that allows it (Google: drive).
	Scopes []string `json:"scopes,omitempty"`
	// APIURL, UploadURL and TokenURL replace the provider's endpoints (tests).
	APIURL    string `json:"api_url,omitempty"`
	UploadURL string `json:"upload_url,omitempty"`
	TokenURL  string `json:"token_url,omitempty"`
}

// AuthConfig names where the account's token is: exactly one of the two. It
// holds a JSON object {"client_id", "client_secret", "refresh_token"} (the
// "authorized_user" form of Google's credentials), from eriphe-core's vault.
type AuthConfig struct {
	TokenEnv  string `json:"token_env,omitempty"`
	TokenFile string `json:"token_file,omitempty"`
}

// FolderConfig is one Folder given to Eriphe: a folder of an account, by its
// provider id, and what Eriphe may do there.
type FolderConfig struct {
	Name    string `json:"name"`    // how tools name it
	Account string `json:"account"` // an account name; may be empty with one account
	ID      string `json:"id"`      // the provider's folder id
	// Access is read (list, search, read), or drop: read and also create new
	// files in the folder's Eriphe/ subfolder. Never modify or delete.
	Access string `json:"access"`
}

const (
	accessRead = "read"
	accessDrop = "drop"
	// resultsFolder is the subfolder of a writable Folder where new files go.
	resultsFolder = "Eriphe"
)

// LoadConfig reads the configuration from the JSON text or, if empty, the file.
func LoadConfig(text, file string) (*Config, error) {
	var b []byte
	switch {
	case strings.TrimSpace(text) != "":
		b = []byte(text)
	case file != "":
		var err error
		if b, err = os.ReadFile(file); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("no configuration: set FILES_CONFIG or FILES_CONFIG_FILE")
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, cfg.normalize()
}

func (cfg *Config) setup() bool { return cfg.Setup == nil || *cfg.Setup }

func (cfg *Config) normalize() error {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = 50000
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 20 << 20
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 60
	}
	if len(cfg.Accounts) == 0 {
		return errors.New("config: no accounts")
	}
	accounts := map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		a.Name = strings.TrimSpace(a.Name)
		a.Provider = strings.ToLower(strings.TrimSpace(a.Provider))
		if a.Name == "" {
			a.Name = a.Provider
		}
		if !slices.Contains(providers, a.Provider) {
			return fmt.Errorf("account %q: provider is one of %s", a.Name, strings.Join(providers, ", "))
		}
		if (a.Auth.TokenEnv == "") == (a.Auth.TokenFile == "") {
			return fmt.Errorf("account %q: auth needs exactly one of token_env, token_file", a.Name)
		}
		key := strings.ToLower(a.Name)
		if accounts[key] {
			return fmt.Errorf("account %q: duplicate name", a.Name)
		}
		accounts[key] = true
	}
	folders := map[string]bool{}
	for i := range cfg.Folders {
		f := &cfg.Folders[i]
		f.Name, f.ID = strings.TrimSpace(f.Name), strings.TrimSpace(f.ID)
		if f.Name == "" || f.ID == "" {
			return fmt.Errorf("folder %d: name and id are required", i+1)
		}
		if f.Account == "" {
			if len(cfg.Accounts) != 1 {
				return fmt.Errorf("folder %q: account is required with several accounts", f.Name)
			}
			f.Account = cfg.Accounts[0].Name
		}
		if !accounts[strings.ToLower(f.Account)] {
			return fmt.Errorf("folder %q: no account %q", f.Name, f.Account)
		}
		if f.Access == "" {
			f.Access = accessRead
		}
		if f.Access != accessRead && f.Access != accessDrop {
			return fmt.Errorf("folder %q: access is read or drop", f.Name)
		}
		key := strings.ToLower(f.Name)
		if folders[key] {
			return fmt.Errorf("folder %q: duplicate name", f.Name)
		}
		folders[key] = true
	}
	return nil
}

// credentials are what refreshes an account's access token.
type credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

// credentials reads the account's token now.
func (a AccountConfig) credentials() (credentials, error) {
	var raw, from string
	if a.Auth.TokenEnv != "" {
		raw, from = os.Getenv(a.Auth.TokenEnv), a.Auth.TokenEnv
	} else {
		b, err := os.ReadFile(a.Auth.TokenFile)
		if err != nil {
			return credentials{}, fmt.Errorf("read token: %w", err)
		}
		raw, from = string(b), a.Auth.TokenFile
	}
	var c credentials
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &c); err != nil {
		return credentials{}, fmt.Errorf("token %s: not a JSON object", from)
	}
	if c.ClientID == "" || c.RefreshToken == "" {
		return credentials{}, fmt.Errorf("token %s: client_id and refresh_token are required", from)
	}
	return c, nil
}
