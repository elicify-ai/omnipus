// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// TestResolveMediaRefs_UnknownRef_DropAndCount verifies that an unresolvable
// media:// ref:
//   - produces an "[attachment unavailable: ...]" marker in the message content
//   - increments the droppedCounter
//   - does NOT panic or silently discard the message
//
// Traces to: #254 silent media drops (MAJOR)
func TestResolveMediaRefs_UnknownRef_DropAndCount(t *testing.T) {
	store := media.NewFileMediaStore()
	var counter atomic.Int64

	messages := []providers.Message{
		{Role: "user", Content: "see attached", Media: []string{"media://does-not-exist"}},
	}

	result := resolveMediaRefs(messages, store, 10*1024*1024, &counter)

	require.Len(t, result, 1)
	// The unknown ref must produce a visible marker in content.
	assert.Contains(t, result[0].Content, "[attachment unavailable:", "LLM must see the unavailable marker")
	// The Media array must be empty (ref could not be resolved).
	assert.Empty(t, result[0].Media, "unresolved ref must not appear in Media array")
	// The dropped counter must be incremented.
	assert.EqualValues(t, 1, counter.Load(), "dropped counter must increment for unknown ref")
}

// TestResolveMediaRefs_MissingFile_DropAndCount verifies that a ref that IS
// registered but whose file has been deleted on disk also drops gracefully.
//
// Traces to: #254 silent media drops (MAJOR)
func TestResolveMediaRefs_MissingFile_DropAndCount(t *testing.T) {
	store := media.NewFileMediaStore()
	var counter atomic.Int64

	// Register a file, then delete it from disk.
	tmpFile := filepath.Join(t.TempDir(), "deleted.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("content"), 0o600))
	ref, err := store.Store(tmpFile, media.MediaMeta{
		Filename:    "deleted.txt",
		ContentType: "text/plain",
	}, "test-scope")
	require.NoError(t, err)
	require.NoError(t, os.Remove(tmpFile)) // delete the file AFTER registering

	messages := []providers.Message{
		{Role: "user", Content: "file was here", Media: []string{ref}},
	}

	result := resolveMediaRefs(messages, store, 10*1024*1024, &counter)

	require.Len(t, result, 1)
	assert.Contains(t, result[0].Content, "[attachment unavailable:", "missing file must produce unavailable marker")
	assert.Empty(t, result[0].Media)
	assert.EqualValues(t, 1, counter.Load(), "dropped counter must increment for missing file")
}

// TestResolveMediaRefs_NonMediaPrefix_Dropped verifies that a non-media:// string
// in the Media array is silently dropped (not forwarded to the LLM).
//
// Traces to: #254 resolveMediaRefs pass-through (MAJOR)
func TestResolveMediaRefs_NonMediaPrefix_Dropped(t *testing.T) {
	store := media.NewFileMediaStore()
	var counter atomic.Int64

	// A local path (telegram/discord fallback pattern) must never reach the LLM.
	messages := []providers.Message{
		{
			Role:    "user",
			Content: "file attached",
			Media:   []string{"/tmp/secret/file.txt", "http://example.com/image.png"},
		},
	}

	result := resolveMediaRefs(messages, store, 10*1024*1024, &counter)

	require.Len(t, result, 1)
	// Non-media:// strings must be dropped. The LLM content should NOT contain
	// the raw path or URL.
	assert.NotContains(t, result[0].Content, "/tmp/secret/file.txt")
	assert.NotContains(t, result[0].Content, "http://example.com/image.png")
	// They also must not appear in Media.
	assert.Empty(t, result[0].Media)
	// Non-media:// strings do not bump the dropped counter (they are invalid
	// inputs from channel fallback code, not "failed to resolve" events).
	assert.EqualValues(t, 0, counter.Load(), "non-media:// strings do not count as dropped media refs")
}

// TestResolveMediaRefs_ValidRef_Resolved verifies a happy-path: a registered
// image ref is base64-encoded into the Media array.
//
// Traces to: #254
func TestResolveMediaRefs_ValidRef_Resolved(t *testing.T) {
	store := media.NewFileMediaStore()
	var counter atomic.Int64

	// Write a minimal 1x1 PNG (8-byte) to disk.
	tmpFile := filepath.Join(t.TempDir(), "img.png")
	// Minimal valid PNG bytes (1x1 transparent pixel)
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG header
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk length + type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, // bit depth + color type
		0x89, 0x00, 0x00, 0x00, 0x0B, 0x49, 0x44, 0x41, // IDAT
		0x54, 0x78, 0x9C, 0x62, 0x00, 0x00, 0x00, 0x02, // compressed data
		0x00, 0x01, 0xE2, 0x21, 0xBC, 0x33, 0x00, 0x00, // rest of IDAT
		0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, // IEND
		0x60, 0x82,
	}
	require.NoError(t, os.WriteFile(tmpFile, pngBytes, 0o600))

	ref, err := store.Store(tmpFile, media.MediaMeta{
		Filename:    "img.png",
		ContentType: "image/png",
	}, "test-scope")
	require.NoError(t, err)

	messages := []providers.Message{
		{Role: "user", Content: "look at this", Media: []string{ref}},
	}

	result := resolveMediaRefs(messages, store, 10*1024*1024, &counter)

	require.Len(t, result, 1)
	// The data URL must be in the Media array.
	require.Len(t, result[0].Media, 1, "resolved image must appear in Media")
	assert.True(t, len(result[0].Media[0]) > 20, "data URL must be non-trivially long")
	// No drop.
	assert.EqualValues(t, 0, counter.Load(), "successful resolve must not increment dropped counter")
}

