// Row B of the live-browser chrome: navigation, omnibox, identity, mode toggles.

import type { FormEvent, RefObject } from 'react'
import {
  ArrowsClockwise,
  CaretLeft,
  ChatCircleDots,
  Cursor,
  Eye,
  Robot,
  SpeakerHigh,
  SpeakerSlash,
  SpinnerGap,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { cn, initialOf } from '@/lib/utils'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { Agent } from '@/lib/api'
import type { DriveMode, LiveStatus, VisualState } from './browserLiveViewModel'

// Toolbar icon buttons share ONE shape (operator direction, 2026-08-04: "the
// buttons should be icons ... it needs to be flatter"). Back, refresh, annotate,
// mute and the degraded-retry all render as a bare 32px glyph with no border and
// no fill — the frames and pill backgrounds made a row of five controls read as
// five competing objects. Hover is the only chrome; active state is carried by
// COLOUR PLUS `aria-pressed`, never colour alone. The coarse-pointer floor keeps
// the WCAG 2.5.8 target even though the visual box shrank. Written literally at
// each JSX site below (not a shared constant referenced across files) — the
// design-system spacing/colour locks only verify a literal class string at its
// own className site.
const TOOLBAR_ICON_BTN =
  'shrink-0 flex h-8 w-8 items-center justify-center rounded-md transition-colors ' +
  'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] ' +
  'disabled:cursor-not-allowed disabled:opacity-40 ' +
  'pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]'

type DriveChip = {
  label: string
  Icon: typeof Robot
  textClass: string
  dotClass: string
  pulse: boolean
}

// ── ADR-040 D6 — the finite key `resolveDriveChip` switches on below.
// `visualState` alone decides 4 of the 7 chip states directly; the 5th
// (`'idle'`) needs `visualDriveMode` too (connecting/reconnecting vs.
// someone else driving vs. genuinely idle) — this collapses both into ONE
// discriminant so `resolveDriveChip` can stay a single flat switch instead
// of delegating to a second dispatcher function from inside a `case`. That
// avoids a real gap: the design-system colour/spacing locks
// (scripts/design-system-locks/ts-colors.mjs, spacing.mjs) resolve a
// dispatcher function's return only when EVERY clause returns an object
// literal directly from a single, top-level, switch-shaped function — a
// case clause that itself CALLS another dispatcher, or an if/else chain
// instead of a switch, is not proven.
type DriveChipKey = 'agent-working' | 'you-driving' | 'annotating' | 'error' | 'idle-disconnected' | 'idle-other-driving' | 'idle-default'

function driveChipKeyFor(visualState: VisualState, visualDriveMode: DriveMode): DriveChipKey {
  if (visualState !== 'idle') return visualState
  if (visualDriveMode === 'disconnected') return 'idle-disconnected'
  if (visualDriveMode === 'other-driving') return 'idle-other-driving'
  return 'idle-default'
}

// ── ADR-040 D6 — header chip config (icon + text label + colour), derived
// from `visualState`/`visualDriveMode` via driveChipKeyFor above. Words +
// icon back up the colour for accessibility (never colour alone).
//
// Kept LOCAL to this file (not shared from browserLiveViewModel.ts) so the
// design-system locks can resolve `driveChip.textClass`/`driveChip.dotClass`
// below back to this function's literal-object returns — cross-file
// resolution of a pre-computed chip object reads as opaque and is flagged.
function resolveDriveChip(state: {
  visualState: VisualState
  visualDriveMode: DriveMode
  agentDisplayName: string
  statusState: LiveStatus
}): DriveChip {
  switch (driveChipKeyFor(state.visualState, state.visualDriveMode)) {
    case 'agent-working':
      return { label: `${state.agentDisplayName} is browsing…`, Icon: Robot, textClass: 'text-[var(--color-info)]', dotClass: 'bg-[var(--color-info)]', pulse: true }
    case 'you-driving':
      return { label: "You're driving", Icon: Cursor, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: true }
    case 'annotating':
      return { label: "You're annotating", Icon: ChatCircleDots, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: false }
    case 'error':
      return { label: 'Error', Icon: WarningCircle, textClass: 'text-[var(--color-error)]', dotClass: 'bg-[var(--color-error)]', pulse: false }
    case 'idle-disconnected':
      return {
        label: state.statusState === 'disconnected' ? 'Reconnecting…' : 'Connecting…',
        Icon: SpinnerGap,
        textClass: 'text-[var(--color-muted)]',
        dotClass: 'bg-[var(--color-muted)]',
        pulse: false,
      }
    case 'idle-other-driving':
      // Informational, NOT a lock-out. Control is shared — this viewer's mouse,
      // keyboard and omnibox all still work while someone else is also active
      // (operator directive, 2026-08-03). The old label read "Someone else is
      // driving", which told the user their input would be ignored — and it
      // was, because the client and server both gated on the lock. Both gates
      // are gone; the chip now just says who else is here.
      return { label: 'Also viewing', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
    default:
      return { label: 'Click to drive', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
  }
}

export function BrowserLiveToolbar({
  connected,
  annotateMode,
  urlInput,
  onUrlChange,
  onUrlFocus,
  onUrlBlur,
  onOmniboxSubmit,
  onToolbarNav,
  addressBarRef,
  resolvedAgent,
  agentDisplayName,
  visualState,
  visualDriveMode,
  statusState,
  canAnnotate,
  onToggleAnnotate,
  showMute,
  videoMuted,
  onToggleMute,
}: {
  connected: boolean
  annotateMode: boolean
  urlInput: string
  onUrlChange: (value: string) => void
  onUrlFocus: () => void
  onUrlBlur: () => void
  onOmniboxSubmit: (e: FormEvent) => void
  onToolbarNav: (kind: 'navigate_back' | 'reload' | 'stop_loading') => void
  addressBarRef: RefObject<HTMLInputElement | null>
  resolvedAgent: Agent | undefined
  agentDisplayName: string
  visualState: VisualState
  visualDriveMode: DriveMode
  statusState: LiveStatus
  canAnnotate: boolean
  onToggleAnnotate: () => void
  showMute: boolean
  videoMuted: boolean
  onToggleMute: () => void
}) {
  const driveChip = resolveDriveChip({ visualState, visualDriveMode, agentDisplayName, statusState })
  return (
    /* == Row B: toolbar ============================================
        [back] [refresh] [address] then the status/identity chips and the
        mode toggles that used to occupy their own row above the tabs. The
        address field is deliberately no longer the full width of the panel;
        it gives that space to the controls, which is what removes the row.

        Only the address input is wrapped in the <form>. Enter-to-submit is
        all the form was ever for, and keeping the toggles outside it avoids
        implying they take part in submission. */
    <div className="flex h-chrome-header min-h-chrome-header shrink-0 items-center gap-[var(--space-1)] px-[var(--space-2)]">
      <IconButton
        onClick={() => onToolbarNav('navigate_back')}
        disabled={!connected} /* not gated on controlledByOther: control is shared (2026-08-03) */
        aria-label="Go back"
        title="Back"
        className={TOOLBAR_ICON_BTN}
      >
        <CaretLeft size={16} weight="bold" />
      </IconButton>
      <IconButton
        onClick={() => onToolbarNav('reload')}
        disabled={!connected} /* not gated on controlledByOther: control is shared (2026-08-03) */
        aria-label="Refresh page"
        title="Refresh"
        className={TOOLBAR_ICON_BTN}
      >
        <ArrowsClockwise size={15} />
      </IconButton>
      <IconButton
        onClick={() => onToolbarNav('stop_loading')}
        disabled={!connected || annotateMode}
        aria-label="Stop loading"
        title="Stop loading"
        className={TOOLBAR_ICON_BTN}
      >
        <X size={15} />
      </IconButton>
      {/* min-w floor is load-bearing, not cosmetic: with `min-w-0 flex-1`
          alone the field collapsed to 23px on a 575px row (measured on UAT
          v59) once the chips and toggles were added beside it — flex happily
          takes a min-content:0 item to zero. The floor makes "the address bar
          stays usable" a guarantee instead of an arithmetic coincidence that
          holds only until the next control is added. */}
      <form onSubmit={onOmniboxSubmit} className="flex min-w-[120px] flex-1 items-center">
      <Input
        ref={addressBarRef}
        type="text"
        value={urlInput}
        onChange={(e) => onUrlChange(e.target.value)}
        onFocus={onUrlFocus}
        onBlur={onUrlBlur}
        placeholder="Search or enter a URL…"
        aria-label="Address bar"
        className="h-8 flex-1 text-[length:var(--type-utility-xs-size)]"
      />
      </form>
      <span
        data-testid="browser-live-agent-chip"
        title={`Driving ${agentDisplayName}'s browser context`}
        className="flex shrink-0 items-center gap-[var(--space-1)] px-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium text-[var(--color-secondary)] whitespace-nowrap"
      >
        <span
          aria-hidden="true"
          className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[length:var(--type-caption-size)] font-bold text-[var(--color-primary)]"
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
        className={cn('flex shrink-0 items-center gap-[var(--space-1)] px-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium whitespace-nowrap', driveChip.textClass)}
      >
        <span
          aria-hidden="true"
          className={cn('h-1.5 w-1.5 shrink-0 rounded-full', driveChip.dotClass, driveChip.pulse && 'motion-safe:animate-pulse')}
        />
        <driveChip.Icon size={12} weight={driveChip.pulse ? 'fill' : 'regular'} />
        {driveChip.label}
      </span>
      {canAnnotate && (
        <IconButton
          onClick={onToggleAnnotate}
          disabled={!connected}
          aria-label={annotateMode ? 'Exit annotate mode' : 'Annotate a region'}
          title={annotateMode ? 'Exit annotate mode' : 'Drag a region (or click a spot) to comment on it'}
          aria-pressed={annotateMode}
          className={cn(TOOLBAR_ICON_BTN, annotateMode ? 'text-[var(--color-accent)]' : undefined)}
        >
          <ChatCircleDots size={16} weight={annotateMode ? 'fill' : 'regular'} />
        </IconButton>
      )}
      {showMute && (
        <IconButton
          onClick={onToggleMute}
          aria-label={videoMuted ? 'Unmute audio' : 'Mute audio'}
          title={videoMuted ? 'Unmute audio' : 'Mute audio'}
          aria-pressed={!videoMuted}
          data-testid="browser-live-mute-toggle"
          className={TOOLBAR_ICON_BTN}
        >
          {videoMuted ? <SpeakerSlash size={16} /> : <SpeakerHigh size={16} />}
        </IconButton>
      )}
    </div>
  )
}
