package gateway

// Test-owned S1b fixtures. Archive construction is independent of omnpkg's
// reader/writer; wire expectations use the already-generated contract types.

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

const (
	s1bRuntimeCode = "runtime_settings_removed"
	s1bNoCodexPath = "This agent cannot run: no program path is set for Codex. A person can set it in Settings → Third-party agents → Codex."
	s1bOpaquePath  = "skills/writing/SKILL.md"
	s1bOpaqueBody  = "# S1b-unknown-not-an-installed-skill\nThis is opaque, not agent JSON.\n"
)

func s1bWorkerRecord(withRuntime bool) map[string]any {
	executor := map[string]any{"kind": "external-cli", "cli": "codex"}
	if withRuntime {
		executor["env_overrides"] = map[string]string{"OPENAI_API_KEY": "sk-TEST-456"}
		executor["cli_args"] = "--key sk-TEST-123"
		executor["cli_path"] = "/source-host/private/codex"
	}
	return map[string]any{
		"id": specSourceID, "name": "Coder", "type": "worker", "description": "  Runs Codex tasks  ",
		// D3-8: non-default values of the fields the external create variant supports.
		"model":               map[string]any{"primary": "m-1", "provider": "openrouter"},
		"max_tool_iterations": 40,
		"figure":              "Robot", "role": "developer", "color": "#6E8BA8",
		"subagents": map[string]any{"executor": executor},
	}
}

func s1bArchive(t *testing.T, manifest *omnpkg.Manifest, parts []tarPart) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	return zstdTar(t, append([]tarPart{{name: "manifest.json", body: raw}}, parts...))
}

func s1bMixedParts(t *testing.T, kind string, schema int, unknownFirst bool) (*omnpkg.Manifest, []tarPart) {
	t.Helper()
	// Decode a test-built Main fixture, never production Export output.
	doc, files := decodePackageBytes(t, validMainPackage(t))
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	var manifest omnpkg.Manifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	unknown := omnpkg.ManifestElement{
		Type: kind, Schema: schema, SourceID: "writing", Name: "Writing", Files: []string{s1bOpaquePath},
	}
	if unknownFirst {
		manifest.Elements = append([]omnpkg.ManifestElement{unknown}, manifest.Elements...)
	} else {
		manifest.Elements = append(manifest.Elements, unknown)
	}
	manifest.Counts[kind] = 1
	manifest.Files[s1bOpaquePath] = omnpkg.FileInfo{SHA256: sha256hex([]byte(s1bOpaqueBody)), Size: int64(len(s1bOpaqueBody))}
	parts := []tarPart{
		{name: agentRecordPath(specSourceID), body: files[agentRecordPath(specSourceID)]},
		{name: agentSoulPath(specSourceID), body: files[agentSoulPath(specSourceID)]},
		{name: agentMDPath(specSourceID), body: files[agentMDPath(specSourceID)]},
		{name: s1bOpaquePath, body: []byte(s1bOpaqueBody)},
	}
	return &manifest, parts
}

func s1bRequireRuntimeGuidance(t *testing.T, message string) {
	t.Helper()
	require.Contains(t, message, "Edit → Runtime", "FR-043 names the per-agent destination, not host Settings")
	require.Contains(t, message, "environment overrides")
	require.Contains(t, message, "CLI arguments")
}
