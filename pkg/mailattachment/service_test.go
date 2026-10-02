package mailattachment_test

// RED pack (w4 spec §9.1 rows 1-3, US-1, US-2; §5.2; §9.3 M1/M4/M5/M10):
// the shared Transfer service. Expected values derive from the spec: Open
// writes NOTHING anywhere (US-1; the observer — a filesystem snapshot and
// the workspace's own git status — is proven live by a Save positive
// control); the 25 MiB DECODED cap is the authority and the two byte roles
// are distinct resources (§5.2); Save lands mail/<mailbox>/<UTC month>/
// with sanitized, numbered names and never overwrites (US-2.AC-1/AC-2);
// a lost response resolves to the PRIOR receipt on an explicit same-token
// retry — exactly one file, no refetch (US-2.AC-6, M-02); audit failure
// after commit stays saved-with-warning (US-2.AC-5, M5).
//
// Mailbox label note: the spec's §2.1 mechanism ("SanitizeAttachmentName
// reused unchanged": separators/colon → hyphens, controls dropped, dots
// neutralized) preserves '@', so the label for user@ex.com IS user@ex.com;
// §8's "user_at_ex.com" example contradicts the spec's own sanitizer
// semantics and is reported as a documentation-level discrepancy, not
// adopted as the oracle.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/mailattachment"
)

// ---- fixture doubles (process edges only: the IMAP reader and the audit sink) ----

type fakePartReader struct {
	cappedCalls, fullCalls, describeCalls int
	part                                  *email.AttachmentPart
	cappedErr, fullErr                    error
}

func (f *fakePartReader) ReadPartCapped(context.Context, string, string, int) (*email.AttachmentPart, error) {
	f.cappedCalls++
	if f.cappedErr != nil {
		return nil, f.cappedErr
	}
	return f.part, nil
}

func (f *fakePartReader) ReadPartFull(context.Context, string, string, int) (*email.AttachmentPart, error) {
	f.fullCalls++
	if f.fullErr != nil {
		return nil, f.fullErr
	}
	return f.part, nil
}

func (f *fakePartReader) DescribeParts(_ context.Context, _, _ string) ([]email.AttachmentPartDescriptor, error) {
	f.describeCalls++
	if f.part == nil {
		return nil, nil
	}
	return []email.AttachmentPartDescriptor{f.part.AttachmentPartDescriptor}, nil
}

type fakeAudit struct {
	status string
	fields []map[string]any
}

func (a *fakeAudit) AttachmentSaved(fields map[string]any) string {
	a.fields = append(a.fields, fields)
	return a.status
}

// ---- workspace + snapshot instrument ----

type wsRoot struct {
	root *library.Root
	dir  string
}

func newWSRoot(t *testing.T) wsRoot {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "work")
	root, err := library.OpenRoot(filepath.Dir(dir), filepath.Base(dir))
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	// The version-control half of the observer: the workspace is a git repo
	// so any write the preview path made would show in status.
	cmd := exec.Command("git", "init", "-q", root.HostPath("."))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return wsRoot{root: root, dir: root.HostPath(".")}
}

// snapshot is the filesystem half of the observer: relative path → content
// hash for every regular file, plus the raw `git status --porcelain` output.
type snapshot struct {
	files map[string]string
	git   string
}

