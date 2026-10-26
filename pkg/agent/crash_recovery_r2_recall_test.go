//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// A3 / DESIGN 3 effects/every later entry and DESIGN 5 exact addresses:
// selective retirement must not renumber the address a real recall mark cites.
func TestCrashRecovery_R2RetiredArchiveRecallKeepsOriginalAddress(t *testing.T) {
	for _, retire := range []bool{false, true} {
		name := "unfiltered_control"
		if retire {
			name = "retired_input_before_retained_result"
		}
		t.Run(name, func(t *testing.T) {
			const nonce = "R2_RECALL_RETAINED_LONG_RESULT"
			f := crashConsumedRoomContext(t, false, func(h *crashHarness, p *crashScript) {
				// A deliberate small configured cap, not a hardcoded assumption
				// about the installed default. Source is three caps long.
				h.al.GetConfig().Context.BuiltinSuccessCap = 1024
				work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
				require.NoError(t, err)
				path := filepath.Join(work, "long-retained-result.txt")
				require.NoError(t, os.WriteFile(path, []byte(nonce+strings.Repeat(" ordinary evidence word ", 160)), 0600))
				ordinary := p.next
				p.next = func(i int, input []providers.Message) (*providers.LLMResponse, error) {
					result, err := ordinary(i, input)
					if i == 0 && err == nil {
						result.ToolCalls = append(result.ToolCalls, crashCall("long-room-result", "read_file", map[string]any{"path": path}))
					}
					return result, err
				}
			})
			var originalLine = -1
			originalTurn := 0
			for _, slot := range f.view.Live {
				if slot.Message.Role == "user" {
					originalTurn++
				}
				if slot.Message.Role == "tool" && slot.Message.ToolCallID == "long-room-result" {
					originalLine = slot.Ordinal
					break
				}
			}
			require.GreaterOrEqual(t, originalLine, 0, "instrument: actual native read result has a stable original ordinal")
			store := f.h.al.GetSessionStore()
			var original providers.Message
			require.NoError(t, store.ScanArchiveRange(context.Background(), f.id, originalLine, originalLine,
				func(_ int, raw []byte, _ memory.ArchivedMessage) error { return json.Unmarshal(raw, &original) }))
			require.Contains(t, original.Content, nonce, "instrument: saved source contains the known real file bytes")
			require.Greater(t, len(original.Content), 1024, "instrument: whole-turn recall must cap this actual result")
			if retire {
				require.NoError(t, f.h.al.DiscardRequest(f.id, f.request))
			}
			tool, found := f.h.al.GetRegistry().primaryChatAgentForTest().Tools.GetIncludingHidden("recall_conversation")
			require.True(t, found, "use the actually registered recall tool and actual archive")
			ctx := tools.WithSessionKey(context.Background(), f.id)
			positive := tool.Execute(ctx, map[string]any{"tool_call_id": "long-room-result", "archive_line": originalLine, "length": 128})
			require.False(t, positive.IsError, "instrument: exact original address is readable: %s", positive.ContentForLLM())
			assert.Contains(t, positive.ContentForLLM(), original.Content[:128])
			result := tool.Execute(ctx, map[string]any{"query": nonce, "max_results": 1})
			require.False(t, result.IsError, result.ContentForLLM())
			span := f.h.al.activeRecallSpan(f.id)
			require.NotNil(t, span, "whole-turn recall installs a real provider-valid span")
			var marks []map[string]any
			for _, message := range span.Messages() {
				if message.Role != "tool" {
					continue
				}
				start := strings.Index(message.Content, "{")
				if start < 0 {
					continue
				}
				var mark map[string]any
				if json.NewDecoder(strings.NewReader(message.Content[start:])).Decode(&mark) == nil && mark["error"] == "tool_result_recall_mark" && mark["tool_call_id"] == "long-room-result" {
					marks = append(marks, mark)
				}
			}
			require.Len(t, marks, 1, "positive instrument: actual capped result supplies exactly one address-bearing mark")
			line, ok := marks[0]["archive_line"].(float64)
			require.True(t, ok)
			t.Logf("RECALL_ADDRESS retired=%t original=%d emitted=%v original_turn=%d emitted_turn=%v", retire, originalLine, line, originalTurn, marks[0]["turn"])
			assert.EqualValues(t, originalLine, line, "filtering never renumbers an original archive address")
			assert.EqualValues(t, originalTurn, marks[0]["turn"], "retained historical effect keeps its original turn identity")
			page := tool.Execute(ctx, map[string]any{"tool_call_id": "long-room-result", "archive_line": line, "length": 128})
			assert.False(t, page.IsError, "following the ACTUAL emitted mark must reach its source: %s", page.ContentForLLM())
			assert.Contains(t, page.ContentForLLM(), original.Content[:128], "mark must page this occurrence, not another archive slot")
		})
	}
}
