// loop_media_dataurl_budget_test.go — the data-URL budget path of
// normalizeImageDataURLToBudget (issue #1435 requirement B, follow-ups from
// the train-6b reviews): a corrupt over-budget history image must be
// replaced by an honest marker naming the real cause (decode failure vs
// too large), never passed through for the provider to reject as
// media_too_large on every later iteration of a tool loop.
package agent

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// issue1435DataURLBudget is the byte budget the fixtures below exceed.
const issue1435DataURLBudget = 50_000

// corruptOverBudgetDataURL builds a data:image/png URL whose header (IHDR)
// still decodes to a 600x400 image but whose body is truncated, so
// image.DecodeConfig succeeds while the full image.Decode fails — the shape
// of a real truncated screenshot persisted into history. The payload is far
// above issue1435DataURLBudget.
func corruptOverBudgetDataURL(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, photoLikeImage(600, 400)))
	full := buf.Bytes()
	require.Greater(t, len(full), issue1435DataURLBudget, "fixture must be over budget")
	truncated := full[:len(full)*3/4] // keep signature + IHDR + partial IDAT, drop IEND
	if _, _, err := image.DecodeConfig(bytes.NewReader(truncated)); err != nil {
		t.Fatalf("fixture: DecodeConfig must succeed on the truncated header: %v", err)
	}
	if _, _, err := image.Decode(bytes.NewReader(truncated)); err == nil {
		t.Fatal("fixture: the truncated payload must fail the full decode")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(truncated)
}

// TestIssue1435_CorruptOverBudgetDataURLIsReplacedNotPassedThrough: a
// corrupt image that is also over budget cannot be made to fit, so it must
// come back empty (the callers replace it with the visible marker). Passing
// it through sends the provider an over-budget payload it rejects with
// media_too_large on every later loop iteration — the exact incident
// #1435 set out to fix.
func TestIssue1435_CorruptOverBudgetDataURLIsReplacedNotPassedThrough(t *testing.T) {
	dataURL := corruptOverBudgetDataURL(t)
	got, cause := normalizeImageDataURLToBudget(dataURL, issue1435DataURLBudget, catalog.ResizeLimits{})
	require.Empty(t, got,
		"a corrupt over-budget image must be replaced by the marker, never passed through")
	require.Equal(t, imageFitUndecodable, cause,
		"the reported cause must be the decode failure, not a resize failure")
}

// TestIssue1435_CorruptOverBudgetMarkerNamesTheRealCause: the honest marker
// for a decode failure must say the data is unreadable, not claim the image
// was "too large ... even after resizing" — the resize ladder never ran.
func TestIssue1435_CorruptOverBudgetMarkerNamesTheRealCause(t *testing.T) {
	dataURL := corruptOverBudgetDataURL(t)
	msgs := []providers.Message{{Role: "user", Content: "look at this", Media: []string{dataURL}}}
	out := fitOrdinaryImageMediaToBudget(msgs, catalog.ResizeLimits{
		MaxBytes:   issue1435DataURLBudget,
		LongEdgePx: 4000,
	})
	require.Len(t, out, 1)
	require.NotContains(t, out[0].Media, dataURL,
		"the corrupt over-budget payload must not be sent to the provider")
	marker := "[attachment unavailable: image (data unreadable: corrupt or truncated, and over the model's size limits)]"
	require.Contains(t, out[0].Content, marker,
		"the marker must name the decode failure, not a resize failure")
	require.NotContains(t, out[0].Content, "even after resizing",
		"the resize wording must not appear for a decode failure")
}

// TestIssue1435_HistoryDataURLDecodeFailureMarkerReachesTheModel: the same
// honest marker for a corrupt over-budget history image through the
// resolveMediaRefsWithOffload path (what the provider actually receives).
func TestIssue1435_HistoryDataURLDecodeFailureMarkerReachesTheModel(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)

	dataURL := corruptOverBudgetDataURL(t)
	msgs := []providers.Message{{Role: "user", Content: "history image", Media: []string{dataURL}}}
	out := resolveMediaRefsWithOffload(msgs, store, issue1435DataURLBudget,
		"openai-chatgpt", "gpt-6.1-sol", nil, nil, nil, "")
	require.Len(t, out, 1)
	require.NotContains(t, out[0].Media, dataURL,
		"the corrupt over-budget history payload must not be sent")
	require.True(t, strings.Contains(out[0].Content,
		"[attachment unavailable: image (data unreadable: corrupt or truncated, and over the model's size limits)]"),
		"the model-facing content must carry the decode-failure marker, got %q", out[0].Content)
}

// TestIssue1435_DataURLNormalizeIsCachedAcrossCalls: an oversized history
// image sits in canonical history with its original bytes, so EVERY provider
// round of a tool loop re-pays the full base64 decode, image.Decode and
// resize ladder for it. The second identical normalize must be served from
// the process-wide normalize cache — the same LRU the file-ref path already
// uses (keyed by sha256 of the raw payload plus the budget) — not re-decoded.
func TestIssue1435_DataURLNormalizeIsCachedAcrossCalls(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, photoLikeImage(600, 400)))
	require.Greater(t, buf.Len(), issue1435DataURLBudget, "fixture must be over budget")
	overBudget := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	// The real callers always pass a budget with a long edge (the default
	// 7680 when no catalog row applies); ResizeToFit requires both fields.
	budget := catalog.ResizeLimits{LongEdgePx: 7680}

	before := library.GlobalNormalizeCacheStats()
	first, cause := normalizeImageDataURLToBudget(overBudget, issue1435DataURLBudget, budget)
	require.NotEmpty(t, first, "fixture: the image must normalize below the budget")
	require.Empty(t, cause)
	mid := library.GlobalNormalizeCacheStats()

	second, cause2 := normalizeImageDataURLToBudget(overBudget, issue1435DataURLBudget, budget)
	require.Equal(t, first, second, "same input and budget must produce the same output")
	require.Empty(t, cause2)
	after := library.GlobalNormalizeCacheStats()

	require.Equal(t, int64(1), mid.Misses-before.Misses,
		"the first normalize is a cache miss")
	require.Greater(t, after.Hits-before.Hits, int64(0),
		"the second identical normalize must be a cache hit, not another full decode-and-resize")
}
