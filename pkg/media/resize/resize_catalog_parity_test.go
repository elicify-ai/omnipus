// resize_catalog_parity_test.go — DS-6 / SC-012, the seed-parity regression
// for the capabilities→catalog fold (ADR-067 T067-07).
//
// The retired pkg/providers/capabilities package carried a hand-written 1.0.0
// seed of 78 models, each with its own resize budget, and the media pipeline
// resolved a budget from it by MODEL ID alone. ADR-067 replaced that with a
// registry-fed 2.0.0 catalog resolved by the exact (provider, model) pair.
// This test walks all 78 of those models and pins, row by row, what the new
// catalog resolves for each — and then proves the resize pipeline actually
// honours the budget it resolved.
//
// The dataset (pkg/providers/catalog/testdata/seed_parity.json) classifies
// every row:
//
//   - divergence "none"      — the catalog resolves exactly what the seed did.
//     These 14 rows are the real parity oracle: their expectations come from
//     the retired seed file, not from the snapshot under test.
//   - divergence "corrected" — the registry disagrees with the hand-written
//     seed (33 rows). Each names its correction_source.
//   - divergence "retired"   — the (provider, model) pair is absent from the
//     registry snapshot entirely (31 rows) and falls to the FR-004 miss
//     defaults: optimistic text+image, document default resize limits.
//
// Read the "retired" count as a finding, not as a pass: those 31 models lost
// their vendor-specific budgets when the registry snapshot replaced the seed.
// The test fails if that count moves in either direction, so the next snapshot
// bump has to be looked at by a person.
package resize_test

