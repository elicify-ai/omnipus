// Omnipus capture extension encoder page.
//
// This page is opened directly by the gateway (via CDP,
// chrome-extension://<pinned-id>/encoder.html) once the extension has been
// loaded into a managed, headless Chrome instance. It self-consumes
// chrome.tabCapture on the ACTIVE tab (no separate consumer tab — the
// spike-proven simplest pattern, see docs/internal/design/wv1-spike-results.md
// Q2/Q3), negotiates a non-trickle WebRTC offer/answer with the gateway's
// loopback capture-ingest WS endpoint, and streams both audio+video tracks.
//
// Config is injected by the gateway BEFORE this script runs, via
// Page.addScriptToEvaluateOnNewDocument setting
// `window.__omnipusCapture = {token, ingestUrl}`. This is intentionally
// NEVER read from URL params or any other lower-trust channel — the
// loopback WS is not a trust boundary by itself, so the token is the only
// thing that authorizes this page to mint a capture session server-side.
//
// Wire frame shapes — pinned by the landed capture-ingest contract
// (contracts/components/schemas/BrowserCapture{Hello,Offer,Answer,Control}Frame.yaml,
// ADR-047 D1/D2/D6):
//   -> {type: 'browser_capture_hello',   token, ext_version}
//   -> {type: 'browser_capture_offer',   sdp}
//   <- {type: 'browser_capture_answer',  sdp}
//   <-  {type: 'browser_capture_control', action: recapture|shutdown|adapt_reset|set_bitrate, reason?, expected_width?, expected_height?}  (server -> client)
//   ->  {type: 'browser_capture_control', action: ping}                        (client -> server)
//
// expected_width/expected_height (2026-07-31 follow-up,
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md): on a
// recapture triggered by a viewport resize, the gateway has already read
// back the tab's ACTUAL CSS viewport via CDP's Page.getLayoutMetrics — the
// one piece of CDP-VERIFIED truth that exists at that moment — and threads
// it through here so captureActiveTabStream can converge on a KNOWN target
// instead of merely polling chrome.tabs.get until two reads agree with each
// other (which two STALE reads can also satisfy). Absent on a recapture with
// no such measurement to offer (e.g. an active-tab switch).
//
// browser_capture_hello is CLIENT -> SERVER ONLY — the gateway never sends
// one back. Per the schema doc: "the gateway audits any hello with a
// missing/invalid/expired token as a rejected ingest-auth attempt and
// closes the connection" — i.e. success is silent (the connection simply
// stays open and you proceed to the next frame); the only observable
// signal for a REJECTED hello is the WS closing. So this page sends hello
// then immediately proceeds to capture + offer, with no ack wait — the
// reconnect watchdog (a WS close before/without ever reaching 'connected')
// is what surfaces an auth rejection.
//
// browser_capture_control's `ping` is likewise CLIENT -> SERVER ONLY (this
// page's own periodic health/reconnect-watchdog beacon); the server-issued
// control actions are exactly `recapture` and `shutdown` — there is no
// `pong` action in the schema's enum.
'use strict';

const LOG_PREFIX = '[omnipus-capture]';

function log(...args) {
  console.log(LOG_PREFIX, ...args);
}

function warn(...args) {
  console.warn(LOG_PREFIX, ...args);
}

// window.__omnipusState is a debug/verification surface only — it is never
// part of the wire protocol. Verification (including this task's
// real-Chrome check) reads it back via CDP Runtime.evaluate.
window.__omnipusState = {
  status: 'idle', // idle -> connecting -> hello_sent -> capturing -> offering -> connected -> reconnecting -> error -> shutdown
  wsState: 'closed',
  iceState: null,
  connState: null,
  lastError: null,
  reconnectAttempts: 0,
  videoTracks: null,
  audioTracks: null,
  // senderConstraints is set only by a POST-negotiation
  // applyVideoSenderConstraints re-apply (see that function) whose
  // setParameters() call actually resolved -- i.e. it is the external,
  // outside-the-page proof (read via CDP Runtime.evaluate) that the
  // degradationPreference/maxBitrate/scaleResolutionDownBy encoding
  // constraints genuinely stuck on a settled sender, not just that this file
  // attempted to set them. Stays null until the first such success.
  senderConstraints: null,
  history: [],
};

function record(evt) {
  window.__omnipusState.history.push({ t: Date.now(), evt: String(evt) });
  if (window.__omnipusState.history.length > 300) window.__omnipusState.history.shift();
  log(evt);
}

function setStatus(s) {
  window.__omnipusState.status = s;
}

// ---- config ---------------------------------------------------------------

function readConfig() {
  const cfg = window.__omnipusCapture;
  if (!cfg || typeof cfg.token !== 'string' || typeof cfg.ingestUrl !== 'string') {
    throw new Error(
      'window.__omnipusCapture missing or malformed (expected {token, ingestUrl} injected by the gateway via Page.addScriptToEvaluateOnNewDocument)'
    );
  }
  return cfg;
}

function extVersion() {
  try {
    return chrome.runtime.getManifest().version;
  } catch (e) {
    return 'unknown';
  }
}

// ---- tab selection ----------------------------------------------------------

function isExtensionUrl(url) {
  return typeof url === 'string' && url.startsWith('chrome-extension://');
}

// findActiveTargetTab implements the targeting rule from the task spec:
// the ACTIVE tab in the last-focused window, falling back to the first
// non-extension tab if there is no such active tab (e.g. the extension's
// own page happens to be focused).
async function findActiveTargetTab() {
  const active = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  const activeCandidate = active.find((t) => !isExtensionUrl(t.url));
  if (activeCandidate) return activeCandidate.id;

  record('findActiveTargetTab: no non-extension active tab, falling back to first non-extension tab');
  const all = await chrome.tabs.query({});
  const fallback = all.find((t) => !isExtensionUrl(t.url));
  if (!fallback) {
    throw new Error('no capturable tab found (only extension pages are open)');
  }
  return fallback.id;
}

// ---- capture ----------------------------------------------------------------

let currentStream = null;
let currentPC = null;
let ws = null;
let shuttingDown = false;
let lastGoodIceTime = Date.now();
let watchdogTimer = null;
let pingBeaconTimer = null;
let reconnectTimer = null;
let reconnectDelayMs = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;
const ICE_BAD_GRACE_MS = 10000;
const PING_BEACON_INTERVAL_MS = 15000;

// expectedCaptureDims (2026-07-31 follow-up,
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md) carries the
// CDP-verified {w, h} a viewport-resize-triggered recapture wants
// captureActiveTabStream to converge on, or null when the incoming recapture
// carried no such hint (e.g. an active-tab switch — see
// handleControlFrame). Set just before runCaptureAndOffer is kicked off for
// a recapture and consumed (then cleared) by captureActiveTabStream's own
// polling logic below, so it is never stale across a LATER recapture that
// omits the fields.
let expectedCaptureDims = null;
// deviceScaleFactor of the captured tab — see the recapture control handler. 1 = CSS.
let captureScale = 1;
// CAPTURE_PIXEL_BUDGET caps the PHYSICAL pixels tabCapture is asked to
// produce (CSS size x captureScale). Measured 2026-08-31 on the 2-core
// hosted UAT box (performance-2x, no GPU): with a viewer attached, Chrome
// sat at 150-192% of ONE core and the machine at 85-99%, which starved
// input dispatch -- scroll arrived in bursts, clicks were dropped, and the
// ICE consent checks missed their deadline ("ice-disconnected-timeout").
// Video looked fine throughout, because one-way media tolerates a busy
// machine and round-trip input does not.
//
// Encode cost scales with pixels, and the file's own history measures it:
// the same scroll at a quarter of the pixels went 1 fps -> 18 fps. Nothing
// bounded that input. live.go's ceiling is 33.2 MPx -- 36x this budget --
// and it exists to stop a nonsense viewport, not to protect the encoder.
//
// 1280x720 is deliberately the same frame the capture already defaults to
// (capW/capH below), so a normal panel is unaffected and only oversized or
// Retina-doubled captures are clamped. Shrinking here is safe for input:
// the server rescales every pointer event from the client's REPORTED
// capture size into CSS space (live.go rescaleInputCoords), so a smaller
// capture is a sharpness trade, never a click-goes-nowhere bug -- see the
// long note on that contract above.
const CAPTURE_PIXEL_BUDGET = 1280 * 720;

// budgetedCaptureDims returns the physical capture size to request, clamped
// to CAPTURE_PIXEL_BUDGET. Scaling is uniform (sqrt of the overshoot) so the
// aspect ratio is preserved and the server-side coordinate rescale stays
// proportional. Dimensions are rounded to even numbers because H.264 chroma
// subsampling requires it. Pure function -- unit-tested without a browser.
function budgetedCaptureDims(cssW, cssH, scale) {
  const w0 = Math.max(2, Math.round(cssW * scale));
  const h0 = Math.max(2, Math.round(cssH * scale));
  const px = w0 * h0;
  if (!(px > CAPTURE_PIXEL_BUDGET)) return { w: w0, h: h0, clamped: false };
  const k = Math.sqrt(CAPTURE_PIXEL_BUDGET / px);
  const w = Math.max(2, Math.round((w0 * k) / 2) * 2);
  const h = Math.max(2, Math.round((h0 * k) / 2) * 2);
  return { w: w, h: h, clamped: true };
}

// Tab id of the CURRENTLY captured tab — recorded at capture time so the
// shutdown handler can tab-mute it. Tab-level mute (chrome.tabs.update
// muted:true) only touches LOCAL speaker output; it is applied strictly
// while NO capture exists (capture is torn down in the same shutdown), so it
// can never repeat the --mute-audio incident, where a browser-level flag
// silenced what tabCapture itself received (rmsMean 0.30258 -> 0).
let capturedTabId = null;

// lastPinnedCapDims records what captureActiveTabStream actually pinned the
// running stream to, and selfHealBudget bounds the post-connect
// verification below (2026-07-31 live UAT, pop-out): multiple recaptures can
// overlap during a panel spin-up (attach-time corrective recapture, the
// viewport apply's own recapture, the chrome-delta compensation's second
// window reflow) and the LAST capture to win can pin a mid-compensation size
// (measured: 1586x730 pinned while the settled viewport was 1586x816). Racing
// orderings are unwinnable one by one — instead, ~1.2s after each capture
// connects, re-read chrome.tabs.get once and self-recapture ONCE if the
// pinned size drifted more than 8px from the settled tab. Bounded: the flag
// resets per capture cycle, and a self-heal recapture that still mismatches
// does not re-arm itself.
let lastPinnedCapDims = null;
// Budget of 3 (not a one-shot flag): live UAT showed overlapping spin-up
// cycles consume a single-shot guard before the WINNING capture connects,
// leaving a drifted stream unhealed (pop-out pinned 1586x730 vs settled
// 816 while a sibling run converged fine). Each server-initiated recapture
// (and the initial capture) grants 3 post-connect checks; self-heal
// recaptures spend from the same budget — bounded convergence, no churn.
let selfHealBudget = 3;

