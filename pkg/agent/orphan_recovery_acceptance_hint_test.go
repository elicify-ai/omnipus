//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Architect Option A's nine literal address controls exercise the real mark
// assertion helper, not a second copy of its regular expression. All other
// fields are the independent M2 fixture's contract values.
func TestOrphanRecoveryRecallHint_ExactArchiveLineToken(t *testing.T) {
	cases := []struct {
		name    string
		address string
		accept  bool
	}{
		{"accept_parenthetical_prose", "archive_line=7 (offset, length)", true},
		{"accept_following_prose", "archive_line=7 to read…", true},
		{"accept_end_of_string", "archive_line=7", true},
		{"accept_json_punctuation", `"archive_line":7}`, true},
		{"accept_immediate_parenthesis", "archive_line=7(", true},
		{"reject_archive_line_70", "archive_line=70", false},
		{"reject_archive_line_700", "archive_line=700", false},
		{"reject_archive_line_7x", "archive_line=7x", false},
		{"reject_archive_line_7_point_5", "archive_line=7.5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, err := json.Marshal(map[string]any{
				"error": "tool_result_recall_mark", "tool": "r1_tool", "tool_call_id": "shared",
				"archive_line": 7, "size_chars": 1200, "turn": 3, "content_state": "emptied",
				"hint": `Call recall_conversation with tool_call_id="shared" and ` + tc.address,
			})
			require.NoError(t, err, "encode independently specified mark fixture")
			sink := orphanACObserveMark(providers.Message{Role: "tool", ToolCallID: "shared", Content: string(content)})
			if tc.accept {
				require.Empty(t, sink.errors, "architect-ruled exact integer token must be accepted: %s", tc.address)
				require.False(t, sink.failed, "valid hint must complete every unchanged mark assertion")
				return
			}
			require.Len(t, sink.errors, 1, "invalid address must fail exactly one assertion: %s", tc.address)
			require.True(t, sink.failed, "invalid address must trigger the helper's fatal assertion")
			require.Contains(t, sink.errors[0], "hint uses the exact original address, accepting whitespace but not another integer with the same prefix",
				"rejection must come from the actual hint-address assertion, not JSON, ID or tool-name validation")
		})
	}
}

// This recording assertion sink observes require's Errorf/FailNow contract in
// process. A typed sentinel preserves fatal short-circuiting; unrelated panics
// propagate, and the caller asserts every recorded failure explicitly.
type orphanACHintSink struct {
	errors []string
	failed bool
}

type orphanACHintAbort struct{}

var _ orphanACMarkTestingT = (*orphanACHintSink)(nil)

func (s *orphanACHintSink) Helper() {}

func (s *orphanACHintSink) Errorf(format string, args ...any) {
	s.errors = append(s.errors, fmt.Sprintf(format, args...))
}

func (s *orphanACHintSink) FailNow() {
	s.failed = true
	panic(orphanACHintAbort{})
}

func orphanACObserveMark(m providers.Message) (sink *orphanACHintSink) {
	sink = &orphanACHintSink{}
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, expected := recovered.(orphanACHintAbort); !expected {
				panic(recovered)
			}
		}
	}()
	orphanACAssertMark(sink, m, "shared", 7, 1200, 3)
	return sink
}
