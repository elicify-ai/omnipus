// gateway_boot_browser.go: Browser warm boot at startup - the warmed tab and warm capture surfaces

package gateway

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	_ "github.com/elicify-ai/omnipus/pkg/channels/dingtalk"
	_ "github.com/elicify-ai/omnipus/pkg/channels/discord"
	_ "github.com/elicify-ai/omnipus/pkg/channels/feishu"
	_ "github.com/elicify-ai/omnipus/pkg/channels/googlechat"
	_ "github.com/elicify-ai/omnipus/pkg/channels/irc"
	_ "github.com/elicify-ai/omnipus/pkg/channels/line"
	_ "github.com/elicify-ai/omnipus/pkg/channels/qq"
	_ "github.com/elicify-ai/omnipus/pkg/channels/slack"
	_ "github.com/elicify-ai/omnipus/pkg/channels/telegram"
	_ "github.com/elicify-ai/omnipus/pkg/channels/wecom"
	_ "github.com/elicify-ai/omnipus/pkg/channels/weixin"
	_ "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browserWarmUpEnabled reports whether RunContextWithOptions' boot-time
// browser warm-up (see the "Boot-time browser WARM-UP" block above) should
// run for cfg, given the current process environment. Extracted as a small,
// pure predicate — no side effects, no coordinator/manager access — so the
// exact decision gateway.go's boot sequence makes is unit-testable without
// booting a full gateway.
//
// cfg.Tools.Browser.WarmAtBoot (default true, see its doc comment on
// BrowserToolConfig) is the operator's opt-out: setting it false keeps browser
// tools fully available and simply defers the Chrome launch to first use.
//
// cfg.Tools.Browser.Enabled (default true) and an empty CDPURL together mean
// "this deployment wants local browser automation" — an empty/remote-CDP or
// explicitly-disabled config must never trigger a local Chrome launch.
//
// OMNIPUS_SKIP_BROWSER_PREPROVISION=1 is an unconditional override: when
// set, this returns false regardless of cfg, matching Preprovision's own
// long-standing "escape hatch a test harness needs" contract (see the boot
// wiring's doc comment for why an integration test harness needs one).
func browserWarmUpEnabled(cfg *config.Config) bool {
	return cfg.Tools.Browser.WarmAtBoot &&
		cfg.Tools.Browser.Enabled &&
		cfg.Tools.Browser.CDPURL == "" &&
		os.Getenv("OMNIPUS_SKIP_BROWSER_PREPROVISION") != "1"
}

// findSharedBrowserCoordinator returns the ONE gateway-scoped
// *browser.BrowserCoordinator shared across every agent's *browser.
// BrowserManager in coordinator mode (nil when none is attached — e.g. every
// agent is configured for remote CDP, or the list is empty). All managers in
// coordinator mode share the exact same coordinator instance
// (pkg/agent/loop.go's registerSharedTools wiring), so the first non-nil
// Coordinator() found is sufficient; there is no need to look further once
// one is found. Extracted from the boot-time warm-up wiring so it is
// unit-testable in isolation with fake managers.
func findSharedBrowserCoordinator(mgrs []*browser.BrowserManager) *browser.BrowserCoordinator {
	for _, mgr := range mgrs {
		if mgr == nil {
			continue
		}
		if coord := mgr.Coordinator(); coord != nil {
			return coord
		}
	}
	return nil
}

// browserWarmTabEnabled reports whether step 1 (warm the first tab) should
// run. AND-ed with browserWarmUpEnabled by construction: warming a tab
// presupposes warming the process, so every existing opt-out — including
// OMNIPUS_SKIP_BROWSER_PREPROVISION=1 and a remote cdp_url — governs this
// too, and an operator who turned warm_at_boot off gets a fully lazy browser
// rather than a half-warmed one.
func browserWarmTabEnabled(cfg *config.Config) bool {
	return browserWarmUpEnabled(cfg) && cfg.Tools.Browser.WarmTabAtBoot
}

// browserWarmCaptureEnabled reports whether step 2 (warm the WebRTC capture)
// should run. Same AND-ing rationale as browserWarmTabEnabled. Note this is
// only the CONFIG half of the decision: the warm-boot path additionally
// re-uses webrtcUnavailableReason — the exact gate a real viewer offer
// applies — so warm-up can never start a capture an offer would have refused.
func browserWarmCaptureEnabled(cfg *config.Config) bool {
	return browserWarmUpEnabled(cfg) && cfg.Tools.Browser.WarmCaptureAtBoot
}

