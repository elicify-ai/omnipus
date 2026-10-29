// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for show_to_user (BS-6.9, BS-6.10, BS-6.13, BS-6.15, FR-014, FR-020).
// The constructor name NewShowToUserTool is in the spec, but a direct call
// would stop this package compiling until the symbol exists. Tests reach the
// tool through GeneralBuiltinMetadata and fail BLOCKED while it is absent.
//
// Assumed injector, used only for the watching-yes branch:
//
//	func (t *ShowToUserTool) SetWatching(fn func(sessionID string) bool)
//
// A nil func means not watching (spec). The not-watching cases do not call it.
package tools

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const (
	chatLinksAsked       = "Asked the Library to open plan.md."
	chatLinksNotWatching = `The user is not watching this chat. Left an "Open in Library" notice; the Library was not opened.`
	chatLinksOutside     = "not in a Library, give the user the full path in text"
	chatLinksWorker      = "only the chat the user is in can open the Library"
	chatLinksNoUI        = "no UI attached"
	chatLinksPlanLink    = "link: omnipus://file/W/notes/plan.md"
)

func chatLinksShowToUser(t *testing.T) Tool {
	t.Helper()
	for _, tool := range GeneralBuiltinMetadata() {
		if tool.Name() == "show_to_user" {
			return tool
		}
	}
	t.Fatal("BLOCKED: show_to_user not implemented — required by FR-014 / BS-6.9")
	return nil
}

func chatLinksOwnFile(t *testing.T) (context.Context, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "plan.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := WithToolContext(context.Background(), "webchat", "chat-1")
	ctx = WithWorkspaceID(ctx, "W")
	ctx = WithTurnWorkspaceDir(ctx, dir)
	ctx = WithAgentID(ctx, "mia")
	ctx = WithTranscriptSessionID(ctx, "root-show")
	ctx = WithToolRootChatSessionID(ctx, "root-show")
	return ctx, dir
}

func chatLinksSetWatching(t *testing.T, tool Tool, watching func(string) bool) {
	t.Helper()
	m := reflect.ValueOf(tool).MethodByName("SetWatching")
	if !m.IsValid() {
		t.Fatal("BLOCKED: show_to_user.SetWatching(func(string) bool) not implemented — required by FR-026 / BS-6.10")
	}
	if m.Type().NumIn() != 1 {
		t.Fatalf("BLOCKED: SetWatching signature %s; this RED pack assumes one func(string) bool", m.Type())
	}
	m.Call([]reflect.Value{reflect.ValueOf(watching)})
}

func chatLinksOpenTarget(t *testing.T, res *ToolResult) (requestID, workspaceID, path string, present bool) {
	t.Helper()
	if res == nil {
		t.Fatal("nil ToolResult")
	}
	f := reflect.ValueOf(res).Elem().FieldByName("OpenInLibrary")
	if !f.IsValid() {
		t.Fatal("BLOCKED: ToolResult.OpenInLibrary not implemented — required by the tool-to-hub seam / BS-6.1")
	}
	if f.Kind() == reflect.Pointer && f.IsNil() {
		return "", "", "", false
	}
	p := f
	if f.Kind() == reflect.Pointer {
		p = f.Elem()
	}
	req := p.FieldByName("RequestID")
	ws := p.FieldByName("WorkspaceID")
	rel := p.FieldByName("Path")
	if !req.IsValid() || !ws.IsValid() || !rel.IsValid() {
		t.Fatalf("BLOCKED: OpenInLibrary type %s; RED pack expects RequestID, WorkspaceID, Path", p.Type())
	}
	return req.String(), ws.String(), rel.String(), true
}