func snap(t *testing.T, w wsRoot) snapshot {
	t.Helper()
	s := snapshot{files: map[string]string{}}
	err := filepath.Walk(w.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(w.dir, path)
		if rerr != nil {
			return rerr
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		s.files[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot walk: %v", err)
	}
	out, gerr := exec.Command("git", "-C", w.dir, "status", "--porcelain").CombinedOutput()
	if gerr != nil {
		t.Fatalf("git status: %v: %s", gerr, out)
	}
	s.git = string(out)
	return s
}

func assertNoWrites(t *testing.T, before, after snapshot) {
	t.Helper()
	if len(after.files) != len(before.files) {
		t.Fatalf("file count changed: before=%d after=%d (US-1: Open writes nothing — no workspace file, no spool, no cache)", len(before.files), len(after.files))
	}
	for rel, hash := range before.files {
		if after.files[rel] != hash {
			t.Fatalf("file %q changed during the operation (US-1: Open writes nothing)", rel)
		}
	}
	if after.git != before.git {
		t.Fatalf("git status changed during the operation:\nbefore: %q\nafter:  %q (US-1: Open writes nothing anywhere)", before.git, after.git)
	}
}

func newService(t *testing.T, w wsRoot, reader *fakePartReader, audit *fakeAudit) *mailattachment.Service {
	t.Helper()
	return mailattachment.NewService(reader, mailattachment.RootWriter{Root: w.root}, audit, mailattachment.NewSaveReceiptStore(), func() string { return "user@ex.com" })
}

func textPart(name, content string) *email.AttachmentPart {
	return &email.AttachmentPart{
		AttachmentPartDescriptor: email.AttachmentPartDescriptor{
			PartIndex: 1, Filename: name, ContentType: "text/plain", IsAttachment: true,
		},
		Data: []byte(content),
	}
}

// ---- US-1: Open writes nothing; the instrument is proven live ----

func TestViewerBytesWritesNothingToDisk(t *testing.T) {
	w := newWSRoot(t)
	payload := "attachment-bytes-0123456789"
	reader := &fakePartReader{part: textPart("notes.txt", payload)}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})

	before := snap(t, w)
	part, err := svc.ViewerBytes(context.Background(), "inbox", "uid:1:1", 1)
	if err != nil {
		t.Fatalf("ViewerBytes: %v", err)
	}
	// Positive control (content actually served): the preview bytes are the
	// attachment bytes.
	if string(part.Data) != payload {
		t.Fatalf("preview content = %q, want the attachment bytes %q (the observer must see content move while nothing is written)", part.Data, payload)
	}
	assertNoWrites(t, before, snap(t, w))
}

func TestSavePositiveControlIsObservedByTheSameInstrument(t *testing.T) {
	w := newWSRoot(t)
	payload := "positive-control-bytes"
	reader := &fakePartReader{part: textPart("report.pdf", payload)}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})

	before := snap(t, w)
	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-positive"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	after := snap(t, w)
	if len(after.files) <= len(before.files) {
		t.Fatalf("the positive-control Save produced no observable write — the instrument could not have seen a preview write either (observer sanity, §5.1/§9.1 row 1)")
	}
	data, err := os.ReadFile(filepath.Join(w.dir, filepath.FromSlash(receipt.Path)))
	if err != nil {
		t.Fatalf("saved file unreadable: %v", err)
	}
	if string(data) != payload {
		t.Fatalf("saved bytes differ from the attachment (US-2.AC-1: byte-identical content)")
	}
	if !strings.Contains(after.git, "mail/") {
		t.Fatalf("git status shows no mail/ path after Save: %q", after.git)
	}
}

// ---- §5.2/M1 at the seam: two distinct byte roles ----

func TestByteRolesAreDistinctResources(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("doc.bin", "x")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})

	if _, err := svc.ViewerBytes(context.Background(), "inbox", "uid:1:1", 1); err != nil {
		t.Fatalf("ViewerBytes: %v", err)
	}
	if reader.fullCalls != 0 {
		t.Fatalf("ViewerBytes dialed the uncapped role %d times — the preview cap path must be the capped reader (§5.2)", reader.fullCalls)
	}
	if _, err := svc.DownloadBytes(context.Background(), "inbox", "uid:1:1", 1); err != nil {
		t.Fatalf("DownloadBytes: %v", err)
	}
	if reader.cappedCalls != 1 || reader.fullCalls != 1 {
		t.Fatalf("role call counts capped=%d full=%d, want 1/1 (§5.2: two distinct server-enforced resources)", reader.cappedCalls, reader.fullCalls)
	}

	// An over-cap part: the viewer role refuses with the typed error while
	// the download role delivers (I-05's late-failure ordering vs the
	// completion guarantee).
	over := textPart("big.bin", strings.Repeat("A", 4096))
	over.DataUnavailable = true
	over.Data = nil
	reader.cappedErr = mailattachment.ErrOverCap
	if _, err := svc.ViewerBytes(context.Background(), "inbox", "uid:1:1", 1); !errors.Is(err, mailattachment.ErrOverCap) {
		t.Fatalf("over-cap preview must refuse with the typed cap error, got %v", err)
	}
	reader.cappedErr = nil
	reader.part.Data = []byte("full-download")
	if _, err := svc.DownloadBytes(context.Background(), "inbox", "uid:1:1", 1); err != nil {
		t.Fatalf("the download role must stream the same part (no preview cap, I-05): %v", err)
	}
}

