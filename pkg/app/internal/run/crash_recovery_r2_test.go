//go:build goolm && stdjson

package run_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/app/internal/run"
	"github.com/stretchr/testify/assert"
)

// PRT T4 / DESIGN 1 exact contract table: CLI automation must distinguish
// incomplete work from both success and generic failure, even without TurnFailed.
func TestRun_CrashRecoveryR2InterruptedIsIncomplete(t *testing.T) {
	no, yes := false, true
	for _, reason := range []string{"tool_limit", "reconciliation_required"} {
		for _, failed := range []*bool{nil, &no} {
			name := reason + "/failure_flag_omitted"
			if failed != nil {
				name = reason + "/failure_flag_false"
			}
			t.Run(name, func(t *testing.T) {
				const partial = "saved partial output, not a completed answer"
				ss := newScriptedServer(t, []any{
					generated.SessionStartedFrame{Type: "session_started", SessionId: "r2-interrupted"},
					generated.TokenFrame{Type: "token", SessionId: "r2-interrupted", Content: partial},
					generated.DoneFrame{Type: "done", SessionId: "r2-interrupted", Stats: &generated.DoneStats{TurnFailed: failed, InterruptionReason: &reason}},
				}, false)
				var stdout, stderr bytes.Buffer
				err := run.Run(context.Background(), makeOptions(ss, false, &stdout, &stderr))
				assert.ErrorIs(t, err, run.ErrTurnInterrupted, "typed incomplete outcome must not become exit-success")
				assert.NotErrorIs(t, err, run.ErrTurnFailed, "interruption is not generic failure")
				assert.ErrorContains(t, err, reason, "retain the typed cause for automation/repair")
				assert.Equal(t, partial+"\n", stdout.String(), "preserve the streamed partial exactly")
				assert.NotContains(t, stderr.String(), partial, "response text belongs only on stdout")
			})
		}
	}
	for _, failed := range []*bool{nil, &no, &yes} {
		t.Run(fmt.Sprintf("ordinary_control_%v", failed), func(t *testing.T) {
			ss := newScriptedServer(t, []any{
				generated.SessionStartedFrame{Type: "session_started", SessionId: "r2-control"},
				generated.DoneFrame{Type: "done", SessionId: "r2-control", Stats: &generated.DoneStats{TurnFailed: failed}},
			}, false)
			var stdout, stderr bytes.Buffer
			err := run.Run(context.Background(), makeOptions(ss, false, &stdout, &stderr))
			if failed != nil && *failed {
				assert.ErrorIs(t, err, run.ErrTurnFailed)
			} else {
				assert.NoError(t, err)
			}
			assert.NotErrorIs(t, err, run.ErrTurnInterrupted, "ordinary completion/failure never inherits an interruption")
		})
	}
}
