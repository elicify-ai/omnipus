package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// T-01 actual Execute, not just a synthesized Result: real extractor info
// supplies 30 pages; the renderer is intentionally absent (AD-15 fallback).
func TestPreviewDocument_RealPDFPageClamps(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	svc := officepreview.Service(t, false, false)
	ws := t.TempDir()
	data := officepreview.PDF(30)
	path := filepath.Join(ws, "thirty.pdf")
	require.NoError(t, os.WriteFile(path, data, 0600))
	sum := sha256.Sum256(data)
	tool := NewPreviewDocumentTool(ws, true, WithPreviewService(svc))
	cases := []struct {
		pages, line, next string
		first, last       int
		refusal           string
	}{
		{"3", "pages 3–3 of 30", "4-13", 3, 3, ""},
		{"2-5", "pages 2–5 of 30", "6-15", 2, 5, ""},
		{"", "pages 1–10 of 30", "11-20", 1, 10, ""},
		{"11-20", "pages 11–20 of 30", "21-30", 11, 20, ""},
		{"25-40", "pages 25–30 of 30 (requested 25–40)", "", 25, 30, ""},
		{"31", "", "", 0, 0, "preview_document: the document has 30 pages"},
	}
	for _, tc := range cases {
		t.Run("pages_"+tc.pages, func(t *testing.T) {
			args := map[string]any{"file": path}
			if tc.pages != "" {
				args["pages"] = tc.pages
			}
			got := tool.Execute(context.Background(), args)
			if tc.refusal != "" {
				require.True(t, got.IsError)
				require.Equal(t, tc.refusal, got.ForLLM)
				return
			}
			require.False(t, got.IsError, "%s", got.ForLLM)
			lines := strings.Split(got.ForLLM, "\n")
			require.GreaterOrEqual(t, len(lines), 2)
			require.Equal(t, "thirty.pdf (version "+hex.EncodeToString(sum[:])[:12]+")", lines[0])
			require.Equal(t, tc.line, lines[1])
			require.Equal(t, tc.first, got.DocumentPreview.Record.FirstPage)
			require.Equal(t, tc.last, got.DocumentPreview.Record.LastPage)
			require.Equal(t, 30, got.DocumentPreview.Record.PageCount)
			if tc.next == "" {
				require.NotContains(t, got.ForLLM, "next batch:")
			} else {
				require.Contains(t, got.ForLLM, "next batch: call preview_document with pages \""+tc.next+"\"")
			}
			require.Empty(t, got.Media, "thumbnails are not generic model-media refs")
		})
	}
}

// AD-5 syntax boundary table independent of renderer/component availability.
func TestPreviewDocument_PageSyntaxNumericBoundaries(t *testing.T) {
	cases := []struct {
		raw         any
		first, last int
		valid       bool
	}{
		{"0", 0, 0, false}, {"1", 1, 1, true}, {"2", 2, 2, true},
		{"9998", 9998, 9998, true}, {"9999", 9999, 9999, true}, {"10000", 0, 0, false},
		{"1-19", 1, 19, true}, {"1-20", 1, 20, true}, {"1-21", 0, 0, false},
		{"-1", 0, 0, false}, {"3-2", 0, 0, false}, {"1.5", 0, 0, false}, {3, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.raw), func(t *testing.T) {
			first, last, def, ok := parsePreviewPages(map[string]any{"pages": tc.raw})
			require.Equal(t, tc.valid, ok)
			if tc.valid {
				require.Equal(t, tc.first, first)
				require.Equal(t, tc.last, last)
				require.False(t, def)
			}
		})
	}
}
