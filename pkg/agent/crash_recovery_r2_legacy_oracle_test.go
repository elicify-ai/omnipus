//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const r2ObservationContent = "SPEC: engine-owned Unknown recovery observation, not an original return"

// DESIGN 2/5 specifies the observation's semantic fields and exact issuing
// occurrence, not its private explanatory prose or serialization order. Check
// every specified field before normalizing ONLY that unspecified content for
// the existing whole-list/order/address equality assertions.
func r2CheckedObservation(t *testing.T, actual providers.Message, id string, issuer session.ArchiveAddress) providers.Message {
	t.Helper()
	require.Equal(t, "tool", actual.Role)
	require.Equal(t, id, actual.ToolCallID)
	var fields map[string]any
	require.NoError(t, json.Unmarshal([]byte(actual.Content), &fields))
	require.Equal(t, "tool_recovery_observation", fields["type"])
	require.Equal(t, "unknown", fields["disposition"])
	require.Equal(t, false, fields["original_result_recovered"])
	require.Equal(t, crashFields(t, issuer), fields["assistant"], "exact assistant occurrence, never a global call-ID substitute")
	actual.Content = r2ObservationContent
	return actual
}

func r2ExpectedObservation(id string) providers.Message {
	return providers.Message{Role: "tool", ToolCallID: id, Content: r2ObservationContent}
}

func r2Issuer(t *testing.T, snapshot session.WindowView, ordinal int) session.ArchiveAddress {
	t.Helper()
	slot, ok := snapshot.Slot(ordinal)
	require.True(t, ok, "fixture's independently specified issuer ordinal %d exists", ordinal)
	require.Equal(t, "assistant", slot.Message.Role)
	return slot.Addr
}
