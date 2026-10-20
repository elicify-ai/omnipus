package promptcache

// D20 bounds — normalize arithmetic (PART2 item8, RED authoring).
//
// Install target: pkg/providers/promptcache/ (package promptcache).
//
// Oracles:
//   - FR-011 / the parser's documented convention: "Unknown fields and
//     non-integer, negative or overflowing counters are ignored" — a
//     malformed recognized counter contributes 0 and never fails the call,
//     while every other valid counter keeps its value.
//   - ModelTokens.yaml / SessionStats.yaml: every token field carries
//     `minimum: 0`; the total is the additive identity
//     total = uncached + output + cache_read + cache_write.
//   - D20: the accounting line carries bounded numeric counters only.
//   - Parent-selected aggregate policy (from the supplied oracle's safe
//     option): an UNREPRESENTABLE additive write split (5m + 1h) or full
//     normalized sum is REJECTED via the existing error return with zero
//     returned usage — never saturated, never wrapped, and no spend/token
//     brake is invented (D13). An aggregate that fits exactly, including
//     exactly MaxInt, must succeed and conserve its components.
//
// Baseline expectations (derived before/without any run): the malformed and
// boundary cases pass against the current parser; the two rejection cases
// are RED against current production, which silently wraps the aggregate
// (S2: cache_write/total became negative in the dispatcher receipt). No
// error wording is asserted anywhere — no text contract was specified.
// All execution was Unknown/not-executed for this author; only gofmt
// parse/format was run on this artifact.

import (
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// expandedBoundsMaxInt is the platform int maximum, derived portably so no
// impossible literal can break a 32-bit build. Never add 1 to it as an int.
const expandedBoundsMaxInt = int(^uint(0) >> 1)

func TestExpandedBounds_Normalize_MalformedRecognizedCountersAreIgnored(t *testing.T) {
	// Every row carries one malformed RECOGNIZED counter plus healthy
	// companions: the malformed field must normalize to 0 and the companions
	// must survive exactly (documented convention). The negative rows are
	// also the M3a killers: removing usageCount's n<0 guard changes both the
	// field and the total asserted here.
	tests := []struct {
		name       string
		providerID string
		raw        string
		want       protocoltypes.UsageInfo
	}{
		{
			name:       "negative_uncached_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":-5,"output_tokens":10,"cache_read_input_tokens":700}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 0, CompletionTokens: 10, CacheReadTokens: 700, TotalTokens: 710},
		},
		{
			name:       "negative_nested_read_is_ignored",
			providerID: "openai",
			raw:        `{"input_tokens":1000,"output_tokens":10,"input_tokens_details":{"cached_tokens":-700,"cache_write_tokens":200}}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 800, CompletionTokens: 10, CacheWriteTokens: 200, TotalTokens: 1010},
		},
		{
			name:       "negative_five_minute_write_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":100,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":-40,"ephemeral_1h_input_tokens":60}}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 100, CacheWriteTokens: 60, CacheWrite5mTokens: 0, CacheWrite1hTokens: 60, TotalTokens: 160},
		},
		{
			name:       "negative_one_hour_write_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":-7}}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 30, CacheWriteTokens: 10, CacheWrite5mTokens: 10, CacheWrite1hTokens: 0, TotalTokens: 40},
		},
		{
			name:       "negative_output_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":50,"output_tokens":-3}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 50, TotalTokens: 50},
		},
		{
			name:       "fractional_uncached_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":12.5,"output_tokens":4}`,
			want:       protocoltypes.UsageInfo{CompletionTokens: 4, TotalTokens: 4},
		},
		{
			name:       "string_read_counter_is_ignored",
			providerID: "openai",
			raw:        `{"input_tokens":1000,"output_tokens":10,"input_tokens_details":{"cached_tokens":"700","cache_write_tokens":200}}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 800, CompletionTokens: 10, CacheWriteTokens: 200, TotalTokens: 1010},
		},
		{
			name:       "array_write_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":10,"cache_creation_input_tokens":[5]}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 10, TotalTokens: 10},
		},
		{
			name:       "object_output_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":7,"output_tokens":{"n":5}}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 7, TotalTokens: 7},
		},
		{
			name:       "null_read_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":20,"cache_read_input_tokens":null}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 20, TotalTokens: 20},
		},
		{
			name:       "boolean_uncached_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":true,"output_tokens":2}`,
			want:       protocoltypes.UsageInfo{CompletionTokens: 2, TotalTokens: 2},
		},
		{
			name:       "out_of_range_uncached_counter_is_ignored",
			providerID: "anthropic",
			raw:        `{"input_tokens":99999999999999999999,"output_tokens":9}`,
			want:       protocoltypes.UsageInfo{CompletionTokens: 9, TotalTokens: 9},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeUsage(tt.providerID, []byte(tt.raw))
			if err != nil {
				t.Fatalf("NormalizeUsage() error = %v (a malformed counter is ignored, never a call failure)", err)
			}
			if got != tt.want {
				t.Fatalf("normalized usage = %+v, want exactly %+v (malformed counter 0, companions preserved)", got, tt.want)
			}
		})
	}
}

