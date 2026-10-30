package session

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// #1452 A2.8 (founder decision 2026-10-30): chat display streams saved
// records; the model context path is separate and stays whole.
// Requirement 3 (model context unchanged): the model path reads the literal
// stored model_message bytes whole, before and after a streamed display read
// of the same both-membership record.
func TestReadModelSlots_FullBytesUnchangedByDisplayStreaming(t *testing.T) {
	s, sid := newEffectsSession(t)
	ctx := context.Background()
	big := strings.Repeat("model<&>\u00e9 ", 30000)
	msg := providers.Message{Role: "user", Content: big}
	_, _, err := s.AppendModelMessage(ctx, sid, ModelAppend{Message: msg, ViewMembership: ViewMembershipBoth, Source: EntrySource{Kind: "user"}, AgentID: "mia"})
	require.NoError(t, err)
	mp, err := EncodeModelPayload(msg)
	require.NoError(t, err)
	wantRaw := historyEncodeJSON(t, mp)
	readSlots := func() {
		var raws [][]byte
		var contents []string
		require.NoError(t, s.ReadModelSlots(ctx, sid, 0, 0, func(slot ModelSlot, raw []byte) error {
			raws = append(raws, append([]byte(nil), raw...))
			contents = append(contents, slot.Message.Content)
			return nil
		}))
		require.Len(t, raws, 1)
		require.Equal(t, string(wantRaw), string(raws[0]), "the literal stored model_message bytes, whole")
		require.Equal(t, big, contents[0], "the model sees the full content")
	}
	readSlots()
	before := historyTree(t, s.BaseDir())
	page := historyRead(t, s, sid, historyOptions(20))
	require.Len(t, page.Entries, 1)
	require.Equal(t, len(big), page.Entries[0].Entry.DisplayContentOriginalSizeBytes(), "positive control: the display copy was shortened from a stand-in")
	require.Len(t, page.Entries[0].Entry.Content, ChatHistoryTextDisplayMaxBytes-1, "64 KiB cut backs off a split rune")
	require.Equal(t, before, historyTree(t, s.BaseDir()), "the display read changes no canonical, index or model state")
	readSlots()
}
