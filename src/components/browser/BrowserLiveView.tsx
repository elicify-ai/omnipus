// BrowserLiveView — shared live-view core for the ADR-038/039/040 interactive
// browser panel. Rendered by two callers:
//   1. BrowserLivePanel.tsx  — inside the app-root Sheet overlay (or docked
//      beside chat when pinned — layout is entirely the panel owner's call)
//   2. routes/_app/browser-live.tsx — the fullscreen pop-out window
//
// Owns the second WS connection (browserLiveWs.ts), the latest screencast
// frame, the ADR-040 "implicit control" state machine (D2), and
// pointer/keyboard capture while driving. Deliberately has NO knowledge of
// how it's being hosted (Sheet vs. docked vs. fullscreen route) — onPopOut/
// onClose/onTogglePin are optional callbacks so each host wires up its own
// chrome semantics (window.open vs. store close vs. window.close vs. ui
// store pin toggle).
//
// ADR-040 "Take the wheel" redesign: control is no longer a persistent
// Take/Release toggle. It is implicit and contextual, derived from two
// existing signals — the live-view control lock (unchanged, still owned by
// this component) and the chat store's per-session `isStreaming` for THIS
// panel's pinned (sessionId, agentId) (agent's turn in flight):
//   - agent working (isStreaming) → watch-only: pointer/keyboard/wheel do
//     NOT drive the page; a "Take over" affordance pauses the agent (reuses
//     the chat store's existing cancelStream — the same action the chat
//     Stop button calls) and then acquires the lock.
//   - agent idle + user doesn't hold the lock → the first pointer
//     interaction on the frame implicitly acquires the lock, then dispatches
//     that same input.
//   - user holds the lock → drives normally (unchanged from ADR-038/039).
// The old "Hand to agent" button is gone — handing back is just sending a
// chat message (the shared tab means the agent resumes on the current page).

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  ArrowSquareOut,
  ChatCircleDots,
  Cursor,
  Eye,
  Globe,
  HandGrabbing,
  Monitor,
  Plus,
  PushPin,
  PushPinSlash,
  Robot,
  SpinnerGap,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { BrowserLiveWsConnection } from '@/lib/browserLiveWs'
import {
  computeCropRect,
  computeModifiers,
  framePixelToDeviceCoords,
  isPrintableKey,
  mapClientToDevice,
  mapClientToFramePixels,
  mapMouseButton,
  scaleCropToImagePixels,
  type FrameCropRect,
} from '@/lib/browserLiveCoords'
import { resolveOmniboxInput } from '@/lib/browserLiveUrl'
import { submitAnnotation, AnnotationBusyError } from '@/lib/browserAnnotate'
import { useUiStore } from '@/store/ui'
import { useChatStore } from '@/store/chat'
import { queryClient } from '@/lib/queryClient'
import type { Agent } from '@/lib/api'
import type { BrowserScreencastFrame, BrowserStatusFrame, BrowserTabsFrame } from '@/lib/api/generated/asyncapi-types'

export interface BrowserLiveViewProps {
  sessionId: string
  agentId: string
  /** Rendered as a header "Pop out" button when provided — exactly like `onClose`/`onTogglePin` below. */
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
  /**
   * ADR-040 D4/D6 — display-only: whether the panel owner currently has this
   * view docked beside chat (pinned) rather than in the overlay Sheet. This
   * view stays otherwise agnostic to the layout. Reviewer finding: an
   * earlier version also used `isPinned` internally to hide the "Pop out"
   * button — that duplicated a decision the panel owner already has to make
   * (a docked panel popping into its own window is a layout no-op the caller
   * would have to reconcile anyway) as a SECOND, easy-to-drift gate on top
   * of `onPopOut`'s own presence. Pop-out now renders purely on `onPopOut`
   * being provided, exactly like `onClose`/`onTogglePin` — the host simply
   * stops passing `onPopOut` once pinned.
   */
  isPinned?: boolean
  /**
   * ADR-040 D1/D4 — renders a header "Pin" toggle when provided; omitted
   * entirely (button hidden) by hosts that don't support docking, exactly
   * like `onPopOut`/`onClose`. The pin/unpin layout swap itself is owned by
   * the caller (BrowserLivePanel.tsx + the `ui` store) — this view only
   * renders the affordance and reflects `isPinned`'s current value.
   */
  onTogglePin?: () => void
  className?: string
}

/** ADR-040 D2/D6 — the three (+ one) mutually-exclusive visual/control states. */
type VisualState = 'agent-working' | 'you-driving' | 'annotating' | 'error' | 'idle'

/**
 * ADR-040 D2 refactor (reviewer finding) — the single "who can drive right
 * now" mode. Previously this was re-derived independently in five different
 * places (the wheel listener, handlePointerMove/Down/Up, handleKeyDown/Up,
 * the cursor style ternary, and the `visualState` ternary), each combining
 * `agentWorkingRef`/`controllingRef`/`annotateMode`/`connected`/
 * `controlledByOther` in a slightly different order — the cursor ternary
 * checked `isControlling` BEFORE `agentWorking` while `visualState` checked
 * `agentWorking` first, so a stale `isControlling:true` during the brief gap
 * before the auto-release effect's ack landed showed a "driving" cursor
 * (none, synthetic cursor visible) at the exact moment the Take-over overlay
 * was already up and dispatch was already blocked.
 *
 * `computeDriveMode` is the ONE place priority is decided (annotating >
 * agent-working > you-driving > disconnected > other-driving > idle).
 * `driveMode` below is the AUTHORITATIVE call — computed from real
 * `isControlling` only, mirrored into `driveModeRef` for the stable-identity
 * handlers, and is what `canDispatchInput`, the cursor style, and
 * `handlePointerDown`'s own "can I acquire the lock" branch all derive
 * from. It must stay tied to the confirmed server state ONLY — see
 * `visualDriveMode`'s doc comment (further down, where `pendingTake` is
 * folded in) for why a DISPLAY-only second call exists instead of adding an
 * optimistic flag to this one.
 */
type DriveMode = 'annotating' | 'agent-working' | 'you-driving' | 'disconnected' | 'other-driving' | 'idle'

function computeDriveMode(state: {
  annotateMode: boolean
  agentWorking: boolean
  isControlling: boolean
  connected: boolean
  controlledByOther: boolean
}): DriveMode {
  if (state.annotateMode) return 'annotating'
  if (state.agentWorking) return 'agent-working'
  if (state.isControlling) return 'you-driving'
  if (!state.connected) return 'disconnected'
  if (state.controlledByOther) return 'other-driving'
  return 'idle'
}

/** ADR-040 D6 — breathing glow border per visual state. `motion-safe:` is the
 * sole reduced-motion mechanism (a pure CSS media-query variant, no JS
 * `matchMedia` needed) — under `prefers-reduced-motion: reduce` the border
 * color still renders, just without the pulse. Module-level: purely a
 * function of `VisualState`, no need to recompute the object per render. */
