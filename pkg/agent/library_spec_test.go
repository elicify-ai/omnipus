// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// library-spec regression tests (2026-07-29 UAT).
//
// Covers D1 (a caption-less media turn must still reach the model), D1b
// (the synthesized "[user uploaded: ...]" announcement), and the
// pkg/agent-owned half of D-1 (SanitizeUploadFilename, the
// RecordUploadWorkPath/LookupUploadWorkPath registry, and its documented
// FallbackAnnouncedUploadPath formula).

package agent

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ---- D1: a caption-less media turn must still reach the model ----

// TestBuildMessages_CaptionlessMediaReachesModel is the D1 regression test.
// Before the fix, context.go's final append gated on
// strings.TrimSpace(currentMessage) != "" ALONE — a turn with media and an
// empty caption was dropped ENTIRELY (media included), so the model never
// even learned a file arrived. This test FAILS against the pre-fix gate
// (len(msgs) would be one shorter, with no trailing user/media message at
// all) and PASSES now that the gate also fires on len(media) > 0.
func TestBuildMessages_CaptionlessMediaReachesModel(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	mediaRefs := []string{"media://workspace/ws1/00000000-0000-0000-0000-000000000001"}

	msgs := cb.BuildMessages(nil, "", mediaRefs, "ws1", "webchat", "chat1", "", "", "", nil)

	require.NotEmpty(t, msgs, "BuildMessages must not return an empty message list")
	last := msgs[len(msgs)-1]
	assert.Equal(t, "user", last.Role,
		"the caption-less media turn must still be appended as the final user message")
	assert.Equal(t, mediaRefs, last.Media,
		"the media refs must survive onto the appended message even though Content is empty")
	assert.Empty(t, last.Content, "caption stays empty — D1 does not synthesize fake caption text")
}

// TestBuildMessages_CaptionAndMediaBothReach verifies the fix did not change
// behavior for the ordinary (non-empty caption) case — still exactly the
// same message, still appended.
func TestBuildMessages_CaptionAndMediaBothReach(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{"AGENT.md": "# Agent"})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	mediaRefs := []string{"media://workspace/ws1/00000000-0000-0000-0000-000000000002"}

	msgs := cb.BuildMessages(nil, "what is this?", mediaRefs, "ws1", "webchat", "chat1", "", "", "", nil)

	last := msgs[len(msgs)-1]
	assert.Equal(t, "user", last.Role)
	assert.Equal(t, "what is this?", last.Content)
	assert.Equal(t, mediaRefs, last.Media)
}

// TestBuildMessages_NoTextNoMedia_NothingAppended verifies the gate still
// does the RIGHT thing for a genuinely empty turn (no text, no media) — it
// must NOT synthesize a spurious empty user message.
func TestBuildMessages_NoTextNoMedia_NothingAppended(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{"AGENT.md": "# Agent"})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	before := cb.BuildMessages(nil, "placeholder", nil, "", "webchat", "chat1", "", "", "", nil)
	after := cb.BuildMessages(nil, "", nil, "", "webchat", "chat1", "", "", "", nil)

	assert.Equal(t, len(before)-1, len(after),
		"an empty caption with no media must still be dropped — only the media-present case changes")
}

// ---- D1b: the synthesized upload announcement ----

