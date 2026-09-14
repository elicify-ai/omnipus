package providers

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Founder decision 2026-09-14: a stall-aborted streaming call classifies as a
// transient transport fault (FailoverTimeout — the only reason the agent loop
// retries inline), exactly like a connection drop, regardless of the
// sentinel's wording. The sentinel must also be reachable through wrapping,
// which is how every caller receives it.
func TestClassifyError_StreamStallIsFailoverTimeout(t *testing.T) {
	for name, err := range map[string]error{
		"typed": common.NewStallError(5 * time.Minute),
		"wrapped": fmt.Errorf("chat stream: %w",
			fmt.Errorf("attempt 2: %w", common.NewStallError(90*time.Second))),
	} {
		t.Run(name, func(t *testing.T) {
			fe := ClassifyError(err, "openrouter", "z-ai/glm-5.3")
			if fe == nil {
				t.Fatal("a stall error must be classifiable")
			}
			if fe.Reason != FailoverTimeout {
				t.Fatalf("Reason = %q, want %q (inline-retryable)", fe.Reason, FailoverTimeout)
			}
			if !fe.IsRetriable() {
				t.Fatal("a stall must be retriable like a network error")
			}
			if !errors.Is(fe.Wrapped, common.ErrStreamStalled) {
				t.Fatalf("the classified error must still expose the sentinel; got %v", fe.Wrapped)
			}
		})
	}
}
