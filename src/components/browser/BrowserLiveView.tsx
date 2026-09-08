// Shared browser panel for the docked view and fullscreen pop-out.
// Video uses WebRTC; all human input, navigation and control use one ordered
// WebSocket. Human input remains available during agent activity. Only the
// explicit Take over button stops the chat response. Control status is a
// presentation hint, not a prerequisite for input; annotation stays local.

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  ArrowSquareOut,
  ArrowsClockwise,
  CaretLeft,
  ChatCircleDots,
  Cursor,
  Eye,
  Globe,
  HandGrabbing,
  Plus,
  Robot,
  SpeakerHigh,
  SpeakerSlash,
  SpinnerGap,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { cn, initialOf } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { BrowserLiveWsConnection, describeVideoHealth, translateBrowserErrorMessage } from '@/lib/browserLiveWs'
import { BrowserWebRTCSession, translateWebRTCFallbackReason, DEFAULT_FIRST_ANSWER_TIMEOUT_MS, type BrowserPeerIdentity } from '@/lib/browserWebRTC'
import {
  computeCropRect,
  computeModifiers,
  computeObjectContainRect,
  framePixelToDeviceCoords,
  isPrintableKey,
  mapClientToFramePixels,
  mapMouseButton,
  scaleCropToImagePixels,
  type DeviceCoords,
  type FrameCropRect,
  type RectLike,
} from '@/lib/browserLiveCoords'
import { mapClientToBrowserCss } from '@/lib/browserFrameCoords'
import { BrowserFrameGate, type BrowserFrameGateState } from '@/lib/browserFrameGate'
import { resolveOmniboxInput } from '@/lib/browserLiveUrl'
import { submitAnnotation, AnnotationBusyError } from '@/lib/browserAnnotate'
import { useUiStore } from '@/store/ui'
import { useChatStore } from '@/store/chat'
import { queryClient } from '@/lib/queryClient'
import type { Agent } from '@/lib/api'
import type {
  BrowserInputFrame,
  BrowserStatusFrame,
  BrowserTabsFrame,
  BrowserVideoHealthFrame,
} from '@/lib/api/generated/asyncapi-types'

export interface BrowserLiveViewProps {
  sessionId: string
  agentId: string
  /** Rendered as a header "Pop out" button when provided — exactly like `onClose` below. */
  onPopOut?: () => void
  /** Rendered as a header "Close" button when provided. */
  onClose?: () => void
  /**
   * ADR-039 D-B1/B2 "Annotate a region" — only works when hosted in the SAME
   * JS realm as the AssistantUI runtime (annotate's Send calls
   * `useChatStore.sendMessage` directly, see browserAnnotate.ts). Defaults
   * to `false` (dead-end-proof by default) so a future third host doesn't
   * silently inherit annotate support it can't deliver on — the docked
   * panel (BrowserLivePanel.tsx) explicitly opts in.
   *
   * UAT finding FE-4: the fullscreen pop-out (`routes/_app/browser-live.tsx`)
   * is a separate `window.open` document with no chat store at all —
   * starting an annotation there and hitting Send could NEVER succeed
   * (submitAnnotation's own re-check always sees a mismatched/absent active
   * chat) and only "Cancel" escaped, discarding the drafted comment. Hiding
   * the "Annotate" button there entirely removes the dead-end.
   */
  canAnnotate?: boolean
  className?: string
  /**
   * A live WebRTC `MediaStream` for the agent's active tab — the ONLY video
   * source this component ever renders (ADR-047; the JPEG-screencast `<img>`
   * sink was deleted outright, not flagged off). Production callers
   * (BrowserLivePanel.tsx, routes/_app/browser-live.tsx) always omit this
   * prop and let the component drive its OWN internal WebRTC signaling over
   * `browserLiveWs.ts`'s offer/answer/state exchange (see the WS lifecycle
   * effect below) — the resulting stream lands in `webrtcStream` state and
   * flows through the `mediaStream` merge point just below the props
   * destructure. This prop exists purely as a test/override seam (a caller
   * can supply a fake `MediaStream` to exercise the sink/coordinate-mapping/
   * annotate-crop machinery without a real signaling round trip — jsdom has
   * no WebRTC implementation) — see BrowserLiveView.webrtcSink.test.tsx.
   * `null`/`undefined` (the default) defers entirely to the internal
   * signaling result. Coordinate mapping always uses the video-dimension
   * variant (`mapClientToDeviceVideo` — `browserLiveCoords.ts`), and the
   * annotate-crop path always draws from the `<video>` element.
   */
  mediaStream?: MediaStream | null
  /**
   * Whether the stream carries an audio track — gates the mute/unmute
   * toolbar button. Ignored while `mediaStream` is null. Defaults to `false`
   * (no audio control shown) rather than inferring it from the stream
   * itself, since the internal signaling result reports this before the
   * track metadata is fully available (see `hasAudio`'s merge point below).
   */
  hasAudio?: boolean
  /**
   * BUG 1 fix (pop-out "tiny letterboxed video", live UAT re-run) — `false`
   * (the default) keeps the historical intrinsic-size-capped layout
   * (`w-auto h-auto max-w-full max-h-full`): the media element never grows
   * past its own native resolution, only shrinks to fit. That's correct for
   * the docked panel (BrowserLivePanel.tsx), whose container is usually
   * close to the native resolution already, and — more importantly —
   * matches the "container tightly wraps the visible content, no
   * letterboxing" invariant the annotate-a-region crop math
   * (containerRef.getBoundingClientRect() used directly, see
   * finalizeSelection/handlePointerDown's annotate branch) depends on.
   *
   * `true` makes the media element FILL its container (`w-full h-full
   * object-contain`), scaling UP past intrinsic size when the container is
   * bigger — this is what the fullscreen pop-out route
   * (routes/_app/browser-live.tsx) needs: a 1600×880 window showing a
   * 639×316 video was ~90% wasted black space under the capped layout.
   * Filling introduces real letterbox/pillarbox bars whenever the
   * container's aspect ratio doesn't match the content's, so every
   * coordinate-mapping call site routes its raw bounding rect through
   * `computeObjectContainRect` (browserLiveCoords.ts) first — see
   * `mapPointerToDeviceCoords` below — to keep pointer/wheel input landing
   * on the right device pixel regardless.
   *
   * Safe to combine with `canAnnotate` as of 2026-07-31. It previously was
   * NOT: annotate's crop math (mapClientToFramePixels / computeCropRect) read
   * `containerRef.getBoundingClientRect()` directly with no letterbox
   * correction, so pairing the two misplaced the selection whenever bars were
   * showing. `finalizeSelection` now routes that rect through
   * `computeObjectContainRect` first — the same correction the pointer/wheel
   * path always applied — which removes the restriction.
   *
   * The docked panel (BrowserLivePanel) now passes BOTH: without
   * `fillContainer` it rendered the page at intrinsic size inside a much
   * larger panel (operator UAT measured ~695x343 in ~890x1010), too small to
   * interact with.
   */
  fillContainer?: boolean
}

/** ADR-040 D2/D6 — the three (+ one) mutually-exclusive visual/control states. */
type VisualState = 'agent-working' | 'you-driving' | 'annotating' | 'error' | 'idle'

/** Presentation state; only annotation and connectivity gate input. */
type DriveMode = 'annotating' | 'agent-working' | 'you-driving' | 'disconnected' | 'other-driving' | 'idle'

function computeDriveMode(state: {
  annotateMode: boolean
  agentWorking: boolean
  isControlling: boolean
  connected: boolean
  controlledByOther: boolean
}): DriveMode {
  if (state.annotateMode) return 'annotating'
  if (!state.connected) return 'disconnected'
  if (state.isControlling) return 'you-driving'
  if (state.agentWorking) return 'agent-working'
  if (state.controlledByOther) return 'other-driving'
  return 'idle'
}

// Toolbar icon buttons share ONE shape (operator direction, 2026-08-04: "the
// buttons should be icons ... it needs to be flatter"). Back, refresh, annotate,
// mute and the degraded-retry all render as a bare 32px glyph with no border and
// no fill — the frames and pill backgrounds made a row of five controls read as
// five competing objects. Hover is the only chrome; active state is carried by
// COLOUR PLUS `aria-pressed`, never colour alone. The coarse-pointer floor keeps
// the WCAG 2.5.8 target even though the visual box shrank.
const TOOLBAR_ICON_BTN =
  'shrink-0 flex h-8 w-8 items-center justify-center rounded-md transition-colors ' +
  'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] ' +
  'disabled:cursor-not-allowed disabled:opacity-40 ' +
  'pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]'


// The visible border/frame around the browser panel is REMOVED per operator
// direction. The header chip (agent identity + drive-status) is the sole
// driving-state signal. The data-visual-state attribute on the (invisible)
// overlay is kept for tests + potential future use.

// Local-only pill states layered on top of the wire `BrowserStatusFrame.state`
// enum: 'connecting' (never attached yet) and 'disconnected' (was attached,
// the WS transport dropped, a reconnect is in flight) both describe SPA
// connection lifecycle, not anything the backend ever sends as a status.
type LiveStatus = BrowserStatusFrame['state'] | 'connecting' | 'disconnected'

// UAT finding FE-7 / D5: the backend surfaces raw Go error strings verbatim
// on a terminal browser_status{state:'error'} frame — e.g. `browser input
// failed: browser live: navigate blocked: ... SSRF: blocked cloud metadata
// endpoint 169.254.169.254` or Go's url.Parse format `parse "...": invalid
// character " " in host name` — as well as on this connection's own `error`
// ErrorFrames (D5 Site 2, handled in browserLiveWs.ts's onmessage). The
// translation now lives in browserLiveWs.ts as `translateBrowserErrorMessage`
// (moved from here) so BOTH leak sites share one function and can never
// drift into different copy for the identical underlying string; see its
// doc comment for the full pattern list and the deliberately-excluded
// already-readable sessionErrorStatus() messages (already-controlled,
// take-control-disabled, no-manager-for-agent, live-view-disabled, malformed
// control).

// Operator directive (JPEG-fallback removal): WebRTC is now the ONLY live-
// video path — there is no second sink left to quietly keep working while
// this one degrades. Every `BrowserWebRTCSession.onFallback` reason
// (including the former "capability gate" reasons — `disabled`/
// `not_capable`/`lite_build`, previously suppressed because JPEG carried on
// underneath) is therefore now a REAL, user-facing failure: see the WS
// lifecycle effect's `machine.onFallback` callback below, which always sets
// `webrtcError` and never swallows a reason silently.
// translateWebRTCFallbackReason (browserWebRTC.ts) turns the raw reason
// string into the honest, actionable message actually shown.

// FIRST_FRAME_TIMEOUT_MS bounds how long the panel shows "Waiting for the first
// frame…" before admitting failure. Generous on purpose: a cold Chrome launch
// plus WebRTC negotiation can legitimately take several seconds on a small box,
// and a premature error on a session that was about to work is worse than a few
// extra seconds of spinner. What is NOT acceptable is waiting forever — see
// firstFrameTimedOut for the silent-failure this bounds.
//
// Bugfix (MED, external review F6, 2026-08-13): this used to be a fixed
// 15_000, defined with no relationship to browserWebRTC.ts's own cold-start
// answer budget (DEFAULT_FIRST_ANSWER_TIMEOUT_MS, 30s — the gateway's
// capture-start + bringToFront + tracks-wait sequence can legitimately run
// past 25s worst case, per that file's own header doc). This timer is armed
// against `videoReady` — a DECODED FRAME, which can only happen AFTER that
// answer round trip completes, AND ICE connects, AND the first video RTP
// packets arrive — so it must never be shorter than the answer budget it
// sits downstream of, or a perfectly healthy cold start shows the red "No
// video received…" error and then connects seconds later anyway (exactly
// what was reported live). Derived from the SAME constant the machine uses
// for its own cold-start timeout, plus a margin for the post-answer
// ICE-connect + first-frame-decode gap, so the two can never silently drift
// apart again the way they just did.
const FIRST_FRAME_TIMEOUT_MS = DEFAULT_FIRST_ANSWER_TIMEOUT_MS + 15_000

// VIEWPORT_SETTLE_MS is how long a new panel size must hold still before it is
// committed to the server. Each commit REBUILDS the capture stream (tabCapture
// constraints are pinned per stream), so an intermediate size captured
// mid-drag or mid-animation costs a visible stall for a geometry that is
// already obsolete. Short enough to feel immediate after a deliberate resize,
// long enough to swallow an animation's intermediate frames.
const VIEWPORT_SETTLE_MS = 250

// MOVE_FLUSH_MS paces coalesced pointer-move sends. Chosen to sit under the
// server's maxInputEventsPerSecond (50/s, pkg/tools/browser/live.go) with
// headroom for the down/up/wheel events that share that budget: at ~16ms a
// sustained drag alone would ride the cap and start losing events to the
// limiter. Not requestAnimationFrame — see scheduleInputFlush.
const MOVE_FLUSH_MS = 25

// Defer capture resizing only while an editable field coincides with a
// keyboard-sized visual viewport occlusion. Desktop focus alone is harmless.
function textFieldHasFocus(frameEl: Element | null): boolean {
  const active = document.activeElement
  if (!active || active === frameEl) return false
  const tag = active.tagName
  const editable = tag === 'INPUT' || tag === 'TEXTAREA' || (active as HTMLElement).isContentEditable === true
  const viewport = window.visualViewport
  return editable && !!viewport && viewport.scale === 1 && viewport.height + viewport.offsetTop < window.innerHeight - 1
}

// BLANK_TAB_URL is the placeholder a not-yet-navigated tab reports. The address
// bar must not display it: showing "about:blank" to a user who is looking at the
// Omnipus start page is noise, and it would also overwrite a url the user is
// about to submit on a fresh tab. Kept in sync with pkg/tools/browser's
// BlankPageURL.
const BLANK_TAB_URL = 'about:blank'

/**
 * ADR-041 D4 — a tab's display label: prefer `title`, fall back to the
 * hostname parsed from `url`, fall back to "New tab". The wire type carries
 * no favicon URL (BrowserTabsFrame.tabs[] has no such field) — the strip
 * uses a plain Phosphor globe glyph per tab instead of attempting to fetch
 * one, so there is no missing-favicon broken-image state to handle.
 */
function tabLabel(tab: BrowserTabsFrame['tabs'][number]): string {
  if (tab.title && tab.title.trim().length > 0) return tab.title
  if (tab.url) {
    try {
      const hostname = new URL(tab.url).hostname
      return hostname || tab.url
    } catch {
      return tab.url
    }
  }
  return 'New tab'
}

/** A finalized region selection, cropped to a File and ready to send (ADR-039 D-B1/B2). */
interface PendingAnnotation { // not-wire-format: local annotate-popover state, never serialized across the gateway/SPA boundary
  file: File
  previewUrl: string
  /** Device (CSS) pixel point — center of the crop — for the D-B3 inspect call. */
  point: { x: number; y: number }
}

/**
 * Shared canvas-crop implementation for annotate-a-region (ADR-039 D-B1/B2),
 * used by BOTH the JPEG `<img>` sink and the WebRTC build's `<video>` sink
 * (W1-F) — draws `source` (already confirmed by the caller to have a live
 * decoded frame available: `img.complete`/`naturalWidth` or a video's
 * `readyState`/`videoWidth`) into an offscreen canvas and returns a cropped
 * PNG File with the exact same output contract regardless of sink.
 *
 * Reuses `scaleCropToImagePixels` (browserLiveCoords.ts) for the
 * frame-space→natural-pixel-space scale correction in BOTH cases rather than
 * duplicating it: for the img sink this corrects for the screencast JPEG's
 * fixed downscale cap (see cropFrameToFile's own doc comment); for the video
 * sink there is no such cap, but the SAME correction still guards against a
 * recapture-driven resolution change landing between when the crop rect was
 * computed (drag-start `frameWidth`/`frameHeight`) and when this draw
 * actually runs (the video's CURRENT `naturalWidth`/`naturalHeight`) — see
 * browserLiveCoords.test.ts's "video-mode reuse" coverage. A no-drift call
 * (the common case for both sinks) is a scale-1 no-op either way.
 *
 * Exceptions from drawImage/getContext (e.g. IndexSizeError on a degenerate
 * zero-width/height rect, or a tainted canvas) are swallowed to null — this
 * is awaited from finalizeSelection, itself invoked fire-and-forget (`void
 * finalizeSelection(...)` from the pointerup handler), so an uncaught
 * rejection here would surface as an unhandled promise rejection with no
 * toast and a frozen selection box; returning null instead routes through
 * finalizeSelection's existing `if (!file) return fail()` path.
 */