// mustEncodeTestPNGLibSpec is a tiny local PNG encoder (mirrors
// mustEncodeTestPNG in loop_media_integration_seams_test.go — duplicated
// rather than shared to keep this file self-contained and independent of
// that test's helper lifetime).
func mustEncodeTestPNGLibSpec(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{10, 20, 30, 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// TestResolveMediaRefs_AnnouncesWorkspaceUpload_UsesRecordedPath is the D1b
// regression test for the common (no-collision) case where the D-1
// dual-write's exact destination was recorded via RecordUploadWorkPath: the
// announcement must use that EXACT path, not the fallback formula.
func TestResolveMediaRefs_AnnouncesWorkspaceUpload_UsesRecordedPath(t *testing.T) {
	resetUploadWorkPathRegistryForTest()
	defer resetUploadWorkPathRegistryForTest()

	home := t.TempDir()
	const wsID = "ws-announce"
	fixed := func() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }
	lib, err := library.New(home, wsID, library.WithClock(fixed))
	require.NoError(t, err)

	pngBytes := mustEncodeTestPNGLibSpec(t, 4, 4)
	ref, _, uploadErr := lib.UploadFixture("Copy of elicify_company_profile.png", bytes.NewReader(pngBytes))
	require.NoError(t, uploadErr)
	require.True(t, media.IsWorkspaceRef(ref))

	// Simulate the D-1 dual-write recording a DE-DUPLICATED destination name
	// (e.g. a same-named file already existed in .library/) — the announced
	// path must reflect this exact recorded value, not the plain fallback.
	RecordUploadWorkPath(ref, ".library/Copy of elicify_company_profile (1).png")

	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		if id != wsID {
			return nil, os.ErrNotExist
		}
		return lib, nil
	})

	msgs := []providers.Message{{Role: "user", Content: "", Media: []string{ref}}}
	resolved := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "", "claude-sonnet-4", nil, nil, nil, wsID)

	require.Len(t, resolved, 1)
	assert.Contains(t, resolved[0].Content,
		"[user uploaded: .library/Copy of elicify_company_profile (1).png]",
		"the announcement must name the EXACT recorded work-tree path so the "+
			"model can pass it straight to read_file/library_read")
}

// TestResolveMediaRefs_AnnouncesWorkspaceUpload_FallsBackWhenNotRecorded
// covers the registry-miss path (process restart, or a ref uploaded before
// this process started): the announcement must still appear, using the
// best-effort plain-name formula.
func TestResolveMediaRefs_AnnouncesWorkspaceUpload_FallsBackWhenNotRecorded(t *testing.T) {
	resetUploadWorkPathRegistryForTest()
	defer resetUploadWorkPathRegistryForTest()

	home := t.TempDir()
	const wsID = "ws-announce-fallback"
	lib, err := library.New(home, wsID)
	require.NoError(t, err)

	pngBytes := mustEncodeTestPNGLibSpec(t, 4, 4)
	ref, _, uploadErr := lib.UploadFixture("report.png", bytes.NewReader(pngBytes))
	require.NoError(t, uploadErr)

	// Deliberately do NOT call RecordUploadWorkPath — this is the "never
	// recorded" case the fallback formula exists for.

	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		return lib, nil
	})

	msgs := []providers.Message{{Role: "user", Content: "", Media: []string{ref}}}
	resolved := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "", "claude-sonnet-4", nil, nil, nil, wsID)

	require.Len(t, resolved, 1)
	assert.Contains(t, resolved[0].Content, "[user uploaded: .library/report.png]")
}

// TestResolveMediaRefs_NonWorkspaceRef_NoAnnouncement verifies a legacy
// media://<uuid> ref (no workspace-relative path exists for it) gets no
// upload announcement at all — buildUploadAnnouncement must return "" for
// these rather than fabricating a misleading path.
func TestResolveMediaRefs_NonWorkspaceRef_NoAnnouncement(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()
	pngPath := dir + "/plain.png"
	require.NoError(t, os.WriteFile(pngPath, mustEncodeTestPNGLibSpec(t, 2, 2), 0o644))
	ref, err := store.Store(pngPath, media.MediaMeta{Filename: "plain.png"}, "test")
	require.NoError(t, err)
	require.False(t, media.IsWorkspaceRef(ref))

	msgs := []providers.Message{{Role: "user", Content: "", Media: []string{ref}}}
	resolved := resolveMediaRefsWithOffload(msgs, store, 10*1024*1024, "", "claude-sonnet-4", nil, nil, nil, "")

	require.Len(t, resolved, 1)
	assert.NotContains(t, resolved[0].Content, "user uploaded",
		"a non-workspace ref has no workspace-relative path — nothing should be announced for it")
}

// ---- D-1 (pkg/agent half): SanitizeUploadFilename + the work-path registry ----

func TestSanitizeUploadFilename(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"plain name", "report.pptx", false},
		{"name with spaces", "Copy of elicify_company_profile.pptx", false},
		{"trims whitespace", "  spaced.txt  ", false},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"forward slash", "a/b.txt", true},
		{"backslash", "a\\b.txt", true},
		{"dot", ".", true},
		{"dot dot", "..", true},
		{"control character", "bad\nname.txt", true},
		{"over-long", string(make([]byte, 300)), true},
		// UAT Issue 6: a name starting with ".." isn't a traversal (it's
		// validated as a single component) but pkg/library's hidden
		// heuristic also matches it, so it must be rejected outright here
		// rather than silently vanishing from the default Library listing.
		{"leading dotdot with suffix", "..sneaky.pdf", true},
		{"leading dotdot url-encoded slash", "..%2fdana-pwned-encoded.txt", true},
		{"triple leading dot", "...triple-dot.txt", true},
		// A conventional single-leading-dot dotfile must remain allowed.
		{"legit single-dot dotfile", ".env", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeUploadFilename(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, got)
		})
	}
}

