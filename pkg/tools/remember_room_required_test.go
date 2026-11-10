// Omnipus — founder ruling (Gap 10): remember's `room` is REQUIRED, no default.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

func rememberRoomTool(t *testing.T) (*tools.RememberTool, *simpleMemStore) {
	t.Helper()
	store := newSimpleMemStore(t.TempDir())
	return tools.NewRememberTool(store, nil), store
}

func TestRememberTool_RoomRequired_MissingOrInvalidRefusedAndNothingWritten(t *testing.T) {
	for name, room := range map[string]any{"missing": nil, "empty": "", "unknown": "team", "wrong_case": "Shared"} {
		t.Run(name, func(t *testing.T) {
			tool, store := rememberRoomTool(t)
			args := map[string]any{"content": "a decision", "category": "key_decision"}
			if room != nil {
				args["room"] = room
			}
			res := tool.Execute(tools.WithWorkspaceID(context.Background(), "ws-1"), args)
			if res == nil || !res.IsError {
				t.Fatalf("a call with room=%v must be refused, got %+v", room, res)
			}
			for _, want := range []string{"room", "private", "shared", "choose"} {
				if !strings.Contains(res.ForLLM, want) {
					t.Errorf("refusal %q must mention %q", res.ForLLM, want)
				}
			}
			if _, err := os.Stat(store.memoryFile()); err == nil {
				t.Error("a refused call must not write anything")
			}
		})
	}
}

func TestRememberTool_RoomRequired_SchemaListsRoomAsRequired(t *testing.T) {
	tool, _ := rememberRoomTool(t)
	req, _ := tool.Parameters()["required"].([]string)
	has := map[string]bool{}
	for _, r := range req {
		has[r] = true
	}
	if !has["room"] || !has["content"] || !has["category"] {
		t.Fatalf("required must list content, category and room, got %v", req)
	}
}

func TestRememberTool_RoomRequired_ExplicitRoomsAccepted(t *testing.T) {
	for _, room := range []string{"private", "shared"} {
		tool, _ := rememberRoomTool(t)
		res := tool.Execute(tools.WithWorkspaceID(context.Background(), "ws-1"),
			map[string]any{"content": "x", "category": "reference", "room": room})
		if res.IsError {
			t.Fatalf("room=%s must be accepted: %s", room, res.ForLLM)
		}
	}
}