async function drawCropToPngFile(
  source: CanvasImageSource,
  naturalWidth: number,
  naturalHeight: number,
  rect: FrameCropRect,
  frameWidth: number,
  frameHeight: number,
): Promise<File | null> {
  const src = scaleCropToImagePixels(rect, frameWidth, frameHeight, naturalWidth, naturalHeight)
  const { x: sx, y: sy, width: sw, height: sh } = src
  try {
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(sw)
    canvas.height = Math.round(sh)
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.drawImage(source, sx, sy, sw, sh, 0, 0, canvas.width, canvas.height)
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
    if (!blob) return null
    return new File([blob], 'annotation.png', { type: 'image/png' })
  } catch {
    return null
  }
}

export function BrowserLiveView({
  sessionId,
  agentId,
  onPopOut,
  onClose,
  canAnnotate = false,
  className,
  // W2-B: these props remain a caller-facing override/test seam exactly as
  // W1-F shipped them (see the prop's own doc comment above) — renamed at
  // the destructuring site so the REST of this component keeps reading the
  // plain `mediaStream`/`hasAudio` identifiers unchanged (dozens of existing
  // call sites), now bound to the merged consts just below instead of the
  // raw props. A caller passing a non-null `mediaStream` still wins outright
  // (used by BrowserLiveView.webrtcSink.test.tsx to exercise the sink/coord/
  // annotate-crop machinery without a real signaling round trip); the
  // default (`null`/omitted) now falls through to THIS component's own
  // internal WebRTC signaling result instead of forcing JPEG-forever.
  mediaStream: mediaStreamProp = null,
  hasAudio: hasAudioProp = false,
  fillContainer = false,
}: BrowserLiveViewProps) {
  const wsRef = useRef<BrowserLiveWsConnection | null>(null)
  // WebRTC build (W2-B) — the viewer-side PC state machine (browserWebRTC.ts),
  // one instance per WS-connection effect lifecycle (see that effect further
  // down), mirroring wsRef's own per-mount lifetime.
  const webrtcRef = useRef<BrowserWebRTCSession | null>(null)
  // Mirrors whether the machine's "input" data channel is currently OPEN —
  // read (never as a dependency) by the stable `dispatchInput` callback
  // below to decide DC-vs-WS routing without needing to be in anyone's
  // dependency array, same rationale as every other *Ref mirror in this
  // file (attachedRef, connectedRef, ...).
  const inputChannelOpenRef = useRef(false)
  const containerRef = useRef<HTMLDivElement | null>(null)
  // Bound to the `<video>` sink's srcObject via the effect below whenever
  // `mediaStream` is set. The `<video>` element is only ever mounted once a
  // stream exists (see the "attached" gate and the sink JSX further down) —
  // there is no other sink for this to stand in for any more.
  const videoRef = useRef<HTMLVideoElement | null>(null)
  // WCAG 2.1.2 fix — Escape-releases-the-wheel focus target: handleKeyDown's
  // Escape branch moves focus here once driving ends, so the user lands
  // somewhere useful instead of on a container that just stopped capturing
  // keys (see releaseWheel below).
  const addressBarRef = useRef<HTMLInputElement | null>(null)
  // urlBarEditingRef guards the address bar against being overwritten while the
  // user is mid-typing. The bar now follows the ACTIVE TAB's url (see the
  // effect below), and without this guard a tab/url update arriving between
  // keystrokes would yank a half-typed address away — the "URL bar clears
  // itself" symptom from the 2026-08-03 recordings. Set on focus, cleared on
  // blur and on submit.
  const urlBarEditingRef = useRef(false)
  // attachedRef mirrors `attached` (mediaStream !== null — see its own
  // definition below) — the "is a live WebRTC session actually attached"
  // signal every stable-identity pointer/keyboard/wheel handler consults
  // before doing real work, replacing the old JPEG-era `frameRef` (which
  // used to serve the same "is there a live session" role, backed by the
  // screencast frame instead of the WebRTC stream).
  const attachedRef = useRef(false)
  const controllingRef = useRef(false)
  // ── ADR-040 D2 implicit control model ───────────────────────────────────
  // True from the instant an implicit (click-to-drive) or explicit (Take
  // over) `sendControl('take')` is sent until the server's 'controlling'
  // browser_status round-trips back (or the take is superseded/abandoned).
  // Prevents a double-fire: a second pointerdown (or a fast double-click on
  // Take over) while a take is already in flight must NOT send a second
  // redundant `browser_control{action:'take'}` frame. The synchronous
  // source of truth for those in-flight guards — always read/written via
  // `setPendingTake` below so the reactive `pendingTake` state (which
  // `driveMode` needs for the chip/glow to update immediately) never drifts
  // out of sync with it.
  const pendingTakeRef = useRef(false)
  // True for the span of a single pointer gesture that implicitly acquired
  // the lock (click-to-drive) — lets pointermove/pointerup for THAT SAME
  // gesture keep dispatching input even though the server's 'controlling'
  // ack (which flips controllingRef) may not have landed yet. Cleared on
  // pointerup (end of the gesture) and whenever the agent starts working.
  // UAT finding FE-6, carried into the ADR-040 model: mirrors
  // `controlledByOther` state so the click-to-drive / Take-over paths can
  // avoid racing a control lock a DIFFERENT connection of this same session
  // already holds (previously enforced by disabling the explicit Take
  // control button; there is no such button anymore, so the guard moves
  // into takeWheelIfNeeded itself).
  const controlledByOtherRef = useRef(false)
  // Mirrors `connected` — click-to-drive must never attempt to acquire the
  // lock (or dispatch input) against a dead/reconnecting transport, matching
  // the pre-ADR-040 regression coverage ("pointer/keyboard handlers must
  // no-op while disconnected, not silently attempt and drop a send").
  const connectedRef = useRef(false)
  // ADR-040 D2 refactor — mirrors `driveMode` (computed below from
  // annotateMode/agentWorking/isControlling/connected/controlledByOther) so
  // the stable-identity handlers (wheel/pointer/keyboard) always read the
  // LATEST single source of truth via `canDispatchInput` instead of
  // re-deriving their own combination of the raw refs above.
  const driveModeRef = useRef<DriveMode>('disconnected')
  // ── Annotate-a-region state (ADR-039 D-B1/B2) — a third interaction mode,
  // mutually exclusive with driving (isControlling). annotateDraggingRef +
  // selectionStartClientRef are refs (not state) so the pointerup handler
  // always reads the exact values captured on pointerdown, never a stale
  // closure — same pattern as attachedRef/controllingRef above.
  const annotateDraggingRef = useRef(false)
  const selectionStartClientRef = useRef<{ x: number; y: number } | null>(null)
  const pendingAnnotationRef = useRef<PendingAnnotation | null>(null)
  // Coalesce mouse moves on the input timer. Store final CSS coordinates so
  // later encoder adaptation cannot reinterpret an already mapped position.
  const pendingMoveRef = useRef<{ x: number; y: number; modifiers: number } | null>(null)
  // Wheel is coalesced on the SAME pacer as moves, with deltas ACCUMULATED
  // (position = latest). Un-paced wheel was the second half of the operator's
  // "clicks work only sometimes": a trackpad/momentum scroll emits wheel at the
  // display refresh rate, so one gesture alone could exceed the server's
  // per-second input budget and the click that followed it was silently
  // dropped by the limiter. Summing deltas preserves total scroll distance, so
  // pacing costs resolution in time, never travel.
  const pendingWheelRef = useRef<{
    x: number
    y: number
    modifiers: number
    deltaX: number
    deltaY: number
  } | null>(null)
  // Guards re-scheduling of the shared move+wheel flush, and OWNS the timer
  // handle. Storing the id is what makes cancellation real: clearing the flag
  // alone left the already-armed callback running, so the next move would arm a
  // second, overlapping timer and the stale one would flush it early — sends
  // bursting faster than the pacer's own cadence, straight into the server's
  // rate limiter. Mirrors settleRef's handle-owning pattern.
  const inputFlushScheduledRef = useRef(false)
  const inputFlushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // This component's OWN WebRTC signaling result (populated by the machine's
  // onStream/onFallback callbacks and the gateway's browser_webrtc_state
  // frame — see the WS lifecycle effect further down). Reset to null/false
  // on fallback AND on transport disconnect (a dropped WS means the PC's
  // fate is unknown/stale either way — there is no second sink to fall back
  // to any more, so a reset here means the panel visibly stops being
  // interactive until the machine is stopped and re-armed on reconnect).
  const [webrtcStream, setWebrtcStream] = useState<MediaStream | null>(null)
  // Persistent, honest failure indicator (operator directive — JPEG fallback
  // removal). WebRTC is the ONLY live-video path now: there is no second
  // sink to quietly keep working while this one degrades, so EVERY
  // `onFallback` reason — including the reasons that used to be silently
  // suppressed because JPEG carried on underneath — lands here and drives
  // the panel's primary error state (see `displayError` below). `null` =
  // no failure reported (yet).
  const [webrtcError, setWebrtcError] = useState<string | null>(null)
  // UAT case 16 — the gateway's free-text cause for THIS failure
  // (browser_webrtc_state.reason_detail), held alongside the closed `reason`
  // enum in `webrtcError`. Kept as separate state rather than pre-joined into
  // one string so the two stay independently inspectable (and so a reason
  // arriving with no detail can never leave a stale detail from a previous,
  // unrelated failure glued to it — every `applyWebrtcFailure` sets BOTH).
  const [webrtcErrorDetail, setWebrtcErrorDetail] = useState<string | null>(null)
  const [webrtcHasAudio, setWebrtcHasAudio] = useState(false)
  // Issue #674 — the gateway's own verdict on the SHARED capture feeding this
  // panel: lost / recovering (with a bounded attempt count) / recovered /
  // unrecoverable. Held as the raw generated frame so no hand-written wire
  // shape is introduced (hard constraint #8) and so every field the gateway
  // chose to send stays available to the copy in describeVideoHealth.
  //
  // This is NOT redundant with `webrtcError`. That one is about THIS viewer's
  // signalling (may I offer, did my PeerConnection fail); this is about the
  // upstream capture, which can die while this viewer's PeerConnection is
  // perfectly healthy — which is exactly the case that used to look like
  // nothing at all for a full FIRST_FRAME_TIMEOUT_MS.
  const [videoHealth, setVideoHealth] = useState<BrowserVideoHealthFrame | null>(null)
  // True after this stream's first frame reaches its expected presentation
  // time. Controls the waiting overlay and first-frame deadline; input has a
  // separate capture-generation proof. Browsers without frame callbacks can
  // show read-only video after loadeddata, but cannot authorize interaction.
  const [videoReady, setVideoReady] = useState(false)
  // Starts MUTED (autoplay-safe: browsers block autoplaying audio without a
  // prior user gesture; the video itself still autoplays fine muted).
  // Flipped by the mute/unmute toolbar button — that click IS the user
  // gesture that makes unmuting reliable. Local component state only (no
  // persistence yet).
  const [videoMuted, setVideoMuted] = useState(true)
  const [statusState, setStatusState] = useState<LiveStatus>('connecting')
  // The human-readable text carried on the latest browser_status frame (set
  // whenever state === 'error' — already-controlled, take-control-disabled,
  // no-manager-for-agent, live-view-disabled, malformed control, etc.).
  // Distinct from connError, which is transport-level (WS create/send/auth
  // failures) rather than a semantic status the backend reported.
  const [statusMessage, setStatusMessage] = useState<string | null>(null)
  // True exactly when the LATEST browser_status frame processed was a
  // terminal error one — tracked independently of `statusState` (see the
  // onStatus handler below: a routine per-request error like a blocked
  // navigate must surface a message WITHOUT overwriting statusState away
  // from 'controlling'), so displayError can't rely on `statusState ===
  // 'error'` alone.
  const [statusIsError, setStatusIsError] = useState(false)
  const [connError, setConnError] = useState<string | null>(null)
  const [connected, setConnected] = useState(false)
  // cursorPos state removed — the synthetic cursor overlay is gone (native
  // cursor only). This eliminates a per-pointer-move setState that re-rendered
  // the entire component (including the <video> sink) on every coalesced
  // move, which competed with wheel/scroll event processing and caused lag.
  // UAT finding FE-6: true whenever the LATEST browser_status frame reported
  // another connection of this same browser session is the one holding
  // control (e.g. the docked panel and a pop-out both watching the same
  // agent) — distinct from `isControlling`, which is about THIS connection.
  const [controlledByOther, setControlledByOther] = useState(false)
  // UAT finding A8 — the reactive mirror of pendingTakeRef (see its own doc
  // comment): a ref alone never triggers a re-render, so `driveMode` (and
  // therefore the header chip / glow border) kept showing stale 'idle' for
  // the whole async gap between sending a take and its ack landing. Written
  // ONLY via `setPendingTake` below, in lockstep with the ref.
  const [pendingTake, setPendingTakeFlag] = useState(false)
  // UAT fix (two-click take-over bug) — reactive mirror of


  // ── Omnibox (ADR-039 D-A2, ADR-040 D5 — always visible) ──────────────────
  const [urlInput, setUrlInput] = useState('')


  // ── Tab strip (ADR-041 D4) — the latest known tab list + active index,
  // straight off the most recent `browser_tabs` frame. `null` until the
  // first one arrives (the strip stays unrendered — never shown empty).
  // Collapsed into a single state slice (reviewer finding F5): `tabs` and
  // `activeIndex` used to be two separate `useState` calls that are ALWAYS
  // set together (in `onTabs` below) and ALWAYS read together (the
  // tab-strip render further down) — two hooks for one atomic value is a
  // foot-gun, since nothing stops a future edit from updating one without
  // the other. Deliberately no optimistic local "active" override on click:
  // the backend re-binds the screencast + re-broadcasts `browser_tabs` on
  // every switch/close/open, so reconciling to the next frame alone keeps
  // this free of drift between what's highlighted and what's actually
  // active. (The ADR itself is silent on whether an optimistic highlight
  // would also be acceptable — this "reconcile from the next frame" choice
  // is a local implementation decision, not something the ADR mandates.)
  const [tabState, setTabState] = useState<{ tabs: BrowserTabsFrame['tabs']; activeIndex: number } | null>(null)
  // Keep the address bar in sync with the ACTIVE TAB's real url.
  //
  // Before this, setUrlInput was called in exactly one place: the omnibox's own
  // submit handler. So the bar only ever showed what the USER typed, and every
  // other navigation left it stale — Back and Refresh (operator report:
  // "back button and refresh button do not work"), agent-driven navigation, and
  // ordinary in-page link clicks. Measured on v53: after pressing Back the page
  // and tab title correctly moved to example.com while the bar still read
  // en.wikipedia.org/wiki/Octopus, which is why the buttons LOOKED broken —
  // they had in fact navigated.
  //
  // tabState is the same source of truth the tab strip renders from (ADR-041
  // D4), so the bar can never disagree with the strip again.
  useEffect(() => {
    if (urlBarEditingRef.current) return // never clobber a half-typed address
    const active = tabState?.tabs?.[tabState.activeIndex]
    const url = active?.url
    if (typeof url === 'string' && url !== '' && url !== BLANK_TAB_URL) {
      setUrlInput(url)
    }
  }, [tabState])

  // ── Annotate mode (ADR-039 D-B1/B2) — container-relative CSS coords for
  // the live selection-box overlay; frozen (not cleared) once a selection
  // finalizes into pendingAnnotation, so the box stays visible behind the
  // comment popover.
  const [annotateMode, setAnnotateMode] = useState(false)
  const [selectionStart, setSelectionStart] = useState<{ x: number; y: number } | null>(null)
  const [selectionCurrent, setSelectionCurrent] = useState<{ x: number; y: number } | null>(null)
  const [pendingAnnotation, setPendingAnnotation] = useState<PendingAnnotation | null>(null)
  const [annotateComment, setAnnotateComment] = useState('')
  const [annotateSubmitting, setAnnotateSubmitting] = useState(false)
  const [annotateError, setAnnotateError] = useState<string | null>(null)

  // The merge point described at the destructuring site above: an explicit
  // caller-supplied `mediaStreamProp` always wins (test/override seam);
  // otherwise this component's own internally-driven signaling result is
  // what every downstream consumer (`activeFrameDims`, `cropFrameToFile`,
  // the sink JSX, the mute toggle, `dispatchInput`) reads as
  // `mediaStream`/`hasAudio`.
  const mediaStream = mediaStreamProp ?? webrtcStream
  const hasAudio = mediaStreamProp !== null ? hasAudioProp : webrtcHasAudio

  // Each capture owns its own gate: generation counters can restart on replacement.
  const captureRef = useRef<{ id: string | null; generation: number; marker: number | null; css: { width: number; height: number } | null; gate: BrowserFrameGate; retired: Set<string> }>({ id: null, generation: 0, marker: null, css: null, gate: new BrowserFrameGate(), retired: new Set() })
  const streamIdentityRef = useRef<BrowserPeerIdentity | null>(null)
  const currentStreamRef = useRef<MediaStream | null>(mediaStream)
  const freshViewerRef = useRef<string | null>(null)
  const requiresFreshViewerRef = useRef(false)
  const captureViewerNeededRef = useRef(false)
  const requestFreshViewerRef = useRef<() => void>(() => {})
  const [frameGateState, setFrameGateState] = useState<BrowserFrameGateState>({ status: 'locked', reason: 'generation-required' })
  const publishedFrameGateStateRef = useRef(frameGateState)
  const [frameCallbacksUnavailable, setFrameCallbacksUnavailable] = useState(false)
  const [frameGeometryReady, setFrameGeometryReady] = useState(false)
  const framePresentationTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const suppliedStreamRef = useRef(mediaStreamProp)
  suppliedStreamRef.current = mediaStreamProp
  const refreshFrameGate = useCallback((): void => {
    const gate = captureRef.current.gate
    const state = gate.read(performance.now())
    if (framePresentationTimerRef.current !== null) clearTimeout(framePresentationTimerRef.current)
    framePresentationTimerRef.current = null
    if (state.status === 'presenting') {
      framePresentationTimerRef.current = setTimeout(() => {
        if (captureRef.current.gate === gate) refreshFrameGate()
      }, Math.ceil(Math.max(0, state.displayAt - performance.now())))
    }
    const previous = publishedFrameGateStateRef.current
    const unchanged =
      (previous.status === 'ready' && state.status === 'ready' && previous.generation === state.generation) ||
      (previous.status === 'locked' && state.status === 'locked' && previous.reason === state.reason) ||
      (previous.status === 'presenting' && state.status === 'presenting' && previous.generation === state.generation && previous.displayAt === state.displayAt) ||
      (previous.status === 'needs-fresh-viewer' && state.status === 'needs-fresh-viewer' && previous.generation === state.generation)
    if (!unchanged) {
      publishedFrameGateStateRef.current = state
      setFrameGateState(state)
    }
    if (state.status === 'needs-fresh-viewer') {
      requiresFreshViewerRef.current = true
      requestFreshViewerRef.current()
    }
  }, [])
  const acceptCapture = useCallback((id: string, generation: number): boolean => {
    const current = captureRef.current
    if (current.retired.has(id)) return false
    if (current.id !== id) {
      if (current.id) {
        captureViewerNeededRef.current = true
        releaseInputsRef.current()
        current.retired.add(current.id)
      }
      const gate = new BrowserFrameGate()
      gate.expectGeneration(generation)
      // An explicit supplied stream has no signaling callback; its first capture
      // still requires a matching received timestamp. Never reuse it on replacement.
      if (streamIdentityRef.current?.captureId === id || (current.id === null && suppliedStreamRef.current)) {
        gate.bindStream(currentStreamRef.current)
      }
      captureRef.current = { id, generation, marker: null, css: null, gate, retired: current.retired }
      freshViewerRef.current = null
      setFrameGeometryReady(false)
    } else {
      if (generation < current.generation) return false
      if (generation > current.generation) {
        releaseInputsRef.current()
        current.css = null
        setFrameGeometryReady(false)
        current.marker = null
        freshViewerRef.current = null
      }
      current.generation = generation
      current.gate.expectGeneration(generation)
    }
    return true
  }, [])

  // Sync `muted` to the DOM PROPERTY, imperatively.
  //
  // React writes `muted` on a <video> as an ATTRIBUTE. The browser reads that
  // attribute once, at mount, to seed the property — and then ignores it. So
  // `muted={videoMuted}` in the JSX below correctly mutes the element on first
  // render and NEVER unmutes it: the toolbar button flipped the icon, flipped
  // aria-pressed, and changed nothing audible. Reported from live use on
  // macOS 2026-08-12 ("the mute unmute icon does not have an effect").
  //
  // This is a long-standing React quirk (facebook/react#10389), not a bug in
  // this component's logic, which is why it survives a reading of the state
  // flow. The only reliable fix is to assign the property directly.
  useEffect(() => {
    const el = videoRef.current
    if (el) el.muted = videoMuted
  }, [videoMuted, mediaStream])

  // "Is a live WebRTC session actually attached" — the direct replacement
  // for the old JPEG-era `frame !== null` gate (see attachedRef's own doc
  // comment). Gates whether the interactive container/video sink mounts at
  // all; `videoReady` (below) separately gates whether it has decoded real
  // pixels yet.
  const attached = mediaStream !== null

  const isControlling = statusState === 'controlling'
  const webrtcErrorMessage = webrtcError
    ? translateWebRTCFallbackReason(webrtcError, webrtcErrorDetail ?? undefined)
    : null
  // Unified error surface: a transport-level error always wins; then a
  // WebRTC failure (there is nothing left to fall back to, so this is
  // terminal until the user retries or a fresh attempt succeeds); then the
  // latest browser_status{state:'error'} frame's message — gated on
  // `statusIsError` rather than `statusState === 'error'` so this still
  // surfaces even when onStatus deliberately left statusState alone (the
  // "error while controlling" case below). This is what actually renders
  // (both before and after the video attaches) — see the "!attached" branch
  // and the persistent error strip below.
  // firstFrameTimedOut (2026-08-03): the connection can be fully established
  // — WS connected, WebRTC stream attached, ZERO console errors — while no
  // real frame ever decodes, because the capture bound to a tab that is no
  // longer the one being shown. Live-measured on UAT: the panel sat on
  // "Waiting for the first frame…" and then fell to indistinguishable
  // black, with nothing anywhere telling the user it had failed. Silence is
  // the bug: without a deadline this state is visually identical to "still
  // loading" forever.
  //
  // Only armed once `connected` is true — before that the honest message is
  // "Connecting…", and a slow connect is not a first-frame failure.
  const [firstFrameTimedOut, setFirstFrameTimedOut] = useState(false)
  // Bumped by `retryWebRTC` (F7 fix, external review 2026-08-13) to re-arm a
  // FRESH FIRST_FRAME_TIMEOUT_MS deadline for a fresh negotiation attempt.
  // Without this, clicking Retry after a `firstFrameTimedOut` failure clears
  // the flag once via `setFirstFrameTimedOut(false)` but never re-runs this
  // effect (neither `videoReady` nor `connected` changes on retry — only the
  // WebRTC PC/DC gets torn down and rebuilt, not the WS `connected` state),
  // so no new timer is ever scheduled: a fresh attempt that ALSO never
  // decodes a frame would leave the panel stuck on "Waiting for the first
  // frame…" forever, silently, instead of re-reporting the same honest
  // failure after another full deadline.
  const [firstFrameDeadlineNonce, setFirstFrameDeadlineNonce] = useState(0)
  useEffect(() => {
    if (videoReady) {
      // A frame decoded (possibly after a previous timeout — a recapture can
      // recover): clear the deadline state so the error does not stick.
      setFirstFrameTimedOut(false)
      return
    }
    if (!connected) {
      setFirstFrameTimedOut(false)
      return
    }
    const timer = setTimeout(() => setFirstFrameTimedOut(true), FIRST_FRAME_TIMEOUT_MS)
    return () => clearTimeout(timer)
  }, [videoReady, connected, firstFrameDeadlineNonce])

  // Issue #674 — the capture-side verdict, ranked BELOW a signalling failure
  // (which is terminal for this viewer and therefore more specific) but ABOVE
  // both the generic status error and the first-frame deadline. That ordering
  // is the whole point of the frame: when the gateway has told us exactly what
  // happened, the panel must say THAT, not fall through to a 45s timeout's
  // guess about a stale tab.
  const videoHealthMessage = describeVideoHealth(videoHealth)

  const frameSupportError = frameCallbacksUnavailable || (frameGateState.status === 'locked' && frameGateState.reason === 'presentation-time-unavailable')
    ? 'Browser input is unavailable because this browser cannot confirm displayed video frames.'
    : null

  const displayError =
    connError ??
    webrtcErrorMessage ??
    videoHealthMessage ??
    frameSupportError ??
    (statusIsError ? statusMessage ?? 'The live browser session reported an error.' : null) ??
    (firstFrameTimedOut && !videoReady
      ? 'No video received from the live browser. The capture may be bound to a tab that is no longer active — try switching tabs or reloading the page.'
      : null)

  // ── ADR-040 D2 — "agent working" signal ───────────────────────────────────
  // Read directly from the per-session bucket (`sessionsById[sessionId]`),
  // NOT the store's top-level `isStreaming` foreground selector — the latter
  // is derived from whichever session is currently ACTIVE in chat, which is
  // not necessarily the (sessionId, agentId) pair this panel is pinned to
  // (the panel's props are captured once at mount and never re-read from the
  // session store — see the `key={sessionId:agentId}` comment on both hosts).
  const agentWorking = useChatStore((s) => s.sessionsById[sessionId]?.isStreaming ?? false)



  // ── ADR-040 D6 / ADR-043 D3 — agent identity for the header chip ──────────
  // Best-effort, read-only cache lookup against the SAME `['agents']` query
  // key the Activity Bar / Agents screen already populate (useRunningActivity.ts)
  // — deliberately NOT a fresh `useQuery` subscription here: this component is
  // mounted standalone by two very different hosts (the docked Sheet and the
  // fullscreen pop-out, which has no app-wide providers set up beyond its own
  // route tree), and a plain `queryClient.getQueryData` read needs no
  // `QueryClientProvider` in the tree, never issues its own network request.
  // `resolvedAgentName` is `undefined` when the cache miss — the header chip
  // and the UAT-fix hand-back hint below each pick their own grammatically
  // appropriate fallback rather than sharing one ('Agent' reads fine as a
  // chip label; a hint sentence needs "the agent").
  //
  // ADR-043 D3 / US-6: the full agent object (not just the name) is resolved
  // so the header's agent-identity chip can render the agent's avatar colour
  // + Phosphor icon the SAME way the composer AgentPicker / message avatars
  // do (reuse, not reinvent). With multiple agents browsing concurrently in
  // isolated per-agent browser contexts, this chip is the persistent identity
  // anchor that disambiguates WHICH agent's context the human is driving.
  const resolvedAgent = queryClient.getQueryData<Agent[]>(['agents'])?.find((a) => a.id === agentId)
  const resolvedAgentName = resolvedAgent?.name
  // `|| 'Agent'` (not `??`): an agent with an EMPTY name ("") should fall back
  // to "Agent" too, not render an empty chip label.
  const agentDisplayName = resolvedAgentName || 'Agent'

  // Server-confirmed presentation state, separate from input eligibility.
  const driveMode: DriveMode = computeDriveMode({ annotateMode, agentWorking, isControlling, connected, controlledByOther })

  // Show immediate feedback for a control request, bounded by its timeout.
  const visualDriveMode: DriveMode = computeDriveMode({
    annotateMode,
    agentWorking,
    isControlling: isControlling || pendingTake,
    connected,
    controlledByOther,
  })

  // ── ADR-040 D2/D6 — the "who's driving" VISUAL bucket, used by both the
  // header chip and the D6 glow border. Folds `disconnected`/`other-driving`
  // into `idle` (the chip itself further distinguishes those — see
  // `driveChip` below) and adds the `error` bucket, which isn't part of
  // `DriveMode` at all (a transport/status error is orthogonal to who's
  // driving). Converted from a nested ternary to if/else per CLAUDE.md.
  let visualState: VisualState
  if (visualDriveMode === 'annotating') {
    visualState = 'annotating'
  } else if (visualDriveMode === 'agent-working') {
    visualState = 'agent-working'
  } else if (visualDriveMode === 'you-driving') {
    visualState = 'you-driving'
  } else if (displayError) {
    visualState = 'error'
  } else {
    visualState = 'idle'
  }

  // Use the native pointer even while the agent is working.
  let cursorStyle: React.CSSProperties['cursor']
  if (driveMode === 'annotating') {
    cursorStyle = 'crosshair'
  } else if (driveMode === 'agent-working') {
    cursorStyle = 'default'
  } else if (driveMode === 'you-driving') {
    // Native cursor — no synthetic overlay (the user sees their real cursor,
    // which is more accurate than a rendered icon). The old 'none' + synthetic
    // cursor caused a double-cursor (native + yellow overlay) when cursor:none
    // didn't apply cleanly to the <img> child.
    cursorStyle = 'default'
  } else {
    cursorStyle = 'pointer'
  }

  // UAT finding A8 — the ONE place `pendingTakeRef`/`pendingTake` is ever
  // written, so the ref (synchronous guards) and the state (drives
  // `computeDriveMode`'s render-time read) can never drift apart. Stable
  // identity (empty deps — both setters it closes over are themselves
  // stable), safe to call from any effect/callback below, including ones
  // defined before this point in render order (function declarations don't
  // need to, and refs/setState setters are available from the first render).
  const setPendingTake = useCallback((value: boolean) => {
    pendingTakeRef.current = value
    setPendingTakeFlag(value)
  }, [])

  useEffect(() => {
    if (!pendingTake) return
    const timer = setTimeout(() => setPendingTake(false), 3000)
    return () => clearTimeout(timer)
  }, [pendingTake, setPendingTake])

  useEffect(() => {
    attachedRef.current = attached
  }, [attached])
  useEffect(() => {
    controllingRef.current = isControlling
    // ADR-040 D2: once the server confirms this connection holds the lock,
    // any implicit/explicit take that was in flight has resolved — clear the
    // in-flight guard so a FUTURE idle→drive transition can fire again.
    if (isControlling) setPendingTake(false)
  }, [isControlling, setPendingTake])
  useEffect(() => {
    controlledByOtherRef.current = controlledByOther
  }, [controlledByOther])
  useEffect(() => {
    connectedRef.current = connected
  }, [connected])
  useEffect(() => {
    driveModeRef.current = driveMode
  }, [driveMode])
  // Keep pendingAnnotationRef in sync so the unmount-cleanup effect below
  // (which must run with empty deps, i.e. read only refs) always revokes the
  // CURRENT preview object URL rather than a stale one captured on mount.
  useEffect(() => {
    pendingAnnotationRef.current = pendingAnnotation
  }, [pendingAnnotation])

  // Revoke any outstanding annotation preview object URL when the component
  // unmounts with a comment popover still open (e.g. the Sheet was closed
  // mid-annotation) — otherwise the blob URL leaks for the tab's lifetime.
  useEffect(() => {
    return () => {
      if (pendingAnnotationRef.current) {
        URL.revokeObjectURL(pendingAnnotationRef.current.previewUrl)
      }
    }
  }, [])

  // NOTE: annotate mode and driving (isControlling) are mutually exclusive
  // (ADR-039 D-B1/B2), enforced PROCEDURALLY rather than by a reactive
  // effect: handleToggleAnnotate releases control before entering annotate
  // mode, `computeDriveMode` gives `annotating` top priority over every
  // other mode (so canDispatchInput/visualState/cursor all agree annotate
  // wins even during the async release gap), and the pointer handlers
  // (handlePointerMove/Down/Up) branch on `annotateMode` BEFORE ever
  // consulting drive state — so no CDP input can be double-dispatched during
  // that gap. A reactive
  // `if (isControlling && annotateMode) setAnnotateMode(false)` effect used
  // to live here as a "belt and braces" guard, but it was actively harmful:
  // `sendControl('release')` is async (isControlling only flips once the
  // server's browser_status frame round-trips back), so the effect fired on
  // the very next render — while isControlling was still stale-true — and
  // immediately reverted the annotate-mode toggle the user just clicked,
  // making "Annotate" a silent no-op on the first click while driving.

  // ── WS lifecycle — one connection per mount (host keys this component by
  // `${sessionId}:${agentId}` so a new target always gets a fresh mount). ──
  // WebRTC build (W2-B): the PC state machine shares this SAME lifecycle —
  // one `BrowserWebRTCSession` per mount, created/torn down alongside the WS
  // connection so a fresh (sessionId, agentId) mount (or a WS-level
  // reconnect within an existing mount, handled by onConnected/onDisconnected
  // below) always starts from a clean signaling slate.
  useEffect(() => {
    captureRef.current = { id: null, generation: 0, marker: null, css: null, gate: new BrowserFrameGate(), retired: new Set() }
    streamIdentityRef.current = null
    freshViewerRef.current = null
    captureViewerNeededRef.current = false
    connectedRef.current = false
    setConnected(false)
    setWebrtcStream(null)
    refreshFrameGate()
    const machine = new BrowserWebRTCSession()
    webrtcRef.current = machine
    machine.onStream((stream, identity) => {
      if (!identity) return
      if (captureRef.current.id !== identity.captureId && !acceptCapture(identity.captureId, identity.generation)) return
      const current = captureRef.current
      if (requiresFreshViewerRef.current && identity.generation !== current.generation) return
      streamIdentityRef.current = identity
      currentStreamRef.current = stream
      if (freshViewerRef.current === `${identity.captureId}:${identity.generation}`) {
        if (!current.gate.bindFreshViewer(stream, identity.generation)) return
      } else {
        current.gate.bindStream(stream)
      }
      captureViewerNeededRef.current = false
      refreshFrameGate()
      setWebrtcStream(stream)
      setWebrtcError(null) // recovered
      setWebrtcErrorDetail(null)
    })
    machine.onInputChannelOpen(() => {
      inputChannelOpenRef.current = true
    })
    machine.onInputChannelClose(() => {
      inputChannelOpenRef.current = false
    })
    // Operator directive (JPEG-fallback removal) — WebRTC is the ONLY live-
    // video path left; there is nothing to silently swap to any more. Every
    // reason lands here, unconditionally: drops the stream, resets
    // `videoReady` (a fresh attempt must decode its own first frame), clears
    // the legacy DC-open flag, and records `reason` as a persistent error
    // (`webrtcError` → `displayError`/`webrtcErrorMessage` above) — never a
    // toast that could auto-dismiss unnoticed. `console.warn` always fires
    // too, for a support engineer reading the console. The machine itself
    // keeps retrying automatically in the background (exponential backoff,
    // up to its own retry budget — browserWebRTC.ts); the error UI's Retry
    // button is for after that budget is exhausted, or for a manual nudge.
    // Factored out (fix-wave, external review F1, 2026-08-13) so the SAME
    // reset-and-report logic can also run from `onWebRTCState` below for the
    // gap that callback covers on its own — see that handler's doc comment.
    const applyWebrtcFailure = (reason: string, detail?: string) => {
      // Logged as ONE argument when there is no gateway cause, not with a
      // trailing empty string: a console line reading `failed: ice-failed ""`
      // is noise, and the arity is what the sibling suites assert on.
      if (detail === undefined) {
        console.warn('[browser-live] WebRTC failed:', reason)
      } else {
        console.warn('[browser-live] WebRTC failed:', reason, detail)
      }
      captureRef.current.gate.bindStream(null)
      refreshFrameGate()
      setWebrtcStream(null)
      setWebrtcHasAudio(false)
      setVideoReady(false)
      setWebrtcError(reason)
      // Always written, never conditionally skipped: a fresh failure with no
      // detail must CLEAR the previous one's, or the panel would attribute an
      // old cause to a new failure.
      setWebrtcErrorDetail(detail ?? null)
      inputChannelOpenRef.current = false
    }
    machine.onFallback(applyWebrtcFailure)
    requestFreshViewerRef.current = () => {
      const current = captureRef.current
      if (!current.id || current.marker === null) return
      const key = `${current.id}:${current.generation}`
      if (freshViewerRef.current === key) return
      freshViewerRef.current = key
      current.gate.bindStream(null)
      machine.stop()
      machine.start((offer) => wsRef.current?.sendWebRTCOffer(offer) ?? false, { captureId: current.id, generation: current.generation })
    }

    const conn = new BrowserLiveWsConnection(sessionId, agentId, {
      // ADR-041 D4 — tab list + active index, broadcast on any
      // open/close/switch/title-change. This is the sole source of truth
      // the tab strip renders from (see the `tabState` doc comment above).
      onTabs: (f) => {
        setTabState({ tabs: f.tabs, activeIndex: f.active_index })
      },
      // Reviewer finding: a terminal browser_status{state:'error'} (e.g. a
      // blocked `navigate`) used to overwrite statusState unconditionally —
      // including while `controlling` — which flipped isControlling to
      // false (URL bar/cursor vanish, Take-control reappears) even though
      // the server never actually released control. `statusMessage` and
      // `statusIsError` always update (the error must still surface), but
      // `statusState` — the sole source of `isControlling` — is now only
      // overwritten by TRUE lifecycle frames (attached/controlling/
      // released/detached/idle); an error frame arriving while controlling
      // leaves the prior 'controlling' state alone via the functional
      // updater (correct even for two onStatus calls delivered in the same
      // tick, since it reads the latest pending value, not a stale closure).
      onStatus: (f) => {
        // ADR-040 D2: ANY status frame arriving means a full round-trip has
        // completed on this connection, so a take that was in flight (either
        // this one's or a stale one) has definitely been resolved one way or
        // another by now — clear the in-flight guard unconditionally rather
        // than only on the 'controlling' branch, so a REJECTED take (e.g. an
        // error frame, or another viewer beat us to it) doesn't leave
        // click-to-drive/Take-over permanently wedged. Also drops the
        // optimistic "you're driving" chip (UAT A8) if the take was in fact
        // rejected — see `visualDriveMode`'s doc comment.
        setPendingTake(false)
        // Reviewer finding F1: a `control_only` frame's SOLE purpose is to
        // broadcast a control-ownership change (take/release/detach) to the
        // OTHER viewers of this session — it carries no lifecycle/error
        // meaning (see BrowserStatusFrame.control_only's doc comment). Two
        // real bugs came from treating it like any other status frame: (a)
        // it arrives as state:'idle' with no message, so unconditionally
        // running the branches below WIPED a real, still-valid error banner
        // shown on this viewer; (b) applying `controlled_by_other ?? false`
        // to every frame meant a later tab-death/error frame (which omits
        // controlled_by_other) would reset it to false and wrongly
        // re-enable "Take control" while someone else was still driving.
        // Apply ONLY the control-ownership axis here and stop.
        if (f.control_only) {
          setControlledByOther(f.controlled_by_other ?? false)
          return
        }
        if (f.operation_only) {
          if (Date.now() - inputFailureAtRef.current >= 3000) {
            inputFailureAtRef.current = Date.now()
            useUiStore.getState().addToast({
              message: f.message ? translateBrowserErrorMessage(f.message) : 'The browser operation failed. Try again.',
              variant: 'error',
            })
          }
          return
        }
        // FE-7: only error-state messages get the raw-Go-string treatment —
        // other states' messages (if ever present) are left alone.
        setStatusMessage(f.state === 'error' && f.message ? translateBrowserErrorMessage(f.message) : f.message ?? null)
        setStatusIsError(f.state === 'error')
        setStatusState((prev) => (f.state === 'error' && prev === 'controlling' ? prev : f.state))
        // FE-6: only overwrite `controlledByOther` when the server actually
        // reported the field on THIS lifecycle/error frame — a frame that
        // omits it (e.g. a tab-death/error frame) must not reset a
        // still-true "someone else is driving" back to false.
        if (f.controlled_by_other !== undefined) setControlledByOther(f.controlled_by_other)
      },
      // ADR-047 (WebRTC build) — the gateway's non-trickle SDP answer to the
      // offer this connection sent (via the machine's `start` callback
      // below). Feeding a stale/unexpected answer is harmless — `applyAnswer`
      // itself no-ops unless the machine is actually `offering`.
      onWebRTCAnswer: (f) => {
        const current = captureRef.current
        if (f.capture_id && current.retired.has(f.capture_id)) return
        if (f.capture_id && current.id && f.capture_id !== current.id) {
          captureViewerNeededRef.current = true
          requestFreshViewerRef.current()
          return
        }
        if (machine.applyAnswer(f) && f.capture_id && f.capture_generation !== undefined) {
          acceptCapture(f.capture_id, f.capture_generation)
          refreshFrameGate()
        }
      },
      // ADR-047 — sent after attach and again on any availability change.
      // `applyState` handles the "fell over mid-session" fallback path;
      // starting the machine on an available:true signal is THIS
      // component's call (wave-plan W2-B wiring note) — `start()` itself is
      // idempotent while already offering/connected, so a repeated
      // available:true (e.g. a periodic re-affirmation) is a safe no-op.
      onWebRTCState: (f) => {
        setWebrtcHasAudio(f.has_audio ?? false)
        // ADR-062 tier 3: adopt the gateway's ICE servers (which carry this
        // viewer's short-lived TURN credentials) BEFORE start() builds the
        // PeerConnection below. Without them a client that cannot hole-punch
        // has no path at all, however healthy the gateway is.
        if (f.ice_servers && f.ice_servers.length > 0) {
          machine.setICEServers(
            f.ice_servers.map((s) => ({
              urls: s.urls,
              username: s.username,
              credential: s.credential,
            })),
          )
        }
        machine.applyState(f)
        if (f.available) {
          // fix-wave B (MED): `sendWebRTCOffer` returns false when the socket
          // was closed mid-ICE-gathering (a genuinely-async gap between when
          // gathering started and when it completes). Propagating that
          // boolean lets the machine (`_beginOffer`, browserWebRTC.ts) fall
          // back immediately with reason 'offer-send-failed' instead of
          // burning the full 5s answer timeout waiting for an answer that was
          // never going to arrive because the offer itself never left.
          machine.start((sdp) => wsRef.current?.sendWebRTCOffer(sdp) ?? false)
          return
        }
        // Bugfix (HIGH, external review F1, 2026-08-13): `applyState` above
        // (browserWebRTC.ts) deliberately only reacts to `available:false`
        // while the machine is `offering`/`connected` — its own doc comment
        // says deciding whether/when to react to an unavailable signal
        // BEFORE `start()` has ever been called is THIS caller's job, not
        // applyState's. But a capability-gate refusal (`disabled`/
        // `lite_build`/`not_capable`) arrives at ATTACH time, before
        // `start()` has EVER run — the machine is still `idle`, so
        // `applyState` no-ops, and (since `f.available` is false) the
        // `machine.start()` branch above is skipped too. NOTHING reacted:
        // the panel sat on "Connecting…" for the full FIRST_FRAME_TIMEOUT_MS
        // and then showed an unrelated "stale tab" message instead of the
        // real, honest capability-gate reason — exactly the silent-degrade
        // ADR-061 exists to eliminate. Cover the complement of applyState's
        // own condition: whenever the machine is NOT actively
        // offering/connected (idle, or already reporting a fallback),
        // nothing else is going to surface this, so surface it here. Reuses
        // the exact same reset-and-report logic `onFallback` uses above, so
        // this can never drift into different copy for the identical signal.
        if (machine.state !== 'offering' && machine.state !== 'connected') {
          applyWebrtcFailure(f.reason ?? 'unavailable', f.reason_detail)
        }
      },
      // Keep the last picture visible through capture recovery, but revoke
      // input proof until the recovered boundary is actually presented. RTP
      // metadata can prove a new generation on the existing peer; browsers
      // without it require a fresh peer authorized for the committed boundary.
      onVideoHealth: (f) => {
        if (f.capture_id && f.capture_generation !== undefined) {
          if (!acceptCapture(f.capture_id, f.capture_generation)) return
          if (f.css_width !== undefined && f.css_height !== undefined) {
            captureRef.current.css = { width: f.css_width, height: f.css_height }
            setFrameGeometryReady(true)
          }
          if (f.state === 'transitioning') releaseInputsRef.current()
          if (f.state === 'recovered' && f.rtp_timestamp !== undefined) {
            if (captureRef.current.marker !== f.rtp_timestamp) freshViewerRef.current = null
            captureRef.current.marker = f.rtp_timestamp
            captureRef.current.gate.acceptBoundary({ generation: f.capture_generation, rtpTimestamp: f.rtp_timestamp })
            if (requiresFreshViewerRef.current || captureViewerNeededRef.current || (streamIdentityRef.current && streamIdentityRef.current.captureId !== f.capture_id)) requestFreshViewerRef.current()
          }
          refreshFrameGate()
        }
        if (f.state === 'lost' || f.state === 'recovering' || f.state === 'unrecoverable') {
          releaseInputsRef.current()
          captureRef.current.gate.suspend()
          captureRef.current.marker = null
          freshViewerRef.current = null
          refreshFrameGate()
        }
        setVideoHealth(f.state === 'recovered' ? null : f)
      },
      onError: (message) => setConnError(message),
      onConnected: () => {
        setConnected(true)
        setConnError(null)
      },
      onDisconnected: () => {
        captureRef.current = { id: null, generation: 0, marker: null, css: null, gate: new BrowserFrameGate(), retired: new Set() }
        streamIdentityRef.current = null
        captureViewerNeededRef.current = false
        currentStreamRef.current = null
        freshViewerRef.current = null
        refreshFrameGate()
        pressedInputsRef.current.clear()
        setConnected(false)
        // The WebRTC session's fate is unknown once the signaling transport
        // that negotiated it drops — stop it outright (closes the PC/DC,
        // cancels any pending retry) rather than let it linger against a
        // gateway session that may already be gone. A fresh
        // `browser_webrtc_state` frame after the WS reconnects (onConnected
        // fires again, browser_attach re-sent) re-arms it via `start()`
        // above, exactly like a first attach.
        machine.stop()
        setWebrtcStream(null)
        setWebrtcHasAudio(false)
        setVideoReady(false)
        // A capture-health verdict is only trustworthy up to the drop; the
        // fresh browser_attach round-trip after reconnect re-establishes it.
        setVideoHealth(null)
        inputChannelOpenRef.current = false
        // The control-lock is server-side and per-connection — once the
        // transport drops, whatever control state we last knew is stale (the
        // human is no longer "driving" anything). Move to the local
        // 'disconnected' pill state so the UI stops claiming control is
        // held, the synthetic cursor clears (via the isControlling effect
        // below), and every pointer/keyboard/wheel handler's `controllingRef`
        // guard starts short-circuiting for the whole reconnect window —
        // re-establishing control requires an explicit take-control action
        // once the fresh browser_attach → browser_status round-trip lands.
        setStatusState('disconnected')
        // A stale error surface (e.g. a blocked-navigate message from just
        // before the drop) must not keep showing through a disconnect.
        setStatusIsError(false)
        // Same staleness reasoning as statusIsError above — the last-known
        // "someone else is driving" fact is only trustworthy up to the drop;
        // the fresh browser_attach → browser_status round-trip refreshes it.
        setControlledByOther(false)
        pendingMoveRef.current = null
        // ADR-040 D2: a disconnect mid-take (or mid implicit-drive gesture)
        // must not leave either guard stuck — the fresh browser_attach →
        // browser_status round-trip after reconnect starts clean. Also
        // drops the optimistic "you're driving" chip (UAT A8).
        setPendingTake(false)
      },
    })
    wsRef.current = conn
    conn.connect()
    return () => {
      releaseInputsRef.current()
      conn.detach()
      conn.close()
      wsRef.current = null
      machine.stop()
      webrtcRef.current = null
      requestFreshViewerRef.current = () => {}
      if (framePresentationTimerRef.current !== null) clearTimeout(framePresentationTimerRef.current)
    }
  }, [sessionId, agentId, acceptCapture, refreshFrameGate])

  // ── Bind the <video> sink's srcObject imperatively. React has no
  // `srcObject` JSX prop (it's a DOM property, not an attribute) — this is
  // the standard pattern. Re-runs whenever `mediaStream` changes (a fresh
  // stream after reconnect/recapture must rebind the SAME <video> element
  // rather than relying on a remount, since the element is already mounted
  // by the time this fires — the JSX below mounts <video> as soon as
  // `attached`/`mediaStream` is truthy, in the SAME render, so there is no
  // separate "element exists yet?" gap to bridge any more). Also resets
  // `videoReady` on every rebind — a new stream must prove it decodes its
  // own first frame before the "waiting" overlay clears; the frame callback
  // below confirms its expected presentation time. No-ops
  // whenever the element isn't currently mounted (mediaStream null →
  // nothing renders — see the "attached" gate in the JSX further down).
  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    video.srcObject = mediaStream ?? null
    currentStreamRef.current = mediaStream
    const current = captureRef.current
    if (current.id === null || streamIdentityRef.current?.captureId === current.id || mediaStream === mediaStreamProp) {
      // A replaced capture stays unbound until its new negotiated stream arrives.
      if (current.retired.size === 0 || streamIdentityRef.current?.captureId === current.id) current.gate.bindStream(mediaStream)
    }
    setVideoReady(false)
    setFrameCallbacksUnavailable(typeof video.requestVideoFrameCallback !== 'function')
    if (!mediaStream || typeof video.requestVideoFrameCallback !== 'function') return
    const stream = mediaStream
    let cancelled = false
    let callbackId = 0
    let firstDisplayAt: number | null = null
    let firstPresented = false
    let firstPresentationTimer: ReturnType<typeof setTimeout> | undefined
    const confirmFirstPresentation = () => {
      if (cancelled || video.srcObject !== stream || firstDisplayAt === null || firstPresented) return
      if (performance.now() >= firstDisplayAt) {
        firstPresented = true
        setVideoReady(true)
      } else {
        firstPresentationTimer = setTimeout(confirmFirstPresentation, Math.ceil(firstDisplayAt - performance.now()))
      }
    }
    const onFrame: VideoFrameRequestCallback = (_now, metadata) => {
      if (cancelled || video.srcObject !== stream) return
      if (firstDisplayAt === null && Number.isFinite(metadata.expectedDisplayTime) && metadata.expectedDisplayTime > 0) {
        firstDisplayAt = metadata.expectedDisplayTime
        confirmFirstPresentation()
      }
      const gate = captureRef.current.gate
      gate.observeFrame(stream, metadata, performance.now())
      refreshFrameGate()
      callbackId = video.requestVideoFrameCallback(onFrame)
    }
    callbackId = video.requestVideoFrameCallback(onFrame)
    return () => {
      cancelled = true
      video.cancelVideoFrameCallback(callbackId)
      if (firstPresentationTimer !== undefined) clearTimeout(firstPresentationTimer)
    }
  }, [mediaStream, mediaStreamProp, refreshFrameGate])

  // ── The single "what are the video sink's real pixel dimensions right
  // now" resolver — null until the <video> element has actually reported its
  // intrinsic size (`videoWidth`/`videoHeight` — 0 until the
  // `loadedmetadata` event fires). Every coordinate-mapping/annotate-crop
  // call routes through this so "no real dimensions yet" is handled in
  // exactly one place. Stable identity except when the `mediaStream` prop
  // itself changes — safe to call from any handler/effect below.
  const activeFrameDims = useCallback((): { width: number; height: number } | null => {
    const video = videoRef.current
    if (mediaStream && video && video.videoWidth > 0 && video.videoHeight > 0) {
      return { width: video.videoWidth, height: video.videoHeight }
    }
    return null
  }, [mediaStream])

  // ── The ONE place a client-space pointer coordinate is turned into the
  // CSS-page coordinate CDP dispatch/BrowserInputFrame expects. Replaces
  // four previously-duplicated call sites (wheel/pointerMove/pointerDown/
  // pointerUp below) with one routed call.
  const mapPointerToDeviceCoords = useCallback(
    (clientX: number, clientY: number, rect: RectLike, allowOutside = false): DeviceCoords | null => {
      const dims = activeFrameDims()
      const css = captureRef.current.css
      if (!dims || !css) return null
      return mapClientToBrowserCss(clientX, clientY, rect, dims.width, dims.height, css.width, css.height, allowOutside)
    },
    [activeFrameDims],
  )

  // Shared human input is independent of control ownership and chat state.
  const canIssueCommands = useCallback(() => connectedRef.current && driveModeRef.current !== 'annotating', [])
  const canDispatchInput = useCallback(() => {
    return canIssueCommands() && captureRef.current.gate.read(performance.now()).status === 'ready'
  }, [canIssueCommands])

  // All human input shares the ordered control socket. Successful send means
  // queued locally, never proof that Chrome executed it. Do not replay on a
  // second transport: a delayed click or text insertion could execute twice.
  const pressedInputsRef = useRef(new Map<string, Omit<BrowserInputFrame, 'type'>>())
  const releaseInputsRef = useRef<() => void>(() => {})
  const inputFailureAtRef = useRef(-Infinity)
  const dispatchInput = useCallback(
    (input: Omit<BrowserInputFrame, 'type'>, cleanup = false): boolean => {
      const initiating = ['navigate', 'back', 'forward', 'reload'].includes(input.kind)
      const current = captureRef.current
      const proof = current.gate.read(performance.now())
      if (!initiating && !cleanup && proof.status !== 'ready') return false
      const payload = !initiating && !cleanup && proof.status === 'ready' && current.id
        ? { ...input, capture_id: current.id, capture_generation: proof.generation }
        : input
      const sent = wsRef.current?.sendInput(payload) ?? false
      if (!sent) {
        if (Date.now() - inputFailureAtRef.current >= 3000) {
          inputFailureAtRef.current = Date.now()
          useUiStore.getState().addToast({ message: 'Browser input was not sent. Check the connection and try again.', variant: 'error' })
        }
        return false
      }
      const held = pressedInputsRef.current
      if (input.kind === 'key_down') held.set(`key:${input.code || input.key}`, { ...payload, kind: 'key_up', modifiers: 0 })
      if (input.kind === 'key_up') held.delete(`key:${input.code || input.key}`)
      if (input.kind === 'mouse_down') held.set(`button:${input.button}`, { ...payload, kind: 'mouse_up', modifiers: 0 })
      if (input.kind === 'mouse_up') held.delete(`button:${input.button}`)
      if (input.kind === 'mouse_move') {
        for (const [key, release] of held) {
          if (release.kind === 'mouse_up') held.set(key, { ...release, x: input.x, y: input.y })
        }
      }
      return true
    }, [],
  )
  const releasePressedInputs = useCallback(() => {
    pendingMoveRef.current = null
    pendingWheelRef.current = null
    const releases = [...pressedInputsRef.current.values()]
    pressedInputsRef.current.clear()
    for (const release of releases) dispatchInput(release, true)
  }, [dispatchInput])
  useEffect(() => {
    releaseInputsRef.current = releasePressedInputs
    const onVisibility = () => { if (document.hidden) releasePressedInputs() }
    window.addEventListener('blur', releasePressedInputs)
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      window.removeEventListener('blur', releasePressedInputs)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [releasePressedInputs])
  useEffect(() => {
    if (annotateMode) releasePressedInputs()
  }, [annotateMode, releasePressedInputs])

  // ── Adaptive viewport (2026-07-31 operator UAT) ──────────────────────────
  // Report the panel's render box so the backend can size the CAPTURED TAB to
  // match. Before this, the tab was pinned to a hardcoded 1280x720 while this
  // panel is an arbitrary, resizable shape (measured ~890x1010, portrait), and
  // since `object-fit: contain` preserves the SOURCE aspect the page could
  // fill only one dimension — the rest was letterboxed black and too small to
  // interact with. devicePixelRatio is sent as Chromium's deviceScaleFactor,
  // which fixes the same report's blur (headless Chrome renders at DPR 1, so
  // anything displayed larger upscales).
  //
  // Debounced hard (400ms) and gated on a meaningful delta, because the
  // backend responds by REBUILDING the capture stream — tabCapture constraints
  // are pinned per stream and cannot be renegotiated on a running track — and
  // each rebuild is a brief visible blip. Sending per resize event would make
  // dragging a window a strobe.
  const lastSentViewportRef = useRef<{ w: number; h: number; dpr: number } | null>(null)
  // settleRef holds the pending "has the size stopped changing?" check — see
  // the SETTLE CHECK in push() below.
  const settleRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => {
    const el = containerRef.current
    if (!el) return undefined
    let timer: ReturnType<typeof setTimeout> | null = null

    // Cold-start race (live UAT 2026-07-31, fresh machine): the attach-time
    // viewport frame can reach the gateway BEFORE the live view exists —
    // handleViewport drops it (applied=false) — and with the panel size never
    // changing again, the sub-threshold gate below suppressed every future
    // send, pinning the capture to launch geometry forever. This effect
    // re-runs when `attached` flips true (media now exists server-side), so
    // clearing the sent-marker here forces exactly one re-send at a moment
    // the server can actually apply it. Worst case is one duplicate frame,
    // which the server-side throttle absorbs.
    lastSentViewportRef.current = null

    const push = () => {
      // TRANSIENT-RESIZE GUARD (operator video 0804, 11 unintended rebuilds).
      //
      // Focusing the address bar makes Safari open its AutoFill accessory bar,
      // which shrinks the browser viewport — and therefore this container — by
      // ~50px. Measured in the recording: focused 644px, blurred 694px, focused
      // 644px again. Each transition pushed a viewport and forced a full
      // capture rebuild, which the operator sees as the panel resizing and
      // stalling for no reason they initiated.
      //
      // The container really did change size, so the ResizeObserver is not
      // wrong — what is wrong is treating a transient, focus-driven change like
      // a deliberate one. While focus sits in one of the panel's own inputs
      // (address bar, annotate field), hold the current geometry: the shrink
      // lasts exactly as long as the accessory bar does, and re-pushing on blur
      // would just replay the same rebuild in reverse.
      //
      // Deliberately checks LIVE focus rather than a stored flag: a blur that
      // never fires (element unmounted, window lost focus) would otherwise
      // wedge the guard on permanently, suppressing real resizes forever.
      //
      // This DEFERS the resize, it does not discard it. On a desktop browser
      // focusing the address bar does not change the container size at all, so
      // a genuine window resize performed while the address bar has focus
      // would otherwise be swallowed here and never recovered — no blur-driven
      // resize follows to replay it. The focusout listener registered below is
      // what closes that hole: it re-runs this same path once focus leaves, at
      // which point the size is real and either commits or dedups.
      if (textFieldHasFocus(el)) return

      const box = el.getBoundingClientRect()
      const w = Math.round(box.width)
      const h = Math.round(box.height)
      if (w < 1 || h < 1) return
      const dpr = window.devicePixelRatio || 1
      const prev = lastSentViewportRef.current
      // A rebuild costs a visible blip, so ignore sub-threshold jitter (a
      // scrollbar appearing, a 1px layout settle). 8px is below what a user
      // would notice as wrong aspect but well above incidental churn.
      if (prev && Math.abs(prev.w - w) < 8 && Math.abs(prev.h - h) < 8 && prev.dpr === dpr) return

      // SETTLE CHECK: only commit a size that has held still. A drag, an
      // animated sidebar, or a transient overlay produces a stream of
      // intermediate sizes; committing any of them costs a rebuild that the
      // next frame invalidates.
      //
      // On a mismatch this RE-ARMS itself rather than returning. Relying on a
      // trailing schedule() to come back would lose the resize outright
      // whenever the size stops changing DURING the settle window: the last
      // ResizeObserver event has already been consumed by the debounce, so
      // nothing else is pending, and the panel would stay the wrong size until
      // some unrelated resize happened to occur. Re-arming converges on the
      // final size on its own, and cannot spin — each pass either commits or
      // observes a NEW size, and a size that keeps changing forever is a
      // resize that is genuinely still in progress.
      const trySettle = (targetW: number, targetH: number) => {
        settleRef.current = setTimeout(() => {
          settleRef.current = null
          const now = el.getBoundingClientRect()
          const nw = Math.round(now.width)
          const nh = Math.round(now.height)
          // A degenerate measurement RE-ARMS rather than returns. Returning
          // here would recreate, one branch over, the lost-resize bug the
          // re-arm below exists to fix: a container that momentarily measures
          // 0x0 mid-chase (parent hidden for a tick, display:none flash during
          // an unrelated layout pass) would abandon the chase silently and the
          // panel would stay wrong-sized until some unrelated resize happened
          // to fire. Under the ResizeObserver path 0 -> real is itself an
          // observed change, but the window-resize FALLBACK path only hears
          // about window events, so there the resize really would be lost.
          // Re-arming costs one 250ms timer while the panel is hidden, which
          // browsers throttle anyway.
          if (nw < 1 || nh < 1) {
            trySettle(targetW, targetH)
            return
          }
          if (nw !== targetW || nh !== targetH) {
            trySettle(nw, nh) // still moving — chase the new size
            return
          }
          // Re-check the focus guard HERE, not only at push() time. A settle
          // armed while nothing was focused would otherwise commit a size
          // measured AFTER the user clicked into the address bar — the
          // AutoFill-shrunk geometry the guard exists to reject, slipping
          // through a 250ms window. Bailing without re-arming is safe ONLY
          // because the focusout listener below re-runs push() on blur; that
          // is what makes this a DEFERRAL rather than a drop.
          if (textFieldHasFocus(el)) return

          // Re-read the DPR instead of reusing push()'s: the chase can span a
          // window being dragged between displays with different pixel ratios.
          // That fires a window resize but need not change the container's CSS
          // box, so ResizeObserver may never re-enter push() — committing
          // push()'s stale DPR would stamp lastSentViewportRef with a ratio the
          // client no longer has, which is the mis-scale this field exists to
          // prevent.
          const settleDpr = window.devicePixelRatio || 1
          const settled = lastSentViewportRef.current
          // Re-check the dedup gate against the SETTLED size: the box may have
          // travelled away and come back, in which case there is nothing to
          // send and a rebuild would be pure cost.
          if (
            settled &&
            Math.abs(settled.w - nw) < 8 &&
            Math.abs(settled.h - nh) < 8 &&
            settled.dpr === settleDpr
          ) {
            return
          }
          if (wsRef.current?.sendViewport(nw, nh, settleDpr)) {
            lastSentViewportRef.current = { w: nw, h: nh, dpr: settleDpr }
          }
        }, VIEWPORT_SETTLE_MS)
      }
      if (settleRef.current !== null) clearTimeout(settleRef.current)
      trySettle(w, h)
    }

    const schedule = () => {
      if (timer !== null) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = null
        push()
      }, 400)
    }

    // Initial push once connected — the WS may not be open on first mount, so
    // `connected` is a dependency and this re-runs when it flips true.
    if (connected) schedule()

    // BLUR CATCH-UP — the other half of the focus guard, and the thing that
    // makes suppression a deferral instead of a silent drop.
    //
    // Both review passes independently proved the same hole: a REAL resize
    // that completes while a text field holds focus is suppressed by the
    // guard and then never retried, because recovery was left to "some later
    // resize event" that need not ever come. On a desktop browser, focusing
    // the address bar does not change the container size at all, so blur
    // produces no resize either — drag the window larger while typing a URL
    // and the panel stayed pinned to the old geometry indefinitely.
    //
    // focusout fires on every focus departure, including the accessory-bar
    // case (where it is redundant — the size change fires its own resize —
    // and harmlessly dedups). Routing through schedule() rather than push()
    // reuses the debounce, so focus churn cannot stampede the settle chase.
    document.addEventListener('focusout', schedule)
    window.visualViewport?.addEventListener('resize', schedule)

    // ResizeObserver is guarded, not assumed. Adaptive sizing is an
    // enhancement; an environment without the API (jsdom under test, an old
    // or locked-down browser) must still get a working panel rather than a
    // crash that takes the whole live view down — which is exactly what an
    // unguarded `new ResizeObserver` did when this landed (174 tests, 8
    // files, all "ResizeObserver is not defined"). Falling back to window
    // resize still tracks the common case, since the panel is sized by the
    // viewport; it just misses container-only changes such as the sidebar
    // being pinned or unpinned.
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', schedule)
      return () => {
        if (timer !== null) clearTimeout(timer)
        if (settleRef.current !== null) clearTimeout(settleRef.current)
        window.removeEventListener('resize', schedule)
        document.removeEventListener('focusout', schedule)
      window.visualViewport?.removeEventListener('resize', schedule)
      }
    }

    const ro = new ResizeObserver(schedule)
    ro.observe(el)
    return () => {
      if (timer !== null) clearTimeout(timer)
      if (settleRef.current !== null) clearTimeout(settleRef.current)
      document.removeEventListener('focusout', schedule)
      window.visualViewport?.removeEventListener('resize', schedule)
      ro.disconnect()
    }
    // `attached` is load-bearing, not incidental (BLOCKER caught in review):
    // containerRef is only attached once a session is attached (the
    // container div is gated on `attached`), but `connected` flips true from
    // ws.onopen — SECONDS before a WebRTC stream ever attaches, since the
    // backend's cold capture start can run ~25s. With `[connected]` alone
    // this effect ran exactly once, while containerRef.current was still
    // null, bailed, and never re-ran — so sendViewport was NEVER called on a
    // fresh open and the capture stayed at its hardcoded default. Same
    // dependency shape the wheel listener below already uses, for the same
    // reason.
  }, [connected, attached])

  const flushPendingMove = useCallback(() => {
    const pending = pendingMoveRef.current
    if (!pending) return
    pendingMoveRef.current = null
    // Reviewer finding (queued-move leak): re-validate the drive gate at
    // FLUSH time, not just at schedule time — the generation can change
    // (or the connection can drop, or annotate mode can engage) in the gap
    // between the pointermove that scheduled this flush and the animation
    // frame/timer actually firing. Without this, a queued position captured
    // while still driving could leak into the tab a frame later, after
    // annotation or disconnection has disabled input.
    if (!canDispatchInput()) return
    dispatchInput(
      {
        kind: 'mouse_move',
        x: pending.x,
        y: pending.y,
        modifiers: pending.modifiers,
      },
    )
  }, [canDispatchInput, dispatchInput])

  // Coalesce moves on a TIMER, not on requestAnimationFrame (operator report,
  // 2026-08-04: "inputs feel laggy/slowish", worst while a video plays).
  //
  // rAF ties the network send to the LOCAL RENDERING pipeline. Whenever the
  // page is busy compositing — exactly what happens while the panel is
  // decoding and painting a video stream — frame callbacks stretch out, so
  // pointer positions queue behind rendering work that has nothing to do with
  // them. The delay is also unpredictable, which is what makes it feel
  // sluggish rather than merely slow.
  //
  // A fixed interval decouples the two: input flows at a steady, known rate
  // regardless of what the compositor is doing. MOVE_FLUSH_MS is set below the
  // server's own rate cap so the pacing stays client-side and predictable
  // instead of being shaped by server-side drops.
  const flushPendingWheel = useCallback(() => {
    const pending = pendingWheelRef.current
    if (!pending) return
    pendingWheelRef.current = null
    // Same flush-time re-validation as the move drain: the capture can change,
    // annotate mode can engage, or the socket can drop between the wheel event
    // and this tick.
    if (!canDispatchInput()) return
    dispatchInput({
      kind: 'wheel',
      x: pending.x,
      y: pending.y,
      delta_x: pending.deltaX,
      delta_y: pending.deltaY,
      modifiers: pending.modifiers,
    })
  }, [canDispatchInput, dispatchInput])

  // Move is drained BEFORE wheel: that is the order a real browser delivers
  // them in when a pointer moves and scrolls in the same tick, and the remote
  // page's hit-testing depends on the cursor already being where the scroll
  // happens.
  const flushPendingInput = useCallback(() => {
    inputFlushScheduledRef.current = false
    inputFlushTimerRef.current = null
    flushPendingMove()
    flushPendingWheel()
  }, [flushPendingMove, flushPendingWheel])

  const cancelInputFlush = useCallback(() => {
    if (inputFlushTimerRef.current !== null) clearTimeout(inputFlushTimerRef.current)
    inputFlushTimerRef.current = null
    inputFlushScheduledRef.current = false
  }, [])

  const scheduleInputFlush = useCallback(() => {
    if (inputFlushScheduledRef.current) return
    inputFlushScheduledRef.current = true
    inputFlushTimerRef.current = setTimeout(flushPendingInput, MOVE_FLUSH_MS)
  }, [flushPendingInput])

  // ── Native (non-passive) wheel listener — React's synthetic onWheel is
  // passive by default, so preventDefault() inside a JSX handler would warn
  // and no-op. Attached once; reads live state via refs to avoid re-binding
  // on every incoming frame. ──
  useEffect(() => {
    const el = containerRef.current
    if (!el) return undefined
    function onWheel(e: WheelEvent) {
      // Annotation and disconnection block remote wheel input;
      // annotate mode excludes driving too (canDispatchInput's driveMode
      // gives annotating top priority — closes a latent gap where a stale
      // isControlling:true during the annotate-entry release race used to
      // let a scroll through).
      if (!canDispatchInput() || !attachedRef.current) return
      e.preventDefault()
      const rect = el!.getBoundingClientRect()
      const device = mapPointerToDeviceCoords(e.clientX, e.clientY, rect)
      if (!device) return
      // Accumulate; the shared pacer dispatches. preventDefault() still has to
      // happen synchronously above, or the host page scrolls instead.
      const prev = pendingWheelRef.current
      pendingWheelRef.current = {
        x: device.x,
        y: device.y,
        modifiers: computeModifiers(e),
        deltaX: (prev?.deltaX ?? 0) + e.deltaX,
        deltaY: (prev?.deltaY ?? 0) + e.deltaY,
      }
      scheduleInputFlush()
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [attached, canDispatchInput, mapPointerToDeviceCoords, scheduleInputFlush])


  // Cancel any in-flight coalesced move on unmount — nothing to flush once
  // the socket is about to be closed/detached by the WS lifecycle effect.
  useEffect(() => {
    return () => {
      if (inputFlushTimerRef.current !== null) clearTimeout(inputFlushTimerRef.current)
      inputFlushTimerRef.current = null
      inputFlushScheduledRef.current = false
      pendingMoveRef.current = null
      pendingWheelRef.current = null
    }
  }, [])

  // Record human activity without pausing chat or waiting for ownership.
  const takeWheelIfNeeded = useCallback(() => {
    if (!connectedRef.current) return
    if (controllingRef.current) return // already driving — nothing to acquire
    if (pendingTakeRef.current) return // a take is already in flight — never double-fire
    setPendingTake(true)
    const sent = wsRef.current?.sendControl('take')
    if (!sent) {
      setPendingTake(false)
      useUiStore.getState().addToast({
        message: 'Could not confirm taking control — click Take over again if needed.',
        variant: 'error',
      })
    }
  }, [setPendingTake])

  // ── Annotate mode (ADR-039 D-B1/B2) ─────────────────────────────────────

  const resetSelection = useCallback(() => {
    annotateDraggingRef.current = false
    selectionStartClientRef.current = null
    setSelectionStart(null)
    setSelectionCurrent(null)
  }, [])

  const handleCancelAnnotation = useCallback(() => {
    if (pendingAnnotationRef.current) URL.revokeObjectURL(pendingAnnotationRef.current.previewUrl)
    setPendingAnnotation(null)
    setAnnotateComment('')
    setAnnotateError(null)
    resetSelection()
  }, [resetSelection])

  const handleToggleAnnotate = useCallback(() => {
    if (annotateMode) {
      setAnnotateMode(false)
      handleCancelAnnotation()
      return
    }
    // Mutually exclusive with driving (ADR-039 D-B1/B2) — release control
    // first so pointer events stop being forwarded as remote input the
    // instant annotate mode takes over.
    if (isControlling) {
      wsRef.current?.sendControl('release')
    }
    // ADR-040 D2: also abandon any implicit-drive gesture/in-flight take —
    // entering Pen mid-gesture must not let a delayed take ack silently
    // start driving again out from under the now-active annotate mode.
    // (computeDriveMode already gives 'annotating' top priority over a
    // stale pendingTake, but clearing it here too keeps the ref and the
    // reactive state from drifting once annotate mode exits again.)
    setPendingTake(false)
    setAnnotateMode(true)
  }, [annotateMode, isControlling, handleCancelAnnotation, setPendingTake])

  // Crops the CURRENTLY-RENDERED <video> sink to a PNG File (mirrors the
  // canvas pattern in media-actions.ts's fetchImagePng). Reads the live
  // element at call time (not a stale snapshot) so the crop always reflects
  // exactly what the user was looking at when they finished the drag/click.
  // `readyState >= 2` (HAVE_CURRENT_DATA) means "there is an actual decoded
  // frame available to draw right now," not just "the element exists in the
  // DOM." `drawCropToPngFile` reuses `scaleCropToImagePixels` to correct for
  // a recapture-driven resolution change landing mid-gesture (see that
  // function's own doc comment).
  const cropFrameToFile = useCallback(
    async (rect: FrameCropRect, frameWidth: number, frameHeight: number): Promise<File | null> => {
      const video = videoRef.current
      if (!video || video.readyState < 2 || video.videoWidth === 0 || video.videoHeight === 0) return null
      return drawCropToPngFile(video, video.videoWidth, video.videoHeight, rect, frameWidth, frameHeight)
    },
    [],
  )

  // Finalizes a drag/click selection into a pendingAnnotation (crop + open
  // the comment popover). Never forwards anything over the control-input WS
  // path — annotate mode is a purely local, client-side interaction until
  // the user hits Send (submitAnnotation).
  const finalizeSelection = useCallback(
    async (containerRect: DOMRect, startClient: { x: number; y: number }, endClient: { x: number; y: number }) => {
      // No popover is open yet at any failure point here (pendingAnnotation
      // is only set on full success below) — setAnnotateError would render
      // into a popover that doesn't exist, and silently returning would leave
      // the frozen selection box up with no feedback (a stuck-looking UI).
      // Every failure path — frame gone, unmeasurable container, degenerate
      // crop rect, or the crop itself failing — toasts and resets instead.
      const fail = () => {
        useUiStore.getState().addToast({ message: 'Could not capture that region — try again.', variant: 'error' })
        resetSelection()
      }
      // activeFrameDims() resolves to the <video>'s videoWidth/videoHeight —
      // the SAME resolver the pointer/wheel handlers use, so the crop rect
      // computed here always matches what cropFrameToFile actually draws
      // from.
      const dims = activeFrameDims()
      if (!dims) return fail()
      // Letterbox correction (2026-07-31) — the precondition this component's
      // `fillContainer` doc comment names for enabling that mode alongside
      // `canAnnotate`. Under `fillContainer` the media element is `w-full
      // h-full object-contain`, so it fills the box and REAL letterbox /
      // pillarbox bars appear whenever the container's aspect ratio differs
      // from the content's — which is the normal case for the docked panel (a
      // tall, narrow column showing a wide page). Mapping a client point
      // against the raw container rect would then treat the bars as part of
      // the image and skew every crop toward the wrong region.
      //
      // The pointer/wheel path already did this (see mapPointerToDeviceCoords);
      // the annotate path did not, which is precisely why the two could not be
      // combined. Routing the rect through the same helper removes that
      // restriction. Degenerate/zero-size boxes are handled inside
      // computeObjectContainRect, and when aspect ratios happen to match it
      // returns the box unchanged — so this is a no-op in the non-letterboxed
      // case rather than a behaviour change.
      const contentRect = computeObjectContainRect(containerRect, dims.width, dims.height)
      const startPx = mapClientToFramePixels(startClient.x, startClient.y, contentRect, dims.width, dims.height)
      const endPx = mapClientToFramePixels(endClient.x, endClient.y, contentRect, dims.width, dims.height)
      if (!startPx || !endPx) return fail()
      const cropRect = computeCropRect(startPx, endPx, dims.width, dims.height)
      if (!cropRect) return fail()

      const file = await cropFrameToFile(cropRect, dims.width, dims.height)
      if (!file) return fail()
      const center = framePixelToDeviceCoords(cropRect.x + cropRect.width / 2, cropRect.y + cropRect.height / 2)
      setPendingAnnotation({ file, previewUrl: URL.createObjectURL(file), point: center })
    },
    [cropFrameToFile, resetSelection, activeFrameDims],
  )

  // Address-bar navigation uses the same ordered connection as live input.
  const handleOmniboxSubmit = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault()
      const resolved = resolveOmniboxInput(urlInput)
      if (!resolved) return
      if (!canIssueCommands()) return
      setStatusMessage(null)
      setStatusIsError(false)
      takeWheelIfNeeded()
      releasePressedInputs()
      if (!dispatchInput({ kind: 'navigate', url: resolved })) return
      setUrlInput(resolved)
      urlBarEditingRef.current = false
    },
    [urlInput, takeWheelIfNeeded, canIssueCommands, dispatchInput, releasePressedInputs],
  )

  const handleToolbarNav = useCallback(
    (kind: 'navigate_back' | 'reload') => {
      if (!canIssueCommands()) return
      setStatusMessage(null)
      setStatusIsError(false)
      takeWheelIfNeeded()
      releasePressedInputs()
      dispatchInput({ kind })
    },
    [takeWheelIfNeeded, canIssueCommands, dispatchInput, releasePressedInputs],
  )

  const handleTabSwitch = useCallback((index: number) => {
    if (!canIssueCommands()) return
    releasePressedInputs()
    takeWheelIfNeeded()
    const sent = wsRef.current?.sendTabAction('switch', index)
    if (!sent) {
      useUiStore.getState().addToast({ message: 'Could not switch tabs — check your connection and try again.', variant: 'error' })
    }
  }, [takeWheelIfNeeded, canIssueCommands, releasePressedInputs])

  const handleTabClose = useCallback((index: number) => {
    if (!canIssueCommands()) return
    releasePressedInputs()
    takeWheelIfNeeded()
    const sent = wsRef.current?.sendTabAction('close', index)
    if (!sent) {
      useUiStore.getState().addToast({ message: 'Could not close that tab — check your connection and try again.', variant: 'error' })
    }
  }, [takeWheelIfNeeded, canIssueCommands, releasePressedInputs])

  const handleTabOpen = useCallback(() => {
    if (!canIssueCommands()) return
    releasePressedInputs()
    takeWheelIfNeeded()
    const sent = wsRef.current?.sendTabAction('open')
    if (!sent) {
      useUiStore.getState().addToast({ message: 'Could not open a new tab — check your connection and try again.', variant: 'error' })
    }
  }, [takeWheelIfNeeded, canIssueCommands, releasePressedInputs])

  const handlePointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (annotateMode) {
      if (!annotateDraggingRef.current || !containerRef.current) return
      const rect = containerRef.current.getBoundingClientRect()
      setSelectionCurrent({ x: e.clientX - rect.left, y: e.clientY - rect.top })
      return
    }
    if (!canDispatchInput() || !attachedRef.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    // Local cursor overlay updates immediately every event — only the
    // network send is throttled, so the synthetic cursor still tracks the
    // pointer at full native resolution.
    const device = mapPointerToDeviceCoords(e.clientX, e.clientY, rect)
    if (!device) return
    // Keep the CSS position computed at this event through the deferred send.
    pendingMoveRef.current = {
      x: device.x,
      y: device.y,
      modifiers: computeModifiers(e),
    }
    scheduleInputFlush()
  }, [scheduleInputFlush, annotateMode, canDispatchInput, mapPointerToDeviceCoords])

  // Focuses the frame container (so keyboard input starts flowing) and
  // best-effort captures the pointer so a drag that leaves the container
  // bounds (a fast selection, or a slider drag while driving) still
  // delivers move/up events here — without this, releasing outside the
  // frame would leave the remote page thinking the mouse button is still
  // held down. Shared by both handlePointerDown branches (annotate-drag
  // start and remote mouse_down) — the capture step is identical either way.
  const focusAndCapturePointer = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    containerRef.current?.focus()
    try {
      e.currentTarget.setPointerCapture(e.pointerId)
    } catch {
      // Pointer capture is best-effort — unsupported/jsdom environments fall
      // back to normal bounds-limited pointer events.
    }
  }, [])

  const handlePointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (annotateMode) {
      // pendingAnnotationRef (not state) — a selection popover already open
      // must be Cancelled or Sent before starting a new drag; reading the
      // ref (rather than adding pendingAnnotation to the dep array) keeps
      // this callback's identity stable across every popover open/close.
      if (!attachedRef.current || !containerRef.current || pendingAnnotationRef.current) return
      focusAndCapturePointer(e)
      const rect = containerRef.current.getBoundingClientRect()
      const point = { x: e.clientX - rect.left, y: e.clientY - rect.top }
      selectionStartClientRef.current = { x: e.clientX, y: e.clientY }
      annotateDraggingRef.current = true
      setSelectionStart(point)
      setSelectionCurrent(point)
      return
    }
    // Require a presented current page and valid content coordinates before
    // changing the control indicator or capturing the pointer.
    if (!attachedRef.current || !activeFrameDims() || !containerRef.current) return
    if (!canDispatchInput()) return
    const rect = containerRef.current.getBoundingClientRect()
    const device = mapPointerToDeviceCoords(e.clientX, e.clientY, rect)
    if (!device) return
    if (driveModeRef.current !== 'you-driving') takeWheelIfNeeded()
    focusAndCapturePointer(e)

    // Drop any coalesced move still waiting to be sent (operator report,
    // 2026-08-04: a click on the video's fullscreen button "did not show the
    // effect"). mouse_down maps its OWN fresh coordinates from this event, so
    // it is always accurate — but a pending move captured up to MOVE_FLUSH_MS
    // earlier would otherwise flush AFTER it, and the remote browser would see
    // press-at-target followed by move-from-an-older-position. That reordering
    // can drag the press off its target or cancel the click outright.
    //
    // The stale position is not merely redundant, it is actively wrong: this
    // event's coordinates supersede it. Clearing the scheduled flag too means
    // the next real move re-arms the timer normally.
    pendingMoveRef.current = null
    cancelInputFlush()
    // A pending WHEEL is flushed rather than dropped: unlike the move, it is
    // not superseded by this event, and letting it land after the press would
    // scroll the target out from under a click that already happened.
    flushPendingWheel()

    dispatchInput(
      {
        kind: 'mouse_down',
        x: device.x,
        y: device.y,
        button: mapMouseButton(e.button),
        modifiers: computeModifiers(e),
      },
    )
  }, [
    annotateMode,
    canDispatchInput,
    activeFrameDims,
    focusAndCapturePointer,
    takeWheelIfNeeded,
    mapPointerToDeviceCoords,
    dispatchInput,
    cancelInputFlush,
    flushPendingWheel,
  ])

  const handlePointerUp = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (annotateMode) {
      if (!annotateDraggingRef.current || !containerRef.current) {
        annotateDraggingRef.current = false
        return
      }
      annotateDraggingRef.current = false
      const startClient = selectionStartClientRef.current
      const rect = containerRef.current.getBoundingClientRect()
      setSelectionCurrent({ x: e.clientX - rect.left, y: e.clientY - rect.top })
      if (startClient) {
        void finalizeSelection(rect, startClient, { x: e.clientX, y: e.clientY })
      }
      return
    }
    // The implicit-drive gesture window (if any) ends with this pointerup
    // regardless of what happens below — captured BEFORE clearing so
    // canDispatchInput sees the value that was true for this gesture's
    // duration, not the just-cleared one.
    if (!canDispatchInput() || !attachedRef.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    const release = pressedInputsRef.current.get(`button:${mapMouseButton(e.button)}`)
    if (!release) return
    const device = mapPointerToDeviceCoords(e.clientX, e.clientY, rect, true)
    if (!device) {
      dispatchInput(release, true)
      return
    }
    // Same supersede-the-stale-move rule as handlePointerDown, applied to the
    // OTHER end of the gesture. mouse_up maps its own fresh coordinates, so a
    // coalesced move captured up to MOVE_FLUSH_MS earlier would land AFTER the
    // release and the remote page would see release-at-target followed by a
    // move from a stale position. For a drag, slider, or text selection — all
    // of which read the last move relative to the release — that is a wrong
    // drop point, not merely a redundant event. A pending WHEEL is flushed
    // rather than dropped, for the same ordering reason as in pointerdown.
    pendingMoveRef.current = null
    cancelInputFlush()
    flushPendingWheel()
    dispatchInput(
      {
        kind: 'mouse_up',
        x: device.x,
        y: device.y,
        button: mapMouseButton(e.button),
        modifiers: computeModifiers(e),
      },
    )
  }, [
    annotateMode,
    finalizeSelection,
    canDispatchInput,
    mapPointerToDeviceCoords,
    dispatchInput,
    cancelInputFlush,
    flushPendingWheel,
  ])

  const handleSendAnnotation = useCallback(() => {
    const annotation = pendingAnnotation
    const comment = annotateComment.trim()
    if (!annotation || comment.length === 0) return
    setAnnotateSubmitting(true)
    setAnnotateError(null)
    // sessionId/agentId — this view's own pinned props, NOT re-read from
    // useSessionStore — so the annotation always targets the browser being
    // annotated even if the globally-active chat has since changed.
    submitAnnotation({ comment, file: annotation.file, point: annotation.point, sessionId, agentId })
      .then(() => {
        useUiStore.getState().addToast({ message: 'Annotation sent to the agent.', variant: 'success' })
        URL.revokeObjectURL(annotation.previewUrl)
        setPendingAnnotation(null)
        setAnnotateComment('')
        setAnnotateMode(false)
        resetSelection()
      })
      .catch((err: unknown) => {
        if (err instanceof AnnotationBusyError) {
          // Never a silent no-op: surface via toast AND keep the popover
          // open (pendingAnnotation is untouched) so the user can just hit
          // Send again once the agent is free.
          useUiStore.getState().addToast({ message: err.message, variant: 'error' })
          return
        }
        setAnnotateError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        setAnnotateSubmitting(false)
      })
  }, [pendingAnnotation, annotateComment, resetSelection, sessionId, agentId])

  const releaseWheel = useCallback(() => {
    const released = wsRef.current?.sendControl('release')
    if (!released) {
      setStatusState('released')
      useUiStore.getState().addToast({
        message: 'Could not confirm releasing control — try again if needed.',
        variant: 'error',
      })
    }
    addressBarRef.current?.focus()
  }, [])

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!canDispatchInput()) return
    // WCAG 2.1.2 "No Keyboard Trap" — Escape is the advertised, always
    // available way to stop driving (see the hand-back hint below, which now
    // advertises it too). This panel used to be hosted in a Radix Sheet,
    // where Escape closing the dialog first gave an INCIDENTAL — if
    // undiscoverable and unadvertised — way out, via a capture-phase
    // document listener that fired before this bubble-phase handler ever
    // ran. The Sheet was retired 2026-07-16 for an always-docked panel (no
    // dialog, no capture-phase Escape listener anywhere above this element
    // anymore), so that incidental escape hatch stopped existing while
    // Escape here stayed a local no-op: every OTHER key is forwarded/
    // preventDefault()'d while driving, so a keyboard-only user who tabbed
    // into the frame had NO way to leave it at all. Escape now actively
    // releases the wheel and returns focus to the address bar — a real,
    // advertised exit that satisfies 2.1.2 on its own, independent of
    // whatever container happens to host this component.
    if (e.key === 'Escape') {
      e.preventDefault()
      releaseWheel()
      return
    }
    e.preventDefault()
    const modifiers = computeModifiers(e)
    if (isPrintableKey(e)) {
      dispatchInput({ kind: 'text', text: e.key, modifiers })
    } else {
      // key_code (DOM KeyboardEvent.keyCode) is REQUIRED for CDP to actually
      // perform editing/navigation keys (Backspace, Delete, Enter, Tab,
      // arrows) and modifier shortcuts (Ctrl+A/C/V) — key/code alone deliver
      // the event but don't delete/submit/move/select. See ADR-039.
      dispatchInput({ kind: 'key_down', key: e.key, code: e.code, key_code: e.keyCode, modifiers })
    }
  }, [canDispatchInput, releaseWheel, dispatchInput])

  const handleKeyUp = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!canDispatchInput()) return
    // Escape's release already happened on keydown above — nothing left to
    // forward for its key_up half (and driveMode may still read stale
    // 'you-driving' for the brief async gap before the release ack lands).
    if (e.key === 'Escape') return
    e.preventDefault()
    // 'text' input is a one-shot insert (no matching key_up — mirrors
    // Input.insertText on the backend, which has no down/up phase).
    if (!isPrintableKey(e) && pressedInputsRef.current.has(`key:${e.code || e.key}`)) {
      dispatchInput({ kind: 'key_up', key: e.key, code: e.code, key_code: e.keyCode, modifiers: computeModifiers(e) })
    }
  }, [canDispatchInput, dispatchInput])

  // ── ADR-040 D6 — header chip config (icon + text label + colour), derived
  // from `visualState`. Words + icon back up the colour for accessibility
  // (never colour alone). The 'idle' bucket further distinguishes connection
  // lifecycle (connecting/reconnecting) from a genuinely idle, ready-to-drive
  // frame — the old corner pill's connecting/disconnected states still need
  // SOME visible home now that the pill itself is gone.
  const driveChip = (() => {
    if (visualState === 'agent-working') {
      return { label: `${agentDisplayName} is browsing…`, Icon: Robot, textClass: 'text-[var(--color-info)]', dotClass: 'bg-[var(--color-info)]', pulse: true }
    }
    if (visualState === 'you-driving') {
      return { label: "You're driving", Icon: Cursor, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: true }
    }
    if (visualState === 'annotating') {
      return { label: "You're annotating", Icon: ChatCircleDots, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: false }
    }
    if (visualState === 'error') {
      return { label: 'Error', Icon: WarningCircle, textClass: 'text-[var(--color-error)]', dotClass: 'bg-[var(--color-error)]', pulse: false }
    }
    // 'idle' visualState — visualDriveMode further distinguishes
    // disconnected/other-driving/genuinely-idle, reading the SAME display
    // source of truth `visualState` itself derives from, instead of
    // re-deriving `!connected`/`controlledByOther` here too.
    if (visualDriveMode === 'disconnected') {
      return {
        label: statusState === 'disconnected' ? 'Reconnecting…' : 'Connecting…',
        Icon: SpinnerGap,
        textClass: 'text-[var(--color-muted)]',
        dotClass: 'bg-[var(--color-muted)]',
        pulse: false,
      }
    }
    if (visualDriveMode === 'other-driving') {
      // Informational, NOT a lock-out. Control is shared — this viewer's mouse,
      // keyboard and omnibox all still work while someone else is also active
      // (operator directive, 2026-08-03). The old label read "Someone else is
      // driving", which told the user their input would be ignored — and it
      // was, because the client and server both gated on the lock. Both gates
      // are gone; the chip now just says who else is here.
      return { label: 'Also viewing', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
    }
    return { label: 'Click to drive', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
  })()

  // ADR-040 D2/D6 — "Take over" affordance's aria-label/title (reviewer
  // finding: this exact ternary was duplicated across both attributes — the
  // F5 anti-pattern reintroduced). One computation, used by both.
  const takeOverLabel = controlledByOther
    ? 'Someone else is currently driving'
    : `Take over — pause ${agentDisplayName} and take control`

  // Operator directive (JPEG-fallback removal) — the one manual "try live
  // video again" entry point, offered wherever `displayError` is shown (the
  // empty-state error and the stuck-decoding overlay below). Clears the
  // stale error optimistically so the UI immediately reflects "trying
  // again" rather than showing the old failure while a fresh attempt is
  // already in flight; a repeat failure re-populates it via `onFallback`.
  // The machine's own automatic retry (exponential backoff, its own budget
  // — browserWebRTC.ts) runs independently of this; this button is for
  // after that budget is exhausted, or a manual nudge.
  //
  // Bugfix (MED, external review F7, 2026-08-13): `machine.start()` alone
  // (the old body) is a documented no-op once the machine is already
  // 'offering' or 'connected' (browserWebRTC.ts's own doc comment) — exactly
  // the state a `firstFrameTimedOut` failure leaves it in (ICE connected,
  // stream attached, just no decoded pixels), so clicking Retry for THAT
  // failure did nothing observable: no new offer went out, and neither
  // `firstFrameTimedOut` nor `webrtcError` was cleared, so the identical
  // message just sat there looking clicked-and-ignored. `stop()` first
  // forces a clean teardown (closes the stale PC/DC, cancels any pending
  // auto-retry, resets the machine to 'idle') so the following `start()`
  // always begins a genuinely fresh negotiation attempt, regardless of what
  // state the machine was previously stuck in — and `firstFrameTimedOut` is
  // now cleared here too, alongside `webrtcError`, since it is the OTHER
  // source `displayError` can come from (see `displayError`'s own doc
  // comment) and a real retry attempt must reset both, not just one.
  const retryWebRTC = () => {
    setWebrtcError(null)
    setWebrtcErrorDetail(null)
    // #674: Retry must clear EVERY source displayError can come from, or the
    // click looks ignored — the same defect F7 fixed for firstFrameTimedOut.
    setVideoHealth(null)
    setFirstFrameTimedOut(false)
    setFirstFrameDeadlineNonce((n) => n + 1)
    releaseInputsRef.current()
    captureRef.current.gate.bindStream(null)
    freshViewerRef.current = null
    refreshFrameGate()
    webrtcRef.current?.stop()
    webrtcRef.current?.start((sdp) => wsRef.current?.sendWebRTCOffer(sdp) ?? false)
  }

  return (
    <div className={cn('flex h-full min-h-0 flex-col bg-[var(--color-primary)]', className)}>
      {/* == Row A: tabs + window controls =============================
          Header consolidation (operator direction, 2026-08-04): the panel used
          to spend FOUR rows on chrome -- identity/controls, handback hint,
          tabs, omnibox -- measured at 156px of a 900px panel (17.3 percent),
          all of it taken from the remote page. It is now two, the way Chrome
          and Safari do it: tabs share the top strip with the window controls,
          everything else rides the toolbar below.

          This row is UNCONDITIONAL even though the tab strip inside it is not.
          Close and Pop-out live here now, and gating the row on
          `tabs.length > 0` would take the only way to close the panel with it
          the moment the tab list arrived empty.

          FIXED height (h-browser-tabs), like the toolbar below: this panel
          pushes its own box as the remote viewport, so a header row that
          changes height forces a full capture rebuild. That is the same
          measured regression the handback hint was made always-mounted for. */}
      <div className="flex h-browser-tabs min-h-browser-tabs shrink-0 items-center gap-1 px-2">
        {tabState && tabState.tabs.length > 0 ? (
          <div
            role="group"
            aria-label="Browser tabs"
            data-testid="browser-tab-strip"
            className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto"
            style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
          >
            {tabState.tabs.map((tab) => {
              const active = tab.index === tabState.activeIndex
              const label = tabLabel(tab)
              return (
                <div
                  key={tab.index}
                  className={cn(
                    'flex shrink-0 max-w-[180px] items-center gap-1.5 rounded-t-md border-b-2 py-1 pl-2.5 pr-1 text-xs transition-colors',
                    // Active tab: Forge-Gold underline + full opacity + a
                    // heavier label weight — colour is never the only signal
                    // (WCAG). Inactive: dimmed, transparent underline.
                    active
                      ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)] text-[var(--color-secondary)] opacity-100'
                      : 'border-transparent text-[var(--color-muted)] opacity-70 hover:bg-[var(--color-surface-1)] hover:opacity-100',
                  )}
                >
                  <button tabIndex={0}
                    type="button"
                    aria-pressed={active}
                    disabled={!connected}
                    onClick={() => handleTabSwitch(tab.index)}
                    title={tab.title || tab.url || 'New tab'}
                    data-testid={`browser-tab-${tab.index}`}
                    className={cn(
                      'flex min-w-0 flex-1 items-center gap-1.5',
                      connected ? 'cursor-pointer' : 'cursor-not-allowed',
                      'disabled:cursor-not-allowed',
                    )}
                  >
                    <Globe size={12} weight={active ? 'fill' : 'regular'} className="shrink-0" />
                    <span className={cn('min-w-0 flex-1 truncate', active && 'font-medium')}>{label}</span>
                  </button>
                  <button tabIndex={0}
                    type="button"
                    onClick={() => handleTabClose(tab.index)}
                    disabled={!connected}
                    aria-label={`Close tab: ${label}`}
                    title="Close tab"
                    data-testid={`browser-tab-close-${tab.index}`}
                    className="shrink-0 rounded p-0.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-1)] hover:text-[var(--color-secondary)] disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    <X size={10} weight="bold" />
                  </button>
                </div>
              )
            })}
            <button tabIndex={0}
              type="button"
              onClick={handleTabOpen}
              disabled={!connected}
              aria-label="Open new tab"
              title="Open a new tab"
              data-testid="browser-tab-new"
              className="shrink-0 rounded p-1 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              <Plus size={13} />
            </button>
          </div>
        ) : (
          <div className="min-w-0 flex-1" />
        )}
        {onPopOut && (
          <button tabIndex={0}
            type="button"
            onClick={onPopOut}
            aria-label="Pop out"
            title="Pop out into its own window"
            className={TOOLBAR_ICON_BTN}
          >
            <ArrowSquareOut size={16} />
          </button>
        )}
        {onClose && (
          <button tabIndex={0}
            type="button"
            onClick={onClose}
            aria-label="Close live browser panel"
            title="Close"
            className={TOOLBAR_ICON_BTN}
          >
            <X size={16} />
          </button>
        )}
      </div>

      {/* == Row B: toolbar ============================================
          [back] [refresh] [address] then the status/identity chips and the
          mode toggles that used to occupy their own row above the tabs. The
          address field is deliberately no longer the full width of the panel;
          it gives that space to the controls, which is what removes the row.

          Only the address input is wrapped in the <form>. Enter-to-submit is
          all the form was ever for, and keeping the toggles outside it avoids
          implying they take part in submission. */}
      <div className="flex h-chrome-header min-h-chrome-header shrink-0 items-center gap-1 px-2">
        <button tabIndex={0}
          type="button"
          onClick={() => handleToolbarNav('navigate_back')}
          disabled={!connected} /* not gated on controlledByOther: control is shared (2026-08-03) */
          aria-label="Go back"
          title="Back"
          className={TOOLBAR_ICON_BTN}
        >
          <CaretLeft size={16} weight="bold" />
        </button>
        <button tabIndex={0}
          type="button"
          onClick={() => handleToolbarNav('reload')}
          disabled={!connected} /* not gated on controlledByOther: control is shared (2026-08-03) */
          aria-label="Refresh page"
          title="Refresh"
          className={TOOLBAR_ICON_BTN}
        >
          <ArrowsClockwise size={15} />
        </button>
        {/* min-w floor is load-bearing, not cosmetic: with `min-w-0 flex-1`
            alone the field collapsed to 23px on a 575px row (measured on UAT
            v59) once the chips and toggles were added beside it — flex happily
            takes a min-content:0 item to zero. The floor makes "the address bar
            stays usable" a guarantee instead of an arithmetic coincidence that
            holds only until the next control is added. */}
        <form onSubmit={handleOmniboxSubmit} className="flex min-w-[120px] flex-1 items-center">
        <Input
          ref={addressBarRef}
          type="text"
          value={urlInput}
          onChange={(e) => setUrlInput(e.target.value)}
          onFocus={() => {
            urlBarEditingRef.current = true
          }}
          onBlur={() => {
            urlBarEditingRef.current = false
          }}
          placeholder="Search or enter a URL…"
          aria-label="Address bar"
          className="h-8 flex-1 text-xs"
        />
        </form>
        <span
          data-testid="browser-live-agent-chip"
          title={`Driving ${agentDisplayName}'s browser context`}
          className="flex shrink-0 items-center gap-1.5 px-1 text-[11px] font-medium text-[var(--color-secondary)] whitespace-nowrap"
        >
          <span
            aria-hidden="true"
            className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[8px] font-bold text-[var(--color-primary)]"
            style={{ backgroundColor: resolvedAgent?.color ?? 'var(--color-surface-3)' }}
          >
            {resolvedAgent?.icon ? (
              <IconRenderer icon={resolvedAgent.icon} size={9} />
            ) : resolvedAgent && resolvedAgent.name ? (
              initialOf(resolvedAgent.name)
            ) : (
              <Robot size={9} />
            )}
          </span>
          {/* Avatar always; the NAME yields first when the row is tight —
              identity survives as the coloured avatar, and the full name is in
              this chip's own `title`. */}
          <span className="hidden max-w-[140px] truncate xl:inline">{agentDisplayName}</span>
        </span>
        <span
          data-testid="browser-live-status-chip"
          className={cn('flex shrink-0 items-center gap-1.5 px-1 text-[11px] font-medium whitespace-nowrap', driveChip.textClass)}
        >
          <span
            aria-hidden="true"
            className={cn('h-1.5 w-1.5 shrink-0 rounded-full', driveChip.dotClass, driveChip.pulse && 'motion-safe:animate-pulse')}
          />
          <driveChip.Icon size={12} weight={driveChip.pulse ? 'fill' : 'regular'} />
          {driveChip.label}
        </span>
        {canAnnotate && (
          <button tabIndex={0}
            type="button"
            onClick={handleToggleAnnotate}
            disabled={!connected}
            aria-label={annotateMode ? 'Exit annotate mode' : 'Annotate a region'}
            title={annotateMode ? 'Exit annotate mode' : 'Drag a region (or click a spot) to comment on it'}
            aria-pressed={annotateMode}
            className={cn(TOOLBAR_ICON_BTN, annotateMode && 'text-[var(--color-accent)]')}
          >
            <ChatCircleDots size={16} weight={annotateMode ? 'fill' : 'regular'} />
          </button>
        )}
        {mediaStream && hasAudio && (
          <button tabIndex={0}
            type="button"
            onClick={() => setVideoMuted((m) => !m)}
            aria-label={videoMuted ? 'Unmute audio' : 'Mute audio'}
            title={videoMuted ? 'Unmute audio' : 'Mute audio'}
            aria-pressed={!videoMuted}
            data-testid="browser-live-mute-toggle"
            className={TOOLBAR_ICON_BTN}
          >
            {videoMuted ? <SpeakerSlash size={16} /> : <SpeakerHigh size={16} />}
          </button>
        )}
      </div>


      {/* Body */}
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-black p-2">
        {/* State overlay — kept for the data-visual-state attribute (tests +
            future use) but the visible border/frame is REMOVED per operator
            direction. The header chip is the sole driving-state signal now. */}
        <div
          aria-hidden="true"
          data-testid="browser-live-glow"
          data-visual-state={visualState}
          className="pointer-events-none absolute inset-0 z-30"
        />
        {/* Handback discoverability (UAT: neither tester worked out that sending
            a chat message hands control back). It lived on its own 23px row,
            then briefly on the toolbar — where it reserved 185px of a 575px row
            and collapsed the address bar to 23px. It belongs on neither: it is
            transient guidance about the FRAME, so it overlays the frame.
            Absolutely positioned, so it costs zero layout and can never move
            the frame — which is what the always-mounted rule was protecting
            against (a header height change forces a full capture rebuild).
            Still ALWAYS MOUNTED and merely `invisible` when idle.
            pointer-events-none so it can never swallow a click meant for the
            page. Names BOTH exits on screen: advertising the Esc escape is what
            satisfies WCAG 2.1.2 (No Keyboard Trap), which a tooltip would not. */}
        <p
          data-testid="browser-live-handback-hint"
          aria-hidden={visualState !== 'you-driving'}
          className={cn(
            'pointer-events-none absolute inset-x-0 bottom-0 z-40 truncate px-3 py-1.5 text-center text-[11px] text-[var(--color-secondary)]',
            'bg-black/60 backdrop-blur-sm',
            visualState !== 'you-driving' && 'invisible',
          )}
        >
          Send a message to hand back to {resolvedAgentName ?? 'the agent'} — or press Esc to stop driving
        </p>
        {!attached && (
          <div className="flex min-w-0 max-w-full flex-col items-center gap-2 p-6 text-center text-sm text-[var(--color-muted)]">
            {displayError ? (
              <>
                <WarningCircle size={22} className="text-[var(--color-error)]" />
                <p className="max-w-full [overflow-wrap:anywhere] text-[var(--color-error)]">{displayError}</p>
                <button
                  type="button"
                  tabIndex={0}
                  onClick={retryWebRTC}
                  data-testid="browser-live-retry"
                  className="mt-1 rounded-full border border-[var(--color-border)] px-3 py-1 text-xs font-medium text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]"
                >
                  Retry
                </button>
              </>
            ) : (
              <>
                <SpinnerGap size={20} className="animate-spin" />
                <p>{connected ? 'Starting live video…' : 'Connecting to the live browser…'}</p>
              </>
            )}
          </div>
        )}

        {attached && (
          <div
            ref={containerRef}
            tabIndex={0}
            role="application"
            aria-label="Live browser view"
            data-testid="browser-live-frame"
            // BUG 1 fix: `fillContainer` (pop-out route) fills the available
            // body space instead of shrink-wrapping the media's intrinsic
            // size — see the prop's own doc comment on why the two layouts
            // must differ and why that's safe for coordinate mapping
            // (computeObjectContainRect in mapPointerToDeviceCoords).
            className={cn(
              'relative select-none outline-none',
              fillContainer ? 'h-full w-full' : 'inline-block max-h-full max-w-full',
            )}
            // ADR-040 D6 — hover cursor communicates the mode at the point of
            // interaction: hidden (synthetic cursor takes over) while
            // actually driving, "watching"/not-allowed while the agent works
            // (pointer handlers no-op there — see handlePointerDown/Move/Up),
            // a plain pointer while idle (click-to-drive is one click away).
            style={{ cursor: cursorStyle }}
            onPointerMove={handlePointerMove}
            onPointerDown={handlePointerDown}
            onPointerUp={handlePointerUp}
            onKeyDown={handleKeyDown}
            onKeyUp={handleKeyUp}
            onBlur={releasePressedInputs}
            onPointerCancel={releasePressedInputs}
            onLostPointerCapture={releasePressedInputs}
            onDragStart={(e) => e.preventDefault()}
          >
            {/* The ONLY video sink — mounted the instant a WebRTC stream is
                attached (`attached`), independently of whether it has
                presented a real frame yet (`videoReady`, tracked by the
                frame callback) — the element has to exist and be
                bound before it can ever report a frame. The "waiting for
                first frame" overlay just below covers the gap honestly
                instead of showing a silent black box. Starts muted
                (autoplay-safe) — see the mute toggle button in the header
                above and the srcObject-binding effect earlier in this
                component. No <track> captions: this is a live remote-control
                surface (mirrors the agent's own screen), not authored video
                content. */}
            <video
              ref={videoRef}
              autoPlay
              playsInline
              muted={videoMuted}
              onLoadedData={(event) => {
                if (typeof event.currentTarget.requestVideoFrameCallback !== 'function') setVideoReady(true)
              }}
              aria-label="Live browser session"
              data-testid="browser-live-video"
              // BUG 1 fix: fillContainer stretches to 100% of the
              // (now-container-sized) box and lets object-contain do the
              // aspect-preserving fit — the OLD `h-auto w-auto max-h-full
              // max-w-full` combination can shrink but never grow past
              // intrinsic size, which is exactly what left the pop-out's
              // video tiny and letterboxed inside a much larger window.
              className={cn(
                'block select-none object-contain',
                fillContainer ? 'h-full w-full' : 'h-auto max-h-full w-auto max-w-full',
              )}
            />
            {/* "Waiting for first frame" honesty gate (2026-08-03 UAT fix) —
                the stream can be attached (ICE connected, track live) while
                no real pixels ever decode, because the capture is bound to a
                tab that is no longer the one being shown. Overlays the still
                (black) video rather than un-mounting it, so frame callbacks
                remain reachable and a late recovery clears this without a
                remount. `firstFrameTimedOut` promotes the spinner to the
                same honest, actionable error + Retry the top-level empty
                state uses once FIRST_FRAME_TIMEOUT_MS elapses with nothing
                decoded. */}
            {!videoReady && (
              <div
                data-testid="browser-live-waiting-overlay"
                className="pointer-events-none absolute inset-0 z-20 flex flex-col items-center justify-center gap-2 bg-black/70 p-6 text-center text-sm text-[var(--color-muted)]"
              >
                {displayError ? (
                  <>
                    <WarningCircle size={22} className="text-[var(--color-error)]" />
                    <p className="max-w-full [overflow-wrap:anywhere] text-[var(--color-error)]">{displayError}</p>
                    <button
                      type="button"
                      tabIndex={0}
                      onClick={retryWebRTC}
                      data-testid="browser-live-retry-overlay"
                      className="pointer-events-auto mt-1 rounded-full border border-[var(--color-border)] px-3 py-1 text-xs font-medium text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]"
                    >
                      Retry
                    </button>
                  </>
                ) : (
                  <>
                    <SpinnerGap size={20} className="animate-spin" />
                    <p>Waiting for the first frame…</p>
                  </>
                )}
              </div>
            )}
            {/* Synthetic cursor removed — the native cursor is used directly
                when driving (more accurate, no double-cursor). The agent's
                pointer is visible in the live video itself. */}
            {/* Selection-box overlay (ADR-039 D-B1/B2) — container-relative CSS
                coords, drawn live while dragging and frozen once the
                selection finalizes into pendingAnnotation (comment popover
                below stays anchored to it). */}
            {annotateMode && selectionStart && selectionCurrent && (
              <div
                data-testid="annotate-selection-box"
                className="pointer-events-none absolute z-10 border-2 border-[var(--color-accent)] bg-[var(--color-accent)]/15"
                style={{
                  left: Math.min(selectionStart.x, selectionCurrent.x),
                  top: Math.min(selectionStart.y, selectionCurrent.y),
                  width: Math.abs(selectionCurrent.x - selectionStart.x),
                  height: Math.abs(selectionCurrent.y - selectionStart.y),
                }}
              />
            )}
          </div>
        )}

        {/* ADR-040 D2/D6 — "Take over" — the ONLY affordance shown while
            agent activity (an explicit action to pause the response). Adjacent
            to the frame (not a header button — D1's header stays limited to
            Close/Pin/Pen/Pop-out). Rendered whenever agent-working, even
            before the video has attached, so the user can pause the agent
            immediately rather than waiting on the first decoded frame. */}
        {visualState === 'agent-working' && (
          <div className="pointer-events-none absolute inset-x-0 top-3 z-20 flex justify-center">
            <button tabIndex={0}
              type="button"
              onClick={() => {
                useChatStore.getState().cancelStream(sessionId)
                takeWheelIfNeeded()
              }}
              // No longer disabled by controlledByOther (2026-08-03): another
              // attached viewer must never make this button dead, since taking
              // over from the agent is exactly what the user is trying to do.
              disabled={!connected}
              aria-label={takeOverLabel}
              title={takeOverLabel}
              className="pointer-events-auto flex items-center gap-1.5 rounded-full border border-[var(--color-info)]/50 bg-[var(--color-surface-1)]/90 px-3 py-1.5 text-xs font-medium text-[var(--color-secondary)] shadow-lg backdrop-blur transition-colors hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              <HandGrabbing size={13} />
              Take over
            </button>
          </div>
        )}

        {/* Annotate comment popover (ADR-039 D-B1/B2) — appears once a
            drag/click selection finalizes into a cropped pendingAnnotation.
            Bottom-anchored (rather than positioned relative to the possibly
            edge-of-frame selection box) to sidestep viewport-clamping math;
            the frozen selection-box overlay above stays visible so the
            connection to what's being discussed is still clear. */}
        {annotateMode && pendingAnnotation && (
          <div
            data-testid="annotate-popover"
            className="absolute inset-x-0 bottom-0 z-20 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 shadow-lg"
          >
            <div className="flex items-start gap-3">
              <img
                src={pendingAnnotation.previewUrl}
                alt="Selected region"
                className="h-16 w-16 shrink-0 rounded border border-[var(--color-border)] object-cover"
              />
              <div className="min-w-0 flex-1">
                <Textarea
                  value={annotateComment}
                  onChange={(e) => setAnnotateComment(e.target.value)}
                  onKeyDown={(e) => {
                    // Cancel the pending annotation on Escape. Historical
                    // note (now stale): this used to also matter for
                    // outrunning Radix's Sheet, which listened for Escape
                    // via a capture-phase document listener that would
                    // otherwise close the whole panel and discard the
                    // drafted comment first. The Sheet was retired
                    // 2026-07-16 (panel is now always a plain docked
                    // `<aside>` — no Sheet, no capture-phase listener
                    // anywhere above this element), so that race no longer
                    // exists; Escape here is just the ordinary "cancel this
                    // popover" affordance. stopPropagation is kept as
                    // defense in depth against any future wrapping
                    // dialog/modal reintroducing the same race.
                    if (e.key === 'Escape') {
                      e.stopPropagation()
                      handleCancelAnnotation()
                    }
                  }}
                  placeholder="What would you like to discuss about this?"
                  aria-label="Annotation comment"
                  className="min-h-[60px] text-xs"
                  disabled={annotateSubmitting}
                  autoFocus
                />
                {annotateError && (
                  <p role="alert" className="mt-1 text-[11px] text-[var(--color-error)]">
                    {annotateError}
                  </p>
                )}
                <div className="mt-2 flex justify-end gap-2">
                  <button tabIndex={0}
                    type="button"
                    onClick={handleCancelAnnotation}
                    disabled={annotateSubmitting}
                    className="rounded px-2.5 py-1 text-xs text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    Cancel
                  </button>
                  <button tabIndex={0}
                    type="button"
                    onClick={handleSendAnnotation}
                    disabled={annotateSubmitting || annotateComment.trim().length === 0}
                    className="rounded bg-[var(--color-accent)] px-3 py-1 text-xs font-medium text-[var(--color-primary)] transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    {annotateSubmitting ? 'Sending…' : 'Send'}
                  </button>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Persistent error strip — shown once the video is ATTACHED AND READY
          (the empty-state branch and the "waiting for first frame" overlay
          above already surface displayError before then, each with its own
          Retry). Covers a transport error (connError) or a terminal
          browser_status{state:'error'} (already-controlled,
          take-control-disabled, no-manager-for-agent, live-view-disabled,
          malformed control, …) arriving AFTER the video was already playing
          fine — a semantic status error is just as visible as a transport
          one. `webrtcError` never reaches this branch: a WebRTC failure
          clears `mediaStream`, which flips `attached` false and routes the
          user to the top-level empty-state error instead. */}
      {attached && videoReady && !displayError && (frameGateState.status !== 'ready' || !frameGeometryReady) && (
        <div role="status" className="shrink-0 border-t border-[var(--color-border)] px-4 py-2 text-xs text-[var(--color-text-secondary)]">
          {frameCallbacksUnavailable || (frameGateState.status === 'locked' && frameGateState.reason === 'presentation-time-unavailable')
            ? 'Browser input is unavailable because this browser cannot confirm displayed video frames.'
            : frameGateState.status === 'ready' && !frameGeometryReady
              ? 'Pointer input is unavailable until the page size is confirmed.'
            : frameGateState.status === 'needs-fresh-viewer'
              ? 'Reconnecting video to restore browser input…'
              : 'Waiting for the current page to appear before enabling browser input…'}
        </div>
      )}
      {attached && videoReady && displayError && (
        <div role="alert" className="min-w-0 shrink-0 [overflow-wrap:anywhere] border-t border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-2 text-xs text-[var(--color-error)]">
          {displayError}
        </div>
      )}
    </div>
  )
}
