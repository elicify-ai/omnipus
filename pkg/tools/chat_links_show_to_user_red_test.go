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
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

const (
	chatLinksAsked       = "Asked the Library to open plan.md."
	chatLinksNotWatching = `The user is not watching this chat. Left an "Open in Library" notice; the Library was not opened.`
	// Full sentences for the BS-6.9 rows. The spec quotes the distinctive
	// clause; the tool prefix and the rest of the sentence are the wording
	// pinned for that row. None of them may contain the rejected path.
	chatLinksOutside     = "show_to_user: this file is not in a Library, give the user the full path in text."
	chatLinksOtherWS     = "show_to_user: that file belongs to another workspace; this chat can only open files in its own workspace."
	chatLinksNotFound    = "show_to_user: there is no file the Library can open at that reference."
	chatLinksNoWorkspace = "show_to_user: this chat has no workspace, so there is no Library to open."
	chatLinksWorker      = "show_to_user: only the chat the user is in can open the Library."
	chatLinksNoUI        = "show_to_user: no UI attached to this channel; tell the user the file name instead."
	chatLinksRate        = "show_to_user: limit reached (5 calls per minute in this chat); tell the user where the file is instead."
	chatLinksPlanLink    = "link: omnipus://file/W/notes/plan.md"
)

func chatLinksShowToUser(t *testing.T, spec string) Tool {
	t.Helper()
	for _, tool := range GeneralBuiltinMetadata() {
		if tool.Name() == "show_to_user" {
			return tool
		}
	}
	t.Fatalf("BLOCKED: show_to_user not implemented — required by %s", spec)
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
	tool := chatLinksShowToUser(t, "FR-014 / BS-6.10")
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
	tool := chatLinksShowToUser(t, "FR-014 / BS-6.1")
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
	tool := chatLinksShowToUser(t, "FR-014 / BS-6.9")
	ctx, dir := chatLinksOwnFile(t)

	outside := filepath.Join(t.TempDir(), "OUTSIDE-TOKEN.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outRes := tool.Execute(ctx, map[string]any{"ref": outside})
	chatLinksExactRefusal(t, outRes, chatLinksOutside, "OUTSIDE-TOKEN", "outside")

	worker := WithTranscriptSessionID(ctx, "child-show")
	worker = WithToolRootChatSessionID(worker, "root-show")
	wres := tool.Execute(worker, map[string]any{"ref": "notes/plan.md"})
	chatLinksExactRefusal(t, wres, chatLinksWorker, "notes/plan.md", "worker")

	tg := WithToolContext(context.Background(), "telegram", "chat-ext")
	tg = WithWorkspaceID(tg, "W")
	tg = WithTurnWorkspaceDir(tg, dir)
	tg = WithTranscriptSessionID(tg, "root-show")
	tg = WithToolRootChatSessionID(tg, "root-show")
	tres := tool.Execute(tg, map[string]any{"ref": "notes/plan.md"})
	chatLinksExactRefusal(t, tres, chatLinksNoUI, "notes/plan.md", "non-web")

	other := tool.Execute(ctx, map[string]any{"ref": "omnipus://file/OTHER/notes/plan.md"})
	chatLinksExactRefusal(t, other, chatLinksOtherWS, "OTHER", "other workspace")
	noWS := WithWorkspaceID(context.Background(), "")
	noWS = WithToolContext(noWS, "webchat", "chat-1")
	noWS = WithTurnWorkspaceDir(noWS, dir)
	noWS = WithTranscriptSessionID(noWS, "root-show")
	noWS = WithToolRootChatSessionID(noWS, "root-show")
	chatLinksExactRefusal(t, tool.Execute(noWS, map[string]any{"ref": "notes/plan.md"}), chatLinksNoWorkspace, "notes/plan.md", "no workspace")
}

func chatLinksExactRefusal(t *testing.T, res *ToolResult, want, raw, label string) {
	t.Helper()
	if res == nil || !res.IsError {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Errorf("%s: want error %q, got %q", label, want, got)
		return
	}
	if res.ForLLM != want {
		t.Errorf("%s = %q, want %q (BS-6.9)", label, res.ForLLM, want)
	}
	if raw != "" && strings.Contains(res.ForLLM, raw) {
		t.Errorf("%s echoes the raw path: %q", label, res.ForLLM)
	}
	if _, _, _, present := chatLinksOpenTarget(t, res); present {
		t.Errorf("%s published an open target", label)
	}
}

func TestShowToUser_LibraryRootMatchesMissing(t *testing.T) {
	tool := chatLinksShowToUser(t, "BS-6.9 / SEC-9")
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
	if missing.ForLLM != chatLinksNotFound || root.ForLLM != chatLinksNotFound || empty.ForLLM != chatLinksNotFound {
		t.Fatalf("missing %q, root %q, empty %q, want byte-identical %q (BS-6.9)", missing.ForLLM, root.ForLLM, empty.ForLLM, chatLinksNotFound)
	}
}

func TestShowToUser_RateLimit_SharedBudget(t *testing.T) {
	a := chatLinksShowToUser(t, "BS-6.13 / FR-020")
	b := chatLinksShowToUser(t, "BS-6.13 / FR-020")
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
	if sixth == nil || !sixth.IsError || sixth.ForLLM != chatLinksRate {
		got := ""
		if sixth != nil {
			got = sixth.ForLLM
		}
		t.Fatalf("sixth call = %q, want %q (BS-6.13)", got, chatLinksRate)
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

// Audit reason codes as the spec writes them (BS-6.15 / FR-020). The event
// name is unspecified, so it is not pinned. Outcome is the existing audit
// decision field and must be non-empty; allow versus deny is not pinned.
const (
	chatLinksAuditOutside   = "outside"
	chatLinksAuditOther     = "other workspace"
	chatLinksAuditDenied    = "denied-or-missing"
	chatLinksAuditNoWS      = "no workspace"
	chatLinksAuditDelegated = "delegated session"
	chatLinksAuditChannel   = "wrong channel"
	chatLinksAuditRate      = "rate limit"
)

func chatLinksSetAudit(t *testing.T, tool Tool, logger *audit.Logger) {
	t.Helper()
	m := reflect.ValueOf(tool).MethodByName("SetAuditLogger")
	if !m.IsValid() {
		t.Fatal("BLOCKED: show_to_user.SetAuditLogger(*audit.Logger) not implemented — required by BS-6.15 / FR-020")
	}
	m.Call([]reflect.Value{reflect.ValueOf(logger)})
}

func chatLinksAuditRoot(ctx context.Context, id string) context.Context {
	ctx = WithTranscriptSessionID(ctx, id)
	ctx = WithToolRootChatSessionID(ctx, id)
	return WithAgentID(ctx, "mia")
}

func chatLinksAuditOnce(t *testing.T, tool Tool, ctx context.Context, ref string) (*ToolResult, []auditRow) {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	chatLinksSetAudit(t, tool, logger)
	res := tool.Execute(ctx, map[string]any{"ref": ref})
	return res, readAuditRows(t, logger, dir)
}

func chatLinksAuditMatch(rows []auditRow, code string) bool {
	needle := `"` + code + `"`
	for _, row := range rows {
		if row.Tool != "show_to_user" || row.AgentID != "mia" || row.Decision == "" {
			continue
		}
		raw, err := json.Marshal(row)
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), needle) {
			return true
		}
	}
	return false
}

func chatLinksAuditSuccess(rows []auditRow) bool {
	for _, row := range rows {
		if row.Tool != "show_to_user" || row.AgentID != "mia" || row.Decision == "" {
			continue
		}
		raw, err := json.Marshal(row)
		if err != nil {
			continue
		}
		text := string(raw)
		if strings.Contains(text, `"W"`) && strings.Contains(text, `"notes/plan.md"`) {
			return true
		}
	}
	return false
}

func TestShowToUser_Audit(t *testing.T) {
	tool := chatLinksShowToUser(t, "BS-6.15 / FR-020")
	base, dir := chatLinksOwnFile(t)

	res, rows := chatLinksAuditOnce(t, tool, chatLinksAuditRoot(base, "audit-ok"), "notes/plan.md")
	if res == nil || res.IsError || !chatLinksAuditSuccess(rows) {
		t.Fatalf("success audit = err:%v rows:%+v, want agent mia, an outcome, workspace W and path notes/plan.md (BS-6.15)", res != nil && res.IsError, rows)
	}

	outside := filepath.Join(t.TempDir(), "OUTSIDE-TOKEN.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	noWS := WithToolContext(context.Background(), "webchat", "chat-1")
	noWS = WithTurnWorkspaceDir(noWS, dir)
	noWS = WithWorkspaceID(noWS, "")
	noWS = chatLinksAuditRoot(noWS, "audit-nows")
	child := chatLinksAuditRoot(base, "audit-root-of-child")
	child = WithTranscriptSessionID(child, "audit-child")

	cases := []struct {
		name string
		ctx  context.Context
		ref  string
		code string
	}{
		{"outside", chatLinksAuditRoot(base, "audit-out"), outside, chatLinksAuditOutside},
		{"other workspace", chatLinksAuditRoot(base, "audit-other"), "omnipus://file/OTHER/notes/plan.md", chatLinksAuditOther},
		{"denied-or-missing", chatLinksAuditRoot(base, "audit-miss"), "gone-chat-links.md", chatLinksAuditDenied},
		{"no workspace", noWS, "notes/plan.md", chatLinksAuditNoWS},
		{"delegated session", child, "notes/plan.md", chatLinksAuditDelegated},
		{"wrong channel", chatLinksAuditRoot(WithToolContext(base, "telegram", "chat-ext"), "audit-tg"), "notes/plan.md", chatLinksAuditChannel},
	}
	for _, tc := range cases {
		got, audited := chatLinksAuditOnce(t, tool, tc.ctx, tc.ref)
		if got == nil || !got.IsError {
			t.Errorf("%s: want a refusal before the audit check", tc.name)
		}
		if !chatLinksAuditMatch(audited, tc.code) {
			t.Errorf("%s: no audit row with agent mia, an outcome and reason code %q (BS-6.15 / FR-020); rows=%+v", tc.name, tc.code, audited)
		}
	}

	rateCtx := chatLinksAuditRoot(base, "audit-rate")
	dirRate := t.TempDir()
	rateLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: dirRate, RetentionDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	chatLinksSetAudit(t, tool, rateLogger)
	for i := 0; i < 5; i++ {
		call := tool.Execute(rateCtx, map[string]any{"ref": "notes/plan.md"})
		if call == nil || call.IsError {
			text := ""
			if call != nil {
				text = call.ForLLM
			}
			t.Fatalf("audit rate call %d failed: %s", i+1, text)
		}
	}
	sixth := tool.Execute(rateCtx, map[string]any{"ref": "notes/plan.md"})
	rateRows := readAuditRows(t, rateLogger, dirRate)
	if sixth == nil || !sixth.IsError || !chatLinksAuditMatch(rateRows, chatLinksAuditRate) {
		t.Fatalf("sixth call audit = %v rows=%+v, want a rate-limit refusal with reason code %q (BS-6.13 / BS-6.15)", sixth, rateRows, chatLinksAuditRate)
	}
	if len(rateRows) < 6 {
		t.Fatalf("rate-limit chat audited %d calls, want all 6 (FR-020)", len(rateRows))
	}
}