// DEFAULT_MAX_VIDEO_BITRATE_BPS (fix-wave finding 4, "overdrive"; revised
// per docs/internal/browser-viewport-input-rootcause-2026-07-31.md fault 2):
// the tabCapture MediaStream has no bandwidth ceiling of its own, so an
// unconstrained VP8 encoder will burn as much CPU/bandwidth as the content
// demands -- a real contributor to pod-CPU-saturation/choppy-input symptoms
// under heavy (video-playing) pages, which is why a cap exists at all. The
// original 2 Mbps cap was set with that CPU-saturation risk in mind, but
// live UAT showed it was actually the PRESSURE that triggered the
// resolution collapse this file's degradationPreference fix (see
// applyVideoSenderConstraints) addresses: paired with 'balanced' degradation,
// 2 Mbps was tight enough that VP8 gave up resolution to stay under it,
// producing a 319x158 stream at panel-sized (~561x587) geometry -- both
// visibly blurry and, worse, wrong for input mapping. 6 Mbps gives VP8
// headroom to hold full panel-sized geometry without hitting the ceiling
// under normal browsing content, while 'maintain-resolution' now means any
// remaining pressure is spent on framerate instead of resolution.
// Overridable per-install via window.__omnipusCapture.maxVideoBitrate
// (bits/sec) for future config -- see the file header for the
// injection mechanism; unset/invalid falls back to this default.
const DEFAULT_MAX_VIDEO_BITRATE_BPS = 6000000;

// OFFER_ANSWER_TIMEOUT_MS (fix-wave finding 4, review-flagged gap): before
// this fix, a browser_capture_offer whose browser_capture_answer never
// arrived (dropped frame, gateway-side error the encoder never learns about
// any other way) left this page silently wedged in 'offering' state
// forever -- the WS itself stays open, so neither the ICE watchdog
// (startWatchdogOnce, which only fires once a PeerConnection ICE state
// exists) nor the reconnect-on-close path (scheduleReconnect) ever engages.
// Closing the WS on timeout routes through the SAME 'close' handler every
// other disconnect uses, so the existing backoff/reconnect logic below
// (scheduleReconnect) is what actually recovers -- no new recovery path.
const OFFER_ANSWER_TIMEOUT_MS = 10000;
let offerAnswerTimer = null;

// armOfferAnswerTimeout captures the CURRENT ws instance so a stale timer
// (one armed before a reconnect already replaced `ws` with a fresh
// connection) can never close a healthy, unrelated newer connection --
// without this guard, a timer that fires just as scheduleReconnect's own
// backoff already replaced `ws` would incorrectly kill the brand new
// connection instead of being the no-op it should be.
function armOfferAnswerTimeout() {
  clearOfferAnswerTimeout();
  const forWs = ws;
  offerAnswerTimer = setTimeout(() => {
    offerAnswerTimer = null;
    if (ws !== forWs) return; // superseded by a newer connection -- stale, ignore
    window.__omnipusState.lastError = 'offer-answer timeout: no browser_capture_answer received';
    record('offer-answer timeout: no browser_capture_answer within ' + OFFER_ANSWER_TIMEOUT_MS + 'ms, forcing reconnect');
    try {
      forWs.close();
    } catch (e) {
      /* ignore */
    }
  }, OFFER_ANSWER_TIMEOUT_MS);
}

function clearOfferAnswerTimeout() {
  if (offerAnswerTimer) {
    clearTimeout(offerAnswerTimer);
    offerAnswerTimer = null;
  }
}

// applyVideoSenderConstraints (fix-wave finding 4) caps the video sender's
// bitrate and sets encoding hints on the video track's RTCRtpSender. Errors
// are logged, not thrown -- a failed setParameters/contentHint assignment
// should degrade to "uncapped bitrate", not abort the whole capture/offer
// flow.
//
// Called THREE times per capture cycle (UAT v24 finding,
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md fault 2,
// fix-wave follow-up): once from runCaptureAndOfferOnce right after
// pc.addTrack (context='pre-negotiation'), again once the
// browser_capture_answer has been applied via setRemoteDescription
// (context='post-answer'), and again on the PC's first transition to
// connectionState 'connected' (context='post-connected'). The
// pre-negotiation call is the one that shipped first and it is NOT enough
// on its own: RTCRtpSender.getParameters()/setParameters() is a
// transactional read-modify-write keyed to the sender's current transport
// state, and calling it before setLocalDescription/setRemoteDescription has
// completed negotiation commonly rejects with InvalidStateError or
// InvalidModificationError on a sender whose transport isn't settled yet.
// Live evidence: even after this function landed with
// degradationPreference='maintain-resolution' + scaleResolutionDownBy=1 + a
// 6 Mbps cap, UAT v24 still measured the delivered stream sitting at
// 228x246 for 60s before stepping to 307x328 (exactly tab/2) and holding --
// the resolution scaler was still active, meaning the pre-negotiation
// setParameters call had almost certainly been silently rejected the whole
// time (its .catch only warn()s, invisible in a headless run). Re-invoking
// this function (idempotent -- same params, same sender) once the
// transceiver has actually settled is what makes the constraints stick;
// the pre-negotiation call is kept anyway as a best-effort "in case this
// browser's sender accepts it early" no-op-on-failure attempt, now that
// failure is no longer invisible (see the lastError writes below).
//
// Each call site is naturally idempotent against re-invocation without any
// extra bookkeeping: the pre-negotiation and post-answer calls each fire at
// most once per capture cycle from their respective call sites, and the
// post-connected call is gated by the connectedConstraintsApplied flag
// newPeerConnection closes over -- so this never "stacks" repeated
// setParameters calls onto a single connectionstatechange listener.
// senderParamsChain serializes every getParameters()->setParameters() pair on
// the video sender.
//
// Why this exists (measured on the hosted box 2026-08-17, and it is NOT the
// empty-encodings theory 1.0.11/1.0.12 were built on -- that error still fired
// with those in place): libwebrtc clears the sender's last_transaction_id_ on
// EVERY setParameters call. A second setParameters built from parameters read
// BEFORE that happened is rejected with
//   InvalidStateError: Failed to set parameters since getParameters() has
//   never been called on this sender
// which reads like "you never called getParameters" but actually means "the
// parameters you are handing me are stale". Three sites apply constraints --
// post-answer, post-connected, and the 2s adaptation loop -- and nothing kept
// them from overlapping.
//
// Queuing makes each pair atomic with respect to the others: the read happens
// inside the queued step, immediately before its own write.
// viewerBitrateCeiling is the maximum bitrate the VIEWER's link was measured
// to sustain, pushed by the gateway as browser_capture_control{set_bitrate}
// (ADR-062 Finding 2). 0 = never reported, use the local calculation alone.
//
// This exists because this page cannot measure the link that matters. Its own
// PeerConnection is loopback to the gateway -- infinite bandwidth, zero loss --
// so libwebrtc's congestion control here is measuring a hop nobody watches.
// The gateway sees the real receiver reports and sends the answer down.
let viewerBitrateCeiling = 0;

let senderParamsChain = Promise.resolve();
function queueSenderParams(fn) {
  const run = function () { return Promise.resolve().then(fn); };
  // Chain through BOTH paths so one rejection cannot wedge the queue forever.
  senderParamsChain = senderParamsChain.then(run, run);
  return senderParamsChain;
}

function encodingsNegotiated(params) {
  // Chrome hands out encodings:[{}] -- a single EMPTY object -- before
  // negotiation has populated the sender. Calling setParameters on that
  // throws InvalidStateError ("getParameters() has never been called on this
  // sender") even though getParameters() was just called.
  //
  // Discriminate on that SHAPE and nothing else. An earlier revision required
  // one of ssrc/rid/codecPayloadType/maxBitrate to be present, which was a
  // guess about fields that are not part of RTCRtpEncodingParameters (and
  // maxBitrate is self-referential -- it only exists after a successful apply,
  // so a wrong guess would disable capping permanently and silently). Any
  // non-empty encoding object is treated as negotiated; only the literal
  // placeholder is skipped.
  if (!params || !params.encodings || params.encodings.length === 0) return false;
  const e = params.encodings[0];
  if (!e || typeof e !== 'object') return false;
  return Object.keys(e).length > 0;
}

