package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/email"
)

// fakeTransport is an in-memory email.Transport for unit tests — no real
// IMAP/SMTP server is contacted.
type fakeTransport struct {
	inbox    []email.Message
	byUID    map[uint32]email.Message
	sent     []email.SendRequest
	failSend bool
	failRead bool
}

func newFakeTransport(msgs ...email.Message) *fakeTransport {
	ft := &fakeTransport{byUID: map[uint32]email.Message{}}
	for _, m := range msgs {
		ft.inbox = append(ft.inbox, m)
		ft.byUID[m.UID] = m
	}
	return ft
}

func (f *fakeTransport) ReadInbox(_ context.Context, limit int, unseenOnly bool) ([]email.Message, error) {
	if f.failRead {
		return nil, errors.New("imap unreachable")
	}
	var out []email.Message
	for _, m := range f.inbox {
		if unseenOnly && m.Seen {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeTransport) Search(_ context.Context, query string, limit int) ([]email.Message, error) {
	if f.failRead {
		return nil, errors.New("imap unreachable")
	}
	q := strings.ToLower(query)
	var out []email.Message
	for _, m := range f.inbox {
		hay := strings.ToLower(m.Subject + " " + m.From + " " + m.Body)
		if strings.Contains(hay, q) {
			out = append(out, m)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeTransport) ReadMessage(_ context.Context, uid uint32) (*email.Message, error) {
	if f.failRead {
		return nil, errors.New("imap unreachable")
	}
	m, ok := f.byUID[uid]
	if !ok {
		return nil, errors.New("not found")
	}
	return &m, nil
}

func (f *fakeTransport) Send(_ context.Context, req email.SendRequest) error {
	if f.failSend {
		return errors.New("smtp rejected")
	}
	f.sent = append(f.sent, req)
	return nil
}

func (f *fakeTransport) MarkSeen(_ context.Context, uid uint32) error {
	if m, ok := f.byUID[uid]; ok {
		m.Seen = true
		f.byUID[uid] = m
	}
	return nil
}

func TestReadInboxTool_ReturnsEnvelopes(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 2, From: "a@x.com", Subject: "Hello", Seen: false},
		email.Message{UID: 1, From: "b@x.com", Subject: "Old", Seen: true},
	)
	tool := NewReadInboxTool(ft)
	res := tool.Execute(context.Background(), map[string]any{"unseen_only": true})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	var got []email.Message
	if err := json.Unmarshal([]byte(res.ForLLM), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].UID != 2 {
		t.Fatalf("expected only unseen UID 2, got %+v", got)
	}
}

func TestReadInboxTool_NoTransport(t *testing.T) {
	tool := NewReadInboxTool(nil)
	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

func TestReadInboxTool_TransportError(t *testing.T) {
	ft := newFakeTransport()
	ft.failRead = true
	res := NewReadInboxTool(ft).Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "read_inbox failed") {
		t.Fatalf("expected read_inbox failure, got %+v", res)
	}
}

func TestSearchEmailTool(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "boss@x.com", Subject: "Invoice March", Body: "please pay"},
		email.Message{UID: 2, From: "spam@x.com", Subject: "Win money", Body: "click"},
	)
	res := NewSearchEmailTool(ft).Execute(context.Background(), map[string]any{"query": "invoice"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	var got []email.Message
	if err := json.Unmarshal([]byte(res.ForLLM), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].UID != 1 {
		t.Fatalf("expected UID 1 match, got %+v", got)
	}
}

func TestSearchEmailTool_MissingQuery(t *testing.T) {
	res := NewSearchEmailTool(newFakeTransport()).Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "query is required") {
		t.Fatalf("expected query-required error, got %+v", res)
	}
}

func TestReadMessageTool(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 7, From: "x@y.com", Subject: "S", Body: "full body here"})
	res := NewReadMessageTool(ft).Execute(context.Background(), map[string]any{"uid": float64(7)})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	var got email.Message
	if err := json.Unmarshal([]byte(res.ForLLM), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Body != "full body here" {
		t.Fatalf("expected body, got %q", got.Body)
	}
}

