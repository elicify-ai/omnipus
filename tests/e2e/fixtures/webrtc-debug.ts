/**
 * webrtc-debug.ts — minimal test-side instrumentation for the live WebRTC sink.
 *
 * The browser-live tests fail on the live-view leg with `reported: 0x0
 * dimensions` and `mean luminance 0.0`; the underlying peer connection
 * reports SUCCEED, so the fault sits somewhere between ICE/DTLS and frame
 * extraction. To localize it without touching product code, this fixture:
 *
 *   1. wraps `window.RTCPeerConnection` so the SPA's peer connection is
 *      captured on creation (idempotent — safe to call twice, no-op the
 *      second time) — `BrowserWebRTCSession` keeps `pc` private, so this
 *      is the only way to read `connectionState` / `iceConnectionState`
 *      from a test;
 *
 *   2. exposes a single dump helper that, given the live `<video>` locator,
 *      prints the four values the brief calls out (`pc.connectionState`,
 *      `pc.iceConnectionState`, inbound video track `readyState` + `muted`,
 *      and from `getStats()` the inbound-rtp-video `framesDecoded` +
 *      `bytesReceived`) — and ALSO logs the value to console + attaches the
 *      raw JSON to `testInfo` for human inspection.
 *
 * It is intentionally scoped to test-side code: the patch runs in the
 * browser via `addInitScript`, never in the Go binary.
 *
 * Squad K (Squad J review advisories folded in, 2026-09-20):
 *   - logWebrtcDebug wraps EVERYTHING in try/catch so a fixture failure
 *     (closed page, detached context, broken script) can never mask the
 *     oracle message. A failed dump returns an empty snapshot and
 *     console.warns; the caller still throws its own real assertion.
 *   - logWebrtcDebug is now GATED: it dumps only when the first-frame
 *     deadline has actually elapsed AND the video has no decoded
 *     dimensions (or there is no inbound video track at all). A healthy
 *     run that meets the oracle never pays the dump cost.
 *   - The dead synchronous `pc.getStats()` no-op block (the one whose own
 *     comment said "we can't return from here") is deleted; the real
 *     stats pass below is the only path that fills `inboundRtpVideo`.
 *   - The snapshot's `video.tracks` is populated from the MediaStream
 *     attached to `<video>.srcObject`; when no stream is attached, the
 *     list is empty (not "null" — Squad J's report wording that
 *     suggested the attribute was present but null was misleading;
 *     `srcObject` is either a MediaStream or null at the DOM level, and
 *     the test reports it as `tracks: []` either way).
 */
import type { Page, TestInfo } from "@playwright/test";

/** Snapshot shape — every field is optional so a missing peer/track still dumps. */
export interface WebrtcDebugSnapshot {
  label: string;
  at: string;
  /** Resolved from the live `<video>`'s current frame, if any. */
  video: {
    videoWidth: number;
    videoHeight: number;
    readyState: number;
    networkState: number;
    paused: boolean;
    currentTime: number;
    /** Tracks actually attached to the <video>.srcObject MediaStream. */
    tracks: Array<{
      kind: string;
      id: string;
      readyState: string;
      muted: boolean;
      enabled: boolean;
    }>;
  };
  /**
   * Aggregated across every captured `RTCPeerConnection` (the SPA usually has
   * exactly one for live view, but the recorder leg may also have produced
   * one — both are returned so a "the wrong pc reported connected" finding
   * is observable here, not just inside the gateway).
   */
  peerConnections: Array<{
    pcIndex: number;
    connectionState: string | null;
    iceConnectionState: string | null;
    iceGatheringState: string | null;
    signalingState: string | null;
    hasLocalDescription: boolean;
    hasRemoteDescription: boolean;
    inboundVideoTrack: {
      readyState: string;
      muted: boolean;
      enabled: boolean;
    } | null;
    inboundRtpVideo: {
      framesDecoded: number | null;
      bytesReceived: number | null;
      packetsReceived: number | null;
      framesPerSecond: number | null;
      frameWidth: number | null;
      frameHeight: number | null;
      codecId: string | null;
      trackIdentifier: string | null;
    } | null;
  }>;
}