// warmCaptureIdleTimeout is how long a boot-warmed capture may run with no
// viewer ever attached. 0 means "never stop it on idle" (an explicit
// operator choice — see WarmCaptureIdleSec's doc comment), which is why a
// non-positive configured value maps to 0 rather than to the default: an
// operator who writes 0 is opting OUT of the idle stop, not asking for the
// shipped 5 minutes back.
func warmCaptureIdleTimeout(cfg *config.Config) time.Duration {
	if cfg.Tools.Browser.WarmCaptureIdleSec <= 0 {
		return 0
	}
	return time.Duration(cfg.Tools.Browser.WarmCaptureIdleSec) * time.Second
}

// pickWarmBrowserManager chooses the ONE browser whose tab (and, optionally,
// capture) gets warmed at boot: the browser of the workspace the DEFAULT agent
// resolves to (ADR-075 FR-016b). It returns (nil, reason) when there is
// nothing to warm — reason is a short operator-facing sentence, and the caller
// logs it exactly once at INFO.
//
// Why one and not all: each warmed tab is a renderer process (74-268MB RSS
// measured on the UAT box, coordinator.go), and a warmed capture is a
// continuously encoding video pipeline of which one host can only usefully
// serve ONE at a time anyway (ADR-048 condition 2 — handleWebRTCOffer still
// denies a second ACTIVELY-VIEWED capture, now across workspaces). Under
// FR-001 a manager is a WORKSPACE's browser, so warming every manager would
// multiply a whole Chrome process and profile by the workspace count to save
// time on exactly one panel.
//
// Why the DEFAULT AGENT'S RESOLVED WORKSPACE, and no fallback:
//
//   - Selection used to compare agents.defaults.default_agent_id against
//     mgr.AgentID(). After FR-001 that accessor returns the manager's BROWSING
//     KEY ("ws:<id>"), so the comparison could never match again and every
//     boot silently took the lexicographic branch instead. This is the fix for
//     that, and it is why the four selection tests in browser_warmboot_test.go
//     had to change with it.
//   - The old lexicographic fallback is GONE. It was a tie-break over
//     workspaces, and picking one would mean starting a Chrome against one
//     workspace's profile — one particular set of live logins — because it
//     sorted first, with nobody watching and nobody asked. That is the same
//     implicit choice FR-033 refuses at every other resolution point, and a
//     latency optimisation is the weakest possible reason to make it. When the
//     default agent resolves to no workspace we warm NOTHING and say so; the
//     lazy path is a complete fallback and costs one panel one cold open.
//
// Determinism is therefore trivial rather than sorted: there is only ever one
// candidate key, so two boots of the same install — and a macOS and a Linux
// host of it — warm the same browser or none.
func pickWarmBrowserManager(
	cfg *config.Config, home string, mgrs []*browser.BrowserManager,
) (*browser.BrowserManager, string) {
	byKey := make(map[string]*browser.BrowserManager, len(mgrs))
	for _, mgr := range mgrs {
		if mgr == nil {
			continue
		}
		// Two hard requirements, both of which a candidate in the normal
		// coordinator-mode gateway always satisfies:
		//   - a real browsing key, because that is what names the browser to
		//     warm and the zero key is not a browser;
		//   - an attached coordinator, so warming drives the ONE shared
		//     Chrome. A manager with no coordinator falls back to the legacy
		//     one-Chrome-per-manager managed mode (manager.go's ensureStarted),
		//     which would have boot spawn a SECOND Chrome process — the exact
		//     opposite of a cheap warm-up. WebRTC capture requires the
		//     coordinator anyway (defaultEncoderStarter refuses without one).
		key := mgr.BrowsingKey()
		if key.IsZero() || mgr.Coordinator() == nil {
			continue
		}
		if _, dup := byKey[key.String()]; dup {
			continue
		}
		byKey[key.String()] = mgr
	}
	if len(byKey) == 0 {
		return nil, "no workspace has a browser manager yet"
	}

	def := strings.TrimSpace(cfg.Agents.Defaults.DefaultAgentID)
	if def == "" {
		return nil, "no default agent is set (agents.defaults.default_agent_id), " +
			"so there is no one workspace to warm"
	}
	key, err := browser.ResolveBrowsingKeyForAgent(home, def, "")
	if err != nil {
		return nil, fmt.Sprintf(
			"the default agent %q is not on exactly one workspace's team, so it resolves to no "+
				"single browser to warm", def)
	}
	mgr, ok := byKey[key.String()]
	if !ok {
		return nil, fmt.Sprintf(
			"the default agent %q resolves to workspace %q, which has no browser manager yet",
			def, key.WorkspaceID())
	}
	return mgr, ""
}

