// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// capturedLogs collects slog records emitted while a test runs, so an
// assertion can be made on WHAT was logged and HOW MANY TIMES rather than on
// the mere fact that logging code exists.
type capturedLogs struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

// matching returns the captured lines containing needle.
func (c *capturedLogs) matching(needle string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, line := range strings.Split(c.buf.String(), "\n") {
		if strings.TrimSpace(line) != "" && strings.Contains(line, needle) {
			out = append(out, line)
		}
	}
	return out
}

// lockedWriter serializes writes into the capture buffer. slog handlers may be
// invoked from several goroutines, and a bytes.Buffer is not safe for
// concurrent use — an unsynchronized capture is a data race that -race would
// (correctly) fail the whole package on.
type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// captureSlogWarnings redirects the default slog logger into a buffer for the
// duration of the test and restores it afterwards.
func captureSlogWarnings(t *testing.T) *capturedLogs {
	t.Helper()
	mu := &sync.Mutex{}
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(lockedWriter{mu: mu, buf: buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &capturedLogs{mu: mu, buf: buf}
}

// FR-068 / FR-068a: agent admission is not a second memory mechanism.
//
// These tests all drive the SAME injected accessor the browser pool reads
// (config.SetMemoryProviderForTest). That is the point: sameness must be
// asserted AT THE SEAM. Two consumers that happen to produce equal outcomes on
// the fixtures a test author chose are not the same mechanism — they are two
// mechanisms that have not disagreed yet, which is precisely the state this
// work exists to end.

// stubMemory installs a fixed memory answer for the duration of a test.
func stubMemory(t *testing.T, high, ok bool) {
	t.Helper()
	restore := config.SetMemoryProviderForTest(
		func() (bool, bool) { return high, ok },
		func() (uint64, bool) {
			if !ok {
				return 0, false
			}
			return 8 << 30, true
		},
	)
	t.Cleanup(restore)
	resetMemoryAdmissionRefusalLogForTest()
	t.Cleanup(resetMemoryAdmissionRefusalLogForTest)
}

// TestAgentAdmission_UsesSameAccessorAndThresholdAsPool proves the sameness at
// the injection seam.
//
// It drives admission off ONE stubbed accessor at the three fixture ratios that
// bracket the threshold — below, at, and above 0.85 — and asserts admission's
// answer tracks the accessor's, not a rule of its own. The 0.85 boundary case
// is included deliberately: the threshold is "> 0.85", so a host exactly AT the
// threshold is not yet under pressure, and an implementation that used >= would
// diverge from the pool at exactly one value and nowhere else.
func TestAgentAdmission_UsesSameAccessorAndThresholdAsPool(t *testing.T) {
	cases := []struct {
		name     string
		ratio    float64
		wantHigh bool
		// wantCap is how many concurrent turns the mechanism permits, with a
		// CONFIGURED cap of 50. Under pressure it collapses to the floor — but
		// to the floor, never to zero: the response is refuse-to-grow, not
		// refuse-to-run, so the first turn is admitted at every ratio.
		wantCap int
	}{
		{"below the threshold (0.84) — headroom, the operator's cap governs", 0.84, false, 50},
		{"exactly at the threshold (0.85) — not yet pressure", 0.85, false, 50},
		{"above the threshold (0.86) — pressure, hold at the floor", 0.86, true, unmeasurableHostAgentFloor},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Drive the REAL predicate off synthetic memory figures rather
			// than stubbing the boolean, so this test exercises the actual
			// comparison the pool will also go through. total=1e6 with
			// available = (1-ratio)*total reproduces the ratio exactly.
			const total = uint64(1_000_000)
			available := uint64(float64(total) * (1 - tc.ratio))
			restore := config.SetMemoryProviderForTest(
				config.MemoryPressureHighFromSignalsForTest(available, total),
				func() (uint64, bool) { return available, true },
			)
			defer restore()
			resetMemoryAdmissionRefusalLogForTest()

			high, ok := config.MemoryPressureHigh()
			if !ok {
				t.Fatal("MemoryPressureHigh reported undeterminable on a fully specified fixture")
			}
			if high != tc.wantHigh {
				t.Fatalf("config.MemoryPressureHigh() at ratio %.2f = %v, want %v — the shared threshold has moved", tc.ratio, high, tc.wantHigh)
			}

			// The consumer, off the same seam. A configured cap of 50, well
			// above the floor, so the only thing that can lower it is memory.
			const configured = 50
			gotCap, memoryBinding := applyMemoryCap(configured)
			if gotCap != tc.wantCap {
				t.Fatalf("applyMemoryCap(%d) at ratio %.2f = %d, want %d — agent admission is not tracking the shared accessor", configured, tc.ratio, gotCap, tc.wantCap)
			}
			if memoryBinding != tc.wantHigh {
				t.Fatalf("applyMemoryCap reported memoryBinding=%v at ratio %.2f, want %v — a refusal names memory only when memory is what refused", memoryBinding, tc.ratio, tc.wantHigh)
			}

			// And the same answer arrived at through the real admission gate:
			// every slot up to the cap admits, the one past it does not. Under
			// pressure the FIRST turn is still admitted, which is the whole
			// difference between refusing to grow and refusing to run.
			a := newAdmissionController(configured)
			for i := 1; i <= tc.wantCap; i++ {
				admitted, reason, release := a.TryAdmitWithReason(scopeName(i))
				if !admitted {
					t.Fatalf("admission %d of %d was refused (reason %q) at ratio %.2f", i, tc.wantCap, reason, tc.ratio)
				}
				defer release()
			}
			admitted, reason, _ := a.TryAdmitWithReason(scopeName(tc.wantCap + 1))
			if admitted {
				t.Fatalf("admission %d succeeded at ratio %.2f, want refused (the cap is %d)", tc.wantCap+1, tc.ratio, tc.wantCap)
			}
			wantReason := ""
			if tc.wantHigh {
				wantReason = config.ReasonMemoryPressure
			}
			if reason != wantReason {
				t.Fatalf("the refusal past the cap carried reason %q, want %q at ratio %.2f — a memory refusal and a configured-cap refusal send an operator to two different remedies, and only one of them exists here", reason, wantReason, tc.ratio)
			}
		})
	}
}

