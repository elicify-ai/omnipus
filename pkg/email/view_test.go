package email

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// startViewIMAP boots the in-memory go-imap server with INBOX/Sent/Drafts
// created, appends each raw message to its named folder, and returns the
// Client under test.
func startViewIMAP(t *testing.T, folderOf ...string) (*Client, [][]byte) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	for _, f := range []string{"INBOX", "Sent", "Drafts"} {
		if err := user.Create(f, nil); err != nil {
			t.Fatalf("create %s: %v", f, err)
		}
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

	appendCl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial (append): %v", err)
	}
	if err = appendCl.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login (append): %v", err)
	}
	folderNames := map[string]string{"inbox": "INBOX", "sent": "Sent", "drafts": "Drafts"}
	var raws [][]byte
	for i, folder := range folderOf {
		raw := mkMsg("Subject "+strconv.Itoa(i+1), "ada@box.test", "body "+strconv.Itoa(i+1))
		name, ok := folderNames[folder]
		if !ok {
			t.Fatalf("bad folder spec %q", folder)
		}
		cmd := appendCl.Append(name, int64(len(raw)), nil)
		if _, err = cmd.Write(raw); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if err = cmd.Close(); err != nil {
			t.Fatalf("append close %d: %v", i, err)
		}
		if _, err = cmd.Wait(); err != nil {
			t.Fatalf("append wait %d: %v", i, err)
		}
		raws = append(raws, raw)
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
		SMTPHost: host,
		Username: testIMAPUser,
		Password: testIMAPPass,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cl, raws
}

