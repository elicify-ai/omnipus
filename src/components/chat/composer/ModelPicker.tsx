import { useEffect, useMemo, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ModelSelector } from '@/components/ui/model-selector'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { fetchAgents, fetchProviders } from '@/lib/api'
import { cn } from '@/lib/utils'

/**
 * ModelPicker — the per-message model selector + its data wiring.
 *
 * Extracted with logic unchanged from ChatControls. Carries the model-only state: the
 * connected-providers query (`availableModels` / `providerGroups`), the
 * `nextModel` / `setNextModel` chat-store slice, the `transcriptLastModel`
 * + `activeAgentModel` memos, and the navigation-aware re-seed effect that
 * makes a model pick sticky within a conversation. Same testid
 * (`composer-model-selector`).
 */
export function ModelPicker({
  className,
  disabled = false,
  tabIndex,
}: {
  className?: string
  /** Read-only mode (e.g. agentRemoved) — disables the underlying ModelSelector trigger. */
  disabled?: boolean
  /** Explicit tab-order position — composer tab ring, see the map in ChatControls.tsx. */
  tabIndex?: number
}) {
  const messages = useChatStore((s) => s.messages)
  const nextModel = useChatStore((s) => s.nextModel)
  const setNextModel = useChatStore((s) => s.setNextModel)
  const addToast = useUiStore((s) => s.addToast)
  const modelSelectorOpen = useUiStore((s) => s.modelSelectorOpen)
  const setModelSelectorOpen = useUiStore((s) => s.setModelSelectorOpen)

  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })

  const { data: providers = [] } = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  const connectedProviders = providers.filter((p) => p.status === 'connected')
  const availableModels = connectedProviders.flatMap((p) => p.models ?? [])
  const providerGroups = connectedProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({ providerName: p.display_name ?? p.name ?? p.id, models: p.models ?? [] }))

  const transcriptLastModel = useMemo<string | null>(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i]
      if (m.role !== 'assistant') continue
      const model = m.model
      if (typeof model === 'string' && model.trim().length > 0) {
        return model.trim()
      }
    }
    return null
  }, [messages])

  const activeAgentModel = useMemo<string | null>(() => {
    const agent = agents.find((a) => a.id === activeAgentId)
    if (!agent || typeof agent.model !== 'string') return null
    const trimmed = agent.model.trim()
    return trimmed.length > 0 ? trimmed : null
  }, [agents, activeAgentId])

  // Re-seed nextModel when the user NAVIGATES to a different session/agent, so
  // the selector shows that session's last-used model. Combined with the chat
  // store no longer clearing nextModel after a send, this makes a model pick
  // sticky within a conversation.
  //
  // MODEL SELECTION SOURCE — the twin of the AGENT PRECEDENCE RULE in
  // src/store/session.ts (fixed for agents in 5157e378, "session attach
  // clobbering the picked agent"). `modelSelectionSource` distinguishes a
  // USER pick (`onPickerChange`) from an AUTO/derived seed (this effect).
  // Without it, ANY activeAgentId change — most commonly the user switching
  // agents mid-conversation via AgentPicker or the "@" mention menu, which
  // changes activeAgentId while activeSessionId stays put — looked identical
  // to a genuine navigation and silently re-seeded nextModel to the new
  // agent's default, discarding whatever model the user had just explicitly
  // picked for the next message. That overwritten value is what went out on
  // the wire (metadata.model_name), so the user's choice was silently
  // ignored.
  //
  // A SESSION change is still treated as real navigation (matching the
  // pre-existing "genuine navigation ... still re-seeds" comment below): it
  // releases any explicit pick and reseeds from the new conversation's own
  // context, same as leaving a workspace releases an explicit agent pick in
  // the agent-precedence rule. Only an agent change WITHOUT a session change
  // is treated as "the same conversation, a new default" rather than
  // "navigation".
  const lastSeedKey = useRef<string>('')
  const modelSelectionSource = useRef<'auto' | 'user'>('auto')
  useEffect(() => {
    const seedKey = `${activeSessionId ?? ''}::${activeAgentId ?? ''}`
    if (seedKey === lastSeedKey.current) return
    const prevSession = lastSeedKey.current.split('::')[0]
    lastSeedKey.current = seedKey
    // Materialization guard: a fresh chat's first send creates a transient
    // '__pending' bucket that then becomes the real session id
    // ('' → '__pending' → realId). That is the SAME conversation continuing,
    // NOT a navigation to a different session, so it must not clobber the model
    // the user just picked for this message (which would otherwise reset to the
    // default and fail to carry to the next message). Genuine navigation —
    // opening/switching to a real session, or New Chat ('' target) — still
    // re-seeds from that target's last-used model.
    if (activeSessionId === '__pending' || prevSession === '__pending') return
    if (activeSessionId !== prevSession) {
      // Genuine navigation to a different conversation — any explicit pick
      // was scoped to the conversation it was made in, so it is released and
      // the new context's own history/default takes over below.
      modelSelectionSource.current = 'auto'
    } else if (modelSelectionSource.current === 'user') {
      // Same conversation, only the agent changed — an explicit per-turn
      // model pick survives an agent change, exactly like an explicit AGENT
      // pick survives a session-derived hint under session.ts's AGENT
      // PRECEDENCE RULE.
      return
    }
    setNextModel(transcriptLastModel ?? activeAgentModel ?? null)
  }, [activeSessionId, activeAgentId, transcriptLastModel, activeAgentModel, setNextModel])

  function onPickerChange(model: string) {
    setNextModel(model || null)
    // Either a non-empty pick (pin this model) or an explicit revert to ''
    // (hand back to auto-follow) is the user's OWN action, not a derived
    // seed — record which, so the re-seed effect above knows whether a later
    // agent-only change is allowed to overwrite it.
    modelSelectionSource.current = model ? 'user' : 'auto'
    if (model === '') {
      addToast({ message: 'Reverted to agent default for next message', variant: 'default' })
    }
  }

  return (
    <div
      className={cn(
        // No pointer-coarse min-height: the 44px floor on this WRAPPER (while
        // the trigger inside stayed h-7, top-aligned) pushed the model
        // selector out of line with its h-7 siblings on touch devices.
        'min-w-0 w-[160px] shrink-0 flex items-center',
        className,
      )}
    >
      <ModelSelector
        variant="ghost"
        models={availableModels}
        value={nextModel ?? ''}
        onChange={onPickerChange}
        placeholder={activeAgentModel ?? 'Select a model…'}
        providerGroups={providerGroups}
        triggerTestId="composer-model-selector"
        constrainToCatalog
        emptyCatalogHint="Connect a provider to pick a model"
        open={modelSelectorOpen}
        onOpenChange={setModelSelectorOpen}
        disabled={disabled}
        tabIndex={tabIndex}
      />
    </div>
  )
}
