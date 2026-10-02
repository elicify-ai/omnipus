package mailattachment

// The Transfer service (ADR-20261001 F2 "One transfer/save service"; w4
// spec §3.1): the ONE application-level function behind Open, browser
// Download, agent read and BOTH saves. Mode selects what happens with the
// fetched part; only Save opens a destination. Composed of injected parts —
// the mail part reader (pkg/email), the authorized destination writer (the
// caller's Library root / fs-policy-authorized writer), the audit sink and
// the receipt store — so it imports no tool package and no HTTP.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/library"
)

// Typed refusals — every one a safe, specific reason; none masquerades as a
// network error (US-2.AC-7). The gateway maps them to their status codes.
var (
	ErrInvalidToken         = errors.New("save_operation_token is required")
	ErrDuplicateTokenCommit = errors.New("save token already committed")
	ErrNotMailDerived       = errors.New("file is not mail-derived")
	ErrOverCap              = email.ErrMailPartTooLarge
	ErrPartMissing          = email.ErrMailPartNotFound
	ErrStaleRef             = email.ErrMailStaleReference
	ErrNoMailboxLabel       = errors.New("mailbox label is required")
	ErrUnsafeName           = errors.New("unsafe file name")
)

// PartReader is the targeted single-part seam the service consumes — the
// pkg/email.Client implements it (pkg/email/attachment_parts.go). Never a
// second connection path: every read rides the caller-injected client, the
// shared budget and pool.
type PartReader interface {
	// ReadPartCapped fetches exactly one part's decoded bytes under the
	// 25 MiB decoded cap (viewer bytes, save).
	ReadPartCapped(ctx context.Context, slug, ref string, partIndex int) (*email.AttachmentPart, error)
	// ReadPartFull fetches exactly one part's decoded bytes with NO preview
	// cap (the browser-Download role streams to completion; grill I-05).
	ReadPartFull(ctx context.Context, slug, ref string, partIndex int) (*email.AttachmentPart, error)
	// DescribeParts returns structure-level descriptors only (the agent
	// list tool; no body bytes, no Seen change).
	DescribeParts(ctx context.Context, slug, ref string) ([]email.AttachmentPartDescriptor, error)
}

// DestinationWriter is the authorized Library write surface the caller
// passes in — the panel path passes a *library.Root-backed writer; the agent
// path passes a writer whose every operation already went through the turn's
// filesystem policy. The service never sees raw paths or handles beyond this
// surface; sanitization is name hygiene, THIS is the authorization.
type DestinationWriter interface {
	// CleanRel validates and normalizes a workspace-relative path.
	CleanRel(raw string) (string, error)
	// ValidateCreateName validates a create candidate (Windows-invalid
	// names, path limits) without creating.
	ValidateCreateName(rel string) error
	// Mkdir ensures one directory level exists (idempotent; refuses a file
	// occupying the path).
	Mkdir(rel string) error
	// CreateUnique reserves a fresh exclusive file with a numbered suffix
	// before the extension; never overwrites.
	CreateUnique(rel string) (finalRel string, file writerFile, err error)
	// HostPath resolves the separately-reported absolute path of a
	// workspace-relative file.
	HostPath(rel string) string
	// MarkerStore is the per-workspace mail-derived marker store (nil when
	// the caller provides none — an HTML save then FAILS CLOSED: an
	// unmarkable mail-derived HTML file must not land scripts-capable).
	MarkerStore() *MarkerStore
}

// writerFile is the minimal file handle the service needs.
type writerFile interface {
	Write(p []byte) (int, error)
	Close() error
}

// remover is the optional cleanup surface: a writer that can remove a
// partially-written file so a failed save leaves no partial output
// (US-2.AC-7).
type remover interface {
	Remove(rel string) error
}

// AuditSink receives the mail.attachment_saved event. The gateway adapter
// records through the existing auditor and maps the outcome to
// recorded/disabled/failed; the service owns the field discipline (no
// bytes, subject, password or token in the event).
type AuditSink interface {
	AttachmentSaved(fields map[string]any) string
}

// Service is the shared save/read/download service.
type Service struct {
	reader    PartReader
	writer    DestinationWriter
	audit     AuditSink
	receipts  *SaveReceiptStore
	mailboxOf func() string // the pair's mailbox label, server-computed
	now       func() time.Time
}

// NewService assembles the service. receipts may be nil for read-only use;
// mailboxOf supplies the pair's mailbox label (server-computed — never an
// agent-supplied host path).
func NewService(reader PartReader, writer DestinationWriter, audit AuditSink, receipts *SaveReceiptStore, mailboxOf func() string) *Service {
	return &Service{
		reader:    reader,
		writer:    writer,
		audit:     audit,
		receipts:  receipts,
		mailboxOf: mailboxOf,
		now:       time.Now,
	}
}