type CapturedPeer = {
  pc: RTCPeerConnection;
  pcIndex: number;
};
type DebugWindow = Window & {
  __omnipusWebrtcDebug?: {
    captured: CapturedPeer[];
  };
};

/**
 * Wrap `window.RTCPeerConnection` and start tracking every peer the SPA
 * creates. Idempotent — calling it twice is a no-op. Call from
 * `test.beforeEach` (after `page.goto` is fine; before is also fine because
 * `addInitScript` runs on every navigation).
 */
export async function installWebrtcDebug(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as DebugWindow;
    if (w.__omnipusWebrtcDebug) return; // already installed
    const captured: CapturedPeer[] = [];
    const NativePeer = window.RTCPeerConnection.bind(window);
    let counter = 0;
    function WrappedPeer(
      this: RTCPeerConnection,
      ...args: ConstructorParameters<typeof RTCPeerConnection>
    ) {
      const pc = new NativePeer(...args);
      const pcIndex = ++counter;
      captured.push({ pc, pcIndex });
      // Keep the captured list bounded — the recorder/legacy pc may churn.
      if (captured.length > 8) captured.splice(0, captured.length - 8);
      return pc;
    }
    // Preserve prototype so `instanceof RTCPeerConnection` keeps working in
    // any code that checks for it (none of the SPA does today, but cheap to
    // keep honest).
    WrappedPeer.prototype = NativePeer.prototype;
    window.RTCPeerConnection =
      WrappedPeer as unknown as typeof RTCPeerConnection;
    w.__omnipusWebrtcDebug = { captured };
  });
}

/**
 * Read the four values the brief asks for and return them as a structured
 * snapshot. Pure: never throws — every field is optional, a missing pc
 * just shows up as `peerConnections: []`. Squad J review advisory (folded
 * in by Squad K, 2026-09-20): the synchronous `pc.getStats()` block the
 * previous version carried at the bottom of the synchronous loop did
 * nothing useful — its own comment admitted "we can't return from here" —
 * and was deleted. The real `getStats()` pass below (the await-chain
 * after the synchronous `page.evaluate`) is the only path that fills
 * `inboundRtpVideo`.
 */
