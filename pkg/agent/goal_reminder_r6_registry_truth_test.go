package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type grInvocationTruthReader interface{ WasInvoked() bool }

// The unknown-call keeper control stops at the REAL offered/policy guard.
// This separate process-edge registry control reaches ExecuteWithContext's
// actual missing-name/validation paths and positively tests the live carrier.
// It never forges a transcript row to pretend a guarded call reached registry.
func TestGoalReminderRegistryInvocationCarrier(t *testing.T) {
	for _, tc := range []struct {
		name, mode, errorText                string
		registered, valid, invoked, deferred bool
		operations                           int64
	}{
		{name: "ActualRegistryLookupMiss", errorText: "not found"},
		{name: "ActualRegistryValidationFailure", registered: true, errorText: "invalid arguments"},
		{name: "ActualExecutedError", registered: true, valid: true, mode: "error", invoked: true, operations: 1, errorText: "failed AFTER invocation"},
		{name: "ActualExecutedPanic", registered: true, valid: true, mode: "panic", invoked: true, operations: 1, errorText: "crashed with panic"},
		{name: "ActualDeferredEntryWithoutOperation", registered: true, valid: true, mode: "deferred", invoked: true, deferred: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := tools.NewToolRegistry()
			probe := &grProcessOperationEdge{mode: tc.mode}
			if tc.registered {
				registry.RegisterReplacing(probe)
			}
			_, exists := registry.Get(probe.Name())
			require.Equal(t, tc.registered, exists)
			args := map[string]any{}
			if tc.valid {
				args["payload"] = "real external-operation fixture input"
			}
			result := registry.ExecuteWithContext(context.Background(), probe.Name(), args, "", "", nil)
			require.NotNil(t, result)
			if tc.errorText != "" {
				require.True(t, result.IsError)
				assert.Contains(t, result.ForLLM, tc.errorText)
			}
			assert.Equal(t, tc.operations, probe.invoked.Load(), "independent actual operation-entry instrument")
			truth, ok := any(result).(grInvocationTruthReader)
			if !ok {
				t.Fatal("BLOCKED: live ToolResult.WasInvoked registry carrier not implemented — required by founder R6 invocation-truth control")
			}
			assert.Equal(t, tc.invoked, truth.WasInvoked(), "lookup/validation must remain distinct from real Execute entry")
			assert.Equal(t, tc.deferred, result.Deferred != nil, "structural deferral is a separate no-work discriminator")
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &fields))
			assert.NotContains(t, fields, "invoked", "execution fact is live only, never a new persisted/wire store")
		})
	}
}
