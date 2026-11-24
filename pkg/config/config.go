// Package config loads the hub configuration from defaults, a YAML file,
// environment variables and flags (in that order).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config is the runtime configuration of the hub.
type Config struct {
	Database  string `mapstructure:"database"`
	Listen    string `mapstructure:"listen"`
	APITokens string `mapstructure:"api_tokens"`
	BatchSize int    `mapstructure:"batch_size"`
	LogLevel  string `mapstructure:"log_level"`

	// AttachmentsDir is where uploaded attachment blobs are stored. When empty
	// it is derived from the database location.
	AttachmentsDir string `mapstructure:"attachments_dir"`

	// APIURL and APIToken configure the CLI client (query/get). When APIURL is
	// empty it is derived from Listen.
	APIURL   string `mapstructure:"api_url"`
	APIToken string `mapstructure:"api_token"`
}

// Load reads the configuration. path may be empty, in which case the default
// locations are searched.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix("COMMHUB")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("database", "commhub.db")
	v.SetDefault("listen", "127.0.0.1:8080")
	v.SetDefault("api_tokens", "")
	v.SetDefault("batch_size", 500)
	v.SetDefault("log_level", "info")
	v.SetDefault("attachments_dir", "")
	v.SetDefault("api_url", "")
	v.SetDefault("api_token", "")

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("commhub")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")

		if dir, err := os.UserConfigDir(); err == nil {
			v.AddConfigPath(filepath.Join(dir, "commhub"))
		}

		v.AddConfigPath("$HOME/.config/commhub")
		v.AddConfigPath("/etc/commhub")
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if path != "" || !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	return cfg, nil
}

// TokenSpec is one configured API token with the scopes it grants. A bare token
// grants read and write; "read:<token>" and "write:<token>" restrict it.
type TokenSpec struct {
	Value string
	Read  bool
	Write bool
}

// ParseTokenSpec parses one api_tokens entry. Unknown prefixes are treated as
// part of the token, so existing random tokens keep working.
func ParseTokenSpec(raw string) TokenSpec {
	raw = strings.TrimSpace(raw)

	scope, value, ok := strings.Cut(raw, ":")
	if !ok {
		return TokenSpec{Value: raw, Read: true, Write: true}
	}

	switch strings.ToLower(scope) {
	case "read":
		return TokenSpec{Value: strings.TrimSpace(value), Read: true}
	case "write":
		return TokenSpec{Value: strings.TrimSpace(value), Write: true}
	case "rw", "all":
		return TokenSpec{Value: strings.TrimSpace(value), Read: true, Write: true}
	default:
		return TokenSpec{Value: raw, Read: true, Write: true}
	}
}

// SplitTokens splits the comma-separated api_tokens setting.
func SplitTokens(raw string) []string {
	parts := strings.Split(raw, ",")
	tokens := make([]string, 0, len(parts))

	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			tokens = append(tokens, part)
		}
	}

	return tokens
}

// TokenSpecs returns the configured tokens with their scopes.
func (c *Config) TokenSpecs() []TokenSpec {
	raw := SplitTokens(c.APITokens)
	specs := make([]TokenSpec, 0, len(raw))

	for _, entry := range raw {
		if spec := ParseTokenSpec(entry); spec.Value != "" {
			specs = append(specs, spec)
		}
	}

	return specs
}

// Tokens returns the configured bearer token values, without scope prefixes. An
// empty list disables auth.
func (c *Config) Tokens() []string {
	specs := c.TokenSpecs()
	tokens := make([]string, 0, len(specs))

	for _, spec := range specs {
		tokens = append(tokens, spec.Value)
	}

	return tokens
}

// BlobDir returns the directory for attachment blobs. It defaults to an
// "attachments" directory next to the database. In-memory and DSN databases
// have no location to derive it from; the caller then gets an empty string.
func (c *Config) BlobDir() string {
	if dir := strings.TrimSpace(c.AttachmentsDir); dir != "" {
		return expandHome(dir)
	}

	if c.Database == ":memory:" || strings.HasPrefix(c.Database, "file:") {
		return ""
	}

	return filepath.Join(filepath.Dir(c.Database), "attachments")
}

// UnixSocketPath reports the socket path when address is a unix listen address.
// Both "unix:/run/x.sock" and "unix:///run/x.sock" are accepted.
func UnixSocketPath(address string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(address), "unix:")
	if !ok {
		return "", false
	}

	rest = strings.TrimPrefix(rest, "//")
	if rest == "" {
		return "", false
	}

	return rest, true
}

// APIBaseURL returns the URL the CLI should talk to. An explicit api_url wins;
// otherwise the listen address is used, with wildcard binds mapped to
// localhost. A unix listen address becomes "unix:<path>".
func (c *Config) APIBaseURL() string {
	if url := strings.TrimRight(strings.TrimSpace(c.APIURL), "/"); url != "" {
		return url
	}

	if path, ok := UnixSocketPath(c.Listen); ok {
		return "unix:" + path
	}

	host, port, err := net.SplitHostPort(strings.TrimSpace(c.Listen))
	if err != nil {
		return "http://127.0.0.1:8080"
	}

	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port)
}

// ClientToken returns the bearer token for the CLI: api_token when set,
// otherwise the first server token (they run from the same config by default).
func (c *Config) ClientToken() string {
	if token := strings.TrimSpace(c.APIToken); token != "" {
		return token
	}

	tokens := c.Tokens()
	if len(tokens) == 0 {
		return ""
	}

	return tokens[0]
}

// Level returns the configured slog level.
func (c *Config) Level() slog.Level {
	switch strings.ToLower(strings.TrimSpace(c.LogLevel)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}

	return path
}