func TestReadMessageTool_BadUID(t *testing.T) {
	res := NewReadMessageTool(newFakeTransport()).Execute(context.Background(), map[string]any{"uid": float64(0)})
	if !res.IsError || !strings.Contains(res.ForLLM, "uid is required") {
		t.Fatalf("expected uid error, got %+v", res)
	}
}

func TestSendEmailTool(t *testing.T) {
	ft := newFakeTransport()
	res := NewSendEmailTool(ft).Execute(context.Background(), map[string]any{
		"to": "dest@x.com", "subject": "Hi", "body": "Hello there",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if len(ft.sent) != 1 || ft.sent[0].To != "dest@x.com" || ft.sent[0].Body != "Hello there" {
		t.Fatalf("send not recorded correctly: %+v", ft.sent)
	}
}

func TestSendEmailTool_MissingTo(t *testing.T) {
	res := NewSendEmailTool(newFakeTransport()).Execute(context.Background(), map[string]any{
		"subject": "Hi", "body": "x",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "to is required") {
		t.Fatalf("expected to-required error, got %+v", res)
	}
}

func TestSendEmailTool_TransportError(t *testing.T) {
	ft := newFakeTransport()
	ft.failSend = true
	res := NewSendEmailTool(ft).Execute(context.Background(), map[string]any{
		"to": "d@x.com", "subject": "s", "body": "b",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "send_email failed") {
		t.Fatalf("expected send failure, got %+v", res)
	}
}

func TestReplyTool_ThreadsToOriginalSender(t *testing.T) {
	ft := newFakeTransport(email.Message{
		UID: 5, From: "alice@x.com", Subject: "Question", MessageID: "<abc@x.com>", Body: "?",
	})
	res := NewReplyTool(ft).Execute(context.Background(), map[string]any{
		"uid": float64(5), "body": "Here is your answer",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if len(ft.sent) != 1 {
		t.Fatalf("expected one send, got %d", len(ft.sent))
	}
	s := ft.sent[0]
	if s.To != "alice@x.com" {
		t.Fatalf("reply must go to original sender, got %q", s.To)
	}
	if s.InReplyTo != "<abc@x.com>" {
		t.Fatalf("reply must thread via In-Reply-To, got %q", s.InReplyTo)
	}
	if !strings.HasPrefix(s.Subject, "Re:") {
		t.Fatalf("reply subject must be Re-prefixed, got %q", s.Subject)
	}
}

func TestReplyTool_AlreadyRePrefixed(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 9, From: "a@x.com", Subject: "Re: Hi"})
	res := NewReplyTool(ft).Execute(context.Background(), map[string]any{"uid": float64(9), "body": "ok"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if ft.sent[0].Subject != "Re: Hi" {
		t.Fatalf("must not double-prefix Re:, got %q", ft.sent[0].Subject)
	}
}

func TestReplyTool_MissingBody(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 1, From: "a@x.com"})
	res := NewReplyTool(ft).Execute(context.Background(), map[string]any{"uid": float64(1)})
	if !res.IsError || !strings.Contains(res.ForLLM, "body is required") {
		t.Fatalf("expected body-required error, got %+v", res)
	}
}

func TestEmailToolset_NamesAndScope(t *testing.T) {
	set := EmailToolset(newFakeTransport())
	want := map[string]bool{
		"read_inbox": false, "search_email": false, "read_message": false,
		"send_email": false, "reply": false,
	}
	for _, tool := range set {
		if _, ok := want[tool.Name()]; !ok {
			t.Fatalf("unexpected tool %q in toolset", tool.Name())
		}
		want[tool.Name()] = true
		if tool.Scope() != ScopeGeneral {
			t.Fatalf("tool %q must be ScopeGeneral", tool.Name())
		}
		if tool.Category() != CategoryCommunication {
			t.Fatalf("tool %q must be CategoryCommunication", tool.Name())
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("toolset missing %q", name)
		}
	}
}

// TestReadInboxTool_LimitArg verifies that the limit argument is applied.
// Traces to: pkg/tools/email.go ReadInboxTool.Execute (limit parsing)
func TestReadInboxTool_LimitArg(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "a@x.com", Subject: "A"},
		email.Message{UID: 2, From: "b@x.com", Subject: "B"},
		email.Message{UID: 3, From: "c@x.com", Subject: "C"},
	)
	tool := NewReadInboxTool(ft)
	res := tool.Execute(context.Background(), map[string]any{"limit": float64(2)})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	var got []email.Message
	if err := json.Unmarshal([]byte(res.ForLLM), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit=2 must cap results at 2, got %d", len(got))
	}
}

// TestReadInboxTool_DefaultLimitAllMessages verifies that without a limit arg
// the default is used and all messages within the default are returned.
// Traces to: pkg/tools/email.go ReadInboxTool.Execute (default limit)
func TestReadInboxTool_DefaultLimitAllMessages(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "a@x.com", Subject: "A"},
		email.Message{UID: 2, From: "b@x.com", Subject: "B"},
	)
	tool := NewReadInboxTool(ft)
	res := tool.Execute(context.Background(), map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	var got []email.Message
	if err := json.Unmarshal([]byte(res.ForLLM), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Both messages are within the default limit of 20.
	if len(got) != 2 {
		t.Fatalf("expected 2 messages with default limit, got %d", len(got))
	}
}

// TestReadInboxTool_Differentiation ensures two different inboxes produce two
// different results (rules out hardcoded/no-op implementation).
func TestReadInboxTool_Differentiation(t *testing.T) {
	ft1 := newFakeTransport(email.Message{UID: 1, From: "a@x.com", Subject: "Alpha"})
	ft2 := newFakeTransport(email.Message{UID: 2, From: "b@x.com", Subject: "Beta"})
	res1 := NewReadInboxTool(ft1).Execute(context.Background(), map[string]any{})
	res2 := NewReadInboxTool(ft2).Execute(context.Background(), map[string]any{})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("read_inbox returned identical results for different transports")
	}
}

// TestSearchEmailTool_NoTransport verifies the nil-transport guard.
// Traces to: pkg/tools/email.go SearchEmailTool.Execute
func TestSearchEmailTool_NoTransport(t *testing.T) {
	res := NewSearchEmailTool(nil).Execute(context.Background(), map[string]any{"query": "hello"})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

// TestSearchEmailTool_TransportError verifies the transport-error path.
// Traces to: pkg/tools/email.go SearchEmailTool.Execute
func TestSearchEmailTool_TransportError(t *testing.T) {
	ft := newFakeTransport()
	ft.failRead = true
	res := NewSearchEmailTool(ft).Execute(context.Background(), map[string]any{"query": "q"})
	if !res.IsError || !strings.Contains(res.ForLLM, "search_email failed") {
		t.Fatalf("expected search_email failure, got %+v", res)
	}
}

// TestSearchEmailTool_Differentiation ensures two different queries yield two
// different results (rules out hardcoded response).
func TestSearchEmailTool_Differentiation(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "boss@x.com", Subject: "Invoice"},
		email.Message{UID: 2, From: "spam@x.com", Subject: "Win money"},
	)
	res1 := NewSearchEmailTool(ft).Execute(context.Background(), map[string]any{"query": "invoice"})
	res2 := NewSearchEmailTool(ft).Execute(context.Background(), map[string]any{"query": "win money"})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("search_email returned same output for different queries")
	}
}

