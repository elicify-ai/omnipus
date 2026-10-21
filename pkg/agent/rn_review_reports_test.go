package agent

// CR-2 / SFH-2 / DV-F1: report provenance is the durable generation, not
// unacknowledged status. A page-limited snapshot must disclose its limit.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func rnReviewQuietArgs(kind, id, text string) map[string]any {
	args := map[string]any{"kind": kind, "message_id": id}
	switch kind {
	case "progress":
		args["text"] = text
	case "checkpoint":
		args["summary"] = text
	case "artifact":
		args["paths"] = []any{text}
	}
	return args
}

func rnReviewGeneration(t *testing.T, msg generated.SessionMessage) *int {
	t.Helper()
	kind, err := msg.Discriminator()
	require.NoError(t, err)
	switch kind {
	case "progress":
		v, decodeErr := msg.AsSessionMessageProgress()
		require.NoError(t, decodeErr)
		return v.Generation
	case "checkpoint":
		v, decodeErr := msg.AsSessionMessageCheckpoint()
		require.NoError(t, decodeErr)
		return v.Generation
	case "artifact":
		v, decodeErr := msg.AsSessionMessageArtifact()
		require.NoError(t, decodeErr)
		return v.Generation
	default:
		t.Fatalf("unexpected report kind %q", kind)
		return nil
	}
}

func TestRNReview_QuietProducerRecordsItsGeneration(t *testing.T) {
	for _, kind := range []string{"progress", "checkpoint", "artifact"} {
		t.Run(kind, func(t *testing.T) {
			al, lifecycle, inbox, deliverer := newPWDeliverTestLoop(t)
			const parent, child = "rn-generation-parent", "rn-generation-child"
			pwSeedDeliverySessions(t, al, parent, child)
			tool, ctx := u4NewMessageParentTool(lifecycle, deliverer, child)
			for generation := 1; generation <= 2; generation++ {
				id := fmt.Sprintf("%s-generation-%d", kind, generation)
				result := tool.Execute(ctx, rnReviewQuietArgs(kind, id, "report-body"))
				require.NotNil(t, result)
				require.False(t, result.IsError, "%s", result.ForLLM)
				messages, _, _, err := inbox.Drain(parent, child, "", 0)
				require.NoError(t, err)
				require.Len(t, messages, generation)
				require.Equal(t, &generation, rnReviewGeneration(t, messages[generation-1]), "the actual producer must record the report's round")
				if generation == 1 {
					require.NoError(t, lifecycle.Mutate(child, func(rec *session.LifecycleRecord) error { rec.State = session.LifecycleCompleted; return nil }))
					newGeneration, reviveErr := al.steerCanceller().Revive(context.Background(), child, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "rn-owner"})
					require.NoError(t, reviveErr)
					require.Equal(t, 2, newGeneration, "normal terminal revival creates round two")
				}
			}
			require.Empty(t, al.bus.InboundChan(), "generation stamping must not make quiet reports wake")
		})
	}
}

