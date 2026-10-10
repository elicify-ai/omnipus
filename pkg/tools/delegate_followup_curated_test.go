// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The delegate steer/resume refusals reach the calling agent (a model). A store
// or revival failure must read as a fixed sentence — never a filesystem path or
// session-file detail — while the cause stays errors.Is-reachable.

package tools

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// curatedRefusalStub is an authored refusal sentence, like the agent's
// curatedTurnError.
type curatedRefusalStub struct{ text string }

func (e curatedRefusalStub) Error() string       { return e.text }
func (e curatedRefusalStub) RefusalText() string { return e.text }

func requireNoStoreDetail(t *testing.T, text string, forbidden ...string) {
	t.Helper()
	for _, f := range forbidden {
		if f != "" && strings.Contains(text, f) {
			t.Errorf("refusal %q leaks store detail %q", text, f)
		}
	}
	for _, f := range []string{".jsonl", "permission denied", "open /", string(filepath.Separator) + "T" + string(filepath.Separator)} {
		if strings.Contains(text, f) {
			t.Errorf("refusal %q leaks store detail %q", text, f)
		}
	}
}

// Oracle: an unreadable lifecycle record (a real failed open, not "not found")
// is refused with a fixed sentence on both entry actions; the cause is kept.
func TestDelegateTool_SteerAndResume_UnreadableRecordRefusalLeaksNoPath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the unreadable-journal instrument needs a non-root runner")
	}
	for _, action := range []string{"resume", "steer"} {
		t.Run(action, func(t *testing.T) {
			sink := &externalSteeringSink{}
			tool, lc, _, _ := newADR053TestTool(t)
			tool.SetSessionLauncher(&dispatchCountingLauncher{})
			tool.SetSteeringSink(sink)
			sessions, parentID, childID := seed3PChild(t, lc)
			tool.SetSessionStore(sessions)
			journal := filepath.Join(lc.Dir(), childID+".jsonl")
			if _, err := os.Stat(journal); err != nil {
				t.Fatalf("instrument check: seeded journal must exist: %v", err)
			}
			if err := os.Chmod(journal, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(journal, 0o600) })
			if f, err := os.Open(journal); err == nil {
				f.Close()
				t.Fatal("instrument check: the journal must really be unreadable")
			}
			ctx := WithTranscriptSessionID(context.Background(), parentID)

			result := tool.Execute(ctx, map[string]any{"action": action, "session_id": childID, "text": "go"})
			if !result.IsError {
				t.Fatalf("an unreadable record must refuse, got success: %s", result.ForLLM)
			}
			requireNoStoreDetail(t, result.ForLLM, lc.Dir(), journal)
			if !strings.Contains(result.ForLLM, childID) {
				t.Errorf("refusal %q should still name the session", result.ForLLM)
			}
			if !errors.Is(result.Err, fs.ErrPermission) {
				t.Errorf("the cause must stay reachable through errors.Is, got %v", result.Err)
			}
			if len(sink.reviveIDs) != 0 {
				t.Errorf("nothing may be revived on a refused read, got %v", sink.reviveIDs)
			}
		})
	}
}

// Oracle: a revival failure whose own text carries a path (an uncurated store
// error) reaches the agent as a fixed sentence; the cause stays reachable.
func TestDelegateTool_Resume_RawReviveErrorLeaksNoPath(t *testing.T) {
	raw := &fs.PathError{Op: "open", Path: "/home/op/.omnipus/sessions/s1/transcript.jsonl", Err: fs.ErrPermission}
	sink := &externalSteeringSink{reviveErr: raw}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(&dispatchCountingLauncher{})
	tool.SetSteeringSink(sink)
	sessions, parentID, childID := seed3PChild(t, lc)
	tool.SetSessionStore(sessions)
	ctx := WithTranscriptSessionID(context.Background(), parentID)

	result := tool.Execute(ctx, map[string]any{"action": "resume", "session_id": childID, "text": "go"})
	if !result.IsError {
		t.Fatalf("a failed revival must be an error, got: %s", result.ForLLM)
	}
	requireNoStoreDetail(t, result.ForLLM, raw.Path)
	if !errors.Is(result.Err, fs.ErrPermission) {
		t.Errorf("the cause must stay reachable through errors.Is, got %v", result.Err)
	}
}
