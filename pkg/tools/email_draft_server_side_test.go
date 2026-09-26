package tools

// N3(a) — spec §7 row 9 / D8 / FR-029 / FR-034: a created draft lands in the
// mailbox's Drafts folder SERVER-SIDE, carrying \Draft and X-Omnipus-Draft.
// The earlier RED test pinned the tool against a fake transport's APPEND,
// which cannot distinguish "the draft lands in Drafts with \Draft" from "the
// fake was configured to record what it was told". This test drives the real
// *email.Client against a real in-memory IMAP server (D36 loopback) and
// inspects server state.

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/email"
)

// startDraftIMAP is the pkg/tools-local mem-IMAP fixture: one user with
// INBOX + Drafts (Sent deliberately absent — the MC-9 scenario in the
// gateway send tests starts from this shape).
func startDraftIMAP(t *testing.T) (int, string, *imapclient.Client) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("mailbox@test.local", "s3cret")
	for _, name := range []string{"INBOX", "Drafts"} {
		require.NoError(t, user.Create(name, nil))
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })
	cl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	require.NoError(t, cl.Login("mailbox@test.local", "s3cret").Wait())
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(portStr, &port)
	require.NoError(t, err)
	return port, ln.Addr().String(), cl
}

// fetchDraftsRow fetches the single Drafts message's flags and raw bytes
// straight from the server.
func fetchDraftsRow(t *testing.T, addr string) ([]imap.Flag, []byte, uint32) {
	t.Helper()
	cl, err := imapclient.DialInsecure(addr, nil)
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, cl.Login("mailbox@test.local", "s3cret").Wait())
	sel, err := cl.Select("Drafts", nil).Wait()
	require.NoError(t, err)
	if sel.NumMessages != 1 {
		t.Fatalf("Drafts holds %d messages, want exactly 1 (the draft the tool saved)", sel.NumMessages)
	}
	opts := &imap.FetchOptions{
		Flags:       true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}
	secs, ferr := cl.Fetch(imap.SeqSetNum(1), opts).Collect()
	require.NoError(t, ferr)
	require.Len(t, secs, 1)
	d := secs[0]
	var raw []byte
	if len(d.BodySection) > 0 {
		raw = d.BodySection[0].Bytes
	}
	if len(raw) == 0 {
		t.Fatal("instrument: fetched Drafts row has an empty body section - the fetch cannot see message bytes, so the header assertion would be vacuous")
	}
	return d.Flags, raw, uint32(sel.UIDValidity)
}

func TestCreateEmailDraft_LandsInDraftsWithDraftFlag(t *testing.T) {
	port, addr, _ := startDraftIMAP(t)
	client, err := email.NewClient(email.Account{
		IMAPHost: "127.0.0.1",
		IMAPPort: port,
		SMTPHost: "127.0.0.1",
		SMTPPort: port,
		Username: "mailbox@test.local",
		Password: "s3cret",
	})
	require.NoError(t, err)

	const marker = "N3-SERVER-SIDE-DRAFT-MARKER-8be1c2"
	res := NewCreateEmailDraftTool(EmailTransports{"ws": client}).Execute(mailCtx(), map[string]any{
		"to":      []any{"human@example.test"},
		"subject": "N3 server-side draft",
		"body":    marker,
	})
	if res.IsError {
		t.Fatalf("create_email_draft errored: %s", res.ForLLM)
	}
	var out struct {
		Created     bool   `json:"created"`
		MessageID   string `json:"message_id"`
		UID         uint32 `json:"uid"`
		UIDValidity uint32 `json:"uidvalidity"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out))
	if !out.Created {
		t.Fatal("created = false, want true")
	}
	if out.MessageID == "" || out.UID == 0 || out.UIDValidity == 0 {
		t.Fatalf("result contract: message_id=%q uid=%d uidvalidity=%d - all three must be present",
			out.MessageID, out.UID, out.UIDValidity)
	}

	flags, raw, uv := fetchDraftsRow(t, addr)
	if uv != out.UIDValidity {
		t.Fatalf("server UIDVALIDITY = %d, tool reported %d - the result must reference the real folder", uv, out.UIDValidity)
	}
	draftFlag := false
	for _, f := range flags {
		if f == imap.FlagDraft {
			draftFlag = true
		}
	}
	if !draftFlag {
		t.Fatalf("D8/FR-029: server-side Drafts flags = %v, must include \\Draft", flags)
	}
	if !strings.Contains(string(raw), "X-Omnipus-Draft:") {
		t.Fatalf("spec row 9: saved draft lacks the X-Omnipus-Draft header; raw starts %q", firstBytes(raw, 200))
	}
	if !strings.Contains(string(raw), marker) {
		t.Fatal("the Drafts row is not the draft the tool saved (body marker absent)")
	}
}

func firstBytes(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}
