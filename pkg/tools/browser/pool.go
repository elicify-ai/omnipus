// Omnipus — the per-workspace browser pool (ADR-072 D1, FR-037/FR-040..FR-054)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

// pool.go replaces "one Chrome for the whole gateway" with "one Chrome per
// BrowsingKey". A workspace's browser is a real, separate OS process with its
// own --user-data-dir profile directory, its own cookie jar, its own logins
// and its own crash blast radius.
//
// That is the change the isolation sentence in browser_list_tabs' and
// browser_open_tab's descriptions rests on. Before this file existed, those
// tools could only honestly say every agent on a workspace shares one browser;
// they could not say a workspace cannot see another workspace's, because one
// Chrome served the whole install. ADR-072 §1.1 is the record of an agent
// getting that wrong in the field.
//
// Invariants, each load-bearing:
//
//	P-1  One live *BrowserCoordinator per key, and it owns exactly one Chrome
//	     process and one profile directory. Never two for one key; never one
//	     for two keys.
//	P-2  pool.mu is NEVER held across a launch, a CDP call, a Close or a
//	     filesystem walk. Launches serialise per key on a channel, not on the
//	     pool lock, so a cold Chrome-for-Testing download in workspace A does
//	     not stall workspace B's tab open.
//	P-3  The pool never launches past the admission gate — not even by one.
//	     Every path to a launch goes through admitLaunchLocked.
//	P-4  Eviction never touches an instance with a live viewer or an in-flight
//	     browser call, and never deletes a profile directory.
//	P-5  Only workspace deletion deletes a profile directory (FR-043a).
//	     Idle close, eviction, roster change, reload and crash recovery all
//	     leave it on disk — that is what makes a login survive them.
//	P-6  A refusal names MEMORY and a remedy that exists. It never names a cap
//	     or a config key, because there is none to raise.
//
// Lock order where more than one lock is involved: writeLease -> pool.mu ->
// manager.mu. Nothing in this file takes a manager lock while holding pool.mu.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// PerBrowserCostBytes is the launch-headroom minimum: the pool refuses to
// start another Chrome unless at least this much memory is available.
//
// ⚠ THIS IS AN ASSUMPTION, NOT A MEASUREMENT OF THE HOST YOU ARE ON.
//
// Provenance, in full, because a figure quoted without it reads as measured:
//
//	value    ~182 MB
//	host     ONE machine, ONE snapshot — an Apple-silicon macOS laptop
//	how      top's PHYSICAL FOOTPRINT column, summed over the Chrome
//	         process tree
//	what     Chrome for Testing, freshly launched, IDLE, one blank tab
//	         and NOT CAPTURING
//
// It is therefore a LOWER BOUND and every arithmetic that consumes it must be
// conservative in that direction. A capturing instance additionally runs the
// injected encoder extension and a video encode loop, and that delta has
// never been measured. Gate G-1 — the marginal PSS cost of a SECOND Chrome on
// Linux WITH capture running — was deferred by the operator on 2026-09-02
// (ADR-072 D1.13: "we work with the data we have as assumption, after
// everything is proven working, you can run the measurement on a Fly Linux
// machine yourself"). Until that runs, treat this as the floor below which a
// launch is certainly unwise, not as the cost of a launch.
//
// If you measure it: report PSS, not RSS. On the measured box RSS over-counts
// by 2.6x (1118 MB RSS against 434 MB PSS) because Chrome's processes share
// enormous read-only mappings, and summing RSS over a process tree charges
// every one of them once per process.
//
// There is deliberately no per-renderer and no per-tab byte constant anywhere
// in this package, and --renderer-process-limit appears in no launch flag
// (FR-062). A tab's cost is not a number anyone can name, which is the whole
// reason ADR-072 D1.5a deleted every tab counter in favour of asking the host
// what it actually has left.
const PerBrowserCostBytes uint64 = 182 << 20

// defaultIdleCloseTTL is how long a workspace's Chrome may sit with zero tabs,
// zero live viewers and no call in flight before the whole process is closed
// (FR-040). The profile directory survives, so the next launch is logged in.
//
// ⚠ ASSUMPTION. 15 minutes is 3x the shipped per-tab idle_ttl of 5 minutes,
// chosen so that the cheap thing (reaping a tab) always happens well before
// the expensive thing (tearing down a Chrome the user may be about to need
// again). It is not derived from a measurement of relaunch cost: gate G-5,
// which would have measured cold start against a warm profile on disk, is
// deferred (ADR-072 D1.13). Overridable via tools.browser.idle_close_ttl.
const defaultIdleCloseTTL = 15 * time.Minute

// thrashWindow and thrashThreshold drive FR-054's evict-then-reopen detection.
//
// ⚠ ASSUMPTIONS, both of them, and the FR says so: their values were to come
// from gate G-5's cold-start measurement, which is deferred (ADR-072 D1.13).
// They are set deliberately CONSERVATIVELY — a wide window and a high count —
// so the WARN fires only on unmistakable thrash rather than on an install that
// merely opens and closes browsers. A false WARN about memory would send an
// operator hunting a problem they do not have.
const (
	thrashWindow    = 10 * time.Minute
	thrashThreshold = 4
)

