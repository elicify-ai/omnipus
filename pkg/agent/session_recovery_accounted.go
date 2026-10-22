package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// settleAccountedFileEffects repairs a crash between a durable original result
// (or checked present/absent observation) and its content-free writer-index update.
// An Unknown observation never retires an unresolved writer.
func settleAccountedFileEffects(ctx context.Context, sessionID string, view session.WindowView) error {
	issuers := map[string]session.ModelSlot{}
	for _, slot := range view.Live {
		if slot.Message.Role == "assistant" && len(slot.Message.ToolCalls) > 0 {
			issuers[slot.Addr.EntryID] = slot
		}
	}
	if slot := view.Anchor; slot != nil && slot.Message.Role == "assistant" {
		issuers[slot.Addr.EntryID] = *slot
	}
	for _, slot := range view.Live {
		if slot.Message.Role != "tool" || slot.ToolResultFor == nil {
			continue
		}
		if slot.Source.Kind != "tool" && slot.Source.Kind != "recovery_observation" {
			continue
		}
		issuer, found := issuers[slot.ToolResultFor.AssistantEntryID]
		if !found || issuer.Source.Execution == nil {
			continue
		}
		if slot.Source.Kind == "recovery_observation" {
			var ob recoveryObservation
			if err := json.Unmarshal([]byte(slot.Message.Content), &ob); err != nil {
				return err
			}
			if ob.Type != "tool_recovery_observation" || ob.OriginalResultRecovered || ob.Assistant != issuer.Addr {
				return fmt.Errorf("file recovery: saved observation identity is corrupt")
			}
			if ob.Disposition != tools.FileEffectPresent && ob.Disposition != tools.FileEffectAbsent {
				continue
			}
		}
		w, err := tools.ReadFileEffectWitness(omnipusHome(), sessionID, issuer.Addr, slot.Message.ToolCallID)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if w.Execution != *issuer.Source.Execution {
			return fmt.Errorf("file recovery: result and witness executions differ")
		}
		accounted := tools.WithFileEffectContext(ctx, tools.FileEffectContext{Home: omnipusHome(), SessionID: sessionID,
			Assistant: issuer.Addr, ToolCallID: slot.Message.ToolCallID, Execution: w.Execution})
		if err := tools.SettleFileEffectWitness(accounted); err != nil {
			return err
		}
	}
	return nil
}
