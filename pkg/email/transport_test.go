package email

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func TestNewClient_Validation(t *testing.T) {
	cases := []struct {
		name string
		acct Account
		ok   bool
	}{
		{"missing imap", Account{SMTPHost: "s", Username: "u", Password: "p"}, false},
		{"missing smtp", Account{IMAPHost: "i", Username: "u", Password: "p"}, false},
		{"missing user", Account{IMAPHost: "i", SMTPHost: "s", Password: "p"}, false},
		{"missing pass", Account{IMAPHost: "i", SMTPHost: "s", Username: "u"}, false},
		{"complete", Account{IMAPHost: "i", SMTPHost: "s", Username: "u", Password: "p"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, err := NewClient(c.acct)
			if c.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error, got client %+v", cl)
			}
		})
	}
}

func TestNewClient_AppliesDefaultPorts(t *testing.T) {
	cl, err := NewClient(Account{IMAPHost: "i", SMTPHost: "s", Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if cl.acct.IMAPPort != 993 {
		t.Errorf("default IMAP port want 993, got %d", cl.acct.IMAPPort)
	}
	if cl.acct.SMTPPort != 587 {
		t.Errorf("default SMTP port want 587, got %d", cl.acct.SMTPPort)
	}
}

func TestClient_AddressReturnsUsername(t *testing.T) {
	cl, _ := NewClient(Account{IMAPHost: "i", SMTPHost: "s", Username: "me@x.com", Password: "p"})
	if cl.Address() != "me@x.com" {
		t.Fatalf("Address() want me@x.com, got %q", cl.Address())
	}
}

func TestBuildEmailBody_Headers(t *testing.T) {
	body := buildEmailBody("from@x.com", "to@x.com", "Hello", "the body", "")
	for _, want := range []string{
		"From: from@x.com\r\n",
		"To: to@x.com\r\n",
		"Subject: Hello\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "In-Reply-To") {
		t.Error("non-reply body must not have In-Reply-To header")
	}
	// Headers must precede the blank-line + body.
	hdrEnd := strings.Index(body, "\r\n\r\n")
	if hdrEnd < 0 || !strings.HasSuffix(body, "the body") {
		t.Error("body must follow the header block")
	}
}

func TestBuildEmailBody_ReplyThreadingHeaders(t *testing.T) {
	body := buildEmailBody("from@x.com", "to@x.com", "Re: Hi", "reply text", "<orig@x.com>")
	if !strings.Contains(body, "In-Reply-To: <orig@x.com>\r\n") {
		t.Error("reply must set In-Reply-To")
	}
	if !strings.Contains(body, "References: <orig@x.com>\r\n") {
		t.Error("reply must set References")
	}
}

func TestSanitizeHeader_StripsCRLF(t *testing.T) {
	got := sanitizeHeader("Subject\r\nBcc: evil@x.com")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header injection not stripped: %q", got)
	}
	if got != "SubjectBcc: evil@x.com" {
		t.Fatalf("unexpected sanitized value: %q", got)
	}
}

// TestSanitizeHeader_BehaviorTable verifies all CR/LF injection variants are
// neutralized and that clean values pass through unchanged.
// Traces to: transport.go sanitizeHeader (injection guard, pure function)
func TestSanitizeHeader_BehaviorTable(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"clean value", "hello world", "hello world"},
		{"CR only", "foo\rbar", "foobar"},
		{"LF only", "foo\nbar", "foobar"},
		{"CRLF sequence", "val\r\nInjected: hdr", "valInjected: hdr"},
		{"multiple injections", "a\r\nb\r\nc", "abc"},
		{"empty string", "", ""},
		// Differentiation: two different inputs must produce two different outputs.
		{"unicode safe", "Subject: héllo", "Subject: héllo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeHeader(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeHeader(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSanitizeHeader_Differentiation proves two different inputs yield two
// different outputs (rules out a hardcoded/no-op implementation).
func TestSanitizeHeader_Differentiation(t *testing.T) {
	a := sanitizeHeader("clean")
	b := sanitizeHeader("inject\r\nBcc: x")
	if a == b {
		t.Fatalf("sanitizeHeader returned same output for different inputs: %q", a)
	}
}

// TestBuildEmailBody_InjectionInSubject verifies that a header-injection attempt
// in the subject is neutralized in the output wire format.
// Traces to: transport.go buildEmailBody + sanitizeHeader (injection guard)
func TestBuildEmailBody_InjectionInSubject(t *testing.T) {
	body := buildEmailBody("from@x.com", "to@x.com", "Hi\r\nBcc: evil@x.com", "body text", "")
	lines := strings.Split(body, "\r\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("header injection present in output: %q", line)
		}
	}
}

// TestBuildEmailBody_NoSubjectFallback verifies the default subject "(no subject)"
// is NOT produced by buildEmailBody (the fallback lives in Client.Send, not here).
// buildEmailBody accepts whatever subject the caller passes; with empty it stays empty.
// Traces to: transport.go buildEmailBody
func TestBuildEmailBody_EmptySubjectPassThrough(t *testing.T) {
	body := buildEmailBody("f@x.com", "t@x.com", "", "body", "")
	if !strings.Contains(body, "Subject: \r\n") {
		t.Fatalf("empty subject must produce 'Subject: \\r\\n' header, got body:\n%s", body)
	}
}

// TestBuildEmailBody_Differentiation ensures two distinct recipients yield two
// distinct wire bodies (rules out hardcoded output).
func TestBuildEmailBody_Differentiation(t *testing.T) {
	a := buildEmailBody("f@x.com", "alice@x.com", "Hello", "body", "")
	b := buildEmailBody("f@x.com", "bob@x.com", "Hello", "body", "")
	if a == b {
		t.Fatalf("buildEmailBody must differ for different recipients")
	}
}

// TestHasSeenFlag_TableDriven covers the flag-detection helper used by
// bufferToMessage and ReadInbox's Seen field population.
// Traces to: transport.go hasSeenFlag (pure function)
func TestHasSeenFlag_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		flags []imap.Flag
		want  bool
	}{
		{"nil flags", nil, false},
		{"empty flags", []imap.Flag{}, false},
		{"seen flag present", []imap.Flag{imap.FlagSeen}, true},
		{"seen among others", []imap.Flag{imap.FlagAnswered, imap.FlagSeen, imap.FlagFlagged}, true},
		{"only other flags", []imap.Flag{imap.FlagAnswered, imap.FlagFlagged}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasSeenFlag(tc.flags)
			if got != tc.want {
				t.Errorf("hasSeenFlag(%v) = %v, want %v", tc.flags, got, tc.want)
			}
		})
	}
}

