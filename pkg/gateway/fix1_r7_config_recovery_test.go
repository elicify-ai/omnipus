package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// CR-T2-R7-02: omission is supported on load, not a different unapplied save.
func TestChannelWrite_NormalizedBulkSaveAllowsNextToolWrite(t *testing.T) {
	for _, explicitType := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-type=%v", explicitType), func(t *testing.T) {
			f, store, d := orderingWriterFixture(t)
			typeField := ""
			if explicitType {
				typeField = `"type":"whatsapp",`
			}
			body := `{"channels":{"whatsapp":{` + typeField + `"enabled":false,"allow_from":["alice"]}}}`
			rec := httptest.NewRecorder()
			f.api.HandleConfig(rec, httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body)))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			live := f.al.GetConfig()
			require.Equal(t, "whatsapp", live.Channels["whatsapp"].Type, "actual loader normalization was applied")
			require.False(t, live.Channels["whatsapp"].Enabled)
			assert.NoError(t, store.CheckConfigSnapshot(live), "a successfully applied normalized save is not unapplied")
			revision := orderingToolRevision(t, d)
			res := systools.NewChannelDisableTool(d).Execute(context.Background(), map[string]any{
				"id": "whatsapp", "expected_revision": revision,
			})
			require.NotNil(t, res)
			assert.False(t, res.IsError, "the next real tool write needs no extra reload: %s", res.ForLLM)
			assert.NotContains(t, res.ForLLM, "saved channel configuration is not applied")
		})
	}
}

// R7-A03: a durable deletion with failed live refresh must remain finalizable
// after the real prerequisite is restored; no native resource is present here.
func TestChannelDelete_SavedUnappliedRemovalCanReconcileAndRecreate(t *testing.T) {
	f, _, _ := orderingWriterFixture(t)
	_, registered := f.svc.ChannelManager.GetChannel("whatsapp")
	require.False(t, registered, "instrument: no native disposal can explain a retained deletion exclusion")
	original := f.api.credStore
	f.api.credStore = credentials.NewStore(filepath.Join(f.f1277.home, "credentials.json"))
	t.Cleanup(func() { f.api.credStore = original })
	deleted := httptest.NewRecorder()
	f.api.deleteChannelInstance(deleted, "whatsapp")
	require.Equal(t, http.StatusInternalServerError, deleted.Code, deleted.Body.String())
	raw, err := f.api.readChannelConfigRaw("whatsapp")
	require.NoError(t, err)
	require.Empty(t, raw, "the removal is durable even though live refresh failed")
	_, stillLive := f.al.GetConfig().Channels["whatsapp"]
	require.True(t, stillLive, "instrument: the live refresh really did fail")

	f.api.credStore = original
	require.NoError(t, f.api.refreshConfigAndRewireServices(f.f1277.configPath), "real saved-file reconciliation")
	_, stillLive = f.al.GetConfig().Channels["whatsapp"]
	require.False(t, stillLive)
	retryDelete := httptest.NewRecorder()
	f.api.deleteChannelInstance(retryDelete, "whatsapp")
	require.Contains(t, []int{http.StatusNotFound, http.StatusNoContent}, retryDelete.Code, retryDelete.Body.String())
	created := f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", "{}")
	assert.Equal(t, http.StatusOK, created.Code, "reconciliation must leave a reachable finalizer, not permanent 'being deleted': %s", created.Body.String())
	_, admissionErr := f.al.AdmitConnectPairing("whatsapp", "fresh-after-delete", orderingChat(t, f))
	assert.NoError(t, admissionErr, "no undisposed client exists: a fresh explicit admission must be possible after recovery")
}