const GLOW_BORDER_CLASSES: Record<VisualState, string> = {
  'agent-working': 'border-[var(--color-info)] shadow-[0_0_18px_2px_var(--color-info)] motion-safe:animate-pulse',
  'you-driving': 'border-[var(--color-accent)] shadow-[0_0_18px_2px_var(--color-accent)] motion-safe:animate-pulse',
  annotating: 'border-[var(--color-accent)] shadow-[0_0_14px_1px_var(--color-accent)]/70',
  error: 'border-[var(--color-error)]/60',
  idle: 'border-[var(--color-border)]',
}

// Local-only pill states layered on top of the wire `BrowserStatusFrame.state`
// enum: 'connecting' (never attached yet) and 'disconnected' (was attached,
// the WS transport dropped, a reconnect is in flight) both describe SPA
// connection lifecycle, not anything the backend ever sends as a status.
type LiveStatus = BrowserStatusFrame['state'] | 'connecting' | 'disconnected'

// UAT finding FE-7: the backend surfaces raw Go error strings verbatim on a
// terminal browser_status{state:'error'} frame — e.g.
// `browser input failed: browser live: navigate blocked: ... SSRF: blocked
// cloud metadata endpoint 169.254.169.254` or Go's url.Parse format
// `parse "...": invalid character " " in host name`. Map the two known,
// user-triggerable cases to plain language; anything unrecognized passes
// through unchanged rather than risk hiding real diagnostic information the
// existing doc comment above (already-controlled, take-control-disabled,
// no-manager-for-agent, live-view-disabled, malformed control) already
// treats as reasonably readable.
function friendlyBrowserStatusMessage(raw: string): string {
  // Order matters: the backend wraps EVERY navigate failure — a Go url.Parse
  // error, an ordinary DNS/network failure, AND an actual SSRF policy block
  // — identically as "... navigate blocked: ...", so classification must run
  // from MOST specific to LEAST specific. Reviewer finding F2: the old
  // `navigate blocked|SSRF` catch-all also matched the ordinary "domain
  // doesn't resolve / typo / site down" path — pkg/security/ssrf.go's
  // resolver returns e.g. "SSRF: DNS resolution failed for <host>" or
  // "SSRF: no addresses found for <host>" for that path (string-prefixed
  // "SSRF:" because the resolver lives in the SSRF-guarded dial path, not
  // because the address was actually policy-blocked) — so a mistyped or
  // unreachable URL was wrongly told it was "blocked for security reasons".
  // The invalid-URL and unreachable branches below are checked BEFORE the
  // real-block branch, and the real-block branch is narrowed to the actual
  // policy-deny messages (cloud metadata / private IP range / explicit
  // "SSRF: blocked ...") rather than any string merely containing "SSRF".
  if (/invalid character .* in host name|parse "[^"]*":/i.test(raw)) {
    console.debug('[browser] invalid URL:', raw)
    return "That doesn't look like a valid web address."
  }
  if (/DNS resolution failed|no addresses found|too many redirects|cannot extract host|could not resolve/i.test(raw)) {
    console.debug('[browser] address unreachable:', raw)
    return "That address couldn't be reached — check the URL and try again."
  }
  if (/blocked cloud metadata|blocked private IP|blocked .* range|SSRF: blocked/i.test(raw)) {
    console.debug('[browser] navigation blocked:', raw)
    return 'That address is blocked for security reasons.'
  }
  return raw
}

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

