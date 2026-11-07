package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// T62 / A2.1 / B73: expected cuts are derived from the authored UTF-8 bytes,
// not the product shortener. The real append/index/reducer remain in use.
func TestChatHistoryPage_DisplayShorteningText(t *testing.T) {
	for _, kind := range []string{"", "message"} {
		for _, role := range []string{"user", "assistant"} {
			for _, n := range []int{65535, 65536, 65537} {
				t.Run(fmt.Sprintf("type_%s_role_%s_bytes_%d", kind, role, n), func(t *testing.T) {
					s, sid := newEffectsSession(t)
					body := strings.Repeat("x", n)
					want := body
					if n > 65536 {
						want = strings.Repeat("x", 65536)
					}
					require.NoError(t, s.AppendTranscriptStrict(sid, TranscriptEntry{ID: "ordinary", Type: EntryType(kind), Role: role, AgentID: "mia", Content: body, ViewMembership: ViewMembershipChat}))
					require.NoError(t, s.Close(), "join owned background work outside read-only check")
					before := historyTree(t, s.BaseDir())
					p, err := s.ReadChatHistoryPage(context.Background(), sid, historyOptions(20))
					require.NoError(t, err)
					require.Len(t, p.Entries, 1)
					require.Equal(t, want, p.Entries[0].Entry.Content, "A2.1: both omitted and explicit message types use the same 65536-byte display cut")
					require.Equal(t, before, historyTree(t, s.BaseDir()), "read cannot change canonical/index/model state")
				})
			}
		}
	}
	for _, role := range []string{"user", "assistant"} {
		t.Run("UTF8_"+role, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			// 65534 ASCII + one three-byte euro =65537 bytes; the euro
			// straddles the cut, so the complete preview is exactly65534.
			prefix := strings.Repeat("x", 65534)
			require.NoError(t, s.AppendTranscriptStrict(sid, TranscriptEntry{ID: "multibyte", Role: role, AgentID: "mia", Content: prefix + "€", ViewMembership: ViewMembershipChat}))
			p := historyRead(t, s, sid, historyOptions(20))
			require.Equal(t, prefix, p.Entries[0].Entry.Content, "never split a UTF-8 character")
			require.True(t, utf8.ValidString(p.Entries[0].Entry.Content))
		})
	}
	for _, kind := range []string{"judge_verdict", "provider_fallback", "system", "compaction", "context_window_notice", "turn_canceled"} {
		t.Run("structured_"+kind, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			body := `{"text":"` + strings.Repeat("s", 65537) + `"}`
			entry := TranscriptEntry{ID: "structured", Type: EntryType(kind), Role: "system", AgentID: "mia", Content: body, ViewMembership: ViewMembershipChat}
			if kind == "context_window_notice" {
				// The durable diagnostic requires its original turn and timestamp
				// before AppendTranscriptStrict validates it (Message.yaml type).
				entry.TurnID = "d2-structured-turn"
				entry.Timestamp = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
				entry.ContextWindowNotice = &generated.ContextWindowNotice{Kind: "mid_turn", Message: "classified notice"}
			}
			require.NoError(t, s.AppendTranscriptStrict(sid, entry))
			p := historyRead(t, s, sid, historyOptions(20))
			require.Len(t, p.Entries, 1)
			require.Equal(t, body, p.Entries[0].Entry.Content, "structured/parsing inputs are NEVER shortened")
		})
	}
}
