package promptcache

// D20 bounds — formatter lower bounds (PART2 item8, RED authoring).
//
// Install target: pkg/providers/promptcache/ (package promptcache).
//
// Oracles:
//   - D20 / FR-025: the per-call accounting line carries BOUNDED numeric
//     counters only. FormatCallAccounting's max(0, …) clamps are the
//     load-bearing enforcement when a caller hands the formatter typed
//     counters that bypassed the parser (the agent loop builds UsageInfo
//     values directly on some paths), so these tests feed NEGATIVE typed
//     counters straight in — the parser bypass is deliberate. Each expected
//     line is the exact full string: any unclamped negative changes it.
//   - absence semantics: unpriced_tokens is listed only when strictly
//     positive; the write split is listed only when either lifetime is
//     positive — and a negative lifetime beside a positive one must appear
//     clamped, not dropped and not negative.
//
// These rows are the M3b killers: removing any of the formatter's lower-
// bound clamps (the five main-line counters or the two write lifetimes)
// changes exactly one row. Baseline expectation: all rows pass against the
// current clamped formatter; they go red only under the mutants. Execution
// was Unknown/not-executed for this author; only gofmt parse/format ran on
// this artifact.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// expandedBoundsCall fixed correlation ids shared by every row: identities
// are not under test, the numeric fields are.
func expandedBoundsCall() CallAccounting {
	return CallAccounting{
		ProviderID: "expanded-bounds",
		ModelID:    "expanded-model",
		AgentID:    "expanded-agent",
		SessionID:  "expanded-session",
		CallID:     "expanded-call",
	}
}

const expandedBoundsLinePrefix = `llm_usage provider="expanded-bounds" model="expanded-model" ` +
	`agent="expanded-agent" session="expanded-session" call="expanded-call" `

func TestExpandedBounds_Format_NegativeCountersClampToZero(t *testing.T) {
	tests := []struct {
		name  string
		usage protocoltypes.UsageInfo
		want  string
	}{
		{
			name:  "negative_uncached_clamps_to_zero",
			usage: protocoltypes.UsageInfo{PromptTokens: -100},
			want:  expandedBoundsLinePrefix + `uncached=0 cache_read=0 cache_write=0 output=0 total=0 billing=unpriced`,
		},
		{
			name:  "negative_cache_read_clamps_to_zero",
			usage: protocoltypes.UsageInfo{CacheReadTokens: -800},
			want:  expandedBoundsLinePrefix + `uncached=0 cache_read=0 cache_write=0 output=0 total=0 billing=unpriced`,
		},
		{
			name:  "negative_cache_write_clamps_to_zero",
			usage: protocoltypes.UsageInfo{CacheWriteTokens: -50},
			want:  expandedBoundsLinePrefix + `uncached=0 cache_read=0 cache_write=0 output=0 total=0 billing=unpriced`,
		},
		{
			name:  "negative_output_clamps_to_zero",
			usage: protocoltypes.UsageInfo{CompletionTokens: -10},
			want:  expandedBoundsLinePrefix + `uncached=0 cache_read=0 cache_write=0 output=0 total=0 billing=unpriced`,
		},
		{
			name:  "negative_total_clamps_to_zero",
			usage: protocoltypes.UsageInfo{TotalTokens: -960},
			want:  expandedBoundsLinePrefix + `uncached=0 cache_read=0 cache_write=0 output=0 total=0 billing=unpriced`,
		},
		{
			name:  "negative_five_minute_write_clamps_while_section_stays_listed",
			usage: protocoltypes.UsageInfo{CacheWrite5mTokens: -40, CacheWrite1hTokens: 60},
			want:  expandedBoundsLinePrefix + `uncached=0 cache_read=0 cache_write=0 output=0 total=0 write_5m=0 write_1h=60 billing=unpriced`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := expandedBoundsCall()
			in.Usage = tt.usage
			got := FormatCallAccounting(in)
			if got != tt.want {
				t.Fatalf("formatted line:\n  got  %q\n  want %q\n(a negative counter must be clamped to 0, never printed)", got, tt.want)
			}
			if strings.Contains(got, "=-") {
				t.Errorf("line contains a negative numeric field: %q (D20 counters are bounded)", got)
			}
		})
	}
}

func TestExpandedBounds_Format_UnpricedTokensAbsentUnlessPositive(t *testing.T) {
	usage := protocoltypes.UsageInfo{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}
	want := expandedBoundsLinePrefix + `uncached=5 cache_read=0 cache_write=0 output=2 total=7 billing=unpriced`

	t.Run("negative_unpriced_tokens_is_absent_not_printed", func(t *testing.T) {
		in := expandedBoundsCall()
		in.Usage = usage
		in.UnpricedTokens = -3
		got := FormatCallAccounting(in)
		if got != want {
			t.Fatalf("formatted line:\n  got  %q\n  want %q\n(a non-positive unpriced count must be absent)", got, want)
		}
		if strings.Contains(got, "unpriced_tokens=") {
			t.Errorf("unpriced_tokens must be absent when not positive, found it in: %q", got)
		}
	})
	t.Run("zero_unpriced_tokens_is_absent", func(t *testing.T) {
		in := expandedBoundsCall()
		in.Usage = usage
		in.UnpricedTokens = 0
		got := FormatCallAccounting(in)
		if got != want {
			t.Fatalf("formatted line:\n  got  %q\n  want %q", got, want)
		}
		if strings.Contains(got, "unpriced_tokens=") {
			t.Errorf("unpriced_tokens must be absent at zero, found it in: %q", got)
		}
	})
}

func TestExpandedBounds_Format_HealthyNonZeroControl(t *testing.T) {
	// The positive control: with healthy counters every field prints its
	// exact value — the clamps must not eat legitimate numbers, and the
	// positive unpriced count must be listed.
	in := expandedBoundsCall()
	in.Usage = protocoltypes.UsageInfo{
		PromptTokens:       100,
		CompletionTokens:   10,
		CacheReadTokens:    800,
		CacheWriteTokens:   100,
		CacheWrite5mTokens: 40,
		CacheWrite1hTokens: 60,
		TotalTokens:        1010,
	}
	in.UnpricedTokens = 7

	want := expandedBoundsLinePrefix +
		`uncached=100 cache_read=800 cache_write=100 output=10 total=1010 ` +
		`write_5m=40 write_1h=60 billing=unpriced unpriced_tokens=7`

	got := FormatCallAccounting(in)
	if got != want {
		t.Fatalf("formatted line:\n  got  %q\n  want %q\n(healthy counters must reach the line exactly)", got, want)
	}
}
