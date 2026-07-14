package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vilaca/devpit/sdk"

	"gopkg.in/yaml.v3"
)

// Config is the loaded, validated application configuration.
//
// Connections are static (ADR-0015): resolved once at startup, immutable at
// runtime. Poll intervals and the staleness threshold are engine constants
// (ADR-0004), not config, and deliberately absent here.
type Config struct {
	// DBPath is the SQLite database path passed to storage.Open.
	DBPath string
	// Connections is the resolved, validated connection list — Load's
	// headline output.
	Connections []sdk.ConnectionConfig
	// Jira is the optional Jira enrichment config. Nil when the jira: block
	// is absent from the config file; present means all three fields are set.
	Jira *JiraConfig
	// Warnings are non-fatal advisories surfaced at load time (e.g. an
	// over-permissive config file — ADR-0019). The caller decides how to
	// present them.
	Warnings []string
}

// JiraConfig holds the credentials for the Jira Cloud enricher (ADR-0022).
type JiraConfig struct {
	BaseURL  string
	Email    string
	APIToken string
}

// fileConfig mirrors the on-disk YAML shape. Field names are the
// snake_case keys users write; Load translates to the sdk types.
type fileConfig struct {
	DBPath      string           `yaml:"db_path"`
	Connections []connectionYAML `yaml:"connections"`
	Jira        *jiraYAML        `yaml:"jira"`
}

type connectionYAML struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	Token   string `yaml:"token"`
	BaseURL string `yaml:"base_url"`
	Label   string `yaml:"label"`
	Handle  string `yaml:"handle"`
}

type jiraYAML struct {
	BaseURL  string `yaml:"base_url"`
	Email    string `yaml:"email"`
	APIToken string `yaml:"api_token"`
}

// DefaultPath returns $XDG_CONFIG_HOME/devpit/config.yaml, falling back to
// ~/.config/devpit/config.yaml when XDG_CONFIG_HOME is unset.
func DefaultPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// No home and no XDG: return a relative path rather than fail here;
			// Load will report a clear open error if it is wrong.
			return filepath.Join("devpit", "config.yaml")
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "devpit", "config.yaml")
}

// Load reads, parses, and validates the config file at path. On success it
// returns the resolved connection list and any non-fatal warnings; any
// structural or validation failure is a fatal error (nil Config fields).
func Load(path string) (Config, error) {
	//nolint:gosec // path is the user-supplied config location; opening it is the point.
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var warnings []string
	if info, statErr := f.Stat(); statErr == nil {
		// The file holds plaintext tokens; warn (do not fail) if it is readable
		// beyond the owner (ADR-0019).
		if info.Mode().Perm()&0o077 != 0 {
			warnings = append(warnings, fmt.Sprintf(
				"config %q is readable by group/others (mode %04o); chmod 600 it — it contains plaintext tokens",
				path, info.Mode().Perm()))
		}
	}

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true) // reject unknown keys so typos surface loudly
	var raw fileConfig
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	cfg, err := validate(raw)
	if err != nil {
		return Config{}, fmt.Errorf("config %q: %w", path, err)
	}
	cfg.Warnings = warnings
	return cfg, nil
}

func validate(raw fileConfig) (Config, error) {
	if raw.DBPath == "" {
		return Config{}, errors.New("db_path is required")
	}

	conns := make([]sdk.ConnectionConfig, 0, len(raw.Connections))
	seen := make(map[string]bool, len(raw.Connections))
	for i, c := range raw.Connections {
		// Identify the offending entry by id when present, else by position.
		where := fmt.Sprintf("connection %d", i)
		if c.ID != "" {
			where = fmt.Sprintf("connection %q", c.ID)
		}

		switch {
		case c.ID == "":
			return Config{}, fmt.Errorf("%s: id is required", where)
		case seen[c.ID]:
			return Config{}, fmt.Errorf("%s: duplicate id", where)
		case c.Type == "":
			return Config{}, fmt.Errorf("%s: type is required", where)
		case sdk.Registry[c.Type] == nil:
			return Config{}, fmt.Errorf("%s: unknown type %q", where, c.Type)
		case c.Token == "":
			return Config{}, fmt.Errorf("%s: token is required", where)
		}
		seen[c.ID] = true

		if c.Label == "" {
			// Label is user-visible on every row; default it here so no
			// consumer ever renders a blank connection tag.
			c.Label = c.ID
		}

		conns = append(conns, sdk.ConnectionConfig{
			ID:      c.ID,
			Type:    c.Type,
			BaseURL: c.BaseURL,
			Token:   c.Token,
			Label:   c.Label,
			Handle:  c.Handle,
		})
	}

	var jira *JiraConfig
	if raw.Jira != nil {
		switch {
		case raw.Jira.BaseURL == "":
			return Config{}, errors.New("jira: base_url is required")
		case raw.Jira.Email == "":
			return Config{}, errors.New("jira: email is required")
		case raw.Jira.APIToken == "":
			return Config{}, errors.New("jira: api_token is required")
		}
		jira = &JiraConfig{
			BaseURL:  raw.Jira.BaseURL,
			Email:    raw.Jira.Email,
			APIToken: raw.Jira.APIToken,
		}
	}

	return Config{DBPath: raw.DBPath, Connections: conns, Jira: jira}, nil
}
