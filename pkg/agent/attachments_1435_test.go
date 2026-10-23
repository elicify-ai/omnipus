package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Verbatim provider evidence in #1435. Expected item 3 says a known size
// failure must retain that cause, never become an unsupported-format error.
const issue1435PatchRejection = "The image you provided requires 38560 patches after processing, exceeding the limit of 30000. Please resize the image and try again."

func issue1435PNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height))))
	return buf.Bytes()
}

func issue1435DataURL(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

// #1435 Expected item 2: inspect the real provider-facing presentation output,
// not just the standalone resizer. Include both accepted input forms so a
// pre-resolved data URL cannot bypass provider/model budget enforcement.
func TestIssue1435_ProviderFacingImageFitsPatchLimit(t *testing.T) {
	const (
		width      = 5120
		height     = 7712
		patchEdge  = 32
		maxPatches = 30_000
		maxBytes   = 10 << 20 // Fixture/operator cap, not a new provider assumption.
	)
	// Exactly the incident's 38,560 patches: (5120/32)*(7712/32) = 160*241.
	require.Equal(t, 38_560, ((width+patchEdge-1)/patchEdge)*((height+patchEdge-1)/patchEdge))
	pngBytes := issue1435PNG(t, width, height)
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err)
	handle := cat.Resolve("openai-chatgpt", "gpt-6.1-sol")
	require.True(t, handle.Found(), "exercise the actual catalog row, not an optimistic miss")
	require.True(t, handle.Supports(catalog.ModalityImage))

	for _, transport := range []string{"registered_ref", "pre_resolved_data_url"} {
		t.Run(transport, func(t *testing.T) {
			t.Setenv("OMNIPUS_HOME", t.TempDir())
			store := media.NewFileMediaStore()
			t.Cleanup(store.Stop)
			input := issue1435DataURL(pngBytes)
			if transport == "registered_ref" {
				path := filepath.Join(t.TempDir(), "accepted.png")
				require.NoError(t, os.WriteFile(path, pngBytes, 0o600))
				input, err = store.Store(path, media.MediaMeta{
					Filename: "accepted.png", ContentType: "image/png", Source: "upload:webchat",
					CleanupPolicy: media.CleanupPolicyForgetOnly,
				}, "upload:issue-1435")
				require.NoError(t, err)
			}
			messages := []providers.Message{{Role: "user", Content: "Read this image.", Media: []string{input}}}
			outbound := resolveMediaRefsWithOffload(messages, store, maxBytes,
				"openai-chatgpt", "gpt-6.1-sol", nil, cat, nil, "")
			require.Len(t, outbound, 1)
			require.Len(t, outbound[0].Media, 1,
				"#1435: accepted oversized image must be resized and sent, not silently replaced by %q", outbound[0].Content)
			dataURL := outbound[0].Media[0]
			_, encoded, ok := bytes.Cut([]byte(dataURL), []byte(";base64,"))
			require.True(t, ok, "provider-facing image must contain encoded bytes, not an unresolved media ref")
			data, err := base64.StdEncoding.DecodeString(string(encoded))
			require.NoError(t, err)
			decoded, _, err := image.Decode(bytes.NewReader(data))
			require.NoError(t, err, "the actual outbound image must fully decode, not just have a valid header")
			outWidth, outHeight := decoded.Bounds().Dx(), decoded.Bounds().Dy()
			require.Positive(t, outWidth)
			require.Positive(t, outHeight)
			patches := ((outWidth + patchEdge - 1) / patchEdge) * ((outHeight + patchEdge - 1) / patchEdge)
			require.LessOrEqual(t, patches, maxPatches,
				"#1435: before the provider call, outbound image %dx%d requires %d patches, limit %d",
				outWidth, outHeight, patches, maxPatches)
			require.LessOrEqual(t, int64(len(data)), handle.Budget().MaxBytes)
			require.LessOrEqual(t, max(outWidth, outHeight), handle.Budget().LongEdgePx)
			require.Equal(t, []string{input}, messages[0].Media,
				"provider preparation must not replace the conversation's accepted attachment ref")
		})
	}
}