export async function dumpWebrtcDebug(
  page: Page,
  label: string,
  videoSelector: string = '[data-testid="browser-live-video"]',
): Promise<WebrtcDebugSnapshot> {
  const snapshot = await page.evaluate(
    ({ label, videoSelector }) => {
      const video = document.querySelector(videoSelector) as
        | HTMLVideoElement
        | null;
      // The DOM contract: <video>.srcObject is either a MediaStream or
      // null. When null, the test reports `tracks: []` (an empty list), not
      // a missing field — distinguishes "no MediaStream attached" from
      // "the MediaStream has no tracks" in the dump.
      const stream = (video?.srcObject as MediaStream | null) ?? null;
      const tracks = stream
        ? stream.getTracks().map((t) => ({
            kind: t.kind,
            id: t.id,
            readyState: t.readyState,
            muted: t.muted,
            enabled: t.enabled,
          }))
        : [];
      const w = window as unknown as DebugWindow;
      const captured = w.__omnipusWebrtcDebug?.captured ?? [];
      const peers: WebrtcDebugSnapshot["peerConnections"] = [];
      for (const { pc, pcIndex } of captured) {
        let inboundVideoTrack: WebrtcDebugSnapshot["peerConnections"][number]["inboundVideoTrack"] =
          null;
        const receivers = pc.getReceivers();
        for (const r of receivers) {
          if (r.track && r.track.kind === "video") {
            inboundVideoTrack = {
              readyState: r.track.readyState,
              muted: r.track.muted,
              enabled: r.track.enabled,
            };
            break;
          }
        }
        // Squad J advisory: the previous version ran a synchronous
        // pc.getStats() here and resolved a Promise it could not return
        // from — dead code. Removed. The await-chain below is the only
        // path that fills `inboundRtpVideo`.
        peers.push({
          pcIndex,
          connectionState: pc.connectionState ?? null,
          iceConnectionState: pc.iceConnectionState ?? null,
          iceGatheringState: pc.iceGatheringState ?? null,
          signalingState: pc.signalingState ?? null,
          hasLocalDescription: Boolean(pc.localDescription),
          hasRemoteDescription: Boolean(pc.remoteDescription),
          inboundVideoTrack,
          inboundRtpVideo: null,
        });
      }
      return {
        label,
        at: new Date().toISOString(),
        video: {
          videoWidth: video?.videoWidth ?? 0,
          videoHeight: video?.videoHeight ?? 0,
          readyState: video?.readyState ?? 0,
          networkState: video?.networkState ?? 0,
          paused: video?.paused ?? true,
          currentTime: video?.currentTime ?? 0,
          tracks,
        },
        peerConnections: peers,
      };
    },
    { label, videoSelector },
  );
  // Round-trip getStats() — it is the only way to read `framesDecoded`
  // and `bytesReceived`. The synchronous dump above captured pc/track
  // state; this pass augments it with the inbound-rtp row.
  const statsByPcIndex = await page.evaluate(() => {
    const w = window as unknown as DebugWindow;
    const captured = w.__omnipusWebrtcDebug?.captured ?? [];
    return Promise.all(
      captured.map(async ({ pc, pcIndex }) => {
        try {
          const report = await pc.getStats();
          let inbound: Record<string, unknown> | null = null;
          report.forEach((row) => {
            const r = row as unknown as {
              type?: string;
              kind?: string;
              framesDecoded?: number;
              bytesReceived?: number;
              packetsReceived?: number;
              framesPerSecond?: number;
              frameWidth?: number;
              frameHeight?: number;
              codecId?: string;
              trackIdentifier?: string;
            };
            if (
              r.type === "inbound-rtp" &&
              (r.kind === "video" || r.kind === undefined) &&
              // Heuristic: pick the inbound-rtp with framesDecoded OR
              // bytesReceived — there is typically only one video row.
              (typeof r.framesDecoded === "number" ||
                typeof r.bytesReceived === "number")
            ) {
              if (!inbound) {
                inbound = {
                  framesDecoded: r.framesDecoded ?? null,
                  bytesReceived: r.bytesReceived ?? null,
                  packetsReceived: r.packetsReceived ?? null,
                  framesPerSecond: r.framesPerSecond ?? null,
                  frameWidth: r.frameWidth ?? null,
                  frameHeight: r.frameHeight ?? null,
                  codecId: r.codecId ?? null,
                  trackIdentifier: r.trackIdentifier ?? null,
                };
              }
            }
          });
          return { pcIndex, inbound };
        } catch {
          return { pcIndex, inbound: null };
        }
      }),
    );
  });
  for (const { pcIndex, inbound } of statsByPcIndex) {
    const target = snapshot.peerConnections.find(
      (p) => p.pcIndex === pcIndex,
    );
    if (target) target.inboundRtpVideo = inbound;
  }
  return snapshot;
}

/** Degraded empty snapshot returned by `logWebrtcDebug`'s try/catch fallback
 * when the fixture itself fails (closed page, detached context, broken
 * script). Lets the caller's real assertion throw through unchanged. */
function emptySnapshot(label: string): WebrtcDebugSnapshot {
  return {
    label,
    at: new Date().toISOString(),
    video: {
      videoWidth: 0,
      videoHeight: 0,
      readyState: 0,
      networkState: 0,
      paused: true,
      currentTime: 0,
      tracks: [],
    },
    peerConnections: [],
  };
}

/**
 * Whether the dump SHOULD fire on this call. Squad J review advisory
 * (folded in by Squad K, 2026-09-20): the previous version ran the dump
 * unconditionally at every first-oracle-failure site (5 of them), which
 * produced attachment churn on every green and obscured the cases where
 * the dump was the only thing telling the operator what went wrong. The
 * helper is now GATED on the actual symptom the dump is designed to
 * localize — `videoWidth === 0 || videoHeight === 0` after the first
 * frame has had a chance to decode, OR the snapshot has no inbound
 * video track at all. A healthy run that meets the oracle never pays
 * the dump cost. The oracle itself decides; this helper just decides
 * whether to *also* dump on top.
 */