// TestAgentAdmission_UnmeasurableHoldsAtFloorAndRefusesThird is FR-068a.
//
// On a host whose memory cannot be measured, admission holds at TWO and refuses
// the third naming memory. Refuse to GROW, never refuse to RUN — a host that
// cannot report its own memory must still be able to run agents, or every
// Windows and gVisor deployment becomes useless for the sake of a reading
// nobody can take.
//
// BOTH halves are asserted in one body. "Refuses the third" alone is satisfied
// by a build that refuses everything, which would be a far worse outcome than
// the one being prevented.
func TestAgentAdmission_UnmeasurableHoldsAtFloorAndRefusesThird(t *testing.T) {
	// Both ok=false shapes must be covered. Only the second is reachable on
	// Linux CI, and the first is the one every Mac and Windows box takes.
	shapes := []struct {
		name    string
		install func(t *testing.T)
	}{
		{
			name: "platform with no reader at all (Windows, BSD)",
			install: func(t *testing.T) {
				stubMemory(t, false, false)
			},
		},
		{
			name: "Linux with an unreadable /proc/meminfo (gVisor, distroless)",
			install: func(t *testing.T) {
				// The same ok=false, arrived at the other way: the readers
				// exist but every signal is undeterminable.
				stubMemory(t, true, false)
			},
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			shape.install(t)

			// A configured cap far above the floor, so if the floor is not
			// applied the third admission succeeds and this test fails.
			a := newAdmissionController(50)

			for i := 1; i <= unmeasurableHostAgentFloor; i++ {
				admitted, reason, release := a.TryAdmitWithReason(scopeName(i))
				if !admitted {
					t.Fatalf("admission %d of %d was REFUSED (reason %q) on an unmeasurable host — the floor must ADMIT up to %d; refusing to run is not the same as refusing to grow",
						i, unmeasurableHostAgentFloor, reason, unmeasurableHostAgentFloor)
				}
				defer release()
			}

			third := unmeasurableHostAgentFloor + 1
			admitted, reason, _ := a.TryAdmitWithReason(scopeName(third))
			if admitted {
				t.Fatalf("admission %d succeeded on an unmeasurable host with a configured cap of 50 — the memory floor is not being applied at all", third)
			}
			if reason != config.ReasonMemoryPressure {
				t.Fatalf("admission %d was refused with reason %q, want %q — an operator told 'the cap is reached' will go looking for a cap to raise, and on this host there is none that will help",
					third, reason, config.ReasonMemoryPressure)
			}
		})
	}
}

// TestAgentAdmission_UnmeasurableDoesNotRaiseAnOperatorsOwnLowerCap is the
// guard on the floor's direction. The floor is a CEILING for the memory
// mechanism, never a floor that overrides an operator downwards-configured
// choice. An operator who set max_parallel_agents=1 asked for single-flight and
// must get it, on an unmeasurable host as much as on any other.
func TestAgentAdmission_UnmeasurableDoesNotRaiseAnOperatorsOwnLowerCap(t *testing.T) {
	stubMemory(t, false, false)

	a := newAdmissionController(1)

	admitted, _, release := a.TryAdmitWithReason("first")
	if !admitted {
		t.Fatal("the first admission was refused with a configured cap of 1")
	}
	defer release()

	if admitted, _, _ := a.TryAdmitWithReason("second"); admitted {
		t.Fatal("a second admission succeeded with an explicitly configured cap of 1 — the unmeasurable-host floor of 2 must never RAISE an operator's own lower choice; that is the silent-clamping anti-pattern in the other direction")
	}
}

