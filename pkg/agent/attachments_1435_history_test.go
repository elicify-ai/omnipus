package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1435 chief evidence A/B: a normal full-page screenshot is 2560x15424,
// requiring (2560/32)*(15424/32) = 38,560 patches, above ChatGPT's 30,000.
// Generate harmless grayscale fixtures; never read the live screenshot.
const (
	issue1435ScreenshotWidth  = 2560
	issue1435ScreenshotHeight = 15424
	issue1435MaxPatches       = 30_000
)

func issue1435FullPageImage(t *testing.T, mime string) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, issue1435ScreenshotWidth, issue1435ScreenshotHeight))
	var buf bytes.Buffer
	if mime == "image/jpeg" {
		require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}))
	} else {
		require.Equal(t, "image/png", mime, "fixture accepts exactly the two incident MIME classes")
		require.NoError(t, png.Encode(&buf, img))
	}
	return buf.Bytes()
}

func issue1435DecodeOutboundImage(t *testing.T, dataURL string) (width, height int, size int64) {
	t.Helper()
	require.True(t, strings.HasPrefix(dataURL, "data:image/"), "provider-facing value must be an image, not an opaque ref")
	_, payload, ok := strings.Cut(dataURL, ";base64,")
	require.True(t, ok)
	raw, err := base64.StdEncoding.DecodeString(payload)
	require.NoError(t, err)
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	require.NoError(t, err, "decode the entire image, not just its header")
	width, height = decoded.Bounds().Dx(), decoded.Bounds().Dy()
	require.Positive(t, width)
	require.Positive(t, height)
	return width, height, int64(len(raw))
}

func issue1435PatchCount(width, height int) int {
	const edge = 32 // Independent OpenAI patch formula, corroborated by the incident's exact count.
	return ((width + edge - 1) / edge) * ((height + edge - 1) / edge)
}

func TestIssue1435_ToolResultImageFitsBeforeHistoryAdmission(t *testing.T) {
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err)
	handle := cat.Resolve("openai-chatgpt", "gpt-6.1-sol")
	require.True(t, handle.Found())
	require.True(t, handle.Supports(catalog.ModalityImage))
	budget := handle.Budget()
	require.Equal(t, 38_560, issue1435PatchCount(issue1435ScreenshotWidth, issue1435ScreenshotHeight))
	for _, mime := range []string{"image/jpeg", "image/png"} {
		t.Run(mime, func(t *testing.T) {
			t.Setenv("OMNIPUS_HOME", t.TempDir())
			store := media.NewFileMediaStore()
			t.Cleanup(store.Stop)
			path := filepath.Join(t.TempDir(), "full-page-image")
			data := issue1435FullPageImage(t, mime)
			require.NoError(t, os.WriteFile(path, data, 0o600))
			ref, err := store.Store(path, media.MediaMeta{
				Filename: "full-page-image", ContentType: mime, Source: "test:screenshot",
				CleanupPolicy: media.CleanupPolicyForgetOnly,
			}, media.SessionInlineScopePrefix+"issue-1435")
			require.NoError(t, err)
			msg := providers.Message{}
			// This is the real production seam immediately before admitToolResult.
			attachToolResultMedia(&msg, []string{ref}, store, int(budget.MaxBytes))
			require.Len(t, msg.Media, 1, "#1435 A: valid full-page tool images must shrink, not disappear")
			width, height, size := issue1435DecodeOutboundImage(t, msg.Media[0])
			require.LessOrEqual(t, issue1435PatchCount(width, height), issue1435MaxPatches,
				"#1435 A: tool-result image must fit BEFORE entering history; %s %dx%d requires %d patches, limit %d",
				mime, width, height, issue1435PatchCount(width, height), issue1435MaxPatches)
			require.LessOrEqual(t, max(width, height), budget.LongEdgePx)
			require.LessOrEqual(t, size, budget.MaxBytes)
		})
	}
}