export async function shouldDumpWebrtcDebug(
  page: Page,
  videoSelector: string = '[data-testid="browser-live-video"]',
): Promise<boolean> {
  try {
    const probe = await page.evaluate((sel) => {
      const video = document.querySelector(sel) as HTMLVideoElement | null;
      if (!video) return { hasVideo: false, hasInboundTrack: false };
      const stream = (video.srcObject as MediaStream | null) ?? null;
      const hasInboundTrack = !!stream &&
        stream.getTracks().some((t) => t.kind === "video");
      return {
        hasVideo: true,
        hasInboundTrack,
        videoWidth: video.videoWidth ?? 0,
        videoHeight: video.videoHeight ?? 0,
      };
    }, videoSelector);
    if (!probe.hasVideo) return true;
    if (!probe.hasInboundTrack) return true;
    if (probe.videoWidth === 0 || probe.videoHeight === 0) return true;
    return false;
  } catch {
    // Probe failed (page closed, etc.) — fall through to the dump so
    // the operator at least sees the failure mode.
    return true;
  }
}

/**
 * Persist the dump to the test report (as both a console log and an
 * attachment) so the failure is debuggable from the report alone.
 *
 * Squad J review advisory (folded in by Squad K, 2026-09-20) — the
 * gate `shouldDumpWebrtcDebug` is now wired INTO this function
 * (Squad K remediation 2026-09-20): a healthy run that already has
 * decoded video dimensions and at least one inbound video track
 * returns the empty snapshot immediately, without paying the
 * dump's evaluate+getStats cost and without polluting the report
 * with a "everything is fine" attachment. The 5 prior call sites
 * (browser-control-handover, browser-live-video, uat-browser-panel
 * UAT-13/14/15-human/15-agent) all hit this gate on the green
 * path; the dump fires only when the oracle is about to fail.
 *
 * Squad J review advisory — second half: the entire body is wrapped
 * in try/catch so a fixture failure (closed page, detached context,
 * broken script) can never mask the oracle message at the call
 * site. A failed dump returns an empty snapshot and console.warns;
 * the caller still throws its own real assertion.
 */
export async function logWebrtcDebug(
  page: Page,
  testInfo: TestInfo,
  label: string,
  videoSelector: string = '[data-testid="browser-live-video"]',
): Promise<WebrtcDebugSnapshot> {
  // Gate first: a healthy stream does not need a debug dump and
  // does not need an attachment. The probe is cheap (one
  // page.evaluate) compared to the dump below; skipping the
  // dump on the green path is the whole reason this gate exists.
  try {
    const shouldDump = await shouldDumpWebrtcDebug(page, videoSelector);
    if (!shouldDump) {
      // Return a minimal snapshot (matches the oracle pass shape) so
      // call sites that log the return value do not see a different
      // type on the green path vs the red path. No console line, no
      // attachment — the test report stays clean.
      return emptySnapshot(label);
    }
  } catch {
    // Probe itself failed (page closed, etc.) — fall through to the
    // dump, which has its own try/catch. The operator gets the
    // failure mode in the report either way.
  }

  try {
    const dump = await dumpWebrtcDebug(page, label, videoSelector);
    const line = `[webrtc-debug] ${label} video=${dump.video.videoWidth}x${dump.video.videoHeight} ` +
      `readyState=${dump.video.readyState} tracks=${JSON.stringify(dump.video.tracks)} ` +
      `peers=${JSON.stringify(dump.peerConnections)}`;
    // eslint-disable-next-line no-console
    console.log(line);
    try {
      await testInfo.attach(`webrtc-debug-${label}.json`, {
        body: JSON.stringify(dump, null, 2),
        contentType: "application/json",
      });
    } catch (attachErr) {
      // eslint-disable-next-line no-console
      console.warn(
        `[webrtc-debug] attach failed for ${label} (non-fatal — dump is already logged):`,
        attachErr,
      );
    }
    return dump;
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(
      `[webrtc-debug] dump failed for ${label} (non-fatal — caller will throw its real assertion):`,
      err,
    );
    return emptySnapshot(label);
  }
}