export function BrowserLiveView({
  sessionId,
  agentId,
  onPopOut,
  onClose,
  canAnnotate = false,
  isPinned = false,
  onTogglePin,
  className,
}: BrowserLiveViewProps) {
  const wsRef = useRef<BrowserLiveWsConnection | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const imgRef = useRef<HTMLImageElement | null>(null)
  const frameRef = useRef<BrowserScreencastFrame | null>(null)
  const controllingRef = useRef(false)
  // ── ADR-040 D2 implicit control model ───────────────────────────────────
  // agentWorkingRef mirrors the `agentWorking` (chat-store isStreaming for
  // this session) state into a ref so the pointer/keyboard/wheel handlers
  // below (all stable useCallbacks) always read the LATEST value without
  // needing it in their dependency arrays — same rationale as
  // frameRef/controllingRef.
  const agentWorkingRef = useRef(false)
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
  const implicitDriveRef = useRef(false)
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
  // closure — same pattern as frameRef/controllingRef above.
  const annotateDraggingRef = useRef(false)
  const selectionStartClientRef = useRef<{ x: number; y: number } | null>(null)
  const pendingAnnotationRef = useRef<PendingAnnotation | null>(null)
  // mouse_move RAF-coalescing (see handlePointerMove below): only the latest
  // pointer position per animation frame is ever sent, so the highest rate
  // this can flood the server's input rate limiter at is one frame's worth
  // of paint cadence — never the native pointermove rate (60-120Hz+, higher
  // on gaming mice/some trackpads).
  const pendingMoveRef = useRef<{ x: number; y: number; modifiers: number } | null>(null)
  const moveFlushScheduledRef = useRef(false)

  const [frame, setFrame] = useState<BrowserScreencastFrame | null>(null)
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
  const [cursorPos, setCursorPos] = useState<{ x: number; y: number } | null>(null)
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

  // ── Omnibox (ADR-039 D-A2, ADR-040 D5 — always visible) ──────────────────
  const [urlInput, setUrlInput] = useState('')

  // ── Tab strip (ADR-041 D4) — the latest known tab list + active index,
  // straight off the most recent `browser_tabs` frame. `null` until the
  // first one arrives (the strip stays unrendered — never shown empty).
  // Deliberately no optimistic local "active" override on click: the ADR
  // permits an optimistic highlight but doesn't require one, and the
  // backend re-binds the screencast + re-broadcasts `browser_tabs` on every
  // switch/close/open, so reconciling to the next frame alone keeps this
  // free of drift between what's highlighted and what's actually active.
  const [tabs, setTabs] = useState<BrowserTabsFrame['tabs'] | null>(null)
  const [activeTabIndex, setActiveTabIndex] = useState(0)

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

  const isControlling = statusState === 'controlling'
  // Unified error surface: a transport-level error always wins; otherwise
  // the latest browser_status{state:'error'} frame's message is shown —
  // gated on `statusIsError` rather than `statusState === 'error'` so this
  // still surfaces even when onStatus deliberately left statusState alone
  // (the "error while controlling" case below). This is what actually
  // renders (both before and after the first frame arrives) — see the
  // "!frame" branch and the persistent error strip below.
  const displayError = connError ?? (statusIsError ? statusMessage ?? 'The live browser session reported an error.' : null)

  // ── ADR-040 D2 — "agent working" signal ───────────────────────────────────
  // Read directly from the per-session bucket (`sessionsById[sessionId]`),
  // NOT the store's top-level `isStreaming` foreground selector — the latter
  // is derived from whichever session is currently ACTIVE in chat, which is
  // not necessarily the (sessionId, agentId) pair this panel is pinned to
  // (the panel's props are captured once at mount and never re-read from the
  // session store — see the `key={sessionId:agentId}` comment on both hosts).
  const agentWorking = useChatStore((s) => s.sessionsById[sessionId]?.isStreaming ?? false)

  // ── ADR-040 D6 — agent display name for the header chip ──────────────────
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
  const resolvedAgentName = queryClient.getQueryData<Agent[]>(['agents'])?.find((a) => a.id === agentId)?.name
  const agentDisplayName = resolvedAgentName ?? 'Agent'

  // ── ADR-040 D2 refactor — the single AUTHORITATIVE "who can actually
  // drive right now" mode (see computeDriveMode's doc comment above for the
  // priority order and the cursor/visualState inconsistency it fixes).
  // `canDispatchInput`, the cursor style, and every pointer/keyboard/wheel
  // handler's own gate derive from this ONE value — never from
  // `visualDriveMode` below, which is display-only.
  const driveMode: DriveMode = computeDriveMode({ annotateMode, agentWorking, isControlling, connected, controlledByOther })

  // ── UAT finding A8 (both testers) — a DISPLAY-only second call to the
  // exact same priority function, used ONLY to compute `visualState` (→ the
  // header chip + D6 glow border) below. A take (explicit "Take over", or
  // the implicit click-to-drive first pointerdown) only flips the real
  // `isControlling` once the server's 'controlling' browser_status ack
  // round-trips back; right after Take-over, `cancelStream` (called first)
  // often flips `agentWorking` to false well BEFORE that ack lands, so
  // `driveMode` above used to fall all the way through to 'idle' ("Click to
  // drive") for that whole async window — even though the user just
  // explicitly took the wheel. Passing `isControlling: isControlling ||
  // pendingTake` here (pendingTake: true from the instant a take is SENT
  // until it's acknowledged/rejected/abandoned/disconnected — see
  // `setPendingTake`) makes the chip/glow show "you're driving" immediately.
  //
  // Deliberately NOT folded into `driveMode` itself: an earlier version of
  // this fix did exactly that, and it broke the "a second gesture starting
  // while the first take is still unacked must not double-dispatch" guard —
  // `handlePointerDown` reads the authoritative `driveMode` to decide
  // whether it's allowed to try ACQUIRING the lock (`mode !== 'idle'`); if
  // that read already said 'you-driving' during the optimistic window, a
  // brand-new gesture skipped the `pendingTakeRef` in-flight check entirely
  // and dispatched input for a take that was never actually confirmed.
  // Keeping `driveMode` strictly tied to the confirmed `isControlling` and
  // only optimistic-izing this separate display value avoids that.
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

  // ── ADR-040 D6 — hover cursor over the frame, derived from the SAME
  // `driveMode` visualState just used. Reviewer finding: this used to be a
  // SEPARATE nested ternary that checked `isControlling` BEFORE
  // `agentWorking` — the opposite order from visualState (which checked
  // `agentWorking` first) — so during the brief async gap before the
  // auto-release effect's ack landed, the cursor showed 'none' (driving,
  // synthetic cursor visible) while the chip already showed "agent is
  // browsing" and the Take-over overlay was already up. Both now derive
  // from `driveMode`, so they can never disagree. Converted from a nested
  // ternary to if/else per CLAUDE.md.
  let cursorStyle: React.CSSProperties['cursor']
  if (driveMode === 'annotating') {
    cursorStyle = 'crosshair'
  } else if (driveMode === 'agent-working') {
    cursorStyle = 'not-allowed'
  } else if (driveMode === 'you-driving') {
    cursorStyle = 'none'
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
    frameRef.current = frame
  }, [frame])
  useEffect(() => {
    controllingRef.current = isControlling
    // Losing control (agent takes it back / released) clears the stale
    // cursor overlay rather than leaving it frozen at the last position.
    if (!isControlling) setCursorPos(null)
    // ADR-040 D2: once the server confirms this connection holds the lock,
    // any implicit/explicit take that was in flight has resolved — clear the
    // in-flight guard so a FUTURE idle→drive transition can fire again.
    if (isControlling) setPendingTake(false)
  }, [isControlling, setPendingTake])
  useEffect(() => {
    agentWorkingRef.current = agentWorking
    // A gesture-scoped implicit-drive window (see implicitDriveRef's doc
    // comment) is meaningless once the agent starts working — watch-only
    // must win immediately, not just at the next pointerup.
    if (agentWorking) implicitDriveRef.current = false
  }, [agentWorking])
  useEffect(() => {
    controlledByOtherRef.current = controlledByOther
  }, [controlledByOther])
  useEffect(() => {
    connectedRef.current = connected
  }, [connected])
  useEffect(() => {
    driveModeRef.current = driveMode
  }, [driveMode])
  // ADR-040 D2 "must-handle": if the agent starts a turn while the user is
  // mid-drive, watch-only wins — release the lock rather than letting the
  // user's live input and the agent's tool input reach the tab
  // simultaneously. (If the release races the agent's own first input frame,
  // the backend's existing serialization on the browser session — not this
  // component — is the actual correctness boundary; this is the client-side
  // half of "never let both drive at once".)
  //
  // Reviewer finding: this used to fire unconditionally and ignore
  // `sendControl`'s return value — a dead/reconnecting transport (no
  // `connectedRef` guard) or a send that silently failed on a
  // technically-open socket left a phantom stuck lock: the SERVER never
  // actually heard 'release', yet nothing here noticed. `computeDriveMode`
  // already gives `agent-working` top priority over `isControlling`, so
  // input dispatch stays correctly blocked regardless — but the server-side
  // lock would stay wrongly held, and a FUTURE `takeWheelIfNeeded()` would
  // wrongly think this connection is "already driving" (`controllingRef`
  // still true) and skip re-sending 'take'. Guard with connectivity, and on
  // a failed send force the local lock state back to released (rather than
  // leaving it silently wedged at 'controlling') and tell the user it needs
  // a retry.
  useEffect(() => {
    if (agentWorking && isControlling && connectedRef.current) {
      const released = wsRef.current?.sendControl('release')
      if (!released) {
        setStatusState('released')
        useUiStore.getState().addToast({
          message: 'Could not confirm pausing control — click Take over again if needed.',
          variant: 'error',
        })
      }
    }
  }, [agentWorking, isControlling])

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
  useEffect(() => {
    const conn = new BrowserLiveWsConnection(sessionId, agentId, {
      onScreencast: (f) => setFrame(f),
      // ADR-041 D4 — tab list + active index, broadcast on any
      // open/close/switch/title-change. This is the sole source of truth
      // the tab strip renders from (see the `tabs`/`activeTabIndex` state
      // doc comment above).
      onTabs: (f) => {
        setTabs(f.tabs)
        setActiveTabIndex(f.active_index)
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
        // FE-7: only error-state messages get the raw-Go-string treatment —
        // other states' messages (if ever present) are left alone.
        setStatusMessage(f.state === 'error' && f.message ? friendlyBrowserStatusMessage(f.message) : f.message ?? null)
        setStatusIsError(f.state === 'error')
        setStatusState((prev) => (f.state === 'error' && prev === 'controlling' ? prev : f.state))
        // FE-6: only overwrite `controlledByOther` when the server actually
        // reported the field on THIS lifecycle/error frame — a frame that
        // omits it (e.g. a tab-death/error frame) must not reset a
        // still-true "someone else is driving" back to false.
        if (f.controlled_by_other !== undefined) setControlledByOther(f.controlled_by_other)
      },
      onError: (message) => setConnError(message),
      onConnected: () => {
        setConnected(true)
        setConnError(null)
      },
      onDisconnected: () => {
        setConnected(false)
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
        implicitDriveRef.current = false
      },
    })
    wsRef.current = conn
    conn.connect()
    return () => {
      conn.detach()
      conn.close()
      wsRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, agentId])

  // ── ADR-040 D2 refactor — the single "can this event reach the remote tab"
  // gate. Every handler below (wheel/pointerMove/pointerUp/keyDown/keyUp)
  // consults this instead of re-deriving its own agentWorking/controlling
  // combination — see computeDriveMode's doc comment for why the priority
  // order matters. `implicitDriveActive` lets pointerMove/pointerUp pass a
  // caller-captured snapshot of `implicitDriveRef.current` (pointerUp reads
  // it BEFORE clearing the ref for the gesture that's ending, so it must be
  // captured, not re-read live) — wheel/keyDown/keyUp, which never
  // participate in the implicit-drive gesture window, just pass `false`.
  // handlePointerDown has its OWN bespoke branching (it's the one handler
  // that can ACQUIRE the lock, not just check it) but still reads the same
  // `driveModeRef` this reads from.
  const canDispatchInput = useCallback((implicitDriveActive: boolean) => {
    return driveModeRef.current === 'you-driving' || implicitDriveActive
  }, [])

  // ── Native (non-passive) wheel listener — React's synthetic onWheel is
  // passive by default, so preventDefault() inside a JSX handler would warn
  // and no-op. Attached once; reads live state via refs to avoid re-binding
  // on every incoming frame. ──
  useEffect(() => {
    const el = containerRef.current
    if (!el) return undefined
    function onWheel(e: WheelEvent) {
      // ADR-040 D2: watch-only never dispatches page input, wheel included;
      // annotate mode excludes driving too (canDispatchInput's driveMode
      // gives annotating top priority — closes a latent gap where a stale
      // isControlling:true during the annotate-entry release race used to
      // let a scroll through).
      if (!canDispatchInput(false) || !frameRef.current) return
      e.preventDefault()
      const rect = el!.getBoundingClientRect()
      const device = mapClientToDevice(
        e.clientX,
        e.clientY,
        rect,
        frameRef.current.width,
        frameRef.current.height,
        frameRef.current.page_scale,
      )
      if (!device) return
      wsRef.current?.sendInput({
        kind: 'wheel',
        x: device.x,
        y: device.y,
        delta_x: e.deltaX,
        delta_y: e.deltaY,
        modifiers: computeModifiers(e),
      })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [frame !== null, canDispatchInput])

  // ── mouse_move RAF coalescing ────────────────────────────────────────────
  // Native pointermove fires far faster than the backend's input rate limiter
  // (50 events/sec) can absorb — a 120Hz+ mouse/trackpad would flood it,
  // causing silent server-side drops and a janky remote cursor. Coalesce to
  // "at most one send per animation frame": every handlePointerMove call
  // overwrites pendingMoveRef with the latest position; a single scheduled
  // flush (idempotent — moveFlushScheduledRef guards re-scheduling) drains
  // whatever is pending when the frame/timer fires. Mirrors the rAF-or-
  // setTimeout(0) fallback ws.ts already uses for inbound batching (rAF is
  // unavailable in jsdom/node and a hidden tab never fires it).
  const flushPendingMove = useCallback(() => {
    moveFlushScheduledRef.current = false
    const pending = pendingMoveRef.current
    if (!pending) return
    pendingMoveRef.current = null
    // Reviewer finding (queued-move leak): re-validate the drive gate at
    // FLUSH time, not just at schedule time — the agent can start working
    // (or the connection can drop, or annotate mode can engage) in the gap
    // between the pointermove that scheduled this flush and the animation
    // frame/timer actually firing. Without this, a queued position captured
    // while still driving could leak into the tab a frame later, after
    // watch-only has already taken over.
    if (!canDispatchInput(implicitDriveRef.current)) return
    wsRef.current?.sendInput({ kind: 'mouse_move', x: pending.x, y: pending.y, modifiers: pending.modifiers })
  }, [canDispatchInput])

  const scheduleMoveFlush = useCallback(() => {
    if (moveFlushScheduledRef.current) return
    moveFlushScheduledRef.current = true
    if (typeof requestAnimationFrame !== 'undefined' && document.visibilityState !== 'hidden') {
      requestAnimationFrame(flushPendingMove)
    } else {
      setTimeout(flushPendingMove, 0)
    }
  }, [flushPendingMove])

  // Cancel any in-flight coalesced move on unmount — nothing to flush once
  // the socket is about to be closed/detached by the WS lifecycle effect.
  useEffect(() => {
    return () => {
      moveFlushScheduledRef.current = false
      pendingMoveRef.current = null
    }
  }, [])

  // ── ADR-040 D2 — the single "acquire the wheel" entry point ──────────────
  // Shared by click-to-drive (implicit, on the first pointerdown while idle),
  // the omnibox submit handler (D5 — submitting while not driving takes the
  // wheel first), and the explicit "Take over" button (D2 — shown only while
  // watch-only). All refs (plus the stable `sessionId` prop), so this stays
  // safe to reference from any other stable useCallback below.
  const takeWheelIfNeeded = useCallback(() => {
    // Defense in depth: the two OTHER callers (omnibox submit, "Take over")
    // already gate their button on `disabled={!connected}`, but a dead
    // transport must never attempt a take regardless of caller — mirrors the
    // pre-ADR-040 "no silent send attempts while disconnected" coverage.
    if (!connectedRef.current) return
    if (controllingRef.current) return // already driving — nothing to acquire
    if (pendingTakeRef.current) return // a take is already in flight — never double-fire
    // UAT finding FE-6, preserved: don't race a DIFFERENT connection of this
    // same session that already holds the lock (the pop-out and the docked
    // panel can both be open on the same agent at once).
    if (controlledByOtherRef.current) return
    if (agentWorkingRef.current) {
      // ADR-040 D2 "Take over": pause the agent FIRST, via the exact same
      // chat-store action the chat Stop button calls — reusing it here
      // rather than inventing a new backend path is the point of this ADR.
      // Reviewer finding (CRITICAL): `cancelStream` now takes an explicit
      // session id and defaults to whichever session is currently ACTIVE in
      // chat when omitted. This panel's pinned `sessionId` is not
      // necessarily that active session (see the `agentWorking` selector's
      // own doc comment above, which reads `sessionsById[sessionId]`
      // directly for exactly this reason) — an unscoped call would pause
      // whatever chat happens to be foregrounded instead of the turn THIS
      // panel is actually watching, and since THIS session's isStreaming
      // never actually goes false, the auto-release effect would
      // immediately hand the lock right back (acquire → instant auto-release
      // loop). Always pass this panel's own pinned session id.
      useChatStore.getState().cancelStream(sessionId)
    }
    // UAT finding A8: flip the reactive chip/glow to "you're driving"
    // OPTIMISTICALLY, the same instant the take is sent — see
    // `visualDriveMode`'s doc comment for why waiting on the server's ack
    // (isControlling) left a visible "Click to drive" flash, and why this is
    // display-only (driveMode/canDispatchInput still wait for the real ack).
    setPendingTake(true)
    wsRef.current?.sendControl('take')
  }, [sessionId, setPendingTake])

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
    implicitDriveRef.current = false
    setPendingTake(false)
    setAnnotateMode(true)
  }, [annotateMode, isControlling, handleCancelAnnotation, setPendingTake])

  // Crops the CURRENTLY-RENDERED screencast frame's <img> to a PNG File
  // (mirrors the canvas pattern in media-actions.ts's fetchImagePng). Reads
  // the live <img> at call time (not a stale snapshot) so the crop always
  // reflects exactly what the user was looking at when they finished the
  // drag/click.
  const cropFrameToFile = useCallback(
    async (rect: FrameCropRect, frameWidth: number, frameHeight: number): Promise<File | null> => {
      const img = imgRef.current
      if (!img || !img.complete || img.naturalWidth === 0) return null
      // UAT finding (blank crop): `rect` is in frame-METADATA space
      // (frame.width/height = CDP Metadata.DeviceWidth/DeviceHeight — the full
      // viewport device-pixel size), but the screencast JPEG is captured with
      // BOTH WithMaxWidth(screencastMaxWidth=1280) AND
      // WithMaxHeight(screencastMaxHeight=720) (live.go), so the decoded
      // <img>'s natural pixels (img.naturalWidth/Height) are DOWNSCALED
      // whenever the device size exceeds the screencast max bound on EITHER
      // axis (a narrow-tall/portrait viewport hits the same downscale via the
      // height axis). Using `rect` directly as the drawImage SOURCE rect then
      // reads an out-of-bounds/misaligned region of the smaller bitmap →
      // drawImage draws nothing → a transparent (blank white/black) crop.
      // Scale the source rect from frame space into the img's actual
      // natural-pixel space; the destination canvas is sized to that native
      // region so we capture at the JPEG's true resolution.
      const src = scaleCropToImagePixels(rect, frameWidth, frameHeight, img.naturalWidth, img.naturalHeight)
      const { x: sx, y: sy, width: sw, height: sh } = src
      // Reviewer finding: drawImage/getContext can throw synchronously (e.g.
      // IndexSizeError on a degenerate zero-width/height rect, or a tainted
      // canvas) — this function is awaited from finalizeSelection, which is
      // itself invoked fire-and-forget (`void finalizeSelection(...)` from the
      // pointerup handler), so an uncaught rejection here would surface as an
      // unhandled promise rejection with no toast and a frozen selection box.
      // Returning null on ANY failure routes through finalizeSelection's
      // existing `if (!file) return fail()` path instead.
      try {
        const canvas = document.createElement('canvas')
        canvas.width = Math.round(sw)
        canvas.height = Math.round(sh)
        const ctx = canvas.getContext('2d')
        if (!ctx) return null
        ctx.drawImage(img, sx, sy, sw, sh, 0, 0, canvas.width, canvas.height)
        const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
        if (!blob) return null
        return new File([blob], 'annotation.png', { type: 'image/png' })
      } catch {
        return null
      }
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
      const frame = frameRef.current
      if (!frame) return fail()
      const startPx = mapClientToFramePixels(startClient.x, startClient.y, containerRect, frame.width, frame.height)
      const endPx = mapClientToFramePixels(endClient.x, endClient.y, containerRect, frame.width, frame.height)
      if (!startPx || !endPx) return fail()
      const cropRect = computeCropRect(startPx, endPx, frame.width, frame.height)
      if (!cropRect) return fail()

      const file = await cropFrameToFile(cropRect, frame.width, frame.height)
      if (!file) return fail()
      const center = framePixelToDeviceCoords(
        cropRect.x + cropRect.width / 2,
        cropRect.y + cropRect.height / 2,
        frame.page_scale,
      )
      setPendingAnnotation({ file, previewUrl: URL.createObjectURL(file), point: center })
    },
    [cropFrameToFile, resetSelection],
  )

  // ── Omnibox (ADR-039 D-A2, ADR-040 D5 — always visible) ───────────────────
  // Submitting is itself a driving action: if the viewer doesn't currently
  // hold the lock, `takeWheelIfNeeded` acquires it first (pausing the agent
  // first, via cancelStream, if it was mid-turn) — exactly the "Take over
  // first, then navigate" behaviour D5 specifies — then the navigate input
  // is dispatched on the same connection right after.
  const handleOmniboxSubmit = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault()
      const resolved = resolveOmniboxInput(urlInput)
      if (!resolved) return
      // Reviewer finding (CRITICAL): submitting is itself a driving action
      // (D5), so it must be gated through the SAME `driveMode` the
      // pointer/keyboard handlers consult, not a hand-rolled duplicate of
      // takeWheelIfNeeded's guards (the previous inline
      // `!controllingRef.current && (!connectedRef.current ||
      // controlledByOtherRef.current)` expression never accounted for
      // annotate mode at all, so submitting while annotating would still
      // implicitly take the wheel and navigate — the same "annotate excludes
      // driving" gap the pointer handlers were already closed against).
      // Blocks exactly the cases takeWheelIfNeeded itself would refuse to
      // acquire for (disconnected / a different connection already driving)
      // plus annotate mode; 'agent-working' and 'idle' are both legitimate
      // starting points for a drive acquisition.
      const mode = driveModeRef.current
      if (mode !== 'you-driving' && mode !== 'agent-working' && mode !== 'idle') return
      // UAT finding: a prior blocked-navigate error banner persisted through a
      // subsequent SUCCESSFUL navigate (a successful navigate emits only
      // screencast frames, never a browser_status, so nothing cleared it).
      // Clear it optimistically on each new submit — if THIS navigate is also
      // rejected, a fresh browser_status(error) re-raises the banner.
      setStatusMessage(null)
      setStatusIsError(false)
      // When agent-working, takeWheelIfNeeded pauses THIS panel's session
      // (cancelStream(sessionId)) then sends control:take; when idle it just
      // sends control:take; when already you-driving it's a no-op. Either
      // way the navigate below rides the SAME connection right after —
      // same-connection WS ordering (as the click-to-drive path relies on)
      // guarantees the server processes control:take before this
      // browser_input{navigate}, so the navigate is dispatched as part of
      // the SAME drive acquisition, never ahead of it.
      takeWheelIfNeeded()
      wsRef.current?.sendInput({ kind: 'navigate', url: resolved })
      setUrlInput(resolved)
    },
    [urlInput, takeWheelIfNeeded],
  )

  // ── Tab strip actions (ADR-041 D4) — switching/opening/closing a tab is a
  // viewer action, like the omnibox, independent of the D2 drive-lock model:
  // it never itself acquires/releases control. Each just sends the frame;
  // the resulting highlight/list update comes from the next `browser_tabs`
  // broadcast (see the `tabs` state doc comment), not from any local write
  // here.
  const handleTabSwitch = useCallback((index: number) => {
    wsRef.current?.sendTabAction('switch', index)
  }, [])

  const handleTabClose = useCallback((index: number, e: React.MouseEvent) => {
    // Stop the click from bubbling to the tab chip's own onClick (which
    // would also fire a redundant 'switch' for the tab being closed).
    e.stopPropagation()
    wsRef.current?.sendTabAction('close', index)
  }, [])

  const handleTabOpen = useCallback(() => {
    wsRef.current?.sendTabAction('open')
  }, [])

  const handlePointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (annotateMode) {
      if (!annotateDraggingRef.current || !containerRef.current) return
      const rect = containerRef.current.getBoundingClientRect()
      setSelectionCurrent({ x: e.clientX - rect.left, y: e.clientY - rect.top })
      return
    }
    // ADR-040 D2: dispatch while EITHER actually driving OR mid-way through
    // the same gesture that just implicitly acquired the lock (the server's
    // ack may not have landed yet — see implicitDriveRef's doc comment).
    // canDispatchInput folds in the agent-working / annotate-mode / drive
    // gates that used to be re-derived here separately.
    if (!canDispatchInput(implicitDriveRef.current) || !frameRef.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    // Local cursor overlay updates immediately every event — only the
    // network send is throttled, so the synthetic cursor still tracks the
    // pointer at full native resolution.
    setCursorPos({ x: e.clientX - rect.left, y: e.clientY - rect.top })
    const device = mapClientToDevice(
      e.clientX,
      e.clientY,
      rect,
      frameRef.current.width,
      frameRef.current.height,
      frameRef.current.page_scale,
    )
    if (!device) return
    pendingMoveRef.current = { x: device.x, y: device.y, modifiers: computeModifiers(e) }
    scheduleMoveFlush()
  }, [scheduleMoveFlush, annotateMode, canDispatchInput])

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
      if (!frameRef.current || !containerRef.current || pendingAnnotationRef.current) return
      focusAndCapturePointer(e)
      const rect = containerRef.current.getBoundingClientRect()
      const point = { x: e.clientX - rect.left, y: e.clientY - rect.top }
      selectionStartClientRef.current = { x: e.clientX, y: e.clientY }
      annotateDraggingRef.current = true
      setSelectionStart(point)
      setSelectionCurrent(point)
      return
    }
    // ADR-040 D2: watch-only — a click on the frame while the agent is
    // working must never dispatch input NOR implicitly take the wheel; the
    // explicit "Take over" button is the only path in that state. This is
    // the one handler that can ACQUIRE the lock (not just check it), so it
    // reads `driveModeRef` directly rather than the shared canDispatchInput
    // boolean gate.
    const mode = driveModeRef.current
    if (mode === 'agent-working') return
    if (mode !== 'you-driving') {
      // A dead/reconnecting transport, or a DIFFERENT connection of this
      // same session already holding the lock (FE-6), must never attempt a
      // take NOR dispatch input — matches the pre-ADR-040 "no silent send
      // attempts" coverage (takeWheelIfNeeded also re-guards both cases, but
      // bail out here too so the dispatch below never even runs). Reviewer
      // finding (pending-take residual): a take already in flight from a
      // DIFFERENT gesture (pendingTakeRef) must not let THIS gesture start
      // dispatching either — we don't yet know whether that pending take
      // will actually land, and takeWheelIfNeeded would silently no-op the
      // (redundant) take anyway, leaving this gesture's input to go out
      // regardless if it weren't guarded here too.
      if (mode !== 'idle' || pendingTakeRef.current) return
      // Idle + not driving: the first pointer interaction implicitly takes
      // the wheel (ADR-040 D2) — acquire the lock, then dispatch this SAME
      // input immediately. Same-connection WS message ordering guarantees
      // the server processes the `browser_control{take}` frame before this
      // `browser_input{mouse_down}` frame, so there's no need to wait for
      // the round-trip ack before dispatching.
      takeWheelIfNeeded()
      implicitDriveRef.current = true
    }
    if (!frameRef.current || !containerRef.current) return
    focusAndCapturePointer(e)
    const rect = containerRef.current.getBoundingClientRect()
    const device = mapClientToDevice(
      e.clientX,
      e.clientY,
      rect,
      frameRef.current.width,
      frameRef.current.height,
      frameRef.current.page_scale,
    )
    if (!device) return
    wsRef.current?.sendInput({
      kind: 'mouse_down',
      x: device.x,
      y: device.y,
      button: mapMouseButton(e.button),
      modifiers: computeModifiers(e),
    })
  }, [annotateMode, focusAndCapturePointer, takeWheelIfNeeded])

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
    const wasImplicitDrive = implicitDriveRef.current
    implicitDriveRef.current = false
    if (!canDispatchInput(wasImplicitDrive) || !frameRef.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    const device = mapClientToDevice(
      e.clientX,
      e.clientY,
      rect,
      frameRef.current.width,
      frameRef.current.height,
      frameRef.current.page_scale,
    )
    if (!device) return
    wsRef.current?.sendInput({
      kind: 'mouse_up',
      x: device.x,
      y: device.y,
      button: mapMouseButton(e.button),
      modifiers: computeModifiers(e),
    })
  }, [annotateMode, finalizeSelection, canDispatchInput])

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

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    // ADR-040 D2: watch-only never dispatches page input, keyboard included —
    // canDispatchInput's driveMode gives agent-working (and annotating) top
    // priority over a stale/in-flight controllingRef, closing the same
    // "async release gap" this used to only defend against for agent-working
    // specifically.
    if (!canDispatchInput(false)) return
    // Escape is deliberately NOT forwarded and NOT preventDefault()'d: Radix's
    // Dialog/Sheet listens for it via a capture-phase document listener
    // (fires before this bubble-phase handler ever runs — no event-handler
    // trick on this element can outrun it), so when hosted in the Sheet
    // overlay, Escape always closes the panel first regardless. Treating
    // Escape as "exit local control" rather than fighting that is also the
    // conventional behaviour for remote-control/remote-desktop widgets.
    if (e.key === 'Escape') return
    e.preventDefault()
    const modifiers = computeModifiers(e)
    if (isPrintableKey(e)) {
      wsRef.current?.sendInput({ kind: 'text', text: e.key, modifiers })
    } else {
      // key_code (DOM KeyboardEvent.keyCode) is REQUIRED for CDP to actually
      // perform editing/navigation keys (Backspace, Delete, Enter, Tab,
      // arrows) and modifier shortcuts (Ctrl+A/C/V) — key/code alone deliver
      // the event but don't delete/submit/move/select. See ADR-039.
      wsRef.current?.sendInput({ kind: 'key_down', key: e.key, code: e.code, key_code: e.keyCode, modifiers })
    }
  }, [canDispatchInput])

  const handleKeyUp = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!canDispatchInput(false)) return
    if (e.key === 'Escape') return
    e.preventDefault()
    // 'text' input is a one-shot insert (no matching key_up — mirrors
    // Input.insertText on the backend, which has no down/up phase).
    if (!isPrintableKey(e)) {
      wsRef.current?.sendInput({ kind: 'key_up', key: e.key, code: e.code, key_code: e.keyCode, modifiers: computeModifiers(e) })
    }
  }, [canDispatchInput])

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
      return { label: 'Someone else is driving', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
    }
    return { label: 'Click to drive', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
  })()

  // ADR-040 D2/D6 — "Take over" affordance's aria-label/title (reviewer
  // finding: this exact ternary was duplicated across both attributes — the
  // F5 anti-pattern reintroduced). One computation, used by both.
  const takeOverLabel = controlledByOther
    ? 'Someone else is currently driving'
    : `Take over — pause ${agentDisplayName} and take control`

  return (
    <div className={cn('flex h-full min-h-0 flex-col bg-[var(--color-primary)]', className)}>
      {/* Header. pr-14 (not just px-4) reserves room past the row's own buttons:
          when hosted in the Sheet overlay (BrowserLivePanel), Radix renders its
          own built-in close (absolute right-2 top-2, h-11 w-11) on top of
          whatever sits underneath — without this reserved gap the rightmost
          header button here would sit directly under it. */}
      <div
        className="flex shrink-0 items-center gap-2 overflow-x-auto border-b border-[var(--color-border)] pl-4 pr-14 py-3"
        style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
      >
        <Monitor size={16} weight="duotone" className="shrink-0 text-[var(--color-accent)]" />
        <h2 className="shrink-0 font-headline text-sm font-semibold text-[var(--color-secondary)]">Live Browser</h2>
        {/* ADR-040 D6 — header chip replaces the old take/release-control-derived
            corner status pill: words + icon + a pulsing "live" dot back up the
            colour (never colour alone) for who's currently driving. */}
        <span
          data-testid="browser-live-status-chip"
          className={cn('flex shrink-0 items-center gap-1.5 rounded px-2 py-0.5 text-[11px] font-medium whitespace-nowrap', driveChip.textClass)}
        >
          <span
            aria-hidden="true"
            className={cn('h-1.5 w-1.5 shrink-0 rounded-full', driveChip.dotClass, driveChip.pulse && 'motion-safe:animate-pulse')}
          />
          <driveChip.Icon size={12} weight={driveChip.pulse ? 'fill' : 'regular'} />
          {driveChip.label}
        </span>
        <div className="flex-1 min-w-2" />
        {/* ADR-040 D1 — minimal header: Pen (annotate) · Pin · Pop out · Close.
            The old Take/Release-control toggle and Hand-to-agent button are
            gone entirely — see the file header comment for the replacement
            implicit-control model. */}
        {/* Annotate toggle (ADR-039 D-B1/B2, ADR-040 D3) — a third interaction
            mode, mutually exclusive with driving: entering it releases control
            (handleToggleAnnotate). Hidden entirely when !canAnnotate (FE-4) —
            see the prop's doc comment. */}
        {canAnnotate && (
          <button
            type="button"
            onClick={handleToggleAnnotate}
            disabled={!connected}
            aria-label={annotateMode ? 'Exit annotate mode' : 'Annotate a region'}
            title={annotateMode ? 'Exit annotate mode' : 'Drag a region (or click a spot) to comment on it'}
            className={cn(
              'flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded px-2.5 py-1.5 text-xs font-medium transition-colors',
              'disabled:cursor-not-allowed disabled:opacity-40',
              annotateMode
                ? 'bg-[var(--color-accent)] text-[var(--color-primary)] hover:opacity-90'
                : 'border border-[var(--color-border)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
            )}
          >
            <ChatCircleDots size={13} />
            {annotateMode ? 'Exit annotate' : 'Annotate'}
          </button>
        )}
        {/* Pin (ADR-040 D1/D4) — hidden entirely when the host doesn't supply
            onTogglePin (e.g. the fullscreen pop-out), exactly like Pop
            out/Close below. Layout itself is entirely the panel owner's job;
            this view only renders the toggle and reflects `isPinned`. */}
        {onTogglePin && (
          <button
            type="button"
            onClick={onTogglePin}
            aria-label={isPinned ? 'Unpin live browser panel' : 'Pin live browser panel'}
            aria-pressed={isPinned}
            title={isPinned ? 'Unpin — return to overlay' : 'Pin — dock beside chat'}
            className={cn(
              'shrink-0 rounded p-1.5 transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]',
              isPinned
                ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent)] hover:bg-[var(--color-accent)]/25'
                : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
            )}
          >
            {isPinned ? <PushPinSlash size={15} weight="fill" /> : <PushPin size={15} />}
          </button>
        )}
        {/* Pop out — de-emphasised utility affordance (ADR-040 D1). Gated on
            `onPopOut` presence ONLY (reviewer finding: dropped the internal
            `&& !isPinned`) — a docked panel popping into its own window is a
            layout no-op the panel owner already has to reconcile, so THAT
            decision (whether to even offer pop-out while pinned) belongs
            entirely to whether the host passes `onPopOut` at all, exactly
            like onClose/onTogglePin above. */}
        {onPopOut && (
          <button
            type="button"
            onClick={onPopOut}
            aria-label="Pop out"
            title="Pop out into its own window"
            className="shrink-0 rounded p-1.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
          >
            <ArrowSquareOut size={15} />
          </button>
        )}
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label="Close live browser panel"
            title="Close"
            className="shrink-0 rounded p-1.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
          >
            <X size={15} />
          </button>
        )}
      </div>

      {/* UAT finding (both testers): neither could discover that sending a
          chat message hands control back to the agent — the file header
          comment explains the mechanism ("handing back is just sending a
          chat message") but nothing on screen ever hinted at it. A single
          small, muted line — shown ONLY while the user actually holds the
          wheel (`visualState === 'you-driving'`, which now also covers the
          UAT-A8 optimistic pendingTake window right after Take-over) — right
          under the header where the "You're driving" chip already lives. */}
      {visualState === 'you-driving' && (
        <p
          data-testid="browser-live-handback-hint"
          className="shrink-0 px-4 pb-1.5 text-[11px] text-[var(--color-muted)]"
        >
          Send a message to hand back to {resolvedAgentName ?? 'the agent'}
        </p>
      )}

      {/* Omnibox (ADR-039 D-A2, ADR-040 D5) — ALWAYS visible now, not gated on
          driving/annotate state. The server's ValidateURL SSRF gate (run on
          every `navigate` input frame) is the real authority on what may
          actually be navigated to; any rejection surfaces via the existing
          browser_status(error) strip below. Submitting while not currently
          driving takes the wheel first (see handleOmniboxSubmit). */}
      <form
        onSubmit={handleOmniboxSubmit}
        className="flex shrink-0 items-center gap-2 border-b border-[var(--color-border)] px-4 py-2"
      >
        <Input
          type="text"
          value={urlInput}
          onChange={(e) => setUrlInput(e.target.value)}
          placeholder="Search or enter a URL…"
          aria-label="Address bar"
          className="h-8 flex-1 text-xs"
        />
        <button
          type="submit"
          disabled={urlInput.trim().length === 0 || !connected || (controlledByOther && !isControlling)}
          className="shrink-0 rounded-md bg-[var(--color-accent)] px-3 h-8 text-xs font-medium text-[var(--color-primary)] transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
        >
          Go
        </button>
      </form>

      {/* Tab strip (ADR-041 D4) — open tabs: favicon-or-globe + truncated
          title/hostname + active highlight + close ✕, plus a trailing ＋ to
          open a new tab. Rendered whenever the tab list is known (the
          backend always keeps at least one tab — see `tabs`' state doc
          comment — so "known" and "non-empty" coincide in practice; the
          `tabs.length > 0` check is just defense in depth against an
          ill-formed frame). Sits below the always-visible omnibox and above
          the screencast frame, matching the ADR-040 header/omnibox stack. */}
      {tabs && tabs.length > 0 && (
        <div
          role="tablist"
          aria-label="Open tabs"
          data-testid="browser-tab-strip"
          className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-[var(--color-border)] px-2 py-1.5"
          style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
        >
          {tabs.map((tab) => {
            const active = tab.index === activeTabIndex
            const label = tabLabel(tab)
            return (
              <div
                key={tab.index}
                role="tab"
                aria-selected={active}
                tabIndex={0}
                data-testid={`browser-tab-${tab.index}`}
                onClick={() => handleTabSwitch(tab.index)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    handleTabSwitch(tab.index)
                  }
                }}
                title={tab.title || tab.url || 'New tab'}
                className={cn(
                  'flex shrink-0 max-w-[180px] cursor-pointer items-center gap-1.5 rounded-t-md border-b-2 px-2.5 py-1 text-xs transition-colors',
                  // Active tab: Forge-Gold underline + full opacity + a
                  // heavier label weight — colour is never the only signal
                  // (WCAG). Inactive: dimmed, transparent underline.
                  active
                    ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)] text-[var(--color-secondary)] opacity-100'
                    : 'border-transparent text-[var(--color-muted)] opacity-70 hover:bg-[var(--color-surface-1)] hover:opacity-100',
                )}
              >
                <Globe size={12} weight={active ? 'fill' : 'regular'} className="shrink-0" />
                <span className={cn('min-w-0 flex-1 truncate', active && 'font-medium')}>{label}</span>
                <button
                  type="button"
                  onClick={(e) => handleTabClose(tab.index, e)}
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
          <button
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
      )}

      {/* Body */}
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-black p-2">
        {/* ADR-040 D6 — breathing glow border: a property of the WHOLE
            surface so the driving mode can't be missed (change-blindness
            fix). A separate absolutely-positioned overlay (rather than
            applying the border/animation to the body div itself) so the
            pulse never touches the actual frame `<img>`'s own opacity.
            `motion-safe:animate-pulse` (baked into GLOW_BORDER_CLASSES) is
            the sole reduced-motion mechanism — a pure CSS media-query
            variant, so under `prefers-reduced-motion: reduce` the border
            colour still renders, just without the pulse; no JS
            `matchMedia` needed. */}
        <div
          aria-hidden="true"
          data-testid="browser-live-glow"
          data-visual-state={visualState}
          className={cn('pointer-events-none absolute inset-0 z-30 border-2 transition-colors duration-500', GLOW_BORDER_CLASSES[visualState])}
        />
        {!frame && (
          <div className="flex flex-col items-center gap-2 p-6 text-center text-sm text-[var(--color-muted)]">
            {displayError ? (
              <>
                <WarningCircle size={22} className="text-[var(--color-error)]" />
                <p className="text-[var(--color-error)]">{displayError}</p>
              </>
            ) : (
              <>
                <SpinnerGap size={20} className="animate-spin" />
                <p>{connected ? 'Waiting for the first frame…' : 'Connecting to the live browser…'}</p>
              </>
            )}
          </div>
        )}

        {frame && (
          <div
            ref={containerRef}
            tabIndex={0}
            role="application"
            aria-label="Live browser view"
            data-testid="browser-live-frame"
            className="relative inline-block max-h-full max-w-full select-none outline-none"
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
            onDragStart={(e) => e.preventDefault()}
          >
            <img
              ref={imgRef}
              src={`data:image/jpeg;base64,${frame.data}`}
              alt="Live browser session"
              draggable={false}
              className="block h-auto max-h-full w-auto max-w-full select-none"
            />
            {isControlling && cursorPos && (
              <div
                data-testid="synthetic-cursor"
                className="pointer-events-none absolute z-10"
                style={{ left: cursorPos.x, top: cursorPos.y, transform: 'translate(-2px, -2px)' }}
              >
                <Cursor size={20} weight="fill" className="text-[var(--color-accent)] drop-shadow" />
              </div>
            )}
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
            watch-only (agent working, user doesn't hold the lock). Adjacent
            to the frame (not a header button — D1's header stays limited to
            Close/Pin/Pen/Pop-out). Rendered whenever agent-working, even
            before a frame has arrived, so the user can pause the agent
            immediately rather than waiting on the first screencast frame. */}
        {visualState === 'agent-working' && (
          <div className="pointer-events-none absolute inset-x-0 top-3 z-20 flex justify-center">
            <button
              type="button"
              onClick={takeWheelIfNeeded}
              // FE-6, preserved: disabled while a DIFFERENT connection of
              // this same session already holds the lock — clicking would
              // just silently no-op (takeWheelIfNeeded's own guard).
              disabled={!connected || controlledByOther}
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
                    // Cancel the pending annotation on Escape rather than
                    // letting it bubble to Radix's Sheet (which would close
                    // the whole panel and discard the drafted comment).
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
                  <button
                    type="button"
                    onClick={handleCancelAnnotation}
                    disabled={annotateSubmitting}
                    className="rounded px-2.5 py-1 text-xs text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    Cancel
                  </button>
                  <button
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

      {/* Persistent error strip — shown alongside the frame once one has rendered (the
          empty-state branch above already surfaces displayError before any frame arrives).
          Covers both transport errors (connError) and a terminal browser_status{state:'error'}
          (already-controlled, take-control-disabled, no-manager-for-agent, live-view-disabled,
          malformed control, …) so a semantic status error is just as visible as a transport one. */}
      {frame && displayError && (
        <div role="alert" className="shrink-0 border-t border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-2 text-xs text-[var(--color-error)]">
          {displayError}
        </div>
      )}
    </div>
  )
}
