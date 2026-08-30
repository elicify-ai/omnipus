package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
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

func (f *fakeTransport) ReadInbox(_ context.Context, opts email.InboxOptions) ([]email.Message, error) {
	if f.failRead {
		return nil, errors.New("imap unreachable")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	var out []email.Message
	for _, m := range f.inbox {
		if opts.UnseenOnly && m.Seen {
			continue
		}
		if opts.BeforeUID > 0 && m.UID >= opts.BeforeUID {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeTransport) Search(_ context.Context, query string, opts email.SearchOptions) (email.SearchResult, error) {
	if f.failRead {
		return email.SearchResult{}, errors.New("imap unreachable")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(query)
	var all []email.Message
	for _, m := range f.inbox {
		hay := strings.ToLower(m.Subject + " " + m.From)
		if opts.Body {
			hay += " " + strings.ToLower(m.Body)
		}
		if opts.BeforeUID > 0 && m.UID >= opts.BeforeUID {
			continue
		}
		if strings.Contains(hay, q) {
			all = append(all, m)
		}
	}
	res := email.SearchResult{TotalMatches: len(all), Messages: []email.Message{}}
	if len(all) > limit {
		res.Truncated = true
		all = all[:limit]
		if len(all) > 0 {
			res.NextBeforeUID = all[len(all)-1].UID
		}
	}
	res.Messages = append(res.Messages, all...)
	return res, nil
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
	tool := NewReadInboxTool(EmailTransports{"ws_test": ft})
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
	tool := NewReadInboxTool(nil /* no mailboxes */)
	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

func TestReadInboxTool_TransportError(t *testing.T) {
	ft := newFakeTransport()
	ft.failRead = true
	res := NewReadInboxTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "read_inbox failed") {
		t.Fatalf("expected read_inbox failure, got %+v", res)
	}
}

func TestSearchEmailTool(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "boss@x.com", Subject: "Invoice March", Body: "please pay"},
		email.Message{UID: 2, From: "spam@x.com", Subject: "Win money", Body: "click"},
	)
	res := NewSearchEmailTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"query": "invoice"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	var got email.SearchResult
	if err := json.Unmarshal([]byte(res.ForLLM), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].UID != 1 {
		t.Fatalf("expected UID 1 match, got %+v", got.Messages)
	}
	if got.TotalMatches != 1 {
		t.Fatalf("expected total_matches 1, got %d", got.TotalMatches)
	}
	if got.Truncated {
		t.Fatalf("expected truncated=false for a single match under the limit")
	}
}

func TestSearchEmailTool_MissingQuery(t *testing.T) {
	res := NewSearchEmailTool(
		EmailTransports{"ws_test": newFakeTransport()},
	).Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "query is required") {
		t.Fatalf("expected query-required error, got %+v", res)
	}
}

func TestReadMessageTool(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 7, From: "x@y.com", Subject: "S", Body: "full body here"})
	res := NewReadMessageTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"uid": float64(7)})
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
	res := NewReadMessageTool(
		EmailTransports{"ws_test": newFakeTransport()},
	).Execute(context.Background(), map[string]any{"uid": float64(0)})
	if !res.IsError || !strings.Contains(res.ForLLM, "uid is required") {
		t.Fatalf("expected uid error, got %+v", res)
	}
}

