package agent

import (
	"errors"
	"strings"
	"testing"
)

// S1 final-gate L1: an ineligible pair is reported as an ordinary error. The
// old format string wrapped a nil error with %w and printed "%!w(<nil>)".
func TestAdmitBoundConnectorInput_IneligibleMainErrorIsWellFormed(t *testing.T) {
	f := boundConnFixture(t)
	f.deps.eligible[addrWS+"/"+addrReceiver] = false
	msg := connMsg("chat-1", "hello")
	refusal, err := f.al.admitBoundConnectorInput(&msg)
	if err == nil || refusal == "" {
		t.Fatalf("an ineligible pair must be refused with an error, got refusal=%q err=%v", refusal, err)
	}
	if strings.Contains(err.Error(), "%!") {
		t.Fatalf("the error text is malformed: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "no eligible main") {
		t.Fatalf("the error must say why: %q", err.Error())
	}

	// An eligibility lookup failure keeps its cause in the chain.
	cause := errors.New("membership unavailable")
	f.deps.err = cause
	_, err = f.al.admitBoundConnectorInput(&msg)
	if !errors.Is(err, cause) {
		t.Fatalf("a lookup failure must wrap its cause, got %v", err)
	}
}
