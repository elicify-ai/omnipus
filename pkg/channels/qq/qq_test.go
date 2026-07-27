package qq

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/openapi/options"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
)

func TestHandleC2CMessage_IncludesAccountIDMetadata(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}

	err := ch.handleC2CMessage()(nil, &dto.WSC2CMessageData{
		ID:      "msg-1",
		Content: "hello",
		Author: &dto.User{
			ID: "7750283E123456",
		},
	})
	if err != nil {
		t.Fatalf("handleC2CMessage() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for inbound message")
			return
		case inbound, ok := <-messageBus.InboundChan():
			if !ok {
				t.Fatal("expected inbound message")
			}
			if inbound.Metadata["account_id"] != "7750283E123456" {
				t.Fatalf("account_id metadata = %q, want %q", inbound.Metadata["account_id"], "7750283E123456")
			}
			return
		}
	}
}

func TestHandleC2CMessage_AttachmentOnlyPublishesMedia(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()
	localPath := writeTempFile(t, t.TempDir(), "image.png", []byte("fake-image"))

	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
		downloadFn: func(urlStr, filename string) string {
			if filename != "image.png" {
				t.Fatalf("download filename = %q, want image.png", filename)
			}
			return localPath
		},
	}
	ch.SetMediaStore(store)

	err := ch.handleC2CMessage()(nil, &dto.WSC2CMessageData{
		ID:      "msg-attachment",
		Content: "",
		Author: &dto.User{
			ID: "7750283E123456",
		},
		Attachments: []*dto.MessageAttachment{{
			URL:         "https://example.com/image.png",
			FileName:    "image.png",
			ContentType: "image/png",
		}},
	})
	if err != nil {
		t.Fatalf("handleC2CMessage() error = %v", err)
	}

	inbound := waitInboundMessage(t, messageBus)
	if inbound.Content != "[image: image.png]" {
		t.Fatalf("inbound.Content = %q", inbound.Content)
	}
	if len(inbound.Media) != 1 {
		t.Fatalf("len(inbound.Media) = %d, want 1", len(inbound.Media))
	}
	if !strings.HasPrefix(inbound.Media[0], "media://") {
		t.Fatalf("inbound.Media[0] = %q, want media:// ref", inbound.Media[0])
	}
	_, meta, err := store.ResolveWithMeta(inbound.Media[0])
	if err != nil {
		t.Fatalf("ResolveWithMeta() error = %v", err)
	}
	if meta.Filename != "image.png" {
		t.Fatalf("meta.Filename = %q, want image.png", meta.Filename)
	}
	if meta.ContentType != "image/png" {
		t.Fatalf("meta.ContentType = %q, want image/png", meta.ContentType)
	}
}

func TestHandleGroupATMessage_AttachmentOnlyPublishesMedia(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()
	localPath := writeTempFile(t, t.TempDir(), "report.pdf", []byte("fake-pdf"))

	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
		downloadFn: func(urlStr, filename string) string {
			if filename != "report.pdf" {
				t.Fatalf("download filename = %q, want report.pdf", filename)
			}
			return localPath
		},
	}
	ch.SetMediaStore(store)

	err := ch.handleGroupATMessage()(nil, &dto.WSGroupATMessageData{
		ID:      "group-attachment",
		GroupID: "group-1",
		Content: "",
		Author: &dto.User{
			ID: "7750283E123456",
		},
		Attachments: []*dto.MessageAttachment{{
			URL:         "https://example.com/report.pdf",
			FileName:    "report.pdf",
			ContentType: "application/pdf",
		}},
	})
	if err != nil {
		t.Fatalf("handleGroupATMessage() error = %v", err)
	}

	inbound := waitInboundMessage(t, messageBus)
	if inbound.Content != "[file: report.pdf]" {
		t.Fatalf("inbound.Content = %q", inbound.Content)
	}
	if len(inbound.Media) != 1 {
		t.Fatalf("len(inbound.Media) = %d, want 1", len(inbound.Media))
	}
	if !strings.HasPrefix(inbound.Media[0], "media://") {
		t.Fatalf("inbound.Media[0] = %q, want media:// ref", inbound.Media[0])
	}
	if inbound.Peer.Kind != bus.PeerGroup || inbound.Peer.ID != "group-1" {
		t.Fatalf("inbound.Peer = %+v, want group/group-1", inbound.Peer)
	}
}

