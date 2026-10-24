import { useEffect, useMemo, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ModelSelector } from '@/components/ui/model-selector'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { fetchAgents, fetchProviders } from '@/lib/api'
import { isProviderUsable } from '@/lib/providerStatus'
import { resolveUniqueModelProvider } from '@/lib/agents/modelToProvider'
import { readSessionModelChoice, writeSessionModelChoice } from '@/store/chat/model-choice'
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
  // H6 (provider hotfix 2026-10-13): the provider row an explicit pick was
  // made under — the (provider, model) selection identity. null = seeded.
  const nextModelProvider = useChatStore((s) => s.nextModelProvider)
  const setNextModel = useChatStore((s) => s.setNextModel)
  const addToast = useUiStore((s) => s.addToast)
  const modelSelectorOpen = useUiStore((s) => s.modelSelectorOpen)
  const setModelSelectorOpen = useUiStore((s) => s.setModelSelectorOpen)

  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })

  const { data: providers = [] } = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  // Subscription providers (openai-chatgpt et al.) are `signed_in`, never
  // `connected` (ADR-068 FR-034) — filter on USABILITY (connected OR
  // signed_in, via the one predicate in providerStatus.ts), not on a key
  // being connected, or every subscription provider's models silently
  // vanish from the picker.
  const usableProviders = providers.filter((p) => isProviderUsable(p.status))
  const availableModels = usableProviders.flatMap((p) => p.models ?? [])
  // H6: `providerId` on each group is what makes the selector's
  // `resolveProviderId`/`onPairChange` emit the real routing key — without it
  // every pick emitted provider:'' and the picked provider was unknowable.
  const providerGroups = usableProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({
      providerName: p.display_name ?? p.name ?? p.id,
      providerId: p.id,
      models: p.models ?? [],
    }))
  // H6 R2 (review round 1 — F3): display-provider resolution for SEEDED
  // values resolves a provider only when EXACTLY ONE usable provider serves
  // the slug. First-wins (the old lookup) presented list order as if it were
  // a recorded choice — the wrong row showed the check whenever two
  // providers advertise the same model. Ambiguous → no row chosen (unknown).
  // The AGENT-default seed no longer goes through this at all: the agent
  // response carries an explicit provider, passed through in the seed below.

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

  // H6 R2 (review round 1 — F3): the agent's own provider routing key. The
  // Agent response carries an explicit `provider` field; an agent-default
  // seed must carry it through instead of dropping it and letting the
  // display guess.
  const activeAgentProvider = useMemo<string | null>(() => {
    const agent = agents.find((a) => a.id === activeAgentId)
    if (!agent || typeof agent.provider !== 'string') return null
    const trimmed = agent.provider.trim()
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
  // Without it, ANY activeAgentId change — the composer picker and the "@"
  // menu no longer do this (FR-007); a session attach or an explicit store
  // write still can — while activeSessionId stays put looked identical
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
    // #1436: the session's own latest explicit choice outranks the transcript
    // and the agent default. A pick is recorded per session (per browser) the
    // moment it is made — no assistant response or send required — so
    // navigation and a full reload both restore exactly what the user chose
    // here, including a deliberate re-selection of the agent's default. The
    // transcript/default seed below applies only to a session with NO stored
    // choice.
    const storedChoice = readSessionModelChoice(activeSessionId)
    if (storedChoice) {
      setNextModel(storedChoice.model, storedChoice.provider)
      return
    }
    // H6 R2 (review round 1 — F3): the seed's provider depends on its SOURCE.
    // An agent-default seed carries the agent's own provider (the Agent
    // response has the field — dropping it made the display guess a
    // different provider's row). A transcript seed genuinely has none — the
    // display then resolves a row only when exactly one provider serves the
    // slug, and otherwise shows no selection rather than a guess.
    setNextModel(
      transcriptLastModel ?? activeAgentModel ?? null,
      transcriptLastModel ? null : activeAgentProvider,
    )
  }, [activeSessionId, activeAgentId, transcriptLastModel, activeAgentModel, activeAgentProvider, setNextModel])

  function onPickerChange(model: string) {
    // provider clears here (omitted second arg → null); a grouped pick's
    // onPairChange (below) immediately re-sets it in the same handler tick.
    setNextModel(model || null)
    // Either a non-empty pick (pin this model) or an explicit revert to ''
    // (hand back to auto-follow) is the user's OWN action, not a derived
    // seed — record which, so the re-seed effect above knows whether a later
    // agent-only change is allowed to overwrite it.
    modelSelectionSource.current = model ? 'user' : 'auto'
    // #1436: persist the session's latest explicit choice at pick time — no
    // send or assistant response required. An empty pick hands the session
    // back to auto-follow, so its stored choice (if any) is cleared and the
    // transcript/agent-default seed applies again.
    writeSessionModelChoice(activeSessionId, model ? { model, provider: null } : null)
    if (model === '') {
      addToast({ message: 'Reverted to agent default for next message', variant: 'default' })
    }
  }

  // H6 (provider hotfix 2026-10-13): the pick is the (provider, model) PAIR —
  // record which provider row it was made under so only that row shows the
  // check when two providers serve the same slug. NOT called for free-text
  // picks (none exist here — the picker is constrained to the catalog).
  // The wire carries model only today (`metadata.model_name`); see the
  // nextModelProvider store comment for the field this becomes if the
  // contract gains `metadata.provider`.
  function onPickerPairChange(pair: { model: string; provider: string }) {
    if (!pair.model) return
    setNextModel(pair.model, pair.provider)
    // #1436: the grouped pick's (provider, model) pair is the recorded
    // identity — restoring it lands the check on exactly the row it was
    // chosen under, even when the slug is served by more than one provider.
    writeSessionModelChoice(activeSessionId, { model: pair.model, provider: pair.provider })
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
        // H6: the picked provider when there is one (explicit pick, or the
        // agent-default seed's carried provider); a provider-less seed
        // resolves a row only when exactly one provider serves the slug —
        // ambiguous seeds show NO chosen row instead of a first-listed guess.
        valueProvider={nextModelProvider ?? resolveUniqueModelProvider(usableProviders, nextModel ?? '')}
        onChange={onPickerChange}
        onPairChange={onPickerPairChange}
        // #1436: mark the agent's default row so the user can see which model
        // is the default and switch back to it deliberately. The mark follows
        // the (provider, model) pair, not the bare slug.
        defaultModel={activeAgentModel}
        defaultProvider={activeAgentProvider}
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
