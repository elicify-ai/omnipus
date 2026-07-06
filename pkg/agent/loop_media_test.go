// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestResolveMediaRefs_UnknownRef_Drop verifies that an unresolvable
// media:// ref:
//   - produces an "[attachment unavailable: ...]" marker in the message content
//   - does NOT panic or silently discard the message
//
// Traces to: #254 silent media drops (MAJOR)
func TestResolveMediaRefs_UnknownRef_Drop(t *testing.T) {
	store := media.NewFileMediaStore()

	messages := []providers.Message{
		{Role: "user", Content: "see attached", Media: []string{"media://does-not-exist"}},
	}

	result := resolveMediaRefs(messages, store, 10*1024*1024, "")

	require.Len(t, result, 1)
	// The unknown ref must produce a visible marker in content.
	assert.Contains(t, result[0].Content, "[attachment unavailable:", "LLM must see the unavailable marker")
	// The Media array must be empty (ref could not be resolved).
	assert.Empty(t, result[0].Media, "unresolved ref must not appear in Media array")
}

// TestResolveMediaRefs_MissingFile_Drop verifies that a ref that IS
// registered but whose file has been deleted on disk also drops gracefully.
//
// Traces to: #254 silent media drops (MAJOR)
func TestResolveMediaRefs_MissingFile_Drop(t *testing.T) {
	store := media.NewFileMediaStore()

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

	result := resolveMediaRefs(messages, store, 10*1024*1024, "")

	require.Len(t, result, 1)
	assert.Contains(t, result[0].Content, "[attachment unavailable:", "missing file must produce unavailable marker")
	assert.Empty(t, result[0].Media)
}

// TestResolveMediaRefs_NonMediaPrefix_PassThrough verifies that non-media:// strings
// in the Media array are passed through unchanged (pre-resolved data URLs, external URLs).
//
// Traces to: #254 resolveMediaRefs pass-through (MAJOR)
func TestResolveMediaRefs_NonMediaPrefix_PassThrough(t *testing.T) {
	store := media.NewFileMediaStore()

	messages := []providers.Message{
		{
			Role:    "user",
			Content: "file attached",
			Media:   []string{"http://example.com/image.png"},
		},
	}

	result := resolveMediaRefs(messages, store, 10*1024*1024, "")

	require.Len(t, result, 1)
	// Non-media:// HTTP URLs are passed through unchanged.
	require.Len(t, result[0].Media, 1)
	assert.Equal(t, "http://example.com/image.png", result[0].Media[0])
}

// TestResolveMediaRefs_ValidRef_Resolved verifies a happy-path: a registered
// image ref is base64-encoded into the Media array.
//
// Traces to: #254
func TestResolveMediaRefs_ValidRef_Resolved(t *testing.T) {
	store := media.NewFileMediaStore()

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

	result := resolveMediaRefs(messages, store, 10*1024*1024, "")

	require.Len(t, result, 1)
	// The data URL must be in the Media array.
	require.Len(t, result[0].Media, 1, "resolved image must appear in Media")
	assert.True(t, len(result[0].Media[0]) > 20, "data URL must be non-trivially long")
}

// TestResolveMediaRefs_OversizeImage_UnavailableMarker verifies that
// when an image ref is registered and its file exists on disk but exceeds the
// maxSize limit, encodeImageToDataURL returns "" and the caller must:
//   - produce an "[attachment unavailable: ... (too large or unreadable)]" marker
//   - NOT add anything to the Media data-URL array
//
// Traces to: #258 Fix 3 — oversize image silently dropped (MAJOR)
func TestResolveMediaRefs_OversizeImage_UnavailableMarker(t *testing.T) {
	store := media.NewFileMediaStore()

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
	result := resolveMediaRefs(messages, store, 0, "")

	require.Len(t, result, 1)
	// Media array must be empty — the oversize image must not appear as a data URL.
	assert.Empty(t, result[0].Media, "oversize image must not appear in Media data-URL array")
	// An unavailability marker must appear in Content.
	assert.Contains(t, result[0].Content, "[attachment unavailable:",
		"oversize image must produce [attachment unavailable] marker in Content")
	assert.Contains(t, result[0].Content, "too large or unreadable",
		"unavailability marker must describe the reason")
}