// ErrBrowserMemoryRefused is the FR-053 named refusal: the host has no room
// for another browser and nothing could be evicted to make room.
//
// It names MEMORY and a remedy that exists. It names no cap and advises
// raising none, deliberately — there is no limit in this build to raise, and
// telling an operator to look for one sends them after a setting that does not
// exist. Errors.Is-comparable so a caller can branch without string matching.
var ErrBrowserMemoryRefused = errors.New("browser: refused to start another browser — this machine is low on memory")

// errPoolClosed is returned once Shutdown has run.
var errPoolClosed = errors.New("browser: the browser pool is shut down")

// ErrBrowserRestarting is what Register returns when this key's browser was
// torn down in the window between the acquire and the registration — an idle
// close, an eviction or a workspace deletion landing on exactly the instance
// the caller was in the middle of attaching to.
//
// It exists because the alternative that used to ship here was silent and
// permanent. Register noticed the instance was no longer live, skipped its
// bookkeeping, and then returned that instance's coordinator anyway with a
// nil error. The caller latched the dead coordinator's root context onto
// itself and marked itself started; because the bookkeeping had been skipped,
// the instance's mgrs set never contained it, so closeInstance's
// invalidateConnection sweep could not reach it and NOTHING ever reset it.
// Every browser call for that workspace then failed with "context canceled"
// until the gateway was restarted.
//
// A refusal is recoverable where that was not: the manager never sets
// m.started, so its very next tool call runs ensureStarted again, re-registers
// against the relaunched browser and succeeds. One failed call beats a
// workspace that cannot browse until someone restarts the process.
//
// errors.Is-comparable so a caller can branch on "retry me" without matching
// prose.
var ErrBrowserRestarting = errors.New("browser: this workspace's browser was closed while this call was connecting to it — retry")

// memoryRefusedError wraps ErrBrowserMemoryRefused with the workspace whose browser
// was refused, so a log line or an agent-visible message says WHICH workspace
// could not start rather than only that something could not.
type memoryRefusedError struct {
	key BrowsingKey
}

func (e memoryRefusedError) Error() string {
	return fmt.Sprintf(
		"browser: this machine is low on memory, so workspace %s cannot start a browser right now. "+
			"Close a live browser panel or a tab in another workspace, or free memory on this machine, and retry",
		e.key.WorkspaceID(),
	)
}

func (e memoryRefusedError) Unwrap() error { return ErrBrowserMemoryRefused }

// ReasonMemoryPressure is the FR-063 reason code a refusal carries so the
// model branches on a code rather than parsing prose. It is the SAME string
// the tab-open gate uses (tabAdoptReasonMemoryPressure), on purpose: one
// constraint, one code.
const ReasonMemoryPressure = "memory_pressure"

// chromeInstance is one key's live browser: one Chrome process, one profile
// directory, one coordinator.
//
// Field ownership, since FR-041's crash containment and FR-040a's
// "pool entry gone, *BrowserManager retained" both depend on the split being
// exact:
//
//   - coord owns rootCtx/rootCancel/cmd/lockFile and the Chrome process. The
//     pool never reaches inside it; it calls WarmUp/Shutdown/PID/Register.
//   - mgrs are the BrowserManagers currently driving this instance. The pool
//     holds them ONLY to ask Viewers()/InFlight() before evicting and to
//     invalidate their connections after a close. Closing an instance does
//     not destroy its managers — they are retained and re-register on their
//     next tool call, which is what makes idle close invisible to the agent.
type chromeInstance struct {
	key        BrowsingKey
	coord      *BrowserCoordinator
	profileDir string

	// lastUsed is stamped by every Acquire. It orders LRU eviction and, with
	// the emptiness checks, decides idle close.
	lastUsed time.Time

	// mgrs is keyed by manager pointer identity. A reload installs a new
	// manager for the same key; the old one is dropped by Release.
	mgrs map[*BrowserManager]struct{}
}

// BrowserPool owns every workspace's Chrome for one gateway. One instance
// lives on *AgentLoop, so it survives ReloadProviderAndConfig exactly as the
// single coordinator used to.
type BrowserPool struct {
	homeDir string

	mu sync.Mutex

	// cfg is the TEMPLATE. Its ProfileDir names the profile ROOT's default
	// entry (…/browser/profiles/default); per-key directories are its FLAT
	// SIBLINGS (…/browser/profiles/ws-<id>), never nested beneath it — which
	// is what keeps InstallRootForProfileDir resolving every key to the ONE
	// managed-Chromium install root (FR-037a). Nesting would give each
	// workspace its own 130 MB Chrome download.
	cfg BrowserConfig

	instances map[string]*chromeInstance

	// launching is the per-key single-flight. A key's entry exists while a
	// launch is in progress; waiters block on the channel with pool.mu
	// RELEASED (P-2).
	launching map[string]chan struct{}

	closed bool

	// --- seams. Production leaves every one of these nil/default. ---

	// newCoordinator builds the per-key coordinator. A test replaces it to
	// inject a fake pipeLauncher and never spawn real Chrome.
	newCoordinator func(homeDir string, cfg BrowserConfig, key BrowsingKey) *BrowserCoordinator

	// availableMemory is the launch gate's reader. Defaults to
	// config.AvailableMemoryBytes — the ONE shared accessor. A test replaces
	// it to drive the gate deterministically, which is the only way to prove
	// the gate is not a no-op (D1.5b).
	availableMemory func() (uint64, bool)

	now func() time.Time

	// idleCloseTTL is tools.browser.idle_close_ttl, reload-applied.
	idleCloseTTL time.Duration

	// cacheTrimInterval is tools.browser.cache_trim_interval, reload-applied:
	// how often CLOSED profiles are swept (FR-072 trigger 3). It does not
	// bound an open profile — see logUnboundedContinuousDriveOnce.
	cacheTrimInterval time.Duration

	// trimResidualLogged latches FR-074's operator-visible line.
	trimResidualLogged bool

	// --- FR-054 thrash bookkeeping ---
	reopens      map[string][]time.Time
	thrashWarned map[string]bool

	// unmeasurableLogged makes FR-065's "availability cannot be determined"
	// line fire ONCE per process rather than once per acquire.
	unmeasurableLogged bool

	// registerRaceHook is a TEST SEAM, nil in production. Register invokes it
	// after the coordinator registration returns and BEFORE the pool re-checks
	// that the instance it registered against is still this key's live one.
	//
	// It exists because that window is the entirety of the ErrBrowserRestarting
	// defect and it is a few instructions wide. A test that tried to hit it by
	// racing goroutines would be proving its own timing, not the guard; with
	// the seam the close lands inside the window every single run.
	registerRaceHook func()
}

