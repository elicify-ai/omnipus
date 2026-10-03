package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
)

type recordingCancelInterceptor struct {
	calls int

	// redirectCalls counts RequestRedirectByChannelChat crossings, kept
	// separate so the cancel-path assertions on calls keep their exact
	// meaning: every non-redirect WeCom test asserts it stays 0, and the
	// /stop-redirect adapter-path test asserts exactly 1 with the recorded
	// arguments below.
	redirectCalls int

	// Arguments of the (single allowed) RequestRedirectByChannelChat
	// crossing, recorded so the /stop-redirect adapter-path test can assert
	// the D9 primitive received the adapter's exact
	// (channelName, chatID, senderID, instruction) tuple — not merely that
	// some call happened.
	redirectChannel     string
	redirectChatID      string
	redirectSenderID    string
	redirectInstruction string
}

func (r *recordingCancelInterceptor) RequestCancelByChannelChat(
	context.Context,
	string,
	string,
	string,
) (bool, bool, error) {
	r.calls++
	return true, false, nil
}

// RequestRedirectByChannelChat satisfies the D9-grown CancelInterceptor
// interface (pkg/channels/cancelparse.go) and RECORDS each crossing with its
// full argument tuple: TestDispatchIncoming_StopRedirectAdapterPath asserts
// exactly one call carrying the adapter's (channelName, chatID, senderID,
// instruction). It still answers with a VISIBLE error — never a silent
// success or no-op — so a redirect ack can never read as a wired redirect.
func (r *recordingCancelInterceptor) RequestRedirectByChannelChat(
	_ context.Context,
	channelName, chatID, senderID, instruction string,
) error {
	r.redirectCalls++
	r.redirectChannel = channelName
	r.redirectChatID = chatID
	r.redirectSenderID = senderID
	r.redirectInstruction = instruction
	return errors.New("recordingCancelInterceptor: redirect crossing recorded; this fake wires no real redirect")
}

func TestDispatchIncoming_DeniedSenderHasNoSideEffects(t *testing.T) {
	messageBus := bus.NewMessageBus()

	const secretRef = "WECOM_TEST_SECRET"
	bundle := credentials.SecretBundle{
		credentials.SecretRef(secretRef): "secret-1",
	}
	ch, err := NewChannel(config.WeComConfig{
		BotID:     "bot-1",
		SecretRef: secretRef,
		AllowFrom: []string{"allowed-user"},
	}, bundle, messageBus)
	if err != nil {
		t.Fatalf("NewChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	ch.routes = newReqIDStore(filepath.Join(t.TempDir(), "reqids.json"))
	ch.SetRunning(true)

	interceptor := &recordingCancelInterceptor{}
	ch.SetCancelInterceptor(interceptor)
	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		return wecomTestAck(nil), nil
	}

	msg := wecomIncomingMessage{
		MsgID:    "msg-denied",
		ChatID:   "chat-denied",
		ChatType: "direct",
		MsgType:  "text",
		Text: &struct {
			Content string `json:"content"`
		}{Content: "/cancel"},
	}
	msg.From.UserID = "denied-user"

	if err := ch.dispatchIncoming("req-denied", msg); err != nil {
		t.Fatalf("dispatchIncoming() error = %v", err)
	}
	_, hasTurn := ch.getTurn("chat-denied")
	_, hasRoute := ch.routes.Get("chat-denied")
	if interceptor.calls != 0 || len(commands) != 0 || hasTurn || hasRoute {
		t.Fatalf(
			"denied sender side effects: cancel calls=%d, commands=%d, turn=%v, route=%v; want all zero/false",
			interceptor.calls,
			len(commands),
			hasTurn,
			hasRoute,
		)
	}
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("denied sender published inbound message: %+v", inbound)
	default:
	}
}

func TestDispatchIncoming_DeniedSenderDoesNotDownloadMedia(t *testing.T) {
	messageBus := bus.NewMessageBus()

	const secretRef = "WECOM_TEST_SECRET"
	bundle := credentials.SecretBundle{
		credentials.SecretRef(secretRef): "secret-1",
	}
	ch, err := NewChannel(config.WeComConfig{
		BotID:     "bot-1",
		SecretRef: secretRef,
		AllowFrom: []string{"allowed-user"},
	}, bundle, messageBus)
	if err != nil {
		t.Fatalf("NewChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	ch.SetMediaStore(mediatest.NewFileMediaStore(t))

	mediaRequests := 0
	ch.mediaClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			mediaRequests++
			return nil, errors.New("unexpected media request")
		}),
	}

	msg := wecomIncomingMessage{
		MsgID:    "msg-denied-media",
		ChatID:   "chat-denied",
		ChatType: "direct",
		MsgType:  "image",
		Image: &struct {
			URL    string `json:"url"`
			AESKey string `json:"aeskey,omitempty"`
		}{URL: "https://wecom.example/media"},
	}
	msg.From.UserID = "denied-user"

	err = ch.dispatchIncoming("req-denied-media", msg)
	if mediaRequests != 0 {
		t.Fatalf("denied sender media requests = %d, want 0", mediaRequests)
	}
	if err != nil {
		t.Fatalf("dispatchIncoming() error = %v", err)
	}
}