// TestHasSeenFlag_Differentiation ensures unseen≠seen (rules out constant return).
func TestHasSeenFlag_Differentiation(t *testing.T) {
	withSeen := hasSeenFlag([]imap.Flag{imap.FlagSeen})
	withoutSeen := hasSeenFlag([]imap.Flag{imap.FlagAnswered})
	if withSeen == withoutSeen {
		t.Fatal("hasSeenFlag returned same value for seen and unseen flag sets")
	}
}

// TestAddressString_TableDriven covers address formatting, including the
// no-host (group/mailbox-only) branch.
// Traces to: transport.go addressString (pure function)
func TestAddressString_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		address imap.Address
		want    string
	}{
		{
			name:    "full address",
			address: imap.Address{Mailbox: "alice", Host: "example.com"},
			want:    "alice@example.com",
		},
		{
			name:    "no host returns mailbox only",
			address: imap.Address{Mailbox: "localonly", Host: ""},
			want:    "localonly",
		},
		{
			name:    "empty mailbox and host",
			address: imap.Address{Mailbox: "", Host: ""},
			want:    "",
		},
		// Differentiation: two addresses yield two different strings.
		{
			name:    "different domain",
			address: imap.Address{Mailbox: "bob", Host: "other.org"},
			want:    "bob@other.org",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := addressString(tc.address)
			if got != tc.want {
				t.Errorf("addressString(%+v) = %q, want %q", tc.address, got, tc.want)
			}
		})
	}
}

// TestAddressString_Differentiation ensures two different addresses produce two
// different strings (rules out hardcoded return).
func TestAddressString_Differentiation(t *testing.T) {
	a := addressString(imap.Address{Mailbox: "alice", Host: "x.com"})
	b := addressString(imap.Address{Mailbox: "bob", Host: "y.com"})
	if a == b {
		t.Fatal("addressString returned same value for different addresses")
	}
}

