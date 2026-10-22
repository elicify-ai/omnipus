package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/mailattachment"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Receipts survive tool re-registration, but never cross an agent/workspace pair.
var attachmentTransferReceipts sync.Map

type attachmentTransferKey struct {
	home, agentID, workspaceID string
}

// SetTransferServiceFactory configures normal registration's context-bound
// factory. The fixed SetTransferService seam remains available unchanged.
func (t *DownloadEmailAttachmentTool) SetTransferServiceFactory(agentID, agentHome string, restrict bool) {
	t.transferAgentID = agentID
	t.agentHome = agentHome
	t.restrict = restrict
	t.serviceFactory = t.newTransferService
}

// SetAuditLogger participates in the existing registry injection contract.
func (t *DownloadEmailAttachmentTool) SetAuditLogger(logger *audit.Logger) {
	t.auditLogger.Store(logger)
}

func (t *DownloadEmailAttachmentTool) newTransferService(ctx context.Context) (*mailattachment.Service, func() error, error) {
	if id := ToolAgentID(ctx); id != "" && id != t.transferAgentID {
		return nil, nil, fmt.Errorf("download_email_attachment: agent context does not match the registered mailbox owner")
	}
	ctx = WithAgentID(ctx, t.transferAgentID)
	tp, err := t.tps.resolve(ctx, t.Name())
	if err != nil {
		return nil, nil, err
	}
	reader, ok := tp.(mailattachment.PartReader)
	if !ok {
		return nil, nil, fmt.Errorf("download_email_attachment: mailbox does not support attachment transfers")
	}
	identity, ok := tp.(mailboxIdentity)
	if !ok {
		return nil, nil, fmt.Errorf("download_email_attachment: mailbox identity is unavailable")
	}
	wsID := ToolWorkspaceID(ctx)
	if wsID == "" && len(t.tps) == 1 {
		for id := range t.tps {
			wsID = id
		}
	}
	if wsID == "" {
		return nil, nil, fmt.Errorf("download_email_attachment: a workspace-bound mailbox is required")
	}
	home := config.OmnipusHomeDir()
	workDir, err := workspace.SafeWorkDir(home, wsID)
	if err != nil {
		return nil, nil, err
	}
	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return nil, nil, err
	}
	authorize := func(rel string) error {
		handle, err := ResolvePath(ctx, policy, t.Name(), "", FSOpWrite, filepath.Join(workDir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		return handle.Close()
	}
	// Check scope before OpenRoot can create the Library work directory.
	if err := authorize("mail"); err != nil {
		return nil, nil, err
	}
	root, err := library.OpenRoot(home, wsID)
	if err != nil {
		return nil, nil, err
	}
	marker := mailattachment.NewMarkerStore(filepath.Join(filepath.Dir(workDir), "mail-marker-index.json"))
	root.AttachPathMarker(marker)
	writer := mailattachment.AuthorizeDestination(mailattachment.RootWriter{Root: root, Marker: marker}, authorize)
	key := attachmentTransferKey{home: home, agentID: t.transferAgentID, workspaceID: wsID}
	store, _ := attachmentTransferReceipts.LoadOrStore(key, mailattachment.NewSaveReceiptStore())
	service := mailattachment.NewService(reader, writer,
		attachmentTransferAudit{logger: t.auditLogger.Load(), agentID: t.transferAgentID, sessionID: ToolTranscriptSessionID(ctx)},
		store.(*mailattachment.SaveReceiptStore), identity.AccountAddress)
	return service, root.Close, nil
}

type attachmentTransferAudit struct {
	logger             *audit.Logger
	agentID, sessionID string
}

func (a attachmentTransferAudit) AttachmentSaved(fields map[string]any) string {
	if a.logger == nil {
		return mailattachment.AuditDisabled
	}
	if err := a.logger.Log(&audit.Entry{
		Event: string(audit.EventMailAttachmentSaved), AgentID: a.agentID,
		SessionID: a.sessionID, Tool: "download_email_attachment", Details: fields,
	}); err != nil {
		// Service.Save returns this failure in the committed receipt's warning.
		return mailattachment.AuditFailed
	}
	return mailattachment.AuditRecorded
}
