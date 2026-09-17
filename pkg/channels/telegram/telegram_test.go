package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
)

const testToken = "1234567890:aaaabbbbaaaabbbbaaaabbbbaaaabbbbccc"

// stubCaller implements ta.Caller for testing.
type stubCaller struct {
	calls  []stubCall
	callFn func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error)
}

type stubCall struct {
	URL  string
	Data *ta.RequestData
}

func (s *stubCaller) Call(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
	s.calls = append(s.calls, stubCall{URL: url, Data: data})
	return s.callFn(ctx, url, data)
}

// stubConstructor implements ta.RequestConstructor for testing.
type stubConstructor struct{}

type multipartCall struct {
	Parameters map[string]string
	FileSizes  map[string]int
}

func (s *stubConstructor) JSONRequest(parameters any) (*ta.RequestData, error) {
	b, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	return &ta.RequestData{
		ContentType: "application/json",
		BodyRaw:     b,
	}, nil
}

func (s *stubConstructor) MultipartRequest(
	parameters map[string]string,
	files map[string]ta.NamedReader,
) (*ta.RequestData, error) {
	return &ta.RequestData{}, nil
}

type multipartRecordingConstructor struct {
	stubConstructor
	calls []multipartCall
}

func (s *multipartRecordingConstructor) MultipartRequest(
	parameters map[string]string,
	files map[string]ta.NamedReader,
) (*ta.RequestData, error) {
	call := multipartCall{
		Parameters: make(map[string]string, len(parameters)),
		FileSizes:  make(map[string]int, len(files)),
	}
	for k, v := range parameters {
		call.Parameters[k] = v
	}
	for field, file := range files {
		if file == nil {
			continue
		}
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		call.FileSizes[field] = len(data)
	}
	s.calls = append(s.calls, call)
	return &ta.RequestData{}, nil
}

// successResponse returns a ta.Response that telego will treat as a successful SendMessage.
func successResponse(t *testing.T) *ta.Response {
	t.Helper()
	msg := &telego.Message{MessageID: 1}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	return &ta.Response{Ok: true, Result: b}
}

// newTestChannel creates a TelegramChannel with a mocked bot for unit testing.
func newTestChannel(t *testing.T, caller *stubCaller) *TelegramChannel {
	return newTestChannelWithConstructor(t, caller, &stubConstructor{})
}

func newTestChannelWithConstructor(
	t *testing.T,
	caller *stubCaller,
	constructor ta.RequestConstructor,
) *TelegramChannel {
	t.Helper()

	bot, err := telego.NewBot(testToken,
		telego.WithAPICaller(caller),
		telego.WithRequestConstructor(constructor),
		telego.WithDiscardLogger(),
	)
	require.NoError(t, err)

	base := channels.NewBaseChannel("telegram", nil, nil, nil,
		channels.WithMaxMessageLength(4000),
	)
	base.SetRunning(true)

	return &TelegramChannel{
		BaseChannel: base,
		bot:         bot,
		config:      config.DefaultConfig(),
	}
}

func TestSendMedia_ImageFallbacksToDocumentOnInvalidDimensions(t *testing.T) {
	constructor := &multipartRecordingConstructor{}
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			switch {
			case strings.Contains(url, "sendPhoto"):
				return nil, errors.New(`api: 400 "Bad Request: PHOTO_INVALID_DIMENSIONS"`)
			case strings.Contains(url, "sendDocument"):
				return successResponse(t), nil
			default:
				t.Fatalf("unexpected API call: %s", url)
				return nil, errors.New("unreachable: t.Fatal halts the test goroutine")
			}
		},
	}
	ch := newTestChannelWithConstructor(t, caller, constructor)

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "woodstock-en-10s.png")
	content := []byte("fake-png-content")
	require.NoError(t, os.WriteFile(localPath, content, 0o644))

	ref, err := store.Store(
		localPath,
		media.MediaMeta{Filename: "woodstock-en-10s.png", ContentType: "image/png"},
		"scope-1",
	)
	require.NoError(t, err)

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "12345",
		Parts: []bus.MediaPart{{
			Type:    "image",
			Ref:     ref,
			Caption: "caption",
		}},
	})

	require.NoError(t, err)
	require.Len(t, caller.calls, 2)
	assert.Contains(t, caller.calls[0].URL, "sendPhoto")
	assert.Contains(t, caller.calls[1].URL, "sendDocument")
	require.Len(t, constructor.calls, 2)
	assert.Equal(t, len(content), constructor.calls[0].FileSizes["photo"])
	assert.Equal(t, len(content), constructor.calls[1].FileSizes["document"])
	assert.Equal(t, "caption", constructor.calls[1].Parameters["caption"])
}