function applyVideoSenderConstraints(pc, opts) {
  opts = opts || {};
  const context = opts.context || 'pre-negotiation';
  const recordSuccess = opts.recordSuccess === true;
  const logPrefix = 'applyVideoSenderConstraints[' + context + ']';

  const sender = pc.getSenders().find((s) => s.track && s.track.kind === 'video');
  if (!sender) {
    const msg = logPrefix + ': no RTCRtpSender found for the video track';
    warn(msg);
    window.__omnipusState.lastError = msg;
    return;
  }
  const videoTrack = sender.track;

  const cfg = window.__omnipusCapture || {};
  const baseBitrate =
    typeof cfg.maxVideoBitrate === 'number' && cfg.maxVideoBitrate > 0 ? cfg.maxVideoBitrate : DEFAULT_MAX_VIDEO_BITRATE_BPS;
  // The cap was tuned for 1x (CSS-resolution) capture. Physical-resolution
  // capture at scale s carries s^2 the pixels; a fixed cap starves VP8, and
  // with degradationPreference 'maintain-resolution' the encoder pays for it
  // in FRAMERATE - observed live (macOS 2026-08-12) as visibly laggy video
  // playback after the blur fix. Scale the cap with the pixel count, bounded
  // at 4x base (= scale 2) so a scale-3/4 tab cannot demand unbounded rate.
  let maxBitrate = Math.min(baseBitrate * 4, Math.round(baseBitrate * captureScale * captureScale));
  // Never ask for more than the viewer's link was measured to carry.
  if (viewerBitrateCeiling > 0 && viewerBitrateCeiling < maxBitrate) {
    maxBitrate = viewerBitrateCeiling;
  }

  queueSenderParams(function () {
   try {
    const params = sender.getParameters();
    // Chrome only treats getParameters() as "called" once negotiation has
    // populated encodings. Synthesizing encodings: [{}] and then calling
    // setParameters() is what produces:
    //   InvalidStateError: Failed to set parameters since getParameters()
    //   has never been called on this sender
    // Live CI (2026-08-16 ui-heavy): that rejection fired at post-connected
    // and the encoder reported it as a stream-quality failure. Skip until
    // the sender has real encodings; the post-answer / post-connected
    // re-applies catch it once they exist. Video keeps flowing uncapped
    // rather than a failed setParameters poisoning the sender.
    if (!encodingsNegotiated(params)) {
      const msg = logPrefix + ': encodings not negotiated, skipping setParameters';
      record(msg);
      // A skip at post-connected means the sender never got its bitrate cap.
      // Report it so it lands in the gateway log instead of dying in an
      // extension console nobody opens (round-2 finding F7's discipline).
      if (context === 'post-connected') reportAdaptFailure(msg);
      return;
    }
    params.encodings[0].maxBitrate = maxBitrate;
    // degradationPreference 'maintain-resolution' (previously 'balanced' --
    // see docs/internal/browser-viewport-input-rootcause-2026-07-31.md,
    // fault 2). The original rationale for 'balanced' was that the captured
    // tab can be anything from a text-heavy page to video playback, and
    // committing to either extreme permanently sacrifices the other content
    // type. That reasoning undersold the cost: live UAT measured the
    // delivered stream at 319x158 on a shared-cpu-2x box -- 'balanced' plus
    // the (then 2 Mbps) bitrate cap let VP8 trade resolution away under CPU
    // pressure, and it kept doing so far below anything a viewer would
    // accept. That downscale is (a) the reported blur -- 319x158 upscaled
    // ~2x into a ~561x587 panel -- and (b) was ORIGINALLY thought to be
    // load-bearing for the SPA's input coordinate mapping too, on the
    // assumption that capture pixels track the page's CSS viewport 1:1
    // (wave-plan key-decision-8). That input-correctness argument no longer
    // holds this fix up: the same fix-wave that raised this bitrate ceiling
    // also made the server independently rescale every pointer event from
    // whatever capture-frame size the client actually reports into the
    // tab's real CSS pixel space -- see pkg/tools/browser/live.go's
    // dispatchInput (which now rescales x/y per-event via
    // rescaleToCSSViewport/rescaleInputCoords using the client's reported
    // CaptureWidth/CaptureHeight, not an assumed 1:1). So a resolution
    // collapse here is no longer a click-goes-nowhere bug -- it is now
    // purely a sharpness/UX regression, handled as a second, independent
    // line of defense. 'maintain-resolution' is kept because blurry video
    // is still worth avoiding, and because avoiding a collapse in the first
    // place is simpler than trusting every input-consuming surface to
    // rescale correctly, not because input correctness depends on it.
    // Paired with the raised bitrate ceiling below (6 Mbps) to give VP8
    // headroom before it needs to degrade at all.
    // 'balanced', reversing the 2026-07-31 'maintain-resolution' pin. That
    // pin predated physical-resolution (2x) capture, and the calculus flips
    // with it: measured 2026-08-13 on a 4-core mobile Intel i7 (native x64,
    // no Rosetta), software VP8 at 1122x1416 under sustained full-motion
    // content (testufo.com) delivered 4-10fps with 3-second dead stalls,
    // because maintain-resolution makes a CPU-bound encoder pay entirely in
    // FRAMERATE. 'balanced' lets it downscale DURING motion - where
    // sharpness is imperceptible anyway - and restore the full 2x when the
    // page is static, which is when text is actually read. Sharp-when-still,
    // fluid-when-moving is the correct trade for a remote-control surface on
    // hardware that cannot have both at once.
    params.degradationPreference = 'balanced';
    // scaleResolutionDownBy is NO LONGER hardcoded to 1 (2026-08-15). It is
    // now owned by the bounded adaptation loop below (see
    // "adaptive resolution" section): currentAdaptScale() returns 1 on a
    // machine that can encode at full resolution, and only the loop -- on
    // the encoder's OWN cpu-limitation self-report -- ever moves it, in
    // fixed steps, never past the hard floor of 2.
    //
    // Reading it from the loop here (rather than writing a literal) is
    // load-bearing, not cosmetic: this function is re-invoked post-answer
    // and post-connected, and a fresh 'connected' transition re-invokes it
    // on every reconnect. A literal 1 would silently undo whatever the loop
    // had already decided, which is exactly the "green but broken" shape
    // where the loop logs a step down and the picture never changes.
    params.encodings[0].scaleResolutionDownBy = currentAdaptScale();
    return sender
      .setParameters(params)
      .then(() => {
        record(logPrefix + ': setParameters applied');
        if (recordSuccess) {
          // The exit-proof probe (CDP Runtime.evaluate) reads this back to
          // verify the constraints ACTUALLY stuck on a settled sender, not
          // merely that this file attempted to set them -- see the
          // window.__omnipusState.senderConstraints doc comment above.
          window.__omnipusState.senderConstraints = {
            degradationPreference: params.degradationPreference,
            maxBitrate: maxBitrate,
            scaleResolutionDownBy: params.encodings[0].scaleResolutionDownBy,
            appliedAt: Date.now(),
            context: context,
          };
        }
      })
      .catch((e) => {
        const msg = logPrefix + ': setParameters failed: ' + String(e);
        warn(msg, e);
        window.__omnipusState.lastError = msg;
        // Same reasoning as the adaptation loop's own apply failure (F7):
        // this rejection means the bitrate ceiling, the degradation
        // preference AND the current adapt scale all silently failed to
        // stick, and this page's console is not a surface anyone reads.
        reportAdaptFailure(msg);
      });
   } catch (e) {
    const msg = logPrefix + ': getParameters/setParameters failed: ' + String(e);
    warn(msg, e);
    window.__omnipusState.lastError = msg;
   }
  });

  // contentHint 'detail' (REVERSED from 'motion', 2026-07-31 live evidence):
  // 'motion' tells libwebrtc this is camera-like video, which ENABLES the
  // quality scaler -- the encoder is allowed to reduce RESOLUTION under
  // CPU/bandwidth pressure. Measured on UAT v24/v25: the delivered stream sat
  // pinned at ~tab/2.7 (228px wide against a 615px tab) even with
  // degradationPreference 'maintain-resolution' + scaleResolutionDownBy 1
  // applied post-negotiation and a 4Mbps start-bitrate hint -- the scaler
  // kept winning, and 'motion' is what licenses it. 'detail' marks the track
  // as screen content whose per-frame legibility matters: libwebrtc disables
  // resolution scaling and sheds FRAMERATE under pressure instead. That is
  // the right trade here -- a text page at full resolution and 10fps is
  // usable; at 228px wide and 30fps it is not, and the original 'motion'
  // rationale (video-playback smoothness) predates the server-side input
  // rescale (live.go rescaleInputCoords), which now keeps clicks accurate
  // regardless. A per-page-adaptive hint remains possible future work.
  // Re-set on every call -- setting an unchanged value is a harmless no-op,
  // so this needs no context guard.
  try {
    videoTrack.contentHint = 'detail';
  } catch (e) {
    const msg = logPrefix + ': setting contentHint failed: ' + String(e);
    warn(msg, e);
    window.__omnipusState.lastError = msg;
  }
}

// ---- adaptive resolution ----------------------------------------------------
//
// WHY THIS EXISTS (measured 2026-08-14/15, hosted UAT vs macOS):
// contentHint='detail' + scaleResolutionDownBy=1 together FORBID the encoder
// from shrinking the picture. That was itself a fix -- on 2026-07-31 the
// stream collapsed to 319x158, unreadably blurry, and the pin is what
// stopped it -- but it leaves FRAME RATE as the only thing that can give,
// and on a slow software encoder it gives all the way down:
//
//   4x SHARED-cpu box, software H.264, scrolling at 1266x1372 ->  1 fps
//     @ ~700kbps, 0% packet loss, machine only 9% busy
//   the same scroll at 632x684 (a quarter of the pixels)       -> 18 fps @ 2.5Mbps
//   an animated page, no input, full resolution                -> 15 fps
//   2x PERFORMANCE-cpu box, same full-resolution scroll        -> 15 fps
//   macOS (VideoToolbox HARDWARE H.264, loopback)              -> 13 fps
//
// So the pipeline, the network and the bitrate cap are all fine: it is the
// per-frame encode cost under a full-screen repaint that collapses, and only
// on a machine whose encoder cannot keep up. The fix is therefore NOT to
// un-pin the scaler (that reproduces 2026-07-31), but a BOUNDED closed loop:
// give up resolution in fixed, logged steps, only while the encoder itself
// reports it is CPU-limited, and never past half linear scale.
//
// PARITY (the point of this change): the user-visible contract on every
// platform becomes "smooth, and as sharp as this machine can manage" instead
// of "sharp on one, a slideshow on the other". Same controls, same log lines,
// same recovery on both. The loop is gated on the encoder's OWN
// qualityLimitationReason === 'cpu' self-report, so on a hardware encoder
// over loopback -- which is never CPU-limited -- it never steps at all and
// the picture stays exactly as sharp as it is today. A macOS-shaped sample
// (reason 'none' at 13 fps) is deliberately NEUTRAL below: not good enough to
// step up, and not evidence of pressure, so it touches nothing.
//
// Hard floor: ADAPT_SCALE_STEPS cannot express a scale worse than 2 (a
// quarter of the pixels), so the 2026-07-31 collapse to ~1/4 LINEAR scale
// cannot recur through this path even if every heuristic below misfires.

// ADAPT_SCALE_STEPS are the only scaleResolutionDownBy values this loop will
// ever set. The last entry IS the hard floor -- keep it at 2.
const ADAPT_SCALE_STEPS = [1, 1.5, 2];
const ADAPT_MAX_STEP_INDEX = ADAPT_SCALE_STEPS.length - 1;

// How often sender stats are sampled. 2s is long enough for
// framesPerSecond (a rolling average) to reflect a step, short enough that a
// scroll-induced collapse is corrected within ~4-6s.
const ADAPT_POLL_MS = 2000;

// Step DOWN when the encoder says 'cpu' and delivered fps is below this.
const ADAPT_TARGET_FPS = 12;

// Step UP only when unlimited AND comfortably above the target. The gap
// between 24 and 12 is the hysteresis band: a step up roughly halves the
// achievable fps (each step is ~2.25x the pixels), so requiring 2x the
// down-threshold before restoring means a step up should not immediately
// re-trigger a step down.
const ADAPT_RESTORE_FPS = 24;

// Consecutive-sample requirements. Asymmetric on purpose: react to pressure
// in ~4s, but require ~10s of sustained headroom before spending it again.
const ADAPT_DOWN_SAMPLES = 2;
const ADAPT_UP_SAMPLES = 5;

// Samples ignored after a step, so the next decision is made on stats that
// actually describe the NEW scale rather than the old one's rolling average.
const ADAPT_SETTLE_SAMPLES = 2;

// ADAPT_EVIDENCE_TTL_MS is how long a step down stays justified without the
// encoder re-confirming CPU pressure (round-2 finding F2, 2026-08-16).
//
// A step down is a HYPOTHESIS about current conditions, not a permanent
// verdict about this machine, and the round-1 loop had no way to retire one:
// the only restore path requires reason !== 'cpu' AND >= 24 fps for five
// consecutive samples. A STATIC page -- the single most common thing this
// panel shows, and the thing a text-reading user most wants sharp -- encodes
// at ~0-2 fps by definition, so on a page that stops moving the restore path
// can NEVER fire and a scale learned during one busy moment latches forever.
//
// So: if no cpu-limited sample has been seen for this long, the loop gives
// one step back and re-tests. If the pressure is real it returns in ~4s (two
// samples), and the TTL restarts -- the cost is a ~4s dip at most once a
// minute on a machine that genuinely cannot keep up. If it is not real, the
// picture comes back to full resolution on its own.
//
// It is also the freshness clock for what a capture REBUILD inherits --
// see adaptCarryOverIndex.
const ADAPT_EVIDENCE_TTL_MS = 60000;

function adaptInitialState() {
  return { index: 0, badStreak: 0, goodStreak: 0, cooldown: 0, lastPressureAt: 0 };
}

// adaptState persists ACROSS recapture cycles CONDITIONALLY: it describes
// this MACHINE's encoder, not this PeerConnection, so resetting it on every
// viewport resize (each of which triggers a recapture) would restart the 1
// fps collapse from scratch every time the panel is dragged -- but a scale
// learned minutes ago, or learned by a capture NOBODY WAS WATCHING, is not
// evidence about what the viewer in front of the panel right now is
// experiencing. adaptCarryOverIndex is the rule; see its doc comment.
let adaptState = adaptInitialState();
let adaptTimer = null;

// adaptCycleCount counts CAPTURES THAT ACTUALLY RAN -- incremented where the
// adaptation loop starts (the PeerConnection's first 'connected' transition),
// which is once per capture cycle and never for a capture that failed to
// negotiate. It exists so the state learned by a page's FIRST capture can be
// treated differently from every later one -- see adaptCarryOverIndex.
//
// Counted at START, not at teardown, deliberately: runCaptureAndOfferOnce
// calls teardownCapture as its own first step, so teardowns are one ahead of
// captures and the very first one happens before any capture exists at all.
// Counting those would make "the first capture's state" arrive a cycle late --
// i.e. exactly the boot-warmed, viewerless capture whose state this rule
// exists to discard would be the one that got carried.
let adaptCycleCount = 0;

function noteAdaptCycleStarted() {
  adaptCycleCount += 1;
}

function clampAdaptIndex(i) {
  if (!(typeof i === 'number') || !isFinite(i) || i < 0) return 0;
  if (i > ADAPT_MAX_STEP_INDEX) return ADAPT_MAX_STEP_INDEX;
  return Math.floor(i);
}