// ---- US-2: Save lands the file ----

func TestSaveLandsInMailFolderWithUTCMonth(t *testing.T) {
	w := newWSRoot(t)
	payload := "q4-bytes"
	reader := &fakePartReader{part: textPart("Q4 report.pdf", payload)}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})

	month := time.Now().UTC().Format("2006-01")
	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-month"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	wantPath := "mail/user@ex.com/" + month + "/Q4 report.pdf"
	if receipt.Path != wantPath {
		t.Fatalf("receipt.Path = %q, want %q (US-2.AC-1: mail/<mailbox>/<UTC save month>/<name>; label per the §2.1 sanitizer mechanism — see the label note above)", receipt.Path, wantPath)
	}
	if receipt.AbsolutePath != w.root.HostPath(receipt.Path) {
		t.Fatalf("receipt.AbsolutePath = %q, want the real host path of the saved file", receipt.AbsolutePath)
	}
	if receipt.SizeBytes != int64(len(payload)) {
		t.Fatalf("receipt.SizeBytes = %d, want %d", receipt.SizeBytes, len(payload))
	}
	if receipt.AuditStatus != mailattachment.AuditRecorded {
		t.Fatalf("audit status = %q, want recorded", receipt.AuditStatus)
	}
	if len(receipt.WarningCode) != 0 {
		t.Fatalf("clean save carries a warning code %q", receipt.WarningCode)
	}
}

