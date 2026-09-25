// ActivityBar — compact, non-scrolling indicator showing live background
// agent/shell activity (native delegate spans, external-CLI 3rd-party
// delegate spans, and background `bash` runs). Click opens the ActivityPanel
// slide-out for full detail.
//
// Mounted once by OmnipusComposer (ChatScreen.tsx), rendered BELOW the
// composer card, bare on the shell background. Renders NOTHING when there is
// nothing to show it for (see shouldMount below) — mirroring
// RateLimitIndicator's own precedent of conditional mounting rather than
// showing an empty/idle state (found via /visual-qa live inspection: an
// always-visible, full-width "No active background work" bar reads as
// noise, not signal, exactly the kind of ambient clutter the Activity Bar
// was designed to REPLACE, not reintroduce). When something is running, the
// indicator is a compact, content-sized pill (no forced full-width stretch)
// so a single running item doesn't visually dominate the composer area.
//
// Fix 1 (2026-07-16): the bar/panel pair now ALSO stays mounted while (a)
// panelOpen is true — an open panel must never vanish mid-inspection just
// because the last running item finished a beat earlier — or (b)
// recentlyFinished still retains any error/interrupted/timeout item, shown
// via a distinct failed-state pill variant (red dot + "N failed", no
// spinner). This replaces an earlier, now-false premise ("completion is
// already narrated in the chat itself, so the bar doesn't need to be the
// system of record for it") that died the moment delegation/background-bash
// cards were hidden from the thread by default (toolVisibility.ts) — a
// failed background span or bash session can now be genuinely invisible
// everywhere else at idle, so the panel is the designated failure-
// transparency surface and must stay reachable for it. A purely-successful
// idle history still disappears entirely, preserving the original "glance,
// not a permanent history browser" intent for the common case.
//
// Delegation chat surface (D5): two pills, one panel. Agents mounts for an
// open agent child (queued included) or a retained non-shell failure; its
// NUMBER is runningChildren (ADR-091 FR-E-005 — lifecycleState 'running'
// only, so bash and queued children are not counted). Commands mounts for
// background bash, or a retained bash failure. Each pill also stays while
// the panel it opened is still open. Commands opens that same panel
// scrolled to the background-commands section.

import { useState, type ReactNode } from 'react'
import { ArrowsClockwise, CaretRight } from '@phosphor-icons/react'
import { ActivityAvatar } from './ActivityAvatar'
import { ActivityPanel } from './ActivityPanel'
import { Button } from '@/components/ui/button'
import { useRunningActivity } from '@/hooks/useRunningActivity'
import type { ActivityItem } from '@/hooks/useRunningActivity'
import { statusDot } from '@/lib/toolStatusConfig'

const MAX_STACK_AVATARS = 4

/**
 * Status values, drawn from `recentlyFinished`, that keep the pill/panel
 * reachable at idle (Fix 1) — the failure-transparency surface now that
 * delegation/background-bash cards are hidden from the thread by default.
 * Deliberately excludes 'cancelled' (a deliberate user/operator stop, not a
 * failure) and 'success' — only genuine failure/interruption states count.
 */
function isFailedStatus(status: ActivityItem['status']): boolean {
  return status === 'error' || status === 'interrupted' || status === 'timeout'
}

const PILL_CLASS =
  'h-auto max-w-full justify-start self-start rounded-full border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-left text-[length:var(--type-utility-xs-size)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-secondary)]'

/** Which pill opened the panel — that pill stays mounted while the panel is open (Fix 1), the other does not (D5). */
type PillKind = 'agents' | 'commands'

