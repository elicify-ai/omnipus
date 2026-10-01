package tools

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Oracles: docs/internal/specs/email-mail-view-spec.md D19, D26, D27, D29,
// D33, MC-11, MC-15, MC-22, MC-27, MC-30, MC-32, MC-35, FR-012, FR-038,
// scenarios B-6, B-9, B-19, B-23, B-41, B-49, B-51, B-52.
// The transport fake is the SMTP/IMAP edge the tool already depends on.
// Path checks go through the real files the tool must resolve; ResolvePath
// is not mocked.

func TestSendEmailTool_MarkdownBodyPipeline(t *testing.T) {
	ft := newFakeTransport()
	tool := NewSendEmailTool(EmailTransports{"ws": ft})
	res := tool.Execute(WithWorkspaceID(context.Background(), "ws"), map[string]any{
		"to":      "a@x.test",
		"subject": "Hello",
		"body":    "# Title\n\nSee [docs](https://docs.example/a).\n\n<script>alert(1)</script>\n",
	})
	if res.IsError {
		t.Fatalf("B-6: send_email error: %s", res.ForLLM)
	}
	if len(ft.sent) != 1 {
		t.Fatalf("B-6: SMTP sends = %d, want 1", len(ft.sent))
	}
	plain, html := mustToolAlternative(t, ft.sent[0].Body)
	if strings.Contains(strings.ToLower(html), "<script") {
		t.Fatalf("MC-2: HTML part still contains a script element:\n%s", html)
	}
	if !strings.Contains(html, "<h1>") || !strings.Contains(html, "Title") {
		t.Fatalf("B-6: HTML part = %q, want an h1 Title", html)
	}
	if !strings.Contains(plain, "docs (https://docs.example/a)") {
		t.Fatalf("MC-30: plain part = %q, want %q", plain, "docs (https://docs.example/a)")
	}
}

func TestSendEmailTool_RecipientLists(t *testing.T) {
	// B-9 / D29: To, Cc and Bcc are lists. The transmitted copy has no Bcc
	// header. d@x.test is still an envelope recipient.
	ft := newFakeTransport()
	tool := NewSendEmailTool(EmailTransports{"ws": ft})
	res := tool.Execute(WithWorkspaceID(context.Background(), "ws"), map[string]any{
		"to":      []any{"a@x.test"},
		"cc":      []any{"b@x.test", "c@x.test"},
		"bcc":     []any{"d@x.test"},
		"subject": "Hi",
		"body":    "Hello",
	})
	if res.IsError || len(ft.sent) != 1 {
		t.Fatalf("B-9/D26: send with To/Cc/Bcc lists failed (%s); SMTP sends=%d, want 1", res.ForLLM, len(ft.sent))
	}
	msg := mustMail(t, ft.sent[0].Body)
	if got := msg.Header.Get("To"); got != "a@x.test" {
		t.Fatalf("B-9: To = %q, want a@x.test", got)
	}
	cc := msg.Header.Get("Cc")
	if !strings.Contains(cc, "b@x.test") || !strings.Contains(cc, "c@x.test") {
		t.Fatalf("B-9: Cc = %q, want b@x.test and c@x.test", cc)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatalf("D29: transmitted copy Bcc = %q, want no Bcc header", msg.Header.Get("Bcc"))
	}
	if !strings.Contains(ft.sent[0].To, "d@x.test") {
		t.Fatalf("D29: SMTP envelope To = %q, want d@x.test included", ft.sent[0].To)
	}
}

