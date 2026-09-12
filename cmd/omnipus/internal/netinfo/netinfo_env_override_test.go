package netinfo

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig drops a minimal gateway config at a temp path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// TestAccessURLsFromConfig_EnvOverridesFile pins the fix for a banner that
// told the operator to open the wrong URL.
//
// pkg/config applies OMNIPUS_GATEWAY_PORT over the file value via its `env`
// struct tag, so the gateway listens on the env port. This function is a
// SECOND, independent reader of the same setting and used to read only the
// file — so the start-up banner printed the file's port (or the 5000
// default) while the gateway served somewhere else. On macOS, port 5000 is
// held by ControlCenter's AirPlay Receiver, so the printed URL answered 403
// from an unrelated process and the gateway looked broken while running
// perfectly one port over.
func TestAccessURLsFromConfig_EnvOverridesFile(t *testing.T) {
	cfg := writeConfig(t, `{"gateway":{"host":"0.0.0.0","port":5000}}`)

	t.Setenv("OMNIPUS_GATEWAY_PORT", "5055")
	got := AccessURLsFromConfig(cfg)

	if got.Primary != "http://localhost:5055" {
		t.Fatalf("Primary = %q, want http://localhost:5055 — the banner must print the port the gateway actually binds", got.Primary)
	}
}

// TestAccessURLsFromConfig_EnvHostAndPublicURLOverrideFile covers the other
// two settings that carry env tags, so a future edit cannot quietly restore
// file-only reads for them.
func TestAccessURLsFromConfig_EnvHostAndPublicURLOverrideFile(t *testing.T) {
	cfg := writeConfig(t, `{"gateway":{"host":"0.0.0.0","port":5000}}`)

	t.Setenv("OMNIPUS_GATEWAY_PUBLIC_URL", "https://omnipus.example")
	if got := AccessURLsFromConfig(cfg); got.Primary != "https://omnipus.example" {
		t.Fatalf("Primary = %q, want the env public_url", got.Primary)
	}

	cfg2 := writeConfig(t, `{"gateway":{"host":"0.0.0.0","port":5000}}`)
	t.Setenv("OMNIPUS_GATEWAY_PUBLIC_URL", "")
	t.Setenv("OMNIPUS_GATEWAY_HOST", "10.1.2.3")
	if got := AccessURLsFromConfig(cfg2); got.Primary != "http://10.1.2.3:5000" {
		t.Fatalf("Primary = %q, want http://10.1.2.3:5000", got.Primary)
	}
}

// TestAccessURLsFromConfig_MalformedEnvPortIsIgnored: a bad value must not
// invent a port. The gateway's own parse rejects it and refuses to start, so
// printing a made-up number here would be the same lie in a new place.
func TestAccessURLsFromConfig_MalformedEnvPortIsIgnored(t *testing.T) {
	cfg := writeConfig(t, `{"gateway":{"host":"0.0.0.0","port":5123}}`)

	for _, bad := range []string{"not-a-number", "0", "-1", "70000"} {
		t.Setenv("OMNIPUS_GATEWAY_PORT", bad)
		if got := AccessURLsFromConfig(cfg); got.Primary != "http://localhost:5123" {
			t.Fatalf("OMNIPUS_GATEWAY_PORT=%q gave Primary %q, want the file's 5123 retained", bad, got.Primary)
		}
	}
}
