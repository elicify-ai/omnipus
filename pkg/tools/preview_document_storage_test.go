package tools

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/preview"
)

func officeTestPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, im))
	return b.Bytes()
}

// T-45, section 5.1. The real result builder, media store and cleaner are
// tested; this is the storage boundary, not a claim about rendering a PDF.
func assertOfficeThumbnailStorage(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	store := media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled: true, MaxAge: time.Nanosecond, Interval: time.Hour,
	})
	t.Cleanup(store.Stop)
	tool := NewPreviewDocumentTool(t.TempDir(), true)
	tool.SetMediaStore(store)
	pngBytes := officeTestPNG(t)
	res := &preview.Result{
		SourceVersion: "a1b2c3d4e5f6", PageCount: 2, First: 1, Last: 2,
		PDF:   []byte("%PDF-live-only"),
		Pages: []preview.Page{{Number: 1, Width: 2, Height: 2, PNG: pngBytes}, {Number: 2, Width: 2, Height: 2, PNG: pngBytes}},
	}
	sessionResult := tool.buildResult(testContext(t), "report.pdf", res, nil)
	require.False(t, sessionResult.IsError)
	require.Empty(t, sessionResult.Media, "display thumbnails must not become model media")
	require.Contains(t, sessionResult.ForLLM, "thumbnails stored: 2 of 2")
	require.Contains(t, sessionResult.ForLLM, "not retained; call preview_document again")
	require.Len(t, sessionResult.DocumentPreview.Thumbnails, 2)
	require.Equal(t, "complete", sessionResult.DocumentPreview.Record.Outcome)
	sessionDir, ok := media.SessionUploadsDir("session-office-preview")
	require.True(t, ok)
	sessionPaths := make([]string, 0, len(sessionResult.DocumentPreview.Thumbnails))
	for i, thumb := range sessionResult.DocumentPreview.Thumbnails {
		path, meta, err := store.ResolveWithMeta(thumb.Ref)
		require.NoError(t, err)
		require.Equal(t, sessionDir, filepath.Dir(path))
		require.Equal(t, media.CleanupPolicyForgetOnly, meta.CleanupPolicy)
		require.Equal(t, "image/png", meta.ContentType)
		require.Equal(t, i+1, thumb.PageNumber)
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.Equal(t, pngBytes, data)
		require.NotContains(t, sessionResult.ForLLM, thumb.Ref)
		require.NotContains(t, sessionResult.ForLLM, path)
		sessionPaths = append(sessionPaths, path)
	}
	require.NotContains(t, sessionResult.ForLLM, "data:image/")

	// No-session control uses the same builder and actual store. Its files
	// must expire, proving CleanExpired actually has eligible entries.
	ephemeral := tool.buildResult(context.Background(), "report.pdf", res, nil)
	require.Len(t, ephemeral.DocumentPreview.Thumbnails, 2)
	ephemeralPaths := make([]string, 0, len(ephemeral.DocumentPreview.Thumbnails))
	for _, thumb := range ephemeral.DocumentPreview.Thumbnails {
		path, meta, err := store.ResolveWithMeta(thumb.Ref)
		require.NoError(t, err)
		require.Equal(t, media.TempDir(), filepath.Dir(path))
		require.Equal(t, media.CleanupPolicyDeleteOnCleanup, meta.CleanupPolicy)
		ephemeralPaths = append(ephemeralPaths, path)
	}
	require.Equal(t, 2, store.CleanExpired(), "only the two ephemeral pages expire; the session pages stay pinned")
	for _, path := range ephemeralPaths {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	for i, thumb := range sessionResult.DocumentPreview.Thumbnails {
		path, err := store.Resolve(thumb.Ref)
		require.NoError(t, err)
		require.Equal(t, sessionPaths[i], path)
	}
	// Wrong-scope control catches accidental global release behavior.
	require.NoError(t, store.ReleaseAll(media.SessionInlineScopePrefix+"other-session"))
	for _, thumb := range sessionResult.DocumentPreview.Thumbnails {
		_, err := store.Resolve(thumb.Ref)
		require.NoError(t, err)
	}
	// Chat deletion is the uploads cascade AND ReleaseAll, per section 5.1.
	require.NoError(t, os.RemoveAll(sessionDir))
	require.NoError(t, store.ReleaseAll(media.SessionInlineScopePrefix+"session-office-preview"))
	for _, thumb := range sessionResult.DocumentPreview.Thumbnails {
		_, err := store.Resolve(thumb.Ref)
		require.Error(t, err)
	}
	for _, path := range sessionPaths {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

// AD-5: punctuation is independently specified as an en dash; argument
// strings still use ASCII hyphens. Range arithmetic is tested in preview.
func TestPreviewDocument_ClampTextUsesSpecRangeAndNextBatch(t *testing.T) {
	tool := NewPreviewDocumentTool(t.TempDir(), true)
	cases := []struct {
		name       string
		result     preview.Result
		line, next string
	}{
		{"partial", preview.Result{PageCount: 30, First: 25, Last: 30, Clamped: true, RequestedFirst: 25, RequestedLast: 40}, "pages 25–30 of 30 (requested 25–40)", ""},
		{"default", preview.Result{PageCount: 30, First: 1, Last: 10}, "pages 1–10 of 30", "next batch: call preview_document with pages \"11-20\""},
		{"middle", preview.Result{PageCount: 30, First: 11, Last: 20}, "pages 11–20 of 30", "next batch: call preview_document with pages \"21-30\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.buildResult(context.Background(), "thirty.pdf", &tc.result, nil)
			lines := strings.Split(got.ForLLM, "\n")
			require.GreaterOrEqual(t, len(lines), 2)
			require.Equal(t, tc.line, lines[1])
			if tc.next == "" {
				require.NotContains(t, got.ForLLM, "next batch:")
			} else {
				require.Contains(t, got.ForLLM, tc.next)
			}
		})
	}
}