// warmBootListenerDialTimeout / warmBootListenerRetryDelay pace that wait.
const (
	warmBootListenerDialTimeout = 500 * time.Millisecond
	warmBootListenerRetryDelay  = 100 * time.Millisecond
)

// waitForGatewayListener blocks until this gateway's own HTTP listener accepts
// a loopback TCP connection, ctx is canceled, or budget expires. Reports
// whether the listener actually answered.
//
// Loopback specifically (not cfg.Gateway.Host): both consumers of this wait are
// in-process loopback clients — the warm tab fetches the start page and the
// encoder page dials ws://127.0.0.1:<port>/api/v1/browser/capture-ingest, the
// same hardcoded loopback address handleWebRTCOffer already builds. A gateway
// bound exclusively to a non-loopback address therefore never satisfies this
// wait, and we proceed on the budget instead — the same outcome the capture
// path would reach on its own.
func waitForGatewayListener(ctx context.Context, port int, budget time.Duration) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(budget)
	for {
		conn, err := net.DialTimeout("tcp", addr, warmBootListenerDialTimeout)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(warmBootListenerRetryDelay):
		}
	}
}

// warmCaptureIdleCheckInterval is how often the idle watcher re-reads a warm
// capture's viewer count. Short relative to the (minutes-long) default idle
// timeout so the handover to a real viewer is noticed promptly, and so the
// watcher exits quickly on gateway shutdown.
//
// 1s, not the original 5s (round-2 finding F2): the handover is no longer a
// bookkeeping event the watcher merely notices — it now performs an action on
// the viewer's behalf (warmCaptureAdaptResetMinAge below), and every second of
// detection lag is a second of that action landing later, mid-view, instead of
// during the first moment of a panel that is still settling. A 1s tick that
// reads one atomic counter, for at most the idle window, is not a cost worth
// weighing against that.
const warmCaptureIdleCheckInterval = time.Second

// warmCaptureAdaptResetMinAge is how long a boot-warmed capture must have run
// UNWATCHED before the handover to its first real viewer forces a recapture.
//
// Why the handover forces one at all (round-2 finding F2): the encoder page
// runs a bounded resolution-adaptation loop (captureext/embedded/encoder.js)
// that steps the picture down when the encoder reports it is CPU-limited. A
// boot-warmed capture is a full software encode with NOBODY WATCHING, running
// during the busiest minute of the process's life — gateway boot, Chrome
// launch, extension load — and on a 2-core hosted Linux box it can reach the
// loop's hard floor (a QUARTER of the pixels) within ~8 seconds. Without this,
// the user's first panel open inherits a resolution decided by, and for, no
// one, and needs 20+ seconds of sustained high frame rate to climb back out.
//
// A recapture is the signal, because it is the one the encoder already
// understands — the same control frame a panel resize sends — and because the
// encoder's own carry-over rule (adaptCarryOverIndex) is what interprets it:
// the FIRST rebuild of a capture starts the viewer at full quality, later ones
// keep whatever the loop learned while its evidence is still fresh. The
// gateway does not, and should not, know about scale factors; it knows about
// viewers, which is the thing the encoder cannot see.
//
// Why an age gate rather than "always": a viewer who opens the panel seconds
// after boot — precisely the case the warm-up was built for, and the one that
// measured 6,655ms -> 1,041ms to first frame — cannot have inherited an
// adaptation, because the loop only starts on the PeerConnection's first
// 'connected' transition and needs two further 2s samples before it can step
// at all (~6s at the very earliest). Recapturing there would spend the whole
// win on a rebuild that resets nothing.
//
// This is a SAFETY NET, not the primary mechanism, which is why a gate that
// sits past the earliest possible step is acceptable rather than sloppy: the
// SPA reports its panel geometry on every fresh attach (browserLiveWs.ts's
// sendViewport, ~650ms in), and that already forces the encoder's first
// rebuild — the one adaptCarryOverIndex resets unconditionally. What this
// adds is the GUARANTEE, so a viewer whose geometry happened not to change,
// or a future SPA that stops sending one, cannot silently inherit a
// resolution chosen with nobody watching. The cost of the belt as well as the
// braces is at most one extra capture rebuild (the same brief blip a panel
// resize causes) in the first seconds of an open, and only for a capture that
// has been warming, unwatched, for longer than this.
//
// A var, not a const, purely so the handover test can exercise the rule
// without a 15-second sleep (same pattern as captureIngestWriteTimeout in
// browser_webrtc.go). Never reassigned in production code.
var warmCaptureAdaptResetMinAge = 15 * time.Second