// TestResolveMediaRefs_NilStore_PassThrough verifies that with a nil store
// the messages are returned unmodified (no panic, no modification).
func TestResolveMediaRefs_NilStore_PassThrough(t *testing.T) {
	messages := []providers.Message{
		{Role: "user", Content: "text only"},
		{Role: "user", Content: "has ref", Media: []string{"media://abc"}},
	}

	result := resolveMediaRefs(messages, nil, 10*1024*1024, "")

	require.Len(t, result, 2)
	// Messages must be returned unchanged when store is nil.
	assert.Equal(t, messages[0].Content, result[0].Content)
	assert.Equal(t, messages[1].Media, result[1].Media)
}

// TestPDFCapableModel_Matrix locks the conservative PDF-capability allow-list.
// The asymmetry matters: a false positive sends a native PDF block to a model
// whose provider rejects it (hard HTTP 400, empty turn), so the list must err
// toward text extraction. Regression: claude-3.5-haiku was wrongly classified
// as capable (matched the old bare "claude-3" substring) and OpenRouter→Bedrock
// returned "'claude-3-5-haiku-...' does not support PDF input." (verified live
// 2026-06-01).
func TestPDFCapableModel_Matrix(t *testing.T) {
	capable := []string{
		"google/gemini-2.0-flash-001", // verified live: native PDF works
		"google/gemini-1.5-pro",
		"anthropic/claude-3.5-sonnet",
		"anthropic/claude-sonnet-4",
		"anthropic/claude-opus-4",
		"openai/gpt-4o",
		"openai/gpt-4.1",
		"openai/o3-mini",
	}
	notCapable := []string{
		"anthropic/claude-3.5-haiku", // verified live: provider 400s on PDF
		"anthropic/claude-3-5-haiku-20241022",
		"anthropic/claude-3-haiku",
		"z-ai/glm-5v-turbo", // verified live: gets text-extraction fallback
		"meta-llama/llama-3.3-70b-instruct",
		"mistralai/mistral-large",
	}
	for _, m := range capable {
		assert.Truef(t, pdfCapableModel(m), "expected %q to be PDF-capable", m)
	}
	for _, m := range notCapable {
		assert.Falsef(t, pdfCapableModel(m), "expected %q to NOT be PDF-capable (must fall back to text)", m)
	}
}

// makeMiniPDF builds a minimal valid single-page PDF with extractable text and
// correct xref offsets, mirroring the document generator used in the live test.
func makeMiniPDF(text string) []byte {
	esc := func(s string) string {
		out := make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			if s[i] == '(' || s[i] == ')' || s[i] == '\\' {
				out = append(out, '\\')
			}
			out = append(out, s[i])
		}
		return string(out)
	}
	stream := "BT /F1 14 Tf 54 720 Td (" + esc(text) + ") Tj ET"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b []byte
	b = append(b, "%PDF-1.4\n"...)
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = len(b)
		b = append(b, fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, body)...)
	}
	xref := len(b)
	b = append(b, fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)...)
	for i := 1; i <= len(objs); i++ {
		b = append(b, fmt.Sprintf("%010d 00000 n \n", offsets[i])...)
	}
	b = append(b, fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", len(objs)+1, xref)...)
	return b
}

func pdfDataURL(text string) string {
	return "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(makeMiniPDF(text))
}

// TestDowngradePDFMediaToText_HappyPath verifies a native PDF block is decoded
// back to extracted text in Content and removed from Media — the recovery the
// agent loop performs when a provider rejects native PDF input.
func TestDowngradePDFMediaToText_HappyPath(t *testing.T) {
	msgs := []providers.Message{{
		Role:    "user",
		Content: "Please read this.",
		Media:   []string{pdfDataURL("SENTINEL_FALLBACK_TOKEN_9931 is the answer.")},
	}}

	changed := downgradePDFMediaToText(msgs)

	require.True(t, changed, "should report a downgrade")
	assert.Empty(t, msgs[0].Media, "native PDF block removed from Media")
	assert.Contains(t, msgs[0].Content, "SENTINEL_FALLBACK_TOKEN_9931", "extracted text injected into Content")
	assert.Contains(t, msgs[0].Content, "[Attached file", "uses the document injection wrapper")
	assert.Contains(t, msgs[0].Content, "Please read this.", "original content preserved")
}

