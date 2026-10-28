package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is where eriphe-core is and the token to reach it, saved by
// `eriphe login`. ERIPHE_URL and ERIPHE_TOKEN override the file.
type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// configPath is <user config dir>/eriphe/config.json.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "eriphe", "config.json"), nil
}

// loadConfig reads the saved configuration, then applies the environment.
func loadConfig() (*Config, error) {
	var c Config
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if v := os.Getenv("ERIPHE_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("ERIPHE_TOKEN"); v != "" {
		c.Token = v
	}
	if c.URL == "" || c.Token == "" {
		return nil, errors.New("not logged in: run `eriphe login <url>` or set ERIPHE_URL and ERIPHE_TOKEN")
	}
	return &c, nil
}

// saveConfig writes the configuration readable by its owner only.
func saveConfig(c *Config) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	raw, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return "", err
	}
	// WriteFile keeps the mode of an existing file.
	return path, os.Chmod(path, 0o600)
}