func TestShowToUser_NotWatching_NoticeTextAndLink(t *testing.T) {
	tool := chatLinksShowToUser(t)
	desc := tool.Description()
	if !strings.Contains(desc, "do not expect the Library to open") {
		t.Errorf("show_to_user description = %q, want the watching sentence from the spec (BS-7.2)", desc)
	}
	ctx, _ := chatLinksOwnFile(t)
	res := tool.Execute(ctx, map[string]any{"ref": "notes/plan.md"})
	if res == nil {
		t.Fatal("nil result")
	}
	if res.IsError {
		t.Fatalf("not watching is not an error (BS-6.10); got %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, chatLinksNotWatching) {
		t.Fatalf("result = %q, want %q (BS-6.10)", res.ForLLM, chatLinksNotWatching)
	}
	if strings.Contains(res.ForLLM, "Asked the Library to open") {
		t.Fatalf("not-watching result claims an open: %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, chatLinksPlanLink) {
		t.Fatalf("result = %q, want %q (FR-011)", res.ForLLM, chatLinksPlanLink)
	}
	id, ws, rel, present := chatLinksOpenTarget(t, res)
	if !present {
		t.Fatal("success must set OpenInLibrary (tool-to-hub seam)")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		t.Errorf("request_id = %q, want 32 lowercase hex", id)
	}
	if ws != "W" || rel != "notes/plan.md" {
		t.Errorf("open target = %s %s, want W notes/plan.md", ws, rel)
	}
}

func TestShowToUser_Watching_AsksLibrary(t *testing.T) {
	tool := chatLinksShowToUser(t)
	chatLinksSetWatching(t, tool, func(string) bool { return true })
	ctx, _ := chatLinksOwnFile(t)
	res := tool.Execute(ctx, map[string]any{"ref": "notes/plan.md"})
	if res == nil || res.IsError {
		text := ""
		if res != nil {
			text = res.ForLLM
		}
		t.Fatalf("watching success failed: %s", text)
	}
	if !strings.Contains(res.ForLLM, chatLinksAsked) {
		t.Fatalf("result = %q, want %q (BS-6.1 / BS-6.10)", res.ForLLM, chatLinksAsked)
	}
	if strings.Contains(res.ForLLM, "was not opened") {
		t.Fatalf("watching result uses the away sentence: %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, chatLinksPlanLink) {
		t.Fatalf("result = %q, want the link line", res.ForLLM)
	}
}

func TestShowToUser_Refusals(t *testing.T) {
	tool := chatLinksShowToUser(t)
	ctx, dir := chatLinksOwnFile(t)

	outside := filepath.Join(t.TempDir(), "OUTSIDE-TOKEN.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outRes := tool.Execute(ctx, map[string]any{"ref": outside})
	if outRes == nil || !outRes.IsError || !strings.Contains(outRes.ForLLM, chatLinksOutside) {
		got := ""
		if outRes != nil {
			got = outRes.ForLLM
		}
		t.Errorf("outside = %q, want it to contain %q (BS-6.9)", got, chatLinksOutside)
	}
	if outRes != nil && strings.Contains(outRes.ForLLM, "OUTSIDE-TOKEN") {
		t.Errorf("outside error echoes the raw path: %q", outRes.ForLLM)
	}

	worker := WithTranscriptSessionID(ctx, "child-show")
	worker = WithToolRootChatSessionID(worker, "root-show")
	wres := tool.Execute(worker, map[string]any{"ref": "notes/plan.md"})
	if wres == nil || !wres.IsError || !strings.Contains(wres.ForLLM, chatLinksWorker) {
		got := ""
		if wres != nil {
			got = wres.ForLLM
		}
		t.Errorf("worker = %q, want %q (BS-6.9)", got, chatLinksWorker)
	}

	tg := WithToolContext(context.Background(), "telegram", "chat-ext")
	tg = WithWorkspaceID(tg, "W")
	tg = WithTurnWorkspaceDir(tg, dir)
	tg = WithTranscriptSessionID(tg, "root-show")
	tg = WithToolRootChatSessionID(tg, "root-show")
	tres := tool.Execute(tg, map[string]any{"ref": "notes/plan.md"})
	if tres == nil || !tres.IsError || !strings.Contains(tres.ForLLM, chatLinksNoUI) {
		got := ""
		if tres != nil {
			got = tres.ForLLM
		}
		t.Errorf("non-web = %q, want %q (BS-6.9)", got, chatLinksNoUI)
	}

	// Exact sentences for other-workspace and no-workspace are not in the spec.
	// Assert an error that is distinct from the three quoted refusals and does
	// not echo the raw path.
	other := tool.Execute(ctx, map[string]any{"ref": "omnipus://file/OTHER/notes/plan.md"})
	chatLinksUnquotedRefusal(t, other, "OTHER", "other workspace")
	noWS := WithWorkspaceID(context.Background(), "")
	noWS = WithToolContext(noWS, "webchat", "chat-1")
	noWS = WithTurnWorkspaceDir(noWS, dir)
	noWS = WithTranscriptSessionID(noWS, "root-show")
	noWS = WithToolRootChatSessionID(noWS, "root-show")
	chatLinksUnquotedRefusal(t, tool.Execute(noWS, map[string]any{"ref": "notes/plan.md"}), "notes/plan.md", "no workspace")
}

func chatLinksUnquotedRefusal(t *testing.T, res *ToolResult, raw, label string) {
	t.Helper()
	if res == nil || !res.IsError || res.ForLLM == "" {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Errorf("%s: want an error, got %q", label, got)
		return
	}
	for _, quoted := range []string{chatLinksOutside, chatLinksWorker, chatLinksNoUI} {
		if strings.Contains(res.ForLLM, quoted) {
			t.Errorf("%s reused quoted refusal %q: %q", label, quoted, res.ForLLM)
		}
	}
	if raw != "" && strings.Contains(res.ForLLM, raw) {
		t.Errorf("%s echoes the raw path: %q", label, res.ForLLM)
	}
	if _, _, _, present := chatLinksOpenTarget(t, res); present {
		t.Errorf("%s published an open target", label)
	}
}

func TestShowToUser_LibraryRootMatchesMissing(t *testing.T) {
	tool := chatLinksShowToUser(t)
	ctx, _ := chatLinksOwnFile(t)
	missing := tool.Execute(ctx, map[string]any{"ref": "gone-chat-links.md"})
	root := tool.Execute(ctx, map[string]any{"ref": "."})
	empty := tool.Execute(ctx, map[string]any{"ref": ""})
	for _, res := range []*ToolResult{missing, root, empty} {
		if res == nil || !res.IsError {
			t.Fatalf("library-root/missing must be an error, got %#v (BS-6.9)", res)
		}
		if strings.Contains(res.ForLLM, "gone-chat-links") {
			t.Fatalf("refusal echoes the raw path: %q", res.ForLLM)
		}
	}
	if missing.ForLLM != root.ForLLM || missing.ForLLM != empty.ForLLM {
		t.Fatalf("missing %q, root %q, empty %q must be byte-identical (BS-6.9)", missing.ForLLM, root.ForLLM, empty.ForLLM)
	}
}

func TestShowToUser_RateLimit_SharedBudget(t *testing.T) {
	a := chatLinksShowToUser(t)
	b := chatLinksShowToUser(t)
	if reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() {
		t.Fatal("two catalog builds returned one instance; BS-6.13 needs two instances sharing one budget")
	}
	ctx, _ := chatLinksOwnFile(t)
	ctx = WithToolRootChatSessionID(ctx, "rate-root")
	ctx = WithTranscriptSessionID(ctx, "rate-root")
	allowed := 0
	for i := 0; i < 3; i++ {
		if res := a.Execute(ctx, map[string]any{"ref": "notes/plan.md"}); res != nil && !res.IsError {
			allowed++
		}
	}
	for i := 0; i < 2; i++ {
		if res := b.Execute(ctx, map[string]any{"ref": "notes/plan.md"}); res != nil && !res.IsError {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("calls allowed before the sixth = %d, want 5 (BS-6.13)", allowed)
	}
	sixth := b.Execute(ctx, map[string]any{"ref": "notes/plan.md"})
	if sixth == nil || !sixth.IsError {
		got := ""
		if sixth != nil {
			got = sixth.ForLLM
		}
		t.Fatalf("sixth call = %q, want a rate-limit error (BS-6.13)", got)
	}
	if strings.Contains(sixth.ForLLM, "notes/plan.md") {
		t.Errorf("rate-limit error echoes the path: %q", sixth.ForLLM)
	}

	// A validation refusal must not consume budget (BS-6.13).
	fresh, _ := chatLinksOwnFile(t)
	fresh = WithToolRootChatSessionID(fresh, "budget-root")
	fresh = WithTranscriptSessionID(fresh, "budget-root")
	refused := WithToolContext(fresh, "telegram", "chat-ext")
	if res := a.Execute(refused, map[string]any{"ref": "notes/plan.md"}); res == nil || !res.IsError {
		t.Fatal("telegram call must be refused before the budget check")
	}
	for i := 0; i < 5; i++ {
		res := a.Execute(fresh, map[string]any{"ref": "notes/plan.md"})
		if res == nil || res.IsError {
			text := ""
			if res != nil {
				text = res.ForLLM
			}
			t.Fatalf("web call %d after one validation refusal failed: %s (validation must not consume budget)", i+1, text)
		}
	}
}
