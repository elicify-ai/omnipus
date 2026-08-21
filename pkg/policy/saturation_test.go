// Tests for the saturation cap validation + evaluation (FR-016).

package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// newTestLogger spins up a logger writing to a temp dir and returns it
// alongside the audit path.
func newTestLogger(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
	})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	// Test cleanup: Close error is inconsequential — t.TempDir() removes the
	// backing directory regardless.
	t.Cleanup(func() { _ = lg.Close() })
	return lg, filepath.Join(dir, "audit.jsonl")
}

func readEvents(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var events []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal: %v: %q", err, line)
		}
		events = append(events, r.Event)
	}
	return events
}

// TestSaturationGuard_NegativeCapRejected — FR-016: a negative cap MUST
// be rejected at boot with HIGH audit `gateway.config.invalid_value` and
// a stderr-fallback line; the gateway exits non-zero.
//
// NOTE: This test mutates the package-level audit.BootAbortWriter and
// therefore must not run with t.Parallel(). The companion
// TestSaturationGuard_NilLogger has the same constraint. Running them in
// parallel races on the writer pointer — the last test to assign wins,
// and the earlier test's buffer ends up empty (observed in CI on fast
// multi-core runners).
func TestSaturationGuard_NegativeCapRejected(t *testing.T) {
	lg, path := newTestLogger(t)

	var stderr bytes.Buffer
	prev := audit.BootAbortWriter
	audit.BootAbortWriter = &stderr
	t.Cleanup(func() { audit.BootAbortWriter = prev })

	effective, ok := ValidateSaturationCap(context.Background(), lg, -1)
	if ok {
		t.Fatalf("ValidateSaturationCap(-1) ok = true, want false")
	}
	if effective != 0 {
		t.Fatalf("effective cap on reject: got %d, want 0", effective)
	}
	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events := readEvents(t, path)
	found := false
	for _, e := range events {
		if e == audit.EventGatewayConfigInvalidValue {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q audit event; got %v",
			audit.EventGatewayConfigInvalidValue, events)
	}
	if !strings.Contains(stderr.String(), "BOOT_ABORT_REASON="+audit.EventGatewayConfigInvalidValue) {
		t.Errorf("expected BOOT_ABORT_REASON line on stderr, got %q", stderr.String())
	}
}

// TestSaturationGuard_ZeroEmitsWarn — FR-016: 0 is the sentinel
// "unlimited"; boot continues but a WARN audit fires.
func TestSaturationGuard_ZeroEmitsWarn(t *testing.T) {
	t.Parallel()
	lg, path := newTestLogger(t)

	effective, ok := ValidateSaturationCap(context.Background(), lg, 0)
	if !ok {
		t.Fatalf("ValidateSaturationCap(0) ok = false, want true")
	}
	if effective != 0 {
		t.Fatalf("effective cap on unlimited: got %d, want 0", effective)
	}
	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events := readEvents(t, path)
	if len(events) != 1 || events[0] != audit.EventGatewayStartupGuardDisabled {
		t.Errorf("expected exactly one %q event, got %v",
			audit.EventGatewayStartupGuardDisabled, events)
	}
}

// TestSaturationGuard_PositiveCapAccepted — happy path: the cap is
// returned verbatim and no audit fires.
func TestSaturationGuard_PositiveCapAccepted(t *testing.T) {
	t.Parallel()
	lg, path := newTestLogger(t)
	effective, ok := ValidateSaturationCap(context.Background(), lg, 64)
	if !ok || effective != 64 {
		t.Fatalf("got (%d, %v), want (64, true)", effective, ok)
	}
	// Force-close before reading back so the writer is flushed; the
	// t.Cleanup close (idempotent no-op on an already-closed logger) is
	// the real guarantee, so this Close's error is not actionable here.
	if closeErr := lg.Close(); closeErr != nil {
		_ = closeErr
	}
	if events := readEvents(t, path); len(events) != 0 {
		t.Errorf("positive cap should not emit audit; got %v", events)
	}
}

// TestSaturationGuard_NilLogger — boot-validation MUST work even when
// audit is disabled (early-boot path); the stderr fallback still fires
// for the negative case.
//
// NOTE: mutates the package-level audit.BootAbortWriter; cannot run
// with t.Parallel(). See TestSaturationGuard_NegativeCapRejected above
// for the race explanation.
func TestSaturationGuard_NilLogger(t *testing.T) {
	var stderr bytes.Buffer
	prev := audit.BootAbortWriter
	audit.BootAbortWriter = &stderr
	t.Cleanup(func() { audit.BootAbortWriter = prev })

	if _, ok := ValidateSaturationCap(context.Background(), nil, -5); ok {
		t.Fatalf("nil-logger negative cap should still be rejected")
	}
	if !strings.Contains(stderr.String(), "BOOT_ABORT_REASON=") {
		t.Errorf("nil-logger path must still print stderr fallback; got %q", stderr.String())
	}
}

// TestShouldSaturate exercises the cap-evaluation matrix.
func TestShouldSaturate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cap, pending int
		want         bool
	}{
		{cap: 0, pending: 0, want: false},         // unlimited
		{cap: 0, pending: 1_000_000, want: false}, // unlimited
		{cap: 64, pending: 0, want: false},
		{cap: 64, pending: 63, want: false},
		{cap: 64, pending: 64, want: true}, // at-cap rejects
		{cap: 64, pending: 100, want: true},
	}
	for _, c := range cases {
		if got := ShouldSaturate(c.cap, c.pending); got != c.want {
			t.Errorf("ShouldSaturate(%d, %d) = %v, want %v",
				c.cap, c.pending, got, c.want)
		}
	}
}