// currentAdaptScale is the single source of truth for scaleResolutionDownBy.
// applyVideoSenderConstraints reads it so its post-answer/post-connected
// re-applies preserve, rather than silently undo, the loop's decision.
function currentAdaptScale() {
  return ADAPT_SCALE_STEPS[clampAdaptIndex(adaptState.index)];
}

// qualityAdaptDecide is deliberately PURE (sample + previous state -> new
// state + action) so the policy can be exercised exhaustively off-browser --
// see TestEncoderJS_QualityAdaptLoop. It never touches the sender itself.
//
// Neutral is the default: anything that is not "cpu-limited and slow" or
// "unlimited and fast" decays both streaks and changes nothing. That matters
// because a STATIC page legitimately produces near-zero fps (there is
// nothing to encode), so low fps ALONE is never treated as pressure.
function qualityAdaptDecide(sample, prev, now) {
  const st = {
    index: clampAdaptIndex(prev && prev.index),
    badStreak: (prev && prev.badStreak) || 0,
    goodStreak: (prev && prev.goodStreak) || 0,
    cooldown: (prev && prev.cooldown) || 0,
    lastPressureAt: (prev && prev.lastPressureAt) || 0,
  };
  sample = sample || {};
  now = typeof now === 'number' && isFinite(now) ? now : Date.now();

  if (st.cooldown > 0) {
    st.cooldown -= 1;
    return { state: st, action: 'settle', note: 'settling after a step, ' + st.cooldown + ' sample(s) left' };
  }

  const fps = typeof sample.framesPerSecond === 'number' && isFinite(sample.framesPerSecond) ? sample.framesPerSecond : null;
  const reason = typeof sample.qualityLimitationReason === 'string' ? sample.qualityLimitationReason : null;
  if (fps === null) {
    // No usable reading yet (the first samples after connect, or a browser
    // that omits it). Never act on absent evidence.
    return { state: st, action: 'hold', note: 'no framesPerSecond in sender stats' };
  }

  // Any cpu-limited sample -- at whatever frame rate -- is fresh evidence
  // that the encoder is the bottleneck, and is what keeps an existing step
  // down justified (ADAPT_EVIDENCE_TTL_MS). Recorded before the thresholds
  // below so a machine that is cpu-limited but still delivering (say 15 fps
  // at scale 1.5, i.e. the step WORKED) does not read as stale evidence.
  if (reason === 'cpu') {
    st.lastPressureAt = now;
  }

  if (reason === 'cpu' && fps < ADAPT_TARGET_FPS) {
    st.goodStreak = 0;
    st.badStreak += 1;
    if (st.badStreak >= ADAPT_DOWN_SAMPLES && st.index < ADAPT_MAX_STEP_INDEX) {
      st.index += 1;
      st.badStreak = 0;
      st.cooldown = ADAPT_SETTLE_SAMPLES;
      return {
        state: st,
        action: 'down',
        note: 'cpu-limited at ' + fps + 'fps (< ' + ADAPT_TARGET_FPS + '), scaling to ' + ADAPT_SCALE_STEPS[st.index],
      };
    }
    const atFloor = st.index >= ADAPT_MAX_STEP_INDEX;
    return {
      state: st,
      action: 'hold',
      note: atFloor
        ? 'cpu-limited at ' + fps + 'fps but already at the hard floor (scale ' + ADAPT_SCALE_STEPS[st.index] + ')'
        : 'cpu-limited at ' + fps + 'fps, ' + st.badStreak + '/' + ADAPT_DOWN_SAMPLES + ' samples',
    };
  }

  if (reason !== 'cpu' && fps >= ADAPT_RESTORE_FPS) {
    st.badStreak = 0;
    st.goodStreak += 1;
    if (st.goodStreak >= ADAPT_UP_SAMPLES && st.index > 0) {
      st.index -= 1;
      st.goodStreak = 0;
      st.cooldown = ADAPT_SETTLE_SAMPLES;
      return {
        state: st,
        action: 'up',
        note: 'headroom at ' + fps + 'fps (>= ' + ADAPT_RESTORE_FPS + '), restoring scale ' + ADAPT_SCALE_STEPS[st.index],
      };
    }
    return { state: st, action: 'hold', note: 'headroom at ' + fps + 'fps, ' + st.goodStreak + '/' + ADAPT_UP_SAMPLES + ' samples' };
  }

  st.badStreak = 0;
  st.goodStreak = 0;

  // Stale-evidence probe (round-2 F2). Nothing above fired, so this sample is
  // neither pressure nor headroom -- the ordinary reading for a page that has
  // stopped moving, which is exactly the reading a boot-warmed capture with
  // no viewer produces for minutes on end. If the picture is still shrunk on
  // the strength of evidence this old, give a step back and re-test rather
  // than waiting for 24 fps that a static page can never deliver.
  if (st.index > 0 && now - st.lastPressureAt >= ADAPT_EVIDENCE_TTL_MS) {
    st.index -= 1;
    st.goodStreak = 0;
    st.cooldown = ADAPT_SETTLE_SAMPLES;
    // Restart the clock so a probe is never taken twice in a row on the same
    // stale reading: the next one is due another full TTL from now.
    st.lastPressureAt = now;
    return {
      state: st,
      action: 'up',
      note:
        'no cpu-limited sample for ' +
        ADAPT_EVIDENCE_TTL_MS +
        'ms (' +
        (reason || 'unknown') +
        ' at ' +
        fps +
        'fps), re-testing scale ' +
        ADAPT_SCALE_STEPS[st.index],
    };
  }

  return { state: st, action: 'hold', note: 'neutral (' + (reason || 'unknown') + ' at ' + fps + 'fps)' };
}

// adaptCarryOverIndex decides what a capture REBUILD (recapture: viewport
// resize, tab change, or the gateway's own boot-warm handover) inherits from
// the cycle that just ended. It is the round-2 F2 fix, and it is
// deliberately pure so the rule can be exercised without a browser.
//
// Two rules, both about whether the learned scale is evidence about the
// stream a VIEWER is now watching:
//
//  1. The state learned by a page's FIRST capture is never inherited. That
//     capture is the one most likely to have been measured under
//     unrepresentative conditions -- gateway boot, Chrome launch and
//     extension load all competing for the same cores -- and, for a capture
//     the gateway warmed at boot (tools.browser.warm_capture_at_boot), it is
//     measured with NOBODY WATCHING. An unwatched warm-up that software-
//     encodes a static page on a 2-core hosted box can reach the hard floor
//     within ~8 seconds; without this rule the user's first panel open would
//     then render at a QUARTER of the pixels on the strength of a
//     measurement no human ever saw, and would need 20+ seconds of >= 24 fps
//     samples to climb back. Re-measuring costs at most ~4s of re-collapse
//     if the pressure is real.
//  2. Every later capture's state is inherited only while its evidence is
//     fresh (ADAPT_EVIDENCE_TTL_MS). This is what preserves the round-1
//     property that matters: dragging the panel fires recaptures seconds
//     apart on a cpu-limited box, and re-collapsing from 1 fps on every drag
//     frame is precisely what the carry-over exists to prevent.
function adaptCarryOverIndex(prev, cycleCount, now) {
  const index = clampAdaptIndex(prev && prev.index);
  if (index === 0) return 0;
  if (!(cycleCount > 1)) return 0;
  now = typeof now === 'number' && isFinite(now) ? now : Date.now();
  const last = (prev && prev.lastPressureAt) || 0;
  if (now - last >= ADAPT_EVIDENCE_TTL_MS) return 0;
  return index;
}

// readVideoSenderSample pulls the two fields the loop needs off the SENDER's
// own stats -- qualityLimitationReason is the encoder telling us, in its own
// words, that CPU is what is holding quality back; framesPerSecond is what
// the viewer actually gets. No gateway round-trip is involved.
async function readVideoSenderSample(pc) {
  const sender = pc.getSenders().find((s) => s.track && s.track.kind === 'video');
  if (!sender || typeof sender.getStats !== 'function') return null;
  const report = await sender.getStats();
  if (!report || typeof report.forEach !== 'function') return null;
  let out = null;
  report.forEach((s) => {
    if (s && s.type === 'outbound-rtp' && (s.kind === 'video' || s.mediaType === 'video')) out = s;
  });
  if (!out) return null;
  return {
    framesPerSecond: out.framesPerSecond,
    qualityLimitationReason: out.qualityLimitationReason,
    frameWidth: out.frameWidth,
    frameHeight: out.frameHeight,
  };
}

async function applyAdaptScale(pc, scale) {
  const sender = pc.getSenders().find((s) => s.track && s.track.kind === 'video');
  if (!sender) throw new Error('no video sender');
  // Queued for the same reason as applyVideoSenderConstraints: this loop runs
  // every 2s and would otherwise interleave with the post-answer /
  // post-connected applies and invalidate their transaction (or theirs, ours).
  return queueSenderParams(async function () {
    const params = sender.getParameters();
    if (!encodingsNegotiated(params)) {
      throw new Error('encodings not negotiated');
    }
    params.encodings[0].scaleResolutionDownBy = scale;
    await sender.setParameters(params);
  });
}

// ADAPT_REPORT_MIN_INTERVAL_MS throttles the server-side failure report
// below. A sender that rejects setParameters once usually rejects it every
// time, and the loop re-tries every 2s -- unthrottled that is a frame every
// 2s forever on a socket whose only other traffic is a 15s heartbeat.
const ADAPT_REPORT_MIN_INTERVAL_MS = 30000;
let lastAdaptReportAt = 0;

// reportAdaptFailure pushes an encoder-side quality-adaptation failure to the
// GATEWAY, over the ingest WebSocket this page already holds open (round-2
// finding F7).
//
// It rides the existing browser_capture_control{action:'ping'} beacon rather
// than inventing a frame type, because the frame's `reason` field is exactly
// this ("optional human-readable context for the action", BrowserCaptureControl
// Frame.yaml) and because a new action would be a wire-contract change the
// encoder cannot make unilaterally -- generated Go/TS types and the inbound
// schema validator all have to move together (Constraint #8). An extra ping
// is semantically harmless: the server's only reaction to one is to refresh
// its liveness timestamp, and this page IS alive.
//
// The gateway half landed with this one: a ping CARRYING a reason is logged
// at WARN ("capture-ingest: encoder reported a stream-quality failure", in
// pkg/gateway/browser_webrtc.go's capture-ingest read loop), while a bare
// liveness ping stays silent. WARN because production runs at warn, so an
// INFO line would reach the process and still never reach the operator.
function reportAdaptFailure(msg) {
  const now = Date.now();
  if (now - lastAdaptReportAt < ADAPT_REPORT_MIN_INTERVAL_MS) return;
  lastAdaptReportAt = now;
  // 512 is the schema's maxLength for `reason`; an over-long frame would be
  // dropped by the gateway's inbound validator, which would turn the report
  // itself into another silent failure.
  const reason = ('quality-adapt apply failed: ' + String(msg)).slice(0, 512);
  sendFrame({ type: 'browser_capture_control', action: 'ping', reason: reason });
}

