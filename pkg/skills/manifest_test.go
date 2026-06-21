package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBundleManifest_Valid(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantKind BundleKind
	}{
		{
			name: "full-plugin-pack",
			raw: `{
				"name": "support-desk",
				"version": "1.2.0",
				"signature": "abc123",
				"skills": "./skills/",
				"mcpServers": "./.mcp.json",
				"agents": "./agents/",
				"channels": "./channels.json",
				"providers": "./providers.json",
				"permissions": { "tools": ["web_search"], "egress": ["api.x.com"] }
			}`,
			wantKind: BundleKindPlugin,
		},
		{
			name:     "skill-only-pack",
			raw:      `{"name": "summarize-pack", "version": "0.1.0", "skills": "./skills/"}`,
			wantKind: BundleKindSkill,
		},
		{
			name:     "agent-pack",
			raw:      `{"name": "ops-pack", "version": "2.0.0", "agents": "./agents/"}`,
			wantKind: BundleKindAgent,
		},
		{
			name:     "mcp-only-is-plugin",
			raw:      `{"name": "tool-pack", "version": "1.0.0", "mcpServers": "./.mcp.json"}`,
			wantKind: BundleKindPlugin,
		},
		{
			name:     "prerelease-semver",
			raw:      `{"name": "beta-pack", "version": "1.0.0-rc.1", "skills": "./skills/"}`,
			wantKind: BundleKindSkill,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseBundleManifest([]byte(tc.raw))
			require.NoError(t, err)
			require.NotNil(t, m)
			assert.Equal(t, tc.wantKind, m.Kind())
		})
	}
}

func TestParseBundleManifest_RequiredFieldsMissing(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		errContains string
	}{
		{
			name:        "missing-name",
			raw:         `{"version": "1.0.0", "skills": "./skills/"}`,
			errContains: "name is required",
		},
		{
			name:        "missing-version",
			raw:         `{"name": "x-pack", "skills": "./skills/"}`,
			errContains: "version is required",
		},
		{
			name:        "no-components-unknown-kind",
			raw:         `{"name": "empty-pack", "version": "1.0.0"}`,
			errContains: "declares no components",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseBundleManifest([]byte(tc.raw))
			require.Error(t, err)
			assert.Nil(t, m)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestParseBundleManifest_BadVersion(t *testing.T) {
	cases := []string{
		`{"name": "x-pack", "version": "1.0", "skills": "./skills/"}`,
		`{"name": "x-pack", "version": "v1.0.0", "skills": "./skills/"}`,
		`{"name": "x-pack", "version": "not-a-version", "skills": "./skills/"}`,
		`{"name": "x-pack", "version": "1.0.0.0", "skills": "./skills/"}`,
		`{"name": "x-pack", "version": "01.0.0", "skills": "./skills/"}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			m, err := ParseBundleManifest([]byte(raw))
			require.Error(t, err)
			assert.Nil(t, m)
			assert.Contains(t, err.Error(), "not a valid semantic version")
		})
	}
}

func TestParseBundleManifest_BadName(t *testing.T) {
	longName := strings.Repeat("a", MaxNameLength+1)
	cases := []struct {
		name        string
		raw         string
		errContains string
	}{
		{
			name:        "non-alphanumeric",
			raw:         `{"name": "bad name!", "version": "1.0.0", "skills": "./skills/"}`,
			errContains: "alphanumeric",
		},
		{
			name:        "too-long",
			raw:         `{"name": "` + longName + `", "version": "1.0.0", "skills": "./skills/"}`,
			errContains: "exceeds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseBundleManifest([]byte(tc.raw))
			require.Error(t, err)
			assert.Nil(t, m)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestParseBundleManifest_PathTraversal(t *testing.T) {
	cases := []string{
		`{"name": "x-pack", "version": "1.0.0", "skills": "../../etc"}`,
		`{"name": "x-pack", "version": "1.0.0", "agents": "/abs/path"}`,
		`{"name": "x-pack", "version": "1.0.0", "mcpServers": "../escape/.mcp.json"}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			m, err := ParseBundleManifest([]byte(raw))
			require.Error(t, err)
			assert.Nil(t, m)
			assert.Contains(t, err.Error(), "bundle directory")
		})
	}
}

func TestParseBundleManifest_MalformedJSON(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		errContains string
	}{
		{"not-json", `not json at all`, "parse bundle manifest"},
		{"truncated", `{"name": "x-pack", "version":`, "parse bundle manifest"},
		{
			"unknown-field",
			`{"name": "x-pack", "version": "1.0.0", "skills": "./s/", "bogus": true}`,
			"parse bundle manifest",
		},
		{"trailing-data", `{"name": "x-pack", "version": "1.0.0", "skills": "./s/"}{}`, "trailing data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseBundleManifest([]byte(tc.raw))
			require.Error(t, err)
			assert.Nil(t, m)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestLoadBundleManifest_FromDir(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name": "disk-pack", "version": "3.1.4", "skills": "./skills/"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, BundleManifestFilename), []byte(manifest), 0o600))

	m, err := LoadBundleManifest(dir)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "disk-pack", m.Name)
	assert.Equal(t, "3.1.4", m.Version)
	assert.Equal(t, BundleKindSkill, m.Kind())
}

func TestLoadBundleManifest_Missing(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadBundleManifest(dir)
	require.Error(t, err)
	assert.Nil(t, m)
	assert.Contains(t, err.Error(), "not found")
}

func TestLoadBundleManifest_MalformedOnDisk(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, BundleManifestFilename), []byte(`{"version": "1.0.0"}`), 0o600))
	m, err := LoadBundleManifest(dir)
	require.Error(t, err)
	assert.Nil(t, m)
	assert.Contains(t, err.Error(), "name is required")
	assert.Contains(t, err.Error(), "declares no components")
}

func TestInstallFromGitHub_RejectsBadBundleManifest(t *testing.T) {
	// Directly exercise the gate the installer wires in: a downloaded bundle
	// directory with a malformed omnipus-plugin.json must be rejected.
	dir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, BundleManifestFilename),
			[]byte(`{"name": "broken", "version": "nope"}`),
			0o600,
		),
	)
	_, err := LoadBundleManifest(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid semantic version")
}
