// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// encoder_launch.go — Wave 2 component M (live-browser video streaming, ADR-044;
// docs/internal/specs/live-browser-video-streaming-spec.md R7). This file owns
// the CDP-driven launch of the controlled WebCodecs *encoder page* as a tab in
// the coordinator's SHARED full-Chrome process (rootCtx here is the coordinator
// root context — pkg/tools/browser/coordinator.go RootContext(), fed in by the
// stream orchestrator in pkg/gateway/browser_stream.go). That single Chrome
// ALSO hosts the per-agent browsing tabs, each in its OWN named browser context
// (ADR-043 isolation); the encoder tab lands in the process's DEFAULT browser
// context, which no agent uses. LaunchEncoderPage opens that default-context
// tab, injects the per-stream ingest capability token out-of-band (never via
// URL), grants audio-capture consent SCOPED TO THE ENCODER-PAGE ORIGIN ONLY
// (when audio is requested — phase 2 only), and navigates the tab to the
// loopback encoder-page URL served by the stream orchestrator
// (pkg/gateway/browser_stream.go).
//
// SECURITY POSTURE (FR-016 / MAJ-003 — why the origin must be unguessable):
//
//   - The encoder tab is created in rootCtx's DEFAULT browser context, via a
//     raw target.CreateTarget with no browserContextId — NOT an isolated
//     per-tab browser context (chromedp.WithNewBrowserContext's path:
//     CreateTarget().WithBrowserContextID(bid)). A freshly-created browser
//     context on full-Chrome --headless has zero windows, so scoping
//     CreateTarget to one returns CDP -32000 "no browser is open"; the
//     DEFAULT context — the one owning the window Chrome opens at process
//     launch — has no such problem and is what this file uses.
//   - PRIMARY DEFENSE = the unguessable encoder-page origin, NOT process
//     isolation. rootCtx here is the coordinator's SHARED Chrome (ADR-044
//     single-Chrome amendment, 2026-07-17): the encoder tab lives in that
//     process's DEFAULT browser context while every per-agent browsing tab
//     runs in its OWN named browser context (ADR-043) in the SAME process. So
//     the original FR-016 threat ("an agent tab navigates to the encoder
//     origin and inherits the audio-capture grant") is NO LONGER structurally
//     impossible — an agent tab now shares this process and could reach the
//     origin IF it learned it. It is re-secured by the orchestrator serving
//     the encoder page on an UNGUESSABLE loopback origin — an OS-assigned
//     random port on 127.0.0.1 PLUS a random secret path — which an agent tab
//     can neither guess nor enumerate. This file relies on the orchestrator
//     having minted that unguessable Origin/EncoderURL; it does not itself
//     pick the origin.
//   - PHASE 1 HAS NO GRANT TO INHERIT. Phase 1 is video-only (cfg.HasAudio is
//     always false — ClassifyVideoCapability returns video-only; capture is
//     server-driven CDP Page.startScreencast with no page-callable API), so NO
//     media permission is granted at all — there is no consent surface for any
//     tab, agent or encoder, to inherit. The audio grant is a phase-2 concern
//     only; when it ships it MUST be scoped to the encoder tab's DEFAULT
//     browser context (see the HasAudio branch in LaunchEncoderPage), PRECISELY
//     because agent tabs now share the process.
//   - The per-stream token is injected via Page.addScriptToEvaluateOnNewDocument
//     so it runs BEFORE the encoder page's own script and is never present in
//     the URL, query string, or the embedded page bytes (FR-013).
//   - No process-global media flag is used: --use-fake-ui-for-media-stream is
//     FORBIDDEN (C-2/P-6). Video capture is CDP Page.startScreencast (component
//     L) which has no page-callable API at all; the ONLY consent surface is
//     the phase-2 per-origin audio grant, and only when audio is requested.

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// audioPermissionName is the W3C Permissions API descriptor name for
// audio (getUserMedia({audio:true})) capture. The encoder page captures Opus
// audio off the PulseAudio sink monitor via getUserMedia; granting this,
// origin-scoped, is the SOLE media-consent surface (FR-016/FR-023).
const audioPermissionName = "microphone"

// encoderLaunchTimeout bounds the CDP round trips LaunchEncoderPage issues
// (target bring-up, permission grant, script injection, navigate). The tab
// context itself outlives this bound — only the launch handshake is bounded so
// a wedged CDP transport cannot hang the orchestrator's cold-start path.
const encoderLaunchTimeout = 15 * time.Second