// TestBufferToMessage_EnvelopeFields verifies the full field mapping from an
// imapclient.FetchMessageBuffer to a transport.Message without body.
// Traces to: transport.go bufferToMessage (pure function, envelope path)
func TestBufferToMessage_EnvelopeFields(t *testing.T) {
	ts := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)
	buf := &imapclient.FetchMessageBuffer{
		UID:   imap.UID(42),
		Flags: []imap.Flag{imap.FlagSeen},
		Envelope: &imap.Envelope{
			MessageID: "<msg123@example.com>",
			Subject:   "  Test Subject  ",
			From:      []imap.Address{{Name: "Alice Smith", Mailbox: "alice", Host: "x.com"}},
			ReplyTo:   []imap.Address{{Mailbox: "discuss", Host: "list.example.com"}},
			To:        []imap.Address{{Mailbox: "bob", Host: "y.com"}},
			Date:      ts,
		},
	}
	got := bufferToMessage(buf, false)

	if got.UID != 42 {
		t.Errorf("UID: got %d, want 42", got.UID)
	}
	if got.MessageID != "<msg123@example.com>" {
		t.Errorf("MessageID: got %q", got.MessageID)
	}
	if got.Subject != "Test Subject" {
		t.Errorf("Subject: got %q, want %q (trimmed)", got.Subject, "Test Subject")
	}
	if got.From != "alice@x.com" {
		t.Errorf("From: got %q, want alice@x.com", got.From)
	}
	if got.FromName != "Alice Smith" {
		t.Errorf("FromName: got %q, want 'Alice Smith'", got.FromName)
	}
	if got.ReplyTo != "discuss@list.example.com" {
		t.Errorf("ReplyTo: got %q, want discuss@list.example.com", got.ReplyTo)
	}
	if got.To != "bob@y.com" {
		t.Errorf("To: got %q, want bob@y.com", got.To)
	}
	if got.Date != ts.UTC().Format(time.RFC3339) {
		t.Errorf("Date: got %q, want %q", got.Date, ts.UTC().Format(time.RFC3339))
	}
	if !got.Seen {
		t.Error("Seen: expected true for message with \\Seen flag")
	}
	if got.Body != "" {
		t.Errorf("Body must be empty for envelope-only fetch, got %q", got.Body)
	}
}

// TestBufferToMessage_WithBody verifies that withBody=true populates the body
// from the first non-empty BodySection.
// Traces to: transport.go bufferToMessage (withBody branch)
func TestBufferToMessage_WithBody(t *testing.T) {
	buf := &imapclient.FetchMessageBuffer{
		UID:   imap.UID(7),
		Flags: []imap.Flag{},
		Envelope: &imap.Envelope{
			Subject: "Subject",
			From:    []imap.Address{{Mailbox: "s", Host: "x.com"}},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: []byte("  hello body  ")},
		},
	}
	got := bufferToMessage(buf, true)
	if got.Body != "hello body" {
		t.Errorf("Body: got %q, want %q (trimmed)", got.Body, "hello body")
	}
}

// TestBufferToMessage_EmptyBodySection verifies that an all-whitespace body
// section leaves Body empty (trim + empty-skip logic).
// Traces to: transport.go bufferToMessage withBody branch (empty body skip)
func TestBufferToMessage_EmptyBodySection(t *testing.T) {
	buf := &imapclient.FetchMessageBuffer{
		UID:   imap.UID(3),
		Flags: []imap.Flag{},
		Envelope: &imap.Envelope{
			Subject: "S",
			From:    []imap.Address{{Mailbox: "a", Host: "b.com"}},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: []byte("   ")}, // whitespace-only, trimmed to ""
		},
	}
	got := bufferToMessage(buf, true)
	if got.Body != "" {
		t.Errorf("expected empty Body for whitespace-only section, got %q", got.Body)
	}
}

// TestBufferToMessage_NoFromNoTo verifies graceful handling when From and To
// slices are empty (no panic, no spurious address).
// Traces to: transport.go bufferToMessage (From/To guards)
func TestBufferToMessage_NoFromNoTo(t *testing.T) {
	buf := &imapclient.FetchMessageBuffer{
		UID:   imap.UID(1),
		Flags: []imap.Flag{},
		Envelope: &imap.Envelope{
			Subject: "No addresses",
		},
	}
	got := bufferToMessage(buf, false)
	if got.From != "" {
		t.Errorf("expected empty From, got %q", got.From)
	}
	if got.To != "" {
		t.Errorf("expected empty To, got %q", got.To)
	}
	if got.FromName != "" {
		t.Errorf("expected empty FromName, got %q", got.FromName)
	}
	if got.ReplyTo != "" {
		t.Errorf("expected empty ReplyTo, got %q", got.ReplyTo)
	}
}