func TestSaveNumberedCollisionIncludingCaseFolded(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("report.pdf", "bytes")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	month := time.Now().UTC().Format("2006-01")
	dir := "mail/user@ex.com/" + month

	// Pre-create report.pdf and report (1).pdf (§8's given); the parent
	// exists first — CreateUnique reserves one file, it does not create dirs.
	if _, _, err := w.root.Mkdir(dir); err != nil {
		t.Fatalf("fixture mkdir %q: %v", dir, err)
	}
	for _, name := range []string{"report.pdf", "report (1).pdf"} {
		rel, f, err := w.root.CreateUnique(dir + "/" + name)
		if err != nil {
			t.Fatalf("fixture create %q: %v", name, err)
		}
		if _, err := f.Write([]byte("pre-existing " + name)); err != nil {
			t.Fatalf("fixture write: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("fixture close: %v", err)
		}
		_ = rel
	}

	r1, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-c1"})
	if err != nil {
		t.Fatalf("Save report.pdf: %v", err)
	}
	if filepath.Base(r1.Path) != "report (2).pdf" {
		t.Fatalf("clashing save landed %q, want report (2).pdf (US-2.AC-2/§8: numbered suffix before the extension, never an overwrite)", r1.Path)
	}

	// The case-folded twin continues the same sequence (§8: report (3).pdf).
	reader.part = textPart("Report.PDF", "twin-bytes")
	r2, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-c2"})
	if err != nil {
		t.Fatalf("Save Report.PDF: %v", err)
	}
	// The declared case is preserved (the §2.1 sanitizer mechanism is
	// case-preserving; §8's lowercase rendering is illustrative); the DERIVABLE
	// oracle is the case-insensitive collision numbering: the twin of
	// report.pdf / report (1).pdf continues at (3).
	if !strings.EqualFold(filepath.Base(r2.Path), "report (3).pdf") {
		t.Fatalf("case-folded clash landed %q, want (3) continuing the case-insensitive sequence (US-2.AC-2)", r2.Path)
	}
}

func TestSaveHostileNameCannotEscapeTheMailFolder(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart(`../../..\evil:name?.pdf`, "hostile")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	month := time.Now().UTC().Format("2006-01")
	before := snap(t, w)

	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-hostile"})
	if err != nil {
		// The chain's validation half may refuse what sanitization could not
		// purify (US-2.AC-3: sanitization is not the authorization, validation
		// is) — but then the refusal is the typed unsafe-name error and no
		// partial file remains.
		if !errors.Is(err, mailattachment.ErrUnsafeName) {
			t.Fatalf("hostile name must either save sanitized or refuse with the typed unsafe-name error, got %v", err)
		}
		after := snap(t, w)
		assertNoWrites(t, before, after)
		return
	}
	if !strings.HasPrefix(receipt.Path, "mail/user@ex.com/"+month+"/") {
		t.Fatalf("hostile declared name landed outside the mail folder: %q (US-2.AC-3: the file never escapes the folder)", receipt.Path)
	}
	base := filepath.Base(receipt.Path)
	for _, bad := range []string{"/", "\\", ":", ".."} {
		if strings.Contains(base, bad) {
			t.Fatalf("stored name %q contains %q (SanitizeAttachmentName output must carry no separators, colons or dot-name segments)", base, bad)
		}
	}
}

// ---- US-2.AC-5/AC-6: audit warning and the lost response ----

func TestAuditFailureAfterCommitIsSavedWithWarning(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("warn.pdf", "warned-bytes")}
	svc := newService(t, w, reader, &fakeAudit{status: mailattachment.AuditFailed})

	receipt, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-warn"})
	if err != nil {
		t.Fatalf("Save must stay saved when the audit write fails after commit (US-2.AC-5, never a failed save): %v", err)
	}
	if receipt.AuditStatus != mailattachment.AuditFailed {
		t.Fatalf("audit status = %q, want failed", receipt.AuditStatus)
	}
	if receipt.WarningCode != mailattachment.WarningAuditWriteFailed {
		t.Fatalf("warning code = %q, want %q (the explicit audit warning)", receipt.WarningCode, mailattachment.WarningAuditWriteFailed)
	}
	if _, err := os.Stat(filepath.Join(w.dir, filepath.FromSlash(receipt.Path))); err != nil {
		t.Fatalf("the file must stand after an audit failure: %v", err)
	}
}

func TestLostResponseSameTokenRetryReturnsPriorReceipt(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("lost.pdf", "lost-bytes")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	month := time.Now().UTC().Format("2006-01")

	first, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-lost"})
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	// (The response was lost; the client shows "Save result unknown".)

	retry, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-lost"})
	if err != nil {
		t.Fatalf("explicit same-token retry: %v", err)
	}
	if retry.Path != first.Path || retry.SizeBytes != first.SizeBytes || retry.AuditStatus != first.AuditStatus {
		t.Fatalf("retry returned a different receipt: first=%+v retry=%+v (US-2.AC-6: the PRIOR receipt)", first, retry)
	}
	if !retry.FromReceipt {
		t.Fatalf("retry reports a fresh save; want FromReceipt (the prior receipt, no re-commit)")
	}
	if reader.cappedCalls != 1 {
		t.Fatalf("the retry refetched the part %d times (want 1) — M-02: no refetch, no second reservation", reader.cappedCalls)
	}
	entries, _ := os.ReadDir(filepath.Join(w.dir, "mail", "user@ex.com", month))
	if len(entries) != 1 {
		t.Fatalf("%d files exist after the retry, want exactly one (US-2.AC-6: exactly one file, original numbered name)", len(entries))
	}
}

