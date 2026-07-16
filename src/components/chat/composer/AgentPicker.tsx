import { useEffect } from 'react'
import { Robot, CaretDown } from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatAgents } from '@/hooks/useChatAgents'
import { cn, initialOf } from '@/lib/utils'

/**
 * AgentPicker — the workspace-team-scoped agent selector.
 *
 * Extracted from ChatControls so it can live inside the composer's context
 * row (next to the input it scopes) instead of the workspace header — logic
 * unchanged except the auto-select session-preservation fix (below) and the
 * error/read-only hardening. Calls the same Zustand stores + react-query
 * keys; same testid (`agent-picker-trigger`); same SC-005 `setActiveSession`
 * contract.
 *
 * Owns the auto-select-first-ready-agent effect and the error/all-draft
 * branch UI below — these are solely the picker's concern. The agent-list
 * query + status/worker/core_team scoping itself is shared via
 * `useChatAgents` (src/hooks/useChatAgents.ts) — ModelPicker and the
 * composer's `@` mention menu (useSlashMenu.ts) run the identical
 * `['agents']` query and all dedupe via React Query's cache.
 *
 * Side-effect contract: the auto-select effect writes to the global session
 * store (`setActiveSession`) as a side effect of mounting, so this component
 * is expected to be mounted exactly once per screen (in the composer's
 * context row) — mounting it twice would race two auto-select writers.
 */