func TestSendMedia_ImageNonDimensionErrorDoesNotFallback(t *testing.T) {
	constructor := &multipartRecordingConstructor{}
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return nil, errors.New("api: 500 \"server exploded\"")
		},
	}
	ch := newTestChannelWithConstructor(t, caller, constructor)

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "image.png")
	require.NoError(t, os.WriteFile(localPath, []byte("fake-png-content"), 0o644))

	ref, err := store.Store(localPath, media.MediaMeta{Filename: "image.png", ContentType: "image/png"}, "scope-1")
	require.NoError(t, err)

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "12345",
		Parts: []bus.MediaPart{{
			Type: "image",
			Ref:  ref,
		}},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, channels.ErrTemporary)
	require.Len(t, caller.calls, 1)
	assert.Contains(t, caller.calls[0].URL, "sendPhoto")
	require.Len(t, constructor.calls, 1)
	assert.NotContains(t, caller.calls[0].URL, "sendDocument")
}

// TestSendMedia_PartialFailureReturnsError is the FIX-2 review regression
// test proving the actual defect end-to-end: 2 of 3 parts genuinely succeed
// (real SendPhoto calls against a stubbed Bot API caller) and 1 fails to
// resolve locally. Before the fix, sentCount(2) > 0 made the original
// `if sentCount == 0` guard silently return nil — the 2 delivered parts left
// the tool's pre-baked success text untouched and neither the user nor the
// LLM ever learned the third part was dropped.
func TestSendMedia_PartialFailureReturnsError(t *testing.T) {
	constructor := &multipartRecordingConstructor{}
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			require.Contains(t, url, "sendPhoto", "only the 2 resolvable parts should ever reach the Bot API")
			return successResponse(t), nil
		},
	}
	ch := newTestChannelWithConstructor(t, caller, constructor)

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	tmpDir := t.TempDir()
	parts := make([]bus.MediaPart, 0, 3)
	for _, name := range []string{"a.png", "b.png"} {
		localPath := filepath.Join(tmpDir, name)
		require.NoError(t, os.WriteFile(localPath, []byte("fake-png-content"), 0o644))
		ref, err := store.Store(localPath, media.MediaMeta{Filename: name, ContentType: "image/png"}, "scope-1")
		require.NoError(t, err)
		parts = append(parts, bus.MediaPart{Type: "image", Ref: ref})
	}
	// Third part deliberately unresolvable.
	parts = append(parts, bus.MediaPart{Type: "image", Ref: "media://does-not-exist"})

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "12345",
		Parts:  parts,
	})

	require.Error(t, err, "SendMedia must report failure even though 2 of 3 parts genuinely succeeded")
	assert.Contains(t, err.Error(), "1 of 3 media parts failed to send")
	assert.ErrorIs(t, err, channels.ErrSendFailed,
		"must be classified permanent so sendMediaWithRetry does not re-deliver the 2 already-successful parts")
	assert.Len(t, caller.calls, 2, "the 2 resolvable parts must still have been sent")
}