func TestRecipientCap_PreDial(t *testing.T) {
	// MC-27 / B-41: 50 recipients after de-dup pass; the 51st fails before SMTP.
	t.Run("fifty passes", func(t *testing.T) {
		ft := newFakeTransport()
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"to": recipientRange(1, 50), "subject": "s", "body": "hello",
		})
		if res.IsError || len(ft.sent) != 1 {
			t.Fatalf("MC-27: 50 recipients rejected (%s); sends=%d, want 1 send", res.ForLLM, len(ft.sent))
		}
	})
	t.Run("fifty one fails before dial", func(t *testing.T) {
		ft := newFakeTransport()
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"to": recipientRange(1, 51), "subject": "s", "body": "hello",
		})
		if !res.IsError || len(ft.sent) != 0 {
			t.Fatalf("MC-27: 51 recipients err=%v sends=%d, want a pre-dial error and 0 sends (%s)", res.IsError, len(ft.sent), res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "50") {
			t.Fatalf("MC-27: error %q does not name the 50-recipient cap", res.ForLLM)
		}
	})
	t.Run("duplicate does not count twice", func(t *testing.T) {
		ft := newFakeTransport()
		addrs := recipientRange(1, 49)
		addrs = append(addrs, "u1@x.test", "u1@x.test")
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"to": addrs, "subject": "s", "body": "hello",
		})
		if res.IsError || len(ft.sent) != 1 {
			t.Fatalf("MC-27: 49 unique + one duplicate rejected (%s); sends=%d, want 1", res.ForLLM, len(ft.sent))
		}
	})
	t.Run("malformed address fails before dial", func(t *testing.T) {
		ft := newFakeTransport()
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
			"to": []any{"not-an-address"}, "subject": "s", "body": "hello",
		})
		if !res.IsError || len(ft.sent) != 0 {
			t.Fatalf("MC-27: malformed address err=%v sends=%d, want parse failure before dial (%s)", res.IsError, len(ft.sent), res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "not-an-address") {
			t.Fatalf("MC-27: error %q does not name the bad address", res.ForLLM)
		}
	})
}

func TestReplyTool_RecipientListsAndMarkdown(t *testing.T) {
	// MC-15: reply gains list parameters and a Markdown body, and stays one tool.
	props := propertiesOf(t, NewReplyTool(nil).Parameters())
	for _, key := range []string{"cc", "bcc"} {
		spec, ok := props[key].(map[string]any)
		if !ok || spec["type"] != "array" {
			t.Fatalf("MC-15/D26: reply parameter %s = %#v, want an array", key, props[key])
		}
	}
	body, _ := props["body"].(map[string]any)
	desc, _ := body["description"].(string)
	if !strings.Contains(strings.ToLower(desc), "markdown") {
		t.Fatalf("MC-15/D3: reply body description %q, want it to say Markdown", desc)
	}
}

func TestCreateEmailDraftTool_ResultContract(t *testing.T) {
	// B-19 / B-23 / MC-11 / FR-012: draft tool appends, does not send, and
	// returns the result object. With no derivable public origin, chat_link
	// is null and chat_link_reason is set (FR-015).
	ft := newFakeTransport()
	tool := toolByName(t, EmailTransports{"ws": ft}, "create_email_draft")
	res := tool.Execute(mailCtx(), map[string]any{
		"to": []any{"a@x.test"}, "subject": "Draft", "body": "Hello",
	})
	if res.IsError {
		t.Fatalf("B-19: create_email_draft error: %s", res.ForLLM)
	}
	if len(ft.sent) != 0 {
		t.Fatalf("B-23: draft creation opened %d SMTP sends, want 0", len(ft.sent))
	}
	payload := toolObject(t, res.ForLLM)
	if payload["created"] != true {
		t.Fatalf("MC-11: created = %#v, want true", payload["created"])
	}
	id, _ := payload["message_id"].(string)
	if !messageID128(id) {
		t.Fatalf("FR-012: message_id = %q, want <32-hex@domain> (128-bit local part)", id)
	}
	if uidNum(payload["uid"]) <= 0 || uidNum(payload["uidvalidity"]) <= 0 {
		t.Fatalf("MC-11: uid=%v uidvalidity=%v, want both > 0", payload["uid"], payload["uidvalidity"])
	}
	if payload["chat_link"] != nil {
		t.Fatalf("FR-015: chat_link = %#v, want null when no public origin is configured", payload["chat_link"])
	}
	reason, _ := payload["chat_link_reason"].(string)
	if reason == "" {
		t.Fatalf("FR-015: chat_link_reason = %#v, want a non-empty reason", payload["chat_link_reason"])
	}
}

