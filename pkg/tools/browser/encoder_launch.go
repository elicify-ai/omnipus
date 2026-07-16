// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// encoder_launch.go — Wave 2 component M (live-browser video streaming, ADR-044;
// docs/internal/specs/live-browser-video-streaming-spec.md R7). This file owns
// the CDP-driven launch of the controlled WebCodecs *encoder page* inside the
// shared headful managed Chrome: it opens an isolated child tab of the
// coordinator's rootCtx, injects the per-stream ingest capability token
// out-of-band (never via URL), grants audio-capture consent SCOPED TO THE
// ENCODER-PAGE ORIGIN ONLY, and navigates the tab to the loopback encoder-page
// URL served by the stream orchestrator (pkg/gateway/browser_stream.go).
//
// SECURITY POSTURE (FR-016 / MAJ-003 — why the origin must be unguessable):
//
//   - The audio-capture grant is applied with Browser.setPermission scoped to
//     the encoder-page ORIGIN. CDP permission grants are origin-scoped, so if
//     the agent could navigate ITS OWN tab to the same origin it would inherit
//     the audio-capture grant. The orchestrator therefore serves the encoder
//     page on an UNGUESSABLE loopback origin — an OS-assigned random port on
//     127.0.0.1 PLUS a random secret path — so an agent-navigated page can
//     neither guess the origin (random port) nor the resource (secret path),
//     and thus can never obtain the audio grant. This file relies on the
//     orchestrator having minted that unguessable Origin/EncoderURL; it does
//     not itself pick the origin.
//   - The per-stream token is injected via Page.addScriptToEvaluateOnNewDocument
//     so it runs BEFORE the encoder page's own script and is never present in
//     the URL, query string, or the embedded page bytes (FR-013).
//   - No process-global media flag is used: --use-fake-ui-for-media-stream is
//     FORBIDDEN (C-2/P-6). Video capture is CDP Page.startScreencast (component
//     L) which has no page-callable API at all; the ONLY consent surface is
//     this per-origin audio grant, and only when audio is requested.

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/page"
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

// LaunchEncoderPage opens the controlled encoder page for one stream inside the
// shared headful Chrome (rootCtx is the coordinator's shared root context;
// coordinator.Register returns it). It:
//
//  1. opens an ISOLATED child tab (a fresh browser context of rootCtx),
//  2. (only when cfg.HasAudio) grants audio-capture consent scoped to
//     cfg.Origin — the unguessable encoder-page origin — so an agent-navigated
//     page on any other origin cannot capture (FR-016),
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

	// Isolated child tab of the shared root — its own browser context keeps the
	// encoder page's storage/permissions disjoint from every agent context.
	tabCtx, cancel := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())

	// Bound only the launch handshake; the tab context itself stays live.
	bringup, cancelBringup := context.WithTimeout(tabCtx, encoderLaunchTimeout)
	defer cancelBringup()

	// First Run materializes the target so its BrowserContextID is available to
	// scope the permission grant to this exact context (mirrors coordinator.go).
	if err := chromedp.Run(bringup); err != nil {
		cancel()
		return nil, fmt.Errorf("encoder launch: create encoder target: %w", err)
	}
	bid := chromedp.FromContext(tabCtx).BrowserContextID

	script, err := buildIngestBootstrapScript(cfg)
	if err != nil {
		cancel()
		return nil, err
	}

	actions := make([]chromedp.Action, 0, 3)
	if cfg.HasAudio {
		// Origin-scoped audio-capture grant (FR-016). Bound to BOTH the
		// unguessable origin AND this specific browser context, so no other
		// origin/context — in particular an agent-navigated tab — inherits it.
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
