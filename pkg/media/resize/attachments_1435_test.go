package resize_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// Oracle: #1435 Expected item 2 supplies the ChatGPT 30,000-patch ceiling.
// OpenAI's image guide supplies ceil(width/32)*ceil(height/32), independently
// of Omnipus's implementation. No expected resized dimensions are invented.
func TestIssue1435_CatalogResizeFitsChatGPTPatchLimit(t *testing.T) {
	const (
		patchEdge  = 32
		maxPatches = 30_000
	)
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err)
	handle := cat.Resolve("openai-chatgpt", "gpt-6.1-sol")
	require.True(t, handle.Found(), "fixture must exercise the actual embedded provider/model row")
	require.True(t, handle.Supports(catalog.ModalityImage))
	budget := handle.Budget()

	for _, tc := range []struct {
		name                 string
		width, height, count int
	}{
		{"one_patch_control", 32, 32, 1},
		// 131*229 = 29,999; 150*200 = 30,000. Both are below the
		// embedded long-edge ceiling, so that separate limit cannot hide a
		// missing patch budget.
		{"one_below_limit", 4192, 7328, 29_999},
		{"exactly_at_limit", 4800, 6400, 30_000},
		// 5,537 rounds UP to 174 patches per side: 174^2 = 30,276.
		{"rounded_patch_grid_exceeds_limit", 5537, 5537, 30_276},
		{"over_patch_limit_but_not_long_edge_or_bytes", 6144, 6400, 38_400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputPatches := ((tc.width + patchEdge - 1) / patchEdge) * ((tc.height + patchEdge - 1) / patchEdge)
			require.Equal(t, tc.count, inputPatches, "independent fixture arithmetic must be exact")
			// Gray keeps the large, highly compressible fixture bounded in
			// memory. It exceeds patches, not the catalog's encoded-byte cap.
			input := image.NewGray(image.Rect(0, 0, tc.width, tc.height))
			result, err := resize.ResizeToFit(input, budget)
			require.NoError(t, err, "#1435: a valid image should be downscaled, not discarded")
			decoded, _, err := image.Decode(bytes.NewReader(result.Data))
			require.NoError(t, err, "output must fully decode; a plausible image header alone is not enough")
			outWidth, outHeight := decoded.Bounds().Dx(), decoded.Bounds().Dy()
			require.Positive(t, outWidth)
			require.Positive(t, outHeight)
			require.LessOrEqual(t, outWidth, tc.width, "downscaling must not enlarge the source")
			require.LessOrEqual(t, outHeight, tc.height, "downscaling must not enlarge the source")
			require.Equal(t, max(outWidth, outHeight), result.LongEdge, "reported size must match encoded pixels")
			require.LessOrEqual(t, max(outWidth, outHeight), budget.LongEdgePx)
			require.LessOrEqual(t, int64(len(result.Data)), budget.MaxBytes)
			patches := ((outWidth + patchEdge - 1) / patchEdge) * ((outHeight + patchEdge - 1) / patchEdge)
			require.LessOrEqual(t, patches, maxPatches,
				"#1435: catalog resize_limits must constrain patches as well as edge/bytes; outbound %dx%d requires %d patches, limit %d",
				outWidth, outHeight, patches, maxPatches)
		})
	}
}
