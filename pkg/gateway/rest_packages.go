// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages.go: the agent package routes (epic #1094 slice S1a, task T-5;
// spec docs/internal/specs/export-import-s1-spec.md revision 7). This file
// holds the routes, the fault seams, the per-gateway package state, the error
// mapping and the audit write. The handlers live in rest_packages_export.go,
// rest_packages_import.go and rest_packages_apply.go; start-up recovery in
// rest_packages_recovery.go. The format itself is pkg/omnpkg.

package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Route body limits (FR-008, FR-045).
const (
	packageUploadBodyLimit = 64 << 20 // upload: 64 MiB
	packageJSONBodyLimit   = 1 << 20  // export and apply JSON: 1 MiB
)

// Gateway-side codes that are not package-format codes (spec error table).
const (
	packageCodeAgentNotFound       = "agent_not_found"
	packageCodeAuditFailed         = "audit_failed"
	packageCodeModeNotAvailable    = "import_mode_not_available"
	packageCodeWorkspaceNotFound   = "workspace_not_found"
	packageCodeDefaultWSMissing    = "default_workspace_missing"
	packageCodeChangedSincePreview = "changed_since_preview"
	packageCodeMainChatPending     = "main_chat_pending"
	packageCodeUnfinishedImport    = "unfinished_import_found"
	packageCodePublishNotDurable   = "publish_not_durable"
)

// packageT5Seams is the fault-injection surface for the package handlers.
// Every field is nil or empty in production (a guard test asserts it).
type packageT5Seams struct {
	// AuditWrite replaces the audit write. A non-nil error is "the audit write
	// fails" (FR-016, FR-040).
	AuditWrite func(kind string, fields map[string]string) error

	// CrashAt names an apply step. "membership" is a fault just before the
	// join (Dataset D7 rows 2, 6, 18): the normal rollback runs. Every other
	// step stops the apply there without compensations, as a crash would
	// (spec N2): "E1", "E4", then inside the join helper's lock hold
	// "after-membership" (W written, no self-row), "self-row" (self-row
	// written, not published) and "rename" (published, journal still
	// started), then "committed" and "P1".
	CrashAt string

	// Reload replaces triggerReloadAndWaitOutcome after the commit point.
	// ReloadTimeout marks the Reload failure as a timeout (D7 row 17).
	Reload        func() (confirmed bool, err error)
	ReloadTimeout bool

	// EnsureMain replaces ensureMainSession for the imported agent (D7 row 8).
	EnsureMain func(agentID, workspaceID string) error

	// Membership seams forwarded into JoinWorkspaceTeamAndPublish options.
	WriteWorkspace       func(dir, wsID string, w workspace.Workspace) error
	SaveDelegation       func(home, wsID string, edges []workspace.DelegationEdge) error
	AfterMembershipWrite func(wsID, agentID string)

	// PublishRename runs before the commit rename (D7 row 22); an error is a
	// failed rename. nil means the plain rename inside PublishPending.
	PublishRename func(from, to string) error

	// DiscardPending replaces agentstore.Store.DiscardPending (D7 rows 6, 18).
	DiscardPending func(id string) error

	// CreatePendingFault makes the pending write count as failed part-way:
	// "soul" (D7 row 1), "agentmd" (D7 row 3).
	CreatePendingFault string

	// RecoveryLeave replaces LeaveWorkspaceTeam during start-up recovery.
	RecoveryLeave func(wsID, agentID string) error

	// SyncDir replaces the workspace and delegation directory syncs (N2).
	SyncDir func(dir string) error

	// Now is the staging clock (Dataset D9). nil means time.Now.
	Now func() time.Time
}

// packageT5 is the process-wide seam set. Tests must not run in parallel.
var packageT5 packageT5Seams

// packageSeamsBlocked reports whether any fault seam is set. Production must
// see false (D7 closing note; guarded by a test).
func packageSeamsBlocked() bool {
	s := packageT5
	return s.AuditWrite != nil || s.CrashAt != "" || s.Reload != nil || s.ReloadTimeout ||
		s.EnsureMain != nil || s.WriteWorkspace != nil || s.SaveDelegation != nil ||
		s.AfterMembershipWrite != nil || s.PublishRename != nil || s.DiscardPending != nil ||
		s.CreatePendingFault != "" || s.RecoveryLeave != nil || s.SyncDir != nil || s.Now != nil
}