// NewBrowserPool constructs the pool. homeDir is $OMNIPUS_HOME (per-key
// ownership markers land at <homeDir>/browser/ws-<id>.pid); cfg is the
// template whose ProfileDir names the profile root.
func NewBrowserPool(homeDir string, cfg BrowserConfig) *BrowserPool {
	p := &BrowserPool{
		homeDir:           homeDir,
		cfg:               cfg,
		instances:         make(map[string]*chromeInstance),
		launching:         make(map[string]chan struct{}),
		reopens:           make(map[string][]time.Time),
		thrashWarned:      make(map[string]bool),
		idleCloseTTL:      cfg.IdleCloseTTL,
		cacheTrimInterval: cfg.CacheTrimInterval,
	}
	if p.idleCloseTTL <= 0 {
		p.idleCloseTTL = defaultIdleCloseTTL
	}
	if p.cacheTrimInterval <= 0 {
		p.cacheTrimInterval = defaultCacheTrimInterval
	}
	logger.InfoCF("browser", "browser pool ready — one Chrome per workspace, bounded by live memory", map[string]any{
		"profile_root":   p.profileRoot(),
		"idle_close_ttl": p.idleCloseTTL.String(),
		"launch_floor":   fmt.Sprintf("%d MB (assumed, macOS idle non-capturing — see PerBrowserCostBytes)", PerBrowserCostBytes>>20),
	})
	return p
}

// ApplyRuntimeConfig applies a reloaded config to the pool and to every live
// instance's coordinator. Launch-time properties of an already-running Chrome
// (headless, exec_path, profile_dir) are the coordinator's business to
// warn about; the pool's own reload-applied key is idle_close_ttl.
func (p *BrowserPool) ApplyRuntimeConfig(newCfg BrowserConfig) {
	ttl := newCfg.IdleCloseTTL
	if ttl <= 0 {
		ttl = defaultIdleCloseTTL
	}
	trimEvery := newCfg.CacheTrimInterval
	if trimEvery <= 0 {
		trimEvery = defaultCacheTrimInterval
	}
	p.mu.Lock()
	p.cfg = newCfg
	p.idleCloseTTL = ttl
	p.cacheTrimInterval = trimEvery
	coords := make([]*BrowserCoordinator, 0, len(p.instances))
	for _, inst := range p.instances {
		coords = append(coords, inst.coord)
	}
	p.mu.Unlock()
	for _, c := range coords {
		c.ApplyRuntimeConfig(newCfg)
	}
}

// profileRoot is the directory per-key profiles are FLAT SIBLINGS inside.
func (p *BrowserPool) profileRoot() string {
	dir := strings.TrimSpace(p.cfg.ProfileDir)
	if dir == "" {
		return filepath.Join(p.homeDir, "browser", "profiles")
	}
	return filepath.Dir(filepath.Clean(dir))
}

// ProfileDirFor renders a key's profile directory: <profileRoot>/ws-<id>.
//
// The path segment is BrowsingKey.ProfileSegment(), which ResolveBrowsingKey
// already validated as a single, traversal-free path segment — so a key that
// exists is a key whose directory name is safe. This function re-checks
// anyway, because it is the last place between a key and a filesystem call and
// a cheap check there is worth more than a proof somewhere else.
func (p *BrowserPool) ProfileDirFor(key BrowsingKey) (string, error) {
	seg := key.ProfileSegment()
	if seg == "" || seg != filepath.Base(seg) || seg == "." || seg == ".." || strings.ContainsRune(seg, os.PathSeparator) {
		return "", fmt.Errorf("browser: %q is not a usable profile directory name for key %q", seg, key.String())
	}
	return filepath.Join(p.profileRoot(), seg), nil
}

// markerPathFor is the per-key ownership marker: <homeDir>/browser/ws-<id>.pid.
// It replaces the single shared-chrome.pid — with N Chromes there is no one
// pid to record, and a marker naming the wrong process is worse than none.
func (p *BrowserPool) markerPathFor(key BrowsingKey) string {
	return filepath.Join(p.homeDir, "browser", key.ProfileSegment()+".pid")
}

