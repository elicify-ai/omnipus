// Omnipus — agent Retry of a private, verified view-membership receipt.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// execRetryMove selects the collection by the private receipt, not a bounded
// collection list or a .view's editable provenance. The normal mutation
// preamble needs a selected collection and so cannot discover a deep root.
func (t *RestructureTool) execRetryMove(ctx context.Context, args map[string]any) *tools.ToolResult {
	target := mutationTarget{
		agentID: tools.ToolAgentID(ctx), workspaceID: TurnWorkspaceID(ctx, t.deps.Home),
	}
	if t.deps.Audit == nil {
		return tools.ErrorResult(string(restructureRetryMoveOp) +
			": refused — no audit sink is configured; this refusal cannot be recorded")
	}
	if _, sent := args["expect_version"]; sent {
		return t.deps.refuse(restructureRetryMoveOp, target, nil,
			"retry_move takes no expect_version: a single-file token cannot guard a multi-file change")
	}
	if unknown := unknownArgs(args, restructureArgNames); len(unknown) > 0 {
		return t.deps.refuse(restructureRetryMoveOp, target, nil, fmt.Sprintf(
			"unknown argument(s) %s; accepted: %s",
			strings.Join(unknown, ", "), strings.Join(restructureArgNames, ", ")))
	}
	for _, name := range []string{"collection", "path", "new_name", "new_folder", "allow_ambiguity", "trashed_at", "folder"} {
		if _, supplied := args[name]; supplied {
			return t.deps.refuse(restructureRetryMoveOp, target, nil,
				"retry_move takes only pending_move_id; remove "+name)
		}
	}
	id := strings.TrimSpace(stringArg(args["pending_move_id"]))
	if id == "" {
		return t.deps.refuse(restructureRetryMoveOp, target, nil, "pending_move_id is required")
	}
	rootPath, relPaths, err := FindViewMoveCollection(t.deps.Home, target.workspaceID, id)
	if err != nil {
		return t.retryMoveError(target, id, nil, nil, true, err)
	}
	if !ResolveScope(t.deps.Home, target.workspaceID).Contains(rootPath) {
		return t.retryMoveError(target, id, nil, nil, true, ErrViewMoveRetryNotFound)
	}
	collection, err := OpenCollection(rootPath)
	if err != nil {
		return t.retryMoveError(target, id, nil, nil, true, err)
	}
	if collection.Root() != rootPath {
		return t.retryMoveError(target, id, nil, nil, true, ErrViewMoveRetryNotFound)
	}
	target.col = ScopedCollection{Name: collection.DisplayName(), Root: rootPath}
	target.collection = collection
	if err := RequireWritableCollection(string(restructureRetryMoveOp), t.deps.Home, target.workspaceID, target.col); err != nil {
		return t.retryMoveError(target, id, relPaths, nil, true, err)
	}
	lockDir, err := LockDirFor(t.deps.Home, rootPath)
	if err != nil {
		return t.retryMoveError(target, id, relPaths, nil, true, err)
	}
	collectionRoot, err := NewCollectionRoot(OSLinkFS(), rootPath)
	if err != nil {
		return t.retryMoveError(target, id, relPaths, nil, true, err)
	}
	paths, mapErr := MapViewRetryLibraryPaths(t.deps.Home, target.workspaceID, rootPath, relPaths)
	if mapErr != nil {
		slog.Warn("knowledge: Retry paths cannot be safely shown to an agent",
			"pending_move_id", id, "error", mapErr)
	}
	if collectionRoot.Path() != rootPath || !ResolveScope(t.deps.Home, target.workspaceID).Contains(rootPath) {
		return t.retryMoveError(target, id, relPaths, nil, true, ErrViewMoveRetryNotFound)
	}
	renamer := &Renamer{
		FS: OSLinkFS(), Root: collectionRoot, AgentID: target.agentID,
		Lock: NoteLockConfig{CollectionRoot: rootPath, LockDir: lockDir},
	}
	res, err := RetryViewMembershipMove(t.deps.Home, renamer, id)
	if err != nil {
		return t.retryMoveError(target, id, relPaths, paths, mapErr == nil, err)
	}
	if res == nil {
		return t.retryMoveError(target, id, relPaths, paths, mapErr == nil,
			errors.New("Retry returned no result"))
	}
	outcome := "re_enrolled"
	if res.NoOp {
		outcome, paths = "already_complete", []string{}
	}
	t.deps.record(AuthorAuditRecord{
		Operation: restructureRetryMoveOp, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: rootPath, Paths: relPaths,
		Reason: "pending_move_id=" + id + " outcome=" + outcome, At: t.deps.now(),
	})
	if mapErr != nil {
		return tools.NewToolResult(fmt.Sprintf(
			"retry_move: outcome: %s; pending_move_id: %s; path list omitted (no safe workspace-relative mapping)",
			outcome, id))
	}
	return tools.NewToolResult(fmt.Sprintf(
		"retry_move: outcome: %s; pending_move_id: %s; re_enrolled_paths: %q",
		outcome, id, paths))
}

// retryMoveError reports only a named, actionable category. Private-index
// failures go to the server log; they never become a cross-workspace oracle.
func (t *RestructureTool) retryMoveError(target mutationTarget, id string, relPaths, paths []string, mapped bool, err error) *tools.ToolResult {
	var lockErr *LockTimeoutError
	var nested *ViewMoveRetryTrackedNestedKBError
	var incomplete *ViewMembershipMoveIncompleteError
	code, message := "", "authority could not be confirmed; check state before retrying"
	switch {
	case errors.Is(err, ErrViewMoveRetryNotFound):
		code, message = "retry_not_found", "pending move not found"
	case errors.Is(err, ErrViewMoveRetryExpired):
		code, message = "retry_expired", "retry receipt expired"
	case errors.As(err, &lockErr):
		code, message = "retry_locked", "collection is locked; retry later"
	case errors.As(err, &nested) && len(nested.Paths) > 0:
		code = "retry_preflight_failed"
		message = "move cannot be replayed safely: " + strings.Join(nested.Paths, ", ") + " is a nested knowledge base with tracked views"
		paths = nested.Paths
	case errors.Is(err, ErrViewMoveRetryPreflight):
		code, message = "retry_preflight_failed", "move cannot be replayed safely; the paths may have changed"
	case errors.Is(err, ErrViewMoveIdentityMismatch):
		code, message = "retry_identity_mismatch", "the revoked view identity no longer matches"
	case errors.As(err, &incomplete):
		code, message = "retry_preflight_failed", "Retry remains incomplete; authority could not be confirmed"
	default:
		slog.Error("knowledge: agent Retry failed", "pending_move_id", id, "error", err)
	}
	text := fmt.Sprintf("pending_move_id: %s; %s", id, message)
	if code != "" {
		text = fmt.Sprintf("code: %s; %s", code, text)
	}
	if mapped && len(paths) > 0 {
		text += fmt.Sprintf("; paths: %q", paths)
	} else if !mapped {
		text += "; path list omitted (no safe workspace-relative mapping)"
	}
	return t.deps.refuse(restructureRetryMoveOp, target, relPaths, text)
}
