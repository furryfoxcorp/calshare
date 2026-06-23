package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clearEnv removes every CALDAV_ var so a test starts from a known baseline.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		if len(e) >= 7 && e[:7] == "CALDAV_" {
			k := e[:indexByte(e, '=')]
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return len(s)
}

func TestDefaults(t *testing.T) {
	clearEnv(t)
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", c.ListenAddr)
	}
	if c.DBPath != "/var/lib/caldav-share/db.sqlite" {
		t.Errorf("DBPath = %q", c.DBPath)
	}
	if c.OIDCScopes != "openid profile email" {
		t.Errorf("OIDCScopes = %q", c.OIDCScopes)
	}
	if c.ICSDefaultPollInterval != 15*time.Minute {
		t.Errorf("ICSDefaultPollInterval = %v", c.ICSDefaultPollInterval)
	}
	if !c.SchedulingEnabled {
		t.Errorf("SchedulingEnabled = false, want true")
	}
	if c.SMTPPort != 587 {
		t.Errorf("SMTPPort = %d, want 587", c.SMTPPort)
	}
	if c.IMAPPort != 993 {
		t.Errorf("IMAPPort = %d, want 993", c.IMAPPort)
	}
	if c.IMAPFolder != "INBOX" {
		t.Errorf("IMAPFolder = %q, want INBOX", c.IMAPFolder)
	}
	if c.IMAPPollInterval != 60*time.Second {
		t.Errorf("IMAPPollInterval = %v", c.IMAPPollInterval)
	}
	if c.DefaultTZ != "UTC" {
		t.Errorf("DefaultTZ = %q", c.DefaultTZ)
	}
	if !c.TrustProxyHeaders {
		t.Errorf("TrustProxyHeaders = false, want true")
	}
	if !c.AutoMigrate {
		t.Errorf("AutoMigrate = false, want true")
	}
}

func TestEnvOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("CALDAV_LISTEN_ADDR", ":9000")
	t.Setenv("CALDAV_ICS_DEFAULT_POLL_INTERVAL", "5m")
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != ":9000" {
		t.Errorf("ListenAddr = %q, want :9000", c.ListenAddr)
	}
	if c.ICSDefaultPollInterval != 5*time.Minute {
		t.Errorf("poll interval = %v, want 5m", c.ICSDefaultPollInterval)
	}
}

func TestFlagBeatsEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("CALDAV_LISTEN_ADDR", ":9000")
	c, err := Load(map[string]string{"listen-addr": ":7777"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != ":7777" {
		t.Errorf("ListenAddr = %q, want :7777 (flag wins)", c.ListenAddr)
	}
}

func TestValidateRequiresExternalURLAndOIDC(t *testing.T) {
	clearEnv(t)
	c, _ := Load(nil)
	if err := c.Validate(); err == nil {
		t.Fatal("Validate passed with no external URL or OIDC config")
	}

	c.ExternalURL = "https://calendar.example"
	c.OIDCIssuer = "https://issuer.example"
	c.OIDCClientID = "id"
	c.OIDCClientSecret = "secret"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate failed with required fields set: %v", err)
	}
}

func TestRedirectURLDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("CALDAV_EXTERNAL_URL", "https://calendar.example")
	t.Setenv("CALDAV_OIDC_ISSUER", "https://issuer.example")
	t.Setenv("CALDAV_OIDC_CLIENT_ID", "id")
	t.Setenv("CALDAV_OIDC_CLIENT_SECRET", "secret")
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := "https://calendar.example/oidc/callback"
	if c.OIDCRedirectURL != want {
		t.Errorf("OIDCRedirectURL = %q, want %q", c.OIDCRedirectURL, want)
	}
}

func TestReplyAddressRequiredWithSMTP(t *testing.T) {
	clearEnv(t)
	c, _ := Load(nil)
	c.ExternalURL = "https://calendar.example"
	c.OIDCIssuer = "https://issuer.example"
	c.OIDCClientID = "id"
	c.OIDCClientSecret = "secret"
	c.SMTPHost = "smtp.example"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate passed with SMTP host but no reply address")
	}
	c.IMIPReplyAddress = "replies@example.com"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate failed with reply address set: %v", err)
	}
}

func TestTOMLLayer(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "listen_addr = \":6060\"\ndefault_tz = \"America/New_York\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// env should still beat TOML
	t.Setenv("CALDAV_DEFAULT_TZ", "Europe/London")
	c, err := Load(map[string]string{"config": path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != ":6060" {
		t.Errorf("ListenAddr = %q, want :6060 (from TOML)", c.ListenAddr)
	}
	if c.DefaultTZ != "Europe/London" {
		t.Errorf("DefaultTZ = %q, want Europe/London (env beats TOML)", c.DefaultTZ)
	}
}
