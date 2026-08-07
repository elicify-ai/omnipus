package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestConfigureChannel_RejectsIdentity covers the FINAL-REVIEW HIGH security fix:
// binding (identity) must NOT be settable via /configure — it bypasses the FR-006
// CoreTeam-membership check that lives only in setChannelRouting. configureChannel
// now rejects any identity field with 400 and directs the caller to the routing
// endpoint, and persists NOTHING (the config entry is untouched).
func TestConfigureChannel_RejectsIdentity(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	// Even a well-formed agent identity is rejected — binding goes through routing.
	for _, body := range []string{
		`{"identity":{"kind":"agent","id":"concierge"}}`, // well-formed but forbidden here
		`{"identity":{"kind":"user"}}`,                   // user-kind override also forbidden
		`{"identity":null}`,                              // even an explicit clear must route through /routing
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.configureChannel(w, r, "telegram")
		assert.Equal(t, http.StatusBadRequest, w.Code,
			"identity via configure %q must be 400, body=%s", body, w.Body.String())
		assert.Contains(t, w.Body.String(), "routing",
			"400 body must direct the caller to the routing endpoint, body=%s", w.Body.String())
	}

	// Nothing was persisted: the channels map is still empty (no telegram entry).
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	_, hasTelegram := channels["telegram"]
	assert.False(t, hasTelegram, "a rejected identity-configure must persist nothing")
}

// TestConfigureChannel_RejectsWorkspaceID covers the FINAL-REVIEW HIGH security
// fix: workspace_id must NOT be settable via /configure. Persisting workspace_id
// (with an identity) makes IsWorkspaceBound() true and routes a workspace's
// inbound traffic at the configured agent WITHOUT the CoreTeam check. configure
// rejects it with 400 and directs the caller to the routing endpoint.
func TestConfigureChannel_RejectsWorkspaceID(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"workspace_id":"sales"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"workspace_id via configure must be 400, body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "routing",
		"400 body must direct the caller to the routing endpoint, body=%s", w.Body.String())

	// Nothing persisted.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	_, hasTelegram := channels["telegram"]
	assert.False(t, hasTelegram, "a rejected workspace_id-configure must persist nothing")
}

// TestConfigureChannel_StripsFilesystemPathFields covers the FINAL-REVIEW HIGH
// security fix: filesystem-path fields (session_store_path, crypto_database_path,
// service_account_file) are stripped from a configure body so an attacker cannot
// persist an attacker-chosen path that a later deleteChannelInstance would
// os.RemoveAll. The configure succeeds (paths are silently ignored, not rejected)
// but NONE of the path fields land in config.json.
func TestConfigureChannel_StripsFilesystemPathFields(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/channels/whatsapp/configure",
		strings.NewReader(
			`{"session_store_path":"/etc","crypto_database_path":"/root/.ssh","service_account_file":"/home/victim/secrets.json"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "whatsapp")
	require.Equal(t, http.StatusOK, w.Code,
		"configure with only path fields must still succeed (paths stripped), body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	channels, _ := diskCfg["channels"].(map[string]any)
	wa, _ := channels["whatsapp"].(map[string]any)
	require.NotNil(t, wa, "whatsapp entry must exist (type discriminator persisted)")
	for _, f := range []string{"session_store_path", "crypto_database_path", "service_account_file"} {
		_, present := wa[f]
		assert.False(t, present, "filesystem-path field %q must be stripped, never persisted", f)
	}
}

// TestHandleChannels_SurfacesInstanceIDAndIdentity covers FR-2.5: GET
// /api/v1/channels surfaces instance_id and identity for configured instances.
func TestHandleChannels_SurfacesInstanceIDAndIdentity(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"type":"telegram","enabled":true,"identity":{"kind":"agent","id":"concierge"}}}}`,
	)
	// HandleChannels reads the in-memory loop config; load the on-disk channels
	// (with the configured instance + identity) into it.
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	api.HandleChannels(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var entries []gen.ChannelEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	var tg *gen.ChannelEntry
	var webchat *gen.ChannelEntry
	for i := range entries {
		switch entries[i].Id {
		case "telegram":
			tg = &entries[i]
		case "webchat":
			webchat = &entries[i]
		}
	}
	require.NotNil(t, tg, "telegram entry must be present")
	require.NotNil(t, tg.InstanceId, "telegram instance_id must be populated (FR-2.5)")
	assert.Equal(t, "telegram", *tg.InstanceId)
	require.NotNil(t, tg.Identity, "telegram identity must be populated (FR-2.5)")
	assert.Equal(t, gen.ChannelEntryIdentityKindAgent, tg.Identity.Kind)
	require.NotNil(t, tg.Identity.Id)
	assert.Equal(t, "concierge", *tg.Identity.Id)

	// A channel with no configured instance has no instance_id/identity.
	require.NotNil(t, webchat, "webchat entry must be present")
	assert.Nil(t, webchat.InstanceId, "webchat (built-in) must have no instance_id")
	assert.Nil(t, webchat.Identity, "webchat (built-in) must have no identity")
}

// NOTE: the former TestSetChannelEnabled_Cap1Duplicate422 was removed with
// ADR-029 — the one-instance-per-type cap is LIFTED (N instances per type are
// now allowed and are the whole point of the feature). Instance-id UNIQUENESS
// (a specific "<type>.<slug>" cannot be created twice) is covered by
// TestCreateChannelInstance_Duplicate_Returns409; multi-instance activation by
// TestInitChannels_NInstancesPerType / TestValidateChannels_MultipleInstancesPerTypeAllowed.