// adaptTick is one iteration of the closed loop. pcOverride exists so the
// off-browser harness can drive a fake PeerConnection through the REAL wiring
// (stats -> decision -> setParameters), not just the pure policy above.
async function adaptTick(pcOverride, nowOverride) {
  if (shuttingDown) return null;
  const pc = pcOverride || currentPC;
  if (!pc) return null;
  if (!pcOverride && pc.connectionState !== 'connected') return null;

  const sample = await readVideoSenderSample(pc);
  if (!sample) return null;

  const before = adaptState.index;
  const decision = qualityAdaptDecide(sample, adaptState, nowOverride);
  adaptState = decision.state;

  if (decision.action === 'down' || decision.action === 'up') {
    const scale = ADAPT_SCALE_STEPS[adaptState.index];
    record('qualityAdapt: step ' + decision.action + ' -> scaleResolutionDownBy ' + scale + ' (' + decision.note + ')');
    try {
      await applyAdaptScale(pc, scale);
      record('qualityAdapt: scaleResolutionDownBy ' + scale + ' applied');
    } catch (e) {
      // Keep the recorded index honest: if setParameters did not take, the
      // stream is still at the OLD scale and the loop must not believe
      // otherwise (or it would never retry, and would later "restore" to a
      // scale that was never applied).
      adaptState.index = before;
      const msg = 'qualityAdapt: setParameters failed, staying at scale ' + ADAPT_SCALE_STEPS[before] + ': ' + String(e);
      warn(msg, e);
      window.__omnipusState.lastError = msg;
      // Round-2 finding F7: an adaptation that does not APPLY is the one
      // failure of this loop nobody outside this page could ever learn
      // about. record()/warn() reach console.log, and nothing forwards the
      // extension page's console anywhere -- so on a hosted box the picture
      // would stay collapsed at 1 fps while every layer reported success,
      // the exact "green but broken" shape this whole file's other fixes
      // exist to close. Push it to the gateway over the ingest socket.
      reportAdaptFailure(msg);
    }
  }

  window.__omnipusState.qualityAdapt = {
    scale: ADAPT_SCALE_STEPS[clampAdaptIndex(adaptState.index)],
    index: adaptState.index,
    action: decision.action,
    note: decision.note,
    // How long ago the encoder last reported CPU as the limiting factor, and
    // how many capture rebuilds this page has been through -- the two inputs
    // to "is the current scale still justified" (adaptCarryOverIndex).
    pressureAgeMs: adaptState.lastPressureAt ? Date.now() - adaptState.lastPressureAt : null,
    cycle: adaptCycleCount,
    fps: sample.framesPerSecond,
    qualityLimitationReason: sample.qualityLimitationReason,
    frameWidth: sample.frameWidth,
    frameHeight: sample.frameHeight,
    at: Date.now(),
  };
  return decision.action;
}

function startQualityAdaptLoop() {
  if (adaptTimer) return;
  noteAdaptCycleStarted();
  adaptTimer = setInterval(() => {
    adaptTick().catch((e) => warn('qualityAdapt: tick failed', e));
  }, ADAPT_POLL_MS);
  record('qualityAdapt: loop started (poll ' + ADAPT_POLL_MS + 'ms, target ' + ADAPT_TARGET_FPS + 'fps, floor scale ' + ADAPT_SCALE_STEPS[ADAPT_MAX_STEP_INDEX] + ')');
}

function stopQualityAdaptLoop() {
  if (adaptTimer) {
    clearInterval(adaptTimer);
    adaptTimer = null;
  }
  const now = Date.now();
  const carried = adaptCarryOverIndex(adaptState, adaptCycleCount, now);
  if (carried !== clampAdaptIndex(adaptState.index)) {
    record(
      'qualityAdapt: capture rebuild starts from scale ' +
        ADAPT_SCALE_STEPS[carried] +
        ' (was ' +
        ADAPT_SCALE_STEPS[clampAdaptIndex(adaptState.index)] +
        '; ' +
        (adaptCycleCount <= 1
          ? 'first capture of this page — it may have had no viewer (boot warm-up)'
          : 'evidence older than ' + ADAPT_EVIDENCE_TTL_MS + 'ms') +
        ')'
    );
  }
  // Carry (or drop) the learned index per adaptCarryOverIndex, drop the
  // streaks, and make the first sample of the next cycle a settle sample, so
  // a decision is never made on stats straddling two different captures.
  adaptState = {
    index: carried,
    badStreak: 0,
    goodStreak: 0,
    cooldown: ADAPT_SETTLE_SAMPLES,
    // A dropped index must drop its evidence with it, or the very first
    // stale-evidence probe of the next cycle would be computed against a
    // timestamp belonging to a scale that is no longer applied.
    lastPressureAt: carried === 0 ? 0 : adaptState.lastPressureAt || 0,
  };
}

// Debug/verification surface only -- never part of the wire protocol, same
// contract as window.__omnipusState. The off-browser harness drives the loop
// through this.
window.__omnipusQualityAdapt = {
  decide: qualityAdaptDecide,
  tick: adaptTick,
  scale: currentAdaptScale,
  steps: ADAPT_SCALE_STEPS,
  carryOver: adaptCarryOverIndex,
  // endCycle drives the REAL teardown-time carry-over decision (the one
  // teardownCapture takes on every recapture), so the off-browser harness can
  // prove what a rebuild inherits without a browser, a WebRTC stack or a
  // relay. Same function the production path calls -- not a re-implementation.
  endCycle: function () {
    stopQualityAdaptLoop();
  },
  // beginCycle is the counting half of the same production wiring
  // (startQualityAdaptLoop calls noteAdaptCycleStarted on the PC's first
  // 'connected'). Exposed separately so the off-browser harness can advance
  // the cycle without starting a real 2s interval that would keep the harness
  // process alive.
  beginCycle: noteAdaptCycleStarted,
  cycles: function () {
    return adaptCycleCount;
  },
  state: function () {
    return adaptState;
  },
  reset: function () {
    adaptState = adaptInitialState();
    adaptCycleCount = 0;
    lastAdaptReportAt = 0;
  },
  constants: {
    pollMs: ADAPT_POLL_MS,
    targetFps: ADAPT_TARGET_FPS,
    restoreFps: ADAPT_RESTORE_FPS,
    downSamples: ADAPT_DOWN_SAMPLES,
    upSamples: ADAPT_UP_SAMPLES,
    settleSamples: ADAPT_SETTLE_SAMPLES,
    maxScale: ADAPT_SCALE_STEPS[ADAPT_MAX_STEP_INDEX],
    evidenceTtlMs: ADAPT_EVIDENCE_TTL_MS,
    reportMinIntervalMs: ADAPT_REPORT_MIN_INTERVAL_MS,
  },
};

// mungeVideoStartBitrate appends libwebrtc's bitrate-hint fmtp parameters
// (x-google-start-bitrate/min-bitrate/max-bitrate, kbps) to whichever video
// codec is ACTUALLY negotiated/preferred in a freshly-created offer, BEFORE
// it is handed to setLocalDescription.
//
// Live evidence (UAT v24, 2026-07-31,
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md fault 2,
// fix-wave follow-up): even with the applyVideoSenderConstraints re-apply
// fix above landed, the delivered stream still sat at 228x246 for a full
// 60s before stepping to 307x328 (exactly tab/2, tab is 615x657) and
// holding there -- a textbook conservative bandwidth-estimation cold-start
// ramp, not the resolution-collapse-under-pressure symptom that fix
// addresses. This encoder's PeerConnection talks to the gateway's pion
// ingest over LOOPBACK -- there is no real, lossy network path to probe
// caution against, so spending 60+ seconds ramping up is pure waste on this
// deployment topology. x-google-start-bitrate tells libwebrtc's encoder to
// skip the slow-start climb and begin at the given rate; the paired
// min/max hints bound the adaptive range it can move within afterward --
// the RTCRtpSender.maxBitrate set in applyVideoSenderConstraints remains
// the authoritative hard ceiling regardless.
//
// FIX-WAVE F4 (external review, 2026-08-13): this function used to locate
// its target payload type with a VP8-only regex
// (/^a=rtpmap:(\d+) VP8\/90000/i) and leave the SDP untouched on any other
// codec. runCaptureAndOfferOnce's H264-preference block (landed the same
// fix-wave that raised DEFAULT_MAX_VIDEO_BITRATE_BPS, see its own comment)
// calls setCodecPreferences to put H264 first on every VideoToolbox host --
// but setCodecPreferences REORDERS the codec list inside the m=video line,
// it does not remove the non-preferred codecs' rtpmap/fmtp lines from the
// SDP. So the VP8-only regex kept matching VP8's rtpmap line even once VP8
// was demoted to a non-preferred fallback, and "succeeded" silently: the
// bitrate hint landed on a payload type Chrome was never going to encode
// with, while H264 -- the codec actually negotiated -- got no hint at all
// and paid the full 60s conservative ramp on the path this fix-wave makes
// primary, with nothing logging the mismatch.
//
// Fix: identify the preferred payload type from the m=video line's OWN
// ordering (SDP semantics -- the first payload type listed after the proto
// token is the most-preferred one, exactly what setCodecPreferences
// reorders), then find THAT payload type's rtpmap/fmtp regardless of codec
// name. This covers H264, VP8, or any future preferred codec with one code
// path instead of a per-codec regex. A genuine miss (m=video line
// unparseable, or no rtpmap found for the preferred payload type) sets
// window.__omnipusState.lastError in addition to warn()ing, so the failure
// is observable on the debug surface CDP verification reads, not
// console-only.
//
// These are libwebrtc-specific SDP fmtp hints (Chrome's own dialect) --
// harmless everywhere else, since a non-libwebrtc implementation on the
// other end simply ignores fmtp parameters it doesn't recognize rather than
// rejecting them.
function mungeVideoStartBitrate(sdp) {
  const START_KBPS = 4000;
  const MIN_KBPS = 1000;
  const MAX_KBPS = 6000;
  const HINTS = 'x-google-start-bitrate=' + START_KBPS + ';x-google-min-bitrate=' + MIN_KBPS + ';x-google-max-bitrate=' + MAX_KBPS;

  const lines = sdp.split('\r\n');

  // Payload type numbers are only unique WITHIN an m-section, not across
  // the whole SDP, so the video m-section's boundaries must be located
  // first -- otherwise a payload type that happens to collide with an
  // unrelated audio payload type number could get the wrong line munged.
  let videoStart = -1;
  let videoEnd = lines.length;
  for (let i = 0; i < lines.length; i++) {
    if (videoStart === -1 && lines[i].indexOf('m=video') === 0) {
      videoStart = i;
      continue;
    }
    if (videoStart !== -1 && lines[i].indexOf('m=') === 0) {
      videoEnd = i;
      break;
    }
  }
  if (videoStart === -1) {
    warn('mungeVideoStartBitrate: no m=video section found, leaving SDP untouched');
    return sdp;
  }

  // m=video line shape: "m=video <port> <proto> <fmt> [<fmt> ...]" -- the
  // FIRST <fmt> token (index 3 after splitting on spaces) is the
  // most-preferred payload type per SDP semantics, and it is exactly what
  // setCodecPreferences reorders. Using it directly (rather than a
  // hardcoded codec name) is what makes this function codec-agnostic.
  const mLineParts = lines[videoStart].split(' ');
  const preferredPt = mLineParts.length > 3 ? mLineParts[3] : null;
  if (!preferredPt) {
    const msg = 'mungeVideoStartBitrate: could not parse a preferred payload type from the m=video line, leaving SDP untouched';
    warn(msg);
    window.__omnipusState.lastError = msg;
    return sdp;
  }

  // Find the preferred payload type's rtpmap line within the video section
  // only, whatever codec it names.
  const rtpmapRe = new RegExp('^a=rtpmap:' + preferredPt + ' ([A-Za-z0-9-]+)/');
  let rtpmapIdx = -1;
  let codecName = null;
  for (let i = videoStart; i < videoEnd; i++) {
    const m = lines[i].match(rtpmapRe);
    if (m) {
      rtpmapIdx = i;
      codecName = m[1];
      break;
    }
  }
  if (rtpmapIdx === -1) {
    const msg = 'mungeVideoStartBitrate: no rtpmap found for preferred payload type ' + preferredPt + ', leaving SDP untouched';
    warn(msg);
    window.__omnipusState.lastError = msg;
    return sdp;
  }

  // Append to an existing a=fmtp line for this payload type if present,
  // otherwise insert a fresh one directly after the rtpmap line.
  const fmtpPrefix = 'a=fmtp:' + preferredPt + ' ';
  let fmtpIdx = -1;
  for (let i = videoStart; i < videoEnd; i++) {
    if (lines[i].indexOf(fmtpPrefix) === 0) {
      fmtpIdx = i;
      break;
    }
  }

  if (fmtpIdx !== -1) {
    lines[fmtpIdx] = lines[fmtpIdx] + ';' + HINTS;
  } else {
    lines.splice(rtpmapIdx + 1, 0, fmtpPrefix + HINTS);
  }

  record('mungeVideoStartBitrate: applied start-bitrate hint to preferred codec ' + codecName + ' (pt=' + preferredPt + ')');
  return lines.join('\r\n');
}