func TestExpandedBounds_Normalize_CounterBoundariesConserve(t *testing.T) {
	// Healthy boundary values per representative field. Every row's total is
	// representable BY CONSTRUCTION (the components sum to at most MaxInt),
	// so none of these may trip the aggregate rejection.
	tests := []struct {
		name       string
		providerID string
		raw        string
		want       protocoltypes.UsageInfo
	}{
		{
			name:       "all_zero_counters_stay_zero",
			providerID: "anthropic",
			raw:        `{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`,
			want:       protocoltypes.UsageInfo{},
		},
		{
			name:       "one_of_each_counts_additively",
			providerID: "anthropic",
			raw:        `{"input_tokens":1,"output_tokens":1,"cache_read_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":1}}`,
			want:       protocoltypes.UsageInfo{PromptTokens: 1, CompletionTokens: 1, CacheReadTokens: 1, CacheWriteTokens: 2, CacheWrite5mTokens: 1, CacheWrite1hTokens: 1, TotalTokens: 5},
		},
		{
			name:       "max_uncached_alone_is_representable",
			providerID: "anthropic",
			raw:        fmt.Sprintf(`{"input_tokens":%d}`, expandedBoundsMaxInt),
			want:       protocoltypes.UsageInfo{PromptTokens: expandedBoundsMaxInt, TotalTokens: expandedBoundsMaxInt},
		},
		{
			name:       "max_minus_one_uncached_with_one_output_sums_exactly_to_max",
			providerID: "anthropic",
			raw:        fmt.Sprintf(`{"input_tokens":%d,"output_tokens":1}`, expandedBoundsMaxInt-1),
			want:       protocoltypes.UsageInfo{PromptTokens: expandedBoundsMaxInt - 1, CompletionTokens: 1, TotalTokens: expandedBoundsMaxInt},
		},
		{
			name:       "max_read_cancels_max_input_in_details_shape",
			providerID: "openai",
			raw:        fmt.Sprintf(`{"input_tokens":%d,"input_tokens_details":{"cached_tokens":%d}}`, expandedBoundsMaxInt, expandedBoundsMaxInt),
			want:       protocoltypes.UsageInfo{PromptTokens: 0, CacheReadTokens: expandedBoundsMaxInt, TotalTokens: expandedBoundsMaxInt},
		},
		{
			name:       "write_split_fitting_exactly_to_max_is_representable",
			providerID: "anthropic",
			raw:        fmt.Sprintf(`{"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":1}}`, expandedBoundsMaxInt-1),
			want:       protocoltypes.UsageInfo{CacheWriteTokens: expandedBoundsMaxInt, CacheWrite5mTokens: expandedBoundsMaxInt - 1, CacheWrite1hTokens: 1, TotalTokens: expandedBoundsMaxInt},
		},
		{
			name:       "max_output_alone_is_representable",
			providerID: "anthropic",
			raw:        fmt.Sprintf(`{"output_tokens":%d}`, expandedBoundsMaxInt),
			want:       protocoltypes.UsageInfo{CompletionTokens: expandedBoundsMaxInt, TotalTokens: expandedBoundsMaxInt},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeUsage(tt.providerID, []byte(tt.raw))
			if err != nil {
				t.Fatalf("NormalizeUsage() error = %v (a representable aggregate must succeed)", err)
			}
			if got != tt.want {
				t.Fatalf("normalized usage = %+v, want exactly %+v (additive identity at the boundary)", got, tt.want)
			}
		})
	}
}