export function AgentPicker({
  className,
  disabled = false,
  tabIndex,
}: {
  className?: string
  /** Read-only mode (e.g. agentRemoved) — disables the trigger and skips auto-select. */
  disabled?: boolean
  /** Explicit tab-order position — composer tab ring, see the map in ChatControls.tsx. */
  tabIndex?: number
}) {
  // Fix H (bugfixes3 sign-off): slice selector, not a whole-store
  // destructure — `useSessionStore()` (no selector) re-rendered this
  // component on ANY write to the session store, including ones it doesn't
  // care about, the same anti-pattern Fix 5 removed from useSlashMenu.ts's
  // "@" mention menu (src/hooks/useSlashMenu.ts). Only `activeAgentId`
  // needs to be reactive here (it drives `effectiveAgentId`/`activeAgent`
  // below, recomputed every render); `activeSessionId` and
  // `setActiveSession` are read fresh via `.getState()` at write time
  // instead — in `handleAgentSelect` below, and already inside the
  // auto-select effect's own body (unchanged, see that effect's doc
  // comment) — matching useSlashMenu.ts's selectMentionAgent, which
  // documents the same "fresh-state-in-write-path" pattern.
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const agentSelectorOpen = useUiStore((s) => s.agentSelectorOpen)
  const setAgentSelectorOpen = useUiStore((s) => s.setAgentSelectorOpen)

  const { agents, chatAgents, isError: agentsError, refetch } = useChatAgents()

  // A background-refetch failure (e.g. gateway restart + tab refocus) must
  // not tear down an already-usable cached picker — only treat this as a
  // hard error when there is no cached agent data to fall back on.
  const hasHardError = agentsError && agents.length === 0
  // Draft-branch: agents exist, but none is ready to chat yet.
  const isDraftOnly = chatAgents.length === 0 && agents.length > 0

  // The `/agents` slash command sets agentSelectorOpen=true unconditionally
  // (useSlashMenu.ts), but the error/draft branches below return before the
  // DropdownMenu mounts — without this reset the flag would latch and the
  // menu would spontaneously pop open the instant the query recovers (e.g. a
  // background refetch resolves). Placed BEFORE the early returns so hook
  // order stays stable across renders.
  useEffect(() => {
    if ((hasHardError || isDraftOnly) && agentSelectorOpen) {
      setAgentSelectorOpen(false)
    }
  }, [hasHardError, isDraftOnly, agentSelectorOpen, setAgentSelectorOpen])

  // Auto-select the first ready agent if none is active yet.
  //
  // Preserve activeSessionId here — setActiveSession writes its first
  // argument to the store UNCONDITIONALLY (src/store/session.ts), so passing
  // null (as this used to) detaches whatever session is currently active.
  // ChatControls only ever rendered in the workspace header, where there was
  // rarely a deep-linked session to lose; the composer also renders on the
  // /sessions/{id} deep-link route (src/routes/_app/sessions.$sessionId.tsx),
  // where a legacy session with no agent fields would silently detach the
  // just-opened session the instant this effect ran.
  //
  // Reads FRESH state via getState() rather than trusting the closure's
  // activeAgentId/activeSessionId, and re-checks `!freshAgent` right before
  // writing — guards against a stale-closure race where this effect and
  // another writer both observed the pre-write activeAgentId===null and
  // would otherwise both fire a write.
  //
  // setActiveSession unconditionally clears attachedSessionType/
  // attachedTaskTitle (src/store/session.ts), which would otherwise silently
  // wipe the "Task:" banner off a deep-linked task session the instant
  // auto-select ran. Capture both BEFORE the write (they're about to be
  // nulled) and restore them via the store's own setAttachedContext setter
  // immediately after.
  useEffect(() => {
    if (disabled) return
    if (!activeAgentId && chatAgents.length > 0) {
      const {
        activeSessionId: freshSession,
        activeAgentId: freshAgent,
        attachedSessionType,
        attachedTaskTitle,
        setAttachedContext,
        // Fix H: read fresh here too (see the slice-selector comment above
        // `activeAgentId`) — `activeSessionId`/`setActiveSession` are no
        // longer captured as outer component-scope bindings, so this
        // effect's ONLY remaining reason to depend on them is gone; the
        // dependency array below drops both accordingly. Nothing else in
        // this effect's logic changes — it already read `freshSession`
        // (not the outer `activeSessionId`) before this fix.
        setActiveSession: freshSetActiveSession,
      } = useSessionStore.getState()
      if (freshAgent) return
      const first = chatAgents[0]
      freshSetActiveSession(freshSession, first.id, first.type)
      if (attachedSessionType) {
        setAttachedContext(attachedSessionType, attachedTaskTitle)
      }
    }
  }, [activeAgentId, chatAgents, disabled])

  if (hasHardError) {
    return (
      <div className={cn('flex items-center gap-2 px-2 min-w-0', className)}>
        <span className="text-xs text-[var(--color-error)] truncate">Could not load agents</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[10px] shrink-0"
          onClick={() => refetch()}
        >
          Retry
        </Button>
      </div>
    )
  }

  if (isDraftOnly) {
    return (
      <div className={cn('flex items-center gap-2 px-2 min-w-0', className)}>
        <span className="text-xs text-[var(--color-muted)] truncate">
          All agents are in draft status. Configure an agent to start chatting.
        </span>
      </div>
    )
  }

  const effectiveAgentId = activeAgentId || chatAgents[0]?.id
  const activeAgent = chatAgents.find((a) => a.id === effectiveAgentId)

  // Fix H: activeSessionId/setActiveSession read fresh via `.getState()` at
  // click time — this is the write path, so the freshest state wins,
  // matching useSlashMenu.ts's selectMentionAgent (Fix 5's documented
  // pattern) now that neither is a captured outer binding any more.
  const handleAgentSelect = (agentId: string) => {
    const { activeSessionId, setActiveSession } = useSessionStore.getState()
    const selected = agents.find((a) => a.id === agentId)
    setActiveSession(activeSessionId, agentId, selected?.type ?? null)
  }

  return (
    <DropdownMenu open={agentSelectorOpen} onOpenChange={setAgentSelectorOpen}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          data-testid="agent-picker-trigger"
          disabled={disabled}
          tabIndex={tabIndex}
          className={cn(
            // Compact everywhere — deliberately NO pointer-coarse:min-h-[44px]:
            // on touch devices the 44px floor inflated the control (operator
            // wants the composer context row genuinely compact).
            'flex items-center gap-1.5 h-7 px-1.5 text-xs font-medium max-w-[200px] min-w-0',
            className,
          )}
          title={activeAgent?.description || activeAgent?.name || 'Select agent'}
          aria-label={`Select agent (current: ${activeAgent?.name ?? 'none'})`}
        >
          {/* Fix 9: aria-hidden — this is a decorative avatar dot (icon or a
              single-letter initial), not label text. J.3 correction
              (bugfixes3 sign-off): the trigger BUTTON already carries an
              explicit `aria-label` below, and an explicit aria-label wins
              outright over descendant text content in the accessible-name
              computation — so the initial was never actually "polluting"
              the button's accessible NAME, even without aria-hidden. The
              real benefit here is for screen readers' browse/reading mode:
              without aria-hidden, a user arrowing through the page's raw
              content would still land on and hear the bare initial
              character as its own piece of text; aria-hidden removes it
              from that traversal entirely, leaving only the meaningful
              "Select agent (current: X)" label. */}
          <div
            aria-hidden="true"
            className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
            style={{ backgroundColor: activeAgent?.color ?? 'var(--color-surface-3)' }}
          >
            {activeAgent
              ? activeAgent.icon
                ? <IconRenderer icon={activeAgent.icon} size={11} />
                : initialOf(activeAgent.name)
              : <Robot size={11} />}
          </div>
          <span className="truncate">
            {activeAgent ? activeAgent.name : 'Select agent'}
          </span>
          <CaretDown size={11} className="shrink-0 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-96">
        {chatAgents.map((agent) => (
          <DropdownMenuItem
            key={agent.id}
            onClick={() => handleAgentSelect(agent.id)}
            className="flex items-center gap-2"
            title={agent.description || agent.name}
          >
            {/* Fix 9: aria-hidden — same rationale as the trigger's dot
                above; the menu item's own accessible name should read from
                the agent name text next to it, not the decorative initial. */}
            <div
              aria-hidden="true"
              className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
              style={{ backgroundColor: agent.color ?? 'var(--color-surface-3)' }}
            >
              {agent.icon
                ? <IconRenderer icon={agent.icon} size={11} />
                : initialOf(agent.name)}
            </div>
            <span className="truncate">{agent.name}</span>
            {agent.id === effectiveAgentId && (
              <span className="ml-auto shrink-0 text-[var(--color-success)] text-[10px]">active</span>
            )}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