// TestDowngradePDFMediaToText_PreservesNonPDF verifies images (and other data
// URLs) are kept while only the PDF block is downgraded.
func TestDowngradePDFMediaToText_PreservesNonPDF(t *testing.T) {
	img := "data:image/png;base64,iVBORw0KGgo="
	msgs := []providers.Message{{
		Role:  "user",
		Media: []string{img, pdfDataURL("KEEP_THE_IMAGE_TOKEN_5577")},
	}}

	changed := downgradePDFMediaToText(msgs)

	require.True(t, changed)
	require.Len(t, msgs[0].Media, 1, "image survives, only the PDF is removed")
	assert.Equal(t, img, msgs[0].Media[0])
	assert.Contains(t, msgs[0].Content, "KEEP_THE_IMAGE_TOKEN_5577")
}

// TestDowngradePDFMediaToText_NoPDF verifies messages without a native PDF block
// are left untouched and the helper reports no change (so the loop never burns
// an extra retry when there is nothing to downgrade).
func TestDowngradePDFMediaToText_NoPDF(t *testing.T) {
	img := "data:image/png;base64,iVBORw0KGgo="
	msgs := []providers.Message{{Role: "user", Content: "hi", Media: []string{img}}}

	changed := downgradePDFMediaToText(msgs)

	assert.False(t, changed, "no PDF block -> no change")
	require.Len(t, msgs[0].Media, 1)
	assert.Equal(t, img, msgs[0].Media[0])
	assert.Equal(t, "hi", msgs[0].Content)
}

// TestIsImageInputRejection covers the detector that converts a provider's
// "model can't view images" 400 into a friendly switch-models message.
// The match must be narrow: real image errors (corrupt data, oversized files,
// content-moderation blocks) must NOT be mistaken for capability-absence errors.
func TestIsImageInputRejection(t *testing.T) {
	// These errors indicate the model/provider lacks vision capability.
	hits := []string{
		"API request failed: Status: 400 ... 'claude-3-5-haiku-20241022' does not support image input.",
		"this model does not support image input",
		"does not support image processing",
		"image input is not supported by this model",
		"no image support available for this model",
		"model does not support image: use a vision-capable model",
		"image not supported",
		"image input not supported",
	}

	// These errors must NOT be treated as capability-absence errors.
	// Returning true for any of these would mask the true failure cause.
	misses := []string{
		// nil and unrelated errors
		"",
		"rate limit exceeded",
		"context length exceeded",
		"'claude-3-5-haiku' does not support PDF input.", // PDF, not image
		"invalid api key",
		// Corrupt / malformed image data — real provider errors, not capability gaps
		"invalid image data provided",
		"invalid image: the file is not a valid JPEG",
		"image data is corrupt or unreadable",
		// Oversized image — infrastructure limit, not a vision-capability error
		"image file too large: maximum size is 5MB",
		"image is too large to process",
		// Content moderation / safety — must surface to the user as-is
		"content moderation: image rejected",
		"image blocked by safety policy",
		"content policy violation in image",
		// Generic processing failure — ambiguous root cause, do not mask
		"unable to process image at this time",
	}

	for _, m := range hits {
		assert.Truef(t, isImageInputRejection(fmt.Errorf("%s", m)), "expected image-rejection for %q", m)
	}
	for _, m := range misses {
		assert.Falsef(t, isImageInputRejection(fmt.Errorf("%s", m)), "did NOT expect image-rejection for %q", m)
	}
	assert.False(t, isImageInputRejection(nil))
}

// imageRejectionProvider is a test provider that always returns a
// vision-capability-absent error so the loop's image-rejection handler fires.
type imageRejectionProvider struct {
	model string
}

func (p *imageRejectionProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return nil, fmt.Errorf("API error: '%s' does not support image input.", p.model)
}

func (p *imageRejectionProvider) GetDefaultModel() string {
	return p.model
}

