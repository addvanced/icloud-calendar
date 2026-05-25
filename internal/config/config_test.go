package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresStrictPermissions(t *testing.T) {
	clearAuthEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `[auth]
apple_id="k@example.com"
app_password="pw"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("expected permissions error, got %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sync.RangeYears != 2 || cfg.Output.DateFormat == "" || cfg.Output.Timezone == "" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestSaveWritesStrictPermissions(t *testing.T) {
	clearAuthEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	err := Save(path, Config{Auth: AuthConfig{AppleID: "k@example.com", AppPassword: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("got permissions %03o", st.Mode().Perm())
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AppleID != "k@example.com" || cfg.Sync.RangeYears != 2 {
		t.Fatalf("bad config %+v", cfg)
	}
}

func TestSaveValidatesRequiredAuth(t *testing.T) {
	clearAuthEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	err := Save(path, Config{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid config") || !strings.Contains(err.Error(), "auth.apple_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("invalid config should not be written, stat err=%v", statErr)
	}
}

func TestSaveFixesExistingPermissionsBeforeWrite(t *testing.T) {
	clearAuthEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Config{Auth: AuthConfig{AppleID: "k@example.com", AppPassword: "pw"}}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("got permissions %03o", st.Mode().Perm())
	}
}

func TestLoadRequiresAuthFields(t *testing.T) {
	clearAuthEnv(t)
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "missing apple id",
			content: `[auth]
app_password="pw"
`,
			wantErr: "auth.apple_id is required",
		},
		{
			name: "missing app password",
			content: `[auth]
apple_id="k@example.com"
`,
			wantErr: "auth.app_password is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.content, 0o600)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadHandlesMalformedTOML(t *testing.T) {
	path := writeConfig(t, "this is not toml (((", 0o600)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("err = %v, want parse config error", err)
	}
}

func TestLoadHandlesMissingFile(t *testing.T) {
	clearAuthEnv(t)
	path := filepath.Join(t.TempDir(), "does-not-exist.toml")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "stat config") {
		t.Fatalf("err = %v, want stat config error", err)
	}
}

func TestLoadPermissionVariants(t *testing.T) {
	content := `[auth]
apple_id="k@example.com"
app_password="pw"
`
	cases := []struct {
		name    string
		perm    os.FileMode
		wantErr bool
	}{
		{name: "0600 strict", perm: 0o600},
		{name: "0400 owner read only", perm: 0o400},
		{name: "0644 group and other readable", perm: 0o644, wantErr: true},
		{name: "0640 group readable", perm: 0o640, wantErr: true},
		{name: "0666 world writable", perm: 0o666, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, content, tc.perm)
			_, err := Load(path)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "permissions") {
					t.Fatalf("err = %v, want permissions error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	clearAuthEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	original := Config{
		Auth: AuthConfig{
			AppleID:     "k@example.com",
			AppPassword: "secret",
		},
		Sync: SyncConfig{
			RangeYears:               5,
			AutoSyncThresholdMinutes: 30,
		},
		Output: OutputConfig{
			DateFormat: "2006-01-02",
			Timezone:   "UTC",
		},
	}
	if err := Save(path, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != original {
		t.Fatalf("roundtrip mismatch:\noriginal: %+v\nloaded:   %+v", original, loaded)
	}
}

func TestDefaultPathLoadAndSaveWithDefaultPath(t *testing.T) {
	clearAuthEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, ".config", "icalendar", "config.toml")
	if path != wantPath {
		t.Fatalf("DefaultPath() = %q, want %q", path, wantPath)
	}
	cfg := Config{Auth: AuthConfig{AppleID: "k@example.com", AppPassword: "pw"}}
	if err := Save("", cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Auth.AppleID != cfg.Auth.AppleID || loaded.Sync.RangeYears != 2 {
		t.Fatalf("loaded default config = %+v", loaded)
	}
}

func TestLoadReportsReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config-dir.toml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("err = %v, want read config error", err)
	}
}

func TestSaveReportsCreateDirError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Save(filepath.Join(parent, "config.toml"), Config{Auth: AuthConfig{AppleID: "k@example.com", AppPassword: "pw"}})
	if err == nil || !strings.Contains(err.Error(), "create config dir") {
		t.Fatalf("err = %v, want create config dir error", err)
	}
}

func TestSaveReportsCreateTempError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	err := Save(filepath.Join(dir, "config.toml"), Config{Auth: AuthConfig{AppleID: "k@example.com", AppPassword: "pw"}})
	if err == nil {
		t.Skip("directory permissions did not block temp file creation in this environment")
	}
	if !strings.Contains(err.Error(), "create temp config") {
		t.Fatalf("err = %v, want create temp config error", err)
	}
}

// clearAuthEnv prevents inherited ICALENDAR_APPLE_ID/PASSWORD env vars from
// leaking into tests that don't intentionally use them.
func clearAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvAppleID, "")
	t.Setenv(EnvPassword, "")
}

func writeConfig(t *testing.T, content string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEnvOverridesAppleID(t *testing.T) {
	content := `[auth]
apple_id="from-file@example.com"
app_password="pw"
`
	path := writeConfig(t, content, 0o600)
	t.Setenv(EnvAppleID, "from-env@example.com")
	t.Setenv(EnvPassword, "") // ensure no external env leaks into the other field

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AppleID != "from-env@example.com" {
		t.Fatalf("env var did not override TOML apple_id: got %q", cfg.Auth.AppleID)
	}
	if cfg.Auth.AppPassword != "pw" {
		t.Fatalf("unaffected field changed unexpectedly: got %q", cfg.Auth.AppPassword)
	}
}

func TestLoadEnvOverridesAppPassword(t *testing.T) {
	content := `[auth]
apple_id="k@example.com"
app_password="from-file"
`
	path := writeConfig(t, content, 0o600)
	t.Setenv(EnvAppleID, "") // ensure no external env leaks into the other field
	t.Setenv(EnvPassword, "from-env")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AppPassword != "from-env" {
		t.Fatalf("env var did not override TOML app_password: got %q", cfg.Auth.AppPassword)
	}
	if cfg.Auth.AppleID != "k@example.com" {
		t.Fatalf("unaffected field changed unexpectedly: got %q", cfg.Auth.AppleID)
	}
}

func TestLoadEmptyEnvDoesNotOverrideFile(t *testing.T) {
	content := `[auth]
apple_id="k@example.com"
app_password="pw"
`
	path := writeConfig(t, content, 0o600)
	t.Setenv(EnvAppleID, "")
	t.Setenv(EnvPassword, "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AppleID != "k@example.com" || cfg.Auth.AppPassword != "pw" {
		t.Fatalf("empty env vars overrode file: %+v", cfg.Auth)
	}
}

func TestLoadMissingFileWithEnvVarsSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.toml")
	t.Setenv(EnvAppleID, "env@example.com")
	t.Setenv(EnvPassword, "env-pw")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (env vars should supply creds)", err)
	}
	if cfg.Auth.AppleID != "env@example.com" || cfg.Auth.AppPassword != "env-pw" {
		t.Fatalf("env-only auth not applied: %+v", cfg.Auth)
	}
	// Defaults must still apply for non-auth fields when no file exists.
	if cfg.Sync.RangeYears != 2 || cfg.Output.Timezone == "" {
		t.Fatalf("defaults not applied without file: %+v", cfg)
	}
}

func TestDetectSystemTimezoneFromTZEnv(t *testing.T) {
	t.Setenv("TZ", "Europe/Berlin")
	if got := DetectSystemTimezone(); got != "Europe/Berlin" {
		t.Fatalf("DetectSystemTimezone() = %q, want Europe/Berlin", got)
	}
}

func TestDetectSystemTimezoneStripsPOSIXLeadingColon(t *testing.T) {
	// POSIX TZ=":Europe/Berlin" should be normalised to the bare IANA name
	// so callers can hand it straight to time.LoadLocation.
	t.Setenv("TZ", ":Europe/Berlin")
	got := DetectSystemTimezone()
	if got != "Europe/Berlin" {
		t.Fatalf("DetectSystemTimezone() = %q, want Europe/Berlin (POSIX colon must be stripped)", got)
	}
	if _, err := time.LoadLocation(got); err != nil {
		t.Fatalf("returned value %q is not loadable: %v", got, err)
	}
}

func TestDetectSystemTimezoneRejectsInvalidTZ(t *testing.T) {
	// An obviously bogus TZ must not be returned verbatim — it would break
	// every downstream time.LoadLocation call.
	t.Setenv("TZ", "Europ/Berlin")
	got := DetectSystemTimezone()
	if got == "Europ/Berlin" {
		t.Fatal("DetectSystemTimezone() returned the invalid TZ verbatim")
	}
	if _, err := time.LoadLocation(got); err != nil {
		t.Fatalf("fallback value %q is itself not loadable: %v", got, err)
	}
}

func TestDetectSystemTimezoneFallsBack(t *testing.T) {
	t.Setenv("TZ", "")
	got := DetectSystemTimezone()
	// On dev machines we can't reliably predict the exact value (it could be
	// any IANA name from /etc/localtime or "UTC"). Just check the contract:
	// non-empty, loadable by time.LoadLocation.
	if got == "" {
		t.Fatal("DetectSystemTimezone() returned empty string")
	}
	if _, err := time.LoadLocation(got); err != nil {
		t.Fatalf("DetectSystemTimezone() = %q, not loadable: %v", got, err)
	}
}

func TestValidateRejectsInvalidTimezone(t *testing.T) {
	clearAuthEnv(t)
	content := `[auth]
apple_id="k@example.com"
app_password="pw"

[output]
timezone="Europ/Berlin"
`
	path := writeConfig(t, content, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for bogus timezone")
	}
	if !strings.Contains(err.Error(), "output.timezone") {
		t.Fatalf("err = %v, want output.timezone validation error", err)
	}
}

func TestValidateAcceptsValidTimezone(t *testing.T) {
	clearAuthEnv(t)
	content := `[auth]
apple_id="k@example.com"
app_password="pw"

[output]
timezone="America/Los_Angeles"
`
	path := writeConfig(t, content, 0o600)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Output.Timezone != "America/Los_Angeles" {
		t.Fatalf("Output.Timezone = %q", cfg.Output.Timezone)
	}
}

func TestApplyDefaultsUsesISODateFormat(t *testing.T) {
	clearAuthEnv(t)
	content := `[auth]
apple_id="k@example.com"
app_password="pw"
`
	path := writeConfig(t, content, 0o600)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.DateFormat != "2006-01-02 15:04" {
		t.Fatalf("default date format = %q, want ISO 2006-01-02 15:04", cfg.Output.DateFormat)
	}
}

func TestLoadMissingFileWithPartialEnvFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.toml")
	t.Setenv(EnvAppleID, "env@example.com")
	t.Setenv(EnvPassword, "") // only one of the two set

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error when file missing and only one env var set")
	}
	if !strings.Contains(err.Error(), "stat config") {
		t.Fatalf("err = %v, want stat config error", err)
	}
}
