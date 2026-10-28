package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestChatHistoryPage_DisplayShorteningTextMetadata(t *testing.T) {
	for _, kind := range []session.EntryType{"", session.EntryTypeMessage} {
		for _, role := range []string{"user", "assistant"} {
			for _, large := range []bool{false, true} {
				t.Run(fmt.Sprintf("type_%s_%s_large_%t", kind, role, large), func(t *testing.T) {
					s, sid, _ := pagingProjectionFixture(t)
					full := strings.Repeat("v", 65536)
					want := full
					if large {
						want = strings.Repeat("v", 65534)
						full = want + "€"
					}
					require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "d2-text-carrier", Type: kind, Role: role, AgentID: "mia", ViewMembership: session.ViewMembershipChat, Content: full}))
					g := d2PrepareOnlyGroup(t, s, sid)
					require.NotNil(t, g.message.Content)
					require.Equal(t, want, *g.message.Content, "ordinary omitted/explicit message has identical UTF8 cut")
					if large {
						require.NotNil(t, g.message.ContentOriginalSizeBytes)
						require.Equal(t, 65537, *g.message.ContentOriginalSizeBytes)
					} else {
						require.Nil(t, g.message.ContentOriginalSizeBytes)
					}
					count := 0
					for _, raw := range g.frames {
						var frame map[string]any
						require.NoError(t, json.Unmarshal(raw, &frame))
						if frame["type"] == "replay_message" {
							count++
							require.Equal(t, want, frame["content"])
							if large {
								require.Equal(t, float64(65537), frame["content_original_size_bytes"])
							} else {
								require.NotContains(t, frame, "content_original_size_bytes")
							}
						}
					}
					require.Equal(t, 1, count)
				})
			}
		}
	}
}
