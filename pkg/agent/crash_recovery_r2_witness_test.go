//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SF4 / DESIGN 2: absent evidence is not evidence of an absent effect. These
// descriptors originate at the actual native write/result crash cut, not from
// a mock reconciler. The intact proven-nonexistence control must still resume.
func TestCrashRecovery_R2IncompleteWitnessCannotProveAbsence(t *testing.T) {
	for _, shape := range []string{"missing_before", "null_before", "empty_before", "missing_exists", "intact_absent_before_control"} {
		t.Run(shape, func(t *testing.T) {
			f := crashFileCut(t, "append_file", func(f *crashFileFixture) {
				f.beforeExists, f.before, f.after = false, "", "A\n"
			})
			wpath := f.witnessFile(t, f.snapshot)
			raw, err := os.ReadFile(wpath)
			require.NoError(t, err)
			var descriptor map[string]any
			require.NoError(t, json.Unmarshal(raw, &descriptor))
			require.Equal(t, crashImage("", false), descriptor["before"], "instrument: original tool proved nonexistence")
			switch shape {
			case "missing_before":
				delete(descriptor, "before")
			case "null_before":
				descriptor["before"] = nil
			case "empty_before":
				descriptor["before"] = map[string]any{}
			case "missing_exists":
				delete(descriptor["before"].(map[string]any), "exists")
			}
			raw, err = json.Marshal(descriptor)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(wpath, raw, 0600))
			p := &crashScript{}
			p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
				if i == 0 {
					return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("new-authorized-effect", "append_file", map[string]any{"path": f.path, "content": "NEW\n"})}, FinishReason: "tool_calls"}, nil
				}
				return &providers.LLMResponse{Content: "new work complete", FinishReason: "stop"}, nil
			}
			f.h.restore(t, f.snapshot, p)
			require.NoError(t, os.Remove(f.path), "instrument: actual current target is absent")
			f.h.recover(t)
			record := f.h.record(t, f.record.SessionID)
			if owner := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(record)); owner != nil {
				crashAwaitDone(t, owner.done)
			}
			if shape == "intact_absent_before_control" {
				require.Len(t, p.calls(), 2, "positive instrument: complete proof allows ordinary fresh work")
				data, err := os.ReadFile(f.path)
				require.NoError(t, err)
				assert.Equal(t, "NEW\n", string(data), "new authorized call, not replay of historical A")
				return
			}
			record = f.h.record(t, f.record.SessionID)
			assert.Equal(t, session.LifecycleInterrupted, record.State, "incomplete evidence must establish Unknown before Dispatch")
			assert.Equal(t, session.InterruptionReconciliationRequired, record.InterruptionReason)
			assert.Empty(t, p.calls(), "missing proof may not authorize a model request or automatic write")
			_, err = os.Stat(f.path)
			assert.True(t, os.IsNotExist(err), "recovery must preserve actual absence; automatic NEW write is forbidden: %v", err)
			held, err := loadRecoveryHold(f.record.SessionID)
			require.NoError(t, err)
			assert.Contains(t, held.Occurrences, tools.FileEffectKey(f.assistant, "uncertain"), "the exact incomplete occurrence remains held")
		})
	}
}

// SF5 / DESIGN 2 report occurrence/reason and DESIGN 5 truthful observations:
// syntax corruption and genuinely missing storage need different repair actions.
// The safe diagnostic may choose its wording, but may not erase the fault class.
func TestCrashRecovery_R2ReconciliationKeepsRepairCause(t *testing.T) {
	for _, fault := range []string{"malformed_json", "missing_file"} {
		t.Run(fault, func(t *testing.T) {
			f := crashFileCut(t, "append_file")
			path := f.witnessFile(t, f.snapshot)
			pattern := `(?i)(missing|not found|no such file|does not exist)`
			if fault == "malformed_json" {
				require.NoError(t, os.WriteFile(path, []byte("{corrupt-witness\n"), 0600))
				pattern = `(?i)(invalid.*(json|character)|(json|witness).*(syntax|malformed|decod)|(syntax|malformed|decod).*(json|witness))`
			} else {
				require.NoError(t, os.Remove(path))
			}
			p := &crashScript{}
			f.h.restore(t, f.snapshot, p)
			_, readErr := tools.ReadFileEffectWitness(f.h.home, f.record.SessionID, f.assistant, "uncertain")
			require.Error(t, readErr, "instrument: real witness I/O/decoder receives the deliberate fault")
			f.h.recover(t)
			record := f.h.record(t, f.record.SessionID)
			assert.Equal(t, session.LifecycleInterrupted, record.State)
			assert.Equal(t, session.InterruptionReconciliationRequired, record.InterruptionReason)
			assert.Empty(t, p.calls(), "diagnostic failure never releases the uncertainty fence")
			held, err := loadRecoveryHold(f.record.SessionID)
			require.NoError(t, err)
			key := tools.FileEffectKey(f.assistant, "uncertain")
			require.Contains(t, held.Occurrences, key, "safe repair cause belongs to the actual issuing occurrence")
			reason := held.Occurrences[key]
			t.Logf("REPAIR_CAUSE fault=%s underlying=%v saved=%q", fault, readErr, reason)
			assert.Contains(t, reason, "witness", "name the failed evidence operation")
			assert.Regexp(t, pattern, reason, "retain a specific safe cause, not generic 'not provable'")
			data, err := os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, f.after, string(data), "failed reconciliation is read-only")
		})
	}
}
