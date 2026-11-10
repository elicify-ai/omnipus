package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/mailattachment"
	"github.com/stretchr/testify/require"
)

const lifetimePayload = "controlled save that must retain its committed outcome\n"

type attachmentSaveBarrier struct {
	entered chan struct{}
	release chan struct{}
	paused  atomic.Bool
	once    sync.Once
}

func newAttachmentSaveBarrier() *attachmentSaveBarrier {
	return &attachmentSaveBarrier{entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *attachmentSaveBarrier) waitAtEdge() {
	b.paused.Store(true)
	close(b.entered)
	<-b.release
	b.paused.Store(false)
}

func (b *attachmentSaveBarrier) unblock() { b.once.Do(func() { close(b.release) }) }

// Only mailbox I/O is controlled. The successful result remains a real part
// consumed by the real Service.Save; the fake can finish after caller cancel.
type lifetimeAttachmentTransport struct {
	*attachmentFakeTransport
	readBarrier *attachmentSaveBarrier
}

func (f *lifetimeAttachmentTransport) ReadPartCapped(ctx context.Context, folder, ref string, index int) (*email.AttachmentPart, error) {
	if f.readBarrier != nil {
		f.readBarrier.waitAtEdge()
	}
	return f.attachmentFakeTransport.ReadPartCapped(ctx, folder, ref, index)
}

type lifetimeAuditAppend struct {
	logger          *audit.Logger
	barrier         *attachmentSaveBarrier
	toolReturned    *atomic.Bool
	afterToolReturn atomic.Bool
	calls           atomic.Int32
	logError        atomic.Value
}

func (a *lifetimeAuditAppend) AttachmentSaved(fields map[string]any) string {
	if a.barrier != nil {
		a.barrier.waitAtEdge() // deterministic filesystem append latency, not a fake save result
	}
	if a.toolReturned.Load() {
		a.afterToolReturn.Store(true)
	}
	a.calls.Add(1)
	if err := a.logger.Log(&audit.Entry{Event: string(audit.EventMailAttachmentSaved), AgentID: "lifetime-agent", Tool: "download_email_attachment", Details: fields}); err != nil {
		a.logError.Store(err)
		return mailattachment.AuditFailed
	}
	return mailattachment.AuditRecorded
}

type attachmentLifetimeFixture struct {
	tool           *DownloadEmailAttachmentTool
	root           *library.Root
	auditPath      string
	barrier        *attachmentSaveBarrier
	audit          *lifetimeAuditAppend
	toolReturned   atomic.Bool
	closeCalls     atomic.Int32
	closeWhileBusy atomic.Bool
}

func newAttachmentLifetimeFixture(t *testing.T, stage string) *attachmentLifetimeFixture {
	t.Helper()
	barrier := newAttachmentSaveBarrier()
	root, err := library.OpenRoot(t.TempDir(), "lifetime-ws")
	require.NoError(t, err)
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, AuditLogRequested: true, HMACKey: bytes.Repeat([]byte{0x42}, 32)})
	require.NoError(t, err) // audit's documented chain key is 32 bytes; no real credential is used
	t.Cleanup(func() { require.NoError(t, logger.Close()) })
	fixture := &attachmentLifetimeFixture{root: root, barrier: barrier, auditPath: filepath.Join(auditDir, "audit.jsonl")}
	transport := &lifetimeAttachmentTransport{attachmentFakeTransport: newAttachmentTransport(nil, &email.AttachmentPart{
		AttachmentPartDescriptor: email.AttachmentPartDescriptor{PartIndex: 1, Filename: "lifetime.txt", ContentType: "text/plain", IsAttachment: true},
		Data:                     []byte(lifetimePayload),
	})}
	fixture.audit = &lifetimeAuditAppend{logger: logger, toolReturned: &fixture.toolReturned}
	if stage == "mailbox_read" {
		transport.readBarrier = barrier
	} else {
		fixture.audit.barrier = barrier
	}
	service := mailattachment.NewService(transport, mailattachment.RootWriter{Root: root}, fixture.audit, mailattachment.NewSaveReceiptStore(), func() string { return "lifetime@example.test" })
	tool := NewDownloadEmailAttachmentTool(EmailTransports{"lifetime-ws": transport})
	// A REAL non-nil budget is essential: the nil-budget path concealed SFH-RR1.
	tool.SetMailBudget(email.NewMailBudget(t.TempDir()), "lifetime-agent")
	// Chief-approved filesystem dependency boundary: a real root/service with
	// observation of the real Close. No replacement of Execute, budget or Save.
	tool.serviceFactory = func(context.Context) (*mailattachment.Service, func() error, error) {
		return service, func() error {
			fixture.closeCalls.Add(1)
			if barrier.paused.Load() {
				fixture.closeWhileBusy.Store(true)
			}
			return root.Close()
		}, nil
	}
	fixture.tool = tool
	t.Cleanup(func() {
		barrier.unblock()
		if fixture.closeCalls.Load() == 0 {
			require.NoError(t, root.Close())
		}
	})
	return fixture
}

func lifetimeAttachmentEvents(t *testing.T, file string) []audit.Entry {
	t.Helper()
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var events []audit.Entry
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		if len(line) != 0 {
			var entry audit.Entry
			require.NoError(t, json.Unmarshal(line, &entry))
			if entry.Event == string(audit.EventMailAttachmentSaved) {
				events = append(events, entry)
			}
		}
	}
	return events
}
