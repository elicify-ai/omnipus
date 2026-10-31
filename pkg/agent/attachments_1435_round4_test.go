package agent

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/stretchr/testify/require"
)

// Issue #1435 Round 4 item 3 (PRT F4): the normalize cache key must keep
// distinguishing budgets that differ ONLY in the patch ceiling. Same bytes,
// model slot, byte limit and edge limit; loose ceiling, tighter ceiling,
// tighter ceiling again. The tight result must be respected on the second
// call (no unsafe reuse of the loose artifact) and the third call must
// reuse the TIGHT artifact (identical bytes), never the loose one.
func TestIssue1435R4_PatchLimitOnlyBudgetMissesCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch-only.png")
	// 512×512 = 16×16 = 256 patches: the loose ceiling keeps it whole, the
	// tight ceiling forces a real downscale (512→384→288, long edge ≥ the
	// 256-px ladder floor), so the two budgets produce different artifacts.
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, 512, 512))))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	const (
		slot    = "issue1435-r4-patch-only"
		maxSize = 10 << 20
	)
	loose := catalog.ResizeLimits{LongEdgePx: 7680, MaxBytes: maxSize, MaxPatches: 30000}
	tight := catalog.ResizeLimits{LongEdgePx: 7680, MaxBytes: maxSize, MaxPatches: 100}

	first := encodeImageToDataURLCached(path, "image/png", info, maxSize, slot, loose)
	second := encodeImageToDataURLCached(path, "image/png", info, maxSize, slot, tight)
	third := encodeImageToDataURLCached(path, "image/png", info, maxSize, slot, tight)

	firstWidth, firstHeight, _ := issue1435DecodeOutboundImage(t, first)
	require.Equal(t, 512, firstWidth, "loose ceiling keeps the full source (256 patches fits)")
	require.Equal(t, 512, firstHeight)

	secondWidth, secondHeight, _ := issue1435DecodeOutboundImage(t, second)
	require.LessOrEqual(t, issue1435PatchCount(secondWidth, secondHeight), 100,
		"#1435 R4 item 3: the tighter patch ceiling must be respected, not served from the loose cache entry")
	require.LessOrEqual(t, max(secondWidth, secondHeight), 320,
		"#1435 R4 item 3: tight output must be a real downscale of the same source")
	require.Greater(t, max(secondWidth, secondHeight), 256,
		"#1435 R4 item 3: the tight downscale must stay above the resize ladder floor")

	thirdWidth, thirdHeight, _ := issue1435DecodeOutboundImage(t, third)
	require.Equal(t, secondWidth, thirdWidth, "tight reuse must stay tight")
	require.Equal(t, secondHeight, thirdHeight)
	require.Equal(t, second, third,
		"#1435 R4 item 3: the third call must reuse the TIGHT artifact byte-identically, never the loose one")
}