function teardownCapture() {
  clearOfferAnswerTimeout();
  // Stop sampling BEFORE the PC goes away, so a tick in flight cannot call
  // getStats()/setParameters() on a closing sender. Whether the learned scale
  // survives into the next cycle is adaptCarryOverIndex's decision (fresh
  // evidence carries over so a panel drag does not re-collapse to 1fps on
  // every frame; a first rebuild, or evidence older than the TTL, starts the
  // viewer at full quality).
  stopQualityAdaptLoop();
  try {
    if (currentPC) currentPC.close();
  } catch (e) {
    warn('teardownCapture: pc.close() failed', e);
  }
  try {
    if (currentStream) currentStream.getTracks().forEach((t) => t.stop());
  } catch (e) {
    warn('teardownCapture: track stop failed', e);
  }
  currentPC = null;
  currentStream = null;
}

// waitIceGatheringComplete blocks until the offer is fully gathered
// (non-trickle ICE, per wave-plan decision #6), with a safety timeout so a
// stalled ICE gatherer can't hang the encoder forever.
function waitIceGatheringComplete(pc) {
  return new Promise((resolve) => {
    if (pc.iceGatheringState === 'complete') {
      resolve();
      return;
    }
    const check = () => {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check);
        clearTimeout(timer);
        resolve();
      }
    };
    pc.addEventListener('icegatheringstatechange', check);
    const timer = setTimeout(() => {
      pc.removeEventListener('icegatheringstatechange', check);
      resolve();
    }, 10000);
  });
}

async function captureActiveTabStream() {
  const tabId = await findActiveTargetTab();
  record('captureActiveTabStream: targetTabId=' + tabId);

  const streamId = await chrome.tabCapture.getMediaStreamId({ targetTabId: tabId });
  record('captureActiveTabStream: got streamId');

  // W3 e2e finding: WITHOUT explicit size constraints, tabCapture delivers
  // its default 4:3 (800x600) frame and LETTERBOXES the (16:9, 1280x720)
  // tab viewport into it — black bars top/bottom, and the viewer's
  // videoWidth/Height no longer matches the page's CSS pixel space, which
  // breaks the wave-plan key-decision-8 assumption ("tab capture delivers
  // device pixels of the tab viewport, page_scale 1.0") that the SPA's
  // click/coordinate mapping relies on. Pin min==max to the captured tab's
  // own viewport size (chrome.tabs.get(...).width/height, CSS px — DPR is 1
  // in the managed headless Chrome) so the capture surface IS the viewport,
  // 1:1, no bars. Fallback 1280x720 matches the coordinator's
  // --window-size default.
  let capW = 1280;
  let capH = 720;

  // expectedCaptureDims (2026-07-31 follow-up,
  // docs/internal/browser-viewport-input-rootcause-2026-07-31.md): consumed
  // and cleared here, exactly once per capture cycle. A viewport-resize
  // recapture carries the gateway's own CDP-verified Page.getLayoutMetrics
  // read-back through CaptureSession.RecaptureAt -> browser_capture_control
  // {expected_width, expected_height} -- the ONLY truth that actually proves
  // what the tab's CSS viewport is at this instant (see live.go's
  // SetViewport doc comment). Two independent failure modes measured live on
  // 2026-07-31 motivate polling against this KNOWN target instead of the
  // plain "two consecutive reads agree with EACH OTHER" strategy the `else`
  // branch below still uses when no such hint is available:
  //   (1) mid-reflow race: a single chrome.tabs.get read during the
  //       window-bounds reflow can catch a transitional size (measured:
  //       stream stuck at 615x766 while the settled, CDP-verified viewport
  //       was already 615x744).
  //   (2) two-agreeing-STALE-reads: the "poll until two consecutive reads
  //       agree" strategy is fooled when chrome.tabs.get simply LAGS the
  //       real window resize entirely -- two stale reads agree with each
  //       other just as readily as two settled ones, so that poll exits
  //       "successfully" pinned to the OLD geometry (measured: capture stuck
  //       at the 1278x632 launch size while the tab was CDP-verified at
  //       615x744 in the SAME second).
  // Converging chrome.tabs.get onto the expected value closes both; if it
  // never gets there within the poll budget, the CDP-verified expected
  // value is trusted directly rather than whatever (possibly still-stale)
  // size chrome.tabs.get last reported.
  const expected = expectedCaptureDims;
  expectedCaptureDims = null;

  if (expected) {
    const TOLERANCE_PX = 8;
    const MAX_TRIES = 16;
    const POLL_INTERVAL_MS = 150;
    let converged = false;
    try {
      for (let attempt = 0; attempt < MAX_TRIES; attempt++) {
        const tab = await chrome.tabs.get(tabId);
        if (
          tab &&
          tab.width &&
          tab.height &&
          Math.abs(tab.width - expected.w) <= TOLERANCE_PX &&
          Math.abs(tab.height - expected.h) <= TOLERANCE_PX
        ) {
          capW = tab.width;
          capH = tab.height;
          converged = true;
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
      }
    } catch (e) {
      warn(
        'captureActiveTabStream: chrome.tabs.get failed while converging on expected dims, using expected values directly',
        e
      );
    }
    if (!converged) {
      // Timeout (or a thrown chrome.tabs.get): the CDP-verified expected
      // values are still the best truth available -- use them directly
      // rather than whatever stale/transitional size chrome.tabs.get last
      // reported.
      warn(
        'captureActiveTabStream: chrome.tabs.get did not converge on expected ' +
          expected.w +
          'x' +
          expected.h +
          ' within ' +
          MAX_TRIES +
          ' tries, using the expected (CDP-verified) values directly'
      );
      capW = expected.w;
      capH = expected.h;
    }
  } else {
    try {
      // Poll until two consecutive reads agree (bounded) — the fallback
      // strategy for a recapture with no CDP-verified hint (e.g. an
      // active-tab switch, live.go's onTabsChanged -> cs.Recapture()). A
      // recapture fires right after SetViewport, and the chrome-delta
      // COMPENSATION step there (live.go, 2026-07-31) applies window bounds
      // twice in quick succession — a single chrome.tabs.get here can catch
      // the tab mid-reflow and pin the whole stream to a transitional size
      // (measured live: capture stuck at 615x766 while the settled,
      // CDP-verified viewport was 615x744). Two agreeing reads 150ms apart
      // mean the reflow has settled. See the expected-dims branch above for
      // why mere self-agreement is NOT good enough once a verified target
      // exists to poll against instead.
      let prevW = 0;
      let prevH = 0;
      for (let attempt = 0; attempt < 4; attempt++) {
        const tab = await chrome.tabs.get(tabId);
        if (tab && tab.width && tab.height) {
          if (tab.width === prevW && tab.height === prevH) {
            break;
          }
          prevW = tab.width;
          prevH = tab.height;
          capW = tab.width;
          capH = tab.height;
        }
        await new Promise((resolve) => setTimeout(resolve, 150));
      }
    } catch (e) {
      warn('captureActiveTabStream: chrome.tabs.get failed, using default capture size', e);
    }
  }
  lastPinnedCapDims = { w: capW, h: capH };
  const capDims = budgetedCaptureDims(capW, capH, captureScale);
  record(
    'captureActiveTabStream: capture size ' +
      capW + 'x' + capH + ' css x' + captureScale +
      ' -> requesting ' + capDims.w + 'x' + capDims.h + ' physical' +
      (capDims.clamped ? ' (CLAMPED to the ' + CAPTURE_PIXEL_BUDGET + 'px budget)' : '')
  );

  // Self-consume: no processing constraints needed — tabCapture's defaults
  // are clean (AGC/EC/NS default OFF), unlike getDisplayMedia. See
  // wv1-spike-results.md Q2.
  capturedTabId = tabId;
  // Undo any shutdown-time local mute from a previous session of this tab:
  // while captured, local audibility is governed by the panel, not the tab.
  //
  // AWAITED (fix-wave F12, external review 2026-08-13): this call used to be
  // fire-and-forget -- chrome.tabs.update() invoked without await, inside a
  // SYNCHRONOUS try/catch that can never observe a promise rejection (MV3's
  // chrome.tabs.update returns a Promise). If the tab's mute flag was still
  // applied when getUserMedia below created the capture, the tabCapture
  // stream started SILENT -- the same rmsMean 0.30258 -> 0 failure class the
  // --mute-audio incident (see capturedTabId's own doc comment above) says
  // must never repeat, just triggered from the opposite direction
  // (unmute-not-yet-applied instead of a browser-level mute flag). Awaiting
  // means getUserMedia never starts until Chrome has actually processed the
  // unmute.
  try {
    await chrome.tabs.update(tabId, { muted: false });
  } catch (e) {
    warn('captureActiveTabStream: tab unmute failed (tab may already be closed)', e);
  }
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
    video: {
      mandatory: {
        chromeMediaSource: 'tab',
        chromeMediaSourceId: streamId,
        // capW/capH are CSS px (chrome.tabs.get and the CDP-verified hint
        // both speak CSS); the tab's compositor surface is CSS x captureScale
        // physical px. Constrain to the physical size so tabCapture does not
        // throw away the Retina pixels — see the capture_scale handler.
        minWidth: capDims.w,
        minHeight: capDims.h,
        maxWidth: capDims.w,
        maxHeight: capDims.h,
        // Frame-rate floor/ceiling. Added 2026-08-13 while chasing a
        // metronomic 2fps that turned out to be the SSRF guard silently
        // blocking the local test page (what got measured was the static
        // start page's refresh heartbeat), so this floor is NOT the fix
        // for that episode and is not load-bearing. Kept as standard
        // practice for occluded-surface capture: min_frame_rate is the
        // knob Chromium's tab capturer uses to actively request
        // compositor frames for occluded surfaces, and a headless
        // captured tab is permanently occluded.
        minFrameRate: 15,
        maxFrameRate: 30,
      },
    },
  });

  window.__omnipusState.videoTracks = stream.getVideoTracks().map((t) => ({ label: t.label, settings: t.getSettings() }));
  window.__omnipusState.audioTracks = stream.getAudioTracks().map((t) => ({ label: t.label, settings: t.getSettings() }));
  record(
    'captureActiveTabStream: MediaStream video=' +
      stream.getVideoTracks().length +
      ' audio=' +
      stream.getAudioTracks().length
  );
  return stream;
}

// DEFAULT_STUN_SERVER is the fallback used only when the gateway injecting
// this page's config predates the `stunServer` key entirely (back-compat).
const DEFAULT_STUN_SERVER = 'stun:stun.l.google.com:19302';

