// Presentation-state helpers for BrowserLiveView: drive/visual buckets,
// chrome constants, and the tab-label / first-frame / viewport pacers.
// Extracted so the live-view component can shrink without changing behaviour.

import {
  ChatCircleDots,
  Cursor,
  Eye,
  Robot,
  SpinnerGap,
  WarningCircle,
} from '@phosphor-icons/react'
import { DEFAULT_FIRST_ANSWER_TIMEOUT_MS } from '@/lib/browserWebRTC'
import type { BrowserStatusFrame, BrowserTabsFrame } from '@/lib/api/generated/asyncapi-types'

/** ADR-040 D2/D6 — the three (+ one) mutually-exclusive visual/control states. */
export type VisualState = 'agent-working' | 'you-driving' | 'annotating' | 'error' | 'idle'

/** Presentation state; only annotation and connectivity gate input. */
export type DriveMode = 'annotating' | 'agent-working' | 'you-driving' | 'disconnected' | 'other-driving' | 'idle'

export function computeDriveMode(state: {
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
export const TOOLBAR_ICON_BTN =
  'shrink-0 flex h-8 w-8 items-center justify-center rounded-md transition-colors ' +
  'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] ' +
  'disabled:cursor-not-allowed disabled:opacity-40 ' +
  'pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]'

// Local-only pill states layered on top of the wire `BrowserStatusFrame.state`
// enum: 'connecting' (never attached yet) and 'disconnected' (was attached,
// the WS transport dropped, a reconnect is in flight) both describe SPA
// connection lifecycle, not anything the backend ever sends as a status.
export type LiveStatus = BrowserStatusFrame['state'] | 'connecting' | 'disconnected'

export type BrowserTabStripState = { tabs: BrowserTabsFrame['tabs']; activeIndex: number }

export type DriveChip = {
  label: string
  Icon: typeof Robot
  textClass: string
  dotClass: string
  pulse: boolean
}

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
export const FIRST_FRAME_TIMEOUT_MS = DEFAULT_FIRST_ANSWER_TIMEOUT_MS + 15_000

// VIEWPORT_SETTLE_MS is how long a new panel size must hold still before it is
// committed to the server. Each commit REBUILDS the capture stream (tabCapture
// constraints are pinned per stream), so an intermediate size captured
// mid-drag or mid-animation costs a visible stall for a geometry that is
// already obsolete. Short enough to feel immediate after a deliberate resize,
// long enough to swallow an animation's intermediate frames.
export const VIEWPORT_SETTLE_MS = 250

// MOVE_FLUSH_MS paces coalesced pointer-move sends. Chosen to sit under the
// server's maxInputEventsPerSecond (50/s, pkg/tools/browser/live.go) with
// headroom for the down/up/wheel events that share that budget: at ~16ms a
// sustained drag alone would ride the cap and start losing events to the
// limiter. Not requestAnimationFrame — see scheduleInputFlush.
export const MOVE_FLUSH_MS = 25

// Defer capture resizing only while an editable field coincides with a
// keyboard-sized visual viewport occlusion. Desktop focus alone is harmless.
export function textFieldHasFocus(frameEl: Element | null): boolean {
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
export const BLANK_TAB_URL = 'about:blank'

/**
 * ADR-041 D4 — a tab's display label: prefer `title`, fall back to the
 * hostname parsed from `url`, fall back to "New tab". The wire type carries
 * no favicon URL (BrowserTabsFrame.tabs[] has no such field) — the strip
 * uses a plain Phosphor globe glyph per tab instead of attempting to fetch
 * one, so there is no missing-favicon broken-image state to handle.
 */
export function tabLabel(tab: BrowserTabsFrame['tabs'][number]): string {
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

// ── ADR-040 D6 — header chip config (icon + text label + colour), derived
// from `visualState`. Words + icon back up the colour for accessibility
// (never colour alone). The 'idle' bucket further distinguishes connection
// lifecycle (connecting/reconnecting) from a genuinely idle, ready-to-drive
// frame — the old corner pill's connecting/disconnected states still need
// SOME visible home now that the pill itself is gone.
export function resolveDriveChip(state: {
  visualState: VisualState
  visualDriveMode: DriveMode
  agentDisplayName: string
  statusState: LiveStatus
}): DriveChip {
  if (state.visualState === 'agent-working') {
    return { label: `${state.agentDisplayName} is browsing…`, Icon: Robot, textClass: 'text-[var(--color-info)]', dotClass: 'bg-[var(--color-info)]', pulse: true }
  }
  if (state.visualState === 'you-driving') {
    return { label: "You're driving", Icon: Cursor, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: true }
  }
  if (state.visualState === 'annotating') {
    return { label: "You're annotating", Icon: ChatCircleDots, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: false }
  }
  if (state.visualState === 'error') {
    return { label: 'Error', Icon: WarningCircle, textClass: 'text-[var(--color-error)]', dotClass: 'bg-[var(--color-error)]', pulse: false }
  }
  // 'idle' visualState — visualDriveMode further distinguishes
  // disconnected/other-driving/genuinely-idle, reading the SAME display
  // source of truth `visualState` itself derives from, instead of
  // re-deriving `!connected`/`controlledByOther` here too.
  if (state.visualDriveMode === 'disconnected') {
    return {
      label: state.statusState === 'disconnected' ? 'Reconnecting…' : 'Connecting…',
      Icon: SpinnerGap,
      textClass: 'text-[var(--color-muted)]',
      dotClass: 'bg-[var(--color-muted)]',
      pulse: false,
    }
  }
  if (state.visualDriveMode === 'other-driving') {
    // Informational, NOT a lock-out. Control is shared — this viewer's mouse,
    // keyboard and omnibox all still work while someone else is also active
    // (operator directive, 2026-08-03). The old label read "Someone else is
    // driving", which told the user their input would be ignored — and it
    // was, because the client and server both gated on the lock. Both gates
    // are gone; the chip now just says who else is here.
    return { label: 'Also viewing', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
  }
  return { label: 'Click to drive', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
}
