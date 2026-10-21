package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Gate round 1, silent-failure SF-04: a malformed percent-encoding in the
// history query must be refused with ruling D3's field-specific 400, never
// silently dropped (url.URL.Query discards malformed pairs, which would turn
// a broken cursor into "omitted" and serve the newest page).
func TestTrain3Fix1_HistoryMalformedEncodingIsRefused(t *testing.T) {
	rh := newRoomsHarness(t)
	roomID := l1332NewSquadRoom(t, rh, "history-malformed")
	l1332SeedDense(t, rh, roomID, 3)

	const limitErr = `{"error":"limit must be an integer from 1 to 1000"}`
	const cursorErr = `{"error":"before_seq must be a positive int64 integer"}`
	for query, want := range map[string]string{
		"?before_seq=%ZZ":              cursorErr,
		"?limit=%ZZ":                   limitErr,
		"?limit=1&limit=%ZZ":           limitErr,
		"?before_seq=2&before_seq=%ZZ": cursorErr,
		"?limit=%ZZ&before_seq=%ZZ":    limitErr, // limit is validated first (D3 order)
	} {
		status, body := l1332Get(rh, roomID, query)
		require.Equal(t, http.StatusBadRequest, status, "%s: body=%s", query, body.Body.String())
		require.JSONEq(t, want, body.Body.String(), query)
	}
	// Control: a well-formed request still pages.
	page := l1332GetPage(t, rh, roomID, "?before_seq=3&limit=10")
	require.Equal(t, []string{"entry 1", "entry 2"}, l1332Texts(t, page))
}
