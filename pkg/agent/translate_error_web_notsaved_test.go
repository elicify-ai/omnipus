// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// S1-08: the WEB/WS error frame for the fixed "couldn't save your message"
// reply. The classifier itself and the CHANNEL path are already pinned
// (TestTranslateTurnError_MessageNotSavedIsTheFixedReply,
// TestStopRedirectRoot_R3_ChannelChatMessageNotSavedRefusesTurnWithFixedReply);
// what was unpinned is the web door — the outbound frame the SPA renders when a
// web chat's own message cannot be appended to its session. Without it a web
// sender would hang or see the raw store error, which is exactly the founder
// ruling the fixed reply exists to enforce.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// blockTranscriptWrite makes the session's transcript path unappendable in a
// uid-independent way: the path becomes a DIRECTORY, so opening it for append
// fails with EISDIR for root and non-root alike (chmod 0o000 is ignored by
// root).
func blockTranscriptWrite(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	path := filepath.Join(al.GetSessionStore().BaseDir(), sessionID, "transcript.jsonl")
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("SETUP: remove transcript: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("SETUP: block the transcript path: %v", err)
	}
}

func TestTranslateTurnError_WebMessageNotSavedPublishesTheFixedReply(t *testing.T) {
	al, msgBus := newConcurrentTestAgentLoop(t)
	defer al.Close()

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("SETUP: create the web chat: %v", err)
	}
	blockTranscriptWrite(t, al, meta.ID)

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := msgBus.PublishInbound(pubCtx, makeSessionMsg(meta.ID, "hello")); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}

	replyCtx, replyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer replyCancel()
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content != messageNotSavedReply {
			t.Errorf("web frame content = %q, want exactly the fixed reply %q",
				out.Content, messageNotSavedReply)
		}
		if strings.Contains(out.Content, "transcript.jsonl") || strings.Contains(out.Content, "is a directory") {
			t.Errorf("the web frame leaks the store's internal error: %q", out.Content)
		}
	case <-replyCtx.Done():
		t.Fatal("no web frame arrived: a web sender whose message could not be saved would hang")
	}
}

// Control: the SAME web fixture with a writable transcript delivers a normal
// reply, so the fixed reply above is not an artefact of the harness.
func TestTranslateTurnError_WebWritableTranscriptDeliversANormalReply(t *testing.T) {
	al, msgBus := newConcurrentTestAgentLoop(t)
	defer al.Close()

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("SETUP: create the web chat: %v", err)
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := msgBus.PublishInbound(pubCtx, makeSessionMsg(meta.ID, "hello")); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}

	replyCtx, replyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer replyCancel()
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content == messageNotSavedReply {
			t.Errorf("instrument: a writable transcript must not produce the not-saved reply, got %q", out.Content)
		}
		if strings.TrimSpace(out.Content) == "" {
			t.Error("instrument: the control must deliver a non-empty reply")
		}
	case <-replyCtx.Done():
		t.Fatal("control: no web frame arrived with a writable transcript")
	}
}
