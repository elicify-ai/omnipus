package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func recoveryReadOnly(tool tools.Tool) bool {
	switch tool.(type) {
	case *tools.ReadFileTool, *tools.ListDirTool, *tools.GrepTool:
		return true
	default:
		return false
	}
}

// witnessFaultReason classifies a witness read failure into its safe fault
// class (SF5, DESIGN 2/5): the saved hold reason names the failed evidence
// operation and keeps the underlying storage/decoder distinction, without
// leaking storage paths. Malformed JSON and genuinely missing storage need
// different repair actions; the generic not-provable text erases both.
func witnessFaultReason(err error) string {
	class := "is unreadable and the fault could not be classified"
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fs.ErrNotExist):
		class = "is missing from recovery storage (no such file)"
	case errors.Is(err, fs.ErrPermission):
		class = "cannot be read with current storage permissions"
	default:
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxErr):
			class = "is malformed and fails to decode (invalid JSON syntax or character)"
		case errors.As(err, &typeErr):
			class = "is malformed and fails to decode (invalid JSON value)"
		}
	}
	return "file-effect witness " + class
}

func (al *AgentLoop) recoveryReadContext(ctx context.Context, rec *session.LifecycleRecord, inst *AgentInstance) (context.Context, error) {
	ctx = tools.WithAgentID(ctx, rec.AgentID)
	ctx = tools.WithWorkspaceID(ctx, rec.WorkspaceID)
	if rec.WorkspaceID != "" {
		if err := restartResumeWorkspaceRefusal(rec.AgentID, rec.WorkspaceID); err != nil {
			return ctx, err
		}
		dir, err := workspace.SafeWorkDir(omnipusHome(), rec.WorkspaceID)
		if err != nil {
			return ctx, err
		}
		ctx = tools.WithTurnWorkspaceDir(ctx, dir)
	} else {
		ctx = tools.WithTurnWorkspaceDir(ctx, inst.Home)
	}
	return ctx, nil
}