func packageNow() time.Time {
	if packageT5.Now != nil {
		return packageT5.Now()
	}
	return time.Now()
}

// packageState is the per-gateway package state: the staging registry and
// what each preview showed (apply lists a change as changed_since_preview,
// FR-041, FR-042).
type packageState struct {
	mu       sync.Mutex
	registry *omnpkg.StagingRegistry
	previews map[string]packagePreviewSnapshot // staging ID -> what the preview showed
}

// packagePreviewSnapshot is what a preview showed that apply re-checks: the
// proposed name, the skills left out as not installed and the providers not
// configured on this install.
type packagePreviewSnapshot struct {
	Name                  string
	DroppedSkills         []string
	UnconfiguredProviders []string
}

// previewSnapshotOf is the snapshot of one planned agent.
func previewSnapshotOf(plan *packageAgentPlan) packagePreviewSnapshot {
	snap := packagePreviewSnapshot{Name: plan.Name}
	for _, w := range plan.Warnings {
		switch w.Code {
		case omnpkg.WarnSkillDropped:
			snap.DroppedSkills = append(snap.DroppedSkills, w.Items...)
		case omnpkg.WarnProviderNotConfigured:
			snap.UnconfiguredProviders = append(snap.UnconfiguredProviders, w.Items...)
		}
	}
	return snap
}

func (a *restAPI) packageStagingBase() string {
	return filepath.Join(a.homePath, "backups", ".staging")
}

// packageRegistry returns the gateway's staging registry, built on first use.
func (a *restAPI) packageRegistry() *omnpkg.StagingRegistry {
	a.packages.mu.Lock()
	defer a.packages.mu.Unlock()
	if a.packages.registry == nil {
		a.packages.registry = omnpkg.NewStagingRegistry(a.packageStagingBase(), packageNow)
		a.packages.previews = map[string]packagePreviewSnapshot{}
	}
	return a.packages.registry
}

func (a *restAPI) rememberPreview(stagingID string, snap packagePreviewSnapshot) {
	a.packages.mu.Lock()
	defer a.packages.mu.Unlock()
	a.packages.previews[stagingID] = snap
}

func (a *restAPI) previewSnapshot(stagingID string) (packagePreviewSnapshot, bool) {
	a.packages.mu.Lock()
	defer a.packages.mu.Unlock()
	snap, ok := a.packages.previews[stagingID]
	return snap, ok
}

func (a *restAPI) forgetPreview(stagingID string) {
	a.packages.mu.Lock()
	defer a.packages.mu.Unlock()
	delete(a.packages.previews, stagingID)
}

// registerPackageRoutes registers the three package routes. It is called from
// the local-mode block of registerAdditionalEndpoints only (FR-045): withAuth
// and RequireNotBypass on all three, the upload with a 64 MiB body limit,
// export and apply with the 1 MiB JSON limit.
func (a *restAPI) registerPackageRoutes(cm httpHandlerRegistrar) {
	cm.RegisterHTTPHandler("/api/v1/packages/export", a.adminWrap(a.handlePackageExport))
	cm.RegisterHTTPHandler("/api/v1/packages/import",
		a.withAuthAndBodyLimit(middleware.RequireNotBypass(a.handlePackageImport), packageUploadBodyLimit))
	cm.RegisterHTTPHandler("/api/v1/packages/import/", a.adminWrap(a.handlePackageImportApply))
}