// ViewerBytes fetches one part under the 25 MiB decoded cap for the
// temporary preview (US-1). No destination is opened; nothing is written
// anywhere.
func (s *Service) ViewerBytes(ctx context.Context, slug, ref string, partIndex int) (*email.AttachmentPart, error) {
	return s.reader.ReadPartCapped(ctx, slug, ref, partIndex)
}

// DownloadBytes fetches one part with NO preview cap for the browser
// Download role (grill I-05: two distinct byte resources; the preview cap is
// never applied here).
func (s *Service) DownloadBytes(ctx context.Context, slug, ref string, partIndex int) (*email.AttachmentPart, error) {
	return s.reader.ReadPartFull(ctx, slug, ref, partIndex)
}

// Describe lists one message's attachment descriptors (structure metadata
// only — the agent list tool; no body bytes, no Seen change).
func (s *Service) Describe(ctx context.Context, slug, ref string) ([]email.AttachmentPartDescriptor, error) {
	return s.reader.DescribeParts(ctx, slug, ref)
}

// SaveRequest is one explicit save.
type SaveRequest struct {
	Slug      string
	Ref       string
	PartIndex int
	// Token is the client-generated opaque save-operation token (M-02) —
	// echoed on the receipt; an explicit same-token retry returns the prior
	// receipt. Required.
	Token string
}

// Save runs the save flow: receipt reconciliation → capped part fetch →
// sanitize→validate chain → parent creation → unique reservation → complete
// publication → marker (HTML) → audit → receipt. Exactly one of: a receipt
// (saved), or a typed refusal with no partial file left behind.
func (s *Service) Save(ctx context.Context, req SaveRequest) (*SaveReceipt, error) {
	if req.Token == "" {
		return nil, ErrInvalidToken
	}
	// M-02: an explicit same-token retry resolves to the prior receipt —
	// exactly one file exists, under the original numbered name, with its
	// intact audit status. No refetch, no second reservation.
	if r, ok := s.receipts.Lookup(req.Token); ok {
		r.FromReceipt = true
		return &r, nil
	}
	if s.writer == nil {
		return nil, errors.New("mailattachment: no destination writer configured")
	}
	label := ""
	if s.mailboxOf != nil {
		label = s.mailboxOf()
	}
	label = sanitizeMailboxLabel(label)
	if label == "" {
		return nil, ErrNoMailboxLabel
	}

	part, err := s.reader.ReadPartCapped(ctx, req.Slug, req.Ref, req.PartIndex)
	if err != nil {
		// Over cap / stale / missing: typed refusals before any destination
		// work — permission and size refusals never masquerade as network
		// errors.
		return nil, err
	}
	if part.DataUnavailable || part.Data == nil {
		return nil, fmt.Errorf("attachment part %d unavailable: over the 25 MiB per-part cap or failed to decode", req.PartIndex)
	}

	name := sanitizeSaveName(part.Filename)
	month := s.now().UTC().Format("2006-01")
	relRaw := path.Join("mail", label, month, name)
	rel, err := s.writer.CleanRel(relRaw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsafeName, err)
	}
	// Sanitization is not the authorization: the candidate passes Library
	// create-name validation BEFORE the reservation, and the final suffixed
	// candidate is validated again after it (US-2.AC-2/AC-3).
	if err := s.writer.ValidateCreateName(rel); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsafeName, err)
	}
	for _, dir := range []string{"mail", path.Join("mail", label), path.Join("mail", label, month)} {
		if err := s.writer.Mkdir(dir); err != nil {
			return nil, fmt.Errorf("could not create the mail save folder: %w", err)
		}
	}

	finalRel, file, err := s.writer.CreateUnique(rel)
	if err != nil {
		return nil, fmt.Errorf("could not reserve the file in the Library: %w", err)
	}
	if err := s.writer.ValidateCreateName(finalRel); err != nil {
		s.cleanupAfterRefusal(finalRel, file)
		return nil, fmt.Errorf("%w: %v", ErrUnsafeName, err)
	}
	var writeErr error
	if n, werr := file.Write(part.Data); werr != nil {
		writeErr = fmt.Errorf("write failed after %d of %d bytes: %w", n, len(part.Data), werr)
	}
	if cerr := file.Close(); cerr != nil && writeErr == nil {
		writeErr = fmt.Errorf("close failed: %w", cerr)
	}
	if writeErr != nil {
		s.cleanupAfterRefusal(finalRel, nil)
		return nil, writeErr
	}

	// The saved mail-derived HTML profile (Q5=A): original bytes, marker
	// written only here. An HTML file whose marker cannot be written fails
	// the save (fail-closed): an unmarkable mail-derived HTML file must
	// never land scripts-capable.
	if s.writer.MarkerStore() != nil && strings.EqualFold(path.Ext(finalRel), ".html") {
		if merr := s.writer.MarkerStore().Mark(finalRel); merr != nil {
			s.cleanupAfterRefusal(finalRel, nil)
			return nil, fmt.Errorf("could not record the mail-derived marker for the saved HTML file: %w", merr)
		}
	}

	absPath := s.writer.HostPath(finalRel)
	receipt := SaveReceipt{
		Token:         req.Token,
		WorkspaceID:   "",
		Path:          finalRel,
		AbsolutePath:  absPath,
		SizeBytes:     int64(len(part.Data)),
		AuditStatus:   AuditDisabled,
		MailboxLabel:  label,
		SavedAtUnixMS: s.now().UnixMilli(),
	}
	if s.audit != nil {
		status := s.audit.AttachmentSaved(map[string]any{
			"workspace_relative_path": finalRel,
			"folder":                  req.Slug,
			"message_ref":             req.Ref,
			"part_index":              req.PartIndex,
			"original_name":           part.Filename,
			"final_name":              path.Base(finalRel),
			"size_bytes":              len(part.Data),
		})
		switch status {
		case AuditRecorded:
			receipt.AuditStatus = AuditRecorded
		case AuditFailed:
			// US-2.AC-5: the file stands; saved=true with the explicit
			// warning — never a failed save, never advice to retry.
			receipt.AuditStatus = AuditFailed
			receipt.WarningCode = WarningAuditWriteFailed
		default:
			receipt.AuditStatus = AuditDisabled
		}
	}
	if err := s.receipts.Record(receipt); err != nil {
		// A duplicate commit under one token would defeat the
		// reconciliation's whole point: refuse loudly rather than overwrite
		// the first outcome.
		return nil, err
	}
	return &receipt, nil
}