// configFor derives a key's own BrowserConfig from the template. The ONLY
// field that differs is ProfileDir — and that single substitution is what
// gives the workspace its own --user-data-dir, its own Chrome HOME/XDG dirs,
// its own launch lock and its own cookie jar, because every one of those is
// already derived from cfg.ProfileDir downstream (exec_resolver.go's
// managedExecAllocatorOpts, coordinator.go's lockPath/launchChrome).
//
// InstallRootForProfileDir is DELIBERATELY unaffected: it resolves
// dirname(profileDir)/../chromium, and a flat sibling has the same dirname as
// the default profile, so N keys share ONE managed-Chromium install (FR-037a).
func (p *BrowserPool) configFor(key BrowsingKey) (BrowserConfig, error) {
	dir, err := p.ProfileDirFor(key)
	if err != nil {
		return BrowserConfig{}, err
	}
	cfg := p.cfg
	cfg.ProfileDir = dir
	return cfg, nil
}

func (p *BrowserPool) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *BrowserPool) readAvailableMemory() (uint64, bool) {
	if p.availableMemory != nil {
		return p.availableMemory()
	}
	return config.AvailableMemoryBytes()
}

// LiveKeys returns the keys with a live Chrome right now, sorted, for logs and
// for the FR-082 host floor.
func (p *BrowserPool) LiveKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.instances))
	for k := range p.instances {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PID returns the pid of key's Chrome, or 0 when it has none.
func (p *BrowserPool) PID(key BrowsingKey) int {
	p.mu.Lock()
	inst := p.instances[key.String()]
	p.mu.Unlock()
	if inst == nil {
		return 0
	}
	return inst.coord.PID()
}

// admitLaunchLocked is THE launch gate (FR-057). Must be called with p.mu held.
//
// It is a HARD stop, never a hint, and it is the only admission control there
// is at this level — every tab counter was deleted (D1.5a), so if this returns
// true when it should not, nothing else is watching.
//
// Three cases, and the third is the one that gets designed wrong:
//
//	measured, enough headroom       admit
//	measured, not enough headroom   refuse (caller evicts and re-asks)
//	NOT MEASURABLE AT ALL           admit exactly ONE browser per HOST, then
//	                                refuse (FR-065 + FR-082)
//
// The unmeasurable branch refuses to GROW, never to RUN. A floor of zero would
// remove browsing entirely from /proc-less deployments this project supports
// (gVisor, GKE Sandbox) on the strength of a reading the host declines to give.
// A floor of "one per key" would be no floor at all, since keys are unbounded —
// it is one per HOST, i.e. len(p.instances) == 0.
func (p *BrowserPool) admitLaunchLocked() (admit, measured bool) {
	avail, ok := p.readAvailableMemory()
	if !ok {
		if !p.unmeasurableLogged {
			p.unmeasurableLogged = true
			logger.WarnCF(
				"browser",
				"memory availability cannot be determined on this host — the browser pool will run ONE browser and refuse to start a second",
				map[string]any{"live_browsers": len(p.instances)},
			)
		}
		return len(p.instances) == 0, false
	}
	return avail >= PerBrowserCostBytes, true
}

// evictableLocked picks the least-recently-used instance that may be evicted,
// or nil. Must be called with p.mu held.
//
// Two guards, both absolute (FR-050, P-4):
//
//	a LIVE VIEWER      somebody is watching this browser right now. Closing it
//	                   blanks their panel to make room for a workspace they are
//	                   not looking at.
//	an IN-FLIGHT CALL  a browser_* tool is mid-execution against this Chrome.
//	                   Killing it turns a working call into an inexplicable
//	                   error inside an agent's turn.
//
// The in-flight read happens under the SAME p.mu that a call's own increment
// takes (FR-051), so a call that starts during selection is either seen here
// or lands on an instance this pass has already declined to evict.
func (p *BrowserPool) evictableLocked() *chromeInstance {
	var best *chromeInstance
	for _, inst := range p.instances {
		if inst.pinned() {
			continue
		}
		if best == nil || inst.lastUsed.Before(best.lastUsed) {
			best = inst
		}
	}
	return best
}

// pinned reports whether this instance may not be evicted or idle-closed.
func (inst *chromeInstance) pinned() bool {
	for m := range inst.mgrs {
		if m == nil {
			continue
		}
		if m.Viewers() > 0 || m.InFlight() > 0 {
			return true
		}
	}
	return false
}

// idle reports whether this instance has nothing left to do: no tabs, no live
// viewer, no call in flight (FR-040).
func (inst *chromeInstance) idle() bool {
	if inst.pinned() {
		return false
	}
	for m := range inst.mgrs {
		if m != nil && m.TotalOpenTabs() > 0 {
			return false
		}
	}
	return true
}

// Register is the manager-facing entry point: resolve (launching if needed)
// key's Chrome, register mgr against it, and hand back the coordinator plus
// its shared root context.
//
// Every browser tool call reaches Chrome through here, so this is where the
// launch gate sits. There is no second path to a launch.
func (p *BrowserPool) Register(
	ctx context.Context,
	key BrowsingKey,
	mgr *BrowserManager,
) (*BrowserCoordinator, context.Context, error) {
	if key.IsZero() {
		return nil, nil, ErrNoBrowsingContext
	}
	if mgr == nil {
		return nil, nil, fmt.Errorf("browser: pool.Register requires a non-nil manager")
	}
	inst, err := p.Acquire(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	rootCtx, regErr := inst.coord.Register(ctx, key.String(), mgr)
	if regErr != nil {
		return nil, nil, regErr
	}
	if p.registerRaceHook != nil {
		p.registerRaceHook()
	}
	// The instance may have been closed while we were registering against it.
	// The bookkeeping and the RETURN VALUE must agree about that: recording
	// nothing and then handing back the dead coordinator anyway is what left a
	// workspace permanently broken, because a manager the mgrs set never knew
	// about is a manager closeInstance can never invalidate. See
	// ErrBrowserRestarting.
	p.mu.Lock()
	live, ok := p.instances[key.String()]
	stillLive := ok && live == inst
	if stillLive {
		inst.mgrs[mgr] = struct{}{}
	}
	p.mu.Unlock()
	if !stillLive {
		logger.InfoCF("browser", "this workspace's browser was closed while a call was connecting to it — asking the caller to retry", map[string]any{
			"workspace": key.WorkspaceID(),
		})
		return nil, nil, ErrBrowserRestarting
	}
	return inst.coord, rootCtx, nil
}

// Acquire returns key's live Chrome, launching one if the admission gate
// allows. It is the ONLY constructor of a chromeInstance.
//
// The gate/evict/wait ladder, in order:
//
//  1. Already live? Stamp lastUsed and return. No gate — this browser is
//     already paid for, and gating an EXISTING browser would make a
//     workspace unusable the moment the host got busy.
//  2. Gate refuses? Evict the least-recently-used EVICTABLE instance and
//     RE-ASK. Nothing surfaces to the agent or the operator: an eviction that
//     succeeds is invisible, and the evicted profile survives on disk so its
//     workspace is still logged in when it comes back.
//  3. Gate still refuses and nothing is evictable? WAIT — memory frees up on
//     its own all the time — to the caller's own deadline, then fail with
//     ErrBrowserMemoryRefused. Never launch anyway, not even by one.
func (p *BrowserPool) Acquire(ctx context.Context, key BrowsingKey) (*chromeInstance, error) {
	if key.IsZero() {
		return nil, ErrNoBrowsingContext
	}
	id := key.String()

	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, errPoolClosed
		}
		if inst, ok := p.instances[id]; ok && inst != nil {
			inst.lastUsed = p.clock()
			p.mu.Unlock()
			return inst, nil
		}
		// Another goroutine is launching this same key. Wait for it with
		// p.mu RELEASED (P-2), then re-loop — the winner will have installed
		// the instance, or failed and left the key launchable again.
		if wait, ok := p.launching[id]; ok {
			p.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// P-3: the gate, and nothing gets past it.
		admit, measured := p.admitLaunchLocked()
		if !admit {
			// EVICTION IS A RESPONSE TO A MEASURED REFUSAL, and only to one.
			//
			// On a host whose memory cannot be read at all, closing one
			// workspace's browser to open another's is a guess dressed up as
			// a policy: there is no evidence the eviction frees enough, or
			// that anything was short in the first place. FR-082's answer for
			// that host is a floor of ONE browser and a refusal past it —
			// refuse to GROW, never refuse to RUN — so this skips eviction
			// entirely and falls through to the named memory refusal.
			var victim *chromeInstance
			if measured {
				victim = p.evictableLocked()
			}
			if victim != nil {
				p.mu.Unlock()
				p.closeInstance(victim, "evicted to make room for another workspace's browser")
				p.noteReopen(victim.key)
				continue
			}
			p.mu.Unlock()
			if waitErr := p.waitForHeadroom(ctx, measured); waitErr != nil {
				logger.WarnCF("browser", "refused to start a browser — no memory and nothing evictable", map[string]any{
					"workspace":     key.WorkspaceID(),
					"live_browsers": len(p.LiveKeys()),
					"reason":        ReasonMemoryPressure,
				})
				return nil, memoryRefusedError{key: key}
			}
			continue
		}

		// We own the launch for this key.
		done := make(chan struct{})
		p.launching[id] = done
		cfg, cfgErr := p.configFor(key)
		p.mu.Unlock()

		if cfgErr != nil {
			p.finishLaunch(id, done)
			return nil, cfgErr
		}

		inst, launchErr := p.launch(ctx, key, cfg)
		p.mu.Lock()
		if launchErr == nil {
			inst.lastUsed = p.clock()
			p.instances[id] = inst
		}
		p.mu.Unlock()
		p.finishLaunch(id, done)
		if launchErr != nil {
			return nil, launchErr
		}
		return inst, nil
	}
}

func (p *BrowserPool) finishLaunch(id string, done chan struct{}) {
	p.mu.Lock()
	if cur, ok := p.launching[id]; ok && cur == done {
		delete(p.launching, id)
	}
	p.mu.Unlock()
	close(done)
}

// launch builds this key's coordinator and brings its Chrome up. Runs with
// p.mu RELEASED — it can resolve a binary, download Chrome-for-Testing and
// block on a CDP handshake for seconds (P-2).
func (p *BrowserPool) launch(ctx context.Context, key BrowsingKey, cfg BrowserConfig) (*chromeInstance, error) {
	if err := os.MkdirAll(cfg.ProfileDir, 0o700); err != nil {
		return nil, fmt.Errorf("browser: cannot create profile directory %s: %w", cfg.ProfileDir, err)
	}
	build := p.newCoordinator
	if build == nil {
		build = newKeyedCoordinator
	}
	coord := build(p.homeDir, cfg, key)
	if err := coord.WarmUp(ctx); err != nil {
		return nil, fmt.Errorf("browser: workspace %s could not start its browser: %w", key.WorkspaceID(), err)
	}
	logger.InfoCF("browser", "started this workspace's own browser", map[string]any{
		"workspace":   key.WorkspaceID(),
		"profile_dir": cfg.ProfileDir,
		"pid":         coord.PID(),
	})
	return &chromeInstance{
		key:        key,
		coord:      coord,
		profileDir: cfg.ProfileDir,
		lastUsed:   p.clock(),
		mgrs:       make(map[*BrowserManager]struct{}),
	}, nil
}

// headroomPollInterval is how often the FR-053 wait re-asks the gate. Short
// enough that a freed workspace is noticed promptly, long enough that a
// blocked acquire is not a spin loop.
const headroomPollInterval = 250 * time.Millisecond

// waitForHeadroom blocks until the gate would admit, or the caller's deadline
// passes. It returns nil when headroom appeared and an error when the caller
// gave up — the caller turns that into the named memory refusal.
//
// A caller with no deadline gets one poll and then a refusal, rather than
// blocking forever: an agent turn that hangs is worse than one that is told no.
func (p *BrowserPool) waitForHeadroom(ctx context.Context, measured bool) error {
	// An unmeasurable host will read the same on the next poll as on this one:
	// there is nothing to wait FOR. Waiting anyway would turn every acquire
	// past the floor into a stall for the caller's whole deadline before
	// returning the same refusal.
	if !measured {
		return ErrBrowserMemoryRefused
	}
	retryable := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		admit, _ := p.admitLaunchLocked()
		return admit || p.evictableLocked() != nil
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		if retryable() {
			return nil
		}
		return ErrBrowserMemoryRefused
	}
	ticker := time.NewTicker(headroomPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if retryable() {
				return nil
			}
		}
	}
}

