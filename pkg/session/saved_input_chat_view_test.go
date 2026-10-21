package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// Oracle: the wake-bubble UAT fix dispatch. Only the event row and reply are
// visible; the saved continuation remains exact user-role model input, once.
// PW-D16 still requires save-before-placement, including across a store reopen.
func TestSavedWakeInput_ChatViewHidesInputAndKeepsModelDelivery(t *testing.T) {
	bodies := []string{
		"A delegated session handed back (final): B1-HELPER-4412",
		"Goal verdict: met. Conditions: all met. Reason: the file exists.",
		"Interrupted by a restart.",
	}
	for i, body := range bodies {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("event-%d/legacy-%t", i, legacy), func(t *testing.T) {
				root := t.TempDir()
				s, err := NewUnifiedStore(root)
				require.NoError(t, err)
				meta, err := s.NewSession(SessionTypeChat, "webchat", "mia")
				require.NoError(t, err)
				sid := meta.ID
				ctx := context.Background()
				id := "saved-input-child:1:final"
				wantInput := providers.Message{Role: "user", Content: body}
				var addr ArchiveAddress
				if legacy {
					// Exact old writer envelope, not a mocked history reader.
					payload, encodeErr := EncodeModelPayload(wantInput)
					require.NoError(t, encodeErr)
					archive, archiveErr := s.archiveStore(sid)
					require.NoError(t, archiveErr)
					addr, err = archive.Append(ArchiveRecord{
						TranscriptEntry: TranscriptEntry{ID: id, Role: "user", Content: body,
							AgentID: "mia", Timestamp: time.Now().UTC(), ViewMembership: ViewMembershipChat},
						ModelMessage: &payload, Source: &EntrySource{Kind: "agent"},
					})
				} else {
					addr, err = s.SaveChatInput(sid, id, "mia", body)
				}
				require.NoError(t, err)
				retry, err := s.SaveChatInput(sid, id, "mia", body)
				require.NoError(t, err)
				require.Equal(t, addr, retry, "retry must keep the original saved payload")

				// A person's identical text is a positive control against hiding by
				// prose. The independent event row and parent reply must survive.
				visible := []TranscriptEntry{
					{ID: "person", Role: "user", Content: body},
					{ID: "event", Type: EntryTypeSystem, Role: "system", SystemSubtype: "helper_event",
						HelperEvent: &generated.HelperEvent{HelperSessionId: "child", Kind: "finished", Text: body, CreatedAt: time.Now().UTC()}},
					{ID: "reply", Role: "assistant", Content: "The helper's result reached me."},
				}
				for _, entry := range visible {
					entry.AgentID, entry.Timestamp = "mia", time.Now().UTC()
					_, err = s.AppendTranscriptAddressed(sid, entry)
					require.NoError(t, err)
				}
				view, err := s.ReadTranscriptView(sid)
				require.NoError(t, err)
				require.Equal(t, []string{"person", "event", "reply"}, viewIDs(view.Entries),
					"history must not expose the raw wake input as a user bubble")
				full, err := s.ReadTranscript(sid)
				require.NoError(t, err)
				require.Equal(t, []string{id, "person", "event", "reply"}, viewIDs(full),
					"hide only in the chat view; recovery keeps the saved input")
				if !legacy {
					require.Equal(t, EntryType("saved_input"), full[0].Type, "new input must be explicitly private, not an ordinary user entry")
				}
				pending, err := s.PendingChatInputs(ctx, sid)
				require.NoError(t, err)
				require.Equal(t, []string{id}, viewIDs(pending))
				require.Equal(t, body, pending[0].Content)
				window, err := s.WindowView(ctx, sid)
				require.NoError(t, err)
				require.Empty(t, window.Live, "saving must not place the input before the safe fence")

				_, err = s.SaveChatInput(sid, id, "mia", "changed wake body")
				require.EqualError(t, err, fmt.Sprintf("saved input %q already exists with different content or membership", id))
				slot, _, err := s.PlaceSavedInput(ctx, sid, addr)
				require.NoError(t, err)
				require.Equal(t, wantInput, slot.Message, "the model must receive the original exact wake input")

				s, err = NewUnifiedStore(root)
				require.NoError(t, err)
				view, err = s.ReadTranscriptView(sid)
				require.NoError(t, err)
				require.Equal(t, []string{"person", "event", "reply"}, viewIDs(view.Entries), "reload must preserve the same visible rows")
				gotAddr, placed, err := s.SavedChatInput(sid, id)
				require.NoError(t, err)
				require.Equal(t, addr, gotAddr)
				require.True(t, placed)
				retry, err = s.SaveChatInput(sid, id, "mia", body)
				require.NoError(t, err)
				require.Equal(t, addr, retry)
				_, _, err = s.PlaceSavedInput(ctx, sid, addr)
				require.ErrorIs(t, err, ErrAlreadyConsumed)
				window, err = s.WindowView(ctx, sid)
				require.NoError(t, err)
				require.Len(t, window.Live, 1, "reopen/retry must not duplicate model delivery")
				require.Equal(t, wantInput, window.Live[0].Message)
				pending, err = s.PendingChatInputs(ctx, sid)
				require.NoError(t, err)
				require.Empty(t, pending)
			})
		}
	}
	t.Run("private-type-without-legacy-prefix", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		_, err := s.SaveChatInput(sid, "internal-outcome", "mia", bodies[0])
		require.NoError(t, err)
		view, err := s.ReadTranscriptView(sid)
		require.NoError(t, err)
		require.Empty(t, view.Entries, "private classification must not depend on the legacy id convention")
	})
	t.Run("legacy-near-matches-stay-visible", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		viewAppend(t, s, sid, "saved-input-", "user", bodies[0], "", 1)
		viewAppend(t, s, sid, "saved-input-assistant", "assistant", bodies[0], "", 2)
		viewAppend(t, s, sid, "saved-input-system", "system", bodies[0], EntryTypeSystem, 3)
		viewAppend(t, s, sid, "saved-input-other-type", "user", bodies[0], EntryTypeSystem, 4)
		archive, err := s.archiveStore(sid)
		require.NoError(t, err)
		payload, err := EncodeModelPayload(providers.Message{Role: "user", Content: bodies[0]})
		require.NoError(t, err)
		_, err = archive.Append(ArchiveRecord{
			TranscriptEntry: TranscriptEntry{ID: "saved-input-both", Role: "user", Content: bodies[0],
				AgentID: "mia", Timestamp: time.Now().UTC(), ViewMembership: ViewMembershipBoth},
			ModelMessage: &payload, Source: &EntrySource{Kind: "user"},
		})
		require.NoError(t, err)
		view, err := s.ReadTranscriptView(sid)
		require.NoError(t, err)
		require.Equal(t, []string{"saved-input-", "saved-input-assistant", "saved-input-system", "saved-input-other-type", "saved-input-both"}, viewIDs(view.Entries))
	})
}
