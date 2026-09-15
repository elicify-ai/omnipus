// config_retention.go: Memory, compaction and retention settings and their helpers

package config

// RetentionSessionDays returns the configured session retention days, defaulting to 90.
func (r OmnipusRetentionConfig) RetentionSessionDays() int {
	if r.SessionDays <= 0 {
		return 90
	}
	return r.SessionDays
}

// IsDisabled reports whether retention enforcement is entirely suppressed (keep forever).
func (r OmnipusRetentionConfig) IsDisabled() bool { return r.Disabled }

// RetentionMemoryRetrosDays returns the configured retro retention, defaulting
// to 180. Retrospecives outlive their transcripts (session default is 90 days)
// so reflections remain queryable long after the raw transcript is swept.
// Spec v7 FR-034 — used by MemoryStore.SweepRetros and the recall search
// window for retrospectives.
func (r OmnipusRetentionConfig) RetentionMemoryRetrosDays() int {
	if r.MemoryRetrosDays <= 0 {
		return 180
	}
	return r.MemoryRetrosDays
}

// RetentionMode summarizes the (session_days, disabled) pair into one of
// three operator-facing states. Use Mode() on OmnipusRetentionConfig to
// derive it; the underlying struct fields remain the authoritative
// storage shape for backward compatibility (see
// TestRetention_ZeroSessionDaysStillMeansDefault90).
type RetentionMode int

const (
	RetentionDefault RetentionMode = iota // session_days <= 0 && !Disabled
	RetentionCustom                       // session_days > 0 && !Disabled
	RetentionForever                      // Disabled == true
)

// String returns a lowercase stable label ("default" / "custom" / "forever").
// Used by log lines and by TS consumers via the wire.
func (m RetentionMode) String() string {
	switch m {
	case RetentionCustom:
		return "custom"
	case RetentionForever:
		return "forever"
	default:
		return "default"
	}
}

// Mode classifies the retention config into one of three states.
// Disabled takes precedence over SessionDays — setting disabled: true with
// session_days: 99 still means "forever".
func (r OmnipusRetentionConfig) Mode() RetentionMode {
	if r.Disabled {
		return RetentionForever
	}
	if r.SessionDays > 0 {
		return RetentionCustom
	}
	return RetentionDefault
}

// memorySignal is one determinable (available, total) reading of this
// host's memory. Two sources can produce one — the kernel's own host-wide
// figures and the process's cgroup limit — and either, both, or neither may
// be determinable at any moment.
type memorySignal struct {
	available uint64
	total     uint64
}

// cgroupBudgetProvider is the cgroup-signal seam. Package-level and
// cross-platform (unlike cgroupRoot, which only exists on Linux) so a test can
// control or forbid the cgroup reading on any platform — notably to prove the
// containerisation predicate never consults it.
var cgroupBudgetProvider = readCgroupMemoryBudgetBytes

// memorySignals returns every DETERMINABLE memory signal, in no particular
// order. An empty slice means this host's memory cannot be measured at all:
// a Windows host (no reader exists), a BSD host (no reader exists), or a
// Linux host whose /proc/meminfo is unreadable (gVisor, distroless, a
// hardened seccomp profile). That is a first-class answer, not an error, and
// it is the ONLY thing that makes the two-valued accessors below report
// ok=false.
//
// The two sources:
//
//  1. The host-wide reading — /proc/meminfo's MemAvailable and MemTotal on
//     Linux, an assembled sysctl approximation on Darwin. Accounts for
//     reclaimable page cache (unlike MemFree).
//  2. The process's cgroup memory limit and the headroom under it
//     (readCgroupMemoryBudgetBytes), when a finite limit is configured —
//     common in containerized deployments (Docker, Fly Machines,
//     Kubernetes), where the limit is frequently far tighter than the host's
//     own total memory and is a STABLE, explicitly configured ceiling rather
//     than a live kernel heuristic.
//
// Both are collected because they answer DIFFERENT questions and a caller
// wants the tighter answer to each. Critically (FR-079), an undeterminable
// signal is OMITTED rather than contributed as a zero: the previous code
// compared a cgroup reading against a host-wide reading that had silently
// fabricated 4 GB when /proc/meminfo was unreadable, so on a /proc-less
// container the invented number could win the comparison and discard the one
// signal that was real.
func memorySignals() []memorySignal {
	var out []memorySignal
	if avail, ok := readMemAvailableBytes(); ok {
		if total, ok := readMemTotalBytes(); ok && total > 0 {
			out = append(out, memorySignal{available: avail, total: total})
		}
	}
	if avail, limit, ok := cgroupBudgetProvider(); ok && limit > 0 {
		out = append(out, memorySignal{available: avail, total: limit})
	}
	return out
}

