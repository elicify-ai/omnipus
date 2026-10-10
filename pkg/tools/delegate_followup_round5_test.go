// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Round-4 NEW-8: the steer enqueue and live-delivery failures, which come from
// the steering sink after the initial reads succeeded, must reach the calling
// agent as fixed sentences too — while authored refusals and plain context
// errors stay visible and every cause stays errors.Is-reachable.

package tools

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// failingEnqueueSink fails the native steer enqueue with a store-level error.
type failingEnqueueSink struct {
	fakeSteeringSink
	err error
}

func (s *failingEnqueueSink) EnqueueSteeringMessage(string, string, providers.Message, string) (string, error) {
	return "", s.err
}

var lateStoreFault = &fs.PathError{Op: "open", Path: "/home/op/.omnipus/session_lifecycle/child.jsonl", Err: fs.ErrPermission}

// Oracle: a native live steer whose enqueue fails with a store error shows a
// fixed sentence, keeps the cause reachable, and delivers nothing.
func TestDelegateSteer_NativeEnqueueFault_LeaksNoPath(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	sink := &failingEnqueueSink{err: lateStoreFault}
	tool.SetSteeringSink(sink)
	seedRedirectChild(t, lc, "child-native-late", session.LifecycleRunning, false)

	res := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"), map[string]any{
		"action": "steer", "session_id": "child-native-late", "text": "go",
	})
	if !res.IsError {
		t.Fatalf("a failed enqueue must be an error, got: %s", res.ForLLM)
	}
	requireNoStoreDetail(t, res.ForLLM, lateStoreFault.Path)
	if !errors.Is(res.Err, fs.ErrPermission) {
		t.Errorf("the cause must stay reachable through errors.Is, got %v", res.Err)
	}
}

// Oracle: a live external delivery that fails with a store error keeps the
// named not_steerable prefix but not the store detail.
func TestDelegateSteer_ExternalDeliveryFault_LeaksNoPath(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSteeringSink(&cliDelivererSink{deliverErr: lateStoreFault})
	seedRedirectChild(t, lc, "child-3p-late", session.LifecycleRunning, true)

	res := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"), map[string]any{
		"action": "steer", "session_id": "child-3p-late", "text": "go",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "not_steerable") {
		t.Fatalf("want the named not_steerable error, got (%v, %q)", res.IsError, res.ForLLM)
	}
	requireNoStoreDetail(t, res.ForLLM, lateStoreFault.Path)
	if !errors.Is(res.Err, fs.ErrPermission) {
		t.Errorf("the cause must stay reachable through errors.Is, got %v", res.Err)
	}
}

// Oracle: an authored refusal from the delivery capability is still shown as
// written (the caller needs it to act).
func TestDelegateSteer_ExternalDeliveryAuthoredRefusalStaysVisible(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSteeringSink(&cliDelivererSink{deliverErr: curatedRefusalStub{"session child-3p-auth has no live external CLI conversation to deliver to"}})
	seedRedirectChild(t, lc, "child-3p-auth", session.LifecycleRunning, true)

	res := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"), map[string]any{
		"action": "steer", "session_id": "child-3p-auth", "text": "go",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "no live external CLI conversation") {
		t.Fatalf("an authored refusal must stay visible, got (%v, %q)", res.IsError, res.ForLLM)
	}
}
