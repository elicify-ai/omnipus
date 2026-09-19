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
 * just shows up as `peerConnections: []`.
 */
export async function dumpWebrtcDebug(
  page: Page,
  label: string,
  videoSelector: string = '[data-testid="browser-live-video"]',
): Promise<WebrtcDebugSnapshot> {
  return page.evaluate(
    ({ label, videoSelector }) => {
      const video = document.querySelector(videoSelector) as
        | HTMLVideoElement
        | null;
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
        let inboundRtp: WebrtcDebugSnapshot["peerConnections"][number]["inboundRtpVideo"] =
          null;
        try {
          const report = pc.getStats();
          // getStats() can return either a Promise<StatsReport> (modern) or
          // a StatsReport (deprecated callback form). Promise.resolve
          // collapses both.
          Promise.resolve(report as unknown as Promise<unknown>).then(
            (resolved) => {
              // We can't return from here — leave a marker the caller can
              // re-read. Practically: the synchronous path below is what
              // the brief actually needs, and `getStats()` results are
              // also written into the snapshot via the await below.
              void resolved;
            },
          );
        } catch {
          // best-effort: pc.getStats() may throw on a closed pc.
        }
        peers.push({
          pcIndex,
          connectionState: pc.connectionState ?? null,
          iceConnectionState: pc.iceConnectionState ?? null,
          iceGatheringState: pc.iceGatheringState ?? null,
          signalingState: pc.signalingState ?? null,
          hasLocalDescription: Boolean(pc.localDescription),
          hasRemoteDescription: Boolean(pc.remoteDescription),
          inboundVideoTrack,
          inboundRtpVideo: inboundRtp,
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
  ).then(async (snapshot) => {
    // Round-trip getStats() — it is the only way to read `framesDecoded`
    // and `bytesReceived`. The synchronous dump above captured pc/track
    // state; this pass augments it with the inbound-rtp row.
    const statsByPcIndex = await page.evaluate(() => {
      const w = window as unknown as DebugWindow;
      const captured = w.__omnipusWebrtcDebug?.captured ?? [];
      return Promise.all(
        captured.map(async ({ pc, pcIndex }, idx) => {
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
          // idx unused but suppresses the unused-arg lint when stats
          // collection grows.
          void idx;
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
  });
}

/**
 * Persist the dump to the test report (as both a console log and an
 * attachment) so the failure is debuggable from the report alone.
 */
export async function logWebrtcDebug(
  page: Page,
  testInfo: TestInfo,
  label: string,
  videoSelector: string = '[data-testid="browser-live-video"]',
): Promise<WebrtcDebugSnapshot> {
  const dump = await dumpWebrtcDebug(page, label, videoSelector);
  const line = `[webrtc-debug] ${label} video=${dump.video.videoWidth}x${dump.video.videoHeight} ` +
    `readyState=${dump.video.readyState} tracks=${JSON.stringify(dump.video.tracks)} ` +
    `peers=${JSON.stringify(dump.peerConnections)}`;
  // eslint-disable-next-line no-console
  console.log(line);
  await testInfo.attach(`webrtc-debug-${label}.json`, {
    body: JSON.stringify(dump, null, 2),
    contentType: "application/json",
  });
  return dump;
}