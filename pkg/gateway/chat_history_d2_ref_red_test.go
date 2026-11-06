package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatHistoryPage_DisplayShorteningKeepsExistingRef(t *testing.T) {
	s, sid, home := pagingProjectionFixture(t)
	store := newToolResultStore(home)
	full := []byte(`{"text":"` + strings.Repeat("x", 100000) + `"}`)
	ref, ok := store.saveJSON(sid, full)
	require.True(t, ok, "real pre-existing live offload fixture must be persisted")
	value := map[string]any{"_ref": true, "ref": ref, "original_size_bytes": float64(len(full)), "preview": "existing live preview"}
	d2AppendResult(t, s, sid, value)
	require.NoError(t, s.Close())
	before := pagingProjectionTree(t, home)
	g := d2PrepareOnlyGroup(t, s, sid, historyPageProjectionDeps{ToolStore: store})
	require.Equal(t, value, d2Result(t, g), "already persisted _ref stays _ref, not a history preview")
	for _, raw := range g.frames {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["type"] == "tool_call_result" {
			require.Equal(t, value, frame["result"])
		}
	}
	require.Equal(t, before, pagingProjectionTree(t, home), "read must not offload/rewrite an existing reference")
	body, err := store.readByRef(sid, ref)
	require.NoError(t, err)
	require.Equal(t, full, body)
}