// warmCaptureHandle is the slice of *browser.CaptureSession the idle watcher
// needs. An interface, not the concrete type, so the watcher's stop/handover
// decision is testable without a real Chrome, a real encoder page and a real
// WebRTC relay — the three things that make the capture path otherwise
// untestable off a live host.
type warmCaptureHandle interface {
	ViewerCount() int
	Done() <-chan struct{}
	Stop()
	// ResetAdaptation pushes a browser_capture_control{adapt_reset} frame to
	// the encoder page: restore full quality WITHOUT rebuilding the capture.
	// Used at handover — see warmCaptureAdaptResetMinAge.
	ResetAdaptation(reason string)
}

// watchWarmCaptureIdle stops a boot-warmed capture that no viewer ever came to
// watch, and gets out of the way of one that a viewer DID take over.
//
// The handover matters: CaptureSession's own grace-stop timer is armed by
// RemoveViewer, so it only ever protects a session that HAD a viewer. A capture
// started by boot warm-up has never had one, so nothing in the capture session
// itself would ever stop it — it would encode video for the process's whole
// lifetime. Once a viewer attaches, that ordinary last-detach grace stop owns
// the session and this watcher exits without touching it.
//
// Deliberately NOT via CaptureSession.SetOnStopped: ensureCaptureSession
// already installs the hook that clears the capture registry and notifies
// viewers, and that hook is single-slot — overwriting it here would silently
// break the teardown path for every session.
func watchWarmCaptureIdle(ctx context.Context, cs warmCaptureHandle, idle time.Duration, agentID string) {
	if cs == nil || idle <= 0 {
		return
	}
	startedAt := time.Now()
	deadline := startedAt.Add(idle)
	ticker := time.NewTicker(warmCaptureIdleCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cs.Done():
			// Someone else already stopped it (an offer superseded it, the
			// browser died, shutdown) — nothing to do.
			return
		case <-ticker.C:
			if cs.ViewerCount() > 0 {
				unwatchedFor := time.Since(startedAt)
				// Round-2 F2: hand the viewer a capture that starts from the
				// quality THEY warrant, not one shaped by minutes of
				// unwatched, boot-contended encoding — see
				// warmCaptureAdaptResetMinAge for the full rationale.
				if unwatchedFor >= warmCaptureAdaptResetMinAge {
					// WARN, not INFO: this costs the viewer a brief, visible
					// stream blip in the first seconds of their panel open,
					// and production logs are WARN-only — an INFO line here
					// would leave an operator investigating "the video
					// blinked when I opened the panel" with nothing to find.
					// INFO, not WARN: this no longer costs the viewer a
					// visible blip. It used to force a capture REBUILD, which
					// measured ~17s to first frame against ~4s without it
					// (hosted box, 2026-08-17) and made a long-lived warm
					// capture worse than none. ResetAdaptation keeps the
					// guarantee -- the viewer starts at full quality -- and
					// drops the rebuild.
					logger.InfoCF("browser",
						"boot-warmed capture adopted by a viewer after running unwatched — resetting the encoder's adaptation so the picture starts at full quality; "+
							"the resolution the encoder settled on with nobody watching is not evidence about this viewer",
						map[string]any{
							"agent_id":      agentID,
							"unwatched_for": unwatchedFor.Round(time.Second).String(),
						})
					cs.ResetAdaptation("boot-warmed capture handed to its first viewer")
				} else {
					logger.InfoCF("browser", "boot-warmed capture adopted by a viewer — handing it to the normal grace-stop path",
						map[string]any{"agent_id": agentID, "unwatched_for": unwatchedFor.Round(time.Second).String()})
				}
				return
			}
			if time.Now().Before(deadline) {
				continue
			}
			// WARN for the same reason the start line is (see
			// startBrowserWarmBoot): this is the moment the idle CPU burn
			// ENDS, and an operator who saw the start line must be able to
			// see it end — in a WARN-only production log, an INFO here would
			// leave "is it still encoding?" unanswerable.
			logger.WarnCF("browser", "boot-warmed capture idle-stopped (no viewer ever attached) — CPU released; the next viewer starts a fresh one",
				map[string]any{"agent_id": agentID, "idle": idle.String()})
			cs.Stop()
			return
		}
	}
}