// TestReadMessageTool_NoTransport verifies the nil-transport guard.
// Traces to: pkg/tools/email.go ReadMessageTool.Execute
func TestReadMessageTool_NoTransport(t *testing.T) {
	res := NewReadMessageTool(nil).Execute(context.Background(), map[string]any{"uid": float64(1)})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

// TestReadMessageTool_TransportError verifies that a transport-level error
// (not-found) is surfaced as an error result.
// Traces to: pkg/tools/email.go ReadMessageTool.Execute
func TestReadMessageTool_TransportError(t *testing.T) {
	ft := newFakeTransport() // empty — UID 99 won't be found
	res := NewReadMessageTool(ft).Execute(context.Background(), map[string]any{"uid": float64(99)})
	if !res.IsError || !strings.Contains(res.ForLLM, "read_message failed") {
		t.Fatalf("expected read_message failure for missing uid, got %+v", res)
	}
}

// TestReadMessageTool_Differentiation ensures two distinct UIDs produce two
// distinct messages.
func TestReadMessageTool_Differentiation(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "a@x.com", Subject: "First", Body: "body1"},
		email.Message{UID: 2, From: "b@x.com", Subject: "Second", Body: "body2"},
	)
	res1 := NewReadMessageTool(ft).Execute(context.Background(), map[string]any{"uid": float64(1)})
	res2 := NewReadMessageTool(ft).Execute(context.Background(), map[string]any{"uid": float64(2)})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("read_message returned same output for different UIDs")
	}
}

