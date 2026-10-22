package agent

import (
	"encoding/json"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// not-wire-format: engine-owned model history observation, never an original return.
type recoveryObservation struct {
	Type                    string                     `json:"type"`
	Disposition             string                     `json:"disposition"`
	Reason                  string                     `json:"reason,omitempty"`
	OriginalResultRecovered bool                       `json:"original_result_recovered"`
	Image                   *tools.FileImage           `json:"current_image,omitempty"`
	Execution               *session.ExecutionIdentity `json:"-"`
	Assistant               session.ArchiveAddress     `json:"assistant"`
}

type orphanRecoveryOptions struct {
	Resolve func(orphanedToolCall) (recoveryObservation, error)
	OnError func(error)
	View    *session.WindowView
}

func unprovedRecoveryObservation(view *session.WindowView, call orphanedToolCall) recoveryObservation {
	ob := recoveryObservation{Disposition: tools.FileEffectUnknown, Reason: "the missing result has no checked recovery adapter"}
	if view != nil {
		_, lines := recoveryWindowHistory(*view, false)
		if call.assistantIndex >= 0 && call.assistantIndex < len(lines) {
			if slot, found := view.Slot(lines[call.assistantIndex]); found {
				ob.Assistant, ob.Execution = slot.Addr, slot.Source.Execution
			}
		}
	}
	return ob
}

func (ob recoveryObservation) message(callID string) (providers.Message, error) {
	ob.Type = "tool_recovery_observation"
	data, err := json.Marshal(ob)
	if err != nil {
		return providers.Message{}, err
	}
	return providers.Message{Role: "tool", ToolCallID: callID, Content: string(data)}, nil
}

func recoveryObservationError(callID string, err error) error {
	return fmt.Errorf("tool recovery: could not record observation for %q: %w", callID, err)
}