// availableRAMBytes returns the current best estimate of memory available
// for starting new work, in bytes, and whether that estimate could be made
// at all.
//
// It is the MINIMUM over the DETERMINABLE signals only (FR-079), so a tight
// container limit is never exceeded by trusting an unconstrained host-wide
// reading, and an unreadable host-wide reading never discards a real cgroup
// one. ok is false when NEITHER signal is determinable — never when one is
// merely tighter than the other.
//
// Known limitation, accepted deliberately: MemAvailable can under-report for
// a period after a fresh boot/container start before the page-cache
// subsystem has warmed up (observed live: 28 MB measured on a box that
// settled at ~370 MB once warm — docs/internal/uat/
// max-parallel-concurrency-gap-2026-07-31.md G4, cross-referenced against
// parallelism-cost-measurement-2026-08-04.md's clean-idle baseline). This is
// NOT "solved" by re-sampling with a short in-process delay at boot — the
// warm-up lag observed is tied to the box's actual workload history, not
// milliseconds, so a boot-time retry loop would not reliably help and would
// only delay every gateway boot for no real benefit. Instead, this value is
// deliberately never frozen at boot for any live caller: every production
// call site re-reads it at the moment of admission, so a transient low
// boot-time reading self-corrects as soon as the host's real availability
// changes, with no operator action required.
func availableRAMBytes() (uint64, bool) {
	signals := memorySignals()
	if len(signals) == 0 {
		return 0, false
	}
	tightest := signals[0].available
	for _, sig := range signals[1:] {
		if sig.available < tightest {
			tightest = sig.available
		}
	}
	return tightest, true
}

// memoryProvider and availableMemoryProvider are the injection seam. They
// are package-level vars, following the same pattern as procMeminfoPath and
// cgroupRoot in this package, purely so a test can drive every consumer of
// the memory mechanism off ONE stub and assert they behave identically at
// the seam rather than inferring sameness from equal outcomes. Production
// code never reassigns them.
var (
	memoryProvider          = liveMemoryPressureHigh
	availableMemoryProvider = availableRAMBytes
)

// MemoryPressureHigh reports whether this host is above
// memoryPressureRatioThreshold, and whether that could be determined.
//
// THIS IS THE SHARED SEAM (FR-068). It is the one accessor and the one
// threshold every consumer reads; sameness between the browser pool and
// agent admission is a property of them calling this function, not of them
// happening to compute equal answers. Test seams that stub memory do so by
// replacing this function's provider (see SetMemoryProviderForTest), which
// is what lets one stub drive both consumers in one test body.
//
// The two return values mean different things and must not be collapsed:
//
//   - (false, true) — measured, and there is headroom. Admit.
//   - (true, true)  — measured, and the host is under pressure. Refuse to
//     grow. This is a HARD stop, not a hint.
//   - (_, false)    — this host's memory cannot be measured at all. Each
//     consumer takes its own documented unmeasurable-host branch. Neither
//     refuses to RUN; both refuse to GROW past a conservative floor.
//
// The ratio is computed per determinable signal and the WORST (highest) is
// returned, matching availableRAMBytes taking the tightest available figure:
// a container at 90% of its cgroup limit is under pressure even if the host
// it sits on is idle.
func MemoryPressureHigh() (high bool, ok bool) {
	WarnOnMemoryMechanismFirstUse()
	return memoryProvider()
}