func TestSendMedia_UploadsLocalFileAsBase64(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	tmpFile, err := os.CreateTemp(t.TempDir(), "qq-media-*.png")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer tmpFile.Close()

	content := []byte("local-image-data")
	if _, writeErr := tmpFile.Write(content); writeErr != nil {
		t.Fatalf("Write() error = %v", writeErr)
	}

	ref, err := store.Store(tmpFile.Name(), media.MediaMeta{
		Filename:    "reply.png",
		ContentType: "image/png",
	}, "qq:test")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("uploaded-file-info")}),
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("group-1", "group")
	ch.lastMsgID.Store("group-1", "msg-1")
	ch.msgSeqCounters.Store("group-1", new(atomic.Uint64))

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "group-1",
		Parts: []bus.MediaPart{{
			Type:    "image",
			Ref:     ref,
			Caption: "see https://example.com/image",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(api.transportCalls) != 1 {
		t.Fatalf("transportCalls = %d, want 1", len(api.transportCalls))
	}
	upload := api.transportCalls[0]
	if upload.method != "POST" {
		t.Fatalf("upload method = %q, want POST", upload.method)
	}
	if upload.url != "https://api.sgroup.qq.com/v2/groups/group-1/files" {
		t.Fatalf("upload url = %q", upload.url)
	}
	if upload.body.URL != "" {
		t.Fatalf("upload URL = %q, want empty", upload.body.URL)
	}
	wantBase64 := base64.StdEncoding.EncodeToString(content)
	if upload.body.FileData != wantBase64 {
		t.Fatalf("upload file_data = %q, want %q", upload.body.FileData, wantBase64)
	}
	if upload.body.FileType != 1 {
		t.Fatalf("upload file_type = %d, want 1", upload.body.FileType)
	}

	if len(api.groupMessages) != 1 {
		t.Fatalf("groupMessages = %d, want 1", len(api.groupMessages))
	}
	msg, ok := api.groupMessages[0].(*dto.MessageToCreate)
	if !ok {
		t.Fatalf("groupMessages[0] type = %T, want *dto.MessageToCreate", api.groupMessages[0])
	}
	if msg.MsgType != dto.RichMediaMsg {
		t.Fatalf("msg.MsgType = %d, want %d", msg.MsgType, dto.RichMediaMsg)
	}
	if msg.MsgID != "msg-1" {
		t.Fatalf("msg.MsgID = %q, want msg-1", msg.MsgID)
	}
	if msg.MsgSeq != 1 {
		t.Fatalf("msg.MsgSeq = %d, want 1", msg.MsgSeq)
	}
	if msg.Content != "see https://example。com/image" {
		t.Fatalf("msg.Content = %q", msg.Content)
	}
	if msg.Media == nil || string(msg.Media.FileInfo) != "uploaded-file-info" {
		t.Fatalf("msg.Media.FileInfo = %q, want uploaded-file-info", string(msg.Media.FileInfo))
	}
}

func TestSendMedia_AudioAt60SecondsUsesVoiceUpload(t *testing.T) {
	assertAudioWAVUploadType(t, 60*time.Second, 3)
}

func TestSendMedia_AudioOver60SecondsFallsBackToFileUpload(t *testing.T) {
	assertAudioWAVUploadType(t, 61*time.Second, 4)
}

func assertAudioWAVUploadType(t *testing.T, duration time.Duration, wantFileType uint64) {
	t.Helper()

	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	localPath := writeWAVFile(t, t.TempDir(), "voice.wav", duration)
	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "voice.wav",
		ContentType: "audio/wav",
	}, "qq:test")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("file-info")}),
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("group-1", "group")

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "group-1",
		Parts: []bus.MediaPart{{
			Type: "audio",
			Ref:  ref,
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(api.transportCalls) != 1 {
		t.Fatalf("transportCalls = %d, want 1", len(api.transportCalls))
	}
	if api.transportCalls[0].body.FileType != wantFileType {
		t.Fatalf("upload file_type = %d, want %d", api.transportCalls[0].body.FileType, wantFileType)
	}
}

func TestSendMedia_RemoteAudioFallsBackToFileUpload(t *testing.T) {
	messageBus := bus.NewMessageBus()
	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("remote-file-info")}),
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.chatType.Store("user-1", "direct")

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "user-1",
		Parts: []bus.MediaPart{{
			Type: "audio",
			Ref:  "https://cdn.example.com/voice.ogg",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(api.transportCalls) != 1 {
		t.Fatalf("transportCalls = %d, want 1", len(api.transportCalls))
	}
	if api.transportCalls[0].body.FileType != 4 {
		t.Fatalf("upload file_type = %d, want 4", api.transportCalls[0].body.FileType)
	}
}

func TestSendMedia_LocalAudioWithUnknownDurationFallsBackToFileUpload(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	localPath := writeTempFile(t, t.TempDir(), "voice.mp3", []byte("not-a-real-mp3"))
	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "voice.mp3",
		ContentType: "audio/mpeg",
	}, "qq:test")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("file-info")}),
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("group-1", "group")

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "group-1",
		Parts: []bus.MediaPart{{
			Type: "audio",
			Ref:  ref,
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(api.transportCalls) != 1 {
		t.Fatalf("transportCalls = %d, want 1", len(api.transportCalls))
	}
	if api.transportCalls[0].body.FileType != 4 {
		t.Fatalf("upload file_type = %d, want 4", api.transportCalls[0].body.FileType)
	}
}

func TestSendMedia_UsesRemoteURLUploadForC2C(t *testing.T) {
	messageBus := bus.NewMessageBus()
	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("remote-file-info")}),
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.chatType.Store("user-1", "direct")

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "user-1",
		Parts: []bus.MediaPart{{
			Type: "file",
			Ref:  "https://cdn.example.com/report.pdf",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(api.transportCalls) != 1 {
		t.Fatalf("transportCalls = %d, want 1", len(api.transportCalls))
	}
	upload := api.transportCalls[0]
	if upload.url != "https://api.sgroup.qq.com/v2/users/user-1/files" {
		t.Fatalf("upload url = %q", upload.url)
	}
	if upload.body.URL != "https://cdn.example.com/report.pdf" {
		t.Fatalf("upload URL = %q", upload.body.URL)
	}
	if upload.body.FileData != "" {
		t.Fatalf("upload file_data = %q, want empty", upload.body.FileData)
	}
	if upload.body.FileType != 4 {
		t.Fatalf("upload file_type = %d, want 4", upload.body.FileType)
	}
	if upload.body.FileName != "report.pdf" {
		t.Fatalf("upload file_name = %q, want report.pdf", upload.body.FileName)
	}

	if len(api.c2cMessages) != 1 {
		t.Fatalf("c2cMessages = %d, want 1", len(api.c2cMessages))
	}
	msg, ok := api.c2cMessages[0].(*dto.MessageToCreate)
	if !ok {
		t.Fatalf("c2cMessages[0] type = %T, want *dto.MessageToCreate", api.c2cMessages[0])
	}
	if msg.MsgType != dto.RichMediaMsg {
		t.Fatalf("msg.MsgType = %d, want %d", msg.MsgType, dto.RichMediaMsg)
	}
	if msg.Media == nil || string(msg.Media.FileInfo) != "remote-file-info" {
		t.Fatalf("msg.Media.FileInfo = %q, want remote-file-info", string(msg.Media.FileInfo))
	}
}

func TestSendMedia_LocalFileUploadIncludesStoredFilename(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	localPath := writeTempFile(t, t.TempDir(), "report.pdf", []byte("fake-pdf"))
	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "report.pdf",
		ContentType: "application/pdf",
	}, "qq:test")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("local-file-info")}),
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("user-1", "direct")

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "user-1",
		Parts: []bus.MediaPart{{
			Type: "file",
			Ref:  ref,
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(api.transportCalls) != 1 {
		t.Fatalf("transportCalls = %d, want 1", len(api.transportCalls))
	}
	upload := api.transportCalls[0]
	if upload.body.FileType != 4 {
		t.Fatalf("upload file_type = %d, want 4", upload.body.FileType)
	}
	if upload.body.FileName != "report.pdf" {
		t.Fatalf("upload file_name = %q, want report.pdf", upload.body.FileName)
	}
	if upload.body.FileData == "" {
		t.Fatal("upload file_data = empty, want base64 payload")
	}
}

func TestSendMedia_ReturnsSendFailedWithoutMediaStore(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         &fakeQQAPI{},
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.chatType.Store("group-1", "group")

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "group-1",
		Parts: []bus.MediaPart{{
			Type: "image",
			Ref:  "media://missing",
		}},
	})
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
	}
}

