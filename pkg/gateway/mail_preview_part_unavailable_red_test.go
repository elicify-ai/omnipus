package gateway

// RED (round 8, silent-failure-hunter F7 — LOW). Oracle: FR-019/MC-42 family
// + the finding's ruling: an inline part whose data is unavailable at fetch
// (over the 25 MiB per-part fetch cap, or a decode failure) must NOT be
// served as HTTP 200 with 0 bytes — a broken image indistinguishable from a
// corrupt one. It must return the same status family the attachment download
// uses for the identical condition: the attachment path refuses an
// unavailable part with 413 (http.StatusRequestEntityTooLarge).
//
// RE-DERIVED for the w5 metadata-only grant (US-6.1/MC-11 — the spec's §2.4
// row orders the pinned preview families re-derived from the spec, never
// weakened). Under the payload-carrying design the condition lived in the
// GRANT: an inline entry with no Data. Message grants now carry no part
// bytes at all — parts are fetched live within the serve request — so the
// SAME condition is constructed through the REAL transport, exactly the
// shape production produces it in: an image part whose transfer-encoding
// cannot decode leaves the view with DataUnavailable set and Data nil while
// the part itself stays in Inline (pkg/email view.go's default branch). The
// protection asserted is unchanged and the positive control is stronger for
// it: the servable and the unavailable part come from ONE staged message
// served through the production chain, so the 413 cannot come from a dead
// route or a missing message.

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMailPreviewPart_UnavailableDataRefusedLikeAttachmentDownload(t *testing.T) {
	useFreshMailPreviewServeLimiter(t)
	fixture, _, token, srv, apiHits := newProductionMailPreviewChain(t)
	require.NotNil(t, fixture)
	require.NotEmpty(t, token)
	require.Zero(t, apiHits.Load())

	// Positive control: the staged message's servable inline PNG (part
	// index 0) serves 200 with ITS decoded bytes — the full 8-byte PNG
	// signature mailPreviewChainRaw's base64 carries.
	resp, err := http.Get(srv.URL + mailPreviewPathPrefix + "part/" + token + "/0")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"control: a part with data serves 200 (proves the route machinery and the live fetch run)")
	require.Equal(t, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, body,
		"control: the served bytes are the staged part's decoded bytes")

	// RED: the unavailable-data part (part index 1 — the corrupt-base64
	// image the transport delivered with Data nil) must NOT 200/0-byte — the
	// attachment path refuses the identical condition with 413.
	respNA, err := http.Get(srv.URL + mailPreviewPathPrefix + "part/" + token + "/1")
	require.NoError(t, err)
	bodyNA, err := io.ReadAll(respNA.Body)
	require.NoError(t, err)
	require.NoError(t, respNA.Body.Close())
	if respNA.StatusCode == http.StatusOK && len(bodyNA) == 0 {
		t.Fatalf("F7: an unavailable inline part is served as 200 with 0 bytes — the attachment download refuses the identical condition with 413 (StatusRequestEntityTooLarge)")
	}
	require.Equal(t, http.StatusRequestEntityTooLarge, respNA.StatusCode,
		"F7: an unavailable inline part must carry the attachment path's 413 status family, not 200 (got %d, body %.200s)",
		respNA.StatusCode, bodyNA)
}