func (al *AgentLoop) recoverSessionToolCalls(ctx context.Context, inst *AgentInstance, rec *session.LifecycleRecord, key string) ([]providers.Message, error) {
	store, ok := inst.Sessions.(session.ContextWindowStore)
	if !ok {
		return nil, fmt.Errorf("tool recovery: addressed store is unavailable")
	}
	view, err := store.WindowView(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := settleAccountedFileEffects(ctx, rec.SessionID, view); err != nil {
		return nil, err
	}
	history, lines := recoveryWindowHistory(view, false)
	readCtx, contextErr := al.recoveryReadContext(ctx, rec, inst)
	var recoveryErr error
	orphans := findOrphanedToolCalls(history)
	unresolvedTargets := map[string]int{}
	witnesses := map[restartCallIdentity]tools.FileEffectWitness{}
	witnessFaults := map[restartCallIdentity]error{}
	for _, o := range orphans {
		slot, found := view.Slot(lines[o.assistantIndex])
		if !found {
			continue
		}
		w, e := tools.ReadFileEffectWitness(omnipusHome(), rec.SessionID, slot.Addr, o.ToolCallID)
		if e == nil {
			witnesses[restartCallIdentity{assistantIndex: o.assistantIndex, toolCallID: o.ToolCallID}] = w
			unresolvedTargets[w.Target]++
		} else {
			// SF5 (DESIGN 2/5): the specific safe fault class survives into the
			// saved hold reason, so the repair report names what actually failed
			// instead of the generic not-provable text.
			witnessFaults[restartCallIdentity{assistantIndex: o.assistantIndex, toolCallID: o.ToolCallID}] = e
		}
	}
	resolve := func(o orphanedToolCall) (recoveryObservation, error) {
		slot, found := view.Slot(lines[o.assistantIndex])
		if !found {
			return recoveryObservation{}, fmt.Errorf("tool recovery: exact issuing occurrence is missing")
		}
		ob := recoveryObservation{Disposition: tools.FileEffectUnknown, Reason: "no current-state proof adapter", Assistant: slot.Addr, Execution: slot.Source.Execution}
		tool, found := inst.Tools.GetIncludingHidden(o.ToolName)
		if _, supported := tool.(tools.FileEffectReconciler); supported {
			if fault := witnessFaults[restartCallIdentity{assistantIndex: o.assistantIndex, toolCallID: o.ToolCallID}]; fault != nil {
				ob.Reason = witnessFaultReason(fault)
			} else {
				ob.Reason = "file-effect witness, issuing identity or current read context is not provable"
			}
		}
		if found && recoveryReadOnly(tool) {
			ob.Disposition = tools.FileEffectAbsent
			ob.Reason = "read-only call interrupted; no mutation proof required"
		} else if adapter, ok := tool.(tools.FileEffectReconciler); ok && contextErr == nil {
			if w, found := witnesses[restartCallIdentity{assistantIndex: o.assistantIndex, toolCallID: o.ToolCallID}]; found && unresolvedTargets[w.Target] == 1 && slot.Source.Execution != nil && *slot.Source.Execution == w.Execution {
				var args map[string]any
				for _, call := range slot.Message.ToolCalls {
					if call.ID == o.ToolCallID {
						args = call.Arguments
						if args == nil && call.Function != nil {
							_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
						}
						break
					}
				}
				encoded, e := json.Marshal(args)
				if e == nil && tools.FileEffectArgumentDigest(encoded) == w.ArgumentDigest {
					checked := adapter.ReconcileFileEffect(readCtx, w)
					ob.Disposition, ob.Reason = checked.Disposition, checked.Reason
					ob.Image = &checked.Image
				}
			}
		}
		if ob.Disposition == tools.FileEffectUnknown {
			if err := recordRecoveryHold(rec.SessionID, tools.FileEffectKey(slot.Addr, o.ToolCallID), ob.Reason, recoveryHeldOperation{Tool: o.ToolName, CallID: o.ToolCallID}); err != nil {
				return ob, err
			}
		}
		return ob, nil
	}
	result := RecoverOrphanedToolCalls(inst.Sessions, key, al.auditLogger, orphanRecoveryOptions{Resolve: resolve, OnError: func(err error) { recoveryErr = err }, View: &view})
	if recoveryErr != nil {
		return nil, recoveryErr
	}
	saved, err := store.WindowView(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := settleAccountedFileEffects(ctx, rec.SessionID, saved); err != nil {
		return nil, err
	}
	held, err := loadRecoveryHold(rec.SessionID)
	if err != nil {
		return nil, err
	}
	if len(held.Occurrences) > 0 && !recoveryConsentAllows(rec, al.executionClaimFor(rec), held) {
		return result, fmt.Errorf("reconciliation_required: unresolved file effects require an explicit continuation after the report")
	}
	return result, nil
}

func (al *AgentLoop) enforceRecoveryWriteFence(ts *turnState, toolName string) error {
	tool, ok := ts.agent.Tools.GetIncludingHidden(toolName)
	if ok && recoveryReadOnly(tool) {
		return nil
	}
	if err := al.enforceInheritedTaskRecovery(ts); err != nil {
		return err
	}
	if lc := al.GetSessionLifecycleStore(); lc != nil {
		id := ts.transcriptSessionID
		if id == "" {
			id = ts.sessionKey
		}
		rec, err := lc.Load(id)
		if err != nil && !errors.Is(err, session.ErrLifecycleNotFound) {
			return err
		}
		if err == nil && rec.SteeredBy != nil {
			if err := al.refuseRetiredRoomWork(rec); err != nil {
				return err
			}
		}
	}
	id := ts.transcriptSessionID
	if id == "" {
		id = ts.sessionKey
	}
	held, err := loadRecoveryHold(id)
	if err != nil {
		return err
	}
	if len(held.Occurrences) == 0 {
		return nil
	}
	rec, err := al.GetSessionLifecycleStore().Load(id)
	if err != nil {
		return err
	}
	if !recoveryConsentAllows(rec, al.tsExecutionClaim(ts, id), held) {
		return fmt.Errorf("reconciliation_required: automatic mutation is fenced")
	}
	return nil
}
