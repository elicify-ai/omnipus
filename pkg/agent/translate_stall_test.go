package agent

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Founder decision 2026-09-14 (UAT E-15c): a streaming call aborted for total
// silence gets its own wire code — provider_stalled — with retry semantics
// like network, and the operator's actual silence limit surfaces in the
// Verbose-chat detail. A plain wrap must classify identically.
func TestTranslateTurnError_ProviderStall(t *testing.T) {
	t.Run("typed stall with duration", func(t *testing.T) {
		llm := TranslateTurnError(fmt.Errorf("chat stream: %w", common.NewStallError(5*time.Minute)))
		if llm.Code != CodeProviderStalled {
			t.Fatalf("code = %q, want provider_stalled", llm.Code)
		}
		if !llm.Retryable {
			t.Fatal("a stall must be retryable like a network error")
		}
		if llm.Message == "" {
			t.Fatal("stall must carry the catalogue's user-facing message")
		}
		if !errors.Is(fmt.Errorf("wrap: %w", common.NewStallError(time.Second)), common.ErrStreamStalled) {
			t.Fatal("sentinel not reachable through wrapping — test premise broken")
		}
	})

	t.Run("bare sentinel", func(t *testing.T) {
		llm := TranslateTurnError(common.ErrStreamStalled)
		if llm.Code != CodeProviderStalled {
			t.Fatalf("code = %q, want provider_stalled", llm.Code)
		}
		if !llm.Retryable {
			t.Fatal("bare sentinel must also be retryable")
		}
	})
}

// The retryability contract is enforced by IsRetryableCode too (the SPA and
// replay paths derive from the wire enum, never from this package's private
// helper).
func TestIsRetryableCode_ProviderStalled(t *testing.T) {
	if !IsRetryableCode(CodeProviderStalled) {
		t.Fatal("IsRetryableCode(provider_stalled) = false, want true")
	}
}
