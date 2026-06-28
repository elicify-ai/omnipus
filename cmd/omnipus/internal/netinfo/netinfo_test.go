package netinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAccessURLs(t *testing.T) {
	lan := []string{"192.168.1.42", "10.0.0.5"}

	tests := []struct {
		name      string
		host      string
		port      int
		publicURL string
		ipv4s     []string
		// expected fields
		wantPrimary     string
		wantExtrasCount int
		wantExtras      []string // nil = don't check individual extras
		wantHint        string
	}{
		{
			name:            "0.0.0.0 no public_url → localhost + LAN IPs",
			host:            "0.0.0.0",
			port:            5000,
			publicURL:       "",
			ipv4s:           lan,
			wantPrimary:     "http://localhost:5000",
			wantExtrasCount: 2,
			wantExtras:      []string{"http://192.168.1.42:5000", "http://10.0.0.5:5000"},
			wantHint:        "",
		},
		{
			name:            "empty host no public_url → localhost + LAN IPs",
			host:            "",
			port:            8080,
			publicURL:       "",
			ipv4s:           lan,
			wantPrimary:     "http://localhost:8080",
			wantExtrasCount: 2,
			wantExtras:      []string{"http://192.168.1.42:8080", "http://10.0.0.5:8080"},
			wantHint:        "",
		},
		{
			name:            "127.0.0.1 → localhost only + hint",
			host:            "127.0.0.1",
			port:            5000,
			publicURL:       "",
			ipv4s:           lan,
			wantPrimary:     "http://localhost:5000",
			wantExtrasCount: 0,
			wantHint:        "bound to localhost",
		},
		{
			name:            "localhost string → localhost only + hint",
			host:            "localhost",
			port:            5000,
			publicURL:       "",
			ipv4s:           lan,
			wantPrimary:     "http://localhost:5000",
			wantExtrasCount: 0,
			wantHint:        "bound to localhost",
		},
		{
			name:            "public_url set → canonical only, no extras, no hint",
			host:            "0.0.0.0",
			port:            5000,
			publicURL:       "https://omni.example.com",
			ipv4s:           lan,
			wantPrimary:     "https://omni.example.com",
			wantExtrasCount: 0,
			wantHint:        "",
		},
		{
			name:            "public_url set with loopback host → still canonical",
			host:            "127.0.0.1",
			port:            5000,
			publicURL:       "https://x.example",
			ipv4s:           lan,
			wantPrimary:     "https://x.example",
			wantExtrasCount: 0,
			wantHint:        "",
		},
		{
			name:            "explicit host (IP) → http://<host>:port",
			host:            "10.1.2.3",
			port:            9000,
			publicURL:       "",
			ipv4s:           lan,
			wantPrimary:     "http://10.1.2.3:9000",
			wantExtrasCount: 0,
			wantHint:        "",
		},
		{
			name:            "0.0.0.0 with no LAN IPs → localhost only (empty extras)",
			host:            "0.0.0.0",
			port:            5000,
			publicURL:       "",
			ipv4s:           nil,
			wantPrimary:     "http://localhost:5000",
			wantExtrasCount: 0,
			wantHint:        "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildAccessURLs(tc.host, tc.port, tc.publicURL, tc.ipv4s)

			if got.Primary != tc.wantPrimary {
				t.Errorf("Primary = %q, want %q", got.Primary, tc.wantPrimary)
			}
			if len(got.Extras) != tc.wantExtrasCount {
				t.Errorf("len(Extras) = %d, want %d (extras: %v)", len(got.Extras), tc.wantExtrasCount, got.Extras)
			}
			// Check specific extras when provided.
			if tc.wantExtras != nil {
				for i, want := range tc.wantExtras {
					if i >= len(got.Extras) {
						t.Errorf("Extras[%d] missing, want %q", i, want)
						continue
					}
					if got.Extras[i] != want {
						t.Errorf("Extras[%d] = %q, want %q", i, got.Extras[i], want)
					}
				}
			}
			if tc.wantHint == "" && got.Hint != "" {
				t.Errorf("Hint = %q, want empty", got.Hint)
			}
			if tc.wantHint != "" && !strings.Contains(got.Hint, tc.wantHint) {
				t.Errorf("Hint = %q, want contains %q", got.Hint, tc.wantHint)
			}
		})
	}
}

