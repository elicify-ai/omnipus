//go:build !cgo

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

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// TestConfigureChannel_PersistsIdentity covers Spec-2 US-5 / FR-2.5: a configure
// request carrying a valid agent identity persists it (kind+id) and writes the
// type discriminator, and a single-instance configure is accepted (not 422).
func TestConfigureChannel_PersistsIdentity(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"identity":{"kind":"agent","id":"concierge"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "single-instance configure must be accepted, body=%s", w.Body.String())

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	tg := diskCfg["channels"].(map[string]any)["telegram"].(map[string]any)
	assert.Equal(t, "telegram", tg["type"], "type discriminator must be persisted (FR-2.5)")
	ident, ok := tg["identity"].(map[string]any)
	require.True(t, ok, "identity must be persisted")
	assert.Equal(t, "agent", ident["kind"])
	assert.Equal(t, "concierge", ident["id"])
}

// TestConfigureChannel_RejectsInvalidIdentity covers the 422 validation path: an
// agent-kind identity with no id is malformed and must be rejected up front so
// identity routing is never a silent no-op.
func TestConfigureChannel_RejectsInvalidIdentity(t *testing.T) {
	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)

	for _, body := range []string{
		`{"identity":{"kind":"agent"}}`,                    // missing id
		`{"identity":{"kind":"agent","id":""}}`,            // empty id
		`{"identity":{"kind":"bogus"}}`,                    // bad kind
		`{"identity":{"id":"x"}}`,                          // missing kind
		`{"identity":{"kind":"agent","id":"x","extra":1}}`, // unknown field
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.configureChannel(w, r, "telegram")
		assert.Equal(
			t,
			http.StatusUnprocessableEntity,
			w.Code,
			"invalid identity %q must be 422, body=%s",
			body,
			w.Body.String(),
		)
	}
}

// TestConfigureChannel_NullIdentityClears confirms an explicit null identity is a
// clear (not a validation error).
func TestConfigureChannel_NullIdentityClears(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"type":"telegram","enabled":false,"identity":{"kind":"agent","id":"x"}}}}`,
	)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"identity":null}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "null identity (clear) must be accepted, body=%s", w.Body.String())
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

// TestSetChannelEnabled_Cap1Duplicate422 covers FR-2.3 / US-2: when config.json
// already holds two instances of one type (a hand-edited duplicate), a REST
// config write surfaces the load-time cap-1 violation as a clean 422
// "one-per-type" rather than an opaque 500.
func TestSetChannelEnabled_Cap1Duplicate422(t *testing.T) {
	api := newChannelTestAPI(
		t,
		`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"type":"telegram","enabled":true},"telegram-2":{"type":"telegram","enabled":true}}}`,
	)

	w := httptest.NewRecorder()
	api.setChannelEnabled(w, "discord", true)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"cap-1 duplicate must surface as 422, not 500; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "one-per-type")
}