// TestResolveMediaRefs_OversizeImage_UnavailableMarkerAndCount verifies that
// when an image ref is registered and its file exists on disk but exceeds the
// maxSize limit, encodeImageToDataURL returns "" and the caller must:
//   - produce an "[attachment unavailable: ... (too large or unreadable)]" marker
//   - increment the droppedCounter
//   - NOT add anything to the Media data-URL array
//
// Traces to: #258 Fix 3 — oversize image silently dropped (MAJOR)
func TestResolveMediaRefs_OversizeImage_UnavailableMarkerAndCount(t *testing.T) {
	store := media.NewFileMediaStore()
	var counter atomic.Int64

	// Write a small image-like file and register it.
	tmpFile := filepath.Join(t.TempDir(), "big.png")
	// Minimal valid PNG bytes (1x1 transparent pixel) — small on disk but we set
	// maxSize=0 so it's always considered "too large".
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00,
	}
	require.NoError(t, os.WriteFile(tmpFile, pngBytes, 0o600))

	ref, err := store.Store(tmpFile, media.MediaMeta{
		Filename:    "big.png",
		ContentType: "image/png",
	}, "test-scope")
	require.NoError(t, err)

	messages := []providers.Message{
		{Role: "user", Content: "huge image", Media: []string{ref}},
	}

	// maxSize=0 forces every image to be considered oversize.
	result := resolveMediaRefs(messages, store, 0, &counter)

	require.Len(t, result, 1)
	// Media array must be empty — the oversize image must not appear as a data URL.
	assert.Empty(t, result[0].Media, "oversize image must not appear in Media data-URL array")
	// An unavailability marker must appear in Content.
	assert.Contains(t, result[0].Content, "[attachment unavailable:",
		"oversize image must produce [attachment unavailable] marker in Content")
	assert.Contains(t, result[0].Content, "too large or unreadable",
		"unavailability marker must describe the reason")
	// The dropped counter must be incremented — same treatment as resolve/stat failures.
	assert.EqualValues(t, 1, counter.Load(), "dropped counter must increment for oversize image")
}

// TestResolveMediaRefs_NilStore_PassThrough verifies that with a nil store
// the messages are returned unmodified (no panic, no modification).
func TestResolveMediaRefs_NilStore_PassThrough(t *testing.T) {
	var counter atomic.Int64

	messages := []providers.Message{
		{Role: "user", Content: "text only"},
		{Role: "user", Content: "has ref", Media: []string{"media://abc"}},
	}

	result := resolveMediaRefs(messages, nil, 10*1024*1024, &counter)

	require.Len(t, result, 2)
	// Messages must be returned unchanged when store is nil.
	assert.Equal(t, messages[0].Content, result[0].Content)
	assert.Equal(t, messages[1].Media, result[1].Media)
	assert.EqualValues(t, 0, counter.Load())
}
