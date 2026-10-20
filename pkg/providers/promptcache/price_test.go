package promptcache

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func usd(v float64) *float64 { return &v }

func near(got, want float64) bool {
	return math.Abs(got-want) <= 1e-12
}

// TestPrice_CatalogArithmetic is TDD row 4. The four terms are the spec's
// priced-call scenario: $3, $0.30, $3.75 and $15 per million tokens.
func TestPrice_CatalogArithmetic(t *testing.T) {
	in := PriceInput{
		ProviderID: "anthropic",
		Cost: &catalog.ModelCost{
			Input:      usd(3),
			CacheRead:  usd(0.30),
			CacheWrite: usd(3.75),
			Output:     usd(15),
		},
		Usage: protocoltypes.UsageInfo{
			PromptTokens:     1000,
			CacheReadTokens:  8000,
			CacheWriteTokens: 2000,
			CompletionTokens: 500,
		},
	}
	got := Price(in)
	const (
		inputPart  = 0.003
		readPart   = 0.0024
		writePart  = 0.0075
		outputPart = 0.0075
	)
	want := inputPart + readPart + writePart + outputPart
	if !near(got.Cost, want) {
		t.Fatalf("cost = %v, want %v + %v + %v + %v = %v", got.Cost, inputPart, readPart, writePart, outputPart, want)
	}
	if got.BillingClass != BillingClassPriced {
		t.Errorf("billing class = %q, want %q", got.BillingClass, BillingClassPriced)
	}
	if got.UnpricedTokens != 0 {
		t.Errorf("unpriced tokens = %d, want 0 for a fully priced call", got.UnpricedTokens)
	}
}

// TestPrice_MissingInputIsUnpriced is TDD row 5 and FR-015. Missing input
// pricing records exactly zero dollars, explicitly unpriced, while retaining
// all 2,200 tokens: 1,000 prompt + 200 output + 1,000 cache read.
func TestPrice_MissingInputIsUnpriced(t *testing.T) {
	got := Price(PriceInput{
		ProviderID: "openai",
		Usage: protocoltypes.UsageInfo{
			PromptTokens:     1000,
			CompletionTokens: 200,
			CacheReadTokens:  1000,
		},
	})
	if got.Cost != 0 {
		t.Fatalf("cost = %v, want exactly 0 dollars for an unpriced call", got.Cost)
	}
	if got.BillingClass != BillingClassUnpriced {
		t.Fatalf("billing class = %q, want %q", got.BillingClass, BillingClassUnpriced)
	}
	const wantUnpricedTokens = 2200 // 1,000 prompt + 200 output + 1,000 cache read
	if got.UnpricedTokens != wantUnpricedTokens {
		t.Fatalf("unpriced tokens = %d, want %d (all prompt, output, and cache-read tokens)", got.UnpricedTokens, wantUnpricedTokens)
	}
}

// TestPrice_MissingCacheUsesInput_ZeroCacheStaysZero is TDD row 6 and the
// cache-price dataset. Input is $2 per million.
func TestPrice_MissingCacheUsesInput_ZeroCacheStaysZero(t *testing.T) {
	cases := []struct {
		name      string
		cache     *float64
		tokens    int
		wantCost  float64
		wantClass string
		unpriced  bool
		input     *float64
	}{
		{name: "absent cache, zero tokens", cache: nil, tokens: 0, wantCost: 0, wantClass: BillingClassPriced, input: usd(2)},
		{name: "absent cache price", cache: nil, tokens: 1000, wantCost: 0.002, wantClass: BillingClassPriced, input: usd(2)},
		{name: "published zero", cache: usd(0), tokens: 1000, wantCost: 0, wantClass: BillingClassPriced, input: usd(2)},
		{name: "real cache price", cache: usd(0.2), tokens: 1000, wantCost: 0.0002, wantClass: BillingClassPriced, input: usd(2)},
		{name: "no price at all", cache: nil, tokens: 1000, wantCost: 0, wantClass: BillingClassUnpriced, unpriced: true, input: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cost *catalog.ModelCost
			if tc.input != nil || tc.cache != nil {
				cost = &catalog.ModelCost{Input: tc.input, CacheRead: tc.cache}
			}
			got := Price(PriceInput{
				ProviderID: "openai",
				Cost:       cost,
				Usage:      protocoltypes.UsageInfo{CacheReadTokens: tc.tokens},
			})
			if !near(got.Cost, tc.wantCost) {
				t.Errorf("cost = %v, want %v", got.Cost, tc.wantCost)
			}
			if got.BillingClass != tc.wantClass {
				t.Errorf("billing class = %q, want %q", got.BillingClass, tc.wantClass)
			}
			if tc.unpriced && got.UnpricedTokens == 0 {
				t.Error("unpriced tokens = 0, want the cache-read tokens kept and not priced as $0.003")
			}
			if tc.name == "published zero" && got.BillingClass == BillingClassUnpriced {
				t.Error("a published cache price of 0 was marked unpriced; it is a real zero")
			}
		})
	}
}