// EncoderLaunchCfg carries everything LaunchEncoderPage needs to bring up one
// encoder page for one stream. All string fields except AudioCodec are
// required.
type EncoderLaunchCfg struct {
	// Token is the per-stream ingest capability token (FR-013), injected into
	// the live page out-of-band via CDP addScriptToEvaluateOnNewDocument —
	// never via URL.
	Token string
	// WSURL is the loopback capture-ingest WebSocket URL the encoder page
	// connects back to (ws://127.0.0.1:<gateway-port>/api/v1/browser/capture-ingest).
	WSURL string
	// StreamID is the unguessable per-stream key this encoder registers chunks
	// for (never the human tab title, FR-016).
	StreamID string
	// VideoCodec is the orchestrator-negotiated target video codec hint (e.g.
	// "avc1.4D401E" or "vp8"). The encoder page independently probes WebCodecs
	// and reports the codec it actually produces in browser_ingest_init; this
	// hint documents intent and lets a future encoder honor a forced codec.
	VideoCodec string
	// HasAudio requests audio capture for this stream. When true, and only
	// then, LaunchEncoderPage grants audio-capture permission scoped to Origin
	// (below). When false, NO media grant is issued, so the encoder page's
	// getUserMedia(audio) fails and the stream is video-only — the grant is the
	// real enforcement of the audio decision, not a page-side hint.
	HasAudio bool
	// AudioCodec is the negotiated audio codec (e.g. "opus"); informational.
	AudioCodec string
	// EncoderURL is the FULL loopback URL (unguessable random-port origin +
	// secret path) to navigate the encoder tab to (served by the orchestrator's
	// per-stream loopback listener from encoderpage.Handler()).
	EncoderURL string
	// Origin is the encoder-page origin (scheme://host:port) the audio-capture
	// grant is scoped to. MUST be the unguessable random-port loopback origin
	// (FR-016) so the agent cannot navigate to it and inherit the grant.
	Origin string
	// Framerate / KeyframeInterval are optional encoder tuning hints forwarded
	// to the page (it defaults them if <= 0). PanelWidth/Height are NOT sent —
	// the encoder page always derives real dimensions from the first decoded
	// frame, never trusting init (encoder.html's documented contract).
	Framerate        int
	KeyframeInterval int
}

func (c EncoderLaunchCfg) validate() error {
	switch {
	case c.Token == "":
		return errors.New("encoder launch: Token is required")
	case c.WSURL == "":
		return errors.New("encoder launch: WSURL is required")
	case c.StreamID == "":
		return errors.New("encoder launch: StreamID is required")
	case c.VideoCodec == "":
		return errors.New("encoder launch: VideoCodec is required")
	case c.EncoderURL == "":
		return errors.New("encoder launch: EncoderURL is required")
	case c.Origin == "":
		return errors.New("encoder launch: Origin is required")
	}
	return nil
}

