//go:build goolm && stdjson

package gateway

// Order 46 — TestLegacySecurityEndpoints_Retired (S-101; C-22 row 6;
// Dataset row 9). Oracle: skills-tools-redesign-spec.md —
//   S-101: "When a client calls GET /security/skill-trust, PUT
//        /security/skill-trust, GET /security/tool-policies and PUT
//        /security/tool-policies … each answers 404 and nothing is stored"
//        (Given: a data folder whose stored trust is block_unverified).
//   C-22 row 6: the endpoints, their handlers, schemas, inbound schemas and
//        API clients are retired, and "a guard test fails if any file in
//        src/, pkg/, cmd/ or tests/ still calls either path".
//
// The HTTP half drives the four calls through the REAL route table
// (registerAdditionalEndpoints — the same call gateway.go makes; a direct
// handler call is not a substitute and would not compile after GREEN deletes
// the handlers). "Nothing is stored" compares sandbox.skill_trust and
// sandbox.tool_policies before and after the PUT attempts.
//
// The guard half scans the four spec-named roots for either path, dropping
// comment-only lines and excluding generated trees (pkg/api/generated,
// src/lib/api/generated — regenerated from contracts/, which C-22 row 6
// retires at the contract source) and the embedded SPA build output
// (pkg/gateway/spa). This test file itself is the one excluded source: it
// drives the retired routes through the mux to prove the 404, so it
// necessarily carries the path strings. The planted-positive control proves
// the instrument (the F11 convention): a caller planted AFTER an excluded
// subtree is caught, comment-only and excluded-subtree copies are not, and a
// clean tree passes.
//
// RED today: rest.go registers both paths (adminWrap HandleSkillTrust;
// withAuth HandleToolPolicies), so the calls answer 200 not 404 and the PUTs
// store; the guard lists the callers that still exist (rest.go, the two
// handlers, the TS clients, SecuritySection.tsx, useReAuthGate.tsx, the
// legacy gateway/settings tests, tests/security and the e2e/harness specs,
// and the inbound schemas C-22 row 6 deletes).
import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"

	"github.com/stretchr/testify/require"
)

const (
	legacySkillTrustPath   = "/api/v1/security/skill-trust"
	legacyToolPoliciesPath = "/api/v1/security/tool-policies"

	// This file drives the retired routes through the mux to prove the 404,
	// so it carries the path strings itself; the guard excludes exactly this
	// file and nothing else.
	guardSelfRelPath = "pkg/gateway/legacy_security_endpoints_retired_red_test.go"
)

// legacySecurityCall is one S-101 call against the real route table.
type legacySecurityCall struct {
	method, path, body string
}

var legacySecurityCalls = []legacySecurityCall{
	{http.MethodGet, legacySkillTrustPath, ""},
	{http.MethodPut, legacySkillTrustPath, `{"level":"allow_all"}`},
	{http.MethodGet, legacyToolPoliciesPath, ""},
	{http.MethodPut, legacyToolPoliciesPath, `{"policies":{"write_file":"allow"}}`},
}