// TestSanitizeUploadFilename_CrossPlatformSafety covers the pkg/pathsafe
// checks layered onto SanitizeUploadFilename: Windows reserved device
// names, NTFS-illegal characters, a trailing dot/space, and the new
// (tighter, rune-based) length cap — see pathsafe's package doc for why
// every one of these applies unconditionally, not only when actually
// running on Windows.
func TestSanitizeUploadFilename_CrossPlatformSafety(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"reserved CON", "CON", true},
		{"reserved con lowercase", "con", true},
		{"reserved NUL with extension", "nul.txt", true},
		{"reserved COM1", "COM1.log", true},
		{"reserved LPT1", "lpt1", true},
		{"not reserved: CONsole", "CONsole.txt", false},
		{"illegal char <", "bad<name.txt", true},
		{"illegal char pipe", "bad|name.txt", true},
		{"illegal char question mark", "bad?name.txt", true},
		{"trailing dot", "report.", true},
		{"trailing space before extension is fine", "report .txt", false},
		{"exactly at the new cap", strings.Repeat("a", pathsafe.MaxComponentNameLength), false},
		{"one over the new cap", strings.Repeat("a", pathsafe.MaxComponentNameLength+1), true},
		// The whole reason the old 256-rune (byte-measured) cap was
		// replaced: a 210-char name passed under it, comfortably, while
		// already being unsafe once nested under a realistic Windows
		// install path (see pathsafe.MaxComponentNameLength's doc).
		{"legacy 210-char name now rejected", strings.Repeat("a", 210), true},
		// Real-world UAT names, including unicode/emoji, must still be
		// allowed — this feature must not regress ordinary filenames.
		{"real-world UAT name 1", "Copy of My Deck.pptx", false},
		{"real-world UAT name 2 (unicode/emoji)", "My Report (final) — résumé 测试 🎉.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeUploadFilename(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, got)
		})
	}
}

func TestUploadWorkPathRegistry_RecordAndLookup(t *testing.T) {
	resetUploadWorkPathRegistryForTest()
	defer resetUploadWorkPathRegistryForTest()

	_, ok := LookupUploadWorkPath("media://workspace/ws1/id1")
	assert.False(t, ok, "an unrecorded ref must miss")

	RecordUploadWorkPath("media://workspace/ws1/id1", ".library/report.pptx")
	got, ok := LookupUploadWorkPath("media://workspace/ws1/id1")
	require.True(t, ok)
	assert.Equal(t, ".library/report.pptx", got)

	// A later record for the SAME ref overwrites (e.g. a retry/replay).
	RecordUploadWorkPath("media://workspace/ws1/id1", ".library/report (1).pptx")
	got, ok = LookupUploadWorkPath("media://workspace/ws1/id1")
	require.True(t, ok)
	assert.Equal(t, ".library/report (1).pptx", got)
}

func TestUploadWorkPathRegistry_EmptyArgsAreNoop(t *testing.T) {
	resetUploadWorkPathRegistryForTest()
	defer resetUploadWorkPathRegistryForTest()

	RecordUploadWorkPath("", ".library/x.txt")
	RecordUploadWorkPath("media://workspace/ws1/id1", "")
	_, ok := LookupUploadWorkPath("media://workspace/ws1/id1")
	assert.False(t, ok, "an empty ref or relPath must not be recorded")
}

func TestFallbackAnnouncedUploadPath(t *testing.T) {
	assert.Equal(t, ".library/report.pptx", FallbackAnnouncedUploadPath("report.pptx"))
	assert.Equal(t, "", FallbackAnnouncedUploadPath(""),
		"an invalid filename yields no announcement, not a malformed one")
	assert.Equal(t, "", FallbackAnnouncedUploadPath("a/b.txt"))
}

func TestLibraryDirName(t *testing.T) {
	assert.Equal(t, ".library", LibraryDirName())
}