// noteReopen records one evict-then-reopen cycle for FR-054 and emits EXACTLY
// ONE warning per key once the cycles cross the threshold inside the window.
//
// What the warning may and may not say matters more than when it fires. It
// names memory as the binding constraint and gives two remedies that actually
// exist — more host memory, or fewer workspaces browsing at once. It names no
// cap and no config key, because there is none: an operator sent looking for a
// number to raise would not find one, and would reasonably conclude the
// software was lying to them.
func (p *BrowserPool) noteReopen(key BrowsingKey) {
	id := key.String()
	now := p.clock()
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := now.Add(-thrashWindow)
	kept := p.reopens[id][:0]
	for _, t := range p.reopens[id] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	p.reopens[id] = kept
	if len(kept) < thrashThreshold || p.thrashWarned[id] {
		return
	}
	p.thrashWarned[id] = true
	contending := make([]string, 0, len(p.instances))
	for k := range p.instances {
		contending = append(contending, k)
	}
	sort.Strings(contending)
	logger.WarnCF(
		"browser",
		"workspaces are repeatedly evicting each other's browsers — this machine does not have memory for this many at once. "+
			"Give the host more memory, or have fewer workspaces browsing at the same time",
		map[string]any{
			"workspace":   key.WorkspaceID(),
			"contending":  contending,
			"cycles":      len(kept),
			"window":      thrashWindow.String(),
			"constrained": "memory",
		},
	)
}

