package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// T65 / A2.2 / B76: the full-entry route uses the same pinned-cut retention
// revalidation as pages. Real native sweep during the actual request, not a
// fabricated tombstone or a sleep chosen to race a filesystem read.
func TestHistoryEntryRead_ExpiryDuringPinnedRead(t *testing.T) {
	d2RequireEntryRoute(t)
	f := r1CleanupBuildFixture(t, historyMeasurementDriver(t))
	t.Cleanup(func() { require.NoError(t, f.store.Close()) })
	t.Setenv("OMNIPUS_BEARER_TOKEN", "d2-route-test-token")
	position := pagingPositionOracle(f.sid, session.ArchiveAddress{PartitionKey: "2026-01-01", ByteOffset: 0, EntryID: "r1-old"})
	prior := d2EntryRequest(f.api, f.sid, position, "Bearer d2-route-test-token", nil)
	require.Equal(t, 200, prior.Code, "positive control: this same-session occurrence existed before retention")
	var before generated.Message
	require.NoError(t, json.Unmarshal(prior.Body.Bytes(), &before))
	require.Equal(t, "r1-old", before.Id)
	require.NotNil(t, before.Content)
	require.Equal(t, "R1_BODY_CANARY", *before.Content)

	var response *httptest.ResponseRecorder
	work := r1CleanupStart(t, func(ctx context.Context) error {
		response = d2EntryRequest(f.api, f.sid, position, "Bearer d2-route-test-token", ctx)
		return nil
	})
	// Existing external-Context instrument proves the real core reader pinned
	// this generation before the real sweep removes the canonical partition.
	r1CleanupRetirePinned(t, f)
	require.NoError(t, r1CleanupJoin(t, work))
	require.NotNil(t, response)
	d2EntryRefusal(t, response, 410, "history entry expired", r1CleanupCanaries(f, position)...)
	require.NoFileExists(t, f.retired, "reader releases its old-generation pin before replying")

	// Obtain the retained occurrence selector from a real public page. It is
	// an opaque request input, not an oracle for the independently seeded body.
	page := pagingRead(t, f.api, f.sid, "limit=20&min_rows=1")
	// Retention can append its own expiry notice; it does not change which
	// independently seeded occurrence this positive control must recover.
	var retainedEntries []generated.Message
	for _, candidate := range page.Entries {
		if candidate.Id == "r1-current" {
			retainedEntries = append(retainedEntries, candidate)
		}
	}
	require.Len(t, retainedEntries, 1, "the retained fixture occurrence remains readable exactly once")
	require.NotNil(t, retainedEntries[0].HistoryPosition)
	retained := *retainedEntries[0].HistoryPosition
	alive := d2EntryRequest(f.api, f.sid, retained, "Bearer d2-route-test-token", nil)
	require.Equal(t, 200, alive.Code, "retention is not a blanket route/session failure")
	var after generated.Message
	require.NoError(t, json.Unmarshal(alive.Body.Bytes(), &after))
	require.Equal(t, "r1-current", after.Id)
	require.NotNil(t, after.Content)
	require.Equal(t, "R1_RETAINED_BODY_CANARY", *after.Content)
}