// EncoderTab is a live encoder-page tab. Done fires when the tab's target
// closes (encoder-page crash) or Close is called — the orchestrator watches it
// to drive the CRIT-002 re-mint + relaunch. Close tears the tab down.
type EncoderTab struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// Done reports the encoder tab's death. It closes when the underlying CDP
// target/context ends — either the encoder page crashed (which, for the
// gateway-owned loopback page, is precisely the "ingest drop" the orchestrator
// recovers from by re-minting + relaunching, CRIT-002) or Close was called.
func (t *EncoderTab) Done() <-chan struct{} {
	if t == nil || t.ctx == nil {
		// A nil/zero tab is treated as already-done so a caller that watches
		// Done() on a failed launch never blocks forever.
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return t.ctx.Done()
}

// Close tears down the encoder tab (cancels its CDP context, which closes the
// target). Idempotent.
func (t *EncoderTab) Close() error {
	if t == nil || t.cancel == nil {
		return nil
	}
	t.cancel()
	return nil
}

// LaunchEncoderPage opens the controlled encoder page for one stream inside
// rootCtx — the coordinator's SHARED full-Chrome process (rootCtx here is the
// coordinator root context — see pkg/tools/browser/coordinator.go RootContext(),
// fed in by the stream orchestrator in pkg/gateway/browser_stream.go). That
// same Chrome also hosts the per-agent browsing tabs in their own named browser
// contexts; the encoder tab lands in the DEFAULT context, which no agent uses.
// It:
//
//  1. opens a new tab in rootCtx's DEFAULT browser context via a raw
//     target.CreateTarget with no browserContextId (see the SECURITY POSTURE
//     note above for why an isolated per-tab browser context does not work
//     against full-Chrome --headless),
//  2. (only when cfg.HasAudio — phase 2) grants audio-capture consent scoped to
//     cfg.Origin, the unguessable encoder-page origin, AND to the encoder tab's
//     DEFAULT browser context (FR-016); the context scoping is required because
//     agent tabs now share this Chrome process — see the HasAudio branch,
//  3. injects window.__OMNIPUS_INGEST__ (token/wsURL/streamId/…) via
//     Page.addScriptToEvaluateOnNewDocument so it runs before the page's own
//     script and the token is never in the URL (FR-013),
//  4. navigates the tab to cfg.EncoderURL.
//
// On any failure it cancels the tab context and returns an error (the
// orchestrator maps that to the unavailable state, FR-018). No
// --use-fake-ui-for-media-stream is used.
func LaunchEncoderPage(rootCtx context.Context, cfg EncoderLaunchCfg) (*EncoderTab, error) {
	if rootCtx == nil {
		return nil, errors.New("encoder launch: rootCtx is required")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	// Land the encoder tab in rootCtx's DEFAULT browser context rather than a
	// fresh one of its own: chromedp.NewContext(rootCtx,
	// WithNewBrowserContext()) issues CreateTarget().WithBrowserContextID(bid),
	// and a brand-new browser context on full-Chrome --headless has zero
	// windows, so that call returns CDP -32000 "no browser is open". A raw
	// CreateTarget with no browserContextId instead lands in the DEFAULT
	// context — the one owning the window Chrome opened at process launch —
	// which works. The DEFAULT context is also the one no agent uses: the
	// coordinator gives each agent its OWN named browser context (ADR-043) in
	// this SAME shared Chrome, so the encoder tab is separated from agent tabs
	// by browser context, not by process. Confidentiality of the encoder origin
	// (see SECURITY POSTURE above), not process isolation, is what keeps an
	// agent tab away from it — and phase 1 is video-only, so there is no grant
	// to inherit anyway.
	c := chromedp.FromContext(rootCtx)
	if c == nil || c.Browser == nil {
		return nil, errors.New("encoder launch: rootCtx has no live browser")
	}
	// WithBackground(true) is REQUIRED here (single-Chrome collapse regression
	// fix): Target.createTarget defaults background=false ("not supported by
	// headless shell" per cdproto — i.e. it DOES take effect on full Chrome,
	// which every process in this topology now is). A foreground-created
	// target steals the "active"/visible slot from whatever target currently
	// holds it. Before the single-Chrome collapse this was harmless — the
	// encoder lived in its OWN dedicated Chrome process (ADR-044 Option A),
	// so its activation could never affect an agent tab in a DIFFERENT
	// process. Now the encoder tab and every agent tab share ONE Chrome
	// process, and startStreamLocked always creates the encoder tab BEFORE
	// calling StartCapture on the agent tab (browser_stream.go), so an
	// encoder tab created in the foreground backgrounds the agent tab at the
	// exact moment its screencast bring-up begins. A backgrounded target's
	// compositor is throttled/suspended even with
	// --disable-backgrounding-occluded-windows/--disable-renderer-backgrounding
	// set (those flags only cover JS timer throttling and occluded-window
	// renderer priority, not this activation-driven visibility change), so
	// Page.startScreencast never emits a single frame and StartCapture always
	// hits its bring-up timeout. The encoder page itself has no need to be
	// foreground — WebCodecs encoding operates on ImageBitmap/VideoFrame data
	// pushed to it programmatically, not on its own rendered/composited
	// pixels — so creating it in the background is fully correct.
	tid, err := target.CreateTarget("about:blank").
		WithBackground(true).
		Do(cdp.WithExecutor(rootCtx, c.Browser))
	if err != nil {
		return nil, fmt.Errorf("encoder launch: create default-context target: %w", err)
	}
	tabCtx, cancel := chromedp.NewContext(rootCtx, chromedp.WithTargetID(tid))

	// Bound only the launch handshake; the tab context itself stays live.
	bringup, cancelBringup := context.WithTimeout(tabCtx, encoderLaunchTimeout)
	defer cancelBringup()

	// First Run attaches chromedp's Context to the target created above:
	// chromedp.WithTargetID makes newTarget skip CreateTarget entirely and go
	// straight to attachTarget, so this materializes the CDP session for the
	// existing target rather than minting a second one. Reuses err (declared
	// above by the CreateTarget call) rather than := so it doesn't shadow it.
	if err = chromedp.Run(bringup); err != nil {
		cancel()
		return nil, fmt.Errorf("encoder launch: attach encoder target: %w", err)
	}
	// BrowserContextID is empty/ambiguous here on purpose: tabCtx was attached
	// via WithTargetID to a target chromedp did not mint itself, so chromedp
	// never populates BrowserContextID for it (that field is only set when
	// this context creates a new browser context, or inherits one from a
	// parent that has one — rootCtx, the coordinator's shared-Chrome root,
	// has neither). See the phase-2 note in the HasAudio branch below for why
	// that is fine today and what must change once audio ships.
	bid := chromedp.FromContext(tabCtx).BrowserContextID

	script, err := buildIngestBootstrapScript(cfg)
	if err != nil {
		cancel()
		return nil, err
	}

	actions := make([]chromedp.Action, 0, 3)
	if cfg.HasAudio {
		// Origin-scoped audio-capture grant (FR-016). Unreachable in phase 1:
		// cfg.HasAudio is always false (ClassifyVideoCapability returns
		// video-only; see EncoderLaunchCfg.HasAudio). PHASE-2 NOTE (ADR-044
		// single-Chrome amendment, 2026-07-17): the encoder tab shares its
		// Chrome process with the per-agent browsing tabs, which run in their
		// OWN named browser contexts (ADR-043). An agent tab is therefore a
		// DIFFERENT browser context in the SAME process and CAN navigate to
		// cfg.Origin if it ever learns it — so when audio capture ships, the
		// grant MUST stay scoped to the encoder tab's own (DEFAULT) browser
		// context via .WithBrowserContextID; it MUST NOT become a browser-wide
		// grant, which every context — including an agent's — would inherit.
		// (This INVERTS the earlier note, which called for dropping
		// .WithBrowserContextID on the now-false premise that no agent tab
		// could ever share this process.) Implementation caveat: bid above is
		// empty because tabCtx was adopted via WithTargetID (see the
		// BrowserContextID note above); for Browser.setPermission an
		// empty/omitted browserContextId targets the DEFAULT browser context —
		// the encoder tab's context — so an empty bid still scopes to the right
		// place, but a phase-2 implementer MUST verify the grant lands on the
		// default context ONLY and confirm an agent (named-context) tab cannot
		// obtain the audio stream. Do NOT widen this to a browser-global grant.
		actions = append(actions,
			browser.SetPermission(
				&browser.PermissionDescriptor{Name: audioPermissionName},
				browser.PermissionSettingGranted,
			).WithOrigin(cfg.Origin).WithBrowserContextID(bid),
		)
	}
	actions = append(actions,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// addScriptToEvaluateOnNewDocument runs on the NEXT document load
			// (the navigate below), before the encoder page's own <script>.
			_, aerr := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
			return aerr
		}),
		chromedp.Navigate(cfg.EncoderURL),
	)

	if err := chromedp.Run(bringup, actions...); err != nil {
		cancel()
		return nil, fmt.Errorf("encoder launch: bring up encoder page: %w", err)
	}

	logger.InfoCF("browser", "encoder page launched", map[string]any{
		"stream_id":   cfg.StreamID,
		"origin":      cfg.Origin,
		"has_audio":   cfg.HasAudio,
		"video_codec": cfg.VideoCodec,
	})
	return &EncoderTab{ctx: tabCtx, cancel: cancel}, nil
}

