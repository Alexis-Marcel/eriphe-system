package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config is the server configuration, read as JSON from CALENDAR_CONFIG or
// from the file named by CALENDAR_CONFIG_FILE (or -config).
type Config struct {
	// Timezone used to read and show times without an offset; default
	// Europe/Paris (or TZ).
	Timezone string          `json:"timezone,omitempty"`
	Accounts []AccountConfig `json:"accounts"`
	// MaxEvents caps list_events results (default 500).
	MaxEvents int `json:"max_events,omitempty"`
	// TimeoutSeconds bounds each request to a provider (default 30).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	loc *time.Location
}

// AccountConfig is one calendar account.
type AccountConfig struct {
	Name string `json:"name"`
	// Provider: icloud, google, fastmail, nextcloud or caldav (any CalDAV
	// server, url required), microsoft (Microsoft Graph, OAuth token).
	Provider string `json:"provider"`
	// URL of the CalDAV server; set by the preset when there is one.
	URL string `json:"url,omitempty"`
	// Username for CalDAV basic auth (iCloud: the Apple ID address). For
	// Google, the account address, used to find the calendars.
	Username string     `json:"username,omitempty"`
	Auth     AuthConfig `json:"auth"`
	// GraphURL overrides https://graph.microsoft.com/v1.0 (tests).
	GraphURL string `json:"graph_url,omitempty"`
}

// AuthConfig names where the secret is. Exactly one is set. Secrets are read
// again at each request, so a token file refreshed by eriphe-core is used
// without a restart.
type AuthConfig struct {
	PasswordEnv    string `json:"password_env,omitempty"`
	PasswordFile   string `json:"password_file,omitempty"`
	OAuthTokenEnv  string `json:"oauth_token_env,omitempty"`
	OAuthTokenFile string `json:"oauth_token_file,omitempty"`
}

func (a AuthConfig) oauth() bool { return a.OAuthTokenEnv != "" || a.OAuthTokenFile != "" }

var caldavURLs = map[string]string{
	"icloud":   "https://caldav.icloud.com",
	"google":   "https://apidata.googleusercontent.com/caldav/v2/",
	"fastmail": "https://caldav.fastmail.com/dav/",
}

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
		return nil, errors.New("no configuration: set CALENDAR_CONFIG or CALENDAR_CONFIG_FILE")
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, cfg.normalize()
}

func (cfg *Config) normalize() error {
	if cfg.Timezone == "" {
		cfg.Timezone = os.Getenv("TZ")
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "Europe/Paris"
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("config: timezone: %w", err)
	}
	cfg.loc = loc
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 500
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if len(cfg.Accounts) == 0 {
		return errors.New("config: no accounts")
	}
	seen := map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		if err := a.normalize(); err != nil {
			return fmt.Errorf("account %q: %w", a.Name, err)
		}
		if seen[strings.ToLower(a.Name)] {
			return fmt.Errorf("account %q: duplicate name", a.Name)
		}
		seen[strings.ToLower(a.Name)] = true
	}
	return nil
}

func (a *AccountConfig) normalize() error {
	a.Provider = strings.ToLower(a.Provider)
	if a.Name == "" {
		a.Name = a.Provider
	}
	n := 0
	for _, s := range []string{a.Auth.PasswordEnv, a.Auth.PasswordFile, a.Auth.OAuthTokenEnv, a.Auth.OAuthTokenFile} {
		if s != "" {
			n++
		}
	}
	if n != 1 {
		return errors.New("auth needs exactly one of password_env, password_file, oauth_token_env, oauth_token_file")
	}
	switch a.Provider {
	case "microsoft":
		if !a.Auth.oauth() {
			return errors.New("microsoft needs an OAuth token")
		}
		if a.GraphURL == "" {
			a.GraphURL = "https://graph.microsoft.com/v1.0"
		}
		return nil
	case "icloud", "google", "fastmail":
		if a.URL == "" {
			a.URL = caldavURLs[a.Provider]
		}
	case "nextcloud", "caldav":
	case "":
		return errors.New("provider is required (icloud, google, fastmail, nextcloud, caldav, microsoft)")
	default:
		return fmt.Errorf("unknown provider %q", a.Provider)
	}
	if a.URL == "" {
		return errors.New("url is required")
	}
	if !strings.HasPrefix(a.URL, "https://") {
		return errors.New("url must be https")
	}
	if a.Provider == "google" && !a.Auth.oauth() {
		return errors.New("google CalDAV needs an OAuth token")
	}
	if !a.Auth.oauth() && a.Username == "" {
		return errors.New("username is required with a password")
	}
	return nil
}

// secret reads the password or token now.
func (a *AccountConfig) secret() (string, error) {
	var v, from string
	switch {
	case a.Auth.PasswordEnv != "":
		v, from = os.Getenv(a.Auth.PasswordEnv), a.Auth.PasswordEnv
	case a.Auth.OAuthTokenEnv != "":
		v, from = os.Getenv(a.Auth.OAuthTokenEnv), a.Auth.OAuthTokenEnv
	default:
		from = a.Auth.PasswordFile + a.Auth.OAuthTokenFile
		b, err := os.ReadFile(from)
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		v = string(b)
	}
	v = strings.TrimSpace(v)
	if a.Auth.oauth() && strings.HasPrefix(v, "{") {
		var t struct {
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal([]byte(v), &t) == nil {
			v = t.AccessToken
		}
	}
	if v == "" {
		return "", fmt.Errorf("secret %s is empty", from)
	}
	return v, nil
}

// authClient adds credentials to each request, read at that moment.
type authClient struct {
	acct *AccountConfig
	http *http.Client
}

func (c *authClient) Do(req *http.Request) (*http.Response, error) {
	secret, err := c.acct.secret()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.acct.Name, err)
	}
	if c.acct.Auth.oauth() {
		req.Header.Set("Authorization", "Bearer "+secret)
	} else {
		req.SetBasicAuth(c.acct.Username, secret)
	}
	return c.http.Do(req)
}
