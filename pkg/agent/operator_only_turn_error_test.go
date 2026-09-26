/**
 * operator_only_turn_error_test.go — provider-messages spec RED tests:
 * TDD row 27 (SC-10, D15, MAJ-107) — operator-only membership.
 *
 * Oracles come from the SPEC ONLY:
 *   - SC-10/MAJ-107: quota_billing and model_retired join
 *     classifyOperatorOnlyTurnError's operator-only set (both config-
 *     attributed per D15, final). Consumers ride existing wiring:
 *       - task run: task_run_loop.go consults the classifier and lands
 *         LifecycleFailed + "operator_action_required" (wiring pinned for the
 *         other causes by task_run_operator_fix_test.go::
 *         TestTaskRun_OperatorOnlyErrorEndsFailedAtOnce; the billing/retired
 *         rows ride the same wiring once membership includes them),
 *       - Judge: judgeDispatchNeedsOperator reports a needed operator fix,
 *       - chat: TranslateTurnError renders the section-6 sentence.
 *   - Boundary (MAJ-107): temporary errors stay temporary — a 429 and a 503
 *     stay operatorFixNone (the Judge keeps its retry backoff; the task
 *     restarts on its attempt limit); C-24's deferred 410 stays temporary.
 *
 * COMPILE-RED: CodeQuotaBilling / CodeModelRetired do not exist yet; the
 * spec names them, so the tests spell them so GREEN has an unambiguous
 * target.
 */

package agent

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// opOnlyBoundary builds the error the HTTP boundary produces, through the
// real HandleErrorResponse.
func opOnlyBoundary(t *testing.T, status int, body string) error {
	t.Helper()
	resp := &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	err := common.HandleErrorResponse(resp, "https://api.example.com")
	if err == nil {
		t.Fatalf("HandleErrorResponse returned nil for status %d, want *common.ProviderError", status)
	}
	return err
}

// TestOperatorOnly_BillingAndRetiredAreOperatorOnly — row 27 membership:
// the two new config-attributed codes are operator-only; temporary errors
// stay temporary (MAJ-107 boundary).
func TestOperatorOnly_BillingAndRetiredAreOperatorOnly(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantCode   LLMErrorCode
		wantOpOnly bool
	}{
		{
			name:       "billing 402 boundary error",
			err:        opOnlyBoundary(t, 402, `{"error":{"message":"insufficient credits"}}`),
			wantCode:   CodeQuotaBilling,
			wantOpOnly: true,
		},
		{
			name: "billing FailoverError identity wrapper",
			err: &providers.FailoverError{
				Reason:   providers.FailoverBilling,
				Provider: "openrouter",
				Model:    "model-a",
				Status:   402,
				Wrapped:  opOnlyBoundary(t, 402, `{"error":{"message":"neutral"}}`),
			},
			wantCode:   CodeQuotaBilling,
			wantOpOnly: true,
		},
		{
			name:       "retired 404 plus retirement phrase",
			err:        opOnlyBoundary(t, 404, `{"error":{"message":"This model has been decommissioned"}}`),
			wantCode:   CodeModelRetired,
			wantOpOnly: true,
		},
		{
			name:       "429 stays temporary",
			err:        opOnlyBoundary(t, 429, `{"error":{"message":"Too many requests"}}`),
			wantCode:   CodeRateLimited,
			wantOpOnly: false,
		},
		{
			name:       "503 outage stays temporary",
			err:        opOnlyBoundary(t, 503, `{"error":{"message":"unavailable"}}`),
			wantCode:   CodeNetwork,
			wantOpOnly: false,
		},
		{
			name:       "410 decommissioned stays temporary (C-24 defers 410)",
			err:        opOnlyBoundary(t, 410, `{"error":{"message":"This model has been decommissioned"}}`),
			wantCode:   CodeUnknown,
			wantOpOnly: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			code, cause := classifyOperatorOnlyTurnError(tc.err)
			gotOpOnly := cause != operatorFixNone
			if code != tc.wantCode || gotOpOnly != tc.wantOpOnly {
				t.Fatalf("classifyOperatorOnlyTurnError(%v) = (%q, cause=%v opOnly=%v), want (%q, opOnly=%v)",
					tc.err, code, cause, gotOpOnly, tc.wantCode, tc.wantOpOnly)
			}
		})
	}
}
