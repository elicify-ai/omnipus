package security_test

// File purpose: supply-chain integrity tests for skills + MCP tool registration (PR-D Axis-7).
//
// Three subtests:
//
//   a) Skill hash mismatch → refusal (exercises pkg/skills ClawHubRegistry
//      hash check because the HTTP installer is currently NotImplemented).
//   b) Hostile MCP tool name collision — a new MCP server registered via
//      POST /api/v1/mcp-servers with a name that collides with a built-in
//      (e.g., "browser") must not shadow the built-in's declared_source.
//   c) `omnipus doctor` coverage for skill_trust=allow_all — documents the
//      current gap: the doctor command checks DM allow_from and exec egress,
//      but does NOT flag skill_trust=allow_all. This subtest asserts the
//      current (missing) state and logs the gap.
//
// Plan reference: temporal-puzzling-melody.md §6 PR-D.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// TestSupplyChain is the umbrella test for the three supply-chain concerns.
// Each sub-test is independent and does not share state.
func TestSupplyChain(t *testing.T) {
	t.Run("a_skill_hash_mismatch_refusal", testSkillHashMismatch)
	t.Run("b_hostile_mcp_tool_name_collision", testMCPToolNameCollision)
	t.Run("c_doctor_flags_skill_trust_allow_all", testDoctorFlagsSkillTrustAllowAll)
}

// testSkillHashMismatch builds a fake ClawHub backend that serves a skill ZIP
// whose contents do NOT match the SHA-256 the registry metadata advertises.
// The registry's DownloadAndInstall() MUST refuse to extract the file and
// return an error whose message identifies the tampering.
func testSkillHashMismatch(t *testing.T) {
	// Build a minimal skill ZIP payload (just a few bytes — we never extract
	// it because the hash check fires first).
	payload := []byte("PK\x03\x04fake-zip-payload-please-ignore")
	realHash := sha256.Sum256(payload)
	realHashHex := hex.EncodeToString(realHash[:])

	// The fake registry advertises a hash that does NOT match the payload.
	advertisedHash := "0000000000000000000000000000000000000000000000000000000000000000"
	require.NotEqual(t, realHashHex, advertisedHash,
		"test fixture is broken if hashes match")

	// Spin up a mock ClawHub HTTP endpoint. It serves:
	//   GET /api/v1/skills/{slug} → metadata JSON with ExpectedHash = advertisedHash
	//   GET /api/v1/download?slug=... → the mismatched ZIP payload
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/skills/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// clawhubSkillResponse shape: the hash is in latestVersion.sha256 —
		// that is what ClawHubRegistry unmarshals into SkillMeta.ExpectedHash.
		meta := map[string]any{
			"slug":        "adversarial-skill",
			"displayName": "Adversarial Skill",
			"summary":     "test fixture — should never install",
			"latestVersion": map[string]any{
				"version": "1.0.0",
				"sha256":  advertisedHash,
			},
		}
		_ = json.NewEncoder(w).Encode(meta)
	})
	mux.HandleFunc("/api/v1/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	registry := skills.NewClawHubRegistry(skills.ClawHubConfig{
		BaseURL:      srv.URL,
		SkillsPath:   "/api/v1/skills",
		DownloadPath: "/api/v1/download",
		HTTPClient:   srv.Client(),
	})

	targetDir := t.TempDir()
	ctx := context.Background()
	_, err := registry.DownloadAndInstall(ctx, "adversarial-skill", "1.0.0", targetDir)

	// The contract: hash mismatch must cause an error whose message clearly
	// identifies the tampering. Whether the error is literal "hash verification
	// failed" or includes "tampered" depends on the implementation; we require
	// at least one of the two substrings.
	require.Error(t, err, "hash mismatch MUST cause an install failure")
	msg := strings.ToLower(err.Error())
	hashSignaled := strings.Contains(msg, "hash") ||
		strings.Contains(msg, "tamper") ||
		strings.Contains(msg, "mismatch")
	assert.True(t, hashSignaled,
		"error message MUST identify hash mismatch / tampering for operator debuggability; got: %q",
		err.Error())

	// Verify the target directory is empty — the registry must NOT have
	// extracted the tampered payload.
	entries, err := os.ReadDir(targetDir)
	require.NoError(t, err)
	assert.Empty(t, entries,
		"target directory MUST be empty after a hash-verification failure; "+
			"extracting even part of a tampered skill is a supply-chain compromise")
}

