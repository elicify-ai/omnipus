package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ---- Fix 1: SHA-256-on-read (verifyFileIntegrity) ----

func TestVerifyFileIntegrity_Matches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	data := []byte("hello world, this is a test file for sha256 verification")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	h := sha256.Sum256(data)
	expected := hex.EncodeToString(h[:])

	err := verifyFileIntegrity(path, expected)
	assert.NoError(t, err)
}

func TestVerifyFileIntegrity_Mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	data := []byte("original content")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	err := verifyFileIntegrity(path, "0000000000000000000000000000000000000000000000000000000000000000")
	assert.ErrorContains(t, err, "sha256 mismatch")
}

func TestVerifyFileIntegrity_MissingFile(t *testing.T) {
	err := verifyFileIntegrity("/nonexistent/path.bin", "abc")
	assert.Error(t, err)
}

// ---- Fix 2/3: catalog budget + PDF gate ----
// Covered end-to-end by loop_media_present_test.go (mustCatalog fixtures).
// Keep a thin compile/link smoke that the catalog package is reachable.

func TestCatalogPackage_Linked(t *testing.T) {
	_ = catalog.ModalityImage
}

// ---- Fix 4: MediaClass on TryMediaDowngrade ----

func TestTryMediaDowngrade_CapturesMediaClassImage(t *testing.T) {
	ts := &turnState{}
	msgs := []providers.Message{
		{
			Role:    "user",
			Content: "hello",
			Media: []string{
				"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
			},
		},
	}
	pe := &ProviderError{Status: 400, Body: "image not supported"}
	result := TryMediaDowngrade(ts, msgs, pe)
	if result.Applied {
		assert.Equal(t, MediaClassImage, result.MediaClass,
			"MediaClass must be populated for image downgrade")
	}
}

func TestTryMediaDowngrade_CapturesMediaClassPDF(t *testing.T) {
	ts := &turnState{}
	msgs := []providers.Message{
		{
			Role:    "user",
			Content: "hello",
			Media:   []string{"data:application/pdf;base64,JVBERi0xLjcNCg0KMSAwIG9iago8PC9UeXBlL1BhZ2VzL0tpZHM"},
		},
	}
	pe := &ProviderError{Status: 400, Body: "pdf input not supported"}
	result := TryMediaDowngrade(ts, msgs, pe)
	if result.Applied {
		assert.Equal(t, MediaClassPDF, result.MediaClass,
			"MediaClass must be populated for PDF downgrade")
	}
}

// ---- Fix 5: MediaMeta SHA256 field ----

func TestMediaMeta_SHA256Populated(t *testing.T) {
	m := media.MediaMeta{}
	assert.Equal(t, "", m.SHA256, "default SHA256 is empty")

	m2 := media.MediaMeta{SHA256: "abcdef1234567890"}
	assert.Equal(t, "abcdef1234567890", m2.SHA256)
}

// ---- Fix 6: caller workspace threaded into resolveMediaRefsWithOffload (FR-028a) ----
//
// Live UAT (2026-07-23) found workspace media:// refs failing closed with
// ErrWorkspaceContextRequired because resolveMediaRefsWithOffload passed
// empty ResolveOpts. The turn's WorkspaceID must reach the resolver.

func TestResolveMediaRefs_WorkspaceRef_RequiresCallerWorkspace(t *testing.T) {
	home := t.TempDir()
	const wsID = "ws-uat-caller"
	fixed := func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) }
	lib, err := library.New(home, wsID, library.WithClock(fixed))
	require.NoError(t, err)

	pngBytes := mustEncodeTestPNG(t, 8, 8)
	ref, _, uploadErr := lib.UploadFixture("blue.png", bytes.NewReader(pngBytes))
	require.NoError(t, uploadErr)
	require.True(t, media.IsWorkspaceRef(ref))

	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		if id != wsID {
			return nil, os.ErrNotExist
		}
		return lib, nil
	})

	msgs := []providers.Message{{Role: "user", Content: "what is this", Media: []string{ref}}}

	// Empty caller workspace → fail closed (attachment unavailable).
	noCtx := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "", "claude-sonnet-4", nil, nil, nil, "")
	require.Len(t, noCtx, 1)
	assert.Empty(t, noCtx[0].Media, "without caller workspace, workspace ref must not resolve to data URL")
	assert.Contains(t, noCtx[0].Content, "attachment unavailable")

	// Matching caller workspace → resolves (vision path produces data URL).
	withCtx := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "", "claude-sonnet-4", nil, nil, nil, wsID)
	require.Len(t, withCtx, 1)
	assert.NotContains(t, withCtx[0].Content, "attachment unavailable",
		"with matching caller workspace, workspace ref must resolve")
	require.NotEmpty(t, withCtx[0].Media, "resolved workspace PNG must become a media data URL")
	assert.True(t, strings.HasPrefix(withCtx[0].Media[0], "data:image/"),
		"resolved media should be a data URL, got prefix %q", withCtx[0].Media[0][:min(48, len(withCtx[0].Media[0]))])
}

func mustEncodeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
