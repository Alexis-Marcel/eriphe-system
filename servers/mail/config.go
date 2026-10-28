package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config is the server configuration, read as JSON from MAIL_CONFIG or from
// the file named by MAIL_CONFIG_FILE (or -config).
type Config struct {
	Accounts []AccountConfig `json:"accounts"`
	// MaxResults caps search_messages results (default 50).
	MaxResults int `json:"max_results,omitempty"`
	// MaxBodyChars caps the text returned by read_message (default 20000).
	MaxBodyChars int `json:"max_body_chars,omitempty"`
	// TimeoutSeconds bounds each IMAP or SMTP session (default 60).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// AccountConfig is one mailbox. With a known provider (given, or guessed from
// the address domain), only name, address and auth are needed.
type AccountConfig struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	DisplayName string `json:"display_name,omitempty"`
	// Provider is a preset: gmail, icloud, outlook, office365, orange, ovh,
	// fastmail. Empty: guessed from the address, or none.
	Provider string `json:"provider,omitempty"`
	// Username for IMAP and SMTP; default the address.
	Username string     `json:"username,omitempty"`
	IMAP     Endpoint   `json:"imap"`
	SMTP     Endpoint   `json:"smtp"`
	Auth     AuthConfig `json:"auth"`
	// Folders overrides the folder used for a role: drafts, sent, trash,
	// archive, junk. Default: the server's special-use flags, then common names.
	Folders map[string]string `json:"folders,omitempty"`
	// SaveSent appends sent messages to the Sent folder. Providers that file
	// SMTP submissions themselves (Gmail, Outlook) default to false.
	SaveSent *bool `json:"save_sent,omitempty"`
	// CAFile is a PEM bundle of extra roots, for self-hosted servers.
	CAFile string `json:"ca_file,omitempty"`
}

// Endpoint is an IMAP or SMTP server. Security is tls (implicit TLS) or
// starttls; it defaults from the port (993 and 465: tls, else starttls).
// There is no plaintext mode.
type Endpoint struct {
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Security string `json:"security,omitempty"`
}

// AuthConfig names where the secret is. Exactly one of the four is set. The
// secret is read again at each connection, so a token file that eriphe-core
// refreshes is picked up without a restart.
type AuthConfig struct {
	PasswordEnv    string `json:"password_env,omitempty"`
	PasswordFile   string `json:"password_file,omitempty"`
	OAuthTokenEnv  string `json:"oauth_token_env,omitempty"`
	OAuthTokenFile string `json:"oauth_token_file,omitempty"`
}

func (a AuthConfig) oauth() bool { return a.OAuthTokenEnv != "" || a.OAuthTokenFile != "" }

type preset struct {
	imap, smtp Endpoint
	saveSent   bool
	folders    map[string]string
}

var presets = map[string]preset{
	"gmail": {
		imap: Endpoint{"imap.gmail.com", 993, "tls"}, smtp: Endpoint{"smtp.gmail.com", 465, "tls"},
		// Gmail's "archive" is removing the Inbox label: moving to All Mail.
		folders: map[string]string{"archive": "[Gmail]/All Mail"},
	},
	"icloud":    {imap: Endpoint{"imap.mail.me.com", 993, "tls"}, smtp: Endpoint{"smtp.mail.me.com", 587, "starttls"}, saveSent: true},
	"outlook":   {imap: Endpoint{"outlook.office365.com", 993, "tls"}, smtp: Endpoint{"smtp-mail.outlook.com", 587, "starttls"}},
	"office365": {imap: Endpoint{"outlook.office365.com", 993, "tls"}, smtp: Endpoint{"smtp.office365.com", 587, "starttls"}},
	"orange":    {imap: Endpoint{"imap.orange.fr", 993, "tls"}, smtp: Endpoint{"smtp.orange.fr", 465, "tls"}, saveSent: true},
	"ovh":       {imap: Endpoint{"ssl0.ovh.net", 993, "tls"}, smtp: Endpoint{"ssl0.ovh.net", 465, "tls"}, saveSent: true},
	"fastmail":  {imap: Endpoint{"imap.fastmail.com", 993, "tls"}, smtp: Endpoint{"smtp.fastmail.com", 465, "tls"}},
}

// providerFor guesses a preset from the address domain.
func providerFor(address string) string {
	_, domain, _ := strings.Cut(strings.ToLower(address), "@")
	switch {
	case domain == "gmail.com" || domain == "googlemail.com":
		return "gmail"
	case domain == "icloud.com" || domain == "me.com" || domain == "mac.com":
		return "icloud"
	case strings.HasPrefix(domain, "outlook.") || strings.HasPrefix(domain, "hotmail.") ||
		strings.HasPrefix(domain, "live.") || domain == "msn.com":
		return "outlook"
	case domain == "orange.fr" || domain == "wanadoo.fr":
		return "orange"
	case domain == "fastmail.com" || domain == "fastmail.fm":
		return "fastmail"
	}
	return ""
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
		return nil, errors.New("no configuration: set MAIL_CONFIG or MAIL_CONFIG_FILE")
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
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 50
	}
	if cfg.MaxBodyChars <= 0 {
		cfg.MaxBodyChars = 20000
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 60
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
		key := strings.ToLower(a.Name)
		if seen[key] {
			return fmt.Errorf("account %q: duplicate name", a.Name)
		}
		seen[key] = true
	}
	return nil
}

func (a *AccountConfig) normalize() error {
	if a.Address == "" || !strings.Contains(a.Address, "@") {
		return errors.New("address is required")
	}
	if a.Name == "" {
		a.Name = a.Address
	}
	if a.Username == "" {
		a.Username = a.Address
	}
	if a.Provider == "" {
		a.Provider = providerFor(a.Address)
	}
	a.Provider = strings.ToLower(a.Provider)
	var p preset
	if a.Provider != "" {
		var ok bool
		if p, ok = presets[a.Provider]; !ok {
			return fmt.Errorf("unknown provider %q", a.Provider)
		}
	}
	fill := func(e *Endpoint, def Endpoint) {
		if e.Host == "" {
			*e = Endpoint{def.Host, def.Port, def.Security}
		}
	}
	fill(&a.IMAP, p.imap)
	fill(&a.SMTP, p.smtp)
	if a.SaveSent == nil {
		v := p.saveSent || a.Provider == ""
		a.SaveSent = &v
	}
	folders := map[string]string{}
	for k, v := range p.folders {
		folders[k] = v
	}
	for k, v := range a.Folders {
		folders[strings.ToLower(k)] = v
	}
	a.Folders = folders

	if err := a.IMAP.normalize(993); err != nil {
		return fmt.Errorf("imap: %w", err)
	}
	if a.SMTP.Host != "" {
		if err := a.SMTP.normalize(465); err != nil {
			return fmt.Errorf("smtp: %w", err)
		}
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
	return nil
}

func (e *Endpoint) normalize(defPort int) error {
	if e.Host == "" {
		return errors.New("host is required (or a known provider)")
	}
	if e.Port == 0 {
		e.Port = defPort
	}
	e.Security = strings.ToLower(e.Security)
	if e.Security == "" {
		if e.Port == 993 || e.Port == 465 {
			e.Security = "tls"
		} else {
			e.Security = "starttls"
		}
	}
	if e.Security != "tls" && e.Security != "starttls" {
		return fmt.Errorf("security %q: only tls or starttls are allowed", e.Security)
	}
	return nil
}

func (e Endpoint) addr() string { return fmt.Sprintf("%s:%d", e.Host, e.Port) }

// secret reads the account's password or token now.
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
	// A token file may hold an OAuth token response rather than the bare token.
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
