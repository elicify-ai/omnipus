package session

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// historyBudgetCases lists incomplete frame bodies with the fewest bytes any
// JSON text needs to finish them (shortest key "", shortest value 0). Worked
// out by hand from the JSON grammar, not from the implementation.
var historyBudgetCases = []struct {
	name, prefix string
	need         int
}{
	{"open_object", `{`, 1},                        // }
	{"key_string_open", `{"`, 4},                   // ":0}
	{"missing_colon", `{"x"`, 3},                   // :0}
	{"missing_value", `{"x":`, 2},                  // 0}
	{"exponent_digit", `{"x":1e`, 2},               // 0}
	{"fraction_digit", `{"x":1.`, 2},               // 0}
	{"minus_digit", `{"x":-`, 2},                   // 0}
	{"literal_suffix", `{"x":tru`, 2},              // e}
	{"null_suffix_nested", `{"a":{"b":nul`, 3},     // l}}
	{"after_comma_key", `{"a":1,`, 5},              // "":0}
	{"array_after_comma", `{"a":[1,`, 3},           // 0]}
	{"array_open", `{"a":[`, 2},                    // ]}
	{"value_string_closed", `{"a":"x"`, 1},         // }
	{"pending_backslash", `{"x":"\`, 3},            // n"}
	{"unicode_no_digits", `{"x":"\u`, 6},           // 0000"}
	{"unicode_two_digits", `{"x":"\u12`, 4},        // 00"}
	{"escaped_quote_brace_key", `{"k\"{[":`, 2},    // 0}
	{"escaped_backslash_then_quote", `{"k\\":`, 2}, // 0}
	{"escaped_quote_in_value", `{"a":"q\"}]`, 2},   // "}
	{"pending_backslash_in_key", `{"k\`, 5},        // n":0}
	{"unicode_escape_then_brace", `{"a":"{{`, 2},   // "}
}

// #1523 round 3: the remaining-size check uses the shortest JSON completion.
// At exactly that budget an incomplete body is accepted as a possible cut;
// one byte less is refused as framed damage.
func TestChatHistoryTornBody_ShortestCompletionBudget(t *testing.T) {
	for _, tc := range historyBudgetCases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.prefix)
			size := int64(len(body) + tc.need)
			require.NoError(t, historyTornBody(body, size), "a body that can still finish in exactly %d more bytes is a possible cut", tc.need)
			require.Error(t, historyTornBody(body, size-1), "a body needing %d more bytes cannot finish in %d", tc.need, tc.need-1)
		})
	}
}

// End to end through reconcile: the six budgets the round-2 re-review found
// accepted, plus pending-escape budgets, are refused as index_inconsistent with
// the damaged bytes kept; the same prefixes at their exact budget are redone.
func TestChatHistoryRecovery_ImpossibleBudgetPrefixesRefused(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		remaining    int
		refused      bool
	}{
		{"key_string_no_value_budget", `{"`, 2, true},
		{"missing_value_budget", `{"x":`, 1, true},
		{"missing_colon_budget", `{"x"`, 1, true},
		{"unicode_digits_budget", `{"x":"\u12`, 2, true},
		{"exponent_digit_budget", `{"x":1e`, 1, true},
		{"literal_suffix_budget", `{"x":tru`, 1, true},
		{"pending_backslash_budget", `{"x":"\`, 2, true},
		{"key_string_exact_budget_control", `{"`, 4, false},
		{"pending_backslash_exact_budget_control", `{"x":"\`, 3, false},
		{"escaped_quote_brace_exact_budget_control", `{"k\"{[":`, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sid, full, prior, body := historyUnpublishedTailFixture(t)
			s.stopStatsFlusher()
			frames := historyWalkFrameStarts(t, body, prior)
			commit := frames[len(frames)-1]
			body = body[:commit+40+len(tc.prefix)]
			binary.BigEndian.PutUint32(body[commit+4:commit+8], uint32(len(tc.prefix)+tc.remaining))
			copy(body[commit+40:], tc.prefix)
			require.NoError(t, os.WriteFile(full, body, 0o600))
			canonical := partitionBytes(t, s, sid)
			_, err := s.reconcileChatHistoryIndex(context.Background(), sid)
			if tc.refused {
				require.ErrorIs(t, err, ErrChatHistoryUnavailable, "impossible budget is framed damage, not a cut-off write")
				var unavailable *ChatHistoryUnavailableError
				require.ErrorAs(t, err, &unavailable)
				require.Equal(t, "index_inconsistent", unavailable.Reason)
				after, readErr := os.ReadFile(full)
				require.NoError(t, readErr)
				require.Equal(t, body, after, "damaged derived bytes stay on disk for diagnosis")
			} else {
				require.NoError(t, err, "a body that can still finish within its length is redone")
				require.Equal(t, []string{"base-000001", "durable-damage-fixture"}, historyIDs(historyRead(t, s, sid, historyOptions(40))))
			}
			require.Equal(t, canonical, partitionBytes(t, s, sid), "canonical bytes never trimmed by derived recovery")
		})
	}
}

// Every cut of real historyEncodeFrame output with escaped quotes, backslashes
// and braces, \u escapes, multibyte UTF-8, nested containers, literals and
// exponent numbers is accepted as a possible cut (adapted from the round-2
// re-review probe). Writer identifiers rarely contain these characters, so
// the real-transaction property alone does not exercise them.
func TestChatHistoryRecovery_RichCodecPrefixesAreAccepted(t *testing.T) {
	key := "quote\" slash\\ braces{}[] control\x00 line\n snowman☃"
	for _, tc := range []struct {
		name  string
		frame historyFrame
	}{
		{"node_rich", historyFrame{Node: &historyNode{Key: key, Priority: 18446744073709551615,
			Value: json.RawMessage(`{"str":"quote:\" slash:\\ braces:{}[]","unicode":"\u0000 é","n":-12.34e+56,"a":[true,false,null,{},[1,2]]}`)}}},
		{"node_negative_exponent", historyFrame{Node: &historyNode{Key: key, Value: json.RawMessage(`-1.25e-9`)}}},
		{"commit_escaped", historyFrame{Commit: &historyCheckpoint{SessionID: key, Generation: key, File: key,
			Root: 1, MutationEnd: 12345, ArchiveEnds: map[string]int64{key: 345}, CurrentKey: key}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := historyEncodeFrame(tc.frame)
			require.NoError(t, err)
			f, err := os.OpenFile(filepath.Join(t.TempDir(), "frame.chi"), os.O_CREATE|os.O_RDWR, 0o600)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, f.Close()) })
			tree := &chatHistoryIndexTree{ctx: context.Background(), file: f}
			for cut := 1; cut < len(b); cut++ {
				require.NoError(t, f.Truncate(0))
				_, err := f.WriteAt(b[:cut], 0)
				require.NoError(t, err)
				_, _, err = tree.readFrame(0, int64(cut))
				require.ErrorIs(t, err, io.ErrUnexpectedEOF, "instrument: cut=%d is incomplete", cut)
				require.NoError(t, tree.proveTornFrame(0, int64(cut)), "genuine codec prefix refused: case=%s cut=%d", tc.name, cut)
			}
			t.Logf("rich codec cuts checked=%d case=%s", len(b)-1, tc.name)
		})
	}
}