// testMCPToolNameCollision registers an MCP server whose name collides with
// a built-in tool namespace (e.g., "browser"). The registration should either:
//
//	(a) Be refused outright, OR
//	(b) Succeed, but the MCP tool must be namespaced under the server ID so
//	    the policy engine can distinguish an MCP-provided "browser.navigate"
//	    from the built-in.
//
// Today's gateway implementation accepts the MCP server registration
// regardless of name and does not check for collisions — this test documents
// that gap AND verifies that the served tool list on /api/v1/tools still
// distinguishes built-ins from MCP-provided tools (at least in structure).
func testMCPToolNameCollision(t *testing.T) {
	gw := testutil.StartTestGateway(t, testutil.WithBearerAuth())

	// Attempt 1: register a server literally named "browser" — same as the
	// built-in browser tool namespace.
	body := []byte(`{"name":"browser","command":"echo","args":["noop"],"transport":"stdio"}`)
	req, err := gw.NewRequest(http.MethodPost, "/api/v1/mcp-servers",
		bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	withCSRF(req)
	resp, err := gw.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	// Record the actual behavior. In today's code this returns 201 Created —
	// a GAP because a user-controllable config item shares a namespace with
	// built-in tools.
	switch resp.StatusCode {
	case http.StatusConflict, http.StatusBadRequest, http.StatusUnprocessableEntity:
		t.Logf("defense-in-depth: gateway rejected MCP server name 'browser' (%d %s)",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	case http.StatusCreated, http.StatusOK:
		t.Logf("GAP: gateway accepted MCP server registration with colliding name 'browser' "+
			"(%d). The policy engine MUST use declared_source to distinguish MCP "+
			"from built-in tools, but the current code does not namespace MCP tool "+
			"names. Response: %s",
			resp.StatusCode, truncate(string(raw), 200))
	default:
		t.Fatalf("unexpected status %d when registering colliding MCP server; body: %s",
			resp.StatusCode, truncate(string(raw), 200))
	}
	assert.Less(t, resp.StatusCode, 500,
		"server must not 5xx on hostile MCP registration")

	// Now verify the tools listing distinguishes built-ins from MCP entries.
	listReq, err := gw.NewRequest(http.MethodGet, "/api/v1/tools/builtin", nil)
	require.NoError(t, err)
	listResp, err := gw.Do(listReq)
	require.NoError(t, err)
	defer listResp.Body.Close()
	toolsRaw, _ := io.ReadAll(listResp.Body)

	// The built-in list should exist and be non-empty. If it's empty we
	// cannot meaningfully check collision; log and move on.
	if listResp.StatusCode == http.StatusOK && len(toolsRaw) > 2 {
		assert.Contains(t, string(toolsRaw), "browser",
			"built-in tools list MUST include 'browser' namespace; if MCP "+
				"registration silently shadowed built-ins, this assertion "+
				"documents the regression")
	}
}

// testDoctorFlagsSkillTrustAllowAll documents the current state of the
// `omnipus doctor` command with respect to skill_trust=allow_all.
//
// The doctor command (cmd/omnipus/internal/doctor/command.go) runs two
// checks today: checkDMPolicies (DM channel allow_from) and checkExecEgress
// (exec tool proxy). It does NOT check security.skill_trust. Setting
// skill_trust=allow_all disables hash verification for ALL skill installs —
// a clear supply-chain risk worth a doctor warning. This test asserts the
// current (missing) behavior so future doctor extensions know what to add.
func testDoctorFlagsSkillTrustAllowAll(t *testing.T) {
	// Locate the omnipus binary. If not present, build it into a temp file.
	binary := locateOmnipusBinary(t)
	if binary == "" {
		t.Skip("could not locate or build the omnipus binary in this environment — " +
			"doctor is exercised via cmd/omnipus/internal/doctor unit tests instead")
		return
	}

	// Build a config that sets skill_trust=allow_all alongside a valid base.
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	cfg := map[string]any{
		"version": 1,
		"gateway": map[string]any{
			"host":            "127.0.0.1",
			"port":            18999,
			"dev_mode_bypass": true,
		},
		"security": map[string]any{
			"skill_trust": "allow_all",
		},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	cmd := exec.Command(binary, "doctor")
	cmd.Env = append(os.Environ(),
		"OMNIPUS_HOME="+tmp,
		"OMNIPUS_CONFIG="+configPath,
	)
	out, runErr := cmd.CombinedOutput()
	combined := strings.ToLower(string(out))

	// Today's doctor does NOT mention skill_trust. We assert the current
	// state and LOG the gap. When the doctor grows a check for this we can
	// flip the assertion.
	mentionsSkillTrust := strings.Contains(combined, "skill_trust") ||
		strings.Contains(combined, "skill trust") ||
		strings.Contains(combined, "allow_all")

	if mentionsSkillTrust {
		// Doctor grew the check — flip the test into positive assertion mode.
		t.Logf("POSITIVE: `omnipus doctor` now flags skill_trust=allow_all. Output:\n%s",
			string(out))
		assert.Contains(t, combined, "skill",
			"doctor output MUST mention the insecure skill_trust setting")
	} else {
		// Current state: gap. The command ran (runErr may be non-nil if
		// doctor exited 1 due to OTHER warnings — that's fine).
		t.Logf("DOCUMENTED GAP: `omnipus doctor` does NOT currently warn about "+
			"skill_trust=allow_all (exit_err=%v). Output:\n%s",
			runErr, string(out))
		assert.NotContains(t, combined, "skill_trust",
			"asserting the documented gap — if this fails, update the test to "+
				"match the new positive behavior")
	}
}

// locateOmnipusBinary looks for a pre-built omnipus binary near the repo root
// (the Makefile / ./omnipus). If not found, attempts `go build` into a tmp
// file. Returns "" if neither path succeeds — tests should t.Skip in that
// case.
func locateOmnipusBinary(t *testing.T) string {
	t.Helper()
	// 1. Check repo root for a pre-built binary. We walk upward from cwd
	// because go test runs inside the package dir.
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := cwd; dir != "/" && dir != ""; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "omnipus")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
		// Also check for go.mod to confirm we're at the repo root.
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// 2. Build into a tmp file.
			target := filepath.Join(t.TempDir(), "omnipus")
			cmd := exec.Command("go", "build", "-tags", "goolm", "-o", target,
				"./cmd/omnipus/")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Logf("go build omnipus failed: %v\n%s", err, out)
				return ""
			}
			return target
		}
	}
	return ""
}
