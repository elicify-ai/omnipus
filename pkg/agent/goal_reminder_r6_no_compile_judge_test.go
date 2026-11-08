package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Paid model edge only. A plausible compile reply ensures the no-record oracle
// also catches an accidental compiler invocation, not merely a parser failure.
type grForbiddenCompileJudge struct{ calls atomic.Int64 }

func (*grForbiddenCompileJudge) GetDefaultModel() string { return "r6-forbidden-compiler" }
func (p *grForbiddenCompileJudge) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: `{"assessment":{"clarity":"clear"},"definition":"Unrequested engine-authored record","criteria":[{"text":"a plan exists","judgment":"boolean"}],"dod":[{"text":"no secrets leak","judgment":"boolean","provenance":"floor"}]}`}, nil
}

// Founder R6 successor of TestFallbackCompile_JudgeModelResolution. Judge
// availability cannot change the five-completed-reminder recordless rule.
// Active working-agent set_goal/Judge adjudication tests remain unchanged.
func TestRecordlessKeeperNeverUsesJudgeCompiler(t *testing.T) {
	for _, available := range []bool{true, false} {
		name := "AvailableTrapJudge"
		if !available {
			name = "NoJudgeProvider"
		}
		t.Run(name, func(t *testing.T) {
			h := grNewRecordlessBrake(t)
			judge, ok := h.al.GetRegistry().GetAgent(string(coreagent.IDJudge))
			require.True(t, ok)
			trap := &grForbiddenCompileJudge{}
			if available {
				judge.Provider = trap
			} else {
				judge.Provider = nil
			}
			original := h.goalID
			assertEmptyRecord := func() {
				g := goalRecordForSession(t, h.id)
				assert.Equal(t, original, g.GoalID)
				assert.Empty(t, g.Definition, "no engine-authored definition")
				assert.Empty(t, g.Criteria, "no deterministic or Judge-backed compiler rubric")
				assert.Equal(t, 0, g.Round, "no timer adjudication")
				assert.Equal(t, int64(0), trap.calls.Load(), "independent external Judge call counter must stay zero")
			}
			for n := 0; n < 5; n++ {
				if n > 0 {
					h.age(t)
				}
				grTextReminder(t, h, h.reminder(t))
				assertEmptyRecord()
			}
			h.age(t)
			grAssertFifthPause(t, h)
			assertEmptyRecord()
			assert.Equal(t, 6, h.root.count(), "one real bootstrap plus five real completed reminder requests")
		})
	}
}
