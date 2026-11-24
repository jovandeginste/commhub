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

// Tokens returns the configured bearer tokens. An empty list disables auth.
func (c *Config) Tokens() []string {
	if strings.TrimSpace(c.APITokens) == "" {
		return nil
	}

	tokens := strings.Split(c.APITokens, ",")
	result := make([]string, 0, len(tokens))

	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" {
			result = append(result, token)
		}
	}

	return result
}

// APIBaseURL returns the URL the CLI should talk to. An explicit api_url wins;
// otherwise the listen address is used, with wildcard binds mapped to
// localhost.
func (c *Config) APIBaseURL() string {
	if url := strings.TrimRight(strings.TrimSpace(c.APIURL), "/"); url != "" {
		return url
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