function ActivityPill({
  testId,
  labelTestId,
  name,
  label,
  spinning,
  failed,
  expanded,
  onClick,
  leading,
}: {
  testId: string
  labelTestId: string
  name: string
  label: string
  spinning: boolean
  failed: boolean
  expanded: boolean
  onClick: () => void
  leading?: ReactNode
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      data-testid={testId}
      onClick={onClick}
      aria-haspopup="dialog"
      aria-expanded={expanded}
      aria-label={`${name} — ${label}`}
      className={PILL_CLASS}
    >
      {leading}
      {spinning ? (
        <ArrowsClockwise size={12} className="shrink-0 animate-spin text-[var(--color-accent)]" aria-hidden="true" />
      ) : failed ? (
        statusDot('bg-[var(--color-error)]')
      ) : (
        statusDot('bg-[var(--color-muted)]')
      )}
      <span className="shrink-0 font-medium text-[var(--color-secondary)]">{name}</span>
      <span
        data-testid={labelTestId}
        className={`min-w-0 truncate font-medium ${failed && !spinning ? 'text-[var(--color-error)]' : 'text-[var(--color-secondary)]'}`}
      >
        {label}
      </span>
      <CaretRight size={12} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
    </Button>
  )
}

export function ActivityBar() {
  // The Agents NUMBER is runningChildren (isRunningAgentChild: kind 'agent'
  // and lifecycleState 'running'). The mount gate is wider: any open agent
  // span, including queued — a queued launch emits subagent_start then
  // subagent_state(queued), so runningChildren stays 0 until Dispatch
  // (steer_launcher.go::publishSteeredLaunch). Bash never enters that count.
  const { runningChildren, runningChildItems, running, recentlyFinished } = useRunningActivity()
  const [panelOpen, setPanelOpen] = useState(false)
  const [heldByPanel, setHeldByPanel] = useState<PillKind | null>(null)
  const [scrollRequest, setScrollRequest] = useState<{ section: 'commands'; nonce: number } | null>(null)

  const agentOpen = running.some((item) => item.kind === 'agent')
  const bashRunning = running.filter((item) => item.kind === 'bash').length
  const failedAgents = recentlyFinished.filter((item) => item.kind !== 'bash' && isFailedStatus(item.status))
  const failedCommands = recentlyFinished.filter((item) => item.kind === 'bash' && isFailedStatus(item.status))

  const showAgents = agentOpen || failedAgents.length > 0 || (panelOpen && heldByPanel === 'agents')
  const showCommands = bashRunning > 0 || failedCommands.length > 0 || (panelOpen && heldByPanel === 'commands')
  if (!showAgents && !showCommands) return null

  const agentLabel = runningChildren > 0
    ? `${runningChildren} running`
    : failedAgents.length > 0
      ? `${failedAgents.length} failed`
      : 'Activity'
  const commandLabel = bashRunning > 0
    ? `${bashRunning} background ${bashRunning === 1 ? 'command' : 'commands'}`
    : failedCommands.length > 0
      ? `${failedCommands.length} failed`
      : 'Activity'

  const stackItems = runningChildItems.slice(0, MAX_STACK_AVATARS)

  function openAgents() {
    setHeldByPanel('agents')
    setScrollRequest(null)
    setPanelOpen(true)
  }

  function openCommands() {
    setHeldByPanel('commands')
    setScrollRequest((prev) => ({ section: 'commands', nonce: (prev?.nonce ?? 0) + 1 }))
    setPanelOpen(true)
  }

  return (
    <>
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        {showAgents && (
          <ActivityPill
            testId="activity-bar"
            labelTestId="activity-bar-label"
            name="Agents"
            label={agentLabel}
            spinning={runningChildren > 0}
            failed={failedAgents.length > 0 && runningChildren === 0}
            expanded={panelOpen}
            onClick={openAgents}
            leading={
              <div className="flex -space-x-[var(--space-2)] shrink-0">
                {stackItems.map((item) => (
                  <div key={item.key} className="rounded-full ring-2 ring-[var(--color-surface-1)]">
                    <ActivityAvatar item={item} size="sm" />
                  </div>
                ))}
              </div>
            }
          />
        )}
        {showCommands && (
          <ActivityPill
            testId="activity-pill-commands"
            labelTestId="activity-pill-commands-label"
            name="Commands"
            label={commandLabel}
            spinning={bashRunning > 0}
            failed={failedCommands.length > 0 && bashRunning === 0}
            expanded={panelOpen}
            onClick={openCommands}
          />
        )}
      </div>
      <ActivityPanel
        open={panelOpen}
        onOpenChange={setPanelOpen}
        running={running}
        recentlyFinished={recentlyFinished}
        scrollRequest={scrollRequest}
      />
    </>
  )
}
