package tools

// RED pack (w4 spec §9.1 row 10; US-3; §8 agent-tools scenarios; §9.3 M9/M10
// at the tool surface): the agent attachment tools. The transport here is a
// fixture double at the process edge (the IMAP boundary); the service is the
// REAL shared Transfer service over a real Library root, so save results are
// real files at real paths.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/mailattachment"
)

// attachmentFakeTransport wraps the shared fixture transport with the
// attachment capability and call counters (MarkSeen observation included).
type attachmentFakeTransport struct {
	*fakeTransport
	seenCalls     int
	describeCalls int
	cappedCalls   int
	descriptors   []email.AttachmentPartDescriptor
	part          *email.AttachmentPart
}

func (f *attachmentFakeTransport) MarkSeen(ctx context.Context, uid uint32) error {
	f.seenCalls++
	return f.fakeTransport.MarkSeen(ctx, uid)
}

func (f *attachmentFakeTransport) DescribeParts(_ context.Context, _, _ string) ([]email.AttachmentPartDescriptor, error) {
	f.describeCalls++
	return f.descriptors, nil
}

func (f *attachmentFakeTransport) ReadPartCapped(_ context.Context, _, _ string, _ int) (*email.AttachmentPart, error) {
	f.cappedCalls++
	return f.part, nil
}

func (f *attachmentFakeTransport) ReadPartFull(_ context.Context, _, _ string, _ int) (*email.AttachmentPart, error) {
	return f.part, nil
}

func newAttachmentTransport(descriptors []email.AttachmentPartDescriptor, part *email.AttachmentPart) *attachmentFakeTransport {
	return &attachmentFakeTransport{fakeTransport: newFakeTransportForAttachments(), descriptors: descriptors, part: part}
}

// newFakeTransportForAttachments builds the base fake with one inbox message
// whose Message-ID is ABSENT (grill I-03's fixture).
func newFakeTransportForAttachments() *fakeTransport {
	return newFakeTransport(email.Message{
		UID:     7,
		Subject: "no-message-id",
		From:    "sender@example.test",
		// MessageID deliberately empty — the I-03 fixture.
	})
}

// --- list: metadata only, no Seen, draft part excluded (US-3.AC-1) ---

