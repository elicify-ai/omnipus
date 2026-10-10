package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// U15 — leaf deletions (FR-038; BDD-12.1; T14, T23).
//
// Spec sources (session-core-spec.md §"U15 — Leaf deletion inventory"):
//   - DEL-16: sse.go::SSEHandler/newSSEHandler + the gateway_boot.go
//     backward-compat SSE registration (the `/api/v1/chat` SSE route). Canonical
//     replacement: WebSocket persistent-session transport.
//   - DEL-25: websocket_streamer.go::WSHandler.GetStreamer missing-session-ID
//     connection-binding fallback. Canonical: producing callers supply an
//     explicit sessionID; a missing target refuses.
//   - DEL-27: contracts DelegateStatusAction.task_id (keep session_id).
//   - DEL-28: contracts DoneStats.tokens_dropped (keep frames_emitted).
//   - DEL-29: rest_tool_registry.go::HandleBuiltinToolsDeprecated + the
//     rest.go `registerCoreRoutes` legacy `/api/v1/tools/builtin` registration
//     (keep HandleToolsRegistry and the live `/api/v1/tools`).
//
// Proving a deletion = K (controlled absence with controls; canonical producer
// still exists) AND B (obsolete behaviour absent; canonical positive works).

// sessionCoreU15AbsentFromPackageSources proves no non-test .go file in the
// current package directory mentions `symbol`. It carries the two controls
// BDD-12.1 requires: a known-present control (proves the reader reads real
// source) and an injected-forbidden control (a temp fixture containing `symbol`
// must be detected by the same matcher — proving a negative here is meaningful).
func sessionCoreU15AbsentFromPackageSources(t *testing.T, symbol, presentControlFile, presentControl string) {
	t.Helper()

	ctrl, err := os.ReadFile(presentControlFile)
	if err != nil {
		t.Fatalf("U15 instrument: present-control read %s: %v", presentControlFile, err)
	}
	if !strings.Contains(string(ctrl), presentControl) {
		t.Fatalf("U15 instrument broken: present-control %q not found in %s", presentControl, presentControlFile)
	}

	injected := filepath.Join(t.TempDir(), "injected_fixture.txt")
	if err := os.WriteFile(injected, []byte(symbol), 0o600); err != nil {
		t.Fatalf("U15 instrument: write injected fixture: %v", err)
	}
	injectedData, err := os.ReadFile(injected)
	if err != nil {
		t.Fatalf("U15 instrument: read injected fixture: %v", err)
	}
	if !strings.Contains(string(injectedData), symbol) {
		t.Fatalf("U15 instrument broken: injected forbidden token %q not detected", symbol)
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("U15 instrument: glob: %v", err)
	}
	sawControlFile := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("U15 sweep: read %s: %v", f, err)
		}
		if strings.Contains(string(data), symbol) {
			t.Errorf("%s still references %q — DEL requires it removed with a clean source sweep (FR-038)", f, symbol)
		}
		if f == presentControlFile {
			sawControlFile = true
		}
	}
	if !sawControlFile {
		t.Fatalf("U15 instrument broken: present-control file %s not among package sources", presentControlFile)
	}
}

// TestSessionCoreU15_SSEHandlerRemoved is the K half of DEL-16. RED on the
// pre-cut code: sse.go declares SSEHandler/newSSEHandler and gateway_boot.go
// registers the `/api/v1/chat` SSE route. The `/api/v1/chat/ws` WebSocket route
// is a different string and stays.
func TestSessionCoreU15_SSEHandlerRemoved(t *testing.T) {
	// Canonical positive control: the WebSocket transport survives.
	sessionCoreU15AbsentFromPackageSources(t, "SSEHandler", "websocket_streamer.go", "func (h *WSHandler) GetStreamer")
	sessionCoreU15AbsentFromPackageSources(t, `"/api/v1/chat"`, "websocket_streamer.go", "func (h *WSHandler) GetStreamer")
}