func TestDispatchIncoming_UsesActualChatIDAndStoresReqIDRoute(t *testing.T) {
	messageBus := bus.NewMessageBus()
	ch := newTestWeComChannel(t, messageBus)

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		return wecomTestAck(nil), nil
	}

	msg := wecomIncomingMessage{
		MsgID:    "msg-1",
		ChatID:   "chat-1",
		ChatType: "direct",
		MsgType:  "text",
		Text: &struct {
			Content string `json:"content"`
		}{Content: "hello"},
	}
	msg.From.UserID = "user-1"

	if err := ch.dispatchIncoming("req-1", msg); err != nil {
		t.Fatalf("dispatchIncoming() error = %v", err)
	}

	select {
	case inbound := <-messageBus.InboundChan():
		if inbound.ChatID != "chat-1" {
			t.Fatalf("inbound ChatID = %q, want chat-1", inbound.ChatID)
		}
		if inbound.MessageID != "msg-1" {
			t.Fatalf("inbound MessageID = %q, want msg-1", inbound.MessageID)
		}
		if inbound.Peer.ID != "chat-1" {
			t.Fatalf("inbound Peer.ID = %q, want chat-1", inbound.Peer.ID)
		}
		if inbound.Metadata["req_id"] != "req-1" {
			t.Fatalf("inbound req_id = %q, want req-1", inbound.Metadata["req_id"])
		}
	default:
		t.Fatal("expected inbound message to be published")
	}

	turn, ok := ch.getTurn("chat-1")
	if !ok {
		t.Fatal("expected queued turn for chat-1")
	}
	if turn.ReqID != "req-1" {
		t.Fatalf("turn.ReqID = %q, want req-1", turn.ReqID)
	}

	route, ok := ch.routes.Get("chat-1")
	if !ok {
		t.Fatal("expected persisted route for chat-1")
	}
	if route.ReqID != "req-1" || route.ChatType != 1 {
		t.Fatalf("route = %+v", route)
	}

	if len(commands) != 1 {
		t.Fatalf("expected 1 opening command, got %d", len(commands))
	}
	if commands[0].Cmd != wecomCmdRespondMsg {
		t.Fatalf("opening command = %q, want %q", commands[0].Cmd, wecomCmdRespondMsg)
	}
	if commands[0].Headers.ReqID != "req-1" {
		t.Fatalf("opening req_id = %q, want req-1", commands[0].Headers.ReqID)
	}
}

func TestNewChannel_DoesNotRegisterMessageSplitLimit(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	if got := ch.MaxMessageLength(); got != 0 {
		t.Fatalf("MaxMessageLength() = %d, want 0", got)
	}
}

func TestBeginStream_UpdateAndFinalize(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)
	ch.queueTurn("chat-1", wecomTurn{
		ReqID:     "req-1",
		ChatID:    "chat-1",
		ChatType:  1,
		StreamID:  "stream-1",
		CreatedAt: time.Now(),
	})

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		return wecomTestAck(nil), nil
	}

	streamer, err := ch.BeginStream(context.Background(), "chat-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	if err := streamer.Update(context.Background(), "draft"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := streamer.Finalize(context.Background(), "final"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	if len(commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(commands))
	}
	for i, wantFinish := range []bool{false, true} {
		if commands[i].Cmd != wecomCmdRespondMsg {
			t.Fatalf("command[%d].Cmd = %q, want %q", i, commands[i].Cmd, wecomCmdRespondMsg)
		}
		body, ok := commands[i].Body.(wecomRespondMsgBody)
		if !ok {
			t.Fatalf("command[%d] body type = %T", i, commands[i].Body)
		}
		if body.Stream == nil {
			t.Fatalf("command[%d] missing stream body", i)
		}
		if body.Stream.ID != "stream-1" {
			t.Fatalf("command[%d] stream id = %q, want stream-1", i, body.Stream.ID)
		}
		if body.Stream.Finish != wantFinish {
			t.Fatalf("command[%d] finish = %v, want %v", i, body.Stream.Finish, wantFinish)
		}
	}
	body0, ok := commands[0].Body.(wecomRespondMsgBody)
	if !ok {
		t.Fatalf("commands[0].Body has unexpected type %T, want wecomRespondMsgBody", commands[0].Body)
	}
	if body0.Stream.Content != "draft" {
		t.Fatalf("update content = %q, want draft", body0.Stream.Content)
	}
	body1, ok := commands[1].Body.(wecomRespondMsgBody)
	if !ok {
		t.Fatalf("commands[1].Body has unexpected type %T, want wecomRespondMsgBody", commands[1].Body)
	}
	if body1.Stream.Content != "final" {
		t.Fatalf("final content = %q, want final", body1.Stream.Content)
	}
	if _, ok := ch.getTurn("chat-1"); ok {
		t.Fatal("expected turn to be consumed after Finalize")
	}
}

