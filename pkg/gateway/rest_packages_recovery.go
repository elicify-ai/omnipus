// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages_recovery.go: start-up recovery of unfinished imports (epic
// #1094 S1a T-5; spec revision 7 FR-062, "Start-up recovery"). It runs once
// at gateway start before the first roster load (gateway.go, before
// seedAndPersistAgentRoster), so nothing has loaded a half-imported agent.
// The decisions are omnpkg.RecoverImports'; this file is its store edge.

package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// bootRecoverImports runs start-up recovery on home. It never fails boot
// (FR-062): every failure is logged, the staging keeps its journal, and the
// next upload warns unfinished_import_found (FR-044). auditWrite is where the
// one audit entry per recovered import goes; nil opens the data folder's
// audit log for the run when cfg.Sandbox.AuditLog is on, and writes nothing
// when it is off.
func bootRecoverImports(ctx context.Context, home string, cfg *config.Config,
	auditWrite func(outcome, agentID, stagingID, message string) error,
) {
	ops := &packageRecoveryOps{home: home, cfg: cfg, auditWrite: auditWrite}
	defer ops.close()
	rep := omnpkg.RecoverImports(ctx, filepath.Join(home, "backups", ".staging"), ops)
	for _, err := range rep.Errors {
		slog.Error("packages: start-up recovery step failed; it runs again at the next start", "error", err)
	}
	// WARN when recovery finished or rolled back an import: the shipped
	// gateway.log_level is warn, and an operator must see that an interrupted
	// import was settled. Anything less stays at INFO.
	level := slog.LevelInfo
	if len(rep.Finished)+len(rep.RolledBack) > 0 {
		level = slog.LevelWarn
	}
	if n := len(rep.Finished) + len(rep.RolledBack) + len(rep.OrphansRemoved) + len(rep.Failed); n > 0 {
		slog.Log(ctx, level, "packages: start-up recovery", "finished", rep.Finished, "rolled_back", rep.RolledBack,
			"orphans_removed", rep.OrphansRemoved, "failed", rep.Failed)
	}
}

// recoverImportsBeforeRoster runs start-up recovery on this gateway's home
// (FR-062). It returns an error only when ctx is already done; a recovery
// failure never aborts boot.
func (a *restAPI) recoverImportsBeforeRoster(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	bootRecoverImports(ctx, a.homePath, a.agentLoop.GetConfig(), func(outcome, agentID, stagingID, message string) error {
		return a.packageAudit("recovery", "", agentID, map[string]string{
			"outcome": outcome, "staging_id": stagingID, "message": message,
		})
	})
	return nil
}

// bootImportsThenRoster is the boot order FR-062 requires: recovery, then the
// first roster load. The returned names are the recorded order.
func (a *restAPI) bootImportsThenRoster(ctx context.Context) ([]string, error) {
	if err := a.recoverImportsBeforeRoster(ctx); err != nil {
		return nil, err
	}
	return []string{"recover-imports", "roster"}, nil
}

// packageRecoveryOps is omnpkg.RecoveryOps over the agent store, the shared
// workspace join/leave and the audit log.
type packageRecoveryOps struct {
	home       string
	cfg        *config.Config
	auditWrite func(outcome, agentID, stagingID, message string) error
	logger     *audit.Logger // opened on first use when auditWrite is nil and audit is on
}

func (o *packageRecoveryOps) store() *agentstore.Store { return agentstore.New(o.home) }

func (o *packageRecoveryOps) membership() systools.WorkspaceMembershipOptions {
	cfg := o.cfg
	return systools.WorkspaceMembershipOptions{
		Home: o.home,
		SelfEdgeExcluded: func() map[string]bool {
			if cfg == nil {
				return nil
			}
			return cfg.SelfEdgeExcludeAgentIDSet()
		},
		DepthCeiling: func() int { return delegationDepthCeiling(cfg) },
	}
}

func (o *packageRecoveryOps) Published(agentID string) (bool, error) {
	if err := agentstore.ValidateAgentID(agentID); err != nil {
		return false, err
	}
	_, err := os.Stat(filepath.Join(o.store().Dir(), agentID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (o *packageRecoveryOps) Leave(wsID, agentID string) error {
	if packageT5.RecoveryLeave != nil {
		return packageT5.RecoveryLeave(wsID, agentID)
	}
	_, err := systools.LeaveWorkspaceTeam(o.membership(), wsID, agentID)
	return err
}

func (o *packageRecoveryOps) Join(wsID, agentID string) error {
	joined, err := systools.JoinWorkspaceTeam(o.membership(), wsID, agentID)
	if err != nil {
		return err
	}
	if !joined {
		return fmt.Errorf("agent %s is not a member of workspace %s after the re-join", agentID, wsID)
	}
	return nil
}

func (o *packageRecoveryOps) DiscardPending(agentID string) error {
	return o.store().DiscardPending(agentID)
}

// UnpublishAndDiscard turns the published record back into a pending one and
// discards it (FR-062: a committed import that cannot be re-joined). Safe
// only before the first roster load, which is the only time recovery runs.
func (o *packageRecoveryOps) UnpublishAndDiscard(agentID string) error {
	if err := agentstore.ValidateAgentID(agentID); err != nil {
		return err
	}
	dir := o.store().Dir()
	if err := os.Rename(filepath.Join(dir, agentID+".json"), filepath.Join(dir, agentID+".pending")); err != nil {
		return fmt.Errorf("unpublish %s: %w", agentID, err)
	}
	return o.store().DiscardPending(agentID)
}

func (o *packageRecoveryOps) PendingIDs() ([]string, error) {
	entries, err := os.ReadDir(o.store().Dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".pending"); ok && !e.IsDir() && agentstore.ValidateAgentID(id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (o *packageRecoveryOps) Audit(outcome, agentID, stagingID, message string) {
	err := o.writeAudit(outcome, agentID, stagingID, message)
	if err != nil {
		slog.Error("packages: audit of a recovered import failed", "agent_id", agentID, "staging_id", stagingID, "error", err)
	}
}

func (o *packageRecoveryOps) writeAudit(outcome, agentID, stagingID, message string) error {
	if o.auditWrite != nil {
		return o.auditWrite(outcome, agentID, stagingID, message)
	}
	if packageT5.AuditWrite != nil {
		return packageT5.AuditWrite("recovery", map[string]string{"outcome": outcome, "staging_id": stagingID})
	}
	if o.cfg == nil || !o.cfg.Sandbox.AuditLog {
		// Audit logging is off (ARCH-RULING-T5-A-D, Decision D): no entry.
		slog.Info("packages: audit entry not written: audit log is off", "operation", "recovery")
		return nil
	}
	if o.logger == nil {
		l, err := audit.NewLogger(audit.LoggerConfig{
			Dir: filepath.Join(o.home, "system"), RedactEnabled: true, AuditLogRequested: true, RetentionDays: 90,
		})
		if err != nil {
			return fmt.Errorf("open audit log: %w", err)
		}
		o.logger = l
	}
	return writePackageAuditEntry(o.logger, "recovery", "", agentID, map[string]string{
		"outcome": outcome, "staging_id": stagingID, "message": message,
	})
}

func (o *packageRecoveryOps) close() {
	if o.logger == nil {
		return
	}
	if err := o.logger.Close(); err != nil {
		slog.Warn("packages: close start-up recovery audit log", "error", err)
	}
}
