package agent

import (
	"fmt"
	"sort"
	"strings"
)

// not-wire-format: public call identifiers for a private, durable recovery hold.
type recoveryHeldOperation struct {
	Tool   string `json:"tool"`
	CallID string `json:"tool_call_id"`
}

func recoveryInterruptionNotice(id string) string {
	const prefix = "reconciliation_required: Automatic continuation stopped because saved effects or request context could not be verified."
	held, err := loadRecoveryHold(id)
	if err != nil || len(held.Occurrences) == 0 {
		return prefix + " Repair the reported storage or context problem before explicitly continuing this chat."
	}
	keys := make([]string, 0, len(held.Occurrences))
	for key := range held.Occurrences {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var text strings.Builder
	text.WriteString(prefix)
	for i, key := range keys {
		if i == 8 {
			fmt.Fprintf(&text, "\n%d more unresolved operations remain recorded.", len(keys)-i)
			break
		}
		op, known := held.Operations[key]
		if known {
			fmt.Fprintf(&text, "\nTool call %q (%s): %s.", op.CallID, op.Tool, held.Occurrences[key])
		} else {
			fmt.Fprintf(&text, "\nAn unresolved saved operation: %s.", held.Occurrences[key])
		}
	}
	text.WriteString("\nThe original results were not recovered. Review this chat; send a new explicit continuation only if you accept that uncertainty. Existing permissions still apply.")
	return text.String()
}