// cleanupAfterRefusal removes a partial/unwanted output and REPORTS a
// cleanup failure — never logged away (US-2.AC-7). file, when non-nil, is
// closed first.
func (s *Service) cleanupAfterRefusal(rel string, file writerFile) {
	if file != nil {
		_ = file.Close()
	}
	r, ok := s.writer.(remover)
	if !ok {
		return
	}
	if err := r.Remove(rel); err != nil {
		// A cleanup failure is itself reported (US-2.AC-7) — never logged
		// away silently, and never a 500-style swallow.
		slog.Warn("mailattachment: cleanup after refused save failed",
			"path", rel, "error", err)
	}
}

// sanitizeSaveName applies the EXISTING sanitizer (never a second one) and
// the download route's empty-name default.
func sanitizeSaveName(name string) string {
	out := email.SanitizeAttachmentName(name)
	if out == "" || out == "-" {
		return "attachment"
	}
	return out
}

// sanitizeMailboxLabel defensively sanitizes the server-computed mailbox
// label (E-1 default (c): the sanitized mailbox address, server-side).
func sanitizeMailboxLabel(label string) string {
	return strings.ReplaceAll(strings.TrimSpace(email.SanitizeAttachmentName(label)), "/", "-")
}

// RootWriter adapts a *library.Root to DestinationWriter for the panel save
// path (the agent path builds its own writer over the turn's authorized
// filesystem policy and passes it in — never a checked string handed to an
// unconfined write).
type RootWriter struct {
	Root   *library.Root
	Marker *MarkerStore
}

// CleanRel validates and normalizes via the Library's own path hygiene.
func (w RootWriter) CleanRel(raw string) (string, error) { return library.CleanRelPath(raw) }

// ValidateCreateName delegates to the root's create-name validation.
func (w RootWriter) ValidateCreateName(rel string) error { return w.Root.ValidateCreateName(rel) }

// Mkdir ensures one directory level through the path-safe primitive.
func (w RootWriter) Mkdir(rel string) error {
	_, _, err := w.Root.Mkdir(rel)
	return err
}

// CreateUnique reserves the exclusive numbered file.
func (w RootWriter) CreateUnique(rel string) (string, writerFile, error) {
	final, f, err := w.Root.CreateUnique(rel)
	if err != nil {
		return "", nil, err
	}
	return final, f, nil
}

// HostPath resolves the absolute host path.
func (w RootWriter) HostPath(rel string) string { return w.Root.HostPath(rel) }

// MarkerStore returns the injected marker store (possibly nil — the save
// then fails closed for HTML, by design).
func (w RootWriter) MarkerStore() *MarkerStore { return w.Marker }

// Remove removes a partial output (the remover surface).
func (w RootWriter) Remove(rel string) error { return w.Root.Delete(rel) }
