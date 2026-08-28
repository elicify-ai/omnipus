// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

// panickyTranscriber panics on every Transcribe call. It reproduces the
// precondition for the unroutable-message goroutine's panic-recovery path
// (pkg/agent/loop.go ~2298-2390): processMessage's very first step,
// transcribeAudioInMessage, calls into a transcriber that panics BEFORE
// resolveMessageRoute is ever re-attempted inside processMessage — mirroring
// panickyMockProvider's role for the session-worker C8 regression test in
// session_worker_test.go, but targeting a component that actually runs on
// this specific code path (an unroutable message never reaches the LLM
// provider: processMessage's internal resolveMessageRoute call would hit the
// exact same routing failure resolveSteeringTarget already saw, and return
// before ever calling Chat()).
type panickyTranscriber struct{}

func (p *panickyTranscriber) Name() string { return "panicky-transcriber" }

func (p *panickyTranscriber) Transcribe(_ context.Context, _ string) (*voice.TranscriptionResponse, error) {
	panic("simulated transcription crash (unroutable-message panic-recovery regression test)")
}

// TestUnroutableMessage_PanicStillPublishesTerminalFrame is the regression
// test for the unroutable-message goroutine's panic-recovery path
// (pkg/agent/loop.go ~2298-2390), which had NO test coverage before this fix
// (confirmed by a separate reviewer pass). It asserts:
//
//  1. A message resolveSteeringTarget cannot route (an explicit agent_id
//     that does not exist in the registry) is dispatched via the bare
//     single-shot "unroutable" goroutine, not a session worker.
//  2. When processMessage panics on that path, the panic-recovery still
//     force-publishes a terminal error frame so the client is not left
//     hanging — mirroring TestSessionWorker_PanicStillPublishesTerminalFrame's
//     C8 assertion for the session-worker path.
//  3. The ORIGINAL panic's message is captured in the logs (not merely
//     "something panicked"). This is the MEDIUM-HIGH fix under test: the
//     inner recover now logs the original panic value BEFORE attempting the
//     force-publish, so the true root cause survives on record even if the
//     publish attempt itself were to panic.
func TestUnroutableMessage_PanicStillPublishesTerminalFrame(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "unroutable-panic.log")

	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.ERROR)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	defer al.Close()

	// Wire an audio-capable pipeline whose transcriber panics. This produces
	// a genuine panic inside processMessage's very first step
	// (transcribeAudioInMessage), which runs unconditionally BEFORE
	// processMessage re-attempts routing — a real provider/tool nil-deref
	// analog that this code path is meant to survive.
	store := media.NewFileMediaStore()
	audioPath := filepath.Join(tmpDir, "voice-note.ogg")
	if err := os.WriteFile(audioPath, []byte("fake audio bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ref, err := store.Store(audioPath, media.MediaMeta{
		Filename:    "voice-note.ogg",
		ContentType: "audio/ogg",
	}, "test-scope")
	if err != nil {
		t.Fatalf("store.Store: %v", err)
	}
	al.SetMediaStore(store)
	al.SetTranscriber(&panickyTranscriber{})

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

	// An explicit agent_id that does not exist in the registry makes
	// resolveMessageRoute (and therefore resolveSteeringTarget) fail — this
	// is what routes the message to the bare single-shot "unroutable" branch
	// (loop.go ~2298) instead of a per-session worker.
	msg := bus.InboundMessage{
		Channel: "web",
		ChatID:  "chat-unroutable-panic",
		Sender: bus.SenderInfo{
			CanonicalID: "user-unroutable-panic",
		},
		Content:  "voice note",
		Media:    []string{ref},
		Metadata: map[string]string{"agent_id": "does-not-exist-in-registry"},
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := msgBus.PublishInbound(pubCtx, msg); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}

	// (a) A terminal error frame IS published — the panic must not leave the
	// client hanging, exactly like the session-worker C8 guarantee.
	replyCtx, replyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer replyCancel()
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content == "" {
			t.Fatal("terminal outbound after panic has empty content")
		}
		t.Logf("terminal outbound after panic: %q", out.Content)
	case <-replyCtx.Done():
		t.Fatal("no terminal frame published after unroutable-message panic — client would hang")
	}

	// The outbound publish above only proves the inner recover ran; the
	// original-panic log write happens in that same recover just before the
	// publish attempt. Poll briefly to avoid a flaky race against the file
	// write landing on disk.
	var logged string
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, readErr := os.ReadFile(logFile)
		if readErr == nil {
			logged = string(data)
			if strings.Contains(logged, "simulated transcription crash") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("log file never captured the original panic message; got:\n%s", logged)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// (b) The ORIGINAL panic's message is captured — not just a generic
	// "something panicked" line. This is the MEDIUM-HIGH regression: without
	// logging the original panic BEFORE the force-publish attempt, a panic
	// inside the publish path would silently discard this diagnostic and
	// only a confusing secondary symptom would reach the logs.
	if !strings.Contains(logged, "simulated transcription crash (unroutable-message panic-recovery regression test)") {
		t.Errorf("log file missing the ORIGINAL panic's message; got:\n%s", logged)
	}
	if !strings.Contains(logged, "Panic in processMessage") {
		t.Errorf("log file missing the pre-publish root-cause log line; got:\n%s", logged)
	}
	if !strings.Contains(logged, "chat-unroutable-panic") {
		t.Errorf("log file missing chat_id context field; got:\n%s", logged)
	}
}