// TestSendMedia_MidLoopSendFailureAfterPartialSuccessIsPermanent is the
// CRITICAL sendfile-fix review regression test: a genuine Bot API failure
// on part 2, after part 1 has already been sent successfully, must be
// classified permanent (channels.ErrSendFailed) — not the bare
// channels.ErrTemporary this used to return unconditionally. Manager's
// sendMediaWithRetry has no per-part resume; retrying this exact msg would
// re-resolve and re-send part 1, duplicating it for the user.
func TestSendMedia_MidLoopSendFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	var sendPhotoCalls int
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			require.Contains(t, url, "sendPhoto", "only image parts are sent in this test")
			sendPhotoCalls++
			if sendPhotoCalls == 1 {
				return successResponse(t), nil
			}
			return nil, errors.New(`api: 500 "Internal Server Error"`)
		},
	}
	ch := newTestChannel(t, caller)

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	tmpDir := t.TempDir()
	parts := make([]bus.MediaPart, 0, 2)
	for _, name := range []string{"a.png", "b.png"} {
		localPath := filepath.Join(tmpDir, name)
		require.NoError(t, os.WriteFile(localPath, []byte("fake-png-content"), 0o644))
		ref, err := store.Store(localPath, media.MediaMeta{Filename: name, ContentType: "image/png"}, "scope-1")
		require.NoError(t, err)
		parts = append(parts, bus.MediaPart{Type: "image", Ref: ref})
	}

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "12345",
		Parts:  parts,
	})

	require.Error(t, err, "SendMedia must report the part-2 send failure")
	assert.ErrorIs(t, err, channels.ErrSendFailed,
		"a mid-loop send failure after a partial success must be classified permanent, "+
			"or Manager.sendMediaWithRetry would retry and re-send part 1 (already delivered)")
	assert.NotErrorIs(t, err, channels.ErrTemporary,
		"must not ALSO match ErrTemporary — that would make the retry decision ambiguous")
	assert.Equal(t, 2, sendPhotoCalls, "part 1 sent once, part 2 attempted once (then SendMedia returned)")
}

// TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning is the FR-028a
// review regression test: a media ref denied by the caller-workspace
// membership guard (media.IsCallerWorkspaceDenied) must get its own
// distinct WARN log line naming it a workspace-boundary denial, instead of
// being folded into the generic "Failed to resolve media ref" ERROR log
// used for a routine stale/missing ref. Delivery semantics (permanent
// ErrSendFailed, no retry) are unchanged either way.
func TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "telegram-media.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	ch := newTestChannel(t, &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			t.Fatal("no Bot API call should be reached: the caller-workspace guard fires before any resolve")
			return nil, errors.New("unreachable: t.Fatal halts the test goroutine")
		},
	})
	ch.SetMediaStore(media.NewFileMediaStore())

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "12345",
		// WorkspaceID deliberately empty: a workspace-prefixed ref with no
		// caller-workspace context trips the FR-028a guard
		// (ErrWorkspaceContextRequired) before any media store provider is
		// even consulted.
		Parts: []bus.MediaPart{{Type: "image", Ref: "media://workspace/other-ws/some-id"}},
	})

	require.Error(t, err, "SendMedia must still report failure for a denied workspace ref")
	assert.ErrorIs(t, err, channels.ErrSendFailed,
		"a caller-workspace denial is permanent, same as any other local resolve failure")

	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr, "reading captured log file")
	logStr := string(logged)
	assert.Contains(t, logStr, "Media ref denied by caller-workspace guard",
		"the FR-028a denial must get its own distinct WARN log line")
	assert.NotContains(t, logStr, "Failed to resolve media ref",
		"the denial must NOT also log the generic message (would defeat distinguishing it)")
}

func TestSend_EmptyContent(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			t.Fatal("SendMessage should not be called for empty content")
			return nil, errors.New("unreachable: t.Fatal halts the test goroutine")
		},
	}
	ch := newTestChannel(t, caller)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "",
	})

	assert.NoError(t, err)
	assert.Empty(t, caller.calls, "no API calls should be made for empty content")
}

func TestSend_ShortMessage_SingleCall(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Hello, world!",
	})

	assert.NoError(t, err)
	assert.Len(t, caller.calls, 1, "short message should result in exactly one SendMessage call")
}

