//go:build darwin || linux

package gateway

import (
	"net/http"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder F1 / D-F5 and docs/security.md: Check CLI checks the program as
// entered. The registered HTTP route must preserve a nonblank pathname.
func TestCliValidate_HTTPPreservesLiteralProgramPath(t *testing.T) {
	p := t11ProgramIdentity(t) // includes an isolated PATH and two real programs
	checked, err := filepath.EvalSymlinks(p.checked)
	require.NoError(t, err)
	require.NotEqual(t, p.other, checked, "instrument: the literal program is not its sibling")
	spawns, launched := t11CountSpawns(t)

	w := p.settings.call(t, http.MethodPost, "/api/v1/system/cli-validate",
		map[string]string{"cli": "codex", "cli_path": p.checked}, "person")
	require.Equal(t, http.StatusOK, w.Code, "the real person reaches the diagnostic: %s", w.Body.String())
	resp := t11DecodeValidate(t, w)
	require.True(t, resp.Ok, "instrument: the real version handshake completes")
	require.Equal(t, gen.CliValidateResponseReasonUnauthenticated, resp.Reason)
	require.NotNil(t, resp.Version)
	assert.Equal(t, "1.2.3", *resp.Version, "the local fixture reports its version")
	require.NotNil(t, resp.ResolvedPath)
	assert.Equal(t, checked, *resp.ResolvedPath, "D-F5: report the literal program that was requested")
	assert.Equal(t, 1, *spawns, "one real version handshake")
	assert.Equal(t, []string{checked}, *launched, "D-F5: check the literal program, without normalization")
	t11AssertProgramIdentity(t, p)
}

// The missing-path rule is unchanged: whitespace-only input is not a program.
// Contract CliValidateResponse pins the reason, null fields and fixed detail.
func TestCliValidate_HTTPBlankProgramPathIsMissing(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"empty", ""},
		{"whitespace_only", " \t\r\n "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := t11ProgramIdentity(t)
			spawns, launched := t11CountSpawns(t)
			w := p.settings.call(t, http.MethodPost, "/api/v1/system/cli-validate",
				map[string]string{"cli": "codex", "cli_path": tc.path}, "person")
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			resp := t11DecodeValidate(t, w)
			assert.False(t, resp.Ok)
			assert.Equal(t, gen.CliValidateResponseReasonMissingBinary, resp.Reason)
			assert.Nil(t, resp.ResolvedPath)
			assert.Nil(t, resp.Version)
			require.NotNil(t, resp.Detail)
			assert.Equal(t, "not found", *resp.Detail, "contract: fixed missing-program detail")
			t11AssertNotSpawned(t, p.checkedMarker, *spawns)
			t11AssertNotSpawned(t, p.otherMarker, *spawns)
			assert.Empty(t, *launched, "a blank path reaches no version probe")
		})
	}
}