// --- boot-time browser WARM SURFACES (tab + capture) ------------------------
//
// browserWarmUpEnabled above governs step 0 of the warm-up ladder: the Chrome
// PROCESS. Measured on a host whose Chrome binary is already present (the
// Docker-image case, no download), that is genuinely all it does — the gateway
// answered HTTP at 2.6s and Chrome's main process was live at 2.8s, with ZERO
// renderer processes: no browsing context, no tab, no page. The first panel
// open then paid for everything else on the user's own critical path:
// attach 0.4s + tab-on-demand 1.0-2.2s + capture-extension load and WebRTC
// negotiation 1.7-6.7s + first frame 1.2-4.3s, ~9.5s in total, and one run in
// three failed outright with an ingest timeout ("no ingest video track after
// waiting 15s").
//
// Two further steps close that gap, deliberately kept SEPARATELY controllable
// because their cost profiles are nothing alike:
//
//	step 1 — the TAB (tools.browser.warm_tab_at_boot, default true).
//	         One renderer parked on a static local page. Nearly free to keep.
//	step 2 — the CAPTURE (tools.browser.warm_capture_at_boot, default true).
//	         The expensive one: a running capture encodes video continuously.
//	         It therefore stops itself after tools.browser.warm_capture_idle_sec
//	         with no viewer ever attached, and the next offer starts a fresh one.
//
// Every property the process warm-up has is preserved here, because the
// reasons for them are unchanged (Hard Constraint #4, graceful degradation):
// best-effort, non-blocking (a bare goroutine nothing joins), panic-contained,
// audited on failure, and skipped entirely by every existing opt-out —
// tools.browser.warm_at_boot, tools.browser.enabled, a set cdp_url (a remote
// CDP Chrome is not ours to warm) and OMNIPUS_SKIP_BROWSER_PREPROVISION=1 —
// since browserWarmTabEnabled/browserWarmCaptureEnabled are both defined as
// browserWarmUpEnabled AND their own flag. Nothing here can fail boot: the
// caller starts it and moves on.
//
// PARITY (macOS vs Linux): everything above the CDP transport is identical on
// both — same defaults, same config keys, same selection rule, same idle stop,
// same log/audit lines, same recovery (the ordinary lazy path). What differs is
// underneath and invisible from here: macOS encodes in hardware over loopback,
// a hosted Linux box encodes in software over a real network, so the WALL-CLOCK
// saving is larger on Linux than on a laptop. A failure degrades identically on
// both: the warm surface is skipped, one WARN + one audit entry is written, and
// the first open behaves exactly as it did before this existed.

// warmBootListenerBudget bounds how long the warm-boot goroutine waits for the
// gateway's own HTTP listener before giving up and warming anyway.
//
// This wait is load-bearing, not politeness: the start page a warm tab lands on
// is served BY THIS GATEWAY (tools.browser.start_page_url defaults to
// http://localhost:<gateway.port>/browser-start, pkg/agent/loop.go), and the
// capture's encoder page connects back to this gateway's own loopback
// capture-ingest WS. Warming before the listener accepts would leave the warm
// tab parked on about:blank — which on this surface reads as a BROKEN panel,
// indistinguishable from a real capture failure (the exact confusion
// manager.go's navigateNewTabToStartPage exists to avoid) — and would fail the
// capture outright. On expiry we proceed regardless rather than skipping: the
// warm-up is best-effort, and a listener that is late is not a reason to leave
// the first open cold.
const warmBootListenerBudget = 20 * time.Second

