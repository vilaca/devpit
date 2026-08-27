package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vilaca/devpit/sdk"
)

// stubRegistry registers a fake provider type for the duration of a test so
// validation (type ∈ Registry) has something to match without importing the
// real providers.
func stubRegistry(t *testing.T, types ...string) {
	t.Helper()
	for _, typ := range types {
		sdk.Registry[typ] = func(sdk.ConnectionConfig) (sdk.Provider, error) { return nil, nil }
		t.Cleanup(func() { delete(sdk.Registry, typ) })
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	stubRegistry(t, "github", "gitlab")
	path := writeConfig(t, `
db_path: /var/lib/devpit/devpit.db
connections:
  - id: gh-personal
    type: github
    token: ghp_abc
    label: Personal
  - id: gl-acme
    type: gitlab
    token: glpat_xyz
    base_url: https://gitlab.acme.com
    handle: bot-user
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != "/var/lib/devpit/devpit.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if len(cfg.Connections) != 2 {
		t.Fatalf("got %d connections, want 2", len(cfg.Connections))
	}
	gh := cfg.Connections[0]
	if gh.ID != "gh-personal" || gh.Type != "github" || gh.Token != "ghp_abc" || gh.Label != "Personal" {
		t.Errorf("gh mismatch: %+v", gh)
	}
	gl := cfg.Connections[1]
	if gl.BaseURL != "https://gitlab.acme.com" || gl.Handle != "bot-user" {
		t.Errorf("gl mismatch: %+v", gl)
	}
	if gl.Label != "gl-acme" {
		t.Errorf("gl.Label = %q, want fallback to id", gl.Label)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", cfg.Warnings)
	}
	if cfg.Listen != DefaultListen {
		t.Errorf("Listen = %q, want default %q", cfg.Listen, DefaultListen)
	}
}

func TestLoadListenOverride(t *testing.T) {
	stubRegistry(t, "github")
	path := writeConfig(t, `
db_path: /tmp/devpit.db
listen: ":7474"
connections:
  - id: gh
    type: github
    token: ghp_abc
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":7474" {
		t.Errorf("Listen = %q, want :7474", cfg.Listen)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "not loopback") {
		t.Errorf("warnings = %v, want one non-loopback listen warning", cfg.Warnings)
	}
}

func TestListenIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{DefaultListen, true},
		{"127.0.0.1:7474", true},
		{"[::1]:7474", true},
		{"Localhost:7474", true}, // hostname compare is case-insensitive
		{":7474", false},
		{"0.0.0.0:7474", false},
		{"[::]:7474", false},
		{"192.168.1.10:7474", false},
		{"not-an-addr", false},
	}
	for _, c := range cases {
		if got := listenIsLoopback(c.addr); got != c.want {
			t.Errorf("listenIsLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestLoadValidationErrors(t *testing.T) {
	stubRegistry(t, "github")
	cases := map[string]string{
		"missing db_path": `
connections:
  - id: a
    type: github
    token: t
`,
		"missing id": `
db_path: x
connections:
  - type: github
    token: t
`,
		"duplicate id": `
db_path: x
connections:
  - id: a
    type: github
    token: t
  - id: a
    type: github
    token: t
`,
		"missing type": `
db_path: x
connections:
  - id: a
    token: t
`,
		"unknown type": `
db_path: x
connections:
  - id: a
    type: bitbucket
    token: t
`,
		"missing token": `
db_path: x
connections:
  - id: a
    type: github
`,
		"unknown key": `
db_path: x
connections:
  - id: a
    type: github
    token: t
    secret: oops
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, body)
			if _, err := Load(path); err == nil {
				t.Fatalf("Load(%s): want error, got nil", name)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("Load missing file: want error")
	}
}

func TestLoadPermissionWarning(t *testing.T) {
	stubRegistry(t, "github")
	path := writeConfig(t, `
db_path: x
connections:
  - id: a
    type: github
    token: t
`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "readable") {
		t.Errorf("warnings = %v, want one permission warning", cfg.Warnings)
	}
}

func TestLoadJiraAbsent(t *testing.T) {
	stubRegistry(t, "github")
	path := writeConfig(t, `
db_path: x
connections:
  - id: a
    type: github
    token: t
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Jira != nil {
		t.Errorf("Jira = %+v, want nil when block absent", cfg.Jira)
	}
}

func TestLoadJiraComplete(t *testing.T) {
	stubRegistry(t, "github")
	path := writeConfig(t, `
db_path: x
connections:
  - id: a
    type: github
    token: t
jira:
  base_url: https://example.atlassian.net
  email: user@example.com
  api_token: secret
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Jira == nil {
		t.Fatal("Jira is nil, want config")
	}
	if cfg.Jira.BaseURL != "https://example.atlassian.net" {
		t.Errorf("BaseURL = %q", cfg.Jira.BaseURL)
	}
	if cfg.Jira.Email != "user@example.com" {
		t.Errorf("Email = %q", cfg.Jira.Email)
	}
	if cfg.Jira.APIToken != "secret" {
		t.Errorf("APIToken = %q", cfg.Jira.APIToken)
	}
}

func TestLoadJiraPartialErrors(t *testing.T) {
	stubRegistry(t, "github")
	base := `
db_path: x
connections:
  - id: a
    type: github
    token: t
`
	cases := map[string]string{
		"missing base_url": base + `jira:
  email: u@e.com
  api_token: t
`,
		"missing email": base + `jira:
  base_url: https://x.atlassian.net
  api_token: t
`,
		"missing api_token": base + `jira:
  base_url: https://x.atlassian.net
  email: u@e.com
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, body)
			if _, err := Load(path); err == nil {
				t.Fatalf("Load(%s): want error, got nil", name)
			}
		})
	}
}

func TestLoadJiraUnknownKey(t *testing.T) {
	stubRegistry(t, "github")
	path := writeConfig(t, `
db_path: x
connections:
  - id: a
    type: github
    token: t
jira:
  base_url: https://x.atlassian.net
  email: u@e.com
  api_token: t
  unknown_field: oops
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load with unknown jira key: want error")
	}
}

func TestLoadExpandsHomeDBPath(t *testing.T) {
	stubRegistry(t, "github")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	path := writeConfig(t, `
db_path: ~/.local/share/devpit/devpit.db
connections:
  - id: a
    type: github
    token: t
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(home, ".local/share/devpit/devpit.db")
	if cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, want)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		in, want string
	}{
		{"/var/lib/devpit.db", "/var/lib/devpit.db"},
		{"relative/devpit.db", "relative/devpit.db"},
		{"~other/db", "~other/db"},
		{"~", home},
		{"~/", home},
		{"~/devpit.db", filepath.Join(home, "devpit.db")},
	}
	for _, c := range cases {
		got, err := expandHome(c.in)
		if err != nil {
			t.Errorf("expandHome(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("expandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExpandHomeBackslashUnix verifies a literal "~\foo" is NOT home-expanded on
// Unix, where `\` is an ordinary filename character (only Windows treats it as a
// path separator).
func TestExpandHomeBackslashUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip(`\ is a path separator on Windows`)
	}
	got, err := expandHome(`~\foo`)
	if err != nil {
		t.Fatalf("expandHome: %v", err)
	}
	if got != `~\foo` {
		t.Errorf(`expandHome(%q) = %q, want %q (unexpanded on Unix)`, `~\foo`, got, `~\foo`)
	}
}

func TestExpandHomeNoHome(t *testing.T) {
	orig := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { userHomeDir = orig })

	if _, err := expandHome("~/devpit.db"); err == nil {
		t.Fatal("expandHome with no home: want error")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
	if got := DefaultPath(); got != "/custom/xdg/devpit/config.yaml" {
		t.Errorf("DefaultPath with XDG = %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(home, ".config", "devpit", "config.yaml")
	if got := DefaultPath(); got != want {
		t.Errorf("DefaultPath fallback = %q, want %q", got, want)
	}
}