func TestRNReview_CurrentSnapshotExcludesOlderAndUnknownRounds(t *testing.T) {
	al, lifecycle, inbox, deliverer := newPWDeliverTestLoop(t)
	const parent, child = "rn-round-parent", "rn-round-child"
	pwSeedDeliverySessions(t, al, parent, child)
	tool, ctx := u4NewMessageParentTool(lifecycle, deliverer, child)
	kinds := []string{"progress", "checkpoint", "artifact"}
	for _, kind := range kinds {
		result := tool.Execute(ctx, rnReviewQuietArgs(kind, "old-"+kind, "RN-OLD-"+kind))
		require.False(t, result.IsError, "%s", result.ForLLM)
	}
	require.NoError(t, lifecycle.Mutate(child, func(rec *session.LifecycleRecord) error { rec.State = session.LifecycleCompleted; return nil }))
	generation, err := al.steerCanceller().Revive(context.Background(), child, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "rn-owner"})
	require.NoError(t, err)
	require.Equal(t, 2, generation)
	for _, kind := range kinds {
		result := tool.Execute(ctx, rnReviewQuietArgs(kind, "current-"+kind, "RN-CURRENT-"+kind))
		require.False(t, result.IsError, "%s", result.ForLLM)
	}
	unknown := bootProgress(t, child, parent, "unknown-round")
	value, err := unknown.AsSessionMessageProgress()
	require.NoError(t, err)
	value.Generation, value.Text = nil, "RN-UNKNOWN-ROUND"
	require.NoError(t, unknown.FromSessionMessageProgress(value))
	_, err = inbox.Append(parent, unknown)
	require.NoError(t, err)
	final := bootHandback(t, child, parent, child+":2:final")
	handback, err := final.AsSessionMessageHandback()
	require.NoError(t, err)
	handback.Generation = &generation
	require.NoError(t, final.FromSessionMessageHandback(handback))
	_, err = deliverer.Deliver(context.Background(), steer.UpwardEvent{ChildSessionID: child, Outcome: steer.OutcomeFinalAnswer, Message: final})
	require.NoError(t, err)
	wake := narrationNextWake(t, al, parent)
	want := "A delegated session handed back (final): finished\n\nStored reports from helper " + child + " (these reports did not wake the parent):\n" +
		"progress current-progress: RN-CURRENT-progress\ncheckpoint current-checkpoint: RN-CURRENT-checkpoint\nartifact current-artifact: RN-CURRENT-artifact"
	require.Equal(t, want, wake.Content, "older and untagged reports are not current-round evidence")
	messages, _, _, err := inbox.Drain(parent, child, "", 0)
	require.NoError(t, err)
	require.Len(t, messages, 8, "filtering a snapshot must not remove historical inbox reports")
}

func TestRNReview_PageLimitedSnapshotDisclosesUnreadEntries(t *testing.T) {
	// The read-page boundary is 200 entries. The final itself is an extra
	// entry: 199 updates fit with it; 200 and 201 updates leave another page.
	for _, count := range []int{199, 200, 201} {
		t.Run(fmt.Sprintf("reports=%d", count), func(t *testing.T) {
			al, _, inbox, deliverer := newPWDeliverTestLoop(t)
			const parent, child = "rn-page-parent", "rn-page-child"
			pwSeedDeliverySessions(t, al, parent, child)
			inbox.InboxUnackedMax, inbox.InboxPerTypeCeiling, inbox.ChildSendRatePerMinute = 400, 400, 1000
			var expected []string
			for i := 1; i <= count; i++ {
				id := fmt.Sprintf("report-%03d", i)
				message := bootProgress(t, child, parent, id)
				progress, err := message.AsSessionMessageProgress()
				require.NoError(t, err)
				progress.Text = id
				require.NoError(t, message.FromSessionMessageProgress(progress))
				_, err = inbox.Append(parent, message)
				require.NoError(t, err)
				if i <= 200 {
					expected = append(expected, "progress "+id+": "+id)
				}
			}
			_, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{ChildSessionID: child,
				Outcome: steer.OutcomeFinalAnswer, Message: bootHandback(t, child, parent, child+":1:final")})
			require.NoError(t, err)
			if count >= 200 {
				expected = append(expected, "Additional inbox entries remain; use delegate inbox to read the complete retained report history.")
			}
			want := "A delegated session handed back (final): finished\n\nStored reports from helper " + child + " (these reports did not wake the parent):\n" + strings.Join(expected, "\n")
			require.Equal(t, want, narrationNextWake(t, al, parent).Content, "a later page must not be silently omitted below the byte budget")
			messages, _, more, err := inbox.Drain(parent, child, "", 400)
			require.NoError(t, err)
			require.False(t, more)
			require.Len(t, messages, count+1, "the bounded snapshot acknowledges none of its reports")
		})
	}
}
