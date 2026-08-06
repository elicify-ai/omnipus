package wecom

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	basechannels "github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning is the FR-028a
// review regression test: a media ref denied by the caller-workspace
// membership guard (media.IsCallerWorkspaceDenied) must get its own
// distinct WARN log line naming it a workspace-boundary denial. Unlike
// Telegram/Matrix/Slack/Discord/Feishu (which `continue` past a resolve
// failure), WeCom's resolveOutboundPart hard-returns the raw error and
// SendMedia wraps it in channels.ErrSendFailed immediately — there was no
// prior logging at this exact call site at all, so this only adds the new
// WARN line, it does not need to distinguish it from an existing ERROR log.
func TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "wecom-media.log")
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

	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)
	ch.SetMediaStore(media.NewFileMediaStore())

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "wecom",
		ChatID:  "chat-1",
		// WorkspaceID deliberately empty: a workspace-prefixed ref with no
		// caller-workspace context trips the FR-028a guard
		// (ErrWorkspaceContextRequired) before any media store provider is
		// even consulted.
		Parts: []bus.MediaPart{{Type: "image", Ref: "media://workspace/other-ws/some-id"}},
	})
	if err == nil {
		t.Fatal("SendMedia() error = nil, want error for a denied workspace ref")
	}
	if !errors.Is(err, basechannels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed "+
			"(a caller-workspace denial is permanent, same as any other local resolve failure)", err)
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

func TestStoreRemoteMedia_DetectsJPEGContentTypeFromBody(t *testing.T) {
	t.Parallel()

	const jpegBase64 = "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////" +
		"//////////////////////////////////////////////////////////////////////////////////////////////2wBDAf//////////////////////////////////////////////////////////////////////////////////////" +
		"//////////////////////////////////////////////////////////////////////////////////////////////wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAb/xAAVEQEBAAAAAAAAAAAAAAAAAAAABf/aAAwDAQACEAMQAAAB6A//xAAVEAEBAAAAAAAAAAAAAAAAAAAAEf/aAAgBAQABBQJf/8QAFBEBAAAAAAAAAAAAAAAAAAAAEP/aAAgBAwEBPwF//8QAFBEBAAAAAAAAAAAAAAAAAAAAEP/aAAgBAgEBPwF//8QAFBABAAAAAAAAAAAAAAAAAAAAEP/aAAgBAQAGPwJf/8QAFBABAAAAAAAAAAAAAAAAAAAAEP/aAAgBAQABPyFf/9k="

	jpegData := decodeTestBase64(t, jpegBase64)
	store := media.NewFileMediaStore()
	ch := &WeComChannel{
		BaseChannel: basechannels.NewBaseChannel("wecom", nil, nil, nil),
		mediaClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
					Body:       io.NopCloser(bytes.NewReader(jpegData)),
				}, nil
			}),
		},
	}
	ch.SetMediaStore(store)

	ref, err := ch.storeRemoteMedia(context.Background(), "test-scope", "msg-1", "https://wecom.example/media", "", "")
	if err != nil {
		t.Fatalf("storeRemoteMedia returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = store.ReleaseAll("test-scope")
	})

	_, meta, err := store.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("resolve media ref: %v", err)
	}
	if meta.ContentType != "image/jpeg" {
		t.Fatalf("expected image/jpeg content type, got %q", meta.ContentType)
	}
	if !strings.HasSuffix(meta.Filename, ".jpg") && !strings.HasSuffix(meta.Filename, ".jpeg") {
		t.Fatalf("expected jpeg filename, got %q", meta.Filename)
	}
}

func TestDetectWeComMediaMetadata_UsesFallbackExtensionWhenBodyUnknown(t *testing.T) {
	t.Parallel()

	filename, contentType := detectWeComMediaMetadata([]byte("not a real image"), "msg-2.pdf", "", "", "")
	if filename != "msg-2.pdf" {
		t.Fatalf("expected fallback filename to be preserved, got %q", filename)
	}
	if contentType != "application/pdf" {
		t.Fatalf("expected application/pdf from fallback extension, got %q", contentType)
	}
}

func TestStoreRemoteMedia_PreservesSuffixFromURL(t *testing.T) {
	t.Parallel()

	docxLikeData := []byte("PK\x03\x04fake office payload")
	store := media.NewFileMediaStore()
	ch := &WeComChannel{
		BaseChannel: basechannels.NewBaseChannel("wecom", nil, nil, nil),
		mediaClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
					Body:       io.NopCloser(bytes.NewReader(docxLikeData)),
				}, nil
			}),
		},
	}
	ch.SetMediaStore(store)

	ref, err := ch.storeRemoteMedia(
		context.Background(),
		"test-scope",
		"msg-docx",
		"https://wecom.example/media/report.docx?signature=1",
		"",
		".bin",
	)
	if err != nil {
		t.Fatalf("storeRemoteMedia returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = store.ReleaseAll("test-scope")
	})

	localPath, meta, err := store.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("resolve media ref: %v", err)
	}
	if !strings.HasSuffix(meta.Filename, ".docx") {
		t.Fatalf("expected docx filename, got %q", meta.Filename)
	}
	if !strings.HasSuffix(strings.ToLower(localPath), ".docx") {
		t.Fatalf("expected docx temp path, got %q", localPath)
	}
}

func TestStoreRemoteMedia_PreservesSuffixFromContentDisposition(t *testing.T) {
	t.Parallel()

	pptxLikeData := []byte("PK\x03\x04fake office payload")
	store := media.NewFileMediaStore()
	ch := &WeComChannel{
		BaseChannel: basechannels.NewBaseChannel("wecom", nil, nil, nil),
		mediaClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type":        []string{"application/octet-stream"},
						"Content-Disposition": []string{`attachment; filename="slides.pptx"`},
					},
					Body: io.NopCloser(bytes.NewReader(pptxLikeData)),
				}, nil
			}),
		},
	}
	ch.SetMediaStore(store)

	ref, err := ch.storeRemoteMedia(
		context.Background(),
		"test-scope",
		"msg-pptx",
		"https://wecom.example/media/download",
		"",
		".bin",
	)
	if err != nil {
		t.Fatalf("storeRemoteMedia returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = store.ReleaseAll("test-scope")
	})

	localPath, meta, err := store.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("resolve media ref: %v", err)
	}
	if !strings.HasSuffix(meta.Filename, ".pptx") {
		t.Fatalf("expected pptx filename, got %q", meta.Filename)
	}
	if !strings.HasSuffix(strings.ToLower(localPath), ".pptx") {
		t.Fatalf("expected pptx temp path, got %q", localPath)
	}
}

func decodeTestBase64(t *testing.T, value string) []byte {
	t.Helper()

	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(value)))
	if err != nil {
		t.Fatalf("decode base64 fixture: %v", err)
	}
	return data
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
