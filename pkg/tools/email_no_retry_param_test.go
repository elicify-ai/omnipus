package tools

// RED round 2 — A8 structural safety proof, tool side. The founder's pinned
// property: the agent-tool path can NEVER set or trigger the retry bypass —
// "no retry parameter anywhere in the tool call surface", structurally true,
// not just conventionally true. This guard dies if any email tool ever grows
// a retry-shaped parameter or advertises a retry bypass in its description.
//
// Green-on-arrival guard: the invariant already holds today; its failability
// activates on violation — a check-no-* guard in test form.
//
// The behavioral half (a tool-path call during backoff refused exactly like
// an automatic REST poll) is pinned at the budget unit:
// TestMailBudget_BackoffRefusalSymmetric's "tool path refused identically"
// subtest (pkg/email/mail_budget_red_test.go).

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmailTools_NoRetryParameter(t *testing.T) {
	tps := EmailTransports{}
	all := []Tool{
		NewReadInboxTool(tps),
		NewSearchEmailTool(tps),
		NewReadMessageTool(tps),
		NewSendEmailTool(tps),
		NewReplyTool(tps),
	}
	for _, tool := range all {
		name := tool.Name()
		t.Run(name, func(t *testing.T) {
			params := tool.Parameters()
			for k, v := range params {
				p, _ := v.(map[string]any)
				props, _ := p["properties"].(map[string]any)
				for propName := range props {
					require.NotContains(t, strings.ToLower(propName), "retry",
						"%s: the tool call surface must never expose a retry-shaped parameter", name)
				}
				require.NotContains(t, strings.ToLower(k), "retry",
					"%s: no retry-shaped schema key", name)
			}
			require.NotContains(t, strings.ToLower(tool.Description()), "retry",
				"%s: the description must not advertise a retry bypass", name)
		})
	}
}