func TestAgentAttachment_SamePolicyResolution(t *testing.T) {
	// MC-35 / B-51 / D33: attachments are a parameter of send_email, not a new
	// tool, and send_email stays AutoAsks.
	props := propertiesOf(t, NewSendEmailTool(nil).Parameters())
	spec, ok := props["attachments"].(map[string]any)
	if !ok || spec["type"] != "array" {
		t.Fatalf("MC-35: send_email attachments = %#v, want an array parameter", props["attachments"])
	}
	if NewSendEmailTool(nil).Name() != "send_email" {
		t.Fatalf("MC-35: tool name = %s, want send_email", NewSendEmailTool(nil).Name())
	}
	for _, tool := range EmailToolset(EmailTransports{}) {
		if strings.Contains(tool.Name(), "attach") {
			t.Fatalf("MC-35: separate attachment tool %q", tool.Name())
		}
	}
	if AutoApproveClassOf("send_email") != AutoAsks {
		t.Fatalf("MC-35: send_email auto-approve class = %s, want asks", AutoApproveClassOf("send_email").String())
	}
	cfg := &ToolPolicyCfg{
		GlobalPolicies: map[string]config.ToolPolicy{"send_email": config.ToolPolicyAllow},
		Policies:       map[string]config.ToolPolicy{"send_email": config.ToolPolicyAsk},
	}
	if got := EffectiveToolPolicy(cfg, ScopeGeneral, "custom", "send_email"); got != "ask" {
		t.Fatalf("MC-35: effective send_email = %q, want ask (attachment must not change this tool's resolution)", got)
	}
}

func TestSendEmailTool_WorkspaceAttachment(t *testing.T) {
	// B-49 / MC-32: a workspace file rides the transmitted MIME part.
	ws := t.TempDir()
	path := filepath.Join(ws, "notes.txt")
	if err := os.WriteFile(path, []byte("NOTES-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	ft := newFakeTransport()
	ctx := WithTurnWorkspaceDir(mailCtx(), ws)
	res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(ctx, map[string]any{
		"to": "a@x.test", "subject": "s", "body": "hello",
		"attachments": []any{"notes.txt"},
	})
	if res.IsError || len(ft.sent) != 1 {
		t.Fatalf("B-49: attach failed (%s); sends=%d, want 1", res.ForLLM, len(ft.sent))
	}
	if !strings.Contains(ft.sent[0].Body, "NOTES-BYTES") || !strings.Contains(ft.sent[0].Body, "notes.txt") {
		t.Fatalf("B-49: transmitted body missing the workspace file name or bytes:\n%s", ft.sent[0].Body)
	}
}

func TestAgentAttachment_CapsAndInvalidRefPreDial(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	secret := filepath.Join(home, "credentials.json")
	if err := os.WriteFile(secret, []byte("SECRET-SENTINEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("eleventh file rejected before dial", func(t *testing.T) {
		refs := writeNFiles(t, ws, 11)
		assertAttachRejected(t, ws, refs, "11")
	})
	t.Run("over 25 MiB rejected before dial", func(t *testing.T) {
		path := filepath.Join(ws, "big.bin")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate((25 << 20) + 1); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		assertAttachRejected(t, ws, []any{path}, "big.bin")
	})
	t.Run("secret carve-out rejected before dial", func(t *testing.T) {
		ft := newFakeTransport()
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(WithTurnWorkspaceDir(mailCtx(), ws), map[string]any{
			"to": "a@x.test", "subject": "s", "body": "hello",
			"attachments": []any{secret},
		})
		if !res.IsError || len(ft.sent) != 0 {
			t.Fatalf("MC-35: credentials.json err=%v sends=%d, want refusal before dial (%s)", res.IsError, len(ft.sent), res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "credentials.json") {
			t.Fatalf("MC-35: error %q does not name credentials.json", res.ForLLM)
		}
		if len(ft.sent) > 0 && strings.Contains(ft.sent[0].Body, "SECRET-SENTINEL") {
			t.Fatal("MC-35: secret bytes were transmitted")
		}
	})
	t.Run("symlink to secret rejected", func(t *testing.T) {
		link := filepath.Join(ws, "looks-fine.txt")
		if err := os.Symlink(secret, link); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		assertAttachRejected(t, ws, []any{"looks-fine.txt"}, "looks-fine.txt")
	})
	t.Run("outside workspace attaches", func(t *testing.T) {
		ft := newFakeTransport()
		res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(WithTurnWorkspaceDir(mailCtx(), ws), map[string]any{
			"to": "a@x.test", "subject": "s", "body": "hello",
			"attachments": []any{outside},
		})
		if res.IsError || len(ft.sent) != 1 {
			t.Fatalf("MC-35: outside-workspace file rejected (%s); sends=%d, want it attached", res.ForLLM, len(ft.sent))
		}
		if !strings.Contains(ft.sent[0].Body, "OUTSIDE-BYTES") {
			t.Fatalf("MC-35: outside file bytes missing from the transmitted body:\n%s", ft.sent[0].Body)
		}
	})
	t.Run("nonexistent named before dial", func(t *testing.T) {
		assertAttachRejected(t, ws, []any{"missing-notes.txt"}, "missing-notes.txt")
	})
}

func assertAttachRejected(t *testing.T, ws string, refs []any, named string) {
	t.Helper()
	ft := newFakeTransport()
	res := NewSendEmailTool(EmailTransports{"ws": ft}).Execute(WithTurnWorkspaceDir(mailCtx(), ws), map[string]any{
		"to": "a@x.test", "subject": "s", "body": "hello", "attachments": refs,
	})
	if !res.IsError || len(ft.sent) != 0 {
		t.Fatalf("MC-32/MC-35: attachment %s err=%v sends=%d, want a pre-dial error (%s)", named, res.IsError, len(ft.sent), res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, named) && named != "11" {
		t.Fatalf("MC-35: error %q does not name %s", res.ForLLM, named)
	}
	if named == "11" && !strings.Contains(res.ForLLM, "10") {
		t.Fatalf("MC-32: error %q does not name the 10-file cap", res.ForLLM)
	}
}

func writeNFiles(t *testing.T, dir string, n int) []any {
	t.Helper()
	var refs []any
	for i := 0; i < n; i++ {
		name := "f" + string(rune('a'+i)) + ".txt"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, name)
	}
	return refs
}

func mailCtx() context.Context {
	return WithWorkspaceID(context.Background(), "ws")
}

func recipientRange(from, to int) []any {
	out := make([]any, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, "u"+strconv.Itoa(i)+"@x.test")
	}
	return out
}

func toolByName(t *testing.T, tps EmailTransports, name string) Tool {
	t.Helper()
	for _, tool := range EmailToolset(tps) {
		if tool.Name() == name {
			return tool
		}
	}
	var names []string
	for _, tool := range EmailToolset(tps) {
		names = append(names, tool.Name())
	}
	t.Fatalf("FR-012: EmailToolset missing %s; names=%v", name, names)
	return nil
}

func propertiesOf(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("tool schema has no properties: %#v", schema)
	}
	return props
}

func toolObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("result is not JSON %q: %v", raw, err)
	}
	return m
}