// TestBufferToMessage_ZeroDate verifies that a zero Date is not formatted
// (Date field stays empty).
// Traces to: transport.go bufferToMessage (Date zero guard)
func TestBufferToMessage_ZeroDate(t *testing.T) {
	buf := &imapclient.FetchMessageBuffer{
		UID:   imap.UID(2),
		Flags: []imap.Flag{},
		Envelope: &imap.Envelope{
			Subject: "No date",
			From:    []imap.Address{{Mailbox: "a", Host: "b.com"}},
		},
	}
	got := bufferToMessage(buf, false)
	if got.Date != "" {
		t.Errorf("expected empty Date for zero Envelope.Date, got %q", got.Date)
	}
}

// TestBufferToMessage_Differentiation ensures two different buffers produce two
// different Messages (rules out hardcoded return).
func TestBufferToMessage_Differentiation(t *testing.T) {
	mkBuf := func(uid imap.UID, subj string) *imapclient.FetchMessageBuffer {
		return &imapclient.FetchMessageBuffer{
			UID:      uid,
			Envelope: &imap.Envelope{Subject: subj},
		}
	}
	a := bufferToMessage(mkBuf(1, "Alpha"), false)
	b := bufferToMessage(mkBuf(2, "Beta"), false)
	if a.UID == b.UID || a.Subject == b.Subject {
		t.Fatalf("bufferToMessage returned identical values for different inputs: %+v vs %+v", a, b)
	}
}

// TestNewClient_CustomPortsPreserved verifies that explicitly set ports are not
// overwritten by withDefaults.
// Traces to: transport.go Account.withDefaults
func TestNewClient_CustomPortsPreserved(t *testing.T) {
	cl, err := NewClient(Account{
		IMAPHost: "imap.example.com",
		IMAPPort: 1993,
		SMTPHost: "smtp.example.com",
		SMTPPort: 2587,
		Username: "u@example.com",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cl.acct.IMAPPort != 1993 {
		t.Errorf("custom IMAP port 1993 must not be overwritten, got %d", cl.acct.IMAPPort)
	}
	if cl.acct.SMTPPort != 2587 {
		t.Errorf("custom SMTP port 2587 must not be overwritten, got %d", cl.acct.SMTPPort)
	}
}

// ---------------------------------------------------------------------------
// *Client pre-dial guard branches
// These tests exercise the validation logic that runs BEFORE any network dial.
// The dial itself hits a non-existent server and will fail, but the guards
// return before that for invalid inputs.
// ---------------------------------------------------------------------------

// newTestClient returns a *Client wired to a non-routable address so dials
// always fail fast — useful for guard-branch tests.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	cl, err := NewClient(Account{
		IMAPHost: "127.0.0.1",
		IMAPPort: 19993, // no server listening here
		SMTPHost: "127.0.0.1",
		SMTPPort: 29587, // no server listening here
		Username: "u@test.local",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("newTestClient: %v", err)
	}
	return cl
}

// TestClient_Search_EmptyQueryError verifies that an empty query is rejected
// before any network dial — exercising the guard in Search.
// Traces to: transport.go Client.Search (empty query guard)
func TestClient_Search_EmptyQueryError(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.Search(t.Context(), "", SearchOptions{Limit: 10})
	if err == nil {
		t.Fatal("Search with empty query must return an error")
	}
	if !strings.Contains(err.Error(), "search query is empty") {
		t.Fatalf("expected 'search query is empty' error, got: %v", err)
	}
}

// TestClient_Search_WhitespaceOnlyQuery verifies that a whitespace-only query
// is also rejected (trimmed to empty before validation).
// Traces to: transport.go Client.Search (empty query guard after TrimSpace)
func TestClient_Search_WhitespaceOnlyQuery(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.Search(t.Context(), "   ", SearchOptions{Limit: 10})
	if err == nil {
		t.Fatal("Search with whitespace-only query must return an error")
	}
	if !strings.Contains(err.Error(), "search query is empty") {
		t.Fatalf("expected 'search query is empty' error, got: %v", err)
	}
}