// TestAgentAdmission_UnmeasurableRefusalIsLoggedOnce fails on zero lines AND on
// one-per-call.
//
// Zero is a silent control: an operator whose host holds at two concurrent
// agents with nothing in the log has no way to discover why. One-per-call is
// just as bad in practice — this path is hit on every admission check, so an
// unthrottled line buries the log it is meant to make diagnosable, and the
// operator ends up filtering it out.
func TestAgentAdmission_UnmeasurableRefusalIsLoggedOnce(t *testing.T) {
	stubMemory(t, false, false)

	logs := captureSlogWarnings(t)

	a := newAdmissionController(50)
	var releases []func()
	for i := 1; i <= unmeasurableHostAgentFloor; i++ {
		_, _, release := a.TryAdmitWithReason(scopeName(i))
		if release != nil {
			releases = append(releases, release)
		}
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()

	// Refuse repeatedly. The condition is static, so the log must not be.
	const refusals = 25
	for i := 0; i < refusals; i++ {
		a.TryAdmitWithReason(scopeName(100 + i))
	}

	lines := logs.matching(config.ReasonMemoryPressure)
	if len(lines) == 0 {
		t.Fatalf("%d memory-bound refusals produced NO log line naming %q — a control that holds a host at a floor with nothing in the log is a control an operator cannot discover", refusals, config.ReasonMemoryPressure)
	}
	if len(lines) > 1 {
		t.Fatalf("%d memory-bound refusals produced %d log lines, want exactly 1 — this path runs on every admission check and the condition is static for the life of the process, so an unthrottled line buries the log it exists to provide", refusals, len(lines))
	}
}

// TestAgentAdmission_UnmeasurableStillServesATurn is the inverse guard, and it
// is the one that matters most.
//
// Every other test here asserts something is REFUSED. A build that refused
// everything would pass all of them. This asserts the product still works: an
// ordinary admission on an unmeasurable host succeeds, does real work, and
// releases its slot so the next one can too.
func TestAgentAdmission_UnmeasurableStillServesATurn(t *testing.T) {
	stubMemory(t, false, false)

	a := newAdmissionController(50)

	// Admit, release, admit again, many times over. If the floor were
	// implemented as a lifetime budget rather than a concurrency bound, this is
	// where it would show.
	for i := 0; i < 10; i++ {
		admitted, reason, release := a.TryAdmitWithReason("the-only-turn")
		if !admitted {
			t.Fatalf("turn %d was refused (reason %q) on an unmeasurable host with nothing else in flight — the floor bounds CONCURRENCY, not total work; a host that cannot measure its memory must still run agents", i, reason)
		}
		release()
	}

	if got := a.ActiveScopes(); got != 0 {
		t.Fatalf("ActiveScopes() = %d after every slot was released, want 0 — a leaked slot would eventually pin an unmeasurable host at zero admissions", got)
	}
}

// TestAgentAdmission_NoPerAgentConstantInPath is the structural half: the
// admission path must contain no per-agent BYTE figure under any name, and no
// second threshold.
//
// The behavioural tests above cannot catch this. A per-agent constant
// reintroduced alongside the live gate would produce identical results on every
// fixture here — it only diverges on a real host, at which point agent
// concurrency is a second mechanism again and nothing in the suite says so.
func TestAgentAdmission_NoPerAgentConstantInPath(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "admission.go"))
	if err != nil {
		t.Fatalf("read admission.go: %v", err)
	}
	text := string(src)

	// Any megabyte-scale byte literal in this file would be a per-agent (or
	// per-anything) memory budget. The gate is a RATIO plus a count; it has no
	// business naming a quantity of bytes.
	byteLiterals := []string{
		"* 1024 * 1024",
		"<< 20",
		"MB",
		"MiB",
	}
	for _, lit := range byteLiterals {
		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // prose may discuss the deleted constant
			}
			if strings.Contains(trimmed, lit) {
				t.Errorf("admission.go's code contains %q (%q) — the admission path must carry no per-agent byte figure under any name; concurrency is bounded by a live ratio, not by dividing memory by an assumed cost", lit, trimmed)
			}
		}
	}

	// And no second threshold: the ratio lives in pkg/config and is read
	// through MemoryPressureHigh.
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, "0.8") || strings.Contains(trimmed, "0.9") {
			t.Errorf("admission.go's code contains a ratio literal (%q) — there is exactly one memory-pressure threshold and it lives in pkg/config; a second one here is how the two mechanisms come back", trimmed)
		}
	}
}

// scopeName produces a DISTINCT scope per index. Distinctness is load-bearing:
// TryAdmit admits a repeat scope without consuming a slot (a follow-up turn in
// an existing session), so a helper that collided would make every "the third
// is refused" assertion pass for the wrong reason.
func scopeName(i int) string {
	return "scope-" + strconv.Itoa(i)
}