// TestPrice_SubscriptionRecordsNoDollars is TDD row 6a. The catalog price
// must not become a dollar cost.
func TestPrice_SubscriptionRecordsNoDollars(t *testing.T) {
	got := Price(PriceInput{
		ProviderID:   "openai",
		BillingClass: BillingClassSubscription,
		Cost: &catalog.ModelCost{
			Input:     usd(3),
			CacheRead: usd(0.30),
			Output:    usd(15),
		},
		Usage: protocoltypes.UsageInfo{
			PromptTokens:     1000,
			CacheReadTokens:  8000,
			CompletionTokens: 500,
		},
	})
	if got.Cost != 0 {
		t.Fatalf("cost = %v, want 0 dollars for a ChatGPT sign-in call", got.Cost)
	}
	if got.BillingClass != BillingClassSubscription {
		t.Fatalf("billing class = %q, want %q", got.BillingClass, BillingClassSubscription)
	}
	if got.SubscriptionTokens == 0 {
		t.Fatal("subscription tokens = 0, want the cached and uncached tokens stored")
	}
}

// TestPrice_OneHourWriteUsesDocumentedMultiplier is TDD row 6b.
func TestPrice_OneHourWriteUsesDocumentedMultiplier(t *testing.T) {
	t.Run("anthropic 2x input when cache_write_1h is absent", func(t *testing.T) {
		got := Price(PriceInput{
			ProviderID: "anthropic",
			Cost: &catalog.ModelCost{
				Input:      usd(1),
				CacheWrite: usd(1.25),
			},
			Usage: protocoltypes.UsageInfo{
				CacheWriteTokens:   2000,
				CacheWrite5mTokens: 1000,
				CacheWrite1hTokens: 1000,
			},
		})
		const fiveMinute = 0.00125
		const oneHour = 0.002
		want := fiveMinute + oneHour
		if !near(got.Cost, want) {
			t.Fatalf("cost = %v, want 5-minute %v + 1-hour %v = %v", got.Cost, fiveMinute, oneHour, want)
		}
		if got.OneHourRule != OneHourRuleTwoTimesInput {
			t.Errorf("one-hour rule = %q, want %q", got.OneHourRule, OneHourRuleTwoTimesInput)
		}
	})

	t.Run("cache_write_1h when published", func(t *testing.T) {
		got := Price(PriceInput{
			ProviderID: "anthropic",
			Cost: &catalog.ModelCost{
				Input:        usd(1),
				CacheWrite:   usd(1.25),
				CacheWrite1h: usd(3),
			},
			Usage: protocoltypes.UsageInfo{
				CacheWriteTokens:   2000,
				CacheWrite5mTokens: 1000,
				CacheWrite1hTokens: 1000,
			},
		})
		const fiveMinute = 0.00125
		const oneHour = 0.003
		want := fiveMinute + oneHour
		if !near(got.Cost, want) {
			t.Fatalf("cost = %v, want %v (1-hour at cache_write_1h $3 per million)", got.Cost, want)
		}
		if got.OneHourRule != OneHourRuleCacheWrite1h {
			t.Errorf("one-hour rule = %q, want %q", got.OneHourRule, OneHourRuleCacheWrite1h)
		}
	})

	t.Run("no documented 1-hour factor leaves that portion unpriced", func(t *testing.T) {
		got := Price(PriceInput{
			ProviderID: "cohere",
			Cost: &catalog.ModelCost{
				Input:      usd(1),
				CacheWrite: usd(1.25),
			},
			Usage: protocoltypes.UsageInfo{
				CacheWriteTokens:   2000,
				CacheWrite5mTokens: 1000,
				CacheWrite1hTokens: 1000,
			},
		})
		const fiveMinute = 0.00125
		if !near(got.Cost, fiveMinute) {
			t.Fatalf("cost = %v, want only the 5-minute portion %v; the 1-hour portion has no documented multiplier", got.Cost, fiveMinute)
		}
		if got.OneHourRule != OneHourRuleUnpriced {
			t.Errorf("one-hour rule = %q, want %q", got.OneHourRule, OneHourRuleUnpriced)
		}
		if got.UnpricedTokens != 1000 {
			t.Errorf("unpriced tokens = %d, want the 1,000 one-hour tokens", got.UnpricedTokens)
		}
		if got.BillingClass != BillingClassPriced {
			t.Errorf("billing class = %q, want %q for a mixed call", got.BillingClass, BillingClassPriced)
		}
	})
}