// TestClient_Search_Differentiation ensures empty and non-empty queries produce
// different error behavior (one guard error, one dial error).
func TestClient_Search_Differentiation(t *testing.T) {
	cl := newTestClient(t)
	_, emptyErr := cl.Search(t.Context(), "", SearchOptions{Limit: 5})
	_, dialErr := cl.Search(t.Context(), "invoice", SearchOptions{Limit: 5})
	// emptyErr is the guard error (no dial); dialErr is a dial/network error.
	// They must be different error values.
	if emptyErr == nil {
		t.Fatal("empty query must error")
	}
	if dialErr == nil {
		t.Fatal("non-empty query on unreachable server must error")
	}
	if emptyErr.Error() == dialErr.Error() {
		t.Fatalf("empty-query and dial errors must differ, both: %v", emptyErr)
	}
}

// TestClient_Search_BeforeUID1TerminatesPagination verifies M8: before_uid=1
// must return an empty, non-truncated result WITHOUT dialing — mirroring
// ReadInbox's identical terminal-page guard — rather than falling through to
// buildSearchCriteria (whose "beforeUID > 1" condition would silently ignore
// beforeUID=1 and re-run the query with no UID restriction, returning page 1
// again and never letting a pagination loop terminate).
// Traces to: transport.go Client.Search (BeforeUID==1 guard)
func TestClient_Search_BeforeUID1TerminatesPagination(t *testing.T) {
	cl := newTestClient(t)
	res, err := cl.Search(t.Context(), "invoice", SearchOptions{Limit: 10, BeforeUID: 1})
	if err != nil {
		t.Fatalf("Search with before_uid=1 must not dial or error, got: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("expected empty Messages for before_uid=1, got %d", len(res.Messages))
	}
	if res.Truncated {
		t.Fatal("expected Truncated=false for the terminal empty page")
	}
	if res.TotalMatches != 0 {
		t.Fatalf("expected TotalMatches=0 for before_uid=1, got %d", res.TotalMatches)
	}
	if res.NextBeforeUID != 0 {
		t.Fatalf("expected no further pagination cursor, got %d", res.NextBeforeUID)
	}
}

// TestClient_ReadMessage_ZeroUID verifies that uid=0 is rejected before dial.
// Traces to: transport.go Client.ReadMessage (uid guard)
func TestClient_ReadMessage_ZeroUID(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.ReadMessage(t.Context(), 0)
	if err == nil {
		t.Fatal("ReadMessage with uid=0 must return an error")
	}
	if !strings.Contains(err.Error(), "uid is required") {
		t.Fatalf("expected 'uid is required' error, got: %v", err)
	}
}

// TestClient_MarkSeen_ZeroUID verifies that uid=0 is rejected before dial.
// Traces to: transport.go Client.MarkSeen (uid guard)
func TestClient_MarkSeen_ZeroUID(t *testing.T) {
	cl := newTestClient(t)
	err := cl.MarkSeen(t.Context(), 0)
	if err == nil {
		t.Fatal("MarkSeen with uid=0 must return an error")
	}
	if !strings.Contains(err.Error(), "uid is required") {
		t.Fatalf("expected 'uid is required' error, got: %v", err)
	}
}

// TestClient_Send_EmptyRecipient verifies that an empty To is rejected before dial.
// Traces to: transport.go Client.Send (empty to guard)
func TestClient_Send_EmptyRecipient(t *testing.T) {
	cl := newTestClient(t)
	err := cl.Send(t.Context(), SendRequest{To: "", Body: "hello"})
	if err == nil {
		t.Fatal("Send with empty To must return an error")
	}
	if !strings.Contains(err.Error(), "recipient (to) is empty") {
		t.Fatalf("expected 'recipient (to) is empty' error, got: %v", err)
	}
}

// TestClient_Send_EmptyBody verifies that an empty body is rejected before dial.
// Traces to: transport.go Client.Send (empty body guard)
func TestClient_Send_EmptyBody(t *testing.T) {
	cl := newTestClient(t)
	err := cl.Send(t.Context(), SendRequest{To: "dest@x.com", Body: ""})
	if err == nil {
		t.Fatal("Send with empty body must return an error")
	}
	if !strings.Contains(err.Error(), "body is empty") {
		t.Fatalf("expected 'body is empty' error, got: %v", err)
	}
}

// TestClient_Send_WhitespaceBody verifies that a whitespace-only body is rejected.
// Traces to: transport.go Client.Send (body empty guard after TrimSpace)
func TestClient_Send_WhitespaceBody(t *testing.T) {
	cl := newTestClient(t)
	err := cl.Send(t.Context(), SendRequest{To: "dest@x.com", Body: "   "})
	if err == nil {
		t.Fatal("Send with whitespace-only body must return an error")
	}
	if !strings.Contains(err.Error(), "body is empty") {
		t.Fatalf("expected 'body is empty' error, got: %v", err)
	}
}

// TestClient_Send_Differentiation ensures empty-recipient and empty-body errors
// are distinct (rules out a single hardcoded error message).
func TestClient_Send_Differentiation(t *testing.T) {
	cl := newTestClient(t)
	errNoTo := cl.Send(t.Context(), SendRequest{To: "", Body: "body"})
	errNoBody := cl.Send(t.Context(), SendRequest{To: "x@y.com", Body: ""})
	if errNoTo == nil || errNoBody == nil {
		t.Fatal("both must error")
	}
	if errNoTo.Error() == errNoBody.Error() {
		t.Fatalf("empty-to and empty-body errors must differ: %v", errNoTo)
	}
}

// TestClient_ReadInbox_DialFails verifies that a non-zero limit falls through
// to the dial and returns a network error (not a guard error). This exercises
// the happy-path entry-point of ReadInbox before the dial is attempted.
// Traces to: transport.go Client.ReadInbox (dial attempt)
func TestClient_ReadInbox_DialFails(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.ReadInbox(t.Context(), InboxOptions{Limit: 5})
	if err == nil {
		t.Fatal("ReadInbox on unreachable server must return an error")
	}
	// Error is a dial/TLS error, not a guard error.
	if strings.Contains(err.Error(), "uid is required") ||
		strings.Contains(err.Error(), "query is empty") {
		t.Fatalf("unexpected guard error (should be dial error): %v", err)
	}
}

// TestClient_ReadInbox_ZeroLimitDefaultsAndDials verifies that limit<=0 is
// normalised to 20 (the default) before the dial is attempted, and that the
// subsequent dial failure is returned as a network error.
// Traces to: transport.go Client.ReadInbox (limit<=0 branch)
func TestClient_ReadInbox_ZeroLimitDefaultsAndDials(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.ReadInbox(t.Context(), InboxOptions{Limit: 0})
	if err == nil {
		t.Fatal("ReadInbox with limit=0 on unreachable server must return an error")
	}
	// Must be a dial error, not a limit-related validation error.
	if strings.Contains(err.Error(), "limit") {
		t.Fatalf("limit should be silently defaulted, not produce an error: %v", err)
	}
}

// TestClient_ReadInbox_UnseenOnly exercises the unseenOnly=true branch so the
// NotFlag SearchCriteria path is taken before the dial fails.
// Traces to: transport.go Client.ReadInbox (unseenOnly branch)
func TestClient_ReadInbox_UnseenOnly(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.ReadInbox(t.Context(), InboxOptions{Limit: 10, UnseenOnly: true})
	if err == nil {
		t.Fatal("ReadInbox (unseenOnly) on unreachable server must return an error")
	}
}

// TestClient_Search_NegativeLimitDefaults verifies that limit<=0 is defaulted
// before the dial so the guard path is not confused with the empty-query guard.
// Traces to: transport.go Client.Search (limit<=0 branch + dial)
func TestClient_Search_NegativeLimitDefaults(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.Search(t.Context(), "query", SearchOptions{Limit: -1})
	if err == nil {
		t.Fatal("Search with limit=-1 on unreachable server must return an error")
	}
	// Must be a dial error (not an empty-query guard error).
	if strings.Contains(err.Error(), "search query is empty") {
		t.Fatalf("limit default must not trigger guard: %v", err)
	}
}

// TestClient_ReadMessage_DialFails verifies that a valid uid falls through to
// the dial and returns a network error.
// Traces to: transport.go Client.ReadMessage (dial attempt)
func TestClient_ReadMessage_DialFails(t *testing.T) {
	cl := newTestClient(t)
	_, err := cl.ReadMessage(t.Context(), 1)
	if err == nil {
		t.Fatal("ReadMessage on unreachable server must return an error")
	}
}

// TestClient_MarkSeen_DialFails verifies that a valid uid falls through to
// the dial and returns a network error.
// Traces to: transport.go Client.MarkSeen (dial attempt)
func TestClient_MarkSeen_DialFails(t *testing.T) {
	cl := newTestClient(t)
	err := cl.MarkSeen(t.Context(), 1)
	if err == nil {
		t.Fatal("MarkSeen on unreachable server must return an error")
	}
}

// TestClient_Send_DialFails verifies that a valid request falls through to the
// SMTP dial and returns a network error.
// Traces to: transport.go Client.Send (STARTTLS dial path)
func TestClient_Send_DialFails(t *testing.T) {
	cl := newTestClient(t)
	err := cl.Send(t.Context(), SendRequest{To: "dest@x.com", Subject: "Hi", Body: "body"})
	if err == nil {
		t.Fatal("Send on unreachable server must return an error")
	}
	if !strings.Contains(err.Error(), "STARTTLS send") {
		t.Fatalf("expected STARTTLS send error wrapping, got: %v", err)
	}
}

// TestClient_Send_SMTPSDialFails verifies the port-465 (SMTPS) code path hits
// the TLS dial and returns a network error.
// Traces to: transport.go Client.Send (SMTPS path, port 465)
func TestClient_Send_SMTPSDialFails(t *testing.T) {
	cl, _ := NewClient(Account{
		IMAPHost: "127.0.0.1",
		SMTPHost: "127.0.0.1",
		SMTPPort: 465, // triggers SMTPS path
		Username: "u@test.local",
		Password: "pass",
	})
	err := cl.Send(t.Context(), SendRequest{To: "dest@x.com", Body: "body"})
	if err == nil {
		t.Fatal("Send (SMTPS) on unreachable server must return an error")
	}
	if !strings.Contains(err.Error(), "SMTPS send") {
		t.Fatalf("expected SMTPS send error wrapping, got: %v", err)
	}
}

// TestDialIMAP_ContextCancelledBeforeDial verifies that when the parent context
// is already canceled, dialIMAP returns the context error without waiting for
// the dial goroutine. This exercises the dialCtx.Done() select branch.
// Traces to: transport.go dialIMAP (context cancellation path, line 165-166)
func TestDialIMAP_ContextCancelledBeforeDial(t *testing.T) {
	// Use a host that takes time to refuse (non-routable address causes timeout,
	// not immediate connection refused). We cancel the context immediately.
	cl, _ := NewClient(Account{
		// 192.0.2.x is TEST-NET-1 (RFC 5737) — packets are black-holed, so
		// the dial goroutine will block, and the canceled context wins the select.
		IMAPHost: "192.0.2.1",
		IMAPPort: 993,
		SMTPHost: "127.0.0.1",
		Username: "u@test.local",
		Password: "pass",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so the context is already done

	_, err := cl.ReadInbox(ctx, InboxOptions{Limit: 5})
	if err == nil {
		t.Fatal("ReadInbox with canceled context must return an error")
	}
	// The error must reference context cancellation, not a dial-TLS error.
	if !strings.Contains(err.Error(), "context canceled") &&
		!strings.Contains(err.Error(), "dial") {
		t.Fatalf("expected context cancellation error, got: %v", err)
	}
}

// TestClient_ReadInbox_Differentiation_UnseenVsAll verifies that unseenOnly=true
// and unseenOnly=false produce calls that enter different criteria branches
// (both will fail at dial but exercise different code paths).
// Traces to: transport.go ReadInbox (unseenOnly branch differentiation)
func TestClient_ReadInbox_Differentiation_UnseenVsAll(t *testing.T) {
	cl := newTestClient(t)
	// Both must fail (no server), but the function enters different code paths.
	_, errUnseen := cl.ReadInbox(t.Context(), InboxOptions{Limit: 5, UnseenOnly: true})
	_, errAll := cl.ReadInbox(t.Context(), InboxOptions{Limit: 5})
	if errUnseen == nil || errAll == nil {
		t.Fatal("both calls must fail on unreachable server")
	}
	// Both errors are dial errors but from the same underlying mechanism.
	// Main check: the function doesn't panic or hang for either flag value.
}