// packageMessages are the human messages per code. A code with no entry uses
// a generic text naming the code.
var packageMessages = map[string]string{
	omnpkg.CodeNotAPackage:                "the file is not an Omnipus package",
	omnpkg.CodePackageTooNew:              "the package was made by a newer Omnipus; update this install to import it",
	omnpkg.CodeElementSchemaTooNew:        "an element in the package was made by a newer Omnipus",
	omnpkg.CodePackageTooLarge:            "the package is too large",
	omnpkg.CodeChecksumMismatch:           "a file in the package does not match its checksum",
	omnpkg.CodeManifestMismatch:           "the package contents do not match its manifest",
	omnpkg.CodeUnsafePath:                 "the package contains an unsafe path",
	omnpkg.CodeUnsafeEntryType:            "the package contains an entry that is not a regular file or folder",
	omnpkg.CodeNoSupportedElement:         "the package contains no element this install can import",
	omnpkg.CodeTooManyAgents:              "the package contains more than one agent",
	omnpkg.CodeElementTypeNotSupported:    "the package contains an element type this install does not import",
	omnpkg.CodeInvalidElement:             "an element in the package is not valid",
	omnpkg.CodeElementNotCreatable:        "the agent in the package breaks a create rule",
	omnpkg.CodeExternalWorkerNotSupported: "third-party workers cannot be exported or imported yet",
	omnpkg.CodeCoreAgentNotExportable:     "built-in agents cannot be exported",
	omnpkg.CodeAgentFileUnreadable:        "an agent file could not be read safely",
	omnpkg.CodeAgentPromptMissing:         "the agent has no prompt",
	omnpkg.CodeExportSelectionInvalid:     "select exactly one agent to export",
	omnpkg.CodeStagingNotFound:            "the staged import was not found or has expired; upload the package again",
	omnpkg.CodeTooManyStagedImports:       "too many staged imports; apply or wait for one to expire",
	omnpkg.CodeImportInProgress:           "another import is being applied; try again when it has finished",
	omnpkg.CodeNameSuffixExhausted:        "every \"(imported N)\" name is taken; rename an existing agent first",
	omnpkg.CodeImportRolledBack:           "the import failed and was undone",
	omnpkg.CodeImportRollbackIncomplete:   "the import failed and could not be fully undone; restart the gateway to finish the clean-up",
	packageCodeAgentNotFound:              "agent not found",
	packageCodeAuditFailed:                "the audit log could not be written",
	packageCodeModeNotAvailable:           "only mode \"new\" is available",
	packageCodeWorkspaceNotFound:          "workspace not found",
	packageCodeDefaultWSMissing:           "there is no default workspace; name a workspace to import into",
}

func packageMessage(code string) string {
	if msg, ok := packageMessages[code]; ok {
		return msg
	}
	return "package error: " + code
}

// packageStatus maps a package-format code to its HTTP status (spec error table).
func packageStatus(pe *omnpkg.PackageError) int {
	switch pe.Code {
	case omnpkg.CodeExportSelectionInvalid:
		return http.StatusBadRequest
	case packageCodeAgentNotFound, omnpkg.CodeStagingNotFound, packageCodeWorkspaceNotFound:
		return http.StatusNotFound
	case omnpkg.CodePackageTooLarge:
		if pe.Detail["limit"] == omnpkg.LimitWindow {
			return http.StatusUnprocessableEntity
		}
		return http.StatusRequestEntityTooLarge
	case omnpkg.CodeTooManyStagedImports, omnpkg.CodeImportInProgress, omnpkg.CodeNameSuffixExhausted,
		packageCodeDefaultWSMissing:
		return http.StatusConflict
	case omnpkg.CodeImportRolledBack, omnpkg.CodeImportRollbackIncomplete, packageCodeAuditFailed:
		return http.StatusInternalServerError
	default:
		return http.StatusUnprocessableEntity
	}
}

// writePackageError writes a coded package refusal as gen.ErrorResponse.
// details carries extra values (failed_step, leftovers …) next to the
// error's own detail entries. A reason detail is added to the message.
func writePackageError(w http.ResponseWriter, pe *omnpkg.PackageError, details map[string]any) {
	code := pe.Code
	resp := gen.ErrorResponse{Error: packageMessage(code), Code: &code}
	if reason := pe.Detail["reason"]; reason != "" {
		resp.Error += ": " + reason
	}
	if pe.Field != "" {
		field := pe.Field
		resp.Field = &field
	}
	merged := map[string]any{}
	for k, v := range pe.Detail {
		merged[k] = v
	}
	for k, v := range details {
		merged[k] = v
	}
	if len(merged) > 0 {
		resp.Details = &merged
	}
	writeJSON(w, packageStatus(pe), resp)
}

// writePackageErr writes err: a coded package error with its status, a body
// over the route limit as 413, anything else as a logged 500.
func writePackageErr(w http.ResponseWriter, op string, err error) {
	var pe *omnpkg.PackageError
	if errors.As(err, &pe) {
		writePackageError(w, pe, nil)
		return
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		jsonErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	slog.Error("packages: request failed", "operation", op, "error", err)
	jsonErr(w, http.StatusInternalServerError, "internal server error")
}

// decodePackageJSON decodes the export or apply JSON body into dst and then
// reads the rest of the body through the route's 1 MiB limiter, so a body
// over the limit is refused even after a complete JSON value (FR-045).
// tooLarge reports that limit; any other error is an unreadable body.
func decodePackageJSON(r *http.Request, dst any) (tooLarge bool, err error) {
	err = json.NewDecoder(r.Body).Decode(dst)
	if err == nil {
		if _, err = io.Copy(io.Discard, r.Body); err != nil {
			err = fmt.Errorf("read request body: %w", err)
		}
	}
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe), err
}

