package weixin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	basechannels "github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// weixinMediaTestServer fakes the getuploadurl, CDN /upload, and sendmessage
// endpoints SendMedia drives, with independent per-call failure injection so
// tests can simulate "part 1 succeeds, part 2 fails" without affecting every
// call to an endpoint uniformly.
type weixinMediaTestServer struct {
	uploadURLCalls int
	cdnUploadCalls int
	sendMsgCalls   int

	// failUploadURLOnCall / failSendMsgOnCall are 1-based call indices for
	// their respective endpoint; 0 means "never fail".
	failUploadURLOnCall int
	failSendMsgOnCall   int
}

func (s *weixinMediaTestServer) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "getuploadurl"):
		s.uploadURLCalls++
		if s.failUploadURLOnCall != 0 && s.uploadURLCalls == s.failUploadURLOnCall {
			_, _ = w.Write([]byte(`{"ret":1,"errcode":500,"errmsg":"getuploadurl boom"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ret":0,"errcode":0,"upload_param":"initial-upload-param"}`))
	case r.URL.Path == "/upload":
		s.cdnUploadCalls++
		w.Header().Set("X-Encrypted-Param", "download-param")
		w.WriteHeader(http.StatusOK)
	case strings.HasSuffix(r.URL.Path, "sendmessage"):
		s.sendMsgCalls++
		if s.failSendMsgOnCall != 0 && s.sendMsgCalls == s.failSendMsgOnCall {
			_, _ = w.Write([]byte(`{"ret":1,"errcode":500,"errmsg":"sendmessage boom"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ret":0,"errcode":0}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// newWeixinMediaTestChannel wires a WeixinChannel to an httptest server
// running weixinMediaTestServer, with config.CDNBaseURL pointed at that same
// server so the plaintext-upload CDN round trip stays local too.
func newWeixinMediaTestChannel(t *testing.T, srv *weixinMediaTestServer) *WeixinChannel {
	t.Helper()
	testSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	t.Cleanup(testSrv.Close)

	api, err := NewApiClient(testSrv.URL+"/", "test-token", "")
	if err != nil {
		t.Fatalf("NewApiClient() error = %v", err)
	}

	msgBus := bus.NewMessageBus()
	base := basechannels.NewBaseChannel("weixin", config.WeixinConfig{}, msgBus, nil)
	base.SetRunning(true)
	ch := &WeixinChannel{
		BaseChannel: base,
		api:         api,
		config: config.WeixinConfig{
			CDNBaseURL: testSrv.URL,
		},
		bus:         msgBus,
		typingCache: make(map[string]typingTicketCacheEntry),
	}
	ch.contextTokens.Store("chat-1", "ctx-1")
	return ch
}

func writeWeixinTempMediaFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// twoPartWeixinMediaMessage returns an OutboundMediaMessage referencing two
// distinct local files, for the sentCount>0 partial-success regression tests
// below.
func twoPartWeixinMediaMessage(t *testing.T, chatID string) bus.OutboundMediaMessage {
	t.Helper()
	tmpDir := t.TempDir()
	parts := make([]bus.MediaPart, 0, 2)
	for _, name := range []string{"a.png", "b.png"} {
		localPath := writeWeixinTempMediaFile(t, tmpDir, name, []byte("fake-image-data-"+name))
		parts = append(parts, bus.MediaPart{Type: "image", Ref: localPath, ContentType: "image/png"})
	}
	return bus.OutboundMediaMessage{ChatID: chatID, Parts: parts}
}

// TestSendMedia_MidLoopUploadFailureAfterPartialSuccessIsPermanent is the
// CRITICAL sendfile-fix review regression test: part 1 fully uploads AND
// sends successfully, then part 2's getuploadurl call fails with a genuine
// API error. Before the fix this returned a bare basechannels.ErrTemporary
// unconditionally, so Manager.sendMediaWithRetry would retry the whole
// message — re-uploading and re-sending part 1, duplicating it for the user.
// The fix must classify this permanent (basechannels.ErrSendFailed) instead.
func TestSendMedia_MidLoopUploadFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	srv := &weixinMediaTestServer{failUploadURLOnCall: 2}
	ch := newWeixinMediaTestChannel(t, srv)

	err := ch.SendMedia(context.Background(), twoPartWeixinMediaMessage(t, "chat-1"))

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from part 2's getuploadurl failure")
	}
	if !errors.Is(err, basechannels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed (permanent, part 1 already delivered)", err)
	}
	if errors.Is(err, basechannels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrTemporary "+
			"(ambiguous retry classification)", err)
	}
	if srv.uploadURLCalls != 2 {
		t.Fatalf("uploadURLCalls = %d, want 2 (part 1 upload, part 2 upload attempt)", srv.uploadURLCalls)
	}
	if srv.sendMsgCalls != 1 {
		t.Fatalf("sendMsgCalls = %d, want 1 (only part 1 reached sendmessage)", srv.sendMsgCalls)
	}
}

// TestSendMedia_MidLoopPostFailureAfterPartialSuccessIsPermanent is the same
// CRITICAL regression as above, but the failure happens one step later: part
// 2's file has already been uploaded to the CDN (getuploadurl + CDN upload
// both succeed) and only the follow-up sendmessage call fails. Before the fix
// this also returned a bare basechannels.ErrTemporary unconditionally.
func TestSendMedia_MidLoopPostFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	srv := &weixinMediaTestServer{failSendMsgOnCall: 2}
	ch := newWeixinMediaTestChannel(t, srv)

	err := ch.SendMedia(context.Background(), twoPartWeixinMediaMessage(t, "chat-1"))

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from part 2's sendmessage failure")
	}
	if !errors.Is(err, basechannels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed (permanent, part 1 already delivered)", err)
	}
	if errors.Is(err, basechannels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrTemporary "+
			"(ambiguous retry classification)", err)
	}
	if srv.uploadURLCalls != 2 {
		t.Fatalf("uploadURLCalls = %d, want 2 (both parts fully uploaded)", srv.uploadURLCalls)
	}
	if srv.cdnUploadCalls != 2 {
		t.Fatalf("cdnUploadCalls = %d, want 2 (both parts fully uploaded)", srv.cdnUploadCalls)
	}
	if srv.sendMsgCalls != 2 {
		t.Fatalf("sendMsgCalls = %d, want 2 (part 1 succeeded, part 2 attempted then failed)", srv.sendMsgCalls)
	}
}

// TestSendMedia_UploadFailureWithNothingSentIsTemporary is the control case:
// when the very first part's upload fails (sentCount == 0, nothing has
// reached the platform yet), the failure must keep its ErrTemporary
// classification so sendMediaWithRetry still retries — a bare retry here
// cannot duplicate anything.
func TestSendMedia_UploadFailureWithNothingSentIsTemporary(t *testing.T) {
	srv := &weixinMediaTestServer{failUploadURLOnCall: 1}
	ch := newWeixinMediaTestChannel(t, srv)

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "chat-1",
		Parts: []bus.MediaPart{{
			Type: "image",
			Ref:  writeWeixinTempMediaFile(t, t.TempDir(), "a.png", []byte("fake-image-data")),
		}},
	})

	if !errors.Is(err, basechannels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v, want ErrTemporary (nothing delivered yet, retry is safe)", err)
	}
	if errors.Is(err, basechannels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrSendFailed "+
			"(ambiguous retry classification)", err)
	}
}

// TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning is the FR-028a
// review regression test: a media ref denied by the caller-workspace
// membership guard (media.IsCallerWorkspaceDenied) must get its own
// distinct WARN log line naming it a workspace-boundary denial. Weixin's
// resolveOutboundPart hard-returns the raw error (wrapped in
// basechannels.ErrSendFailed by the SendMedia loop) and had no logging at
// this exact call site before this fix, so this only adds the new WARN
// line — there is no prior generic message to distinguish it from here.
func TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "weixin-media.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging() error = %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	srv := &weixinMediaTestServer{}
	ch := newWeixinMediaTestChannel(t, srv)
	ch.SetMediaStore(media.NewFileMediaStore())

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "chat-1",
		// WorkspaceID deliberately empty: a workspace-prefixed ref with no
		// caller-workspace context trips the FR-028a guard
		// (ErrWorkspaceContextRequired) before any workspace-library
		// provider is even consulted (none is wired on this store).
		Parts: []bus.MediaPart{{Type: "image", Ref: "media://workspace/other-ws/some-id"}},
	})
	if err == nil {
		t.Fatal("SendMedia() error = nil, want error for a denied workspace ref")
	}
	if !errors.Is(err, basechannels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed "+
			"(a caller-workspace denial is permanent, same as any other local resolve failure)", err)
	}
	if srv.uploadURLCalls != 0 {
		t.Fatalf("uploadURLCalls = %d, want 0 (the guard fires before any upload attempt)", srv.uploadURLCalls)
	}

	logged, readErr := os.ReadFile(logFile)
	if readErr != nil {
		t.Fatalf("reading captured log file: %v", readErr)
	}
	logStr := string(logged)
	if !strings.Contains(logStr, "Media ref denied by caller-workspace guard") {
		t.Fatalf("log file missing the distinct FR-028a denial WARN line; got:\n%s", logStr)
	}
}
