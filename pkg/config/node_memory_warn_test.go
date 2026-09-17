// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// lockedBuf serializes writes from the slog handler. Handlers may be called
// from several goroutines and bytes.Buffer is not safe for concurrent use — an
// unsynchronized capture is a data race that -race would correctly fail the
// whole package on.
type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// captureWarnings redirects the default slog logger (which pkg/logger writes
// through) into a buffer for the test's duration.
func captureWarnings(t *testing.T) *lockedBuf {
	t.Helper()
	lb := &lockedBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return lb
}

// TestNodeMemoryWarn_FiresOnlyInAnUnlimitedContainer is FR-077's whole
// requirement in ONE test body: four cases, THREE of which assert SILENCE.
//
// The three silent cases are what make this test worth having. A warning that
// fires on all four is trivially "covered" by a test that only checks the noisy
// case — and it is also useless, because an operator who sees it on their
// laptop, in a properly-limited container, and on bare metal learns within a
// week to filter it out. At that point the one deployment it exists for gets
// no warning at all, and nothing in the suite says so.
func TestNodeMemoryWarn_FiresOnlyInAnUnlimitedContainer(t *testing.T) {
	cases := []struct {
		name          string
		containerised bool
		limited       bool
		wantWarn      bool
	}{
		{"containerised with NO memory limit — the one case that needs saying", true, false, true},
		{"containerised WITH a memory limit — the reader has this container's own budget", true, true, false},
		{"bare metal with no cgroup limit — the host's memory IS this process's memory", false, false, false},
		{"bare metal with a cgroup limit — someone set one deliberately", false, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetNodeMemoryWarnForTest()
			resetNodeMemoryWarnOnceForTest()
			t.Cleanup(resetNodeMemoryWarnForTest)
			t.Cleanup(resetNodeMemoryWarnOnceForTest)

			// Containerisation seams.
			cgroupFile, dockerenvFile := withContainerFixtures(t)
			cgroupContent := "0::/user.slice/user-1000.slice/session-2.scope\n"
			if tc.containerised {
				cgroupContent = "0::/kubepods/besteffort/pod1234abcd\n"
			}
			if err := os.WriteFile(cgroupFile, []byte(cgroupContent), 0o600); err != nil {
				t.Fatalf("write cgroup fixture: %v", err)
			}
			_ = dockerenvFile // deliberately absent in every case

			// Memory-limit seam, set INDEPENDENTLY of containerisation — which
			// is the whole point of FR-076 and is what makes all four
			// combinations expressible at all. Driven through the exported
			// provider rather than a fixture directory so this test runs on
			// every platform, not only the one with cgroups.
			limited := tc.limited
			t.Cleanup(SetCgroupBudgetProviderForTest(func() (uint64, uint64, bool) {
				if !limited {
					return 0, 0, false
				}
				return 2 << 30, 4 << 30, true
			}))

			logs := captureWarnings(t)

			// Startup must SUCCEED in all four cases. This is a warning, never
			// a refusal: an operator who has deliberately given a container the
			// whole node is not doing anything wrong, and refusing to boot over
			// a sizing heuristic would be far worse than the problem.
			WarnIfContainerHasNoMemoryLimit()

			out := logs.String()
			fired := strings.Contains(out, "no container memory limit set")

			if fired != tc.wantWarn {
				t.Fatalf("warning fired = %v, want %v (containerised=%v, limited=%v).\nCaptured log:\n%s",
					fired, tc.wantWarn, tc.containerised, tc.limited, out)
			}

			if tc.wantWarn {
				// The warning must name BOTH the condition and a remedy that
				// exists. A warning naming a condition with no remedy just
				// tells an operator they have a problem.
				if !strings.Contains(out, "sizing against node memory") {
					t.Errorf("the warning does not name the CONSEQUENCE (\"sizing against node memory\") — without it an operator cannot tell whether it matters.\nCaptured log:\n%s", out)
				}
				if !strings.Contains(out, "resources.limits.memory") {
					t.Errorf("the warning does not name the REMEDY (resources.limits.memory) — the fix is one field in a pod spec and the reader picks it up with no change here, so there is no excuse for making an operator go looking.\nCaptured log:\n%s", out)
				}
				if strings.Contains(strings.ToLower(out), "refus") || strings.Contains(strings.ToLower(out), "abort") {
					t.Errorf("the warning reads as a refusal. Startup succeeds in all four cases; wording it as a refusal sends an operator to debug a boot failure that did not happen.\nCaptured log:\n%s", out)
				}
			}
		})
	}
}

// TestNodeMemoryWarn_IsOncePerProcess: a second call emits nothing further.
//
// The condition is static for the life of the process — a container does not
// acquire a memory limit while running — so a repeated warning is pure noise,
// and noise is how a real warning stops being read.
func TestNodeMemoryWarn_IsOncePerProcess(t *testing.T) {
	resetNodeMemoryWarnForTest()
	resetNodeMemoryWarnOnceForTest()
	t.Cleanup(resetNodeMemoryWarnForTest)
	t.Cleanup(resetNodeMemoryWarnOnceForTest)

	cgroupFile, _ := withContainerFixtures(t)
	if err := os.WriteFile(cgroupFile, []byte("0::/kubepods/pod-abc\n"), 0o600); err != nil {
		t.Fatalf("write cgroup fixture: %v", err)
	}
	t.Cleanup(SetCgroupBudgetProviderForTest(func() (uint64, uint64, bool) {
		return 0, 0, false // no limit configured
	}))

	logs := captureWarnings(t)

	for i := 0; i < 5; i++ {
		WarnIfContainerHasNoMemoryLimit()
	}

	got := strings.Count(logs.String(), "no container memory limit set")
	if got != 1 {
		t.Fatalf("5 calls produced %d warnings, want exactly 1 — the condition is static for the life of the process, so a repeated line is pure noise and noise is how a real warning stops being read.\nCaptured log:\n%s", got, logs.String())
	}
}