// Release drops mgr's registration from key's instance without touching the
// Chrome process — the reload path (FR-043: a Settings save must not cost the
// workspace its login).
func (p *BrowserPool) Release(key BrowsingKey, mgr *BrowserManager) {
	p.mu.Lock()
	inst := p.instances[key.String()]
	if inst != nil && mgr != nil {
		delete(inst.mgrs, mgr)
	}
	coord := (*BrowserCoordinator)(nil)
	if inst != nil {
		coord = inst.coord
	}
	p.mu.Unlock()
	if coord != nil {
		coord.Release(key.String())
	}
}

// Close tears down key's Chrome. The profile directory SURVIVES (P-5) — this
// is idle close, eviction, roster change and workspace-deletion's first half,
// and only the last of those may then delete the profile, via DeleteProfile.
//
// Post-close state (FR-040a): the pool entry and the Chrome process are gone;
// every *BrowserManager is RETAINED and simply re-registers on its next tool
// call, which is what makes an idle close invisible to the agent that comes
// back.
func (p *BrowserPool) Close(key BrowsingKey) {
	p.mu.Lock()
	inst := p.instances[key.String()]
	delete(p.instances, key.String())
	p.mu.Unlock()
	if inst == nil {
		return
	}
	p.closeInstance(inst, "closed")
}

