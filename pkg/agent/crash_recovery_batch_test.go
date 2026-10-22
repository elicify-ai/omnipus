//go:build goolm && stdjson

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// DESIGN Decision 5: the provider sees settled siblings, not whole-batch
// omission. Exercise the actual adapter bytes, group validator and reload.
// Q15 end-to-end write gating is separately tested by the file-cut pack.
func TestCrashRecovery_PartialBatchPreservesSuccessfulEffects(t *testing.T) {
	for _, reopen := range []bool{false, true} {
		name := "first_recovery"
		if reopen {
			name = "reopened_snapshot"
		}
		t.Run(name, func(t *testing.T) {
			h := cwR1New(t, 100000)
			work := filepath.Join(h.cfg.Agents.Defaults.Home, "work")
			require.NoError(t, os.MkdirAll(work, 0700))
			path := filepath.Join(work, "effects.txt")
			result := tools.NewAppendFileTool(work, true).Execute(context.Background(), map[string]any{"path": path, "content": "A\n"})
			require.False(t, result.IsError, "A is a real successful append before its result commits: %s", result.ForLLM)
			a := crashCall("settled-a", "append_file", map[string]any{"path": path, "content": "A\n"})
			b := crashCall("unresolved-b", "append_file", map[string]any{"path": path, "content": "B\n"})
			raw := []providers.Message{{Role: "user", Content: "Append A and B once."}, {Role: "assistant", ToolCalls: []providers.ToolCall{a, b}},
				{Role: "tool", ToolCallID: a.ID, Content: result.ContentForLLM()},
				{Role: "system", Content: `{"type":"steering_control","sequence":19,"content":"preserve independent control"}`}}
			h.append(t, raw...)
			before := orphanACSnapshot(t, h)
			cleaned := RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil)
			if reopen {
				orphanACReopen(t, h)
			}
			ts := h.turn("")
			assembled := orphanACAssemble(t, h, ts, cleaned)
			require.NoError(t, validateWindowGroups(assembled), "recovery supplies provider-valid tool groups")
			recorder, provider := cwR1OpenAI(t, 0)
			cwR1Send(t, cwR1Flow(h, ts, assembled, provider))
			requests := recorder.requests(t)
			require.Len(t, requests, 1, "exact first resumed provider request")
			messages := cwR1Messages(t, requests[0])
			var foundA []providers.Message
			var declarations int
			var observations []providers.Message
			for _, m := range messages {
				for _, c := range m.ToolCalls {
					if c.ID == a.ID {
						declarations++
					}
				}
				if m.Role == "tool" && m.ToolCallID == a.ID {
					foundA = append(foundA, m)
				}
				if m.Role == "tool" && m.ToolCallID == b.ID {
					observations = append(observations, m)
				}
			}
			assert.Equal(t, 1, declarations, "D: first resumed model input must retain the original successful A declaration")
			assert.Equal(t, []providers.Message{raw[2]}, foundA, "D: first resumed model input must retain A's exact durable success, not conceal it with B")
			assert.Len(t, observations, 1, "only the missing occurrence receives an engine recovery observation")
			if len(observations) == 1 {
				assert.NotContains(t, observations[0].Content, "denied", "an absent result is not approval evidence")
				assert.NotEqual(t, result.ContentForLLM(), observations[0].Content, "B's missing return must not be fabricated from sibling A")
			}
			// The provider envelope may fold system controls into the pinned system
			// message. Require the complete control value exactly once, not a
			// particular transport partition or a substring of its identifier.
			var controls int
			for _, m := range messages {
				if m.Role != "system" {
					continue
				}
				for _, line := range strings.Split(m.Content, "\n") {
					if line == raw[3].Content {
						controls++
					}
				}
			}
			assert.Equal(t, 1, controls, "independent control survives exactly once in the actual provider input")
			after := orphanACSnapshot(t, h)
			require.Equal(t, viewArchived(before), viewArchived(after)[:len(raw)], "original addressed archive prefix remains unchanged")
			archiveBytes := orphanACArchiveBytes(t, h)
			RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil)
			require.Equal(t, archiveBytes, orphanACArchiveBytes(t, h), "repeated recovery appends no duplicate observation")
			bytes, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, "A\n", string(bytes), "projection never re-executes A or guesses B")
		})
	}
}

func TestCrashRecovery_MissingResultIsNotApprovalDeny(t *testing.T) {
	h := cwR1New(t, 100000)
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: filepath.Join(h.cfg.Agents.Defaults.Home, "audit"), RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logger.Close()) })
	// Positive instrument control: this denial has real approval evidence, unlike
	// the missing-result occurrence. The audit reader must find it unchanged.
	require.NoError(t, logger.Log(&audit.Entry{Event: audit.EventToolPolicyAskDenied, Decision: audit.DecisionDeny, Tool: "append_file",
		Details: map[string]any{"tool_call_id": "actually-denied", "reason": "user_denied"}}))
	h.append(t, providers.Message{Role: "user", Content: "execute the pending operation"}, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
		crashCall("unknown-call", "append_file", map[string]any{"path": "out.txt", "content": "A\n"}),
	}})
	RecoverOrphanedToolCalls(h.agent.Sessions, h.key, logger)
	entries, err := readAuditEntries(filepath.Join(h.cfg.Agents.Defaults.Home, "audit", "audit.jsonl"))
	require.NoError(t, err)
	var realDenials, unknown []map[string]any
	for _, entry := range entries {
		details, ok := entry["details"].(map[string]any)
		if !ok {
			continue
		}
		switch details["tool_call_id"] {
		case "actually-denied":
			realDenials = append(realDenials, entry)
		case "unknown-call":
			unknown = append(unknown, entry)
		}
	}
	require.Len(t, realDenials, 1, "audit reader must see the known-denial positive control")
	assert.Equal(t, "deny", realDenials[0]["decision"], "actual approval denial stays Deny")
	require.Len(t, unknown, 1, "one attributable recovery event, not a silently omitted audit")
	assert.Equal(t, "tool.recovery.interrupted", unknown[0]["event"], "D: absence of a tool result is recovery uncertainty, not policy Ask denied")
	assert.Equal(t, "error", unknown[0]["decision"], "D: missing result without approval evidence must never be labelled Deny")
}

// Occurrence identity and intentional repetition are negative controls for D.
func TestCrashRecovery_LaterReusedIDAndIdenticalAppendsRemainDistinct(t *testing.T) {
	h := cwR1New(t, 100000)
	work := filepath.Join(h.cfg.Agents.Defaults.Home, "work")
	require.NoError(t, os.MkdirAll(work, 0700))
	path := filepath.Join(work, "repeat.txt")
	call := crashCall("reused", "append_file", map[string]any{"path": path, "content": "same\n"})
	h.append(t, providers.Message{Role: "user", Content: "old occurrence"}, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}}, orphanACMarker(call.ID),
		providers.Message{Role: "user", Content: "append the same text twice intentionally"})
	for range 2 {
		result := tools.NewAppendFileTool(work, true).Execute(context.Background(), call.Arguments)
		require.False(t, result.IsError, "new explicit occurrence is not globally deduplicated")
		h.append(t, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}}, providers.Message{Role: "tool", ToolCallID: call.ID, Content: result.ContentForLLM()})
	}
	orphanACReopen(t, h)
	messages := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	var results int
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID == call.ID {
			results++
		}
	}
	assert.Equal(t, 2, results, "later assistant occurrences using one provider ID both retain their results")
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "same\nsame\n", string(bytes), "two intentional identical appends are two effects")
	require.NoError(t, validateWindowGroups(messages))
}