func uidNum(v any) float64 {
	n, _ := v.(float64)
	return n
}

func messageID128(id string) bool {
	if len(id) < 3 || id[0] != '<' || id[len(id)-1] != '>' {
		return false
	}
	local, domain, ok := strings.Cut(id[1:len(id)-1], "@")
	if !ok || domain == "" || len(local) != 32 {
		return false
	}
	for _, r := range local {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func mustMail(t *testing.T, raw string) *mail.Message {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("transmitted body is not RFC 5322: %v\n%s", err, raw)
	}
	return msg
}

func mustToolAlternative(t *testing.T, raw string) (plain, html string) {
	t.Helper()
	msg := mustMail(t, raw)
	media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || media != "multipart/alternative" {
		t.Fatalf("MC-3: Content-Type = %q (%v), want multipart/alternative\n%s", msg.Header.Get("Content-Type"), err, raw)
	}
	boundary := params["boundary"]
	rawBody, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(rawBody)
	var sawPlain, sawHTML int
	for _, part := range strings.Split(body, "--"+boundary) {
		part = strings.TrimSpace(part)
		if part == "" || part == "--" {
			continue
		}
		header, content, ok := strings.Cut(part, "\r\n\r\n")
		if !ok {
			header, content, ok = strings.Cut(part, "\n\n")
		}
		if !ok {
			continue
		}
		lower := strings.ToLower(header)
		switch {
		case strings.Contains(lower, "text/plain"):
			sawPlain++
			plain = content
		case strings.Contains(lower, "text/html"):
			sawHTML++
			html = content
		}
	}
	if sawPlain != 1 || sawHTML != 1 {
		t.Fatalf("MC-3: plain parts=%d html parts=%d\n%s", sawPlain, sawHTML, raw)
	}
	return plain, html
}