// sessionCoreU15AbsentFromFile is the file-scoped variant of the absence check,
// used when the forbidden token legitimately appears ELSEWHERE in the package.
// DEL-25 removes only the GetStreamer fallback; the other `h.sessionIDs[chatID]`
// bindings (websocket_chat.go, websocket_forward_hub.go) are canonical and stay.
// A missing file counts as absent. Controls: known-present (proves the reader
// reads real source) and injected-forbidden (proves the matcher detects a real
// occurrence).
func sessionCoreU15AbsentFromFile(t *testing.T, file, symbol, presentControl string) {
	t.Helper()

	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("U15 sweep: read %s: %v", file, err)
	}
	src := string(data)
	if !strings.Contains(src, presentControl) {
		t.Fatalf("U15 instrument broken: present-control %q not found in %s", presentControl, file)
	}

	injected := filepath.Join(t.TempDir(), "injected_fixture.txt")
	if err := os.WriteFile(injected, []byte(symbol), 0o600); err != nil {
		t.Fatalf("U15 instrument: write injected fixture: %v", err)
	}
	injectedData, err := os.ReadFile(injected)
	if err != nil {
		t.Fatalf("U15 instrument: read injected fixture: %v", err)
	}
	if !strings.Contains(string(injectedData), symbol) {
		t.Fatalf("U15 instrument broken: injected forbidden token %q not detected", symbol)
	}

	if strings.Contains(src, symbol) {
		t.Errorf("%s still references %q — DEL-25 requires the GetStreamer guessed binding removed (FR-038)", file, symbol)
	}
}

// TestSessionCoreU15_GetStreamerGuessedBindingRemoved is the K half of DEL-25.
// RED on the pre-cut code: WSHandler.GetStreamer still resolves a missing
// sessionID from the chatID's current binding (`h.sessionIDs[chatID]` under the
// "Backward-compat fallback" comment). Both the fallback code and its marker
// must go; the GetStreamer method itself stays (present-control).
func TestSessionCoreU15_GetStreamerGuessedBindingRemoved(t *testing.T) {
	const file = "websocket_streamer.go"
	const present = "func (h *WSHandler) GetStreamer"
	sessionCoreU15AbsentFromFile(t, file, "h.sessionIDs[chatID]", present)
	sessionCoreU15AbsentFromFile(t, file, "Backward-compat fallback for a caller", present)
}

// TestSessionCoreU15_BuiltinToolsDeprecatedRouteRemoved is the K half of DEL-29.
// RED on the pre-cut code: rest_tool_registry.go defines
// HandleBuiltinToolsDeprecated and rest.go registers the legacy
// `/api/v1/tools/builtin` route. The live `/api/v1/tools` route and
// HandleToolsRegistry stay (present-control).
func TestSessionCoreU15_BuiltinToolsDeprecatedRouteRemoved(t *testing.T) {
	sessionCoreU15AbsentFromPackageSources(t, "HandleBuiltinToolsDeprecated", "rest_tool_registry.go", "HandleToolsRegistry")
	sessionCoreU15AbsentFromPackageSources(t, `"/api/v1/tools/builtin"`, "rest_tool_registry.go", "HandleToolsRegistry")
}

// TestSessionCoreU15_DelegateStatusAction_TaskIdAliasRemoved is the K half of
// DEL-27, checked on the real generated wire type. RED on the pre-cut code: the
// struct still carries TaskId (`task_id`). Canonical positive: SessionId
// (`session_id`, the child identity) stays.
func TestSessionCoreU15_DelegateStatusAction_TaskIdAliasRemoved(t *testing.T) {
	typ := reflect.TypeOf(gen.DelegateStatusAction{})

	if f, ok := typ.FieldByName("TaskId"); ok {
		t.Errorf("DelegateStatusAction must not carry the deprecated task_id alias (DEL-27): field %s json=%q", f.Name, f.Tag.Get("json"))
	}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "task_id" || strings.HasPrefix(tag, "task_id,") {
			t.Errorf("DelegateStatusAction still serialises task_id (DEL-27): field %s json=%q", typ.Field(i).Name, tag)
		}
	}
	if _, ok := typ.FieldByName("SessionId"); !ok {
		t.Fatal("DelegateStatusAction must keep session_id — the canonical child identity DEL-27 preserves")
	}
}

// TestSessionCoreU15_DoneStats_TokensDroppedRemoved is the K half of DEL-28,
// checked on the real generated wire type. RED on the pre-cut code: the struct
// still carries TokensDropped (`tokens_dropped`). Canonical positive:
// FramesEmitted stays.
func TestSessionCoreU15_DoneStats_TokensDroppedRemoved(t *testing.T) {
	typ := reflect.TypeOf(gen.DoneStats{})

	if f, ok := typ.FieldByName("TokensDropped"); ok {
		t.Errorf("DoneStats must not carry the deprecated tokens_dropped property (DEL-28): field %s json=%q", f.Name, f.Tag.Get("json"))
	}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "tokens_dropped" || strings.HasPrefix(tag, "tokens_dropped,") {
			t.Errorf("DoneStats still serialises tokens_dropped (DEL-28): field %s json=%q", typ.Field(i).Name, tag)
		}
	}
	if _, ok := typ.FieldByName("FramesEmitted"); !ok {
		t.Fatal("DoneStats must keep frames_emitted — the canonical diagnostic DEL-28 preserves")
	}
}
