package envcontext_test

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/envcontext"
)

// TestRender_PathsCannotUse_ConditionalOnSandboxMode covers finding 6
// (context-audit 2026-08): the "### Paths you cannot use" section used to
// state absolute denials ("is denied", "are blocked") unconditionally,
// regardless of whether the sandbox was actually kernel-enforced, running in
// application-level fallback, or fully off (sandbox.mode: off is an
// explicitly supported operator choice) — misleading the agent about what is
// actually guaranteed. This proves the wording is now calibrated to the
// resolved mode.
func TestRender_PathsCannotUse_ConditionalOnSandboxMode(t *testing.T) {
	base := func(mode string) *mockProvider {
		return &mockProvider{
			sandboxMode:   mode,
			networkPolicy: envcontext.NetworkPolicy{OutboundAllowed: false},
			workspacePath: "/workspace",
			omnipusHome:   "/home/.omnipus",
		}
	}

	t.Run("kernel-enforced (landlock) states absolute denial", func(t *testing.T) {
		out := envcontext.Render(base("landlock-abi-4"), "")
		if !strings.Contains(out, "is denied unless explicitly allow-listed") {
			t.Errorf("expected absolute denial wording for a kernel-enforced mode; got:\n%s", out)
		}
		if strings.Contains(out, "rules to follow, not guarantees") {
			t.Errorf("kernel-enforced mode should not carry the fallback hedge; got:\n%s", out)
		}
	})

	t.Run("kernel-enforced (seatbelt) states absolute denial", func(t *testing.T) {
		out := envcontext.Render(base("seatbelt"), "")
		if !strings.Contains(out, "is denied unless explicitly allow-listed") {
			t.Errorf("expected absolute denial wording for a kernel-enforced mode; got:\n%s", out)
		}
	})

	t.Run("fallback hedges enforcement as application-level, not a guarantee", func(t *testing.T) {
		out := envcontext.Render(base("fallback"), "")
		if !strings.Contains(out, "APPLICATION-level") {
			t.Errorf("expected fallback mode to name application-level enforcement; got:\n%s", out)
		}
		if !strings.Contains(out, "rules to follow, not guarantees") {
			t.Errorf("expected fallback mode to hedge as a rule, not a guarantee; got:\n%s", out)
		}
	})

	t.Run("off states plainly that no sandbox is active", func(t *testing.T) {
		out := envcontext.Render(base("off"), "")
		if !strings.Contains(out, "No sandbox is active") {
			t.Errorf("expected off mode to state plainly that no sandbox is active; got:\n%s", out)
		}
		if strings.Contains(out, "is denied unless explicitly allow-listed") {
			t.Errorf("off mode must not claim an unenforced denial as absolute fact; got:\n%s", out)
		}
	})

	t.Run("unknown (SandboxMode error) hedges like fallback rather than claiming a guarantee", func(t *testing.T) {
		p := base("")
		p.sandboxErr = errFakeSandbox
		out := envcontext.Render(p, "")
		if !strings.Contains(out, "rules to follow, not guarantees") {
			t.Errorf("expected the conservative fallback-style hedge for an unresolvable sandbox mode; got:\n%s", out)
		}
		if strings.Contains(out, "is denied unless explicitly allow-listed") {
			t.Errorf("an unresolvable sandbox mode must not be presented as an absolute guarantee; got:\n%s", out)
		}
	})

	// All three postures must render distinguishably different "Paths you
	// cannot use" wording — a regression that collapsed them back to one
	// unconditional string would defeat the whole fix even if each
	// individual assertion above still passed by coincidence.
	t.Run("off, fallback, and kernel-enforced all differ", func(t *testing.T) {
		off := envcontext.Render(base("off"), "")
		fallback := envcontext.Render(base("fallback"), "")
		kernel := envcontext.Render(base("landlock-abi-4"), "")
		if off == fallback || off == kernel || fallback == kernel {
			t.Errorf("expected off/fallback/kernel-enforced renders to all differ;\noff:\n%s\nfallback:\n%s\nkernel:\n%s",
				off, fallback, kernel)
		}
	})
}

// errFakeSandbox is a fixed sentinel error for the "unknown" SandboxMode()
// test case above.
var errFakeSandbox = &fakeSandboxError{}

type fakeSandboxError struct{}

func (e *fakeSandboxError) Error() string { return "fake sandbox mode error" }