func TestSend_StreamFailureFallsBackToActualChatID(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)
	ch.queueTurn("chat-1", wecomTurn{
		ReqID:     "req-1",
		ChatID:    "chat-1",
		ChatType:  1,
		StreamID:  "stream-1",
		CreatedAt: time.Now(),
	})
	ch.queueTurn("chat-1", wecomTurn{
		ReqID:     "req-2",
		ChatID:    "chat-1",
		ChatType:  1,
		StreamID:  "stream-2",
		CreatedAt: time.Now(),
	})
	if err := ch.routes.Put("chat-1", "req-2", 1, time.Hour); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		if len(commands) == 1 && cmd.Cmd == wecomCmdRespondMsg {
			return wecomEnvelope{}, errors.New("stream send failed")
		}
		return wecomTestAck(nil), nil
	}

	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel: "wecom",
		ChatID:  "chat-1",
		Content: "hello",
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if len(commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(commands))
	}
	if commands[0].Cmd != wecomCmdRespondMsg || commands[0].Headers.ReqID != "req-1" {
		t.Fatalf("first command = %+v", commands[0])
	}
	if commands[1].Cmd != wecomCmdSendMsg {
		t.Fatalf("second command = %q, want %q", commands[1].Cmd, wecomCmdSendMsg)
	}
	body, ok := commands[1].Body.(wecomSendMsgBody)
	if !ok {
		t.Fatalf("unexpected send body type %T", commands[1].Body)
	}
	if body.ChatID != "chat-1" {
		t.Fatalf("send chatid = %q, want chat-1", body.ChatID)
	}
	if body.ChatType != 1 {
		t.Fatalf("send chat_type = %d, want 1", body.ChatType)
	}

	nextTurn, ok := ch.getTurn("chat-1")
	if !ok {
		t.Fatal("expected second turn to remain queued")
	}
	if nextTurn.ReqID != "req-2" {
		t.Fatalf("next queued req_id = %q, want req-2", nextTurn.ReqID)
	}
}

func TestSend_DoesNotSplitStreamReply(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)
	ch.queueTurn("chat-1", wecomTurn{
		ReqID:     "req-1",
		ChatID:    "chat-1",
		ChatType:  1,
		StreamID:  "stream-1",
		CreatedAt: time.Now(),
	})

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		return wecomTestAck(nil), nil
	}

	content := strings.Repeat("\u4e2d", 30000)
	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel: "wecom",
		ChatID:  "chat-1",
		Content: content,
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if len(commands) != 1 {
		t.Fatalf("expected 1 stream command, got %d", len(commands))
	}
	body, ok := commands[0].Body.(wecomRespondMsgBody)
	if !ok {
		t.Fatalf("unexpected body type %T", commands[0].Body)
	}
	if body.Stream == nil || !body.Stream.Finish {
		t.Fatalf("stream body = %+v", body.Stream)
	}
	if body.Stream.Content != content {
		t.Fatalf("stream content length = %d, want %d", len(body.Stream.Content), len(content))
	}
}

func TestSend_DoesNotSplitActivePush(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		return wecomTestAck(nil), nil
	}

	content := strings.Repeat("a", 30000)
	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel: "wecom",
		ChatID:  "chat-1",
		Content: content,
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if len(commands) != 1 {
		t.Fatalf("expected 1 send command, got %d", len(commands))
	}
	if commands[0].Cmd != wecomCmdSendMsg {
		t.Fatalf("command = %q, want %q", commands[0].Cmd, wecomCmdSendMsg)
	}
	body, ok := commands[0].Body.(wecomSendMsgBody)
	if !ok {
		t.Fatalf("unexpected body type %T", commands[0].Body)
	}
	if body.Markdown == nil || body.Markdown.Content != content {
		t.Fatalf("markdown content length = %d, want %d", len(body.Markdown.Content), len(content))
	}
}