// Seed an OLD oversized tool-result image directly in the real archive. A fix
// only at tool-image admission must not hide this separate old-history case.
// Each send rebuilds from that archive, with one real retry guard per turn.
func TestIssue1435_OldScreenshotFitsEveryIterationAndReopenedTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	archiveDir := filepath.Join(home, "sessions")
	archive, err := session.NewUnifiedStore(archiveDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, archive.Close()) })
	meta, err := archive.NewSession(session.SessionTypeChat, "web", "jarvis")
	require.NoError(t, err)
	const callID = "full-page-screenshot"
	require.NoError(t, archive.AddFullMessage(meta.ID, providers.Message{
		Role: "assistant", ToolCalls: []providers.ToolCall{{ID: callID, Type: "function",
			Function: &providers.FunctionCall{Name: "browser_screenshot", Arguments: `{}`}}},
	}))
	oldURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(issue1435FullPageImage(t, "image/jpeg"))
	require.NoError(t, archive.AddFullMessage(meta.ID, providers.Message{
		Role: "tool", ToolCallID: callID, Content: "Full-page screenshot.", Media: []string{oldURL},
	}))
	initial := archive.GetHistory(meta.ID)
	require.Len(t, initial, 2)
	require.Equal(t, []string{oldURL}, initial[1].Media, "positive control: the actual archive contains the old image")
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err)
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	for turn := 1; turn <= 2; turn++ {
		if turn == 2 {
			require.NoError(t, archive.Close())
			archive, err = session.NewUnifiedStore(archiveDir)
			require.NoError(t, err, "the next turn reads a fresh archive instance, not an in-memory edited call copy")
		}
		ts := &turnState{agentID: "jarvis", turnID: fmt.Sprintf("issue-1435-turn-%d", turn)}
		for iteration := 1; iteration <= 2; iteration++ {
			t.Run(fmt.Sprintf("turn_%d_iteration_%d", turn, iteration), func(t *testing.T) {
				history := archive.GetHistory(meta.ID)
				require.Len(t, history, 2, "history must not be erased to avoid the provider failure")
				outbound := resolveMediaRefsWithOffload(history, store, 10<<20,
					"openai-chatgpt", "gpt-6.1-sol", nil, cat, nil, "")
				require.Len(t, outbound, 2)
				require.Len(t, outbound[1].Media, 1, "#1435 B: every iteration/turn must retain a readable image")
				width, height, _ := issue1435DecodeOutboundImage(t, outbound[1].Media[0])
				patches := issue1435PatchCount(width, height)
				// Model the provider's documented rejection at the process edge.
				// All archive, preparation and downgrade logic stays real. Driving
				// feedback before the assertion lets later subcases exercise the
				// consumed once-per-turn guard instead of stopping after iteration 1.
				if patches > issue1435MaxPatches {
					usedBefore := ts.imageRetryDone.Load()
					result := TryMediaDowngrade(ts, outbound, &ProviderError{
						Status: 400, Body: issue1435PatchRejection, Provider: "openai-chatgpt", Model: "gpt-6.1-sol",
					})
					t.Logf("turn=%d iteration=%d image_retry_used_before=%t downgrade=%+v", turn, iteration, usedBefore, result)
				}
				assert.LessOrEqual(t, patches, issue1435MaxPatches,
					"#1435 B: old screenshot returned at turn %d iteration %d: %dx%d requires %d patches; one-shot strip must not be the only protection",
					turn, iteration, width, height, patches)
			})
		}
	}
}

// Chief evidence C: the actual transcript writer reapplies the earlier
// successful-strip stamp. A known later image-size error must survive it on
// replay, not just look correct at an isolated classifier call.
func TestIssue1435_PatchFailureReplayKeepsSizeReasonAfterPriorStrip(t *testing.T) {
	for _, writer := range []string{"provider_error", "already_classified_error"} {
		t.Run(writer, func(t *testing.T) {
			archive, err := session.NewUnifiedStore(t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, archive.Close()) })
			meta, err := archive.NewSession(session.SessionTypeChat, "web", "jarvis")
			require.NoError(t, err)
			ts := &turnState{transcriptStore: archive, transcriptSessionID: meta.ID, agentID: "jarvis"}
			// This is legitimate existing turn state after an earlier fallback.
			// Do not fake the verdict for the NEW, specifically known size error.
			ts.setOutcomeRelabel(CodeMediaUnsupported)
			pe := &ProviderError{Status: 400, Body: issue1435PatchRejection, Provider: "openai-chatgpt", Model: "gpt-6.1-sol"}
			if writer == "provider_error" {
				ts.appendErrorTranscript(EventKindError.String(), "llm", pe.Error(), pe)
			} else {
				ts.appendClassifiedError(EventKindError.String(), "provider", TranslateLLMError(pe, ""))
			}
			entries, err := archive.ReadTranscript(meta.ID)
			require.NoError(t, err)
			require.Len(t, entries, 1, "the error must actually reach the persisted conversation")
			require.Equal(t, "error", entries[0].Status)
			require.NotEqual(t, string(CodeMediaUnsupported), entries[0].ErrorCode,
				"#1435 C: an earlier strip-retry must not relabel a later patch-limit failure as unsupported format; persisted copy: %q", entries[0].Content)
			require.Regexp(t, `(?i)(image|attachment)`, entries[0].Content)
			require.Regexp(t, `(?i)(too large|exceed|limit|patch)`, entries[0].Content)
			require.NotRegexp(t, `(?i)(use that kind of attachment|different file format)`, entries[0].Content)
		})
	}
}