func TestSend_LongMessage_SingleCall(t *testing.T) {
	// With WithMaxMessageLength(4000), the Manager pre-splits messages before
	// they reach Send(). A message at exactly 4000 chars should go through
	// as a single SendMessage call (no re-split needed since HTML expansion
	// won't exceed 4096 for plain text).
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	longContent := strings.Repeat("a", 4000)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: longContent,
	})

	assert.NoError(t, err)
	assert.Len(t, caller.calls, 1, "pre-split message within limit should result in one SendMessage call")
}

func TestSend_HTMLFallback_PerChunk(t *testing.T) {
	callCount := 0
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			callCount++
			// Fail on odd calls (HTML attempt), succeed on even calls (plain text fallback)
			if callCount%2 == 1 {
				return nil, errors.New("Bad Request: can't parse entities")
			}
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Hello **world**",
	})

	assert.NoError(t, err)
	// One short message → 1 HTML attempt (fail) + 1 plain text fallback (success) = 2 calls
	assert.Equal(t, 2, len(caller.calls), "should have HTML attempt + plain text fallback")
}

func TestSend_HTMLFallback_BothFail(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return nil, errors.New("send failed")
		},
	}
	ch := newTestChannel(t, caller)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Hello",
	})

	assert.Error(t, err)
	assert.True(t, errors.Is(err, channels.ErrTemporary), "error should wrap ErrTemporary")
	assert.Equal(t, 2, len(caller.calls), "should have HTML attempt + plain text attempt")
}

func TestSend_LongMessage_HTMLFallback_StopsOnError(t *testing.T) {
	// With a long message that gets split into 2 chunks, if both HTML and
	// plain text fail on the first chunk, Send should return early.
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return nil, errors.New("send failed")
		},
	}
	ch := newTestChannel(t, caller)

	longContent := strings.Repeat("x", 4001)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: longContent,
	})

	assert.Error(t, err)
	// Should fail on the first chunk (2 calls: HTML + fallback), never reaching the second chunk.
	assert.Equal(t, 2, len(caller.calls), "should stop after first chunk fails both HTML and plain text")
}

func TestSend_MarkdownShortButHTMLLong_MultipleCalls(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	// Create markdown whose length is <= 4000 but whose HTML expansion is much longer.
	// "**a** " (6 chars) becomes "<b>a</b> " (9 chars) in HTML, so repeating it many times
	// yields HTML that exceeds Telegram's limit while markdown stays within it.
	markdownContent := strings.Repeat("**a** ", 600) // 3600 chars markdown, HTML ~5400+ chars
	assert.LessOrEqual(t, len([]rune(markdownContent)), 4000, "markdown content must not exceed chunk size")

	htmlExpanded := markdownToTelegramHTML(markdownContent)
	assert.Greater(
		t, len([]rune(htmlExpanded)), 4096,
		"HTML expansion must exceed Telegram limit for this test to be meaningful",
	)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: markdownContent,
	})

	assert.NoError(t, err)
	assert.Greater(
		t, len(caller.calls), 1,
		"markdown-short but HTML-long message should be split into multiple SendMessage calls",
	)
}

