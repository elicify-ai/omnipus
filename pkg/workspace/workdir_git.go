// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"errors"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// EnsureWorkDir validates id, creates the workspace's work/ directory if
// absent, and idempotently auto-initializes it as a hidden go-git evidence
// repo (pkg/gitevidence.Open) — the work/-dir auto-init hook required
// wherever a workspace's work/ dir is materialized (spec
// "unified-goal-plan-subagent-spec.md" FR-151, User Story 10).
//
// This is the SANCTIONED replacement for the "SafeWorkDir + os.MkdirAll"
// pattern currently duplicated across call sites —
// pkg/agent/workspace_reroot.go's resolveTurnWorkDirOrRefuse,
// pkg/agent/loop_env.go's wireWorkingDirInjectors, pkg/agent/loop.go's
// CoreTeam re-rooting block, and pkg/gateway/rest_executor_smoketest.go —
// each of which currently calls SafeWorkDir + os.MkdirAll directly and
// never touches the git evidence layer. Migrating those call sites to
// EnsureWorkDir is how the auto-init hook actually becomes wired into
// every code path that materializes a work/ dir; until they do, work/
// directories keep getting created exactly as before (this function is
// purely additive — it changes no existing exported behavior) but without
// git evidence for that particular call path.
//
// EnsureWorkDir NEVER fails the caller's directory-creation intent because
// of the git layer: a git auto-init problem (a nested user repo per
// gitevidence.ErrNestedRepo, or any other gitevidence.Open error) is
// logged and the plain work/ directory is still returned with a nil error
// — the git evidence layer degrades gracefully (MIN-6: "user repo wins,
// ours skips, planner/Judge/owner told" — the WARN log is that signal;
// callers that need to know the evidence layer's live status for a given
// workspace should call gitevidence.Open(dir) themselves and inspect the
// error, e.g. to decide the D13 Play-from-commit fallback).
func EnsureWorkDir(home, id string) (string, error) {
	dir, err := SafeWorkDir(home, id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("workspace: ensure work dir %q: %w", id, err)
	}
	if _, gitErr := gitevidence.Open(dir); gitErr != nil {
		if errors.Is(gitErr, gitevidence.ErrNestedRepo) {
			logger.WarnCF("workspace", "work dir has a nested non-Omnipus git repository; git evidence layer degraded for this workspace", map[string]any{
				"workspace_id": id, "dir": dir, "error": gitErr.Error(),
			})
		} else {
			logger.WarnCF("workspace", "git evidence auto-init failed; continuing without the evidence layer for this workspace", map[string]any{
				"workspace_id": id, "dir": dir, "error": gitErr.Error(),
			})
		}
	}
	return dir, nil
}