// TestSendEmailTool_MissingBody verifies that a missing body returns an error.
// Traces to: pkg/tools/email.go SendEmailTool.Execute
func TestSendEmailTool_MissingBody(t *testing.T) {
	res := NewSendEmailTool(newFakeTransport()).Execute(context.Background(), map[string]any{
		"to": "d@x.com", "subject": "s",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "body is required") {
		t.Fatalf("expected body-required error, got %+v", res)
	}
}

// TestSendEmailTool_NoTransport verifies the nil-transport guard.
// Traces to: pkg/tools/email.go SendEmailTool.Execute
func TestSendEmailTool_NoTransport(t *testing.T) {
	res := NewSendEmailTool(nil).Execute(context.Background(), map[string]any{
		"to": "d@x.com", "subject": "s", "body": "b",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

// TestSendEmailTool_Differentiation verifies two different recipients produce
// two different response payloads (rules out hardcoded sent response).
func TestSendEmailTool_Differentiation(t *testing.T) {
	ft := newFakeTransport()
	res1 := NewSendEmailTool(ft).Execute(context.Background(), map[string]any{
		"to": "alice@x.com", "subject": "Hi", "body": "Hello",
	})
	res2 := NewSendEmailTool(ft).Execute(context.Background(), map[string]any{
		"to": "bob@x.com", "subject": "Hi", "body": "Hello",
	})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("send_email returned identical output for different recipients")
	}
}

// TestSendEmailTool_Persistence verifies that sent messages are actually
// recorded in the transport (write→read-back).
// Traces to: pkg/tools/email.go SendEmailTool.Execute (persistence check)
func TestSendEmailTool_Persistence(t *testing.T) {
	ft := newFakeTransport()
	if len(ft.sent) != 0 {
		t.Fatalf("precondition: transport must start empty")
	}
	NewSendEmailTool(ft).Execute(context.Background(), map[string]any{
		"to": "dest@x.com", "subject": "Test", "body": "Verify me",
	})
	if len(ft.sent) != 1 {
		t.Fatalf("expected 1 recorded send, got %d", len(ft.sent))
	}
	if ft.sent[0].Body != "Verify me" {
		t.Fatalf("send body not persisted: got %q", ft.sent[0].Body)
	}
}

// TestReplyTool_NoTransport verifies the nil-transport guard.
// Traces to: pkg/tools/email.go ReplyTool.Execute
func TestReplyTool_NoTransport(t *testing.T) {
	res := NewReplyTool(nil).Execute(context.Background(), map[string]any{
		"uid": float64(1), "body": "reply",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

// TestReplyTool_MissingUID verifies the missing UID guard.
// Traces to: pkg/tools/email.go ReplyTool.Execute
func TestReplyTool_MissingUID(t *testing.T) {
	res := NewReplyTool(newFakeTransport()).Execute(context.Background(), map[string]any{
		"body": "hello",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "uid is required") {
		t.Fatalf("expected uid-required error, got %+v", res)
	}
}

// TestReplyTool_OriginalMessageNotFound verifies the transport read-failure path.
// Traces to: pkg/tools/email.go ReplyTool.Execute (ReadMessage error)
func TestReplyTool_OriginalMessageNotFound(t *testing.T) {
	ft := newFakeTransport() // uid 99 not in transport
	res := NewReplyTool(ft).Execute(context.Background(), map[string]any{
		"uid": float64(99), "body": "reply",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "could not load original message") {
		t.Fatalf("expected load-failure error, got %+v", res)
	}
}

// TestReplyTool_NoSenderOnOriginal verifies the guard for a message with no From.
// Traces to: pkg/tools/email.go ReplyTool.Execute (empty From guard)
func TestReplyTool_NoSenderOnOriginal(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 3, From: "", Subject: "Weird"})
	res := NewReplyTool(ft).Execute(context.Background(), map[string]any{
		"uid": float64(3), "body": "reply",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "no sender") {
		t.Fatalf("expected no-sender error, got %+v", res)
	}
}

// TestReplyTool_SendError verifies the SMTP send-failure path.
// Traces to: pkg/tools/email.go ReplyTool.Execute (Send error)
func TestReplyTool_SendError(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 4, From: "a@x.com", Subject: "Help"})
	ft.failSend = true
	res := NewReplyTool(ft).Execute(context.Background(), map[string]any{
		"uid": float64(4), "body": "I can help",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "reply failed") {
		t.Fatalf("expected reply failure, got %+v", res)
	}
}

// TestReplyTool_Differentiation verifies that replying to two different
// messages yields two different response payloads.
func TestReplyTool_Differentiation(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "alice@x.com", Subject: "Alpha", MessageID: "<a@x.com>"},
		email.Message{UID: 2, From: "bob@x.com", Subject: "Beta", MessageID: "<b@x.com>"},
	)
	res1 := NewReplyTool(ft).Execute(context.Background(), map[string]any{
		"uid": float64(1), "body": "ok",
	})
	res2 := NewReplyTool(ft).Execute(context.Background(), map[string]any{
		"uid": float64(2), "body": "ok",
	})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("reply returned identical output for different original messages")
	}
}

// TestParseUID_TableDriven covers all branches of the parseUID helper.
// Traces to: pkg/tools/email.go parseUID (helper function)
func TestParseUID_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		input any
		uid   uint32
		ok    bool
	}{
		{"float64 valid", float64(5), 5, true},
		{"float64 zero", float64(0), 0, false},
		{"float64 negative", float64(-1), 0, false},
		{"int valid", 10, 10, true},
		{"int zero", 0, 0, false},
		{"string", "5", 0, false},
		{"nil", nil, 0, false},
		{"float64 one", float64(1), 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uid, ok := parseUID(tc.input)
			if ok != tc.ok {
				t.Errorf("parseUID(%v): ok=%v, want %v", tc.input, ok, tc.ok)
			}
			if ok && uid != tc.uid {
				t.Errorf("parseUID(%v): uid=%d, want %d", tc.input, uid, tc.uid)
			}
		})
	}
}

// TestParseUID_Differentiation ensures valid and invalid input produce
// different ok values (rules out constant true/false).
func TestParseUID_Differentiation(t *testing.T) {
	_, ok1 := parseUID(float64(5))
	_, ok2 := parseUID(float64(0))
	if ok1 == ok2 {
		t.Fatal("parseUID returned same ok for valid and invalid inputs")
	}
}

// TestReadMessageTool_MissingUID verifies the guard for absent uid key.
// Traces to: pkg/tools/email.go ReadMessageTool.Execute
func TestReadMessageTool_MissingUID(t *testing.T) {
	res := NewReadMessageTool(newFakeTransport()).Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "uid is required") {
		t.Fatalf("expected uid-required error, got %+v", res)
	}
}
