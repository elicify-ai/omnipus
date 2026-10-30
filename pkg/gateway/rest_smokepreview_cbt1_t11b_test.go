// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for T11 slice B / contract change C-B1 (t11 spec §4 "Smoke test /
// command preview (X4)" and §8 FR-12): "Remove the free `cli_path` field from
// both requests. The routes use the saved Settings path for the request's
// CLI." Named test: TestSmokeAndPreview_RejectFreePath ("Given a request with
// cli_path / When received / Then 400").
//
// The routes take NO program path from the caller anymore: the program for
// the request's CLI comes from Settings → Third-party agents (spec §4; T8's
// contract commit 1243ed887 owns the settings store). A request body that
// still carries `cli_path` must therefore be refused 400 — after C-B1 the
// field is no longer in ExecutorSmokeTestRequest / ExecutorCommandPreviewRequest
// (both additionalProperties:false), and the handler decode must reject it in
// BOTH validation modes the way decodeAndValidateStrict does for
// additionalProperties:false contracts (a silent json-decode drop would leave
// the caller's free path silently ignored — the exact shape this refusal
// exists to prevent).
//
// REAL: the two registered routes (HandleAgents carve-out).
// MOCK: none. The smoke-test request's cli_path names a nonexistent binary so
// today's pre-C-B1 handler fails fast at binary resolution — no subprocess,
// no model call (the TestPostAgentsExecutorSmokeTest_MissingBinary shape).
//
// RECONCILIATION NOTE for the T8/C-B1 implementer: the existing happy-path
// tests rest_executor_preview_test.go::TestPostAgentsExecutorPreview_Codex_HappyPath
// and rest_executor_smoketest_test.go::TestPostAgentsExecutorSmokeTest_HappyPath
// still DRIVE the free cli_path and will need updating in the same change.
//
// RED TODAY: both routes still accept cli_path (the fields exist in the
// contract until C-B1 lands), so each subtest fails with got-200-want-400.
package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSmokeAndPreview_RejectFreePath(t *testing.T) {
	t.Run("command preview refuses a free cli_path", func(t *testing.T) {
		api := executorDefaultsTestAPI(t)
		code, resp := postExecutorPreview(t, api, map[string]any{
			"cli":      "codex",
			"model":    "gpt-5-codex",
			"cli_path": "/usr/local/bin/codex",
		})
		require.Equal(t, http.StatusBadRequest, code,
			"C-B1: executor-preview takes no free cli_path — the program comes from Settings; body binary=%q",
			resp.Binary)
	})

	t.Run("smoke test refuses a free cli_path before any spawn", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()
		t.Setenv("OMNIPUS_HOME", t.TempDir())

		// A nonexistent path: pre-C-B1 the handler accepts the body and only
		// then fails at binary resolution (200 + ok:false) — no spawn, no
		// model call; post-C-B1 the decode refuses the field with 400 before
		// any of that.
		code, resp := postExecutorSmokeTest(t, api,
			`{"cli":"codex","cli_path":"/nonexistent/t11b-codex"}`)
		require.Equal(t, http.StatusBadRequest, code,
			"C-B1: executor-smoke-test takes no free cli_path — the program comes from Settings; respOk=%v",
			resp.Ok)
	})
}