func TestDifferentTokenIsANewSave(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("new.pdf", "new-bytes")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	month := time.Now().UTC().Format("2006-01")

	if _, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-a"}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-b"})
	if err != nil {
		t.Fatalf("deliberate new save with a fresh token: %v", err)
	}
	if filepath.Base(second.Path) != "new (1).pdf" {
		t.Fatalf("fresh-token save landed %q, want new (1).pdf (US-2.AC-6: a different token is a new request; numbered suffix)", second.Path)
	}
	entries, _ := os.ReadDir(filepath.Join(w.dir, "mail", "user@ex.com", month))
	if len(entries) != 2 {
		t.Fatalf("%d files exist, want 2 (one per explicit save)", len(entries))
	}
}

// US-2.AC-7: a save whose part fetch fails with a typed refusal surfaces
// that refusal verbatim — permission/size refusals never masquerade as
// network errors — and no destination work or audit happens.
func TestSaveTypedRefusalSurfacesVerbatim(t *testing.T) {
	w := newWSRoot(t)
	audit := &fakeAudit{status: "recorded"}
	reader := &fakePartReader{part: textPart("big.pdf", "x"), cappedErr: mailattachment.ErrOverCap}
	svc := newService(t, w, reader, audit)
	before := snap(t, w)
	_, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-overcap"})
	if !errors.Is(err, mailattachment.ErrOverCap) {
		t.Fatalf("Save over-cap refusal = %v, want the typed cap error (US-2.AC-7: refusals never masquerade as network errors)", err)
	}
	assertNoWrites(t, before, snap(t, w))
	if len(audit.fields) != 0 {
		t.Fatalf("a refused save produced %d audit events", len(audit.fields))
	}
}

func TestSaveWithoutTokenIsRefusedAndWritesNothing(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("x.txt", "x")}
	svc := newService(t, w, reader, &fakeAudit{status: "recorded"})
	before := snap(t, w)
	if _, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1}); !errors.Is(err, mailattachment.ErrInvalidToken) {
		t.Fatalf("missing token must refuse with ErrInvalidToken, got %v", err)
	}
	assertNoWrites(t, before, snap(t, w))
}

func TestSaveWithoutMailboxLabelIsRefused(t *testing.T) {
	w := newWSRoot(t)
	reader := &fakePartReader{part: textPart("x.txt", "x")}
	svc := mailattachment.NewService(reader, mailattachment.RootWriter{Root: w.root}, &fakeAudit{status: "recorded"}, mailattachment.NewSaveReceiptStore(), func() string { return "" })
	if _, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "t"}); !errors.Is(err, mailattachment.ErrNoMailboxLabel) {
		t.Fatalf("empty mailbox label must refuse with ErrNoMailboxLabel, got %v", err)
	}
}

// ---- the audit event's field discipline (US-2.AC-4) ----

func TestAuditEventCarriesNoBytesSubjectOrToken(t *testing.T) {
	w := newWSRoot(t)
	payload := "secret-attachment-bytes"
	reader := &fakePartReader{part: textPart("audit.pdf", payload)}
	audit := &fakeAudit{status: "recorded"}
	svc := newService(t, w, reader, audit)

	if _, err := svc.Save(context.Background(), mailattachment.SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "tok-audit-secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(audit.fields) != 1 {
		t.Fatalf("expected exactly one audit event, got %d", len(audit.fields))
	}
	f := audit.fields[0]
	for _, want := range []string{"part_index", "original_name", "final_name", "workspace_relative_path", "size_bytes", "folder", "message_ref"} {
		if _, ok := f[want]; !ok {
			t.Fatalf("audit event lacks %q (US-2.AC-4 field set): %v", want, f)
		}
	}
	for _, banned := range []string{"token", "subject", "password", "bytes", "data"} {
		if _, ok := f[banned]; ok {
			t.Fatalf("audit event carries banned field %q (US-2.AC-4: no bytes, subject, password or token)", banned)
		}
	}
	if f["size_bytes"] != len(payload) {
		t.Fatalf("size_bytes = %v, want %d", f["size_bytes"], len(payload))
	}
}
