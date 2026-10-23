//go:build goolm && stdjson

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Decode the real HTTP writer bytes, not a generated struct whose nil/default
// value could hide a present JSON key. A map of RawMessage is observation only,
// not a parallel transport type or validator.
func crashProducerSessionJSON(t *testing.T, f *i1R1BootFixture, id string) map[string]json.RawMessage {
	t.Helper()
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, json.Valid(w.Body.Bytes()), "instrument: actual REST output is valid encoded JSON")
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	raw, exists := envelope["session"]
	require.True(t, exists, "instrument: inspect the actual session response, not an empty result")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.JSONEq(t, `"`+id+`"`, string(fields["id"]))
	return fields
}

// CONTRACT-SHAPE-DECISION D / Session.yaml::execution: only the producer may
// emit this field, and any Interrupted projection must omit the key entirely.
func TestCrashRecovery_ProducerInterruptedRESTJSONOmitsExecution(t *testing.T) {
	for _, c := range []struct {
		name   string
		reason session.InterruptionReason
		state  session.LifecycleState
	}{
		{"durable_tool_limit", session.InterruptionToolLimit, session.LifecycleInterrupted},
		{"durable_reconciliation", session.InterruptionReconciliationRequired, session.LifecycleInterrupted},
		{"prior_boot_running", "", session.LifecycleRunning},
		{"prior_boot_queued", "", session.LifecycleQueued},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newI1R1BootFixture(t)
			liveID := f.root(t, session.LifecycleRunning, f.boot.Current())
			live := crashProducerSessionJSON(t, f, liveID)
			require.JSONEq(t, `"working"`, string(live["lifecycle_state"]))
			require.Contains(t, live, "execution", "positive instrument: a genuine current Running record produces execution")
			require.JSONEq(t, `"running"`, string(live["execution"]))

			epoch := f.oldEpoch
			initial := c.state
			if c.state == session.LifecycleInterrupted {
				epoch, initial = f.boot.Current(), session.LifecycleRunning
			}
			id := f.root(t, initial, epoch)
			wantReason := "restart"
			if c.state == session.LifecycleInterrupted {
				record, err := f.ls.Load(id)
				require.NoError(t, err)
				record.State, record.InterruptionReason = c.state, c.reason
				require.NoError(t, f.ls.Persist(record), "persist a genuine typed ending through the public producer path")
				wantReason = string(c.reason)
			}
			before := f.journal(t, id)
			fields := crashProducerSessionJSON(t, f, id)
			assert.JSONEq(t, `"interrupted"`, string(fields["lifecycle_state"]))
			assert.JSONEq(t, `"`+wantReason+`"`, string(fields["interruption_reason"]))
			_, present := fields["execution"]
			assert.False(t, present, "Interrupted REST JSON must omit execution, not emit running, queued or null")
			assert.Equal(t, before, f.journal(t, id), "projecting output cannot rewrite the stored producing execution")
		})
	}
}