func TestSendMedia_SendsActiveImage(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)

	store := mediatest.NewFileMediaStore(t)
	ch.SetMediaStore(store)

	imageData := wecomTestJPEGData(t)
	imagePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imagePath, imageData, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ref, err := store.Store(imagePath, media.MediaMeta{
		Filename:      "photo.jpg",
		ContentType:   "image/jpeg",
		Source:        "test",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "scope-1")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		switch cmd.Cmd {
		case wecomCmdUploadMediaInit:
			return wecomTestAck(wecomUploadMediaInitResponse{UploadID: "upload-1"}), nil
		case wecomCmdUploadMediaEnd:
			return wecomTestAck(wecomUploadMediaFinishResponse{
				Type:    "image",
				MediaID: "media-1",
			}), nil
		default:
			return wecomTestAck(nil), nil
		}
	}

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "wecom",
		ChatID:  "chat-1",
		Parts: []bus.MediaPart{{
			Ref:         ref,
			Type:        "image",
			Filename:    "photo.jpg",
			ContentType: "image/jpeg",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(commands) != 4 {
		t.Fatalf("expected 4 commands, got %d", len(commands))
	}
	if commands[0].Cmd != wecomCmdUploadMediaInit {
		t.Fatalf("first command = %q, want %q", commands[0].Cmd, wecomCmdUploadMediaInit)
	}
	initBody, ok := commands[0].Body.(wecomUploadMediaInitBody)
	if !ok {
		t.Fatalf("unexpected init body type %T", commands[0].Body)
	}
	if initBody.Type != "image" || initBody.Filename != "photo.jpg" || initBody.TotalChunks != 1 {
		t.Fatalf("init body = %+v", initBody)
	}
	if commands[1].Cmd != wecomCmdUploadMediaChunk {
		t.Fatalf("second command = %q, want %q", commands[1].Cmd, wecomCmdUploadMediaChunk)
	}
	chunkBody, ok := commands[1].Body.(wecomUploadMediaChunkBody)
	if !ok {
		t.Fatalf("unexpected chunk body type %T", commands[1].Body)
	}
	if chunkBody.UploadID != "upload-1" || chunkBody.ChunkIndex != 0 || chunkBody.Base64Data == "" {
		t.Fatalf("chunk body = %+v", chunkBody)
	}
	if commands[2].Cmd != wecomCmdUploadMediaEnd {
		t.Fatalf("third command = %q, want %q", commands[2].Cmd, wecomCmdUploadMediaEnd)
	}
	if commands[3].Cmd != wecomCmdSendMsg {
		t.Fatalf("fourth command = %q, want %q", commands[3].Cmd, wecomCmdSendMsg)
	}

	body, ok := commands[3].Body.(wecomSendMsgBody)
	if !ok {
		t.Fatalf("unexpected send body type %T", commands[3].Body)
	}
	if body.MsgType != "image" || body.Image == nil {
		t.Fatalf("send body = %+v", body)
	}
	if body.ChatID != "chat-1" {
		t.Fatalf("send chatid = %q, want chat-1", body.ChatID)
	}
	if body.Image.MediaID != "media-1" {
		t.Fatalf("image media_id = %q, want media-1", body.Image.MediaID)
	}
}

func TestSendMedia_UsesTurnImageAndFinishesStream(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)

	store := mediatest.NewFileMediaStore(t)
	ch.SetMediaStore(store)

	imageData := wecomTestJPEGData(t)
	imagePath := filepath.Join(t.TempDir(), "reply.jpg")
	if err := os.WriteFile(imagePath, imageData, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ref, err := store.Store(imagePath, media.MediaMeta{
		Filename:      "reply.jpg",
		ContentType:   "image/jpeg",
		Source:        "test",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "scope-2")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	ch.queueTurn("chat-1", wecomTurn{
		ReqID:     "req-1",
		ChatID:    "chat-1",
		ChatType:  1,
		StreamID:  "stream-1",
		CreatedAt: time.Now(),
	})
	putErr := ch.routes.Put("chat-1", "req-1", 1, time.Hour)
	if putErr != nil {
		t.Fatalf("Put() error = %v", putErr)
	}

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		switch cmd.Cmd {
		case wecomCmdUploadMediaInit:
			return wecomTestAck(wecomUploadMediaInitResponse{UploadID: "upload-2"}), nil
		case wecomCmdUploadMediaEnd:
			return wecomTestAck(wecomUploadMediaFinishResponse{
				Type:    "image",
				MediaID: "media-2",
			}), nil
		default:
			return wecomTestAck(nil), nil
		}
	}

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "wecom",
		ChatID:  "chat-1",
		Parts: []bus.MediaPart{{
			Ref:         ref,
			Type:        "image",
			Filename:    "reply.jpg",
			ContentType: "image/jpeg",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(commands) != 5 {
		t.Fatalf("expected 5 commands, got %d", len(commands))
	}
	if commands[0].Cmd != wecomCmdUploadMediaInit {
		t.Fatalf("first command = %+v", commands[0])
	}
	if commands[1].Cmd != wecomCmdUploadMediaChunk {
		t.Fatalf("second command = %+v", commands[1])
	}
	if commands[2].Cmd != wecomCmdUploadMediaEnd {
		t.Fatalf("third command = %+v", commands[2])
	}
	if commands[3].Cmd != wecomCmdRespondMsg || commands[3].Headers.ReqID != "req-1" {
		t.Fatalf("fourth command = %+v", commands[3])
	}
	if commands[4].Cmd != wecomCmdRespondMsg || commands[4].Headers.ReqID != "req-1" {
		t.Fatalf("fifth command = %+v", commands[4])
	}

	imageBody, ok := commands[3].Body.(wecomRespondMsgBody)
	if !ok {
		t.Fatalf("unexpected image body type %T", commands[3].Body)
	}
	if imageBody.MsgType != "image" || imageBody.Image == nil {
		t.Fatalf("image body = %+v", imageBody)
	}
	if imageBody.Image.MediaID != "media-2" {
		t.Fatalf("image media_id = %q, want media-2", imageBody.Image.MediaID)
	}

	streamBody, ok := commands[4].Body.(wecomRespondMsgBody)
	if !ok {
		t.Fatalf("unexpected finish body type %T", commands[4].Body)
	}
	if streamBody.MsgType != "stream" || streamBody.Stream == nil || !streamBody.Stream.Finish {
		t.Fatalf("finish body = %+v", streamBody)
	}

	if _, ok := ch.getTurn("chat-1"); ok {
		t.Fatal("expected turn to be removed after media send")
	}
}

func TestSendMedia_SendsActiveFile(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	ch.SetRunning(true)

	store := mediatest.NewFileMediaStore(t)
	ch.SetMediaStore(store)

	filePath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ref, err := store.Store(filePath, media.MediaMeta{
		Filename:      "report.pdf",
		ContentType:   "application/pdf",
		Source:        "test",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "scope-3")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	var commands []wecomCommand
	ch.commandSend = func(cmd wecomCommand, _ time.Duration) (wecomEnvelope, error) {
		commands = append(commands, cmd)
		switch cmd.Cmd {
		case wecomCmdUploadMediaInit:
			return wecomTestAck(wecomUploadMediaInitResponse{UploadID: "upload-3"}), nil
		case wecomCmdUploadMediaEnd:
			return wecomTestAck(wecomUploadMediaFinishResponse{
				Type:    "file",
				MediaID: "media-3",
			}), nil
		default:
			return wecomTestAck(nil), nil
		}
	}

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "wecom",
		ChatID:  "chat-2",
		Parts: []bus.MediaPart{{
			Ref:         ref,
			Type:        "file",
			Filename:    "report.pdf",
			ContentType: "application/pdf",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	if len(commands) != 4 {
		t.Fatalf("expected 4 commands, got %d", len(commands))
	}
	if commands[0].Cmd != wecomCmdUploadMediaInit {
		t.Fatalf("first command = %q, want %q", commands[0].Cmd, wecomCmdUploadMediaInit)
	}
	initBody, ok := commands[0].Body.(wecomUploadMediaInitBody)
	if !ok {
		t.Fatalf("unexpected init body type %T", commands[0].Body)
	}
	if initBody.Type != "file" || initBody.Filename != "report.pdf" {
		t.Fatalf("init body = %+v", initBody)
	}
	if commands[1].Cmd != wecomCmdUploadMediaChunk {
		t.Fatalf("second command = %q, want %q", commands[1].Cmd, wecomCmdUploadMediaChunk)
	}
	if commands[2].Cmd != wecomCmdUploadMediaEnd {
		t.Fatalf("third command = %q, want %q", commands[2].Cmd, wecomCmdUploadMediaEnd)
	}
	if commands[3].Cmd != wecomCmdSendMsg {
		t.Fatalf("fourth command = %q, want %q", commands[3].Cmd, wecomCmdSendMsg)
	}

	body, ok := commands[3].Body.(wecomSendMsgBody)
	if !ok {
		t.Fatalf("unexpected body type %T", commands[3].Body)
	}
	if body.MsgType != "file" || body.File == nil {
		t.Fatalf("body = %+v", body)
	}
	if body.File.MediaID != "media-3" {
		t.Fatalf("file media_id = %q, want media-3", body.File.MediaID)
	}
}

func TestWeComChannel_NameReflectsInstanceID(t *testing.T) {
	ch := newTestWeComChannel(t, bus.NewMessageBus())
	if ch.Name() != "wecom" {
		t.Errorf("Name() before SetInstanceID = %q, want %q", ch.Name(), "wecom")
	}
	// After SetInstanceID, Name() must return the instance key so that
	// InboundMessage.Channel == manager map key == ch.Name().
	ch.SetInstanceID("wecom.eu")
	if ch.Name() != "wecom.eu" {
		t.Errorf("Name() after SetInstanceID = %q, want %q", ch.Name(), "wecom.eu")
	}
}

func newTestWeComChannel(t *testing.T, messageBus *bus.MessageBus) *WeComChannel {
	t.Helper()

	const secretRef = "WECOM_TEST_SECRET"
	bundle := credentials.SecretBundle{
		credentials.SecretRef(secretRef): "secret-1",
	}
	cfg := config.WeComConfig{BotID: "bot-1", SecretRef: secretRef}
	ch, err := NewChannel(cfg, bundle, messageBus)
	if err != nil {
		t.Fatalf("NewChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	ch.routes = newReqIDStore(filepath.Join(t.TempDir(), "reqids.json"))
	return ch
}

func wecomTestJPEGData(t *testing.T) []byte {
	t.Helper()

	const jpegBase64 = "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////" +
		"//////////////////////////////////////////////////////////////////////////////////////////////2wBDAf//////////////////////////////////////////////////////////////////////////////////////" +
		"//////////////////////////////////////////////////////////////////////////////////////////////wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAb/xAAVEQEBAAAAAAAAAAAAAAAAAAAABf/aAAwDAQACEAMQAAAB6A//xAAVEAEBAAAAAAAAAAAAAAAAAAAAEf/aAAgBAQABBQJf/8QAFBEBAAAAAAAAAAAAAAAAAAAAEP/aAAgBAwEBPwF//8QAFBEBAAAAAAAAAAAAAAAAAAAAEP/aAAgBAgEBPwF//8QAFBABAAAAAAAAAAAAAAAAAAAAEP/aAAgBAQAGPwJf/8QAFBABAAAAAAAAAAAAAAAAAAAAEP/aAAgBAQABPyFf/9k="

	return decodeTestBase64(t, jpegBase64)
}

func TestDecodeWeComUploadFinish_AcceptsNumericCreatedAt(t *testing.T) {
	t.Parallel()

	resp, err := decodeWeComEnvelopeBody[wecomUploadMediaFinishResponse](wecomEnvelope{
		Body: json.RawMessage(`{"type":"file","media_id":"media-1","created_at":1380000000}`),
	})
	if err != nil {
		t.Fatalf("decodeWeComEnvelopeBody() error = %v", err)
	}
	if resp.Type != "file" || resp.MediaID != "media-1" {
		t.Fatalf("response = %+v", resp)
	}
	if string(resp.CreatedAt) != "1380000000" {
		t.Fatalf("created_at = %s, want 1380000000", string(resp.CreatedAt))
	}
}

func wecomTestAck(body any) wecomEnvelope {
	var raw []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		raw = encoded
	}
	return wecomEnvelope{
		ErrCode: 0,
		ErrMsg:  "ok",
		Body:    raw,
	}
}

// --- D9 /stop-redirect adapter reachability (ADR-20260928 D9, corrected
// transport ruling §3.1) -----------------------------------------------
//
// Specification under test: a Tier-B channel adapter must recognise
// /stop-redirect inside its OWN dispatch path and route it through
// channels.DispatchRedirectIfRecognized BEFORE the message can reach the
// agent loop's intake (the pkg/bus inbound channel) — mid-stream text would
// only enter the steering queue, which is exactly what the corrected
// transport ruling §3.1 forbids. Every expected value below derives from
// that D9 contract (pkg/channels/cancelparse.go::DispatchRedirectIfRecognized)
// and from the allow-list precedent in
// TestDispatchIncoming_DeniedSenderHasNoSideEffects — NOT from the current
// implementation, whose dispatchIncoming wires no redirect dispatch at all.
//
// Deliberate gaps: command-word case/whitespace rules and the bare-form
// usage text are unit-tested in pkg/channels/cancelparse_test.go; the ack
// wording is the sendFn reply path, not the adapter reachability defect
// under test. The green run and mutation probes for this test are CHECK's
// duty and are deferred per the RED role split.

// newStopRedirectTestChannel builds a WeComChannel wired the way the
// dispatch-path tests need: fresh req-id routes, running state, a recording
// CancelInterceptor at the agent-loop seam, and a commandSend stub standing
// in for the WeCom websocket transport. allowFrom mirrors config.WeComConfig's
// allow-list (nil/empty = allow all, per BaseChannel.IsAllowedSender).
func newStopRedirectTestChannel(
	t *testing.T,
	messageBus *bus.MessageBus,
	allowFrom []string,
) (*WeComChannel, *recordingCancelInterceptor) {
	t.Helper()

	const secretRef = "WECOM_TEST_SECRET"
	bundle := credentials.SecretBundle{
		credentials.SecretRef(secretRef): "secret-1",
	}
	ch, err := NewChannel(config.WeComConfig{
		BotID:     "bot-1",
		SecretRef: secretRef,
		AllowFrom: allowFrom,
	}, bundle, messageBus)
	if err != nil {
		t.Fatalf("NewChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	ch.routes = newReqIDStore(filepath.Join(t.TempDir(), "reqids.json"))
	ch.SetRunning(true)

	interceptor := &recordingCancelInterceptor{}
	ch.SetCancelInterceptor(interceptor)
	ch.commandSend = func(wecomCommand, time.Duration) (wecomEnvelope, error) {
		return wecomTestAck(nil), nil
	}
	return ch, interceptor
}

func stopRedirectTestMessage(msgID, chatID, userID, content string) wecomIncomingMessage {
	msg := wecomIncomingMessage{
		MsgID:    msgID,
		ChatID:   chatID,
		ChatType: "direct",
		MsgType:  "text",
		Text: &struct {
			Content string `json:"content"`
		}{Content: content},
	}
	msg.From.UserID = userID
	return msg
}

// dispatchAndDrain runs the real dispatch path and drains every inbound
// message the bus holds afterwards, so an interception assertion can prove
// the message never reached the agent loop's intake.
func dispatchAndDrain(
	t *testing.T,
	ch *WeComChannel,
	messageBus *bus.MessageBus,
	reqID string,
	msg wecomIncomingMessage,
) []bus.InboundMessage {
	t.Helper()
	if err := ch.dispatchIncoming(reqID, msg); err != nil {
		t.Fatalf("dispatchIncoming() error = %v", err)
	}
	var drained []bus.InboundMessage
	for {
		select {
		case m := <-messageBus.InboundChan():
			drained = append(drained, m)
		default:
			return drained
		}
	}
}

// TestDispatchIncoming_StopRedirectAdapterPath proves the WeCom adapter
// dispatch path intercepts /stop-redirect through the D9 redirect dispatch
// — one interceptor call with the exact (channel, chat, sender, instruction)
// tuple and no fallthrough to the agent-loop intake — while leaving the
// ordinary-text path, the /cancel path and the sender allow-list untouched.
func TestDispatchIncoming_StopRedirectAdapterPath(t *testing.T) {
	t.Run("redirect_command_intercepted_once_before_intake", func(t *testing.T) {
		messageBus := bus.NewMessageBus()
		ch, interceptor := newStopRedirectTestChannel(t, messageBus, nil)

		msg := stopRedirectTestMessage("msg-sr-1", "chat-sr-1", "user-sr-1", "/stop-redirect do this")
		inbound := dispatchAndDrain(t, ch, messageBus, "req-sr-1", msg)

		if interceptor.redirectCalls != 1 {
			t.Fatalf(
				"/stop-redirect was not routed through the adapter's redirect dispatch: redirectCalls = %d, want 1 (cancelCalls = %d, inbound published = %d)",
				interceptor.redirectCalls, interceptor.calls, len(inbound),
			)
		}
		if interceptor.redirectChannel != "wecom" ||
			interceptor.redirectChatID != "chat-sr-1" ||
			interceptor.redirectSenderID != "user-sr-1" ||
			interceptor.redirectInstruction != "do this" {
			t.Fatalf(
				"redirect primitive arguments = (channel=%q, chat=%q, sender=%q, instruction=%q), want (wecom, chat-sr-1, user-sr-1, do this)",
				interceptor.redirectChannel, interceptor.redirectChatID, interceptor.redirectSenderID, interceptor.redirectInstruction,
			)
		}
		if len(inbound) != 0 {
			t.Fatalf(
				"/stop-redirect fell through to the agent-loop intake: %d inbound message(s) published, want 0 — the command must be consumed before HandleMessage (corrected transport ruling §3.1)",
				len(inbound),
			)
		}
	})

	t.Run("bare_redirect_command_consumed_without_handler_call", func(t *testing.T) {
		messageBus := bus.NewMessageBus()
		ch, interceptor := newStopRedirectTestChannel(t, messageBus, nil)

		msg := stopRedirectTestMessage("msg-sr-2", "chat-sr-2", "user-sr-2", "/stop-redirect")
		inbound := dispatchAndDrain(t, ch, messageBus, "req-sr-2", msg)

		if interceptor.redirectCalls != 0 {
			t.Fatalf(
				"bare /stop-redirect reached the redirect handler: redirectCalls = %d, want 0 (the bare form is usage-only and changes nothing)",
				interceptor.redirectCalls,
			)
		}
		if len(inbound) != 0 {
			t.Fatalf(
				"bare /stop-redirect fell through to the agent-loop intake: inbound published = %d, want 0 (it must be consumed with the usage reply)",
				len(inbound),
			)
		}
	})

	t.Run("ordinary_text_dispatches_normally_without_redirect", func(t *testing.T) {
		messageBus := bus.NewMessageBus()
		ch, interceptor := newStopRedirectTestChannel(t, messageBus, nil)

		msg := stopRedirectTestMessage("msg-sr-3", "chat-sr-3", "user-sr-3", "do this")
		inbound := dispatchAndDrain(t, ch, messageBus, "req-sr-3", msg)

		if len(inbound) != 1 {
			t.Fatalf("ordinary text inbound count = %d, want 1", len(inbound))
		}
		if inbound[0].ChatID != "chat-sr-3" {
			t.Fatalf("ordinary text inbound ChatID = %q, want chat-sr-3", inbound[0].ChatID)
		}
		if inbound[0].Content != "do this" {
			t.Fatalf("ordinary text inbound Content = %q, want \"do this\"", inbound[0].Content)
		}
		if interceptor.redirectCalls != 0 {
			t.Fatalf("ordinary text triggered redirect: redirectCalls = %d, want 0", interceptor.redirectCalls)
		}
		if interceptor.calls != 0 {
			t.Fatalf("ordinary text triggered cancel: cancelCalls = %d, want 0", interceptor.calls)
		}
	})

	t.Run("cancel_command_stays_on_cancel_path", func(t *testing.T) {
		messageBus := bus.NewMessageBus()
		ch, interceptor := newStopRedirectTestChannel(t, messageBus, nil)

		msg := stopRedirectTestMessage("msg-sr-4", "chat-sr-4", "user-sr-4", "/cancel")
		inbound := dispatchAndDrain(t, ch, messageBus, "req-sr-4", msg)

		if interceptor.calls != 1 {
			t.Fatalf("/cancel cancelCalls = %d, want 1 (cancel must keep firing the cancel state machine)", interceptor.calls)
		}
		if interceptor.redirectCalls != 0 {
			t.Fatalf("/cancel triggered redirect: redirectCalls = %d, want 0", interceptor.redirectCalls)
		}
		if len(inbound) != 0 {
			t.Fatalf("/cancel reached the agent-loop intake: inbound published = %d, want 0", len(inbound))
		}
	})

	t.Run("denied_sender_never_reaches_redirect", func(t *testing.T) {
		messageBus := bus.NewMessageBus()
		ch, interceptor := newStopRedirectTestChannel(t, messageBus, []string{"allowed-user"})

		msg := stopRedirectTestMessage("msg-sr-5", "chat-sr-5", "denied-user", "/stop-redirect do this")
		inbound := dispatchAndDrain(t, ch, messageBus, "req-sr-5", msg)

		_, hasTurn := ch.getTurn("chat-sr-5")
		_, hasRoute := ch.routes.Get("chat-sr-5")
		if interceptor.redirectCalls != 0 || interceptor.calls != 0 || len(inbound) != 0 || hasTurn || hasRoute {
			t.Fatalf(
				"denied sender side effects: redirectCalls = %d, cancelCalls = %d, inbound = %d, turn = %v, route = %v; want all zero/false",
				interceptor.redirectCalls, interceptor.calls, len(inbound), hasTurn, hasRoute,
			)
		}
	})
}