// The two rejection cases are the S2 closure the parent selected: an
// unrepresentable aggregate must come back through the EXISTING error
// return with zero usage — not wrapped, not saturated, not clipped to a
// brake. No error wording is asserted (none was specified).

func TestExpandedBounds_Normalize_RejectsUnrepresentableWriteSplit(t *testing.T) {
	// Each counter is individually representable; 5m + 1h is not. This is
	// the required-rejection counterpart of the supplied oracle, which
	// tolerates the error path — this test requires it.
	raw := []byte(fmt.Sprintf(`{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":1}}`, expandedBoundsMaxInt))

	got, err := NormalizeUsage("anthropic", raw)
	if err == nil {
		t.Fatalf("NormalizeUsage() accepted an unrepresentable write split (5m=%d + 1h=1), returning %+v; it must be rejected via the error return", expandedBoundsMaxInt, got)
	}
	if got != (protocoltypes.UsageInfo{}) {
		t.Fatalf("rejected split returned usage %+v, want the zero value alongside the error", got)
	}

	ptr, parseErr := ParseUsage("anthropic", raw)
	if ptr != nil {
		t.Fatalf("ParseUsage() exposed a nonnil usage through a failed normalization: %+v", *ptr)
	}
	if parseErr == nil {
		t.Fatal("ParseUsage() returned nil error for an unrepresentable write split")
	}
}

func TestExpandedBounds_Normalize_RejectsOverflowingFullSum(t *testing.T) {
	// The write split fits (1 + 0) and every counter is representable, but
	// uncached + read + write + output = (Max-1) + 1 + 1 + (Max-1) is not.
	// Kills a reinstated unchecked four-way sum (the split-only guard alone
	// would keep passing this).
	raw := []byte(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":0}}`, expandedBoundsMaxInt-1, expandedBoundsMaxInt-1))

	got, err := NormalizeUsage("anthropic", raw)
	if err == nil {
		t.Fatalf("NormalizeUsage() accepted a full token sum that cannot be represented, returning %+v; it must be rejected via the error return", got)
	}
	if got != (protocoltypes.UsageInfo{}) {
		t.Fatalf("rejected sum returned usage %+v, want the zero value alongside the error", got)
	}

	ptr, parseErr := ParseUsage("anthropic", raw)
	if ptr != nil {
		t.Fatalf("ParseUsage() exposed a nonnil usage through a failed normalization: %+v", *ptr)
	}
	if parseErr == nil {
		t.Fatal("ParseUsage() returned nil error for an unrepresentable full sum")
	}
}

func TestExpandedBounds_Normalize_ExactMaxIntTotalSucceedsAndConserves(t *testing.T) {
	// The control the rejection must not eat: (Max-3) + 1 + 1 + 1 is exactly
	// representable. A rejection that fires at the exact boundary, or a
	// saturation clamp standing in for the rejection, fails here.
	raw := []byte(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":1,"cache_read_input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":0}}`, expandedBoundsMaxInt-3))

	ptr, err := ParseUsage("anthropic", raw)
	if err != nil {
		t.Fatalf("ParseUsage() error = %v (an exactly representable MaxInt total must succeed)", err)
	}
	if ptr == nil {
		t.Fatal("ParseUsage() returned nil usage for a representable aggregate")
	}
	want := protocoltypes.UsageInfo{
		PromptTokens:       expandedBoundsMaxInt - 3,
		CompletionTokens:   1,
		CacheReadTokens:    1,
		CacheWriteTokens:   1,
		CacheWrite5mTokens: 1,
		CacheWrite1hTokens: 0,
		TotalTokens:        expandedBoundsMaxInt,
	}
	if *ptr != want {
		t.Fatalf("conserved usage = %+v, want exactly %+v (components intact, total exactly MaxInt)", *ptr, want)
	}
}