// startBrowserWarmBoot kicks off warm-up steps 1 and 2 (see the block comment
// above) on their own goroutine and returns immediately. Call it AFTER the HTTP
// listener is up: both steps need this gateway to be serving (start page /
// capture-ingest WS), which is why they live here rather than alongside the
// process warm-up earlier in boot.
//
// Nothing joins the returned goroutine, exactly like the process warm-up's:
// gateway shutdown must not wait for a browser, and ctx (the gateway's own
// shutdown-aware context) is what stops it early.
func startBrowserWarmBoot(
	ctx context.Context,
	cfg *config.Config,
	homePath string,
	agentLoop *agent.AgentLoop,
	h *BrowserWSHandler,
) {
	if agentLoop == nil {
		return
	}
	wantTab := browserWarmTabEnabled(cfg)
	wantCapture := browserWarmCaptureEnabled(cfg)
	if !wantTab && !wantCapture {
		return
	}
	mgr, skipReason := pickWarmBrowserManager(cfg, homePath, agentLoop.BrowserManagers())
	if mgr == nil {
		// Say so rather than falling through silently — "enabled but nothing
		// to warm" is otherwise indistinguishable from "disabled" and from
		// "still running", the same three-way ambiguity the process warm-up's
		// own no-coordinator branch calls out.
		//
		// ONE line, at INFO (FR-016b). Nothing is wrong here: skipping the
		// warm-up costs the first panel open a cold start and nothing else,
		// and a WARN would tell an operator to fix a configuration that may be
		// exactly what they intended.
		logger.InfoCF("browser",
			"boot-time browser tab/capture warm-up enabled but skipped — "+skipReason+
				"; the first panel open will build the browser lazily",
			nil)
		return
	}

	go func() {
		// A panic here must never take the gateway down with it: this is an
		// optional latency optimisation, and the lazy path is a complete
		// fallback. Same contract as the process warm-up's own recover().
		defer func() {
			if r := recover(); r != nil {
				logger.WarnCF("browser",
					"boot-time browser tab/capture warm-up panicked — recovered; the first panel open will build them lazily",
					map[string]any{"panic": fmt.Sprintf("%v", r)})
				audit.Emit(context.Background(), agentLoop.AuditLogger(),
					audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
					map[string]any{"stage": "warm_boot", "reason": "panic", "error": fmt.Sprintf("%v", r)})
			}
		}()

		agentID := mgr.AgentID()
		if !waitForGatewayListener(ctx, cfg.Gateway.Port, warmBootListenerBudget) {
			if ctx.Err() != nil {
				return
			}
			logger.WarnCF("browser",
				"boot-time browser warm-up: gateway listener did not answer on loopback in time — warming anyway (a warm tab may land on about:blank)",
				map[string]any{"agent_id": agentID, "budget": warmBootListenerBudget.String()})
		}
		if ctx.Err() != nil {
			return
		}

		// Step 1 — the tab. mgr.Session(mgr.OperatorSessionID()) is the SAME
		// call the live panel and the capture's tab resolution make, so this
		// warms the tab they will actually use rather than parking a stray
		// extra one in the shared Chrome (which the encoder's fallback tab
		// resolution could then bind to by mistake).
		//
		// It is the WORKSPACE-OWNED tab set, not an agent's: under ADR-075
		// FR-080 a browser_* tool addresses its own SESSION's tabs, which do
		// not exist until that session browses and cannot be warmed at boot.
		// The panel's tabs can be, and are.
		if wantTab {
			started := time.Now()
			if _, err := mgr.Session(mgr.OperatorSessionID()); err != nil {
				logger.WarnCF("browser",
					"boot-time browser tab warm-up failed — the first panel open will build the tab lazily",
					map[string]any{"agent_id": agentID, "error": err.Error()})
				audit.Emit(context.Background(), agentLoop.AuditLogger(),
					audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
					map[string]any{"stage": "tab", "reason": "error", "agent_id": agentID, "error": err.Error()})
			} else {
				logger.InfoCF("browser", "boot-time browser tab warmed (parked on the start page)",
					map[string]any{"agent_id": agentID, "took": time.Since(started).String()})
			}
		}

		if !wantCapture || h == nil || ctx.Err() != nil {
			return
		}

		// Step 2 — the capture. Refuse for exactly the reasons a real viewer
		// offer would refuse (webrtcUnavailableReason is the shared gate
		// handleWebRTCOffer and announceWebRTCAvailability both use), so
		// warm-up can never start something an offer would not have.
		if reason := webrtcUnavailableReason(cfg, mgr); reason != "" {
			logger.InfoCF("browser", "boot-time capture warm-up skipped — WebRTC capture is unavailable for this agent",
				map[string]any{"agent_id": agentID, "reason": reason})
			return
		}

		// Take the same fence the offer path takes around get-or-create, so a
		// viewer offer racing this warm-up cannot produce two capture sessions
		// for the same agent (ADR-048 condition 2 / the fence's own TOCTOU
		// rationale). Released before Start, which does CDP work — never hold
		// a process-wide mutex across that.
		//
		// The empty panel tab set id is deliberate (issue #671): boot-time
		// warm-up has no viewer and no chat to resolve against, so the capture
		// binds to the operator's workspace-owned set — the same set step 1
		// above just warmed, and the same one this path has always used. A
		// real viewer's offer resolves its own.
		h.captureFenceMu.Lock()
		cs, err := h.ensureCaptureSession(mgr, agentID, "", cfg)
		h.captureFenceMu.Unlock()
		if err != nil {
			logger.WarnCF("browser", "boot-time capture warm-up: could not create the capture session",
				map[string]any{"agent_id": agentID, "error": err.Error()})
			audit.Emit(context.Background(), agentLoop.AuditLogger(),
				audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
				map[string]any{"stage": "capture", "reason": "ensure_failed", "agent_id": agentID, "error": err.Error()})
			return
		}

		started := time.Now()
		ingestURL := fmt.Sprintf("ws://127.0.0.1:%d/api/v1/browser/capture-ingest", cfg.Gateway.Port)
		if _, startErr := cs.Start(ctx, ingestURL); startErr != nil {
			logger.WarnCF("browser",
				"boot-time capture warm-up failed to start — the first viewer will start one the ordinary way",
				map[string]any{"agent_id": agentID, "error": startErr.Error()})
			audit.Emit(context.Background(), agentLoop.AuditLogger(),
				audit.EventBrowserWarmUpFailed, audit.SeverityWarn,
				map[string]any{"stage": "capture", "reason": "start_failed", "agent_id": agentID, "error": startErr.Error()})
			// Stop() is idempotent and its onStopped hook (installed by
			// ensureCaptureSession) clears BOTH the manager's reference and the
			// token registry, so the next offer builds a fresh session instead
			// of reusing this permanently-broken one — the same recovery
			// handleWebRTCOffer performs on a failed Start.
			cs.Stop()
			return
		}
		idle := warmCaptureIdleTimeout(cfg)
		// WARN, deliberately, for a SUCCESSFUL step (round-2 finding F2).
		// This line is the only disclosure an operator gets that their box is
		// now software-encoding video for a viewer who does not exist —
		// measured at ~26% of one core for the whole idle window on macOS,
		// and more on a hosted Linux box with no hardware encoder. Every
		// other warm-boot line is INFO, and this project's production logs
		// are WARN-only, so at INFO the entire feature (and its cost) is
		// invisible in exactly the deployment where it is most expensive.
		// Bounded: this fires at most once per boot, and only when
		// tools.browser.warm_capture_at_boot is on. It names both dials so
		// the line itself is the fix.
		//
		// A warm capture that is NEVER idle-stopped (idle_sec 0, an explicit
		// operator opt-out) burns that core for the process's whole lifetime,
		// so it says so instead of printing "0s".
		idleDesc := idle.String()
		if idle <= 0 {
			idleDesc = "never (tools.browser.warm_capture_idle_sec=0)"
		}
		logger.WarnCF("browser",
			"boot-time capture warm-up started: Chrome is now encoding this agent's tab with NO viewer attached, to make the first panel open fast. "+
				"Cost ~1/4 of a CPU core until a viewer attaches or it idle-stops. Turn it off with tools.browser.warm_capture_at_boot=false, "+
				"or shorten tools.browser.warm_capture_idle_sec.",
			map[string]any{
				"agent_id":     agentID,
				"took":         time.Since(started).String(),
				"idle_stop_in": idleDesc,
			})
		// Note the boundary this leaves, deliberately: with idle-stop turned
		// OFF (warm_capture_idle_sec=0) there is no watcher, so there is no
		// handover recapture either — the F2 belt is absent in exactly the
		// configuration where an unwatched capture runs longest. The braces
		// still hold there: the encoder discards what its FIRST capture
		// learned on that capture's first rebuild (adaptCarryOverIndex), and
		// the SPA forces one by reporting its panel geometry on attach. An
		// operator who opts out of the idle stop gets the same picture, just
		// without the second, gateway-side guarantee.
		if idle > 0 {
			go watchWarmCaptureIdle(ctx, cs, idle, agentID)
		}
	}()
}
