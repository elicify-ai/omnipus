package gateway

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// D10/D15: this is a transport assertion adapter, not a test-local page reader.
// Every source is obtained through actual HTTP and the generated envelope. An
// explicit before operation crosses each reached clear boundary deliberately.
func historyMigrationAllPages(t *testing.T, sid string, fetch func(string) (int, []byte)) []generated.Message {
	t.Helper()
	messages := make([]generated.Message, 0)
	seenPositions := map[string]bool{}
	seenCursors := map[string]bool{}
	query, mode := "", "latest"
	for {
		status, body := fetch(query)
		require.Equal(t, http.StatusOK, status, "D15 all-retained history read: %s", body)
		var page generated.SessionHistoryResponse
		require.NoError(t, json.Unmarshal(body, &page), "D10 rejects bare arrays and malformed envelopes")
		require.Equal(t, sid, page.SessionId)
		pagingShape(t, page, page.HasEarlier, mode == "older")
		historyMigrationMetadata(t, page.PageInfo, mode)
		message, serverError := validateBodyAgainstSchema("SessionHistoryResponse", body)
		require.False(t, serverError, "generated history schema must compile")
		require.Empty(t, message, "D15 never turns a wrong shape into empty history")
		for _, entry := range page.Entries {
			require.NotNil(t, entry.HistoryPosition)
			position := *entry.HistoryPosition
			require.False(t, seenPositions[position], "exclusive older pages must not overlap source occurrences")
			seenPositions[position] = true
		}
		messages = append(page.Entries, messages...) // pages and sources stay ascending
		if !page.HasEarlier {
			require.Nil(t, page.NextBefore, "terminal page has no fabricated continuation")
			return messages
		}
		require.NotEmpty(t, page.Entries, "a continuing page must make source progress")
		require.NotNil(t, page.NextBefore)
		require.NotEmpty(t, *page.NextBefore)
		require.False(t, seenCursors[*page.NextBefore], "D15 repeated cursor cannot silently truncate or loop")
		seenCursors[*page.NextBefore] = true
		query, mode = "?before="+url.QueryEscape(*page.NextBefore), "older"
	}
}