func TestSendMedia_ReturnsSendFailedWhenLocalFileExceedsBase64MiBLimit(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	tmpFile, err := os.CreateTemp(t.TempDir(), "qq-media-too-large-*.bin")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer tmpFile.Close()

	content := make([]byte, bytesPerMiB+1)
	if _, writeErr := tmpFile.Write(content); writeErr != nil {
		t.Fatalf("Write() error = %v", writeErr)
	}

	ref, err := store.Store(tmpFile.Name(), media.MediaMeta{
		Filename:    "large.bin",
		ContentType: "application/octet-stream",
	}, "qq:test")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	api := &fakeQQAPI{}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		config: config.QQConfig{
			MaxBase64FileSizeMiB: 1,
		},
		api:   api,
		dedup: make(map[string]time.Time),
		done:  make(chan struct{}),
		ctx:   context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("group-1", "group")

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "group-1",
		Parts: []bus.MediaPart{{
			Type: "file",
			Ref:  ref,
		}},
	})
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
	}
	if len(api.transportCalls) != 0 {
		t.Fatalf("transportCalls = %d, want 0", len(api.transportCalls))
	}
}

// buildTwoPartMediaMessage stores two distinct local files and returns an
// OutboundMediaMessage referencing both, for the sentCount>0 partial-success
// regression tests below.
func buildTwoPartMediaMessage(t *testing.T, store *media.FileMediaStore, chatID string) bus.OutboundMediaMessage {
	t.Helper()

	tmpDir := t.TempDir()
	parts := make([]bus.MediaPart, 0, 2)
	for _, name := range []string{"a.png", "b.png"} {
		localPath := writeTempFile(t, tmpDir, name, []byte("fake-image-data-"+name))
		ref, err := store.Store(localPath, media.MediaMeta{
			Filename:    name,
			ContentType: "image/png",
		}, "qq:test")
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}
		parts = append(parts, bus.MediaPart{Type: "image", Ref: ref})
	}

	return bus.OutboundMediaMessage{ChatID: chatID, Parts: parts}
}