// resolveIceServers implements window.__omnipusCapture.stunServer's
// tri-state contract (fix-wave, LOW -- previously this hardcoded the Google
// public STUN server unconditionally, ignoring operator config):
//   - a non-empty string  -> use exactly that STUN server (operator config)
//   - an empty string (''), i.e. the key IS present but deliberately blank
//     -> host-candidates-only, no STUN server at all (iceServers: [])
//   - the key absent entirely (an older gateway build that predates this
//     config, or a verification harness that injects window.__omnipusCapture
//     without it) -> fall back to DEFAULT_STUN_SERVER, matching this file's
//     pre-fix-wave behavior exactly (back-compat).
function resolveIceServers() {
  const cfg = window.__omnipusCapture || {};
  if (typeof cfg.stunServer === 'string') {
    return cfg.stunServer === '' ? [] : [{ urls: cfg.stunServer }];
  }
  return [{ urls: DEFAULT_STUN_SERVER }];
}

function newPeerConnection() {
  const pc = new RTCPeerConnection({ iceServers: resolveIceServers() });
  // connectedConstraintsApplied is closed over per-PC (a fresh PC is created
  // for every capture/recapture cycle, see runCaptureAndOfferOnce), so this
  // guards only the FIRST 'connected' transition of THIS pc -- re-applying
  // applyVideoSenderConstraints on every later connectionstatechange
  // fluctuation (e.g. a transient disconnected->connected blip) would be
  // harmless (setParameters is idempotent) but pointless, so it's skipped.
  let connectedConstraintsApplied = false;
  pc.oniceconnectionstatechange = () => {
    window.__omnipusState.iceState = pc.iceConnectionState;
    record('ice state -> ' + pc.iceConnectionState);
    if (pc.iceConnectionState === 'connected' || pc.iceConnectionState === 'completed') {
      lastGoodIceTime = Date.now();
    }
  };
  pc.onconnectionstatechange = () => {
    window.__omnipusState.connState = pc.connectionState;
    record('connection state -> ' + pc.connectionState);
    // Second post-negotiation re-apply point (see applyVideoSenderConstraints's
    // doc comment) -- by 'connected' the transceiver is fully settled, so
    // this is the most reliable point for setParameters to actually stick.
    if (pc.connectionState === 'connected' && !connectedConstraintsApplied) {
      connectedConstraintsApplied = true;
      applyVideoSenderConstraints(pc, { context: 'post-connected', recordSuccess: true });
      // Only now is there a settled sender whose getStats() reports a real
      // qualityLimitationReason/framesPerSecond, so this is where the
      // bounded adaptation loop starts. It is stopped by teardownCapture.
      startQualityAdaptLoop();
    }
  };
  return pc;
}

// runCaptureAndOffer performs (or re-performs, for recapture) the full
// capture -> offer flow: acquire the active tab's MediaStream, build a
// fresh non-trickle offer, and send it as a browser_capture_offer frame.
// Tears down any previous PC/stream first, so it is safe to call both for
// the initial connect and for a recapture control message — matching
// wv1-spike-results.md Q3's proven "new PC with fresh SSRCs replaces the
// old one" recovery pattern rather than incremental same-PC renegotiation.
// captureInFlight serialises runCaptureAndOffer (2026-07-31, found by review
// of the adaptive-viewport feature). This function has two awaits before it
// assigns currentPC/currentStream, and it had NO in-flight guard. That was
// practically unreachable while the only trigger was an active-tab switch (a
// rare, effectively serialized event) — but the viewport feature added a
// second, client-driven, HIGH-FREQUENCY trigger: every panel resize sends
// browser_viewport, and the gateway answers with a recapture. A CaptureSession
// is shared per AGENT, so two viewers of the same tab each push their own
// geometry with no cross-viewer coordination.
//
// Two overlapping calls race the same chrome.tabCapture pipeline for one tab.
// Chrome allows only one active tabCapture stream per tab, so the loser's
// getUserMedia throws — and handleControlFrame's catch closes the WHOLE ingest
// WebSocket, tearing down a session that was otherwise fine. Even without a
// throw, the loser's PeerConnection and MediaStreamTrack are orphaned, because
// teardownCapture only ever closes whatever the globals currently point at.
//
// Coalesce rather than queue: if a recapture arrives while one is running, set
// a rerun flag and let the in-flight call loop once more when it finishes. The
// geometry we want is always the LATEST one, so collapsing N pending
// recaptures into one extra pass is both correct and cheaper.
let captureInFlight = false;
let captureRerunRequested = false;

async function runCaptureAndOffer() {
  if (captureInFlight) {
    captureRerunRequested = true;
    record('runCaptureAndOffer: already in flight, coalescing into a rerun');
    return;
  }
  captureInFlight = true;
  try {
    do {
      captureRerunRequested = false;
      await runCaptureAndOfferOnce();
    } while (captureRerunRequested);
  } finally {
    captureInFlight = false;
  }
}

async function runCaptureAndOfferOnce() {
  teardownCapture();
  setStatus('capturing');

  const stream = await captureActiveTabStream();
  currentStream = stream;

  setStatus('offering');
  const pc = newPeerConnection();
  currentPC = pc;
  stream.getTracks().forEach((t) => pc.addTrack(t, stream));
  // Prefer H.264 over VP8 (measured 2026-08-13): software VP8 at the 2x
  // capture size (1122x1416) tops out at 4-12fps on a 4-core mobile Intel -
  // the encoder, not bitrate or transport, is the ceiling (proven by
  // testufo.com runs: 'balanced' degradation removed the 3s stalls but avg
  // fps stayed ~7). H.264 engages VideoToolbox HARDWARE encode on macOS,
  // taking the encode off the CPU entirely. Best-effort: if H.264 is absent
  // from capabilities (or setCodecPreferences unsupported) the negotiation
  // falls back to the previous VP8 path untouched. The Pion relay registers
  // default codecs incl. H264, and it forwards RTP without transcoding, so
  // the preference must be expressed HERE, on the sending leg.
  try {
    const caps = RTCRtpSender.getCapabilities && RTCRtpSender.getCapabilities('video');
    if (caps && caps.codecs && caps.codecs.length) {
      const h264 = caps.codecs.filter((c) => /h264/i.test(c.mimeType));
      if (h264.length) {
        const rest = caps.codecs.filter((c) => !/h264/i.test(c.mimeType));
        const tr = pc.getTransceivers().find((x) => x.sender && x.sender.track && x.sender.track.kind === 'video');
        if (tr && tr.setCodecPreferences) {
          tr.setCodecPreferences(h264.concat(rest));
          record('codec preference: H264 first (' + h264.length + ' profiles)');
        }
      }
    }
  } catch (e) {
    warn('setCodecPreferences failed; keeping default codec order', e);
  }

  // Fix-wave finding 4: cap bitrate + set encoding hints on the video
  // sender now that addTrack has created it. Video-only (audio/Opus has no
  // equivalent overdrive risk here). This pre-negotiation call is
  // best-effort -- see applyVideoSenderConstraints's doc comment for why
  // the post-answer/post-connected re-applies below are the ones that
  // actually matter.
  const videoTrack = stream.getVideoTracks()[0];
  if (videoTrack) {
    applyVideoSenderConstraints(pc, { context: 'pre-negotiation' });
  }

  const offer = await pc.createOffer();
  offer.sdp = mungeVideoStartBitrate(offer.sdp);
  await pc.setLocalDescription(offer);
  await waitIceGatheringComplete(pc);

  record('runCaptureAndOffer: sending browser_capture_offer');
  sendFrame({ type: 'browser_capture_offer', sdp: pc.localDescription.sdp });
  armOfferAnswerTimeout();
}

// ---- signaling ----------------------------------------------------------------

function sendFrame(frame) {
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    warn('sendFrame: WS not open, dropping frame', frame.type);
    return;
  }
  ws.send(JSON.stringify(frame));
}

// handleControlFrame handles the SERVER -> CLIENT control actions
// (recapture, shutdown, adapt_reset, set_bitrate). `ping` is this page's own CLIENT -> SERVER health
// beacon (see startPingBeacon) — the gateway never sends ping to us, and
// there is no `pong` action in the schema, so neither is handled as an
// inbound case here.
async function handleControlFrame(msg) {
  const action = msg.action;
  record('control frame: action=' + action + (msg.reason ? ' reason=' + msg.reason : ''));

  if (action === 'set_bitrate') {
    const bps = typeof msg.max_bitrate === 'number' && isFinite(msg.max_bitrate) ? msg.max_bitrate : 0;
    if (bps > 0) {
      viewerBitrateCeiling = bps;
      record('viewer bitrate ceiling set to ' + bps + ' bps');
      if (currentPC) {
        applyVideoSenderConstraints(currentPC, { context: 'set-bitrate', recordSuccess: true });
      }
    }
    return;
  }

  if (action === 'adapt_reset') {
    // A boot-warmed capture is being handed to its FIRST real viewer. The
    // resolution the adaptation loop settled on while nobody was watching is
    // not evidence about this viewer, so start them at full quality.
    //
    // Deliberately NOT a recapture: tearing the capture down and rebuilding it
    // measured ~17s to first frame against ~4s without (hosted box,
    // 2026-08-17), which is what made keeping a warm capture alive worse than
    // not having one. Resetting the adaptation state and re-applying the
    // sender constraints achieves the same "start clean" guarantee with no
    // rebuild and no visible blip.
    adaptState = adaptInitialState();
    adaptCycleCount = 0;
    if (currentPC) {
      applyVideoSenderConstraints(currentPC, { context: 'adapt-reset', recordSuccess: true });
    }
    record('adapt reset for viewer handover: scale restored to ' + currentAdaptScale());
    return;
  }

  if (action === 'recapture') {
    // Read expected_width/expected_height off the frame BEFORE kicking off
    // runCaptureAndOffer -- captureActiveTabStream (called from inside that
    // chain) is what actually consumes and clears expectedCaptureDims. Both
    // fields must be present and numeric; anything else (either omitted, or
    // an older/malformed server) means "no CDP-verified hint" and this
    // recapture falls back to the historical two-agreeing-reads poll.
    expectedCaptureDims =
      typeof msg.expected_width === 'number' && typeof msg.expected_height === 'number'
        ? { w: msg.expected_width, h: msg.expected_height }
        : null;
    // Physical-pixel capture (blur fix, macOS 2026-08-12): the tab renders at
    // this deviceScaleFactor (Emulation override, driven by the controlling
    // viewer's devicePixelRatio), so the media constraints multiply by it —
    // otherwise tabCapture downscales the 2x compositor surface to CSS pixels
    // and every Retina viewer gets a 1x frame stretched over 2x display
    // pixels. Clamped to [1,4] mirroring the contract.
    //
    // ABSENT MEANS 1, NOT "unchanged" (fix-wave F3, external review
    // 2026-08-13 -- reverses this field's prior "sticky across recaptures"
    // contract). A CaptureSession is shared per AGENT, so a viewer dragging
    // the panel from a Retina (DPR 2) monitor to a non-Retina one -- or a
    // SECOND viewer on the same session at DPR 1 -- triggers a recapture
    // whose capture_scale field the server sends only when scale > 1.
    // Treating "field absent" as "leave captureScale wherever it last was"
    // pinned the encoder at 2x forever in that case: 4x the pixels against
    // applyVideoSenderConstraints' scale^2 bitrate ceiling, on a tab now
    // rendering at 1x -- exactly the CPU-encode load this file's other
    // fixes exist to reduce. The server is being made to send this field
    // unconditionally too, but this side must be correct on its own
    // regardless of what the server does.
    captureScale =
      typeof msg.capture_scale === 'number' && isFinite(msg.capture_scale) ? Math.min(4, Math.max(1, msg.capture_scale)) : 1;
    record('control frame: capture_scale=' + captureScale);
    // Each SERVER-initiated recapture gets one post-connect self-heal check;
    // a self-heal's own recapture deliberately does not re-arm this (no loop).
    selfHealBudget = 3;
    const forWs = ws;
    try {
      await runCaptureAndOffer();
    } catch (e) {
      window.__omnipusState.lastError = String(e);
      record('recapture FAILED: ' + e);
      // fix-wave (HIGH): without closing the socket here, a failed
      // recapture left the ping beacon running forever with no way for the
      // gateway to ever learn capture died -- the panel would freeze
      // permanently (never recovers, never retries). Closing routes through
      // the SAME 'close' handler every other disconnect uses, so the
      // existing scheduleReconnect backoff/reconnect loop -- the documented
      // recovery path -- is what actually recovers; no new recovery path is
      // introduced. Guard against closing a newer socket, matching
      // armOfferAnswerTimeout's forWs pattern above, in case a reconnect
      // already replaced `ws` while this recapture attempt was in flight.
      if (ws === forWs) {
        try {
          forWs.close();
        } catch (closeErr) {
          /* ignore */
        }
      }
    }
    return;
  }

  if (action === 'shutdown') {
    shuttingDown = true;
    setStatus('shutdown');
    stopWatchdog();
    stopPingBeacon();
    teardownCapture();
    // Phantom-audio fix (live report, macOS 2026-08-13): the human closed
    // the panel, every viewer detached, the grace timer stopped the capture
    // — and the captured tab KEPT PLAYING, audibly, from a browser with no
    // visible window. Shutdown now tab-mutes the captured tab (local output
    // only; there is no capture left to affect). The next capture start
    // unmutes it symmetrically.
    if (capturedTabId != null) {
      // AWAITED (fix-wave F12, external review 2026-08-13): chrome.tabs.update
      // returns a Promise in MV3, so the previous synchronous try/catch never
      // caught anything -- a closed tab produced an UNHANDLED promise
      // rejection while this line's record() unconditionally logged
      // "shutdown: tab-muted" regardless of whether the mute actually
      // applied. Awaiting makes the rejection real and catchable, so success
      // and failure are both logged honestly.
      try {
        await chrome.tabs.update(capturedTabId, { muted: true });
        record('shutdown: tab-muted ' + capturedTabId);
      } catch (e) {
        warn('shutdown tab-mute failed', e);
        record('shutdown: tab-mute FAILED for ' + capturedTabId + ': ' + e);
      }
    }
    try {
      ws.close();
    } catch (e) {
      /* ignore */
    }
    return;
  }

  warn('unexpected control action from server, ignoring:', action);
}