// #1435 Expected item 3 gives the meaning and remedy, not a new code name or
// exact replacement sentence. Match that meaning without inventing either.
func TestIssue1435_UnavailableAttachmentExplainsReattach(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	_, missingErr := store.Resolve("media://4b55b1db-d624-4ca4-a88c-685f53104b32")
	require.ErrorIs(t, missingErr, media.ErrNotFound, "fixture must originate in the real resolver")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"resolver_error", missingErr},
		{"wrapped_resolver_error", fmt.Errorf("resolve conversation attachment: %w", missingErr)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TranslateTurnError(tc.err)
			require.NotEqual(t, CodeMediaUnsupported, got.Code, "a missing attachment is not an unsupported format")
			require.Regexp(t, `(?i)attachment.*(no longer available|unavailable|missing)`, got.Message,
				"#1435: ordinary user-visible copy must explain that the attachment is no longer available; got code=%s", got.Code)
			require.Regexp(t, `(?i)re[- ]?attach`, got.Message,
				"#1435: the remedy is to re-attach, not to switch models or inspect Verbose chat")
		})
	}
}

func TestIssue1435_ImageSizeFailureExplainsActualReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"chatgpt_patch_limit_400", 400, issue1435PatchRejection},
		{"json_patch_limit_400", 400, fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error"}}`, issue1435PatchRejection)},
		{"patch_limit_without_http_status", 0, issue1435PatchRejection},
		{"image_too_large_413", 413, "The image is too large. Please resize the image and try again."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Use the real provider-side error carrier for the terminal chain,
			// not the agent-side presentation value as a synthetic transport.
			providerErr := &common.ProviderError{Status: tc.status, Body: tc.body}
			failedAttempt := &providers.FailoverError{
				Status: tc.status, Provider: "openai-chatgpt", Model: "gpt-6.1-sol", Wrapped: providerErr,
			}
			pe := ProviderErrorFromFailover(failedAttempt)
			// Both live/provider translation and terminal turn translation must
			// retain the cause; neither may rely on Verbose-only Detail.
			for _, translated := range []struct {
				name string
				got  LLMError
			}{
				{"provider_error", TranslateLLMError(pe, "")},
				{"terminal_wrapped_error", TranslateTurnError(fmt.Errorf("image request failed: %w", failedAttempt))},
			} {
				t.Run(translated.name, func(t *testing.T) {
					got := translated.got
					require.NotEqual(t, CodeMediaUnsupported, got.Code, "size is not unsupported format")
					require.Regexp(t, `(?i)(image|attachment)`, got.Message,
						"#1435: user-facing copy must name the failed image, not a generic rejected request; code=%s", got.Code)
					require.Regexp(t, `(?i)(too large|exceed|limit|patch)`, got.Message,
						"#1435: the public message must retain the provider's image-size reason")
					require.NotRegexp(t, `(?i)(different file format|switch to a model that supports it|use that kind of attachment)`, got.Message,
						"a size failure must not use the generic unsupported-format advice")
				})
			}
		})
	}
}

// The current outcome fallback may mistake success after stripping an image
// for proof the format was unsupported. A known size error must never enter
// that generic format-downgrade path (#1435 Expected item 3).
func TestIssue1435_PatchFailureDoesNotStripImageAsUnsupported(t *testing.T) {
	imageURL := issue1435DataURL(issue1435PNG(t, 32, 32))
	messages := []providers.Message{{Role: "user", Content: "Read this image.", Media: []string{imageURL}}}
	want := []providers.Message{{Role: "user", Content: "Read this image.", Media: []string{imageURL}}}
	pe := &ProviderError{
		Status: 400, Body: issue1435PatchRejection, Provider: "openai-chatgpt", Model: "gpt-6.1-sol",
	}
	result := TryMediaDowngrade(&turnState{agentID: "jarvis", turnID: "issue-1435"}, messages, pe)
	require.False(t, result.Applied,
		"#1435: known patch overflow must retain its size cause, not trigger generic format strip-retry; got %+v", result)
	require.Equal(t, want, messages, "a size failure must not erase the attachment and inject unsupported-format advice")
}