// TestSendMedia_MidLoopUploadFailureAfterPartialSuccessIsPermanent is the
// CRITICAL sendfile-fix review regression test: part 1 fully uploads AND
// posts successfully, then part 2's upload (Transport) call fails with a
// genuine API error. Before the fix this returned a bare channels.ErrTemporary
// unconditionally, so Manager.sendMediaWithRetry would retry the whole
// message — re-uploading and re-posting part 1, duplicating it for the user.
// The fix must classify this permanent (channels.ErrSendFailed) instead.
func TestSendMedia_MidLoopUploadFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("uploaded-file-info")}),
		transportErrs: []error{nil, errors.New("network exploded")},
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("group-1", "group")

	err := ch.SendMedia(context.Background(), buildTwoPartMediaMessage(t, store, "group-1"))

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from part 2's upload failure")
	}
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed (permanent, part 1 already delivered)", err)
	}
	if errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrTemporary "+
			"(ambiguous retry classification)", err)
	}
	if len(api.transportCalls) != 2 {
		t.Fatalf("transportCalls = %d, want 2 (part 1 upload, part 2 upload attempt)", len(api.transportCalls))
	}
	if len(api.groupMessages) != 1 {
		t.Fatalf("groupMessages = %d, want 1 (only part 1 reached the post step)", len(api.groupMessages))
	}
}