func packageErr(code string, kv ...string) *omnpkg.PackageError {
	pe := &omnpkg.PackageError{Code: code}
	if len(kv) > 1 {
		pe.Detail = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			pe.Detail[kv[i]] = kv[i+1]
		}
	}
	return pe
}

// packageActor is the audit actor: the authenticated user withAuth put in
// the context, else (a handler reached without withAuth, as in unit tests)
// the identity the request's bearer resolves to, else "".
func (a *restAPI) packageActor(r *http.Request) string {
	if actor := auditActor(r); actor != "" {
		return actor
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		return ""
	}
	if user, _, matched := resolveBearerIdentity(a.agentLoop.GetConfig(), raw); matched && user != nil {
		return user.Username
	}
	if env := os.Getenv("OMNIPUS_BEARER_TOKEN"); env != "" && subtle.ConstantTimeCompare([]byte(raw), []byte(env)) == 1 {
		return envTokenUser.Username
	}
	return ""
}

// packageAuditLogger is the shared audit logger package events go to: the
// gateway's, else the agent loop's. nil means audit logging was off when the
// gateway started (sandbox.audit_log): package operations then write no
// entry and are not refused for it (ARCH-RULING-T5-A-D, Decision D).
func (a *restAPI) packageAuditLogger() *audit.Logger {
	if a.auditor != nil {
		return a.auditor
	}
	if a.agentLoop != nil {
		return a.agentLoop.AuditLogger()
	}
	return nil
}

// packageAudit writes one package audit entry. kind is the operation
// (export, upload, import, recovery); the entry's Details carry it as
// "operation" plus fields. With audit logging off it writes nothing.
func (a *restAPI) packageAudit(kind, actor, agentID string, fields map[string]string) error {
	if packageT5.AuditWrite != nil {
		return packageT5.AuditWrite(kind, fields)
	}
	logger := a.packageAuditLogger()
	if logger == nil {
		slog.Info("packages: audit entry not written: audit log is off", "operation", kind)
		return nil
	}
	return writePackageAuditEntry(logger, kind, actor, agentID, fields)
}

// packageAuditEvents maps a package operation to its registered audit event.
var packageAuditEvents = map[string]string{
	"export":   audit.EventPackageExport,
	"upload":   audit.EventPackageUpload,
	"import":   audit.EventPackageImport,
	"recovery": audit.EventPackageRecovery,
}

func writePackageAuditEntry(logger *audit.Logger, kind, actor, agentID string, fields map[string]string) error {
	event, ok := packageAuditEvents[kind]
	if !ok {
		return fmt.Errorf("package audit: unknown operation %q", kind)
	}
	details := map[string]any{"operation": kind}
	for k, v := range fields {
		details[k] = v
	}
	return logger.Log(&audit.Entry{
		Event:   event,
		AgentID: agentID,
		User:    actor,
		Details: details,
	})
}

// syncPackageDir makes renames inside dir durable on Linux and macOS (spec
// N2; Go cannot sync a directory on Windows, so there it does nothing).
func syncPackageDir(dir string) error {
	if packageT5.SyncDir != nil {
		return packageT5.SyncDir(dir)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s for sync: %w", dir, err)
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil || closeErr != nil {
		return fmt.Errorf("sync %s: %w", dir, errors.Join(syncErr, closeErr))
	}
	return nil
}

// resized returns a fresh slice of s's type with n zero elements. The
// generated result and preview types use anonymous element structs; this
// fills them without restating the generated shape.
func resized[S ~[]E, E any](_ S, n int) S { return make(S, n) }

// packageWarning is one result or preview warning before it is put on the
// wire (internal, not a wire type).
type packageWarning struct {
	Code, ElementSourceID, Message string
	Items                          []string
}

func warningsFromEngine(ws []omnpkg.Warning) []packageWarning {
	out := make([]packageWarning, 0, len(ws))
	for _, w := range ws {
		out = append(out, packageWarning{Code: w.Code, ElementSourceID: w.ElementSourceID, Message: w.Message, Items: w.Items})
	}
	return out
}