func TestSendEmailTool(t *testing.T) {
	ft := newFakeTransport()
	res := NewSendEmailTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	res := NewSendEmailTool(
		EmailTransports{"ws_test": newFakeTransport()},
	).Execute(context.Background(), map[string]any{
		"subject": "Hi", "body": "x",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "to is required") {
		t.Fatalf("expected to-required error, got %+v", res)
	}
}

func TestSendEmailTool_TransportError(t *testing.T) {
	ft := newFakeTransport()
	ft.failSend = true
	res := NewSendEmailTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	res := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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

// TestReplyTool_PrefersReplyToOverFrom verifies F7: when the original message
// carries a Reply-To header, the reply is addressed there instead of From —
// the address the sender (list mail, ticketing system, no-reply@ sender)
// explicitly asked replies to go to.
func TestReplyTool_PrefersReplyToOverFrom(t *testing.T) {
	ft := newFakeTransport(email.Message{
		UID: 7, From: "no-reply@list.example.com", ReplyTo: "discuss@list.example.com",
		Subject: "Weekly digest", MessageID: "<digest@list.example.com>",
	})
	res := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
		"uid": float64(7), "body": "Thanks for the digest",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if len(ft.sent) != 1 {
		t.Fatalf("expected one send, got %d", len(ft.sent))
	}
	if got := ft.sent[0].To; got != "discuss@list.example.com" {
		t.Fatalf("reply must go to Reply-To when present, got %q", got)
	}
	if !strings.Contains(res.ForLLM, `"to":"discuss@list.example.com"`) {
		t.Fatalf("result must echo the Reply-To address actually used, got %+v", res)
	}
}

// TestReplyTool_FallsBackToFromWhenNoReplyTo verifies that with no Reply-To
// header, the reply still goes to From as before (F7 regression guard).
func TestReplyTool_FallsBackToFromWhenNoReplyTo(t *testing.T) {
	ft := newFakeTransport(email.Message{
		UID: 8, From: "alice@x.com", Subject: "Hi", MessageID: "<h@x.com>",
	})
	res := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
		"uid": float64(8), "body": "hello",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if got := ft.sent[0].To; got != "alice@x.com" {
		t.Fatalf("reply must fall back to From when no Reply-To, got %q", got)
	}
}

func TestReplyTool_AlreadyRePrefixed(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 9, From: "a@x.com", Subject: "Re: Hi"})
	res := NewReplyTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"uid": float64(9), "body": "ok"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if ft.sent[0].Subject != "Re: Hi" {
		t.Fatalf("must not double-prefix Re:, got %q", ft.sent[0].Subject)
	}
}

func TestReplyTool_MissingBody(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 1, From: "a@x.com"})
	res := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{"uid": float64(1)})
	if !res.IsError || !strings.Contains(res.ForLLM, "body is required") {
		t.Fatalf("expected body-required error, got %+v", res)
	}
}

func TestEmailToolset_NamesAndScope(t *testing.T) {
	set := EmailToolset(EmailTransports{"ws_test": newFakeTransport()})
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
	tool := NewReadInboxTool(EmailTransports{"ws_test": ft})
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
	tool := NewReadInboxTool(EmailTransports{"ws_test": ft})
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
	res1 := NewReadInboxTool(EmailTransports{"ws_test": ft1}).Execute(context.Background(), map[string]any{})
	res2 := NewReadInboxTool(EmailTransports{"ws_test": ft2}).Execute(context.Background(), map[string]any{})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("read_inbox returned identical results for different transports")
	}
}

// TestSearchEmailTool_NoTransport verifies the nil-transport guard.
// Traces to: pkg/tools/email.go SearchEmailTool.Execute
func TestSearchEmailTool_NoTransport(t *testing.T) {
	res := NewSearchEmailTool(nil /* no mailboxes */).Execute(context.Background(), map[string]any{"query": "hello"})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

// TestSearchEmailTool_TransportError verifies the transport-error path.
// Traces to: pkg/tools/email.go SearchEmailTool.Execute
func TestSearchEmailTool_TransportError(t *testing.T) {
	ft := newFakeTransport()
	ft.failRead = true
	res := NewSearchEmailTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"query": "q"})
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
	res1 := NewSearchEmailTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"query": "invoice"})
	res2 := NewSearchEmailTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"query": "win money"})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("search_email returned same output for different queries")
	}
}

// TestReadMessageTool_NoTransport verifies the nil-transport guard.
// Traces to: pkg/tools/email.go ReadMessageTool.Execute
func TestReadMessageTool_NoTransport(t *testing.T) {
	res := NewReadMessageTool(nil /* no mailboxes */).Execute(context.Background(), map[string]any{"uid": float64(1)})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

// TestReadMessageTool_TransportError verifies that a transport-level error
// (not-found) is surfaced as an error result.
// Traces to: pkg/tools/email.go ReadMessageTool.Execute
func TestReadMessageTool_TransportError(t *testing.T) {
	ft := newFakeTransport() // empty — UID 99 won't be found
	res := NewReadMessageTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"uid": float64(99)})
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
	res1 := NewReadMessageTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"uid": float64(1)})
	res2 := NewReadMessageTool(
		EmailTransports{"ws_test": ft},
	).Execute(context.Background(), map[string]any{"uid": float64(2)})
	if res1.ForLLM == res2.ForLLM {
		t.Fatal("read_message returned same output for different UIDs")
	}
}