// TestPrice_ContextTier is TDD row 7. "Up to 100,000" is the base rate.
// Exactly 100,000 stays on the base rate. A source that includes the boundary
// in the high band (at_least) keeps that meaning.
func TestPrice_ContextTier(t *testing.T) {
	above := &catalog.ModelCost{
		Input: usd(1),
		Tiers: []catalog.ModelCostTier{{
			Input: usd(2),
			Tier:  catalog.ContextTier{Type: "context", Size: 100000, Boundary: catalog.CostBoundaryAbove},
		}},
	}
	cases := []struct {
		name   string
		cost   *catalog.ModelCost
		tokens int
		want   float64
	}{
		{"99999 base", above, 99999, 99999.0 / 1e6 * 1},
		{"100000 base", above, 100000, 100000.0 / 1e6 * 1},
		{"100001 high", above, 100001, 100001.0 / 1e6 * 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Price(PriceInput{
				ProviderID: "anthropic",
				Cost:       tc.cost,
				Usage:      protocoltypes.UsageInfo{PromptTokens: tc.tokens},
			})
			if !near(got.Cost, tc.want) {
				t.Fatalf("cost = %v, want %v (%s)", got.Cost, tc.want, tc.name)
			}
		})
	}

	t.Run("at_least includes the boundary token in the high band", func(t *testing.T) {
		cost := &catalog.ModelCost{
			Input: usd(1),
			Tiers: []catalog.ModelCostTier{{
				Input: usd(2),
				Tier:  catalog.ContextTier{Type: "context", Size: 100000, Boundary: catalog.CostBoundaryAtLeast},
			}},
		}
		got := Price(PriceInput{
			ProviderID: "anthropic",
			Cost:       cost,
			Usage:      protocoltypes.UsageInfo{PromptTokens: 100000},
		})
		want := 100000.0 / 1e6 * 2
		if !near(got.Cost, want) {
			t.Fatalf("cost = %v, want %v (exactly 100,000 is in the high band when the source includes it)", got.Cost, want)
		}
	})
}

// TestPrice_TierUsesCacheHeavyInput is TDD row 27. Input size is uncached
// plus cache read plus cache write. Output is not part of the size.
func TestPrice_TierUsesCacheHeavyInput(t *testing.T) {
	above := &catalog.ModelCost{
		Input: usd(1),
		Tiers: []catalog.ModelCostTier{{
			Input: usd(2),
			Tier:  catalog.ContextTier{Type: "context", Size: 100000, Boundary: catalog.CostBoundaryAbove},
		}},
	}
	t.Run("100 uncached plus 99900 read is base", func(t *testing.T) {
		got := Price(PriceInput{
			ProviderID: "anthropic",
			Cost:       above,
			Usage:      protocoltypes.UsageInfo{PromptTokens: 100, CacheReadTokens: 99900, CompletionTokens: 50000},
		})
		want := 100000.0 / 1e6 * 1
		if !near(got.Cost, want) {
			t.Fatalf("cost = %v, want base-rate %v (output must not push the band)", got.Cost, want)
		}
	})
	t.Run("100 uncached plus 99901 read is high", func(t *testing.T) {
		got := Price(PriceInput{
			ProviderID: "anthropic",
			Cost:       above,
			Usage:      protocoltypes.UsageInfo{PromptTokens: 100, CacheReadTokens: 99901},
		})
		want := 100001.0 / 1e6 * 2
		if !near(got.Cost, want) {
			t.Fatalf("cost = %v, want high-rate %v", got.Cost, want)
		}
	})

	t.Run("canonical 512000 tier wins over the legacy alias at 200001", func(t *testing.T) {
		cost := &catalog.ModelCost{
			Input: usd(1),
			Tiers: []catalog.ModelCostTier{{
				Input: usd(9),
				Tier:  catalog.ContextTier{Type: "context", Size: 512000, Boundary: catalog.CostBoundaryAbove},
			}},
			ContextOver200k: &catalog.ModelCost{Input: usd(5)},
		}
		got := Price(PriceInput{
			ProviderID: "anthropic",
			Cost:       cost,
			Usage:      protocoltypes.UsageInfo{PromptTokens: 200001},
		})
		want := 200001.0 / 1e6 * 1
		if !near(got.Cost, want) {
			t.Fatalf("cost = %v, want base %v; 200,001 is not the 512,000 band and the alias must not win", got.Cost, want)
		}
	})

	t.Run("alias alone keeps exactly 200000 on the base rate", func(t *testing.T) {
		cost := &catalog.ModelCost{
			Input:           usd(1),
			ContextOver200k: &catalog.ModelCost{Input: usd(5)},
		}
		base := Price(PriceInput{
			ProviderID: "anthropic",
			Cost:       cost,
			Usage:      protocoltypes.UsageInfo{PromptTokens: 200000},
		})
		if !near(base.Cost, 200000.0/1e6*1) {
			t.Fatalf("cost at 200,000 = %v, want the base rate", base.Cost)
		}
		high := Price(PriceInput{
			ProviderID: "anthropic",
			Cost:       cost,
			Usage:      protocoltypes.UsageInfo{PromptTokens: 200001},
		})
		if !near(high.Cost, 200001.0/1e6*5) {
			t.Fatalf("cost at 200,001 = %v, want the alias rate 5 per million", high.Cost)
		}
	})
}

// TestPrice_NoRateTable is TDD row 8. The hand-kept table symbol is gone.
func TestPrice_NoRateTable(t *testing.T) {
	path := filepathJoinAgentLoop()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(b)
	for _, symbol := range []string{"llmRateTable", "llmRateFallback"} {
		if strings.Contains(src, symbol) {
			t.Errorf("hand-kept rate table symbol %s is still in %s; FR-014 deletes it", symbol, path)
		}
	}
}

func filepathJoinAgentLoop() string {
	return "../../agent/loop.go"
}
