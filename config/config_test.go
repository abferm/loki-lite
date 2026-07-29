package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Journal.Dir != "/var/log/journal" {
		t.Errorf("expected default journal.dir, got %q", cfg.Journal.Dir)
	}
	if cfg.Journal.Name != "" {
		t.Errorf("expected default journal.name empty, got %q", cfg.Journal.Name)
	}
	if cfg.Server.Addr != ":3100" {
		t.Errorf("expected default server.addr, got %q", cfg.Server.Addr)
	}
	if cfg.Server.PoolMax != 10 {
		t.Errorf("expected default server.pool_max, got %d", cfg.Server.PoolMax)
	}
	if len(cfg.Schema.Exclude) != 4 {
		t.Errorf("expected 4 default excludes, got %d", len(cfg.Schema.Exclude))
	}
}

func TestLoadTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[journal]
dir = "/custom/journal"
name = "system"

[schema]
exclude = ["FIELD_A", "FIELD_B"]

[server]
addr = ":9999"
pool_max = 5
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Journal.Dir != "/custom/journal" {
		t.Errorf("expected /custom/journal, got %q", cfg.Journal.Dir)
	}
	if cfg.Journal.Name != "system" {
		t.Errorf("expected system, got %q", cfg.Journal.Name)
	}
	if cfg.Server.Addr != ":9999" {
		t.Errorf("expected :9999, got %q", cfg.Server.Addr)
	}
	if cfg.Server.PoolMax != 5 {
		t.Errorf("expected 5, got %d", cfg.Server.PoolMax)
	}
	if len(cfg.Schema.Exclude) != 2 {
		t.Fatalf("expected 2 excludes, got %d", len(cfg.Schema.Exclude))
	}
	if cfg.Schema.Exclude[0] != "FIELD_A" || cfg.Schema.Exclude[1] != "FIELD_B" {
		t.Errorf("unexpected exclude values: %v", cfg.Schema.Exclude)
	}
}

func TestSubstEnv(t *testing.T) {
	const tomlContent = `dir = "${MY_DIR:-/fallback/path}"`

	result := substEnv(tomlContent)
	if result != `dir = "/fallback/path"` {
		t.Errorf("expected default substitution, got %q", result)
	}

	t.Setenv("MY_DIR", "/env/path")
	result = substEnv(tomlContent)
	if result != `dir = "/env/path"` {
		t.Errorf("expected env substitution, got %q", result)
	}
}

func TestLoadEnvSubstitution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[journal]
dir = "${JOURNAL_DIR:-/var/log/journal}"

[schema]
exclude = ["MESSAGE"]

[server]
addr = "${ADDR:-:3100}"
pool_max = ${POOL_MAX:-10}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("JOURNAL_DIR", "/custom/journal")
	t.Setenv("ADDR", ":9999")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Journal.Dir != "/custom/journal" {
		t.Errorf("expected /custom/journal from env, got %q", cfg.Journal.Dir)
	}
	if cfg.Server.Addr != ":9999" {
		t.Errorf("expected :9999 from env, got %q", cfg.Server.Addr)
	}
	if cfg.Server.PoolMax != 10 {
		t.Errorf("expected 10 from default, got %d", cfg.Server.PoolMax)
	}
}