// TestSendEmailTool_MissingBody verifies that a missing body returns an error.
// Traces to: pkg/tools/email.go SendEmailTool.Execute
func TestSendEmailTool_MissingBody(t *testing.T) {
	res := NewSendEmailTool(
		EmailTransports{"ws_test": newFakeTransport()},
	).Execute(context.Background(), map[string]any{
		"to": "d@x.com", "subject": "s",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "body is required") {
		t.Fatalf("expected body-required error, got %+v", res)
	}
}

// TestSendEmailTool_NoTransport verifies the nil-transport guard.
// Traces to: pkg/tools/email.go SendEmailTool.Execute
func TestSendEmailTool_NoTransport(t *testing.T) {
	res := NewSendEmailTool(nil /* no mailboxes */).Execute(context.Background(), map[string]any{
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
	res1 := NewSendEmailTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
		"to": "alice@x.com", "subject": "Hi", "body": "Hello",
	})
	res2 := NewSendEmailTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	NewSendEmailTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	res := NewReplyTool(nil /* no mailboxes */).Execute(context.Background(), map[string]any{
		"uid": float64(1), "body": "reply",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "no mailbox") {
		t.Fatalf("expected no-mailbox error, got %+v", res)
	}
}

// TestReplyTool_MissingUID verifies the missing UID guard.
// Traces to: pkg/tools/email.go ReplyTool.Execute
func TestReplyTool_MissingUID(t *testing.T) {
	res := NewReplyTool(EmailTransports{"ws_test": newFakeTransport()}).Execute(context.Background(), map[string]any{
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
	res := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	res := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	res := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	res1 := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
		"uid": float64(1), "body": "ok",
	})
	res2 := NewReplyTool(EmailTransports{"ws_test": ft}).Execute(context.Background(), map[string]any{
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
	res := NewReadMessageTool(
		EmailTransports{"ws_test": newFakeTransport()},
	).Execute(context.Background(), map[string]any{})
	if !res.IsError || !strings.Contains(res.ForLLM, "uid is required") {
		t.Fatalf("expected uid-required error, got %+v", res)
	}
}

// ── before_uid cursor validation + body-search arg (remediation) ────────────
//
// A present-but-malformed before_uid must be REJECTED, never silently coerced to
// 0 (which would rerun as page 1 — re-processing messages / never terminating a
// pagination loop). An absent before_uid stays valid (no cursor).

func TestReadInboxTool_InvalidBeforeUIDErrors(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 1, From: "a@x.com", Subject: "One"})
	for _, bad := range []any{"notanumber", float64(0), float64(-3), true} {
		res := NewReadInboxTool(EmailTransports{"ws_test": ft}).Execute(
			context.Background(), map[string]any{"before_uid": bad})
		if !res.IsError || !strings.Contains(res.ForLLM, "before_uid must be a positive integer") {
			t.Fatalf("before_uid=%v (%T) must error, got %+v", bad, bad, res)
		}
	}
}

func TestSearchEmailTool_InvalidBeforeUIDErrors(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 1, From: "a@x.com", Subject: "Hit"})
	res := NewSearchEmailTool(EmailTransports{"ws_test": ft}).Execute(
		context.Background(), map[string]any{"query": "Hit", "before_uid": "bogus"})
	if !res.IsError || !strings.Contains(res.ForLLM, "before_uid must be a positive integer") {
		t.Fatalf("invalid before_uid on search must error, got %+v", res)
	}
}

func TestReadInboxTool_AbsentBeforeUIDStaysValid(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 1, From: "a@x.com", Subject: "One"})
	res := NewReadInboxTool(EmailTransports{"ws_test": ft}).Execute(
		context.Background(), map[string]any{})
	if res.IsError {
		t.Fatalf("absent before_uid must remain valid (no cursor): %s", res.ForLLM)
	}
}

func TestReadInboxTool_ValidBeforeUIDFilters(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "a@x.com", Subject: "One"},
		email.Message{UID: 2, From: "b@x.com", Subject: "Two"},
		email.Message{UID: 3, From: "c@x.com", Subject: "Three"},
	)
	res := NewReadInboxTool(EmailTransports{"ws_test": ft}).Execute(
		context.Background(), map[string]any{"before_uid": float64(3)})
	if res.IsError {
		t.Fatalf("valid before_uid must not error: %s", res.ForLLM)
	}
	var got []email.Message
	if err := json.Unmarshal([]byte(res.ForLLM), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("before_uid=3 over {1,2,3} must return 2 messages, got %d", len(got))
	}
	for _, m := range got {
		if m.UID >= 3 {
			t.Fatalf("before_uid=3 must exclude UID>=3, got %d", m.UID)
		}
	}
}