func TestListEmailAttachmentsToolMetadataOnly(t *testing.T) {
	ft := newAttachmentTransport(
		[]email.AttachmentPartDescriptor{
			{PartIndex: 0, ContentType: "text/plain", IsAttachment: false},
			{PartIndex: 1, Filename: "q4.pdf", ContentType: "application/pdf", Disposition: "attachment", ReportedSizeBytes: 1234, IsAttachment: true},
			{PartIndex: 2, Filename: "message.md", ContentType: "text/markdown", Disposition: "attachment", OmnipusDraftBody: true, IsAttachment: true},
			{PartIndex: 3, Filename: "mystery.bin", ContentType: "application/octet-stream", ReportedSizeBytes: -1, IsAttachment: true},
		},
		nil,
	)
	_ = ft
	res := NewListEmailAttachmentsTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": "uid:99:7",
	})
	if res.IsError {
		t.Fatalf("list failed: %s", res.ForLLM)
	}
	var out struct {
		MessageRef  string `json:"message_ref"`
		Attachments []struct {
			PartIndex         int    `json:"part_index"`
			Filename          string `json:"filename"`
			ContentType       string `json:"content_type"`
			ReportedSizeBytes *int64 `json:"reported_size_bytes"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("unmarshal list result: %v (%s)", err, res.ForLLM)
	}
	if len(out.Attachments) != 2 {
		t.Fatalf("list returned %d attachments, want 2 — the draft bookkeeping part and the non-attachment body are never listed (US-3.AC-1): %s", len(out.Attachments), res.ForLLM)
	}
	if out.Attachments[0].PartIndex != 1 || out.Attachments[0].Filename != "q4.pdf" {
		t.Fatalf("first descriptor = %+v, want the q4.pdf leaf at its stable index", out.Attachments[0])
	}
	if out.Attachments[1].PartIndex != 3 || out.Attachments[1].ReportedSizeBytes != nil {
		t.Fatalf("unreported size must claim nothing (reported_size_bytes omitted), got %+v", out.Attachments[1])
	}
	if ft.seenCalls != 0 {
		t.Fatalf("listing changed the Seen state %d times (US-3.AC-1: no Seen change just to list)", ft.seenCalls)
	}
}

// --- read: text in, explicit unsupported out, no fake reads (US-3.AC-3, E-2) ---

func TestReadEmailAttachmentToolTextAndUnsupported(t *testing.T) {
	// A text part reads as text.
	ftText := newAttachmentTransport(nil, &email.AttachmentPart{
		AttachmentPartDescriptor: email.AttachmentPartDescriptor{PartIndex: 1, Filename: "notes.txt", ContentType: "text/plain"},
		Data:                     []byte("the actual content"),
	})
	res := NewReadEmailAttachmentTool(EmailTransports{"ws": ftText}).Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": "uid:99:7", "part_index": float64(1),
	})
	if res.IsError {
		t.Fatalf("text read failed: %s", res.ForLLM)
	}
	var txt struct {
		Content    string `json:"content"`
		SizeBytes  int    `json:"size_bytes"`
		PartIndex  int    `json:"part_index"`
		Translated string `json:"-"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &txt); err != nil {
		t.Fatalf("unmarshal read result: %v (%s)", err, res.ForLLM)
	}
	if txt.Content != "the actual content" || txt.SizeBytes != len("the actual content") {
		t.Fatalf("text read = (%q,%d), want the actual content and its length", txt.Content, txt.SizeBytes)
	}

	// A binary part yields the explicit unsupported outcome plus the Save
	// option — never an empty read, never binary as text (US-3.AC-3).
	ftBin := newAttachmentTransport(nil, &email.AttachmentPart{
		AttachmentPartDescriptor: email.AttachmentPartDescriptor{PartIndex: 2, Filename: "pic.png", ContentType: "image/png"},
		Data:                     []byte{0x89, 'P', 'N', 'G', 0x00, 0xFF},
	})
	res2 := NewReadEmailAttachmentTool(EmailTransports{"ws": ftBin}).Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": "uid:99:7", "part_index": float64(2),
	})
	if res2.IsError {
		t.Fatalf("binary read returned a hard error; want the explicit unsupported outcome: %s", res2.ForLLM)
	}
	var uns struct {
		Unsupported bool   `json:"unsupported"`
		CanSave     bool   `json:"can_save"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
	}
	if err := json.Unmarshal([]byte(res2.ForLLM), &uns); err != nil {
		t.Fatalf("unmarshal unsupported result: %v (%s)", err, res2.ForLLM)
	}
	if !uns.Unsupported || !uns.CanSave || uns.Filename != "pic.png" {
		t.Fatalf("unsupported outcome = %+v, want unsupported+can_save with the descriptor", uns)
	}
	if strings.Contains(res2.ForLLM, "\x89PNG") {
		t.Fatalf("binary bytes leaked into the text result (US-3.AC-3: never binary disguised as text)")
	}

	// An unavailable part is an honest error naming the cap and the Download option.
	ftOver := newAttachmentTransport(nil, &email.AttachmentPart{
		AttachmentPartDescriptor: email.AttachmentPartDescriptor{PartIndex: 1, Filename: "big.bin", ContentType: "application/octet-stream"},
		DataUnavailable:          true,
	})
	res3 := NewReadEmailAttachmentTool(EmailTransports{"ws": ftOver}).Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": "uid:99:7", "part_index": float64(1),
	})
	if !res3.IsError || !strings.Contains(res3.ForLLM, "25 MiB") || !strings.Contains(res3.ForLLM, "download_email_attachment") {
		t.Fatalf("unavailable part must error naming the cap and the save option, got err=%v text=%s", res3.Err, res3.ForLLM)
	}
}

// --- download: the shared service, real paths, ordinary refusal ---

func TestDownloadEmailAttachmentToolReturnsRealPaths(t *testing.T) {
	dir := t.TempDir()
	root, err := library.OpenRoot(filepath.Dir(filepath.Join(dir, "work")), "work")
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer root.Close()
	payload := "saved-by-tool"
	ft := newAttachmentTransport(nil, &email.AttachmentPart{
		AttachmentPartDescriptor: email.AttachmentPartDescriptor{PartIndex: 1, Filename: "tool-saved.pdf", ContentType: "application/pdf", IsAttachment: true},
		Data:                     []byte(payload),
	})
	svc := mailattachment.NewService(ft, mailattachment.RootWriter{Root: root}, nil, mailattachment.NewSaveReceiptStore(), func() string { return "user@ex.com" })

	tool := NewDownloadEmailAttachmentTool(EmailTransports{"ws": ft})
	tool.SetTransferService(svc, root.HostPath("."), false)
	res := tool.Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": "uid:99:7", "part_index": float64(1),
	})
	if res.IsError {
		t.Fatalf("tool save failed: %s", res.ForLLM)
	}
	var out struct {
		Saved        bool   `json:"saved"`
		Path         string `json:"path"`
		AbsolutePath string `json:"absolute_path"`
		SizeBytes    int64  `json:"size_bytes"`
		AuditStatus  string `json:"audit_status"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("unmarshal save result: %v (%s)", err, res.ForLLM)
	}
	month := time.Now().UTC().Format("2006-01")
	if !out.Saved || !strings.HasPrefix(out.Path, "mail/user@ex.com/"+month+"/") || filepath.Base(out.Path) != "tool-saved.pdf" {
		t.Fatalf("save result = %+v, want the real workspace path under mail/<mailbox>/<month>/ (US-3.AC-7)", out)
	}
	if out.AbsolutePath == "" || out.SizeBytes != int64(len(payload)) {
		t.Fatalf("save result paths/size incomplete: %+v", out)
	}
	// The agent's ordinary file tools can read that exact path immediately.
	data, rerr := os.ReadFile(out.AbsolutePath)
	if rerr != nil || string(data) != payload {
		t.Fatalf("the saved file is not readable at the returned absolute path: %v", rerr)
	}
}

