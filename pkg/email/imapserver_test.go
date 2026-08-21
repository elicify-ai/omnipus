package email

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// These tests drive the REAL Client (search/read-inbox/read-message code paths,
// including the context-bounded IMAP commands and the BODY[]+decode wiring)
// against an in-memory IMAP server from the same go-imap module — no new
// dependency, no live server, no network. The production TLS dialer is swapped
// for a plaintext dial to the test server via the imapDial seam; production code
// never reassigns imapDial.

const (
	testIMAPUser = "mailbox@test.local"
	testIMAPPass = "s3cret"
)

// startMemIMAP boots an in-memory IMAP server, appends the given raw messages to
// INBOX (in order, so UIDs are 1..N), points imapDial at it, and returns a
// *Client wired to it. seenUIDs marks messages \Seen at append time.
func startMemIMAP(t *testing.T, msgs [][]byte, seenUIDs map[int]bool) *Client {
	t.Helper()

	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	mem.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	// Append messages through a raw client so the server assigns real UIDs.
	appendCl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial (append): %v", err)
	}
	if err = appendCl.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login (append): %v", err)
	}
	for i, raw := range msgs {
		var opts *imap.AppendOptions
		if seenUIDs[i+1] {
			opts = &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}}
		}
		cmd := appendCl.Append("INBOX", int64(len(raw)), opts)
		if _, err = cmd.Write(raw); err != nil {
			t.Fatalf("append write %d: %v", i, err)
		}
		if err = cmd.Close(); err != nil {
			t.Fatalf("append close %d: %v", i, err)
		}
		if _, err = cmd.Wait(); err != nil {
			t.Fatalf("append wait %d: %v", i, err)
		}
	}
	_ = appendCl.Close()

	prev := imapDial
	imapDial = func(addr string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(addr, nil)
	}
	t.Cleanup(func() { imapDial = prev })

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	cl, err := NewClient(Account{
		IMAPHost: host,
		IMAPPort: port,
		SMTPHost: host, // unused by these tests
		Username: testIMAPUser,
		Password: testIMAPPass,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cl
}

// mkMsg builds a minimal RFC 822 message with a distinct subject/body.
func mkMsg(subject, from, body string) []byte {
	return []byte(
		"From: " + from + "\r\n" +
			"To: mailbox@test.local\r\n" +
			"Subject: " + subject + "\r\n" +
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
			"Message-ID: <" + subject + "@test.local>\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"\r\n" + body + "\r\n")
}

func uidsOf(msgs []Message) []uint32 {
	out := make([]uint32, len(msgs))
	for i, m := range msgs {
		out[i] = m.UID
	}
	return out
}

func eqUint32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSearch_TruncationAndTotalMatches proves that when matches exceed the
// limit, results are capped, Truncated is set, TotalMatches reflects the full
// count, and NextBeforeUID cursors to the next (older) page.
func TestSearch_TruncationAndTotalMatches(t *testing.T) {
	var msgs [][]byte
	for i := 1; i <= 5; i++ {
		msgs = append(msgs, mkMsg(fmt.Sprintf("Report %d", i), "boss@x.com", "quarterly numbers"))
	}
	cl := startMemIMAP(t, msgs, nil) // UIDs 1..5, all match "Report"

	res, err := cl.Search(context.Background(), "Report", SearchOptions{Limit: 2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.TotalMatches != 5 {
		t.Errorf("TotalMatches = %d, want 5", res.TotalMatches)
	}
	if !res.Truncated {
		t.Error("Truncated must be true when matches (5) > limit (2)")
	}
	if got := uidsOf(res.Messages); !eqUint32(got, []uint32{5, 4}) {
		t.Errorf("page UIDs = %v, want [5 4] (newest first)", got)
	}
	// Cursor is the smallest UID on this page → next page continues below it.
	if res.NextBeforeUID != 4 {
		t.Errorf("NextBeforeUID = %d, want 4", res.NextBeforeUID)
	}
}

// TestSearch_PaginationBeforeUID proves before_uid pages deterministically
// through the match set without raising the limit.
func TestSearch_PaginationBeforeUID(t *testing.T) {
	var msgs [][]byte
	for i := 1; i <= 5; i++ {
		msgs = append(msgs, mkMsg(fmt.Sprintf("Report %d", i), "boss@x.com", "body"))
	}
	cl := startMemIMAP(t, msgs, nil) // UIDs 1..5

	// Page 2: everything strictly older than UID 4 → {1,2,3}, newest-first, cap 2.
	res, err := cl.Search(context.Background(), "Report", SearchOptions{Limit: 2, BeforeUID: 4})
	if err != nil {
		t.Fatalf("search page 2: %v", err)
	}
	if res.TotalMatches != 3 {
		t.Errorf("page2 TotalMatches = %d, want 3", res.TotalMatches)
	}
	if got := uidsOf(res.Messages); !eqUint32(got, []uint32{3, 2}) {
		t.Errorf("page2 UIDs = %v, want [3 2]", got)
	}
	if !res.Truncated || res.NextBeforeUID != 2 {
		t.Errorf("page2 Truncated=%v NextBeforeUID=%d, want true/2", res.Truncated, res.NextBeforeUID)
	}

	// Page 3: older than UID 2 → {1} only, not truncated, no cursor.
	res3, err := cl.Search(context.Background(), "Report", SearchOptions{Limit: 2, BeforeUID: 2})
	if err != nil {
		t.Fatalf("search page 3: %v", err)
	}
	if got := uidsOf(res3.Messages); !eqUint32(got, []uint32{1}) {
		t.Errorf("page3 UIDs = %v, want [1]", got)
	}
	if res3.Truncated {
		t.Error("page3 must not be truncated (1 match, limit 2)")
	}
	if res3.NextBeforeUID != 0 {
		t.Errorf("page3 NextBeforeUID = %d, want 0", res3.NextBeforeUID)
	}
}

// TestSearch_LimitClampedInGo proves the limit is clamped in Go (not merely the
// JSON schema): a request for 1000 against 101 matches returns exactly
// maxListLimit (100), with Truncated set.
func TestSearch_LimitClampedInGo(t *testing.T) {
	var msgs [][]byte
	for i := 1; i <= maxListLimit+1; i++ { // 101 messages, all match
		msgs = append(msgs, mkMsg(fmt.Sprintf("Bulk %d", i), "list@x.com", "hello"))
	}
	cl := startMemIMAP(t, msgs, nil)

	res, err := cl.Search(context.Background(), "Bulk", SearchOptions{Limit: 1000})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Messages) != maxListLimit {
		t.Errorf("returned %d messages, want clamp to %d", len(res.Messages), maxListLimit)
	}
	if res.TotalMatches != maxListLimit+1 {
		t.Errorf("TotalMatches = %d, want %d", res.TotalMatches, maxListLimit+1)
	}
	if !res.Truncated {
		t.Error("Truncated must be true (101 matches, clamped to 100)")
	}
}

// TestSearch_BodyOptIn proves the default search matches sender/subject only,
// and body=true is required to match on body content.
func TestSearch_BodyOptIn(t *testing.T) {
	msgs := [][]byte{
		mkMsg("Weekly digest", "news@x.com", "the secret pineapple ships Tuesday"),
	}
	cl := startMemIMAP(t, msgs, nil)

	// "pineapple" appears only in the body → no match without body=true.
	light, err := cl.Search(context.Background(), "pineapple", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("light search: %v", err)
	}
	if light.TotalMatches != 0 {
		t.Errorf("default search must not scan bodies; got %d matches", light.TotalMatches)
	}
	// With body=true it matches.
	heavy, err := cl.Search(context.Background(), "pineapple", SearchOptions{Limit: 10, Body: true})
	if err != nil {
		t.Fatalf("body search: %v", err)
	}
	if heavy.TotalMatches != 1 || len(heavy.Messages) != 1 {
		t.Errorf("body=true must match body content; got %+v", heavy)
	}
}

// TestReadInbox_TrailingRange proves the default read_inbox path returns the
// newest N messages (via the EXISTS-count trailing sequence range, not SEARCH
// ALL), newest first.
func TestReadInbox_TrailingRange(t *testing.T) {
	var msgs [][]byte
	for i := 1; i <= 5; i++ {
		msgs = append(msgs, mkMsg(fmt.Sprintf("Msg %d", i), "a@x.com", "body"))
	}
	cl := startMemIMAP(t, msgs, nil) // UIDs 1..5

	got, err := cl.ReadInbox(context.Background(), InboxOptions{Limit: 3})
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if u := uidsOf(got); !eqUint32(u, []uint32{5, 4, 3}) {
		t.Errorf("trailing range UIDs = %v, want [5 4 3]", u)
	}
	// Envelope-only: bodies must be empty on the list path.
	for _, m := range got {
		if m.Body != "" {
			t.Errorf("list result must be envelope-only, got body %q", m.Body)
		}
	}
}

// TestReadInbox_UnseenOnly proves unseen-only mode returns only \Unseen messages.
func TestReadInbox_UnseenOnly(t *testing.T) {
	msgs := [][]byte{
		mkMsg("One", "a@x.com", "b"),
		mkMsg("Two", "a@x.com", "b"),
		mkMsg("Three", "a@x.com", "b"),
	}
	// Mark UID 2 as \Seen at append time.
	cl := startMemIMAP(t, msgs, map[int]bool{2: true})

	got, err := cl.ReadInbox(context.Background(), InboxOptions{Limit: 10, UnseenOnly: true})
	if err != nil {
		t.Fatalf("read inbox unseen: %v", err)
	}
	if u := uidsOf(got); !eqUint32(u, []uint32{3, 1}) {
		t.Errorf("unseen UIDs = %v, want [3 1] (UID 2 was seen)", u)
	}
}

// TestReadInbox_BeforeUIDPagination proves read_inbox honours the before_uid
// cursor.
func TestReadInbox_BeforeUIDPagination(t *testing.T) {
	var msgs [][]byte
	for i := 1; i <= 5; i++ {
		msgs = append(msgs, mkMsg(fmt.Sprintf("Msg %d", i), "a@x.com", "b"))
	}
	cl := startMemIMAP(t, msgs, nil)

	got, err := cl.ReadInbox(context.Background(), InboxOptions{Limit: 2, BeforeUID: 4})
	if err != nil {
		t.Fatalf("read inbox before_uid: %v", err)
	}
	if u := uidsOf(got); !eqUint32(u, []uint32{3, 2}) {
		t.Errorf("before_uid=4 UIDs = %v, want [3 2]", u)
	}
}

// TestReadMessage_DecodesBase64ThroughServer is the end-to-end proof that the
// read path fetches the FULL message (BODY[]) and decodes MIME: a base64
// text/plain message read by UID returns the decoded text, not the base64 blob.
func TestReadMessage_DecodesBase64ThroughServer(t *testing.T) {
	const want = "Decoded through a real IMAP round-trip."
	// base64 body, assembled as a real message (encoded by the stdlib, so the
	// oracle is the plaintext, not a copied-in constant).
	b64 := base64.StdEncoding.EncodeToString([]byte(want))
	raw := []byte(
		"From: sender@x.com\r\n" +
			"To: mailbox@test.local\r\n" +
			"Subject: Encoded\r\n" +
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
			"Message-ID: <enc@test.local>\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"Content-Transfer-Encoding: base64\r\n" +
			"\r\n" + b64 + "\r\n")
	cl := startMemIMAP(t, [][]byte{raw}, nil) // UID 1

	msg, err := cl.ReadMessage(context.Background(), 1)
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	if msg.Body != want {
		t.Fatalf("body not decoded through server.\n got: %q\nwant: %q", msg.Body, want)
	}
}

// TestReadInbox_EmptyInbox proves the empty-mailbox paths return an empty result
// with no error (ReadInbox default path and Search) through the real server.
func TestReadInbox_EmptyInbox(t *testing.T) {
	cl := startMemIMAP(t, nil, nil) // INBOX created, zero messages

	got, err := cl.ReadInbox(context.Background(), InboxOptions{Limit: 10})
	if err != nil {
		t.Fatalf("empty inbox read must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty inbox must return no messages, got %d", len(got))
	}

	res, err := cl.Search(context.Background(), "anything", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("search of empty inbox must not error: %v", err)
	}
	if res.TotalMatches != 0 || len(res.Messages) != 0 {
		t.Fatalf("empty search must return zero matches, got %+v", res)
	}
	if res.Messages == nil {
		t.Fatal("empty search Messages must be a non-nil empty slice for stable JSON")
	}
}

// TestReadMessage_AttachmentOnlyMarker proves an attachment-only message read
// through the server yields a non-empty, flagged body (never a silent ""), and
// that the decoded attachment bytes do not leak into the body.
func TestReadMessage_AttachmentOnlyMarker(t *testing.T) {
	const boundary = "BOUND1"
	const secret = "BINARY_ATTACH_ONLY"
	raw := []byte(
		"From: sender@x.com\r\n" +
			"To: mailbox@test.local\r\n" +
			"Subject: Files\r\n" +
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
			"Message-ID: <att@test.local>\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
			"--" + boundary + "\r\n" +
			"Content-Type: application/octet-stream; name=\"data.bin\"\r\n" +
			"Content-Disposition: attachment; filename=\"data.bin\"\r\n" +
			"Content-Transfer-Encoding: base64\r\n\r\n" +
			base64.StdEncoding.EncodeToString([]byte(secret)) + "\r\n" +
			"--" + boundary + "--\r\n")
	cl := startMemIMAP(t, [][]byte{raw}, nil)

	msg, err := cl.ReadMessage(context.Background(), 1)
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	if msg.Body == "" {
		t.Fatal("attachment-only message must not yield an empty body (silent '')")
	}
	if !strings.Contains(msg.Body, "attachment-only") {
		t.Fatalf("attachment-only message must be flagged with a marker, got %q", msg.Body)
	}
	if strings.Contains(msg.Body, secret) {
		t.Fatalf("decoded attachment content must not appear in the body: %q", msg.Body)
	}
}

// TestSearch_CanceledContextReturnsError proves an already-canceled context
// makes an operation fail fast rather than proceed.
func TestSearch_CanceledContextReturnsError(t *testing.T) {
	cl := startMemIMAP(t, [][]byte{mkMsg("X", "a@x.com", "b")}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cl.Search(ctx, "X", SearchOptions{Limit: 5}); err == nil {
		t.Fatal("search with canceled context must return an error")
	}
}

// TestRunIMAP_DeadlineReturnsErrorNotHang proves the command-timeout mechanism:
// a slow IMAP command bounded by runIMAP returns a timeout error promptly
// instead of blocking for the command's full duration. This is what every
// UIDSearch/Fetch/Store/Login/Select goes through, so a runaway body scan on a
// huge mailbox cannot hang the turn.
func TestRunIMAP_DeadlineReturnsErrorNotHang(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := runIMAP(ctx, "slow search", func() (int, error) {
		time.Sleep(3 * time.Second) // stands in for a hung IMAP command
		return 0, nil
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("runIMAP must return an error when the deadline fires before the command completes")
	}
	if !strings.Contains(err.Error(), "slow search") {
		t.Errorf("error should name the operation, got: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("runIMAP waited %v — it must not block for the full command duration", elapsed)
	}
}
