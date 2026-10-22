package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func recoveryHistory(store session.SessionStore, key string, view *session.WindowView) ([]providers.Message, error) {
	if view != nil {
		history, _ := recoveryWindowHistory(*view, false)
		return history, nil
	}
	if checked, ok := store.(session.ContextWindowStore); ok {
		snap, err := checked.WindowView(context.Background(), key)
		if err != nil {
			return nil, err
		}
		history, _ := recoveryWindowHistory(snap, false)
		return history, nil
	}
	return store.GetHistory(key), nil
}

// bindRecoveryToolResults attaches durably addressed results to their exact
// declaration in the provider view. A late recovery observation may be archived
// after unrelated inputs; those inputs keep their order, and no archive is rewritten.
func bindRecoveryToolResults(snap session.WindowView, history []providers.Message, lines []int) ([]providers.Message, []int) {
	issuers := map[string]int{}
	for i, msg := range history {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		if slot, ok := snap.Slot(lines[i]); ok {
			issuers[slot.Addr.EntryID] = i
		}
	}
	results := map[int][]int{}
	moved := map[int]bool{}
	for i, msg := range history {
		if msg.Role != "tool" {
			continue
		}
		slot, ok := snap.Slot(lines[i])
		if !ok || slot.ToolResultFor == nil {
			continue
		}
		issuer, found := issuers[slot.ToolResultFor.AssistantEntryID]
		if !found || issuer >= i {
			continue
		}
		results[issuer] = append(results[issuer], i)
		moved[i] = true
	}
	out := make([]providers.Message, 0, len(history))
	ordinals := make([]int, 0, len(lines))
	for i, msg := range history {
		if moved[i] {
			continue
		}
		out, ordinals = append(out, msg), append(ordinals, lines[i])
		for _, result := range results[i] {
			out, ordinals = append(out, history[result]), append(ordinals, lines[result])
		}
	}
	return out, ordinals
}