// TestAgentLoop_ImageRejection_FriendlyMessage verifies that when the provider
// returns a vision-capability-absent error the loop:
//
//	(a) returns a friendly assistant message — not a raw error,
//	(b) the message mentions the model name and the inability to view images,
//	(c) the turn ends with TurnEndStatusCompleted (not error),
//	(d) no EventKindError event is emitted.
//
// Traces to: sprint/258 review finding #2 (loop.go image→friendly-message has no test).
func TestAgentLoop_ImageRejection_FriendlyMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-imgrej-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	const modelName = "anthropic/claude-3-5-haiku"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         modelName,
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &imageRejectionProvider{model: modelName}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	defaultAgent := al.registry.GetDefaultAgent()
	require.NotNil(t, defaultAgent, "default agent must exist")

	response, loopErr := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		SessionKey:      "img-rej-test",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "What is in this image?",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})

	// (a) The loop must succeed — image rejection is handled, not propagated.
	require.NoError(t, loopErr, "image rejection must not cause a loop error")

	// (b) The response must describe the inability to view images and name the model
	// (or a recognizable substring of it — the loop uses the resolved model name,
	// which may omit the provider prefix, e.g. "claude-3-5-haiku" from
	// "anthropic/claude-3-5-haiku").
	assert.True(t, strings.Contains(response, "can't view images") ||
		strings.Contains(response, "cannot view images") ||
		strings.Contains(response, "I can't view"),
		"response must mention inability to view images, got: %q", response)
	// Extract the trailing part after the last "/" so "anthropic/claude-3-5-haiku"
	// becomes "claude-3-5-haiku" — the loop uses the resolved (abbreviated) name.
	shortModel := modelName
	if idx := strings.LastIndex(modelName, "/"); idx >= 0 {
		shortModel = modelName[idx+1:]
	}
	assert.Contains(t, response, shortModel,
		"response must name the offending model so the user knows which one to switch, got: %q", response)

	events := collectEventStream(sub.C)

	// (c) Turn must end as completed, not error.
	turnEndEvt, hasTurnEnd := findEvent(events, EventKindTurnEnd)
	require.True(t, hasTurnEnd, "TurnEnd event must be emitted")
	turnEndPayload, ok := turnEndEvt.Payload.(TurnEndPayload)
	require.True(t, ok, "TurnEnd payload must be TurnEndPayload, got %T", turnEndEvt.Payload)
	assert.Equal(t, TurnEndStatusCompleted, turnEndPayload.Status,
		"turn must end as completed, not error")

	// (d) No error event must be emitted.
	_, hasErrorEvt := findEvent(events, EventKindError)
	assert.False(t, hasErrorEvt, "no EventKindError must be emitted when image rejection is handled gracefully")
}

// TestAgentLoop_NonImageError_PropagatesAsError verifies the negative case:
// a non-image error (e.g. "rate limit exceeded") still causes an error turn and
// is NOT swallowed by the image-rejection handler.
//
// Traces to: sprint/258 review finding #2 (negative test case).
func TestAgentLoop_NonImageError_PropagatesAsError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-nonimg-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	// Provider always returns a rate-limit error — must NOT be handled as an image rejection.
	rateLimitErr := fmt.Errorf("rate limit exceeded: too many requests per minute")
	provider := &failFirstMockProvider{
		failures:  999, // never succeeds
		failError: rateLimitErr,
	}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	defaultAgent := al.registry.GetDefaultAgent()
	require.NotNil(t, defaultAgent, "default agent must exist")

	_, loopErr := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		SessionKey:      "nonimg-err-test",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "Hello",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})

	// The loop must surface the error — it is not an image-capability error.
	require.Error(t, loopErr, "non-image error must propagate as a loop error")

	events := collectEventStream(sub.C)

	// An EventKindError must have been emitted.
	_, hasErrorEvt := findEvent(events, EventKindError)
	assert.True(t, hasErrorEvt, "EventKindError must be emitted for a non-image LLM error")

	// The turn must end with error status.
	turnEndEvt, hasTurnEnd := findEvent(events, EventKindTurnEnd)
	require.True(t, hasTurnEnd, "TurnEnd event must be emitted even on error")
	turnEndPayload, ok := turnEndEvt.Payload.(TurnEndPayload)
	require.True(t, ok, "TurnEnd payload must be TurnEndPayload, got %T", turnEndEvt.Payload)
	assert.Equal(t, TurnEndStatusError, turnEndPayload.Status,
		"turn must end as error for a non-image LLM failure")
}
