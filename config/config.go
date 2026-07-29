package config

import (
	"fmt"
	"os"
	"regexp"

	"github.com/BurntSushi/toml"
)

// Config holds all loki-lite configuration.
type Config struct {
	Journal JournalConfig `toml:"journal"`
	Schema  SchemaConfig  `toml:"schema"`
	Server  ServerConfig  `toml:"server"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	// Addr is the listen address.
	// Default: :3100
	Addr string `toml:"addr"`
	// PoolMax is the maximum number of pooled journal connections.
	// Default: 10
	PoolMax int `toml:"pool_max"`
}

// JournalConfig holds journal-related configuration.
type JournalConfig struct {
	// Dir is the path to the journald directory.
	// Default: /var/log/journal
	Dir string `toml:"dir"`
	// Name is an optional journal name prefix (empty = all).
	// Default: ""
	Name string `toml:"name"`
}

// SchemaConfig holds label schema configuration.
type SchemaConfig struct {
	// Exclude lists journal fields to exclude from stream labels.
	// Default: [MESSAGE, SYSLOG_TIMESTAMP, _SOURCE_MONOTONIC_TIMESTAMP, _SOURCE_REALTIME_TIMESTAMP]
	Exclude []string `toml:"exclude"`
}

// defaultConfig returns the default configuration.
func defaultConfig() Config {
	return Config{
		Journal: JournalConfig{
			Dir:  "/var/log/journal",
			Name: "",
		},
		Schema: SchemaConfig{
			Exclude: []string{
				"MESSAGE",
				"SYSLOG_TIMESTAMP",
				"_SOURCE_MONOTONIC_TIMESTAMP",
				"_SOURCE_REALTIME_TIMESTAMP",
			},
		},
		Server: ServerConfig{
			Addr:    ":3100",
			PoolMax: 10,
		},
	}
}

// Load reads a TOML file, substitutes ${VAR:-default} environment variable
// references, and returns the parsed configuration. If path is empty, returns
// the default configuration. Missing keys use their default values.
func Load(path string) (*Config, error) {
	cfg := defaultConfig()
	if path == "" {
		return &cfg, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	resolved := substEnv(string(raw))

	if err := toml.Unmarshal([]byte(resolved), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// substEnv replaces ${VAR:-default} patterns with environment variable values.
// If VAR is set and non-empty, the pattern is replaced with its value.
// Otherwise, the default value is used.
func substEnv(s string) string {
	return envSub.ReplaceAllStringFunc(s, func(match string) string {
		parts := envSub.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		name := parts[1]
		def := parts[2]

		val, ok := os.LookupEnv(name)
		if ok && val != "" {
			return val
		}
		return def
	})
}

var envSub = regexp.MustCompile(`\$\{([^:}-]+):-([^}]*)\}`)
