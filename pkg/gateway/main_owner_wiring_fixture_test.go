package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Owner wiring pack: oracles are chief I1-I3 and the owner-fix brief, not
// observed outputs. Real session persistence, schedule selection, REST handlers,
// and WebSocket stages remain under test. Only existing harness process-edge
// fakes (the external model/executor) are reused. No mutation probes here;
// independent CHECK owns mutation proof for any tests already passing.
//
// Planned checks: creator/person vs operator vs no-account creation; main-only,
// human-only adoption after a saved send; no overwrite/adoption on read/failure;
// exact recency/file preservation. Historical fixture times avoid sleeps.
// CHECK should kill removed actor forwarding, cli stamping, adoption before
// append, adoption on read, and recency bumps. Stop/redirect and Windows are
// explicitly outside this dispatch; no UI rendering or live-model claim is made.

func ownerWiringAccounts() []config.UserConfig {
	return []config.UserConfig{
		{Username: ""}, {Username: "cli"}, {Username: "operator"},
		{Username: "alice"}, {Username: "bob"},
	}
}

func ownerWiringFreshMeta(t *testing.T, store *session.UnifiedStore, id string) *session.UnifiedMeta {
	t.Helper()
	fresh, err := session.NewUnifiedStore(store.BaseDir())
	require.NoError(t, err, "open an independent disk-reading store")
	t.Cleanup(func() { require.NoError(t, fresh.Close()) })
	meta, err := fresh.GetMeta(id)
	require.NoError(t, err, "read the persisted owner without the writing cache")
	return meta
}

func ownerWiringIdentityBytes(t *testing.T, store *session.UnifiedStore, id string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), id, "meta.json"))
	require.NoError(t, err, "read the actual identity file")
	return raw
}

func ownerWiringHistoricalRecency(t *testing.T, store *session.UnifiedStore, id string) time.Time {
	t.Helper()
	require.NoError(t, store.FlushSessionStats(id), "settle any fixture stats before changing its clock")
	var doc map[string]any
	require.NoError(t, json.Unmarshal(ownerWiringIdentityBytes(t, store, id), &doc))
	created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) // Arbitrary historical fixture, not a clock tolerance.
	updated := created.Add(time.Hour)
	doc["created_at"] = created.Format(time.RFC3339Nano)
	doc["updated_at"] = updated.Format(time.RFC3339Nano)
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(store.BaseDir(), id, "meta.json"), raw, 0o600))
	store.EvictSessionMeta(id)
	meta, err := store.GetMeta(id)
	require.NoError(t, err)
	require.Equal(t, updated, meta.UpdatedAt, "fixture must expose the historical recency")
	return updated
}

func ownerWiringRESTGet(t *testing.T, api *restAPI, path, actor string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if actor != "" {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: actor}))
	}
	w := httptest.NewRecorder()
	api.HandleSessions(w, r)
	return w
}
