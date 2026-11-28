package bedrock

// pdf_wire.go — the optional transport capabilities of issue #1968 (spec
// FR-11, FR-12) for the Converse API.

import (
	"encoding/json"
	"fmt"
)

// SerializedRequestBytes returns the exact byte length of the Converse body
// Chat would send for this request. Converse has no streaming variant
// here, so stream does not change the body.
func (p *Provider) SerializedRequestBytes(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	_ bool,
) (int, error) {
	payload, err := json.Marshal(buildConverseRequestFor(messages, tools, p.baseModel(model), options))
	if err != nil {
		return 0, fmt.Errorf("bedrock converse request: %w", err)
	}
	return len(payload), nil
}

// WireMessageGroups reports which internal messages share one Converse
// message: convertMessages merges consecutive tool results into one user
// message; every other message is its own wire message.
func (p *Provider) WireMessageGroups(messages []Message) [][]int {
	var groups [][]int
	for i := 0; i < len(messages); i++ {
		if isToolResult(messages[i]) && i > 0 && isToolResult(messages[i-1]) && len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], i)
			continue
		}
		groups = append(groups, []int{i})
	}
	return groups
}
