// stub_t5_for_red.go — compile-only RED stubs for epic #1094 slice S1a task T-5
// (the package export/import handlers).
//
// DELETE when GREEN lands. Every handler here writes 501 not_implemented so
// the RED tests in packages_t5_*_test.go fail on an assertion (expected spec
// status versus 501), never on an undefined symbol. Nothing in this file
// implements the spec.
//
// Shapes the tests call, which GREEN's pkg/gateway/rest_packages.go must keep:
//
//	handlePackageExport, handlePackageImport, handlePackageImportApply
//	registerPackageRoutes — called from the local-mode block of rest.go
//	recoverImportsBeforeRoster — omnpkg.RecoverImports, before the first
//	    roster load (gateway.go, before seedAndPersistAgentRoster)
//	bootImportsThenRoster — that order, returning the step names
//	packageT5 — fault seams (nil / empty in production; a guard lives in the
//	    rollback tests). GREEN reads these and forwards them. Names match
//	    spec revision 7: crashAt, audit write, reload, ensureMainSession,
//	    WriteWorkspace, SaveDelegation, AfterMembershipWrite, publish rename,
//	    DiscardPending, CreatePending fault, recovery leave, directory sync, clock.
package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// packageT5Seams is the fault-injection surface for the T-5 handlers.
// Every field is nil or empty in production. Tests set a field and restore
// it; GREEN's apply, upload, export and start-up recovery must honor it.
type packageT5Seams struct {
	// AuditWrite replaces the audit write. A non-nil error is "the audit write
	// fails" (FR-016, FR-040): export is refused with no bytes; an upload
	// refusal or a rollback keeps its true answer; after the commit point the
	// result carries the warning audit_failed.
	AuditWrite func(kind string, fields map[string]string) error

	// CrashAt, when non-empty, stops apply at that step without running
	// compensations (spec "Not guaranteed" N2). Steps: "E1", "E4",
	// "membership", "self-row", "rename", "committed", "P1".
	CrashAt string

	// Reload replaces triggerReloadAndWaitOutcome after the commit point.
	// confirmed false with a non-nil error is D7 row 7; ReloadTimeout is D7
	// row 17 (activation pending, no agent_created, no main chat).
	Reload        func() (confirmed bool, err error)
	ReloadTimeout bool

	// EnsureMain replaces ensureMainSession for the imported agent (D7 row 8).
	EnsureMain func(agentID, workspaceID string) error

	// Membership seams forwarded into JoinWorkspaceTeamAndPublish options.
	// nil means the real function (sign-off C-5, as amended by revision 7).
	WriteWorkspace       func(dir, wsID string, w workspace.Workspace) error
	SaveDelegation       func(home, wsID string, edges []workspace.DelegationEdge) error
	AfterMembershipWrite func(wsID, agentID string)

	// PublishRename is the rename seam inside PublishPending (D7 row 22).
	// nil means os.Rename.
	PublishRename func(from, to string) error

	// DiscardPending replaces agentstore.Store.DiscardPending (D7 rows 6, 18).
	DiscardPending func(id string) error

	// CreatePendingFault makes CreatePendingState fail part-way:
	// "soul" (D7 row 1), "agentmd" (D7 row 3). Empty means no fault.
	CreatePendingFault string

	// RecoveryLeave replaces LeaveWorkspaceTeam during start-up recovery
	// (D7 row 23g). nil means the real leave.
	RecoveryLeave func(wsID, agentID string) error

	// SyncDir is N2's directory-sync seam. nil means the real sync. Tests
	// count calls and require they happen before the answer.
	SyncDir func(dir string) error

	// Now is the staging clock (Dataset D9). nil means time.Now.
	Now func() time.Time
}

// packageT5 is the process-wide seam set. Tests must not run in parallel:
// each test copies this value and restores it with t.Cleanup.
var packageT5 packageT5Seams

// packageSeamsBlocked references every seam so the RED stub keeps the fault
// surface compiled in. The result does not change the 501 answer.
func packageSeamsBlocked() bool {
	s := packageT5
	return s.AuditWrite != nil || s.CrashAt != "" || s.Reload != nil || s.ReloadTimeout ||
		s.EnsureMain != nil || s.WriteWorkspace != nil || s.SaveDelegation != nil ||
		s.AfterMembershipWrite != nil || s.PublishRename != nil || s.DiscardPending != nil ||
		s.CreatePendingFault != "" || s.RecoveryLeave != nil || s.SyncDir != nil || s.Now != nil
}

func packageNotImplemented(w http.ResponseWriter) {
	_ = packageSeamsBlocked()
	jsonErrCode(w, http.StatusNotImplemented, "not implemented", "not_implemented")
}

// handlePackageExport is POST /api/v1/packages/export (FR-010).
func (a *restAPI) handlePackageExport(w http.ResponseWriter, r *http.Request) {
	packageNotImplemented(w)
}

// handlePackageImport is POST /api/v1/packages/import (FR-020, FR-021).
func (a *restAPI) handlePackageImport(w http.ResponseWriter, r *http.Request) {
	packageNotImplemented(w)
}

// handlePackageImportApply is POST /api/v1/packages/import/{staging_id}/apply
// (FR-030, FR-037). The staging id is the path segment between "import/" and
// "/apply".
func (a *restAPI) handlePackageImportApply(w http.ResponseWriter, r *http.Request) {
	packageNotImplemented(w)
}

// registerPackageRoutes registers the three package routes. GREEN calls it
// from the local-mode block of registerAdditionalEndpoints (FR-045): local
// mode only, withAuth, RequireNotBypass, upload body limit 64 MiB, export and
// apply JSON body limit 1 MiB. The RED stub registers the handlers with no
// middleware and no edition check, and they answer 501.
func (a *restAPI) registerPackageRoutes(cm httpHandlerRegistrar) {
	cm.RegisterHTTPHandler("/api/v1/packages/export", http.HandlerFunc(a.handlePackageExport))
	cm.RegisterHTTPHandler("/api/v1/packages/import", http.HandlerFunc(a.handlePackageImport))
	cm.RegisterHTTPHandler("/api/v1/packages/import/", http.HandlerFunc(a.handlePackageImportApply))
}

// recoverImportsHook is the RED stand-in. A var, not a direct return, so the
// call is not a constant (staticcheck SA4023). GREEN replaces the var.
var recoverImportsHook = func(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("not implemented")
}

// recoverImportsBeforeRoster runs start-up recovery (FR-062, revision 7)
// before any roster load. The RED stub does not touch the disk.
func (a *restAPI) recoverImportsBeforeRoster(ctx context.Context) error {
	_ = packageSeamsBlocked()
	return recoverImportsHook(ctx)
}

// bootImportsThenRoster is the boot order FR-062 requires: recovery, then the
// first roster load. The returned names are the recorded order. The RED stub
// returns neither a successful recovery nor a roster load.
func (a *restAPI) bootImportsThenRoster(ctx context.Context) ([]string, error) {
	if err := a.recoverImportsBeforeRoster(ctx); err != nil {
		return nil, err
	}
	return []string{"recover-imports", "roster"}, nil
}
