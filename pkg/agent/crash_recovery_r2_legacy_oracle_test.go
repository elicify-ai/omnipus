//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"strings"
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

// orphanACFindObservation returns the engine-authored observation the DESIGN-5
// projection inserts for callID. The observation is a fixture (like the archive
// records it completes); its specified fields are checked by r2CheckedObservation
// and its private prose is never used as an oracle.
func orphanACFindObservation(t *testing.T, view []providers.Message, callID string) providers.Message {
	t.Helper()
	for _, m := range view {
		if m.Role == "tool" && m.ToolCallID == callID && strings.Contains(m.Content, "tool_recovery_observation") {
			return m
		}
	}
	t.Fatalf("fixture: the recovered model view has no engine observation for %q", callID)
	return providers.Message{}
}

// orphanACDesign5View builds the reconciled DESIGN-5 model view for a fixture:
// the retained raw archive lines in order, with the engine observation
// (ephemeral, ordinal -1) inserted at obsAt when the projection produces one.
//
// DESIGN 5 (and its 2026-10-26 "Trim shares the projection" addendum): retain
// the assistant declaration and every durable sibling result, fill only the
// missing occurrence with the engine observation, and exclude the marker. Every
// retained record keeps its ORIGINAL archive address; the inserted observation
// is ephemeral.
func orphanACDesign5View(t *testing.T, h *cwR1Harness, raw []providers.Message, lines []int, obsCallID string, obsAt int) ([]providers.Message, []int) {
	t.Helper()
	view := make([]providers.Message, 0, len(lines)+1)
	view = append(view, orphanACPick(raw, lines...)...)
	addresses := make([]int, 0, len(lines)+1)
	addresses = append(addresses, lines...)
	if obsCallID == "" {
		return view, addresses
	}
	assembled := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	obs := orphanACFindObservation(t, assembled[1:], obsCallID)
	view = append(view[:obsAt:obsAt], append([]providers.Message{obs}, view[obsAt:]...)...)
	addresses = append(addresses[:obsAt:obsAt], append([]int{-1}, addresses[obsAt:]...)...)
	return view, addresses
}