// closeInstance performs the teardown with p.mu NOT held (P-2). It removes the
// instance from the map first when called from Close; the eviction path passes
// an instance it has already selected, so it deletes here too — both orders
// end with exactly one Shutdown, because the map delete is idempotent and the
// coordinator's own Shutdown is guarded by its shutdown flag.
func (p *BrowserPool) closeInstance(inst *chromeInstance, why string) {
	p.mu.Lock()
	if cur, ok := p.instances[inst.key.String()]; ok && cur == inst {
		delete(p.instances, inst.key.String())
	}
	mgrs := make([]*BrowserManager, 0, len(inst.mgrs))
	for m := range inst.mgrs {
		mgrs = append(mgrs, m)
	}
	p.mu.Unlock()

	inst.coord.Shutdown()
	// The managers survive the process. Invalidating their connections is what
	// makes their next tool call re-register (and re-launch) instead of
	// driving a dead pipe forever.
	for _, m := range mgrs {
		if m != nil {
			m.invalidateConnection()
		}
	}
	_ = os.Remove(p.markerPathFor(inst.key))
	logger.InfoCF("browser", "closed this workspace's browser (its profile is kept)", map[string]any{
		"workspace":   inst.key.WorkspaceID(),
		"why":         why,
		"profile_dir": inst.profileDir,
	})
	// FR-072 trigger 1, and the primary one: the browser this key owned has
	// just gone away, so its disposable cache is trimmable RIGHT NOW —
	// milliseconds after the close, with no interval to wait for. The
	// scheduled pass exists for profiles closed by something this process did
	// not see (a previous run, another gateway).
	p.logUnboundedContinuousDriveOnce()
	p.TrimProfile(inst.key)
}

// CloseIdle closes every instance that has had nothing to do for longer than
// idle_close_ttl (FR-040). Returns the keys it closed.
//
// The caller is the gateway's existing one-minute sweep, AFTER its
// ReapIdleSessions loop (FR-040a) — that order matters: the per-tab reaper is
// what makes an instance reach zero tabs in the first place, so running whole-
// Chrome close first would always find work still outstanding and never close
// anything. A sweep that can never close anything is the shape of no-op this
// requirement exists to forbid.
func (p *BrowserPool) CloseIdle(now time.Time) []string {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	ttl := p.idleCloseTTL
	var due []*chromeInstance
	for _, inst := range p.instances {
		if now.Sub(inst.lastUsed) < ttl {
			continue
		}
		if !inst.idle() {
			continue
		}
		due = append(due, inst)
	}
	p.mu.Unlock()

	closed := make([]string, 0, len(due))
	for _, inst := range due {
		p.closeInstance(inst, "idle past tools.browser.idle_close_ttl")
		closed = append(closed, inst.key.String())
	}
	sort.Strings(closed)
	return closed
}

// DeleteProfile removes key's profile directory from disk. This is the ONLY
// function in the package that does, and it has exactly ONE legitimate
// trigger: the workspace was DELETED (FR-043a, SC-017).
//
// It refuses while that key still has a live Chrome, because deleting a
// profile out from under a running Chrome races the browser's own writes. The
// caller's contract is Close(key) first, and only once it has RETURNED,
// DeleteProfile(key).
//
// Idle close, eviction, roster change, reload and crash recovery must NEVER
// reach here. Each of them is a case where the workspace still exists and its
// user still expects to be logged in when they come back.
func (p *BrowserPool) DeleteProfile(key BrowsingKey) error {
	p.mu.Lock()
	_, live := p.instances[key.String()]
	p.mu.Unlock()
	if live {
		return fmt.Errorf(
			"browser: refusing to delete workspace %s's profile while its browser is still running — call Close first",
			key.WorkspaceID(),
		)
	}
	dir, err := p.ProfileDirFor(key)
	if err != nil {
		return err
	}
	if rmErr := os.RemoveAll(dir); rmErr != nil {
		return fmt.Errorf("browser: could not delete workspace %s's browser profile: %w", key.WorkspaceID(), rmErr)
	}
	_ = os.Remove(p.markerPathFor(key))
	logger.InfoCF("browser", "deleted a deleted workspace's browser profile", map[string]any{
		"workspace":   key.WorkspaceID(),
		"profile_dir": dir,
	})
	return nil
}

// Preprovision resolves (downloading if necessary) the managed Chromium ONCE
// at boot, with zero live keys (FR-016c).
//
// It replaces the gateway's boot-time range over BrowserManagers(), which
// under a lazy pool is an empty slice at boot — so the warm-up it was supposed
// to perform silently did nothing and every install paid the 30-60s
// Chrome-for-Testing download on a user's first click instead.
//
// It resolves against the TEMPLATE ProfileDir, not any key's, because
// InstallRootForProfileDir is key-independent by construction (FR-037a) and
// there is exactly one install to fetch for all of them.
func (p *BrowserPool) Preprovision(ctx context.Context) (string, error) {
	p.mu.Lock()
	cfg := p.cfg
	p.mu.Unlock()
	var caches execPathCaches
	path, err := caches.resolve(ctx, cfg)
	if err != nil {
		return "", err
	}
	logger.InfoCF("browser", "preprovision resolved the managed Chromium once for every workspace", map[string]any{
		"exec_path":    path,
		"install_root": InstallRootForProfileDir(cfg.ProfileDir),
	})
	return path, nil
}

// CacheTrimInterval is tools.browser.cache_trim_interval's effective value —
// how often the gateway sweeps CLOSED profiles (FR-072 trigger 3).
//
// It is NOT a bound on profile size and the caller must not present it as one
// (FR-074). Nothing is trimmed while a Chrome is live.
func (p *BrowserPool) CacheTrimInterval() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cacheTrimInterval
}

