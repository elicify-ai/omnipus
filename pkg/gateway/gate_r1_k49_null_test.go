// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestK49Options_NullAndWrongTypeRefusedWithoutWrite is Train 2 gate r1
// architect F05: available_in_channel_turns is a non-null boolean
// (SecretOptionsUpdateRequest.yaml). JSON null must be refused with 400 and no
// write — Go's decoder would otherwise leave the generated bool at false and
// silently switch an enabled flag off.
func TestK49Options_NullAndWrongTypeRefusedWithoutWrite(t *testing.T) {
	f := newWU5GatewayFixture(t, true)
	refs := f.storePrivateVersion(t, "sales", wu5ValueV1)
	require.Len(t, refs, 1)
	url := fmt.Sprintf(wu5OptionsURL, refs[0])

	rec := f.do(http.MethodPatch, url, `{"available_in_channel_turns":true}`)
	require.Equal(t, http.StatusOK, rec.Code, "control: a true flip is accepted; got %d %s", rec.Code, rec.Body.String())

	for _, body := range []string{
		`{"available_in_channel_turns":null}`,
		`{"available_in_channel_turns":"false"}`,
		`{"available_in_channel_turns":0}`,
	} {
		refused := f.do(http.MethodPatch, url, body)
		require.Equal(t, http.StatusBadRequest, refused.Code, "%s must be refused as malformed; got %d %s", body, refused.Code, refused.Body.String())
		entry := f.entryFor(refs)
		require.NotNil(t, entry.AvailableInChannelTurns)
		require.True(t, *entry.AvailableInChannelTurns, "%s must not change the stored flag", body)
	}

	rec = f.do(http.MethodPatch, url, `{"available_in_channel_turns":false}`)
	require.Equal(t, http.StatusOK, rec.Code, "control: a false flip is accepted; got %d %s", rec.Code, rec.Body.String())
	entry := f.entryFor(refs)
	require.NotNil(t, entry.AvailableInChannelTurns)
	require.False(t, *entry.AvailableInChannelTurns, "control: false is stored")
}