func TestDownloadEmailAttachmentToolUnwiredRefuses(t *testing.T) {
	ft := newAttachmentTransport(nil, nil)
	res := NewDownloadEmailAttachmentTool(EmailTransports{"ws": ft}).Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": "uid:99:7", "part_index": float64(1),
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "not wired") {
		t.Fatalf("unwired save tool must refuse with an explicit, visible error, got err=%v text=%s", res.Err, res.ForLLM)
	}
}

// --- the I-03 journey: chained from actual tool output alone ---

func TestNoMessageIDJourneyChainsFromToolOutput(t *testing.T) {
	ft := newAttachmentTransport(
		[]email.AttachmentPartDescriptor{
			{PartIndex: 1, Filename: "journey.txt", ContentType: "text/plain", Disposition: "attachment", IsAttachment: true},
		},
		&email.AttachmentPart{
			AttachmentPartDescriptor: email.AttachmentPartDescriptor{PartIndex: 1, Filename: "journey.txt", ContentType: "text/plain"},
			Data:                     []byte("journey content"),
		},
	)
	tps := EmailTransports{"ws": ft}

	// Step 1: read_message. The reference the journey consumes MUST come from
	// this output — the spec forbids minting one locally (register rows 8/16).
	readRes := NewReadMessageTool(tps).Execute(mailCtx(), map[string]any{"uid": float64(7)})
	if readRes.IsError {
		t.Fatalf("read_message failed: %s", readRes.ForLLM)
	}
	var msg struct {
		MessageID  string `json:"message_id"`
		MessageRef string `json:"message_ref"`
		UID        uint32 `json:"uid"`
	}
	if err := json.Unmarshal([]byte(readRes.ForLLM), &msg); err != nil {
		t.Fatalf("unmarshal read_message output: %v (%s)", err, readRes.ForLLM)
	}
	if msg.MessageID != "" {
		t.Fatalf("fixture message unexpectedly carries a Message-ID — the I-03 fixture requires one WITHOUT it")
	}
	if msg.MessageRef == "" {
		t.Fatalf("BLOCKED: read_message carries no message_ref — the Wave-D issuer (w5 US-9, register rows 8/16) has not landed; the spec forbids the tools minting one locally, so the no-Message-ID journey cannot run yet (US-3.AC-4)")
	}

	// Steps 2-4 chain strictly on returned values.
	listRes := NewListEmailAttachmentsTool(tps).Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": msg.MessageRef,
	})
	if listRes.IsError {
		t.Fatalf("list failed: %s", listRes.ForLLM)
	}
	readAttRes := NewReadEmailAttachmentTool(tps).Execute(mailCtx(), map[string]any{
		"folder": "inbox", "message_ref": msg.MessageRef, "part_index": float64(1),
	})
	if readAttRes.IsError {
		t.Fatalf("read failed: %s", readAttRes.ForLLM)
	}
}