// AvailableMemoryBytes is the exported two-valued live-memory accessor:
// bytes of headroom, and whether that could be determined at all.
//
// Callers wanting a yes/no admission decision should use MemoryPressureHigh
// instead — it carries the one shared threshold, so a caller that compares
// this figure against a threshold of its own has quietly created the second
// mechanism. This exists for callers that need an absolute figure, notably
// the browser pool's per-launch headroom check (does this host have room for
// one more Chrome), which is a bytes question and not a ratio question.
func AvailableMemoryBytes() (uint64, bool) {
	WarnOnMemoryMechanismFirstUse()
	return availableMemoryProvider()
}

// SetCgroupBudgetProviderForTest replaces the cgroup memory-limit reader for
// the duration of a test and returns a restore function. Exported because the
// FR-076 independence property — containerisation is detected WITHOUT reading
// the limit — is only assertable by making the reader fail loudly if touched.
func SetCgroupBudgetProviderForTest(fn func() (uint64, uint64, bool)) func() {
	prev := cgroupBudgetProvider
	cgroupBudgetProvider = fn
	return func() { cgroupBudgetProvider = prev }
}

// memoryPressureRatioThreshold is THE threshold. Singular, deliberately.
//
// Every admission consumer in this process — the browser pool at launch and
// at every tab open, agent admission on the delegation path — asks the same
// question of the same numbers through MemoryPressureHigh, and this is the
// number it compares against. A second threshold constant anywhere would
// re-create the exact defect this work exists to remove: two mechanisms
// disagreeing about one machine, each individually defensible, together
// incoherent.
//
// 0.85 means "85% of the memory budget is in non-reclaimable use". Under a
// cgroup limit that is memory.current-minus-reclaimable over memory.max —
// i.e. the ratio the browser-pool spec names directly. Off a cgroup it is
// the same shape against the host-wide figures. The value leaves roughly a
// seventh of the budget as headroom, which on any host large enough to run
// a browser at all is more than one Chrome's measured launch cost.
const memoryPressureRatioThreshold = 0.85

// liveMemoryPressureHigh is MemoryPressureHigh's real implementation.
func liveMemoryPressureHigh() (bool, bool) {
	signals := memorySignals()
	if len(signals) == 0 {
		return false, false
	}
	worst := 0.0
	for _, sig := range signals {
		if sig.total == 0 {
			continue
		}
		var used float64
		if sig.available < sig.total {
			used = float64(sig.total-sig.available) / float64(sig.total)
		}
		if used > worst {
			worst = used
		}
	}
	return worst > memoryPressureRatioThreshold, true
}

// SetMemoryProviderForTest replaces BOTH memory accessors with stubs for the
// duration of a test and returns a restore function. Exported because the
// consumers under test live in other packages (pkg/agent, pkg/tools/browser)
// and the whole point of the seam is that one stub drives all of them.
//
// It is a test helper in a production file for the same reason
// procMeminfoPath is a var: the alternative is threading an interface
// through every admission call site, which is a much larger change to make
// one property assertable.
func SetMemoryProviderForTest(pressure func() (bool, bool), available func() (uint64, bool)) func() {
	prevPressure, prevAvailable := memoryProvider, availableMemoryProvider
	if pressure != nil {
		memoryProvider = pressure
	}
	if available != nil {
		availableMemoryProvider = available
	}
	return func() {
		memoryProvider, availableMemoryProvider = prevPressure, prevAvailable
	}
}

// MemoryPressureHighFromSignalsForTest builds a MemoryPressureHigh provider
// from one synthetic (available, total) pair, running the REAL threshold
// comparison over it.
//
// Tests use it rather than stubbing the boolean directly so that a consumer
// test actually exercises the shared comparison — including the boundary,
// where "> threshold" and ">= threshold" differ at exactly one value and
// nowhere else. A test that stubs the boolean proves the consumer branches on
// what it is told; this proves the consumer branches on what the mechanism
// decides.
func MemoryPressureHighFromSignalsForTest(available, total uint64) func() (bool, bool) {
	return func() (bool, bool) {
		if total == 0 {
			return false, false
		}
		used := 0.0
		if available < total {
			used = float64(total-available) / float64(total)
		}
		return used > memoryPressureRatioThreshold, true
	}
}
