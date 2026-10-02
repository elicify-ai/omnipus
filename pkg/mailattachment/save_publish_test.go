package mailattachment

// RED pack (w4 spec §9.1 row 2, US-2.AC-7): publication discipline — a
// mid-write destination failure leaves no partial file and no audit event.
// This oracle needs to name the service's unexported writerFile, so it lives
// in the internal test package (Go: two test packages per directory).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/library"
)

type pubFailingFile struct{}

func (pubFailingFile) Write([]byte) (int, error) { return 0, errors.New("disk full (simulated)") }
func (pubFailingFile) Close() error              { return nil }

type pubFailingCreateWriter struct {
	RootWriter
	created string
}

func (w *pubFailingCreateWriter) CreateUnique(rel string) (string, writerFile, error) {
	final, _, err := w.RootWriter.CreateUnique(rel)
	if err != nil {
		return "", nil, err
	}
	w.created = final
	return final, pubFailingFile{}, nil
}

type pubReader struct{ part *email.AttachmentPart }

func (r *pubReader) ReadPartCapped(context.Context, string, string, int) (*email.AttachmentPart, error) {
	return r.part, nil
}
func (r *pubReader) ReadPartFull(context.Context, string, string, int) (*email.AttachmentPart, error) {
	return r.part, nil
}
func (r *pubReader) DescribeParts(context.Context, string, string) ([]email.AttachmentPartDescriptor, error) {
	return nil, nil
}

type pubAudit struct{ calls int }

func (a *pubAudit) AttachmentSaved(map[string]any) string { a.calls++; return AuditRecorded }

func TestSaveMidWriteFailureLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	root, err := library.OpenRoot(filepath.Dir(filepath.Join(dir, "work")), "work")
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer root.Close()
	reader := &pubReader{part: &email.AttachmentPart{
		AttachmentPartDescriptor: email.AttachmentPartDescriptor{PartIndex: 1, Filename: "partial.pdf", ContentType: "application/pdf", IsAttachment: true},
		Data:                     []byte("partial-bytes"),
	}}
	writer := &pubFailingCreateWriter{RootWriter: RootWriter{Root: root}}
	audit := &pubAudit{}
	svc := NewService(reader, writer, audit, NewSaveReceiptStore(), func() string { return "user@ex.com" })

	_, err = svc.Save(context.Background(), SaveRequest{Slug: "inbox", Ref: "uid:1:1", PartIndex: 1, Token: "t-partial"})
	if err == nil {
		t.Fatalf("mid-write failure must surface as an error (never a silent partial save)")
	}
	if !errors.Is(err, ErrDestinationWrite) {
		t.Fatalf("mid-write failure must be the destination-write class, got %v", err)
	}
	if writer.created == "" {
		t.Fatalf("the failure never reached a reserved file — the test could not observe cleanup")
	}
	if _, statErr := os.Stat(filepath.Join(root.HostPath("."), filepath.FromSlash(writer.created))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial file %q still exists after the refused save (US-2.AC-7: incomplete output is removed)", writer.created)
	}
	if audit.calls != 0 {
		t.Fatalf("a save that never committed produced %d audit events", audit.calls)
	}
}