func TestSend_HTMLOverflow_WordBoundary(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	// We want to force a split near index ~2600 while keeping markdown length <= 4000.
	// Prefix of 430 bold units (6 chars each) = 2580 chars.
	// Expansion per unit is +3 chars when converted to HTML, so 2580 + 430*3 = 3870.
	prefix := strings.Repeat("**a** ", 430)
	targetWord := "TARGETWORDTHATSTAYSTOGETHER"
	// Suffix of 230 bold units (6 chars each) = 1380 chars.
	// Total markdown length: 2580 (prefix) + 27 (target word) + 1380 (suffix) = 3987 <= 4000.
	// HTML expansion adds ~3 chars per bold unit: (430 + 230)*3 = 1980 extra chars,
	// so total HTML length comfortably exceeds 4096.
	suffix := strings.Repeat(" **b**", 230)
	content := prefix + targetWord + suffix

	// Ensure the test content matches the intended boundary conditions.
	assert.LessOrEqual(t, len([]rune(content)), 4000, "markdown content must not exceed chunk size for this test")

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "123456",
		Content: content,
	})

	assert.NoError(t, err)

	foundFullWord := false
	for i, call := range caller.calls {
		var params map[string]any
		err := json.Unmarshal(call.Data.BodyRaw, &params)
		require.NoError(t, err)
		text, _ := params["text"].(string)

		hasWord := strings.Contains(text, targetWord)
		t.Logf("Chunk %d length: %d, contains target word: %v", i, len(text), hasWord)

		if hasWord {
			foundFullWord = true
			break
		}
	}

	assert.True(t, foundFullWord, "The target word should not be split between chunks")
}

func TestSend_NotRunning(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			t.Fatal("should not be called")
			return nil, errors.New("unreachable: t.Fatal halts the test goroutine")
		},
	}
	ch := newTestChannel(t, caller)
	ch.SetRunning(false)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Hello",
	})

	assert.ErrorIs(t, err, channels.ErrNotRunning)
	assert.Empty(t, caller.calls)
}

func TestSend_InvalidChatID(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			t.Fatal("should not be called")
			return nil, errors.New("unreachable: t.Fatal halts the test goroutine")
		},
	}
	ch := newTestChannel(t, caller)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "not-a-number",
		Content: "Hello",
	})

	assert.Error(t, err)
	assert.True(t, errors.Is(err, channels.ErrSendFailed), "error should wrap ErrSendFailed")
	assert.Empty(t, caller.calls)
}

func TestParseTelegramChatID_Plain(t *testing.T) {
	cid, tid, err := parseTelegramChatID("12345")
	assert.NoError(t, err)
	assert.Equal(t, int64(12345), cid)
	assert.Equal(t, 0, tid)
}

func TestParseTelegramChatID_NegativeGroup(t *testing.T) {
	cid, tid, err := parseTelegramChatID("-1001234567890")
	assert.NoError(t, err)
	assert.Equal(t, int64(-1001234567890), cid)
	assert.Equal(t, 0, tid)
}

func TestParseTelegramChatID_WithThreadID(t *testing.T) {
	cid, tid, err := parseTelegramChatID("-1001234567890/42")
	assert.NoError(t, err)
	assert.Equal(t, int64(-1001234567890), cid)
	assert.Equal(t, 42, tid)
}

func TestParseTelegramChatID_GeneralTopic(t *testing.T) {
	cid, tid, err := parseTelegramChatID("-100123/1")
	assert.NoError(t, err)
	assert.Equal(t, int64(-100123), cid)
	assert.Equal(t, 1, tid)
}

func TestParseTelegramChatID_Invalid(t *testing.T) {
	_, _, err := parseTelegramChatID("not-a-number")
	assert.Error(t, err)
}

func TestParseTelegramChatID_InvalidThreadID(t *testing.T) {
	_, _, err := parseTelegramChatID("-100123/not-a-thread")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid thread ID")
}

func TestSend_WithForumThreadID(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "-1001234567890/42",
		Content: "Hello from topic",
	})

	assert.NoError(t, err)
	assert.Len(t, caller.calls, 1)
}

