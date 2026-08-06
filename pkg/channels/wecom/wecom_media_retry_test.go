package wecom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// buildWeComTwoPartMediaMessage stores two small local JPEGs in the given
// media store and returns an OutboundMediaMessage referencing both, mirroring
// qq_test.go's buildTwoPartMediaMessage helper.
func buildWeComTwoPartMediaMessage(t *testing.T, store *media.FileMediaStore, chatID string) bus.OutboundMediaMessage {
	t.Helper()

	tmpDir := t.TempDir()
	parts := make([]bus.MediaPart, 0, 2)
	for i, name := range []string{"a.jpg", "b.jpg"} {
		data := wecomTestJPEGData(t)
		localPath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(localPath, data, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		ref, err := store.Store(localPath, media.MediaMeta{
			Filename:      name,
			ContentType:   "image/jpeg",
			Source:        "test",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, fmt.Sprintf("wecom:test:%d", i))
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}
		parts = append(parts, bus.MediaPart{Type: "image", Ref: ref, Filename: name, ContentType: "image/jpeg"})
	}

	return bus.OutboundMediaMessage{Channel: "wecom", ChatID: chatID, Parts: parts}
}

// TestSendMedia_MidLoopSendFailureAfterPartialSuccessIsPermanent is the
// CRITICAL sendfile-fix review regression test for WeCom: part 1 uploads AND
// sends successfully (its media_id reaches the platform via wecomCmdSendMsg),
// then part 2 uploads successfully too, but the follow-up wecomCmdSendMsg
// send fails with exactly the kind of error writeAndWaitAck returns in
// production — an ack timeout, unconditionally wrapped in
// channels.ErrTemporary (see wecom.go's writeAndWaitAck, "timeout waiting for
// WeCom ack" branch). Before the fix, SendMedia propagated that bare
// ErrTemporary straight through with no awareness that part 1 already sent,
// so Manager.sendMediaWithRetry would retry the WHOLE message — re-uploading
// and re-sending part 1's image, duplicating it for the user. The fix must
// classify this permanent (channels.ErrSendFailed) instead.
func TestSendMedia_MidLoopSendFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	initCalls := 0
	finishCalls := 0
	sendMsgCalls := 0
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		switch cmd.Cmd {
		case wecomCmdUploadMediaInit:
			initCalls++
			return wecomTestAck(wecomUploadMediaInitResponse{UploadID: fmt.Sprintf("upload-%d", initCalls)}), nil
		case wecomCmdUploadMediaChunk:
			return wecomTestAck(nil), nil
		case wecomCmdUploadMediaEnd:
			finishCalls++
			return wecomTestAck(wecomUploadMediaFinishResponse{
				Type:    "image",
				MediaID: fmt.Sprintf("media-%d", finishCalls),
			}), nil
		case wecomCmdSendMsg:
			sendMsgCalls++
			if sendMsgCalls == 2 {
				return wecomEnvelope{}, fmt.Errorf("%w: timeout waiting for WeCom ack", channels.ErrTemporary)
			}
			return wecomTestAck(nil), nil
		default:
			return wecomTestAck(nil), nil
		}
	}

	msg := buildWeComTwoPartMediaMessage(t, store, "chat-1")
	err := ch.SendMedia(context.Background(), msg)

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from part 2's send failure")
	}
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed (permanent, part 1 already delivered)", err)
	}
	if errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrTemporary "+
			"(ambiguous retry classification)", err)
	}
	if sendMsgCalls != 2 {
		t.Fatalf("wecomCmdSendMsg calls = %d, want 2 (part 1 succeeded, part 2 attempted then failed)", sendMsgCalls)
	}
	if finishCalls != 2 {
		t.Fatalf("wecomCmdUploadMediaEnd calls = %d, want 2 (both parts fully uploaded before part 2's send failed)",
			finishCalls)
	}
}

// TestSendMedia_FirstPartSendFailureWithNothingSentIsTemporary is the control
// case: the ONLY part's upload succeeds but its send (wecomCmdSendMsg) fails
// before anything has reached the platform (sentCount == 0). The failure must
// keep its ErrTemporary classification so sendMediaWithRetry still retries —
// a bare retry here cannot duplicate anything because nothing was delivered.
func TestSendMedia_FirstPartSendFailureWithNothingSentIsTemporary(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		switch cmd.Cmd {
		case wecomCmdUploadMediaInit:
			return wecomTestAck(wecomUploadMediaInitResponse{UploadID: "upload-1"}), nil
		case wecomCmdUploadMediaChunk:
			return wecomTestAck(nil), nil
		case wecomCmdUploadMediaEnd:
			return wecomTestAck(wecomUploadMediaFinishResponse{Type: "image", MediaID: "media-1"}), nil
		case wecomCmdSendMsg:
			return wecomEnvelope{}, fmt.Errorf("%w: timeout waiting for WeCom ack", channels.ErrTemporary)
		default:
			return wecomTestAck(nil), nil
		}
	}

	msg := buildWeComTwoPartMediaMessage(t, store, "chat-1")
	msg.Parts = msg.Parts[:1] // only one part: failure happens with sentCount == 0

	err := ch.SendMedia(context.Background(), msg)

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from the send failure")
	}
	if !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v, want ErrTemporary (nothing sent yet, safe to retry)", err)
	}
	if errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v must not be classified permanent when nothing was sent", err)
	}
}

// TestSendMedia_MidLoopCaptionFailureAfterPartialSuccessIsPermanent covers the
// empty-ref (caption-only) branch of SendMedia's loop: part 1 is a real image
// that sends successfully, part 2 has no media ref (only caption text) and
// its sendActivePush call fails. Before the fix this returned a bare
// ErrTemporary regardless of part 1 already having been delivered.
func TestSendMedia_MidLoopCaptionFailureAfterPartialSuccessIsPermanent(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	sendMsgCalls := 0
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		switch cmd.Cmd {
		case wecomCmdUploadMediaInit:
			return wecomTestAck(wecomUploadMediaInitResponse{UploadID: "upload-1"}), nil
		case wecomCmdUploadMediaChunk:
			return wecomTestAck(nil), nil
		case wecomCmdUploadMediaEnd:
			return wecomTestAck(wecomUploadMediaFinishResponse{Type: "image", MediaID: "media-1"}), nil
		case wecomCmdSendMsg:
			sendMsgCalls++
			if sendMsgCalls == 2 {
				return wecomEnvelope{}, fmt.Errorf("%w: timeout waiting for WeCom ack", channels.ErrTemporary)
			}
			return wecomTestAck(nil), nil
		default:
			return wecomTestAck(nil), nil
		}
	}

	imagePart := buildWeComTwoPartMediaMessage(t, store, "chat-1").Parts[0]
	msg := bus.OutboundMediaMessage{
		Channel: "wecom",
		ChatID:  "chat-1",
		Parts: []bus.MediaPart{
			imagePart,
			{Caption: "just a caption, no attachment"},
		},
	}

	err := ch.SendMedia(context.Background(), msg)

	if err == nil {
		t.Fatal("SendMedia() error = nil, want error from part 2's caption push failure")
	}
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed (permanent, part 1 already delivered)", err)
	}
	if errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v must not ALSO match ErrTemporary "+
			"(ambiguous retry classification)", err)
	}
	if sendMsgCalls != 2 {
		t.Fatalf("wecomCmdSendMsg calls = %d, want 2 (part 1's image send + part 2's caption push attempt)",
			sendMsgCalls)
	}
}