// ingestBootstrap is the exact shape injected as window.__OMNIPUS_INGEST__.
// The encoder page REQUIRES token/wsURL/streamId; videoCodec/hasAudio are
// forwarded as intent hints (the page independently probes WebCodecs and
// reports the codec it actually produces). JSON-marshaled so every value is
// safely escaped for injection into a script literal.
type ingestBootstrap struct {
	Token            string `json:"token"`
	WSURL            string `json:"wsURL"`
	StreamID         string `json:"streamId"`
	VideoCodec       string `json:"videoCodec"`
	HasAudio         bool   `json:"hasAudio"`
	AudioCodec       string `json:"audioCodec,omitempty"`
	Framerate        int    `json:"framerate,omitempty"`
	KeyframeInterval int    `json:"keyframeInterval,omitempty"`
}

// buildIngestBootstrapScript builds the JS snippet that sets
// window.__OMNIPUS_INGEST__ before the encoder page's own script runs. Values
// are JSON-encoded (not string-concatenated) so a hostile-looking token can
// never break out of the literal.
func buildIngestBootstrapScript(cfg EncoderLaunchCfg) (string, error) {
	b := ingestBootstrap{
		Token:            cfg.Token,
		WSURL:            cfg.WSURL,
		StreamID:         cfg.StreamID,
		VideoCodec:       cfg.VideoCodec,
		HasAudio:         cfg.HasAudio,
		Framerate:        cfg.Framerate,
		KeyframeInterval: cfg.KeyframeInterval,
	}
	if cfg.HasAudio {
		b.AudioCodec = cfg.AudioCodec
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("encoder launch: marshal ingest bootstrap: %w", err)
	}
	// Object.freeze prevents later page/script code from tampering with the
	// injected capability token in place.
	return "window.__OMNIPUS_INGEST__ = Object.freeze(" + string(payload) + ");", nil
}