func TestHandleMessage_ForumTopic_SetsMetadata(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &TelegramChannel{
		BaseChannel: channels.NewBaseChannel("telegram", nil, messageBus, nil),
		ctx:         context.Background(),
	}

	msg := &telego.Message{
		Text:            "hello from topic",
		MessageID:       10,
		MessageThreadID: 42,
		Chat: telego.Chat{
			ID:      -1001234567890,
			Type:    "supergroup",
			IsForum: true,
		},
		From: &telego.User{
			ID:        7,
			FirstName: "Alice",
		},
	}

	err := ch.handleMessage(context.Background(), msg)
	require.NoError(t, err)

	inbound, ok := <-messageBus.InboundChan()
	require.True(t, ok, "expected inbound message")

	// Composite chatID should include thread ID
	assert.Equal(t, "-1001234567890/42", inbound.ChatID)

	// Peer ID should include thread ID for session key isolation
	assert.Equal(t, bus.PeerGroup, inbound.Peer.Kind)
	assert.Equal(t, "-1001234567890/42", inbound.Peer.ID)

	// Parent peer metadata should be set for agent binding
	assert.Equal(t, "topic", inbound.Metadata["parent_peer_kind"])
	assert.Equal(t, "42", inbound.Metadata["parent_peer_id"])
}

func TestHandleMessage_NoForum_NoThreadMetadata(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &TelegramChannel{
		BaseChannel: channels.NewBaseChannel("telegram", nil, messageBus, nil),
		ctx:         context.Background(),
	}

	msg := &telego.Message{
		Text:      "regular group message",
		MessageID: 11,
		Chat: telego.Chat{
			ID:   -100999,
			Type: "group",
		},
		From: &telego.User{
			ID:        8,
			FirstName: "Bob",
		},
	}

	err := ch.handleMessage(context.Background(), msg)
	require.NoError(t, err)

	inbound, ok := <-messageBus.InboundChan()
	require.True(t, ok)

	// Plain chatID without thread suffix
	assert.Equal(t, "-100999", inbound.ChatID)

	// Peer ID should be raw chat ID (no thread suffix)
	assert.Equal(t, bus.PeerGroup, inbound.Peer.Kind)
	assert.Equal(t, "-100999", inbound.Peer.ID)

	// No parent peer metadata
	assert.Empty(t, inbound.Metadata["parent_peer_kind"])
	assert.Empty(t, inbound.Metadata["parent_peer_id"])
}

func TestHandleMessage_ReplyThread_NonForum_NoIsolation(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &TelegramChannel{
		BaseChannel: channels.NewBaseChannel("telegram", nil, messageBus, nil),
		ctx:         context.Background(),
	}

	// In regular groups, reply threads set MessageThreadID to the original
	// message ID. This should NOT trigger per-thread session isolation.
	msg := &telego.Message{
		Text:            "reply in thread",
		MessageID:       20,
		MessageThreadID: 15,
		Chat: telego.Chat{
			ID:      -100999,
			Type:    "supergroup",
			IsForum: false,
		},
		From: &telego.User{
			ID:        9,
			FirstName: "Carol",
		},
	}

	err := ch.handleMessage(context.Background(), msg)
	require.NoError(t, err)

	inbound, ok := <-messageBus.InboundChan()
	require.True(t, ok)

	// chatID should NOT include thread suffix for non-forum groups
	assert.Equal(t, "-100999", inbound.ChatID)

	// Peer ID should be raw chat ID (shared session for whole group)
	assert.Equal(t, bus.PeerGroup, inbound.Peer.Kind)
	assert.Equal(t, "-100999", inbound.Peer.ID)

	// No parent peer metadata
	assert.Empty(t, inbound.Metadata["parent_peer_kind"])
	assert.Empty(t, inbound.Metadata["parent_peer_id"])
}

func TestHandleMessage_EmptyContent_Ignored(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &TelegramChannel{
		BaseChannel: channels.NewBaseChannel("telegram", nil, messageBus, nil),
		ctx:         context.Background(),
	}

	// Service message with no text/caption/media (like ForumTopicCreated)
	msg := &telego.Message{
		MessageID: 123,
		Chat: telego.Chat{
			ID:   456,
			Type: "group",
		},
		From: &telego.User{
			ID:        789,
			FirstName: "User",
		},
	}

	err := ch.handleMessage(context.Background(), msg)
	require.NoError(t, err)

	// Should NOT publish to message bus
	select {
	case <-messageBus.InboundChan():
		t.Fatal("Empty message should not be published to message bus")
	default:
	}
}