import (
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

type parityLimits struct {
	LongEdgePx int   `json:"long_edge_px"`
	MaxBytes   int64 `json:"max_bytes"`
}

type parityRow struct {
	SeedProvider            string       `json:"seed_provider"`
	Provider                string       `json:"provider"`
	Model                   string       `json:"model"`
	SeedInputModalities     []string     `json:"seed_input_modalities"`
	SeedResizeBudget        parityLimits `json:"seed_resize_budget"`
	ExpectedFound           bool         `json:"expected_found"`
	ExpectedInputModalities []string     `json:"expected_input_modalities"`
	ExpectedResizeLimits    parityLimits `json:"expected_resize_limits"`
	Divergence              string       `json:"divergence"`
	CorrectionSource        string       `json:"correction_source"`
}

type parityDataset struct {
	GeneratedFrom          string         `json:"generated_from"`
	CatalogSnapshotVersion string         `json:"catalog_snapshot_version"`
	CatalogSource          string         `json:"catalog_source"`
	Counts                 map[string]int `json:"counts"`
	Rows                   []parityRow    `json:"rows"`
}

func loadParityDataset(t *testing.T) parityDataset {
	t.Helper()
	path := filepath.Join("..", "..", "providers", "catalog", "testdata", "seed_parity.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "DS-6 dataset must be committed at %s", path)
	var ds parityDataset
	require.NoError(t, json.Unmarshal(data, &ds))
	return ds
}

// TestMediaResize_BudgetsUnchangedForSeedModels (DS-6, SC-012) is the whole
// dataset walked against the real embedded snapshot: modalities, resize
// limits, and the resize pipeline's actual behaviour under each budget.
func TestMediaResize_BudgetsUnchangedForSeedModels(t *testing.T) {
	ds := loadParityDataset(t)
	require.Len(t, ds.Rows, 78, "DS-6 is exactly the 78 models of the retired 1.0.0 seed")

	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err, "the embedded snapshot must parse")

	seen := make(map[string]struct{}, len(ds.Rows))
	counts := map[string]int{}
	budgets := map[parityLimits][]string{}

	for _, row := range ds.Rows {
		key := row.Provider + "/" + row.Model
		_, dup := seen[key]
		require.False(t, dup, "duplicate DS-6 row %s", key)
		seen[key] = struct{}{}
		counts[row.Divergence]++

		t.Run(key, func(t *testing.T) {
			h := cat.Resolve(row.Provider, row.Model)
			require.Equal(t, row.ExpectedFound, h.Found(),
				"row %s: catalog presence must match the dataset", key)

			got := make([]string, 0, len(h.InputModalities()))
			for _, m := range h.InputModalities() {
				got = append(got, string(m))
			}
			sort.Strings(got)
			assert.Equal(t, row.ExpectedInputModalities, got,
				"row %s: resolved input modalities", key)

			budget := h.Budget()
			assert.Equal(t, row.ExpectedResizeLimits,
				parityLimits{LongEdgePx: budget.LongEdgePx, MaxBytes: budget.MaxBytes},
				"row %s: resolved resize limits", key)

			switch row.Divergence {
			case "none":
				// The parity oracle: expectations equal the retired seed's own
				// values, so this row proves the fold changed nothing.
				assert.Equal(t, row.SeedInputModalities, row.ExpectedInputModalities,
					"row %s is marked unchanged, so its expectation must equal the seed value", key)
				assert.Equal(t, row.SeedResizeBudget, row.ExpectedResizeLimits,
					"row %s is marked unchanged, so its budget must equal the seed budget", key)
				assert.Empty(t, row.CorrectionSource,
					"an unchanged row carries no correction_source")
			case "corrected", "retired":
				assert.NotEmpty(t, row.CorrectionSource,
					"row %s diverges from the seed and must name its correction_source (F-33)", key)
			default:
				t.Fatalf("row %s: unknown divergence %q", key, row.Divergence)
			}
		})
		budgets[row.ExpectedResizeLimits] = append(budgets[row.ExpectedResizeLimits], key)
	}

	// The behavioural half: the pipeline must actually honour every budget the
	// catalog resolved across the 78 rows. Encoding is the expensive part, so
	// each DISTINCT budget is exercised once rather than once per row — the
	// coverage is identical (rows sharing a budget share an outcome) and the
	// test stays seconds rather than a minute. A 4000×3000 source is larger
	// than every long-edge ceiling in play, so each case is a real shrink.
	require.NotEmpty(t, budgets)
	src := syntheticImage(4000, 3000)
	for budget, models := range budgets {
		limits := catalog.ResizeLimits{LongEdgePx: budget.LongEdgePx, MaxBytes: budget.MaxBytes}
		result, resizeErr := resize.ResizeToFit(src, limits)
		require.NoError(t, resizeErr, "resize under budget %+v (models %v)", budget, models)
		assert.LessOrEqual(t, result.LongEdge, budget.LongEdgePx,
			"output long edge must respect budget %+v (models %v)", budget, models)
		assert.LessOrEqual(t, int64(len(result.Data)), budget.MaxBytes,
			"output size must respect budget %+v (models %v)", budget, models)
	}

	// The dataset's own tallies must match what was just walked, and must
	// match the file's recorded counts — so a snapshot bump that silently
	// retires or corrects more seed models fails here instead of passing
	// unnoticed.
	assert.Equal(t, ds.Counts, counts,
		"the dataset's recorded divergence counts must match its rows")
	assert.Equal(t, 14, counts["none"],
		"14 of the 78 seed models resolve exactly as they did before the fold")
	assert.Equal(t, 33, counts["corrected"],
		"33 seed models resolve to registry-corrected modalities or limits")
	assert.Equal(t, 31, counts["retired"],
		"31 seed models are absent from the registry snapshot and fall to the FR-004 miss defaults — "+
			"if this moves, the snapshot changed which models it carries")
}

// TestMediaResize_MissServesDocumentDefault pins the FR-004 consumer contract
// the 31 retired rows rely on: a (provider, model) miss hands the media path
// the document's default resize limits, never a zero budget the pipeline would
// reject.
func TestMediaResize_MissServesDocumentDefault(t *testing.T) {
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err)

	h := cat.Resolve("openai", "a-model-that-does-not-exist")
	require.False(t, h.Found())
	budget := h.Budget()
	require.Positive(t, budget.LongEdgePx, "a miss must never serve a zero long edge")
	require.Positive(t, budget.MaxBytes, "a miss must never serve a zero byte ceiling")

	result, err := resize.ResizeToFit(syntheticImage(4000, 3000), budget)
	require.NoError(t, err)
	assert.LessOrEqual(t, result.LongEdge, budget.LongEdgePx)
	assert.LessOrEqual(t, int64(len(result.Data)), budget.MaxBytes)
}

// syntheticImage builds a deterministic, non-uniform RGBA image. A flat colour
// would compress to a few bytes and make every byte-ceiling assertion pass
// vacuously; the gradient keeps the encoded size meaningful.
func syntheticImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((x * 7) % 251),
				G: uint8((y * 13) % 241),
				B: uint8((x*y)%239 + 8),
				A: 255,
			})
		}
	}
	return img
}
