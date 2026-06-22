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