// normalizedPolicies copies a policies map for comparison, treating nil and
// empty as the same no-entries state (the stored CONTENT is the oracle, not
// the representation).
func normalizedPolicies(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestLegacySecurityEndpoints_Retired(t *testing.T) {
	t.Run("S-101: each legacy call answers 404 and stores nothing", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		t.Setenv("OMNIPUS_BEARER_TOKEN", packageTestBearer)
		// S-101 Given: a data folder whose stored trust is block_unverified.
		api.agentLoop.GetConfig().Sandbox.SkillTrust = config.SkillTrustBlockUnverified
		policiesBefore := normalizedPolicies(api.agentLoop.GetConfig().Sandbox.ToolPolicies)

		mux := http.NewServeMux()
		api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

		for _, tc := range legacySecurityCalls {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := packageAuthRequest(t, tc.method, tc.path, body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			require.Equalf(t, http.StatusNotFound, w.Code,
				"S-101 / C-22 row 6: %s %s is retired and must answer 404", tc.method, tc.path)
		}

		// "nothing is stored": the PUT attempts leave sandbox.skill_trust at
		// the given block_unverified and sandbox.tool_policies untouched.
		// Read the LIVE config after the calls — the legacy PUTs trigger a
		// reload that replaces the config object, so a pointer captured
		// before the calls would read a stale snapshot.
		after := api.agentLoop.GetConfig()
		require.Equal(t, config.SkillTrustBlockUnverified, after.Sandbox.SkillTrust,
			"S-101: nothing is stored — the retired PUT must not change sandbox.skill_trust")
		require.Equal(t, policiesBefore, normalizedPolicies(after.Sandbox.ToolPolicies),
			"S-101: nothing is stored — the retired tool-policies PUT must not change sandbox.tool_policies")
	})

	t.Run("C-22 row 6: the guard finds no caller of either path in src/, pkg/, cmd/ or tests/", func(t *testing.T) {
		repoRoot := filepath.Join("..", "..")
		for _, root := range []string{"src", "pkg", "cmd", "tests"} {
			if st, err := os.Stat(filepath.Join(repoRoot, root)); err != nil || !st.IsDir() {
				t.Fatalf("BLOCKED: scan root %s is missing — the guard refuses to green on an absent tree", root)
			}
		}
		offenders := scanLegacySecurityPathCallers(t, repoRoot)
		require.Empty(t, offenders,
			"C-22 row 6: files under src/, pkg/, cmd/ or tests/ still reference the retired "+
				"paths /security/skill-trust or /security/tool-policies; the endpoints, their "+
				"handlers, schemas, inbound schemas, API clients and every caller are retired:\n%s",
			strings.Join(offenders, "\n"))
	})

	// The F11 convention: prove the guard could have seen a violation before
	// trusting its empty result on the real tree.
	t.Run("guard instrument: planted-positive and exclusion controls", func(t *testing.T) {
		root := t.TempDir()
		write := func(rel, content string) {
			t.Helper()
			p := filepath.Join(root, filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(content+"\n"), 0o600))
		}
		write("src/legacy-caller.ts", `await fetch("/api/v1/security/skill-trust", { method: "PUT" });`)
		write("pkg/note.go", "// legacy /api/v1/security/skill-trust was retired (comment only)")
		write("node_modules/leftover.js", `fetch("/api/v1/security/tool-policies");`)
		write("pkg/gateway/spa/assets/app.js", `fetch("/api/v1/security/tool-policies");`)
		write("pkg/api/generated/types.gen.go", `const legacy = "/api/v1/security/skill-trust";`)

		found := scanLegacySecurityPathCallers(t, root)
		sort.Strings(found)
		require.Len(t, found, 1,
			"guard instrument: exactly the planted caller is caught — comment-only, "+
				"generated and build-output copies must not be")
		if len(found) == 1 {
			require.Contains(t, found[0], filepath.Join("src", "legacy-caller.ts"),
				"guard instrument: the planted caller is the one caught")
		}

		clean := scanLegacySecurityPathCallers(t, t.TempDir())
		require.Empty(t, clean, "guard instrument: a clean tree passes")
	})
}

// scanLegacySecurityPathCallers walks the four spec-named roots under root,
// skipping generated/build subtrees and comment-only lines, and returns one
// "path:line :: matched line" entry per offending file.
func scanLegacySecurityPathCallers(t *testing.T, root string) []string {
	t.Helper()
	skipDirNames := map[string]bool{
		"node_modules": true,
		".git":         true,
		"dist":         true,
		"spa":          true, // the embedded build output under pkg/gateway/spa
		"generated":    true, // pkg/api/generated, src/lib/api/generated
		"testdata":     true,
	}
	var offenders []string
	for _, scanRoot := range []string{"src", "pkg", "cmd", "tests"} {
		full := filepath.Join(root, scanRoot)
		if st, statErr := os.Stat(full); statErr != nil || !st.IsDir() {
			continue // fixture trees may not carry all four roots; the real-tree subtest asserts them
		}
		err := filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirNames[d.Name()] {
					// SkipDir skips ONLY this subtree; the walk continues with
					// the siblings (CHECK round 1 finding 11).
					return filepath.SkipDir
				}
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if filepath.ToSlash(rel) == guardSelfRelPath {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for i, line := range strings.Split(string(raw), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "//") ||
					strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") ||
					strings.HasPrefix(trimmed, "#") {
					continue // comment-only line (the established guard convention)
				}
				if strings.Contains(line, "/security/skill-trust") ||
					strings.Contains(line, "/security/tool-policies") {
					offenders = append(offenders,
						filepath.ToSlash(rel)+":"+strconv.Itoa(i+1)+" :: "+truncate(line, 120))
					break // one offending line is enough to name the file
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("BLOCKED: scanning %s failed: %v", full, err)
		}
	}
	sort.Strings(offenders)
	return offenders
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