async function handleWsMessage(raw) {
  let msg;
  try {
    msg = JSON.parse(raw);
  } catch (e) {
    warn('bad frame JSON, ignoring:', e);
    return;
  }

  switch (msg.type) {
    case 'browser_capture_answer':
      // Clear the offer-answer timeout as soon as a response arrives at
      // all -- even if setRemoteDescription below then fails, that's a
      // different (already-logged) failure class, not the "silently
      // wedged, no server response" case armOfferAnswerTimeout guards.
      clearOfferAnswerTimeout();
      if (!currentPC) {
        warn('browser_capture_answer received with no active PeerConnection, ignoring');
        return;
      }
      // A second answer on a PC that is already stable is what froze the
      // 2026-08-16 ui-heavy live-video run: Chrome logged
      // "Failed to set remote answer sdp: Called in wrong state: stable",
      // DTLS never started, and ingest ICE failed 30s later. Recapture /
      // viewport / self-heal can deliver a late answer for the previous
      // offer; applying it does not recover and can stall the current
      // sender. Only an answer that completes the offer we just sent is
      // legal.
      if (currentPC.signalingState && currentPC.signalingState !== 'have-local-offer') {
        warn('browser_capture_answer ignored: signalingState=' + currentPC.signalingState + ' (want have-local-offer)');
        record('ignored stale browser_capture_answer in state ' + currentPC.signalingState);
        return;
      }
      try {
        await currentPC.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
        setStatus('connected');
        lastGoodIceTime = Date.now();
        record('applied browser_capture_answer, PC connecting');
        if (selfHealBudget > 0) {
          selfHealBudget--;
          const healPC = currentPC;
          setTimeout(async () => {
            if (currentPC !== healPC || shuttingDown || !lastPinnedCapDims) return;
            try {
              const tabId = await findActiveTargetTab();
              const tab = await chrome.tabs.get(tabId);
              if (
                tab && tab.width && tab.height &&
                (Math.abs(tab.width - lastPinnedCapDims.w) > 8 || Math.abs(tab.height - lastPinnedCapDims.h) > 8)
              ) {
                record('self-heal: pinned ' + lastPinnedCapDims.w + 'x' + lastPinnedCapDims.h + ' drifted from tab ' + tab.width + 'x' + tab.height + ' — recapturing once');
                expectedCaptureDims = { w: tab.width, h: tab.height };
                await runCaptureAndOffer();
              }
            } catch (e) {
              warn('self-heal check failed', e);
            }
          }, 1200);
        }
        // First post-negotiation re-apply point (see
        // applyVideoSenderConstraints's doc comment): the remote answer is
        // now applied, so the transceiver's direction/codecs are settled
        // even though ICE/DTLS may still be finishing up. A second
        // re-apply follows at pc.connectionState === 'connected'
        // (newPeerConnection's onconnectionstatechange) for whichever
        // browser needs the transport fully up before setParameters sticks.
        applyVideoSenderConstraints(currentPC, { context: 'post-answer', recordSuccess: true });
      } catch (e) {
        window.__omnipusState.lastError = String(e);
        record('setRemoteDescription(answer) FAILED: ' + e);
      }
      break;

    case 'browser_capture_control':
      await handleControlFrame(msg);
      break;

    case 'error':
      // ErrorFrame (contracts/components/schemas/ErrorFrame.yaml) — server
      // -> client. Surface it for debugging. NOTE this is NOT what an
      // invalid hello token produces — a rejected hello closes the
      // connection with NO frame at all (see this file's header doc
      // comment: "success is silent ... the only observable signal for a
      // REJECTED hello is the WS closing"). The one actual producer of this
      // frame is a rejected browser_capture_offer: the gateway sends this
      // frame, THEN closes the connection.
      window.__omnipusState.lastError = msg.message || 'error frame with no message';
      record('server error frame: ' + window.__omnipusState.lastError);
      break;

    default:
      warn('unknown frame type, ignoring:', msg.type);
  }
}

function connectOnce(cfg) {
  setStatus('connecting');
  window.__omnipusState.wsState = 'connecting';
  ws = new WebSocket(cfg.ingestUrl);

  ws.addEventListener('open', async () => {
    window.__omnipusState.wsState = 'open';
    reconnectDelayMs = 1000; // reset backoff on a clean connect
    record('WS open, sending hello');
    setStatus('hello_sent');
    sendFrame({ type: 'browser_capture_hello', token: cfg.token, ext_version: extVersion() });
    startWatchdogOnce();
    startPingBeacon();
    const forWs = ws;

    // browser_capture_hello is client -> server only (no ack frame) — a
    // rejected token surfaces as the gateway closing the connection, not a
    // nack. Proceed straight to capture + offer; the reconnect watchdog
    // handles the rejection case via the resulting WS close.
    try {
      await runCaptureAndOffer();
    } catch (e) {
      window.__omnipusState.lastError = String(e);
      setStatus('error');
      record('initial capture FAILED: ' + e);
      // fix-wave (HIGH): without closing the socket here, an initial capture
      // failure (no capturable tab, getUserMedia denied, etc.) left the page
      // silently wedged in 'error' forever -- the ping beacon kept running
      // and the gateway had no way to learn capture never started. Closing
      // engages the SAME scheduleReconnect backoff/reconnect loop as every
      // other disconnect (the documented recovery path) -- a fresh connect
      // re-attempts capture. Guard against closing a newer socket, matching
      // armOfferAnswerTimeout's forWs pattern above.
      if (ws === forWs) {
        try {
          forWs.close();
        } catch (closeErr) {
          /* ignore */
        }
      }
    }
  });

  ws.addEventListener('message', (ev) => {
    handleWsMessage(typeof ev.data === 'string' ? ev.data : '');
  });

  ws.addEventListener('close', () => {
    window.__omnipusState.wsState = 'closed';
    record('WS closed');
    stopPingBeacon();
    if (!shuttingDown) scheduleReconnect(cfg);
  });

  ws.addEventListener('error', (e) => {
    warn('WS error', e && e.message ? e.message : e);
  });
}

// scheduleReconnect drives the "reconnect watchdog ... full restart loop
// with backoff" requirement for a dropped WS (as opposed to a bad-ICE
// restart, which forces the WS closed and lands here too).
function scheduleReconnect(cfg) {
  if (shuttingDown || reconnectTimer) return;
  setStatus('reconnecting');
  window.__omnipusState.reconnectAttempts += 1;
  record('scheduling reconnect in ' + reconnectDelayMs + 'ms');
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    teardownCapture();
    connectOnce(cfg);
  }, reconnectDelayMs);
  reconnectDelayMs = Math.min(reconnectDelayMs * 2, RECONNECT_MAX_DELAY_MS);
}

// ---- watchdog -------------------------------------------------------------

function startWatchdogOnce() {
  if (watchdogTimer) return;
  watchdogTimer = setInterval(() => {
    if (shuttingDown || !currentPC) return;
    const s = currentPC.iceConnectionState;
    const bad = s === 'failed' || s === 'disconnected' || s === 'closed';
    if (bad && Date.now() - lastGoodIceTime > ICE_BAD_GRACE_MS) {
      record('watchdog: ICE ' + s + ' for >' + ICE_BAD_GRACE_MS + 'ms, forcing full restart');
      lastGoodIceTime = Date.now(); // avoid a hot loop while the restart runs
      try {
        ws.close(); // the close handler drives the actual reconnect
      } catch (e) {
        /* ignore */
      }
    }
  }, 2000);
}

function stopWatchdog() {
  if (watchdogTimer) {
    clearInterval(watchdogTimer);
    watchdogTimer = null;
  }
}

// startPingBeacon sends this page's own CLIENT -> SERVER health/liveness
// beacon (browser_capture_control action=ping) on a fixed interval — the
// "encoder page's periodic health beacon / reconnect-watchdog signal" the
// schema describes. Distinct from the ICE-state watchdog above: this beacon
// exists so the gateway can detect a hung-but-still-WS-open encoder even
// when no ICE state change would otherwise reveal it.
function startPingBeacon() {
  if (pingBeaconTimer) return;
  pingBeaconTimer = setInterval(() => {
    if (shuttingDown) return;
    sendFrame({ type: 'browser_capture_control', action: 'ping' });
  }, PING_BEACON_INTERVAL_MS);
}

function stopPingBeacon() {
  if (pingBeaconTimer) {
    clearInterval(pingBeaconTimer);
    pingBeaconTimer = null;
  }
}

// ---- entry point ------------------------------------------------------------

// window.__omnipusStart is the single entry point. In production, config
// is present at parse time (injected via addScriptToEvaluateOnNewDocument
// before this script tag runs) so this file self-starts below. It is also
// exposed for explicit invocation — e.g. a verification harness that
// injects window.__omnipusCapture AFTER navigation, mirroring the spike's
// startEncoding() call pattern (spikes/wv1-webrtc/q4-bidir/encoder/run.js).
window.__omnipusStart = async function () {
  try {
    const cfg = readConfig();
    connectOnce(cfg);
    return { ok: true };
  } catch (e) {
    window.__omnipusState.lastError = String(e);
    setStatus('error');
    record('start FAILED: ' + e);
    return { ok: false, error: String(e) };
  }
};

if (window.__omnipusCapture) {
  window.__omnipusStart();
} else {
  record('waiting for window.__omnipusCapture to be set (call window.__omnipusStart() once ready)');
}