// Shutdown closes every live Chrome. Profiles survive — a gateway restart is
// not a reason for anyone to be logged out.
func (p *BrowserPool) Shutdown() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	all := make([]*chromeInstance, 0, len(p.instances))
	for _, inst := range p.instances {
		all = append(all, inst)
	}
	p.instances = make(map[string]*chromeInstance)
	p.mu.Unlock()
	for _, inst := range all {
		inst.coord.Shutdown()
		_ = os.Remove(p.markerPathFor(inst.key))
	}
}

// --- FR-042a: boot marker reconciliation ------------------------------------

// ReconcileMarkers inspects every <homeDir>/browser/ws-*.pid left behind by a
// previous run and decides, per key, what to do about it.
//
// The discrimination is by the LAUNCH LOCK, not by the marker's pid — this is
// the whole point, and getting it backwards is how a running gateway's Chrome
// gets killed by a second gateway starting up. A pid alone cannot distinguish
// "our Chrome from a crashed run, still alive and orphaned" from "another live
// gateway's Chrome, being actively driven". The lock can:
//
//	lock acquirable + pid dead           stale marker from a crash. Clear it,
//	                                     INFO, terminate nothing.
//	lock acquirable + pid alive          an orphan: our Chrome outlived its
//	  + identity confirmed               gateway and nobody is driving it.
//	                                     Terminate it, WARN.
//	lock HELD + pid alive                another live gateway owns this key.
//	                                     Refuse the key, terminate NOTHING.
//
// Identity confirmation reads /proc/<pid>/exe and is Linux-only. On macOS
// there is no equivalent that does not shell out (which this project forbids
// on security-relevant paths), so an orphan there is CLEARED WITHOUT BEING
// TERMINATED and a WARN says so — killing a pid we cannot identify is how an
// unrelated process dies.
//
// Returns the keys it refused: those must not be launched by this gateway.
func (p *BrowserPool) ReconcileMarkers() []string {
	dir := filepath.Join(p.homeDir, "browser")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var refused []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, browsingProfileSegmentPrefix) || !strings.HasSuffix(name, ".pid") {
			continue
		}
		seg := strings.TrimSuffix(name, ".pid")
		workspaceID := strings.TrimPrefix(seg, browsingProfileSegmentPrefix)
		key, keyErr := newBrowsingKey(workspaceID)
		if keyErr != nil {
			continue
		}
		if p.reconcileOne(key) {
			refused = append(refused, key.String())
		}
	}
	sort.Strings(refused)
	return refused
}

// reconcileOne handles a single marker. Returns true when the key is REFUSED
// because another live gateway holds it.
func (p *BrowserPool) reconcileOne(key BrowsingKey) bool {
	markerPath := p.markerPathFor(key)
	pid, owner, err := readOwnershipMarkerAt(markerPath)
	if err != nil || owner != ownershipMarkerOwner {
		_ = os.Remove(markerPath)
		return false
	}
	profileDir, dirErr := p.ProfileDirFor(key)
	if dirErr != nil {
		return false
	}
	lockPath := filepath.Join(profileDir, launchLockFileName)

	f, acquired, lockErr := acquireLaunchLock(lockPath)
	if lockErr != nil {
		// We could not even test the lock. The conservative answer is to leave
		// everything alone: refusing costs this key its browser until the next
		// boot; terminating on a guess could kill a live gateway's Chrome.
		logger.WarnCF("browser", "could not test a workspace's launch lock at boot — leaving its browser alone", map[string]any{
			"workspace": key.WorkspaceID(),
			"error":     lockErr.Error(),
		})
		return true
	}
	if !acquired {
		if pidAlive(pid) {
			logger.WarnCF(
				"browser",
				"another running gateway owns this workspace's browser — this gateway will not touch it",
				map[string]any{"workspace": key.WorkspaceID(), "pid": pid},
			)
			return true
		}
		// Lock held but the pid is dead: off Unix, flock does not auto-release,
		// so this is a stale lockfile rather than a live owner. Leave it for
		// takeLaunchLock's own stale-clearing path.
		return false
	}
	// We hold the lock, so no other gateway is driving this key.
	defer releaseLaunchLock(f)

	if !pidAlive(pid) {
		logger.InfoCF("browser", "cleared a stale browser marker from a previous run", map[string]any{
			"workspace": key.WorkspaceID(),
			"pid":       pid,
		})
		_ = os.Remove(markerPath)
		return false
	}
	// Alive, and nobody is driving it: an orphan.
	if confirmProcessIsOurChrome(pid) {
		logger.WarnCF("browser", "terminating an orphaned browser left by a previous run", map[string]any{
			"workspace": key.WorkspaceID(),
			"pid":       pid,
		})
		terminatePID(pid)
		_ = os.Remove(markerPath)
		return false
	}
	logger.WarnCF(
		"browser",
		"a previous run's browser marker names a live process this platform cannot identify — clearing the marker WITHOUT terminating anything",
		map[string]any{"workspace": key.WorkspaceID(), "pid": pid, "why": "process identity confirmation is Linux-only"},
	)
	_ = os.Remove(markerPath)
	return false
}