func TestRender_IncludesAllFields(t *testing.T) {
	t.Run("localhost+LAN+no-hint", func(t *testing.T) {
		u := AccessURLs{
			Primary: "http://localhost:5000",
			Extras:  []string{"http://192.168.1.42:5000"},
		}
		out := Render(u)
		if !strings.Contains(out, "http://localhost:5000") {
			t.Errorf("Render missing primary URL")
		}
		if !strings.Contains(out, "http://192.168.1.42:5000") {
			t.Errorf("Render missing extra URL")
		}
		if strings.Contains(out, "Hint") {
			t.Errorf("Render printed hint when none was set")
		}
	})

	t.Run("loopback+hint", func(t *testing.T) {
		u := AccessURLs{
			Primary: "http://localhost:5000",
			Hint:    "bound to localhost — set gateway.host=0.0.0.0 to allow other devices",
		}
		out := Render(u)
		if !strings.Contains(out, "http://localhost:5000") {
			t.Errorf("Render missing primary URL")
		}
		if !strings.Contains(out, "bound to localhost") {
			t.Errorf("Render missing hint")
		}
		if strings.Contains(out, "On your network") {
			t.Errorf("Render printed extras when none were set")
		}
	})

	t.Run("canonical-public-url", func(t *testing.T) {
		u := AccessURLs{Primary: "https://omni.example.com"}
		out := Render(u)
		if !strings.Contains(out, "https://omni.example.com") {
			t.Errorf("Render missing canonical URL")
		}
	})
}

// TestAccessURLsFromConfig verifies the de-duplicated helper that reads
// host/port/public_url from config.json and calls BuildAccessURLs.
func TestAccessURLsFromConfig(t *testing.T) {
	t.Run("reads host port and public_url", func(t *testing.T) {
		dir := t.TempDir()
		cfg := map[string]any{
			"gateway": map[string]any{
				"host":       "127.0.0.1",
				"port":       float64(9090),
				"public_url": "",
			},
		}
		writeCfg(t, dir, cfg)
		urls := AccessURLsFromConfig(filepath.Join(dir, "config.json"))
		// host=127.0.0.1 → loopback rule → localhost only + hint.
		if urls.Primary != "http://localhost:9090" {
			t.Errorf("Primary = %q, want http://localhost:9090", urls.Primary)
		}
		if !strings.Contains(urls.Hint, "bound to localhost") {
			t.Errorf("expected loopback hint, got %q", urls.Hint)
		}
	})

	t.Run("uses public_url when set", func(t *testing.T) {
		dir := t.TempDir()
		cfg := map[string]any{
			"gateway": map[string]any{
				"port":       float64(5000),
				"public_url": "https://omni.example.com",
			},
		}
		writeCfg(t, dir, cfg)
		urls := AccessURLsFromConfig(filepath.Join(dir, "config.json"))
		if urls.Primary != "https://omni.example.com" {
			t.Errorf("Primary = %q, want https://omni.example.com", urls.Primary)
		}
	})

	t.Run("missing config falls back to defaults", func(t *testing.T) {
		urls := AccessURLsFromConfig("/nonexistent/path/config.json")
		// Default: host="" port=5000 → 0.0.0.0 rule → localhost.
		if !strings.HasPrefix(urls.Primary, "http://localhost:5000") {
			t.Errorf("Primary = %q, want http://localhost:5000", urls.Primary)
		}
	})
}

// writeCfg is a test helper that writes a config map as JSON to
// <dir>/config.json.
func writeCfg(t *testing.T, dir string, cfg map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
}
