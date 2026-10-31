//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 1059–1064, 1261–1271; TDD 23, D-8.1–5.
// Inject only the real turn's dispatch refusal. The executor, paired goal,
// completion, transcript/outcome and registered-credential logger are real.
func TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink(t *testing.T) {
	const registered = "t10-fixture-registered-credential-7W92"
	for _, tc := range []struct {
		name, cause, diagnostic string
		registered, plain       bool
	}{
		{"D8-1-registered", "adapter rejected credential " + registered, "adapter rejected credential", true, false},
		{"D8-2-unknown-token", "provider refused sk-live-T10-UNKNOWN-NOT-REGISTERED", "sk-live-T10-UNKNOWN-NOT-REGISTERED", false, false},
		{"D8-3-path-env", "open /srv/private/t10-runtime.env: PRIVATE_REGION=t10-secret-region", "/srv/private/t10-runtime.env", false, false},
		{"D8-4-hook-stderr", "hook stderr: credential=" + registered, "hook stderr", true, false},
		{"D8-5-plain-control", "The task could not be started", "The task could not be started", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateSensitiveValueReplacer(t)
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(c *config.Config) {
				if tc.registered {
					c.RegisterSensitiveValues([]string{registered})
				}
			})
			t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
			readLog := enableLeakTestLogFile(t)
			tk := newRunLoopTask(t, al, nil)
			claimed, err := al.taskStore.ClaimForRun(tk.ID, time.Now())
			require.NoError(t, err)
			sid, err := al.taskExecutor.createTaskSessionSync(claimed)
			require.NoError(t, err)
			require.NotEmpty(t, sid)
			calls := 0
			next := al.taskExecutor.executeTaskRun(context.Background(), claimed, sid, "", nil, func(string) (string, error) {
				calls++
				return "", fmt.Errorf("%s: %w", tc.cause, ErrTaskRunNotDispatched)
			})
			assert.Equal(t, 1, calls)
			assert.Empty(t, next, "never redispatch work that did not start")
			fresh, err := al.taskStore.Get(tk.ID)
			require.NoError(t, err)
			assert.Equal(t, task.StatusFailed, fresh.Status)
			assert.Zero(t, fresh.AttemptCount)
			rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
			goalJSON, err := json.Marshal(rec)
			require.NoError(t, err)
			waitForGoalOutcomeEntry(t, al.GetSessionStore(), sid)
			outcome := requireOneGoalOutcome(t, al.GetSessionStore(), sid, rec.GoalID)
			entries, err := al.GetSessionStore().ReadTranscript(sid)
			require.NoError(t, err)
			chat := []string{outcome.Content}
			for _, entry := range entries {
				chat = append(chat, entry.Content)
			}
			for name, text := range map[string]string{"task result": fresh.Result, "goal record": string(goalJSON), "chat outcome/transcript": strings.Join(chat, "\n")} {
				assert.Contains(t, strings.ToLower(text), "could not be started", name+" must carry the plain user explanation")
				assert.NotContains(t, text, registered, name+" must never contain a registered secret")
				assert.NotContains(t, text, ErrTaskRunNotDispatched.Error(), name+" must not render the raw error wrapper")
				if !tc.plain {
					assert.NotContains(t, text, tc.diagnostic, name+" must map diagnostics, not rely on registered-value scrubbing")
				}
			}
			logText := readLog()
			assert.Contains(t, logText, "Agent execution failed")
			assert.Contains(t, logText, tc.diagnostic, "operator diagnostic retained")
			assert.NotContains(t, logText, registered, "registered credential absent even from operator log")
			if tc.registered {
				assert.Contains(t, logText, "[FILTERED]")
			}
		})
	}
}
