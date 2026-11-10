package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Chief Q3 A / NEW-SF-2: an explicitly empty graph is stored as no graph, not
// an allocated empty slice. The partial-response revision must match read-back.
func TestRoomsFix4_PartialGraphClearReportsReadBackRevision(t *testing.T) {
	h := wu15NewHarness(t)
	j := &fix4RoomJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home))}
	fix4InstallJournal(t, h, j)
	h.create("squad", "Launch", "mia", "jim")
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/sales", nil)
	require.Equal(t, http.StatusOK, status)
	var before gen.Workspace
	require.NoError(t, json.Unmarshal(body, &before))
	setup := fmt.Sprintf(`{"revision":%q,"delegation":[{"from_agent":"mia","to_agent":"jim"}]}`, before.Revision)
	status, body = h.rawHTTP(http.MethodPut, "/api/v1/workspaces/sales", []byte(setup))
	require.Equal(t, http.StatusOK, status, "healthy setup graph: %s", body)
	actual, err := workspace.ReadState(h.home, "sales")
	require.NoError(t, err)
	require.Len(t, actual.Delegation, 1)
	j.readFault.Store(true)
	clearRequest := fmt.Sprintf(`{"revision":%q,"delegation":[]}`, actual.Revision)
	status, body = h.rawHTTP(http.MethodPut, "/api/v1/workspaces/sales", []byte(clearRequest))
	require.Equal(t, http.StatusInternalServerError, status, "saved graph clear plus room-sync failure: %s", body)
	require.Positive(t, j.hits.Load(), "instrument: room synchronization failed")
	requireRoomContract(t, "ConfigurationMutationState", json.RawMessage(body))
	var failure gen.ConfigurationMutationState
	require.NoError(t, json.Unmarshal(body, &failure))
	require.Equal(t, gen.ConfigurationMutationStatePersistenceStatusPartial, failure.PersistenceStatus)
	require.Equal(t, []string{"delegation"}, failure.ChangedFields)
	after, err := workspace.ReadState(h.home, "sales")
	require.NoError(t, err)
	require.Nil(t, after.Delegation, "storage canonicalizes an empty graph to absent")
	require.NotEqual(t, actual.Revision, after.Revision)
	require.Equal(t, after.Revision, failure.Revision, "partial response must report the actually-saved graph representation")
	j.readFault.Store(false)
	retry := fmt.Sprintf(`{"revision":%q,"description":"retry after saved graph clear"}`, failure.Revision)
	status, body = h.rawHTTP(http.MethodPut, "/api/v1/workspaces/sales", []byte(retry))
	require.Equal(t, http.StatusOK, status, "the reported saved revision must be accepted without an intervening edit: %s", body)
}