func TestSearchEmailTool_BodyArgTriggersBodyMatch(t *testing.T) {
	ft := newFakeTransport(
		email.Message{UID: 1, From: "news@x.com", Subject: "Digest", Body: "the pineapple ships Tuesday"},
	)
	// Header-only search (default) must NOT match a body-only term.
	resNoBody := NewSearchEmailTool(EmailTransports{"ws_test": ft}).Execute(
		context.Background(), map[string]any{"query": "pineapple"})
	var got1 email.SearchResult
	if err := json.Unmarshal([]byte(resNoBody.ForLLM), &got1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got1.TotalMatches != 0 {
		t.Fatalf("without body=true a body-only term must not match; got %d", got1.TotalMatches)
	}
	// body=true opts into the body scan and matches.
	resBody := NewSearchEmailTool(EmailTransports{"ws_test": ft}).Execute(
		context.Background(), map[string]any{"query": "pineapple", "body": true})
	var got2 email.SearchResult
	if err := json.Unmarshal([]byte(resBody.ForLLM), &got2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got2.TotalMatches != 1 || len(got2.Messages) != 1 || got2.Messages[0].UID != 1 {
		t.Fatalf("body=true must match body content; got %+v", got2)
	}
}

// ── Workspace resolution (per-(agent, workspace) mailboxes) ──────────────────
//
// The tools hold the agent's full workspace→transport map and pick the
// transport for the CURRENT turn via tools.ToolWorkspaceID(ctx) — never from a
// model-supplied parameter. These tests pin that contract.

func TestEmailTransports_ResolveBoundWorkspace(t *testing.T) {
	ftA := newFakeTransport(email.Message{UID: 1, From: "a@x.com", Subject: "in-A", Seen: false})
	ftB := newFakeTransport(email.Message{UID: 9, From: "b@x.com", Subject: "in-B", Seen: false})
	tool := NewReadInboxTool(EmailTransports{"ws_a": ftA, "ws_b": ftB})

	// Turn bound to ws_b → reads ws_b's inbox, never ws_a's.
	ctx := WithWorkspaceID(context.Background(), "ws_b")
	res := tool.Execute(ctx, map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "in-B") || strings.Contains(res.ForLLM, "in-A") {
		t.Fatalf("must read ONLY the bound workspace's inbox, got %s", res.ForLLM)
	}
}

func TestEmailTransports_BoundWorkspaceWithoutMailboxErrors(t *testing.T) {
	tool := NewReadInboxTool(EmailTransports{"ws_a": newFakeTransport()})
	ctx := WithWorkspaceID(context.Background(), "ws_other")
	res := tool.Execute(ctx, map[string]any{})
	if !res.IsError {
		t.Fatalf("expected error for a workspace without a mailbox, got %+v", res)
	}
	// The error names the current workspace and the ones that DO have a mailbox.
	if !strings.Contains(res.ForLLM, "ws_other") || !strings.Contains(res.ForLLM, "ws_a") {
		t.Fatalf("error must name the workspaces, got %s", res.ForLLM)
	}
}

func TestEmailTransports_UnboundTurnSingleMailboxFallsBack(t *testing.T) {
	ft := newFakeTransport(email.Message{UID: 1, From: "a@x.com", Subject: "solo", Seen: false})
	tool := NewReadInboxTool(EmailTransports{"ws_only": ft})
	// No workspace bound to the turn + exactly one mailbox → graceful fallback.
	res := tool.Execute(context.Background(), map[string]any{})
	if res.IsError {
		t.Fatalf("single-mailbox fallback must work, got %s", res.ForLLM)
	}
}

func TestEmailTransports_UnboundTurnMultipleMailboxesAmbiguous(t *testing.T) {
	tool := NewReadInboxTool(EmailTransports{
		"ws_a": newFakeTransport(),
		"ws_b": newFakeTransport(),
	})
	// No workspace bound + several mailboxes → refuse to guess between roles.
	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatalf("ambiguous resolution must error, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "ws_a") || !strings.Contains(res.ForLLM, "ws_b") {
		t.Fatalf("error must list the candidate workspaces, got %s", res.ForLLM)
	}
}

func TestEmailTransports_SendGoesToBoundWorkspaceMailbox(t *testing.T) {
	ftA := newFakeTransport()
	ftB := newFakeTransport()
	tool := NewSendEmailTool(EmailTransports{"ws_a": ftA, "ws_b": ftB})
	ctx := WithWorkspaceID(context.Background(), "ws_a")
	res := tool.Execute(ctx, map[string]any{"to": "x@y.com", "subject": "s", "body": "b"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if len(ftA.sent) != 1 || len(ftB.sent) != 0 {
		t.Fatalf("send must use ONLY the bound workspace's transport (A=%d B=%d)", len(ftA.sent), len(ftB.sent))
	}
}