// mkViewMsg builds a multipart message with text/html/markdown parts, one
// attachment and one inline cid: part, optionally as an Omnipus draft.
func mkViewMsg(subject, body string, draft bool) []byte {
	var b strings.Builder
	b.WriteString("From: Ada <ada@box.test>\r\n")
	b.WriteString("To: Grace <grace@box.test>\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Date: Sat, 26 Sep 2026 08:00:00 +0000\r\n")
	b.WriteString("Message-ID: <view-test-" + subject + "@box.test>\r\n")
	if draft {
		b.WriteString("X-Omnipus-Draft: true\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(`Content-Type: multipart/mixed; boundary="VBOUND"` + "\r\n")
	b.WriteString("\r\n")

	parts := []string{}
	add := func(s string) { parts = append(parts, s) }
	add("--VBOUND\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
	add("--VBOUND\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<html><body><p>" + body + "</p></body></html>\r\n")
	if draft {
		add("--VBOUND\r\nContent-Type: text/markdown; charset=utf-8\r\n\r\n" + body + " md\r\n")
	}

	att := "--VBOUND\r\nContent-Disposition: attachment; filename=\"notes.txt\"\r\nContent-Type: text/plain\r\n\r\nNOTES-BYTES\r\n"
	add(att)
	add("--VBOUND\r\nContent-Type: image/png\r\nContent-ID: <img1@box.test>\r\n\r\nPNGDATA\r\n")
	add("--VBOUND--\r\n")
	return []byte(b.String() + strings.Join(parts, ""))
}

// viewMsg is one raw message appended to a named D5 folder slug.
type viewMsg struct {
	folder string
	raw    []byte
}

// startViewIMAPRaw is the raw-message harness: caller-supplied messages and
// an explicit capability set (UIDPLUS on/off selects DeleteDraft's branch).
func startViewIMAPRaw(t *testing.T, msgs []viewMsg, caps imap.CapSet) *Client {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	for _, f := range []string{"INBOX", "Sent", "Drafts"} {
		if err := user.Create(f, nil); err != nil {
			t.Fatalf("create %s: %v", f, err)
		}
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         caps,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	appendCl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial (append): %v", err)
	}
	if err = appendCl.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login (append): %v", err)
	}
	folderNames := map[string]string{"inbox": "INBOX", "sent": "Sent", "drafts": "Drafts"}
	for i, m := range msgs {
		name, ok := folderNames[m.folder]
		if !ok {
			t.Fatalf("bad folder spec %q", m.folder)
		}
		cmd := appendCl.Append(name, int64(len(m.raw)), nil)
		if _, err = cmd.Write(m.raw); err != nil {
			t.Fatalf("append %d: %v", i, err)
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
		SMTPHost: host,
		Username: testIMAPUser,
		Password: testIMAPPass,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cl
}
func TestView_ParseMailRef(t *testing.T) {
	valid := []struct {
		ref  string
		kind string
		vv   uint32
		uid  uint32
		mid  string
	}{
		{ref: "uid:42:7", kind: "uid", vv: 42, uid: 7},
		{ref: "  uid:42:7  ", kind: "uid", vv: 42, uid: 7},
		{ref: "mid:<a@b>", kind: "mid", mid: "<a@b>"},
	}
	for _, tc := range valid {
		r, err := parseMailRef(tc.ref)
		if err != nil {
			t.Fatalf("parseMailRef(%q): %v", tc.ref, err)
		}
		if r.kind != tc.kind || r.uidvalidity != tc.vv || r.uid != tc.uid || r.messageID != tc.mid {
			t.Fatalf("parseMailRef(%q) = %+v", tc.ref, r)
		}
	}
	bad := []string{
		"", "bogus", "uid:", "uid:1", "uid:x:y", "uid:1:0", "uid:1:2:3",
		"mid:", "mid:no-angles@x", "mid:<a@b", "mid:<a@b>extra",
		"a\r\nb", strings.Repeat("x", 1101),
	}
	for _, ref := range bad {
		if _, err := parseMailRef(ref); !errors.Is(err, ErrMailRefInvalid) {
			t.Fatalf("parseMailRef(%q): want ErrMailRefInvalid, got %v", ref, err)
		}
	}
}

func TestReadView_FullMIMEWalk(t *testing.T) {
	raw := mkViewMsg("walkme", "hello body", true)
	cl := startViewIMAPRaw(t, []viewMsg{{folder: "drafts", raw: raw}}, nil)
	v, err := cl.ReadView(context.Background(), FolderDrafts, "mid:<view-test-walkme@box.test>")
	if err != nil {
		t.Fatalf("ReadView: %v", err)
	}
	if !v.IsOmnipusDraft {
		t.Fatalf("IsOmnipusDraft = false, want true")
	}
	if strings.TrimSpace(v.TextBody) != "hello body" {
		t.Fatalf("TextBody = %q", v.TextBody)
	}
	if !v.HasHTML || !strings.Contains(v.HTMLBody, "<p>hello body</p>") {
		t.Fatalf("HTMLBody = %q, HasHTML = %v", v.HTMLBody, v.HasHTML)
	}
	if strings.TrimSpace(v.BodyMarkdown) != "hello body md" || v.MarkdownLossy {
		t.Fatalf("BodyMarkdown = %q lossy = %v; want stored markdown, lossless", v.BodyMarkdown, v.MarkdownLossy)
	}
	if len(v.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(v.Attachments))
	}
	att := v.Attachments[0]
	if att.Filename != "notes.txt" || !strings.Contains(string(att.Data), "NOTES-BYTES") || att.SizeBytes != len(att.Data) {
		t.Fatalf("attachment = %+v", att)
	}
	if len(v.Inline) != 1 || v.Inline[0].ContentID != "img1@box.test" {
		t.Fatalf("inline = %+v", v.Inline)
	}
	if v.UID == 0 || v.UIDValidity == 0 {
		t.Fatalf("uid/uidvalidity = %d/%d", v.UID, v.UIDValidity)
	}
	if v.Subject != "walkme" || v.MessageID != "view-test-walkme@box.test" {
		t.Fatalf("subject=%q mid=%q", v.Subject, v.MessageID)
	}
}
func TestReadView_UIDRefRoundTrip(t *testing.T) {
	raw := mkMsg("roundtrip", "ada@box.test", "body")
	cl := startViewIMAPRaw(t, []viewMsg{{folder: "inbox", raw: raw}}, nil)
	page, uv, _, err := cl.ReadFolderPage(context.Background(), FolderInbox, 10, 0)
	if err != nil || len(page) != 1 {
		t.Fatalf("page: err=%v len=%d", err, len(page))
	}
	ref := "uid:" + strconv.FormatUint(uint64(uv), 10) + ":" + strconv.FormatUint(uint64(page[0].UID), 10)
	v, err := cl.ReadView(context.Background(), FolderInbox, ref)
	if err != nil {
		t.Fatalf("ReadView(%s): %v", ref, err)
	}
	if v.UID != page[0].UID || v.Subject != "roundtrip" {
		t.Fatalf("view uid=%d subject=%q", v.UID, v.Subject)
	}
	missing := "uid:" + strconv.FormatUint(uint64(uv), 10) + ":999"
	if _, err := cl.ReadView(context.Background(), FolderInbox, missing); err == nil {
		t.Fatalf("missing uid %s resolved without error", missing)
	}
}

func TestFolderCounts(t *testing.T) {
	cl, _ := startViewIMAP(t, "inbox", "sent")
	stats, err := cl.FolderCounts(context.Background())
	if err != nil {
		t.Fatalf("FolderCounts: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("stats = %d, want 3", len(stats))
	}
	bySlug := map[string]FolderStat{}
	for _, s := range stats {
		bySlug[s.Slug] = s
		if s.UIDValidity == 0 {
			t.Fatalf("slug %s: uidvalidity 0", s.Slug)
		}
	}
	if bySlug[FolderInbox].Total != 1 || bySlug[FolderSent].Total != 1 || bySlug[FolderDrafts].Total != 0 {
		t.Fatalf("totals: %+v", stats)
	}
	if bySlug[FolderInbox].Unseen != 1 {
		t.Fatalf("inbox unseen = %d, want 1", bySlug[FolderInbox].Unseen)
	}
	if bySlug[FolderInbox].DisplayName != "INBOX" {
		t.Fatalf("inbox display = %q", bySlug[FolderInbox].DisplayName)
	}
}
func TestReadFolderPage_TruncationAndCursor(t *testing.T) {
	cl, _ := startViewIMAP(t, "inbox", "inbox", "inbox")
	page1, uv, trunc, err := cl.ReadFolderPage(context.Background(), FolderInbox, 2, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || !trunc {
		t.Fatalf("page1 len=%d trunc=%v; want 2, true", len(page1), trunc)
	}
	if page1[0].UID <= page1[1].UID {
		t.Fatalf("page not newest-first: %d then %d", page1[0].UID, page1[1].UID)
	}
	page2, uv2, trunc2, err := cl.ReadFolderPage(context.Background(), FolderInbox, 2, page1[1].UID)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 1 || trunc2 {
		t.Fatalf("page2 len=%d trunc=%v; want 1, false", len(page2), trunc2)
	}
	if page2[0].UID >= page1[1].UID {
		t.Fatalf("cursor not strict-below: %d vs %d", page2[0].UID, page1[1].UID)
	}
	if uv != uv2 {
		t.Fatalf("uidvalidity drifted: %d vs %d", uv, uv2)
	}
	seen := map[uint32]bool{page1[0].UID: true, page1[1].UID: true, page2[0].UID: true}
	if len(seen) != 3 {
		t.Fatalf("pages overlap or drop rows: %v", seen)
	}
}

func TestMarkSeenIn(t *testing.T) {
	cl, _ := startViewIMAP(t, "inbox")
	page, _, _, err := cl.ReadFolderPage(context.Background(), FolderInbox, 10, 0)
	if err != nil || len(page) != 1 {
		t.Fatalf("page: err=%v len=%d", err, len(page))
	}
	uid := page[0].UID
	if err := cl.MarkSeenIn(context.Background(), FolderInbox, uid); err != nil {
		t.Fatalf("MarkSeenIn: %v", err)
	}
	if err := cl.MarkSeenIn(context.Background(), FolderInbox, uid); err != nil {
		t.Fatalf("MarkSeenIn (idempotent): %v", err)
	}
	stats, err := cl.FolderCounts(context.Background())
	if err != nil {
		t.Fatalf("FolderCounts: %v", err)
	}
	for _, s := range stats {
		if s.Slug == FolderInbox && s.Unseen != 0 {
			t.Fatalf("inbox unseen = %d after mark seen, want 0", s.Unseen)
		}
	}
}

func TestDeleteDraft_UIDExpunge(t *testing.T) {
	raw := mkMsg("draft-one", "ada@box.test", "body")
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}
	cl := startViewIMAPRaw(t, []viewMsg{{folder: "drafts", raw: raw}}, caps)
	page, _, _, err := cl.ReadFolderPage(context.Background(), FolderDrafts, 10, 0)
	if err != nil || len(page) != 1 {
		t.Fatalf("page: err=%v len=%d", err, len(page))
	}
	if err := cl.DeleteDraft(context.Background(), page[0].UID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}
	after, _, _, err := cl.ReadFolderPage(context.Background(), FolderDrafts, 10, 0)
	if err != nil {
		t.Fatalf("page after: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("draft survived UID EXPUNGE: %d messages", len(after))
	}
}

func TestDeleteDraft_DeferredWithoutUIDPLUS(t *testing.T) {
	cl, _ := startViewIMAP(t, "drafts")
	page, _, _, err := cl.ReadFolderPage(context.Background(), FolderDrafts, 10, 0)
	if err != nil || len(page) != 1 {
		t.Fatalf("page: err=%v len=%d", err, len(page))
	}
	if err := cl.DeleteDraft(context.Background(), page[0].UID); err != nil {
		t.Fatalf("DeleteDraft (deferred): %v", err)
	}
	after, _, _, err := cl.ReadFolderPage(context.Background(), FolderDrafts, 10, 0)
	if err != nil {
		t.Fatalf("page after: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("deferred path must NOT expunge: %d messages", len(after))
	}
}
