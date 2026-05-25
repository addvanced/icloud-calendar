package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Env var names that override Auth fields in config.toml.
// Set by Hermes plugin install via the `requires_env` manifest block.
const (
	EnvAppleID  = "ICALENDAR_APPLE_ID"
	EnvPassword = "ICALENDAR_PASSWORD"
)

type Config struct {
	Auth   AuthConfig   `toml:"auth"`
	Sync   SyncConfig   `toml:"sync"`
	Output OutputConfig `toml:"output"`
}

type AuthConfig struct {
	AppleID     string `toml:"apple_id"`
	AppPassword string `toml:"app_password"`
}

type SyncConfig struct {
	RangeYears               int `toml:"range_years"`
	AutoSyncThresholdMinutes int `toml:"auto_sync_threshold_minutes"`
}

type OutputConfig struct {
	DateFormat string `toml:"date_format"`
	Timezone   string `toml:"timezone"`
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "icalendar", "config.toml"), nil
}

// DetectSystemTimezone returns the system's IANA TZ name, falling back to
// "UTC" if it can't be detected. Every candidate is validated against
// time.LoadLocation, so the return value is always safe to pass to it. Used
// as the setup-wizard default so we don't ship a Copenhagen-centric
// assumption to a worldwide audience.
//
// POSIX permits TZ=":Europe/Berlin" (with a leading colon) — the colon
// instructs the runtime to look up the rest in zoneinfo. We strip the colon
// before validating, since Go's time package expects a bare IANA name.
func DetectSystemTimezone() string {
	candidates := []string{
		strings.TrimPrefix(os.Getenv("TZ"), ":"),
	}
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		candidates = append(candidates, strings.TrimSpace(string(data)))
	}
	// macOS and many Linux distros symlink /etc/localtime into the zoneinfo db.
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		const marker = "zoneinfo/"
		if idx := strings.Index(target, marker); idx >= 0 {
			candidates = append(candidates, target[idx+len(marker):])
		}
	}

	for _, name := range candidates {
		if name == "" {
			continue
		}
		if _, err := time.LoadLocation(name); err == nil {
			return name
		}
	}
	return "UTC"
}

func Load(path string) (Config, error) {
	var cfg Config
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return cfg, err
		}
		path = p
	}
	fileLoaded, err := loadFromFile(path, &cfg)
	if err != nil {
		return cfg, err
	}
	applyEnvOverrides(&cfg)
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		if !fileLoaded {
			return cfg, fmt.Errorf(
				"no config file at %q and env vars (%s/%s) did not supply required fields: %w",
				path, EnvAppleID, EnvPassword, err,
			)
		}
		return cfg, fmt.Errorf("invalid config %q: %w", path, err)
	}
	return cfg, nil
}

// loadFromFile reads config.toml into cfg if it exists. Returns (loaded, err).
// When the file does not exist but both auth env vars are set, returns
// (false, nil) so Load can proceed with env-var-only credentials.
func loadFromFile(path string, cfg *Config) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if os.Getenv(EnvAppleID) != "" && os.Getenv(EnvPassword) != "" {
				return false, nil
			}
		}
		return false, fmt.Errorf("stat config %q: %w", path, err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return false, fmt.Errorf("config %q permissions must be 600 or stricter, got %03o", path, st.Mode().Perm())
	}
	// #nosec G304 -- path is either the fixed default config path or an explicit test path.
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read config %q: %w", path, err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return false, fmt.Errorf("parse config %q: %w", path, err)
	}
	return true, nil
}

// applyEnvOverrides lets ICALENDAR_APPLE_ID/ICALENDAR_PASSWORD override the
// TOML values. Hermes-managed installs store credentials in the gateway's
// .env file via the plugin manifest's requires_env block; that path always
// wins over a stale config.toml.
func applyEnvOverrides(c *Config) {
	if v := os.Getenv(EnvAppleID); v != "" {
		c.Auth.AppleID = v
	}
	if v := os.Getenv(EnvPassword); v != "" {
		c.Auth.AppPassword = v
	}
}

func Save(path string, cfg Config) error {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir %q: %w", filepath.Dir(path), err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("invalid config %q: %w", path, err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config %q: %w", path, err)
	}
	dir := filepath.Dir(path)
	// #nosec G304 -- path is either the fixed default config path or an explicit test path.
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config in %q: %w", dir, err)
	}
	tmpPath := f.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod temp config %q: %w", tmpPath, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp config %q: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp config %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config %q: %w", path, err)
	}
	return nil
}

func (c *Config) validate() error {
	if c.Auth.AppleID == "" {
		return fmt.Errorf("auth.apple_id is required")
	}
	if c.Auth.AppPassword == "" {
		return fmt.Errorf("auth.app_password is required")
	}
	if c.Output.Timezone != "" {
		if _, err := time.LoadLocation(c.Output.Timezone); err != nil {
			return fmt.Errorf("output.timezone %q is not a valid IANA name: %w", c.Output.Timezone, err)
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Sync.RangeYears == 0 {
		c.Sync.RangeYears = 2
	}
	if c.Sync.AutoSyncThresholdMinutes == 0 {
		c.Sync.AutoSyncThresholdMinutes = 15
	}
	if c.Output.DateFormat == "" {
		c.Output.DateFormat = "2006-01-02 15:04"
	}
	if c.Output.Timezone == "" {
		c.Output.Timezone = DetectSystemTimezone()
	}
}
