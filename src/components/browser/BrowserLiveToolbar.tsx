// Row B of the live-browser chrome: navigation, omnibox, identity, mode toggles.

import type { FormEvent, RefObject } from 'react'
import {
  ArrowsClockwise,
  CaretLeft,
  ChatCircleDots,
  Robot,
  SpeakerHigh,
  SpeakerSlash,
  X,
} from '@phosphor-icons/react'
import { cn, initialOf } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { Agent } from '@/lib/api'
import { TOOLBAR_ICON_BTN, type DriveChip } from './browserLiveViewModel'

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
  driveChip,
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
  driveChip: DriveChip
  canAnnotate: boolean
  onToggleAnnotate: () => void
  showMute: boolean
  videoMuted: boolean
  onToggleMute: () => void
}) {
  return (
    /* == Row B: toolbar ============================================
        [back] [refresh] [address] then the status/identity chips and the
        mode toggles that used to occupy their own row above the tabs. The
        address field is deliberately no longer the full width of the panel;
        it gives that space to the controls, which is what removes the row.

        Only the address input is wrapped in the <form>. Enter-to-submit is
        all the form was ever for, and keeping the toggles outside it avoids
        implying they take part in submission. */
    <div className="flex h-chrome-header min-h-chrome-header shrink-0 items-center gap-1 px-2">
      <button tabIndex={0}
        type="button"
        onClick={() => onToolbarNav('navigate_back')}
        disabled={!connected} /* not gated on controlledByOther: control is shared (2026-08-03) */
        aria-label="Go back"
        title="Back"
        className={TOOLBAR_ICON_BTN}
      >
        <CaretLeft size={16} weight="bold" />
      </button>
      <button tabIndex={0}
        type="button"
        onClick={() => onToolbarNav('reload')}
        disabled={!connected} /* not gated on controlledByOther: control is shared (2026-08-03) */
        aria-label="Refresh page"
        title="Refresh"
        className={TOOLBAR_ICON_BTN}
      >
        <ArrowsClockwise size={15} />
      </button>
      <button
        type="button"
        tabIndex={0}
        onClick={() => onToolbarNav('stop_loading')}
        disabled={!connected || annotateMode}
        aria-label="Stop loading"
        title="Stop loading"
        className={TOOLBAR_ICON_BTN}
      >
        <X size={15} />
      </button>
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
          onClick={onToggleAnnotate}
          disabled={!connected}
          aria-label={annotateMode ? 'Exit annotate mode' : 'Annotate a region'}
          title={annotateMode ? 'Exit annotate mode' : 'Drag a region (or click a spot) to comment on it'}
          aria-pressed={annotateMode}
          className={cn(TOOLBAR_ICON_BTN, annotateMode && 'text-[var(--color-accent)]')}
        >
          <ChatCircleDots size={16} weight={annotateMode ? 'fill' : 'regular'} />
        </button>
      )}
      {showMute && (
        <button tabIndex={0}
          type="button"
          onClick={onToggleMute}
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
  )
}