// TestSendMedia_MidLoopPostFailureAfterPartialSuccessIsPermanent is the same
// CRITICAL regression as above, but the failure happens one step later: part
// 2's file has already been uploaded to QQ's CDN (Transport succeeds) and
// only the follow-up PostGroupMessage/PostC2CMessage call fails. Before the
// fix this also returned a bare channels.ErrTemporary unconditionally.
func TestSendMedia_MidLoopPostFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	api := &fakeQQAPI{
		transportResp: mustJSON(t, dto.Message{FileInfo: []byte("uploaded-file-info")}),
		groupErrs:     []error{nil, errors.New("post rejected")},
	}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("group-1", "group")

	err := ch.SendMedia(context.Background(), buildTwoPartMediaMessage(t, store, "group-1"))

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from part 2's post failure")
	}
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed (permanent, part 1 already delivered)", err)
	}
	if errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrTemporary "+
			"(ambiguous retry classification)", err)
	}
	if len(api.transportCalls) != 2 {
		t.Fatalf("transportCalls = %d, want 2 (both parts fully uploaded)", len(api.transportCalls))
	}
	if len(api.groupMessages) != 2 {
		t.Fatalf("groupMessages = %d, want 2 (part 1 succeeded, part 2 attempted then failed)",
			len(api.groupMessages))
	}
}

// TestSendMedia_UploadFailureWithNothingSentIsTemporary is the control case:
// when the very first part's upload fails (sentCount == 0, nothing has
// reached QQ yet), the failure must keep its ErrTemporary classification so
// sendMediaWithRetry still retries — a bare retry here cannot duplicate
// anything.
func TestSendMedia_UploadFailureWithNothingSentIsTemporary(t *testing.T) {
	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	localPath := writeTempFile(t, t.TempDir(), "a.png", []byte("fake-image-data"))
	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "a.png",
		ContentType: "image/png",
	}, "qq:test")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	api := &fakeQQAPI{transportErr: errors.New("network exploded")}
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         api,
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(store)
	ch.chatType.Store("group-1", "group")

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "group-1",
		Parts:  []bus.MediaPart{{Type: "image", Ref: ref}},
	})

	if !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v, want ErrTemporary (nothing delivered yet, retry is safe)", err)
	}
	if errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrSendFailed "+
			"(ambiguous retry classification)", err)
	}
}

// TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning is the FR-028a
// review regression test: a media ref denied by the caller-workspace
// membership guard (media.IsCallerWorkspaceDenied) must get its own
// distinct WARN log line naming it a workspace-boundary denial. QQ's
// buildMediaUpload hard-returns the raw error (wrapped in
// channels.ErrSendFailed by uploadMedia's caller) and had no logging at
// this exact call site before this fix, so this only adds the new WARN
// line — there is no prior generic message to distinguish it from here.
func TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "qq-media.log")
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

	messageBus := bus.NewMessageBus()
	ch := &QQChannel{
		BaseChannel: channels.NewBaseChannel("qq", nil, messageBus, nil),
		api:         &fakeQQAPI{},
		dedup:       make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         context.Background(),
	}
	ch.SetRunning(true)
	ch.SetMediaStore(media.NewFileMediaStore())
	ch.chatType.Store("group-1", "group")

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "group-1",
		// WorkspaceID deliberately empty: a workspace-prefixed ref with no
		// caller-workspace context trips the FR-028a guard
		// (ErrWorkspaceContextRequired) before any media store provider is
		// even consulted.
		Parts: []bus.MediaPart{{Type: "image", Ref: "media://workspace/other-ws/some-id"}},
	})
	if err == nil {
		t.Fatal("SendMedia() error = nil, want error for a denied workspace ref")
	}
	if !errors.Is(err, channels.ErrSendFailed) {
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

type fakeQQAPI struct {
	transportResp  []byte
	transportErr   error
	groupErr       error
	c2cErr         error
	transportCalls []fakeTransportCall
	groupMessages  []dto.APIMessage
	c2cMessages    []dto.APIMessage

	// transportErrs and groupErrs allow injecting a failure on a specific
	// call index (0-based), overriding transportErr/groupErr for that call
	// only, so tests can simulate "part 1 succeeds, part 2 fails" without
	// affecting every call uniformly.
	transportErrs []error
	groupErrs     []error
}

type fakeTransportCall struct {
	method string
	url    string
	body   qqMediaUpload
}

func (f *fakeQQAPI) WS(
	context.Context,
	map[string]string,
	string,
) (*dto.WebsocketAP, error) {
	return nil, nil
}

func (f *fakeQQAPI) PostGroupMessage(
	_ context.Context,
	_ string,
	msg dto.APIMessage,
	_ ...options.Option,
) (*dto.Message, error) {
	idx := len(f.groupMessages)
	f.groupMessages = append(f.groupMessages, msg)
	if idx < len(f.groupErrs) && f.groupErrs[idx] != nil {
		return &dto.Message{}, f.groupErrs[idx]
	}
	return &dto.Message{}, f.groupErr
}

func (f *fakeQQAPI) PostC2CMessage(
	_ context.Context,
	_ string,
	msg dto.APIMessage,
	_ ...options.Option,
) (*dto.Message, error) {
	f.c2cMessages = append(f.c2cMessages, msg)
	return &dto.Message{}, f.c2cErr
}

func (f *fakeQQAPI) Transport(_ context.Context, method, url string, body any) ([]byte, error) {
	upload, ok := body.(*qqMediaUpload)
	if !ok {
		return nil, errors.New("unexpected transport body type")
	}
	idx := len(f.transportCalls)
	f.transportCalls = append(f.transportCalls, fakeTransportCall{
		method: method,
		url:    url,
		body:   *upload,
	})
	if idx < len(f.transportErrs) && f.transportErrs[idx] != nil {
		return f.transportResp, f.transportErrs[idx]
	}
	return f.transportResp, f.transportErr
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return b
}

func waitInboundMessage(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for inbound message")
		case inbound, ok := <-messageBus.InboundChan():
			if !ok {
				t.Fatal("expected inbound message")
			}
			return inbound
		}
	}
}

func writeTempFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()

	path := dir + "/" + name
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func writeWAVFile(t *testing.T, dir, name string, duration time.Duration) string {
	t.Helper()

	const (
		sampleRate    = 8000
		numChannels   = 1
		bitsPerSample = 8
	)

	dataSize := uint32(duration / time.Second * sampleRate * numChannels * (bitsPerSample / 8))
	byteRate := uint32(sampleRate * numChannels * (bitsPerSample / 8))
	blockAlign := uint16(numChannels * (bitsPerSample / 8))

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	if err := binary.Write(&buf, binary.LittleEndian, uint32(36)+dataSize); err != nil {
		t.Fatalf("binary.Write(riff size) error = %v", err)
	}
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	if err := binary.Write(&buf, binary.LittleEndian, uint32(16)); err != nil {
		t.Fatalf("binary.Write(fmt chunk size) error = %v", err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint16(1)); err != nil {
		t.Fatalf("binary.Write(audio format) error = %v", err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint16(numChannels)); err != nil {
		t.Fatalf("binary.Write(channels) error = %v", err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint32(sampleRate)); err != nil {
		t.Fatalf("binary.Write(sample rate) error = %v", err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, byteRate); err != nil {
		t.Fatalf("binary.Write(byte rate) error = %v", err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, blockAlign); err != nil {
		t.Fatalf("binary.Write(block align) error = %v", err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample)); err != nil {
		t.Fatalf("binary.Write(bits per sample) error = %v", err)
	}
	buf.WriteString("data")
	if err := binary.Write(&buf, binary.LittleEndian, dataSize); err != nil {
		t.Fatalf("binary.Write(data size) error = %v", err)
	}
	buf.Write(make([]byte, dataSize))

	return writeTempFile(t, dir, name, buf.Bytes())
}
