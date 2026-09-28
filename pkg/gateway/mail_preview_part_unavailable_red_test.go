package gateway

// RED (round 8, silent-failure-hunter F7 — LOW). Oracle: FR-019/MC-42 family
// + the finding's ruling: an inline part whose data was unavailable at fetch
// (over the 25 MiB per-part fetch cap, or a decode failure) must NOT be
// served as HTTP 200 with 0 bytes — a broken image indistinguishable from a
// corrupt one. It must return the same status family the attachment download
// uses for the identical condition: rest_mail_read.go::handleMailAttachment
// refuses a DataUnavailable part with 413
// (http.StatusRequestEntityTooLarge). On HEAD the mint drops the
// DataUnavailable flag at the mailPreviewInline boundary and servePart
// answers 200/0-byte for an empty-data image part.
//
// The unavailable condition is constructed with an image-typed inline part
// carrying NO data (what an over-cap/undecodable part becomes at mint —
// handleMint copies ContentType+Data only). A 0-byte image is degenerate by
// construction; the honest contract is: no 200-with-empty-image. The
// positive control (a part WITH data serves 200 with its bytes) proves the
// route machinery works and that the refusal targets the empty-data case.

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMailPreviewPart_UnavailableDataRefusedLikeAttachmentDownload(t *testing.T) {
	useFreshMailPreviewServeLimiter(t)
	fixture, store, token, srv, apiHits := newProductionMailPreviewChain(t)
	require.NotNil(t, fixture)
	require.NotEmpty(t, token)
	require.Zero(t, apiHits.Load())

	tokOK, merr := store.mint("c:qa-part-ok", mailPreviewGrant{
		HTML: mailSanitizePreviewHTML("<p>ctrl</p>", nil, nil),
		Inline: []mailPreviewInline{
			{ContentType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
		},
	})
	require.NoError(t, merr)

	tokNA, merr := store.mint("c:qa-part-na", mailPreviewGrant{
		HTML: mailSanitizePreviewHTML("<p>unavailable part</p>", nil, nil),
		Inline: []mailPreviewInline{
			{ContentType: "image/png"}, // no Data — the unavailable-data condition
		},
	})
	require.NoError(t, merr)

	// Positive control: a part WITH data serves 200 with its bytes.
	resp, err := http.Get(srv.URL + mailPreviewPathPrefix + "part/" + tokOK + "/0")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"control: a part with data serves 200 (proves the route machinery runs)")
	require.Equal(t, []byte{0x89, 'P', 'N', 'G'}, body,
		"control: the served bytes are the part's bytes")

	// RED: the unavailable-data part must NOT 200/0-byte — the attachment
	// download path refuses the identical condition with 413.
	respNA, err := http.Get(srv.URL + mailPreviewPathPrefix + "part/" + tokNA + "/0")
	require.NoError(t, err)
	bodyNA, err := io.ReadAll(respNA.Body)
	require.NoError(t, err)
	require.NoError(t, respNA.Body.Close())
	if respNA.StatusCode == http.StatusOK && len(bodyNA) == 0 {
		t.Fatalf("F7: an unavailable inline part is served as 200 with 0 bytes — the attachment download refuses the identical condition with 413 (StatusRequestEntityTooLarge)")
	}
	require.Equal(t, http.StatusRequestEntityTooLarge, respNA.StatusCode,
		"F7: an unavailable inline part must carry the attachment path's 413 status family, not 200")
}
