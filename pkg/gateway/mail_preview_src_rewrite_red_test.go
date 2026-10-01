package gateway

// RED (round 8, silent-failure-hunter F6 — LOW). Oracle: FR-019 ("inline
// cid: images render via the token-scoped part route") with the finding's
// shape: pkg/gateway/rest_mail_preview.go::mailRewritePreviewSources matches
// src="cid:…" case- and quote-exactly, so real-world mail HTML — SRC="cid:
// (Outlook normalizes attribute names to upper case) and unquoted src=cid:
// values — never rewrites, fails the post-sanitize src allowlist, and the
// inline (or consented remote) image VANISHES from the preview with no
// marker. Rewriting must cover the case/quote variants like the lower-case
// quoted form. The extract side (mailExtractRemoteImageURLs) is already
// case-insensitive; only the rewrite is case/quote-exact.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/email"
)

const f6Cid = "image001@x.test"

func TestMailRewritePreviewSources_CaseAndQuoteVariants(t *testing.T) {
	inlines := []email.MailPart{{ContentID: "<" + f6Cid + ">"}}
	partPath := mailPreviewPartPrefix + mailTokenPlaceholder + "/0"

	t.Run("positive control: lower-case quoted rewrites", func(t *testing.T) {
		out := mailRewritePreviewSources(`<img src="cid:`+f6Cid+`">`, inlines, nil)
		assert.Contains(t, out, `src="`+partPath+`"`,
			"the existing lower-case quoted rewrite is the contract every variant must match")
		assert.NotContains(t, out, "cid:"+f6Cid)
	})

	t.Run("upper-case SRC attribute rewrites like the lower-case form", func(t *testing.T) {
		out := mailRewritePreviewSources(`<img SRC="cid:`+f6Cid+`">`, inlines, nil)
		require.Contains(t, out, partPath,
			"FR-019: an upper-case SRC cid reference must land on the token-scoped part route, not vanish from the preview")
		require.NotContains(t, out, "cid:"+f6Cid,
			"an unrewritten cid: value fails the post-sanitize allowlist and the image is stripped silently")
	})

	t.Run("unquoted src=cid rewrites like the quoted form", func(t *testing.T) {
		out := mailRewritePreviewSources(`<img src=cid:`+f6Cid+`>`, inlines, nil)
		require.Contains(t, out, partPath,
			"FR-019: an unquoted cid reference must land on the token-scoped part route")
		require.NotContains(t, out, "cid:"+f6Cid,
			"an unrewritten unquoted cid: value is stripped by the sanitizer — the image vanishes")
	})

	t.Run("consented remote image with upper-case SRC rewrites", func(t *testing.T) {
		remote := "https://remota.invalid/img.png"
		out := mailRewritePreviewSources(`<img SRC="`+remote+`">`, nil, []string{remote})
		require.Contains(t, out, mailPreviewImgPrefix+mailTokenPlaceholder+"/0",
			"a consented remote image must land on the token-scoped proxy route regardless of attribute case")
		require.NotContains(t, out, remote,
			"an unrewritten https URL fails the post-sanitize allowlist and the consented image is dropped")
	})
}
