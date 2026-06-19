import { useState, useEffect, useRef, useMemo } from 'react'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  X,
  CaretDown,
  CaretUp,
  UploadSimple,
  Info,
  Plus,
  Sparkle,
  Star,
  Lightning,
  ArrowUp,
  ArrowDown,
  Warning,
  Lock,
  WarningCircle,
  Trash,
} from '@phosphor-icons/react'
import { useAutoSave } from '@/hooks/useAutoSave'
import { useFocusRestore } from '@/hooks/useFocusRestore'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { SmartSelect } from '@/components/ui/smart-select'
import { ModelSelector } from '@/components/ui/model-selector'
import { useModelToProvider } from '@/lib/agents/modelToProvider'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { isKnownModelSlug } from '@/lib/agents/model-validation'
import { ToolsAndPermissions } from './ToolsAndPermissions'
import { SandboxProfileSelector } from './SandboxProfileSelector'
import { ShellDenyPatternsEditor } from './ShellDenyPatternsEditor'
import { ExecutorSelector } from './ExecutorSelector'
import { BehaviorFields, AvatarColorPicker, IconPicker, AvatarHeader } from './AgentFormFields'
import { SchedulesList } from '@/components/command-center/SchedulesList'
import { ScheduleFormSheet } from '@/components/command-center/ScheduleFormSheet'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import {
  fetchAgent,
  fetchAppState,
  updateAgent,
  deleteAgent,
  fetchProviders,
  fetchAgentSessions,
  fetchActivity,
  fetchSkills,
  testAgentRunner,
  isWorker,
  type AgentSession,
  type ActivityEvent,
  type AgentToolsCfg,
  type SandboxProfile,
  type Skill,
  type ExecutorConfig,
} from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'
import type { FallbackModel } from '@/lib/api/generated/openapi-types'
import { type IconName } from '@/lib/agentIcons'

/** Editor's fallback entry — `FallbackModel` from the contract with `provider` narrowed to required (the editor always populates it at hydration). */
type FallbackEntry = FallbackModel & { provider: string }

interface AgentProfileProps {
  /**
   * Explicit agent id (wins over the store-driven `editAgentId`). Used by
   * tests and direct renders; the primary path is the UI store.
   */
  agentId?: string
}

export function AgentProfile({ agentId: agentIdProp }: AgentProfileProps = {}) {
  const editAgentId = useUiStore((s) => s.editAgentId)
  const closeEditAgentSlideOver = useUiStore((s) => s.closeEditAgentSlideOver)
  const addToast = useUiStore((s) => s.addToast)
  const agentId = agentIdProp ?? editAgentId
  const isOpen = agentId !== null

  const queryClient = useQueryClient()

  const { data: agent, isLoading, isError, error: agentError, refetch: refetchAgent } = useQuery({
    queryKey: ['agent', agentId],
    queryFn: () => fetchAgent(agentId as string),
    enabled: agentId !== null,
  })

  const { data: providers = [], isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  const { data: agentSessions = [], isError: sessionsError } = useQuery({
    queryKey: ['agent-sessions', agentId],
    queryFn: () => fetchAgentSessions(agentId as string),
    enabled: agentId !== null,
  })

  const { data: allActivity = [], isError: activityError } = useQuery({
    queryKey: ['activity'],
    queryFn: fetchActivity,
    staleTime: 30_000,
  })

  const { data: appState } = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
    staleTime: 60_000,
  })

  // US-E6: fetch available (installed) skills so the picker can show them.
  const { data: availableSkills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: fetchSkills,
    staleTime: 60_000,
  })

  const recentActivity = allActivity
    .filter((e) => e.agent_id === agentId)
    .slice(0, 5)

  const connectedProviders = providers.filter((p) => p.status === 'connected')
  const availableModels = connectedProviders.flatMap((p) => p.models ?? [])
  const providerGroups = connectedProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({ providerName: p.display_name ?? p.name ?? p.id, models: p.models ?? [] }))

  // W6-C2 / I9: model → provider-id lookup. Extracted to
  // `src/lib/agents/modelToProvider.ts` so the same helper is
  // reusable + unit-testable. Returns the **provider id** (the wire
  // routing key per FR-007 / FallbackModel.yaml), NOT the display
  // name — pre-C2 the editor was storing `display_name ?? name ?? id`
  // and emitting that to the wire, which silently downgraded
  // `provider` to a brand label and broke runtime resolution.
  const { lookup: modelToProvider } = useModelToProvider(connectedProviders)

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  // Tracks whether the initial hydration from the server has completed.
  // Guards auto-save from firing with default (empty) state before the first fetch resolves.
  const hasHydrated = useRef(false)

  // I12: cache the executor kind+cli we've already passed the runner-test for,
  // so the auto-save debounce (500 ms) doesn't re-fire the test on every
  // unrelated keystroke while the user has external-cli selected. We re-test
  // only when the kind or cli actually changes (or the agent id changes).
  type ExecutorSig = { kind: ExecutorConfig['kind']; cli?: ExecutorConfig['cli']; agentId: string | null }
  const testedExecutorSig = useRef<ExecutorSig | null>(null)

  // Reset flags when navigating to a different agent
  useEffect(() => {
    isDirtyRef.current = false
    hasHydrated.current = false
  }, [agentId])

  // W6-B1 / I7 (WCAG 2.4.3): restore focus to the element that triggered the
  // slide-over (typically the AgentCard button — also covers the TrustGraph
  // row click and the /agents/:id route mount) on close.
  //
  // Wave 6 / B-fix: extracted to `useFocusRestore` hook so the same
  // proven pattern (capture-in-onOpenAutoFocus + restore-via-RAF + try/
  // catch) is shared with the modal in CreateAgentModal. Future Wave C
  // consumers (ModelFooter slide-over) will adopt the hook instead of
  // forking this block.
  const { onOpenAutoFocus: handleOpenAutoFocus } = useFocusRestore(isOpen)

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('')
  const [selectedColor, setSelectedColor] = useState<string | undefined>(undefined)
  const [selectedIcon, setSelectedIcon] = useState<IconName>('Robot')
  // W6-B4 / G3: `default` flag mirrors Agent.default on the wire. At most one
  // agent is default across the roster; the backend enforces that on PUT.
  // The toggle in the Identity strip is the only way to flip it from the
  // Edit profile (previously, users had to go back to the Agents list).
  const [isDefault, setIsDefault] = useState(false)
  // Fallback chain — the wire shape is `[{model, provider}]`; the editor
  // tracks it 1:1 with `provider` always populated (see FallbackEntry
  // type). Provider is needed for the chip badge; the wire payload in
  // `formData` below emits the same shape.
  const [fallbackModels, setFallbackModels] = useState<FallbackEntry[]>([])
  const [temperature, setTemperature] = useState(1.0)
  const [maxTokens, setMaxTokens] = useState(4096)
  const [topP, setTopP] = useState(1.0)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [useGlobalRateLimits, setUseGlobalRateLimits] = useState(true)
  const [maxLlmCallsPerHour, setMaxLlmCallsPerHour] = useState<number | ''>('')
  const [maxToolCallsPerMinute, setMaxToolCallsPerMinute] = useState<number | ''>('')
  const [maxCostPerDay, setMaxCostPerDay] = useState<number | ''>('')
  const [soul, setSoul] = useState('')
  const [instructions, setInstructions] = useState('')
  // W6-B4 / G1: per-agent persona voice identifier (TTS voice name or model ID).
  // Schema-pinned on Agent.voice; not active until v0.2.0 TTS. Empty string
  // means "not configured" — the wire payload omits the field entirely.
  const [voice, setVoice] = useState('')
  const [heartbeat, setHeartbeat] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState(0)
  const [maxToolIterations, setMaxToolIterations] = useState(50)
  const [steeringMode, setSteeringMode] = useState<'one-at-a-time' | 'queue-and-process'>('one-at-a-time')
  const [heartbeatEnabled, setHeartbeatEnabled] = useState(false)
  const [heartbeatInterval, setHeartbeatInterval] = useState(30)
  const [creatingSchedule, setCreatingSchedule] = useState(false)
  // Wave 5 / spec §6.1 BDD #15: Edit slide-over footer Delete agent.
  // Opens an AlertDialog; the confirm mutation invalidates the list and
  // closes the slide-over. Locked agents do not render the trigger.
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [toolsCfg, setToolsCfg] = useState<AgentToolsCfg>({
    builtin: { default_policy: 'allow' },
  })
  // US-E6: per-agent skill assignment (opt-in, default none).
  const [agentSkills, setAgentSkills] = useState<string[]>([])
  const [sandboxProfile, setSandboxProfile] = useState<SandboxProfile | undefined>(undefined)
  const [shellDenyPatterns, setShellDenyPatterns] = useState<string[]>([])
  const [shellAdvancedOpen, setShellAdvancedOpen] = useState(false)
  // Spec-4 FR-4.1: sub-agent executor (native default / external-cli / remote-a2a).
  const [executor, setExecutor] = useState<ExecutorConfig | undefined>(undefined)

  // W6-C4 / G12: when the primary model isn't known to any connected
  // provider, render a persistent inline warning under the picker. The
  // ModelSelector's own trigger button already shows an "Unresolved" chip
  // (src/components/ui/model-selector.tsx); this complementary explanatory
  // line gives the user a clear next step ("add a provider that supports
  // this model") per the G12 ticket's product copy. Empty / unset values
  // are intentionally NOT flagged — that's the default state, not an
  // unresolved one. Computed AFTER `model` is declared so the reference
  // is sound; the `useMemo` would be premature here because the consumers
  // below only need the boolean, not a memoized reference.
  const primaryModelUnresolved =
    model.trim() !== '' && !isKnownModelSlug(model, providers)

  useEffect(() => {
    if (!agent) return
    // isDirtyRef prevents background refetch from overwriting unsaved user edits.
    // We depend on the stable agentId prop (not agent?.id which can be undefined
    // during loading) so the effect re-runs reliably on agent navigation.
    if (isDirtyRef.current) return
    setName(agent.name ?? '')
    setDescription(agent.description ?? '')
    setModel(agent.model ?? '')
    setSelectedColor(agent.color)
    setSelectedIcon((agent.icon as IconName) ?? 'Robot')
    // W6-B4 / G3: hydrate the `default` flag from the agent response. The
    // wire field is a plain boolean; absent = false. The Identity strip
    // shows the current state of this flag and lets the user flip it.
    setIsDefault(agent.default ?? false)
    // Hydrate the fallback chain from the wire. `provider` is optional on
    // the wire type; narrow to required by looking up via modelToProvider,
    // else empty string (renders as a muted dash in the chip).
    const rawFallbacks = agent.fallback_models ?? []
    setFallbackModels(
      rawFallbacks.map((entry) => ({
        ...entry,
        provider: entry.provider || modelToProvider(entry.model) || '',
      })),
    )
    setTemperature(agent.model_params?.temperature ?? 1.0)
    setMaxTokens(agent.model_params?.max_tokens ?? 4096)
    setTopP(agent.model_params?.top_p ?? 1.0)
    setUseGlobalRateLimits(agent.rate_limits?.use_global_defaults ?? true)
    setMaxLlmCallsPerHour(agent.rate_limits?.max_llm_calls_per_hour ?? '')
    setMaxToolCallsPerMinute(agent.rate_limits?.max_tool_calls_per_minute ?? '')
    setMaxCostPerDay(agent.rate_limits?.max_cost_per_day ?? '')
    setSoul(agent.soul ?? '')
    setInstructions(agent.instructions ?? '')
    // W6-B4 / G1: hydrate the persona voice. The wire field is nullable;
    // `null` and absent both render as the empty string in the input.
    // W6-B-fix: trim existing whitespace on disk so the input never
    // renders 3 spaces and the wire round-trip is clean (whitespace was
    // silently reaching the server).
    setVoice((agent.voice ?? '').trim())
    setHeartbeat(agent.heartbeat ?? '')
    setTimeoutSeconds(agent.timeout_seconds ?? 0)
    setMaxToolIterations(agent.max_tool_iterations ?? 50)
    setSteeringMode((agent.steering_mode ?? 'one-at-a-time') as 'one-at-a-time' | 'queue-and-process')
    setHeartbeatEnabled(agent.heartbeat_enabled ?? false)
    setHeartbeatInterval(agent.heartbeat_interval ?? 30)
    setSandboxProfile(agent.sandbox_profile)
    setShellDenyPatterns(agent.shell_policy?.custom_deny_patterns ?? [])
    // Spec-4: hydrate executor (absent → native default, modelled as undefined).
    setExecutor(agent.executor)
    if (agent.tools_cfg) setToolsCfg((prev) => ({
      builtin: agent.tools_cfg?.builtin ?? prev.builtin,
      mcp: agent.tools_cfg?.mcp as AgentToolsCfg['mcp'],
    }))
    // US-E6: hydrate agent skills from the API response (default none).
    setAgentSkills(agent.skills ?? [])
    hasHydrated.current = true
  }, [agentId, agent])

  const formData = useMemo(() => ({
    name,
    description,
    model,
    color: selectedColor,
    icon: selectedIcon,
    // W6-B4 / G3: `default` flag. Always sent with the current value so the
    // PUT payload reflects the user's intent. The wire contract says
    // "Omitting this field leaves the flag unchanged" — but auto-save only
    // fires on a real change, and on the real change the user has flipped
    // this toggle, so emitting the new value is exactly right. If a future
    // edit (say, renaming) does not flip `default`, the formData still
    // includes the existing value; the server treats the PUT as "set to
    // this value", which preserves the existing value — the desired
    // behavior. PUT semantics: at most one agent is default across the
    // roster; the backend enforces uniqueness on PUT and demotes the prior
    // default to false in the same transaction.
    default: isDefault,
    // Editor state matches the wire shape 1:1; emit `undefined` for
    // empty (treated as "no fallbacks" by the backend).
    fallback_models: fallbackModels.length > 0 ? fallbackModels : undefined,
    model_params: { temperature, max_tokens: maxTokens, top_p: topP },
    rate_limits: {
      use_global_defaults: useGlobalRateLimits,
      max_llm_calls_per_hour: maxLlmCallsPerHour !== '' ? maxLlmCallsPerHour : undefined,
      max_tool_calls_per_minute: maxToolCallsPerMinute !== '' ? maxToolCallsPerMinute : undefined,
      max_cost_per_day: maxCostPerDay !== '' ? maxCostPerDay : undefined,
    },
    soul,
    instructions,
    // W6-B4 / G1: voice is optional — emit only when non-empty so the backend
    // can leave the field unchanged when the user hasn't set it. An empty
    // string and `undefined` are semantically equivalent for the wire (both
    // mean "no override"); sending `null` explicitly would clear an existing
    // value, which is the right semantics for "Clear voice" but not for an
    // untouched field. We send `undefined` (omitted) for the empty case.
    // W6-B-fix: trim on the wire so whitespace-only inputs collapse to
    // "no voice configured" rather than persisting a literal "   " that
    // breaks TTS lookup at v0.2.0 release.
    voice: voice.trim() !== '' ? voice.trim() : undefined,
    heartbeat,
    timeout_seconds: timeoutSeconds > 0 ? timeoutSeconds : undefined,
    max_tool_iterations: maxToolIterations,
    steering_mode: steeringMode,
    heartbeat_enabled: heartbeatEnabled,
    heartbeat_interval: heartbeatInterval,
    // 'none' is a UI-only marker meaning "inherit global default". Strip it before
    // submitting so the backend receives undefined (omitted) rather than a value
    // that fails the sandbox_profile enum validation (contract does not include 'none').
    sandbox_profile: sandboxProfile === 'none' ? undefined : sandboxProfile,
    shell_policy: {
      custom_deny_patterns: shellDenyPatterns.filter((p) => p.trim() !== ''),
    },
    tools_cfg: toolsCfg,
    // US-E6: include the per-agent skill list in the auto-save payload.
    // Send the current list (may be empty, meaning no skills). The backend
    // treats an absent field as "leave unchanged" and an explicit empty
    // array as "remove all skills" — we always send the current value so
    // a deliberate clear (removing the last skill) is persisted correctly.
    skills: agentSkills,
    // Spec-4 FR-4.1: persist the executor only when explicitly configured.
    // Omitting it (undefined) leaves the backend on its "native" default
    // rather than forcing an empty value over the wire.
    executor,
  }), [
    name, description, model, selectedColor, selectedIcon, isDefault, fallbackModels,
    temperature, maxTokens, topP, useGlobalRateLimits, maxLlmCallsPerHour,
    maxToolCallsPerMinute, maxCostPerDay, soul, instructions, voice, heartbeat,
    timeoutSeconds, maxToolIterations, steeringMode,
    heartbeatEnabled, heartbeatInterval, sandboxProfile, shellDenyPatterns,
    toolsCfg, agentSkills, executor,
  ])

  const { status: saveStatus, error: saveError } = useAutoSave(
    formData,
    async (data) => {
      // Do not save before the server data has been hydrated into state —
      // saving before hydration would overwrite real data with empty defaults.
      if (!hasHydrated.current) return
      // Form is mounted at the layout level; its state can outlive a
      // closed sheet. Refuse a save with no id rather than PUT /null.
      if (agentId === null) return
      // I12: when the agent's executor is external-cli, run the runner-test
      // BEFORE allowing the save to commit. We re-test only when the kind+cli
      // signature actually changes (or the agent id changes) so the auto-save
      // debounce doesn't re-fire on every keystroke. On a failure, throw —
      // useAutoSave catches and surfaces the error inline, the save is
      // blocked, and the form stays dirty so the user can fix and retry.
      if (data.executor?.kind === 'external-cli' && agentId) {
        const sig: ExecutorSig = {
          kind: data.executor.kind,
          cli: data.executor.cli,
          agentId,
        }
        const last = testedExecutorSig.current
        const needsTest = !last
          || last.agentId !== sig.agentId
          || last.kind !== sig.kind
          || last.cli !== sig.cli
        if (needsTest) {
          let result
          try {
            result = await testAgentRunner(agentId)
          } catch (err) {
            // Network / 5xx failure — surface the message inline. The user
            // can retry the save (or run the explicit Test Connection button).
            const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : String(err)
            addToast({
              message: `Runner test failed before save: ${msg}`,
              variant: 'error',
            })
            throw new Error(`Runner test failed: ${msg}`)
          }
          if (!result.ok) {
            // Backend returned a structured failure (missing-binary,
            // unauthenticated, handshake-failed, unknown-cli, not-external-cli).
            // Block the save — operator needs to fix the runner first.
            addToast({
              message: `Runner test failed: ${result.message}. Fix the runner before saving.`,
              variant: 'error',
            })
            throw new Error(`Runner test failed (${result.reason || 'unknown'}): ${result.message}`)
          }
          // Cache the signature only on success — a failed test keeps us
          // ready to retry on the next change.
          testedExecutorSig.current = sig
        }
      }
      // Locked agents: strip every field the backend treats as immutable for
      // the locked roster (see `.preview-doc/agents.html` for the current
      // 4-base roster). Identity fields plus the sandbox profile, shell
      // policy, tools_cfg, and skills are all built-in for these agents —
      // sending them yields a 403 from the locked-field validator, and the
      // autosave indicator would surface a spurious error. Skills are
      // stripped here (B-2 defense-in-depth on the frontend side): the
      // Skills picker is rendered disabled for locked agents, so this strip
      // is the belt-and-suspenders path for any state that may survive hydration.
      const payload = agent?.locked
        ? (({
            name: _n, description: _d, soul: _s, color: _c, icon: _i,
            heartbeat: _h, instructions: _ins, sandbox_profile: _sp,
            shell_policy: _shp, tools_cfg: _tc, skills: _sk, executor: _ex, ...rest
          }) => rest)(data)
        : data
      await updateAgent(agentId, payload)
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
    // Locked agents can still save model and tool changes — do not disable auto-save
  )


  // Add a fallback from the <ModelSelector> dropdown. Pairs the picked
  // model with the provider that owns it (via modelToProvider). De-dupes
  // by model — the fallback list cannot have the same model twice
  // (US-3). Free-text picks (model not in any connected provider) are
  // accepted but surface a warning toast.
  function addFallbackFromSelector(modelSlug: string) {
    const trimmed = modelSlug.trim()
    if (!trimmed) return
    const knownProvider = modelToProvider(trimmed)
    if (!knownProvider) {
      addToast({
        message: `"${trimmed}" isn't listed by any connected provider — saving anyway, but the fallback may not work.`,
        variant: 'warning',
      })
    }
    setFallbackModels((prev) => {
      if (prev.some((f) => f.model === trimmed)) return prev
      return [...prev, { model: trimmed, provider: knownProvider ?? '' }]
    })
  }

  function removeFallback(modelSlug: string) {
    setFallbackModels((prev) => prev.filter((f) => f.model !== modelSlug))
  }

  // W6-C2 / I9: per-chip provider picker. Picking a different provider
  // for an existing fallback updates the entry's `provider` field
  // (the wire routing key — see FR-007 / FallbackModel.yaml). Picking
  // "—" (empty) clears the provider so the chip flips into the
  // "Provider not connected" state (I11). No-op when the value is
  // unchanged.
  function setFallbackProvider(modelSlug: string, providerId: string) {
    setFallbackModels((prev) =>
      prev.map((f) => (f.model === modelSlug ? { ...f, provider: providerId } : f)),
    )
  }

  // W6-C2 / I10: reorder helper. The wire contract for `fallback_models`
  // is "tried in the order they appear in the array" (see
  // FallbackModel.yaml description); order matters semantically.
  // Up/down arrow buttons keep the move explicit and keyboard-friendly
  // (no native HTML5 drag-and-drop needed, no extra library, accessible
  // to screen readers and keyboard-only users).
  function moveFallback(modelSlug: string, direction: -1 | 1) {
    setFallbackModels((prev) => {
      const idx = prev.findIndex((f) => f.model === modelSlug)
      if (idx < 0) return prev
      const target = idx + direction
      if (target < 0 || target >= prev.length) return prev
      const next = prev.slice()
      const [item] = next.splice(idx, 1)
      next.splice(target, 0, item)
      return next
    })
  }

  function UploadButton({ onUpload }: { onUpload: (content: string) => void }) {
    return (
      <button
        type="button"
        onClick={() => {
          const input = document.createElement('input')
          input.type = 'file'
          input.accept = '.md,.markdown,.txt'
          input.onchange = (e) => {
            const file = (e.target as HTMLInputElement).files?.[0]
            if (!file) return
            if (file.size > 1_000_000) {
              addToast({ message: `File too large (${(file.size / 1_000_000).toFixed(1)}MB). Max 1MB for markdown files.`, variant: 'error' })
              return
            }
            const reader = new FileReader()
            reader.onload = () => {
              onUpload(reader.result as string)
              markDirty()
            }
            reader.onerror = () => {
              addToast({ message: `Failed to read ${file.name}: ${reader.error?.message ?? 'unknown error'}`, variant: 'error' })
            }
            reader.readAsText(file)
          }
          input.click()
        }}
        className="h-7 px-2 text-xs rounded border border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex items-center gap-1"
      >
        <UploadSimple size={12} />
        Upload .md
      </button>
    )
  }

  const recentSessions = agentSessions.slice(0, 10)

  // Wave 5 / spec §6.1 BDD #15: Delete agent confirmation. Mirrors the
  // pattern from SchedulesList (`doDelete`): the mutation invalidates
  // the list cache on success, surfaces the API error inline on failure,
  // and closes the slide-over only on success (so a network blip keeps
  // the operator on the same page). The button itself is hidden for
  // locked agents (see SheetFooter below).
  const deleteAgentMutation = useMutation({
    mutationFn: (id: string) => deleteAgent(id),
    onSuccess: () => {
      // Drop the deleted agent from the list cache immediately so no
      // per-id GET refetch fires for a resource that no longer exists.
      queryClient.setQueryData(['agents'], (prev: unknown) => {
        if (!Array.isArray(prev)) return prev
        return prev.filter((a) => (a as { id?: string }).id !== agentId)
      })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
      setDeleteOpen(false)
      closeEditAgentSlideOver()
      addToast({ message: 'Agent deleted', variant: 'success' })
    },
    onError: (err: unknown) => {
      const msg = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : 'Delete failed'
      addToast({ message: `Delete failed: ${msg}`, variant: 'error' })
      setDeleteOpen(false)
    },
  })

  if (isLoading) {
    return (
      <ProfileSheet
        isOpen={isOpen}
        onClose={closeEditAgentSlideOver}
        title="Edit agent"
        onOpenAutoFocus={handleOpenAutoFocus}
      >
        <div className="flex flex-1 items-center justify-center text-[var(--color-muted)] text-sm">
          Loading agent...
        </div>
      </ProfileSheet>
    )
  }

  if (isError || !agent) {
    // Distinguish "this agent does not exist" (404) from transient errors so
    // the user gets the right copy and the right path forward. The previous
    // version lumped every error into "Agent not found", which misled users
    // on 500s / 401s / 502s and gave them no retry affordance.
    const isNotFound = isApiError(agentError) && agentError.status === 404
    const title = isNotFound ? 'Agent not found' : "Couldn't load agent"
    const detail = isNotFound
      ? 'This agent may have been deleted.'
      : isApiError(agentError)
        ? agentError.userMessage
        : agentError instanceof Error
          ? agentError.message
          : 'Check your connection and try again.'
    return (
      <ProfileSheet
        isOpen={isOpen}
        onClose={closeEditAgentSlideOver}
        title={isNotFound ? 'Agent not found' : "Couldn't load agent"}
        onOpenAutoFocus={handleOpenAutoFocus}
      >
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-8 text-center">
          <p className="text-sm font-medium text-[var(--color-secondary)]">{title}</p>
          <p className="text-xs text-[var(--color-muted)] max-w-sm">{detail}</p>
          <div className="flex gap-2">
            {!isNotFound && (
              <Button variant="outline" size="sm" onClick={() => refetchAgent()}>
                Retry
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={closeEditAgentSlideOver}>
              Back to Agents
            </Button>
          </div>
        </div>
      </ProfileSheet>
    )
  }

  const isLocked = agent.locked === true
  const canEdit = !isLocked
  // Past the early returns, `agent` is non-null and `agentId` is the id used
  // to fetch it. Narrow once for child components that take `string`.
  const resolvedAgentId = agentId as string
  // Tier-branched form. Workers are delegation-only labour agents: never a chat
  // target, no heartbeat, never the default, and carry an executor (the worker's
  // defining property). Base agents (core/custom/system) run native/in-process
  // only — no third-party executor is selectable for them. The locked concept
  // (`.preview-doc/agents.html`) makes the worker-vs-base split a property of
  // the agent itself, so we branch once here and let the JSX ask `isWorkerAgent`
  // to decide which accordions render. See the contract schema for the
  // `worker` type value: contracts/components/schemas/Agent.yaml.
  const isWorkerAgent = isWorker(agent)
  // W6-C1 / M11: native workers are delegation-only labour agents. Their
  // Tools / Skills / Sandbox settings are inherited from the caller and
  // have no effect on a native runtime, so the lower accordions are
  // hidden for them. External-cli workers still need those accordions
  // because the external runner respects the policy. The callout at the
  // top of the profile explains the omission.
  const isNativeWorkerAgent = isWorkerAgent && (!executor || executor.kind === 'native')

  return (
    <ProfileSheet
      isOpen={isOpen}
      onClose={closeEditAgentSlideOver}
      title={`Edit ${agent.name}`}
      onOpenAutoFocus={handleOpenAutoFocus}
    >
      <SheetHeader className="px-8 pt-7 pb-5 border-b border-[var(--color-border)] shrink-0">
          <div className="flex items-center gap-4">
            <AvatarHeader color={selectedColor} />
            <div className="min-w-0">
              <h1 className="font-headline text-xl font-bold text-[var(--color-secondary)]">{agent.name}</h1>
              <div className="flex items-center gap-2 mt-1">
                <Badge variant={agent.type === 'core' ? 'secondary' : 'outline'}>
                  {agent.type}
                </Badge>
                {agent.locked && (
                  <Badge variant="outline" className="text-[var(--color-muted)] border-[var(--color-border)]">
                    read-only
                  </Badge>
                )}
                <span className="text-xs text-[var(--color-muted)]">{agent.description}</span>
          </div>
        </div>
        </div>
        </SheetHeader>

      {/* Wave 5 / spec §6 BDD #13 + §6.4: locked-banner for built-in core
          agents. Pinned at the top of the body, above the tab bar, so the
          operator sees it before any field interactions. Uses the same
          amber/warning visual language as the executor-external-cli
          callout (sibling concept — "this agent is special, read the
          caveat before editing"). Hidden for non-locked agents. */}
      {agent.type === 'core' && agent.locked && (
        <div
          role="alert"
          data-testid="locked-banner"
          className="mx-8 mt-4 rounded-md border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 flex items-start gap-3"
        >
          <WarningCircle className="h-5 w-5 text-[var(--color-error)] shrink-0 mt-0.5" weight="fill" aria-hidden="true" />
          <div className="text-sm">
            <div className="font-semibold text-[var(--color-error)]">This is a built-in core agent</div>
            <div className="text-[var(--color-muted)] mt-1">
              Most fields are read-only. To create your own chat colleague, use the + Add Main button.
            </div>
          </div>
        </div>
      )}

      {/* Scrollable body. Inner padding/width mirrors CreateAgentModal etc. */}
      <div className="flex-1 overflow-y-auto">
        <div className="max-w-2xl mx-auto px-8 py-6 space-y-4">
      {/* W6-B1 / I1: cap the visible-on-open section count at Miller's 7±2.
          Base agents open Identity + Sandbox + Model Configuration + Behavior
          (4 accordions — the Identity strip header is also visible above, so
          the user sees 5 top-level chunks). Workers replace Behavior with
          Executor + Tools & Permissions (Tools is priority for a worker since
          it's their run-time surface; Behavior's persona/heartbeat sub-blocks
          don't apply). Schedules, Sessions, Activity stay collapsed — they're
          reference material, not editing surfaces. */}
      {/* W6-C1 / M11: native-worker delegation-only callout. Native workers
          (default executor.kind) are delegation-only labour agents — they
          are not chat targets and they run with a compiled allow/deny
          rail. Their Tools, Skills, and Sandbox settings are inherited
          from the caller (the agent that delegates work to them) and
          editing them on the worker has no runtime effect. Surface this
          prominently at the top of the profile so the operator
          understands why the lower accordions are absent (or collapsed
          to a summary). External-cli workers DO need their own Tools /
          Sandbox because the external runner respects them — the
          callout only renders for native workers. */}
      {isNativeWorkerAgent && (
        <div
          data-testid="native-worker-delegation-callout"
          role="note"
          aria-label="Delegation-only worker"
          className="rounded-md border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/10 px-4 py-3 flex items-start gap-3"
        >
          <Lightning
            size={16}
            weight="fill"
            className="text-[var(--color-accent)] mt-0.5 shrink-0"
            aria-hidden="true"
          />
          <div className="min-w-0">
            <p className="text-sm font-medium text-[var(--color-secondary)]">
              This is a delegation-only worker
            </p>
            <p className="text-[12px] text-[var(--color-muted)] leading-snug mt-0.5">
              Tools, Skills, and Sandbox settings are inherited from the
              agent that delegates work to this worker and have no
              effect on a native (in-process) runtime. Configure them on
              the caller, or switch this worker to an external runtime
              (Executor accordion) to make them local.
            </p>
          </div>
        </div>
      )}
      {/* Wave 5 / spec §6: Edit slide-over layout is a Tab bar (4–5 tabs
          depending on type) instead of the prior 10-section Accordion. The
          `Tabs` primitive is a controlled Radix Tabs component — see
          `src/components/ui/tabs.tsx`. Section content is grouped as
          specified in §6.2 (Main), §6.3 (Subagent), §6.4 (Subagent External).
          Sessions / Schedules / Activity are NOT inside the tab bar — they
          are reference surfaces (default-collapsed accordions below) so the
          primary tab bar is not crowded with non-editing affordances. */}
      <Tabs defaultValue="basics" className="w-full">
        <TabsList className="w-full justify-start overflow-x-auto">
          <TabsTrigger value="basics" data-testid="tab-basics" className="font-headline">Basics</TabsTrigger>
          <TabsTrigger value="personality" data-testid="tab-personality" className="font-headline">Personality</TabsTrigger>
          <TabsTrigger value="tools" data-testid="tab-tools" className="font-headline">Tools</TabsTrigger>
          {agent.type === 'subagent_3p' && (
            <TabsTrigger value="runtime" data-testid="tab-runtime" className="font-headline">Runtime</TabsTrigger>
          )}
          <TabsTrigger value="advanced" data-testid="tab-advanced" className="font-headline">Advanced</TabsTrigger>
        </TabsList>

        {/* ── BASICS TAB ─────────────────────────────────────────────────
            Identity (name/description/default toggle/delegation policy
            summary/avatar color/icon) + Model Configuration (model selector,
            fallback editor, sampling parameters) + Sandbox (per agent
            type: editable for custom, read-only for locked, hidden for
            native workers). The Executor (Spec-4) is a worker-only
            concern — for subagent_3p it is the headline of the Runtime
            tab below; for native workers (no external-cli selected) the
            whole thing is inherited from the caller so it is shown as a
            read-only summary in Advanced. */}
        <TabsContent value="basics" className="space-y-6">
          {/* Identity — always rendered; read-only for locked (core) agents */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Identity</p>
            <div className="space-y-3">
              <div className="space-y-2">
                <Input
                  data-testid="agent-name-input"
                  value={name}
                  disabled={isLocked}
                  readOnly={isLocked}
                  onChange={isLocked ? undefined : (e) => { markDirty(); setName(e.target.value) }}
                  placeholder="Agent name"
                  className="text-sm"
                />
                {canEdit && (
                  <Textarea
                    value={description}
                    onChange={(e) => { markDirty(); setDescription(e.target.value) }}
                    placeholder="Short description of this agent's purpose"
                    rows={2}
                    className="text-sm resize-none"
                  />
                )}
              </div>
              {/* W6-B4 / G3: Default agent toggle. Hidden for locked core
                  agents (locked roster: Mia is the seeded default and the
                  field is immutable for them). Hidden for workers — the
                  locked concept makes "default" a non-worker concept. */}
              {canEdit && !isWorkerAgent && (
                <div
                  data-testid="default-toggle-row"
                  className="flex items-center justify-between gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
                >
                  <div className="flex items-center gap-2 min-w-0">
                    <Star
                      size={14}
                      weight={isDefault ? 'fill' : 'regular'}
                      className={isDefault ? 'text-[var(--color-accent)] shrink-0' : 'text-[var(--color-muted)] shrink-0'}
                      aria-hidden="true"
                    />
                    <div className="min-w-0">
                      <p className="text-sm text-[var(--color-secondary)]">Default agent</p>
                      <p className="text-[11px] text-[var(--color-muted)] leading-snug">
                        Handles inbound messages with no more-specific routing rule. Only one agent is default at a time.
                      </p>
                    </div>
                  </div>
                  <Switch
                    data-testid="default-toggle"
                    checked={isDefault}
                    onCheckedChange={(v) => {
                      markDirty()
                      setIsDefault(v)
                      queryClient.invalidateQueries({ queryKey: ['agents'] })
                      addToast({
                        message: v
                          ? `${name || agent.name} is now the default agent`
                          : `${name || agent.name} is no longer the default agent`,
                        variant: 'success',
                      })
                    }}
                    aria-label={isDefault ? 'Unset as default agent' : 'Set as default agent'}
                  />
                </div>
              )}
              {canEdit && !isWorkerAgent && (() => {
                const dp = agent.delegation_policy
                const toCount = dp?.to?.length ?? 0
                const acceptFromCount = dp?.accept_from?.length ?? 0
                const modesCount = dp?.modes?.length ?? 0
                const hasDepth = typeof dp?.depth === 'number'
                const hasBudget = !!(dp?.budget && (dp.budget.max_cost_usd !== undefined || dp.budget.max_tokens !== undefined))
                const ruleCount = toCount + acceptFromCount + modesCount + (hasDepth ? 1 : 0) + (hasBudget ? 1 : 0)
                const rulesLabel = ruleCount === 1 ? '1 rule' : `${ruleCount} rules`
                return (
                  <div
                    data-testid="delegation-policy-summary"
                    className="flex items-center justify-between gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
                  >
                    <div className="min-w-0">
                      <p className="text-sm text-[var(--color-secondary)]">Delegation policy</p>
                      <p className="text-[11px] text-[var(--color-muted)] leading-snug">
                        {rulesLabel} configured. Edit on the Trust graph.
                      </p>
                    </div>
                    <Link
                      to="/agents/trust"
                      search={{ agent: resolvedAgentId }}
                      className="inline-flex items-center gap-1 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2.5 py-1 text-xs font-medium text-[var(--color-secondary)] transition-colors hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-primary)]"
                      data-testid="delegation-policy-link"
                      aria-label={`Edit delegation policy for ${agent.name}`}
                    >
                      Open Trust graph
                    </Link>
                  </div>
                )
              })()}
              {canEdit && (
                <div className="space-y-1.5">
                  <p className="text-xs text-[var(--color-muted)]">Avatar color</p>
                  <AvatarColorPicker
                    value={selectedColor ?? ''}
                    onChange={(color) => { markDirty(); setSelectedColor(color) }}
                    testIdPrefix="avatar-color"
                  />
                </div>
              )}
              {canEdit && (
                <div className="space-y-1.5">
                  <p className="text-xs text-[var(--color-muted)]">Avatar icon</p>
                  <IconPicker
                    value={selectedIcon}
                    onChange={(icon) => { markDirty(); setSelectedIcon(icon) }}
                    triggerTestId="avatar-icon-trigger"
                  />
                </div>
              )}
            </div>
          </section>

          <Separator />

          {/* Model Configuration — picker, unresolved-slug indicator, fallback editor */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Model</p>
            {providersError && (
              <p className="text-xs text-[var(--color-warning)]">
                Could not load providers. You can still enter a model slug manually.
              </p>
            )}
            <ModelSelector
              models={availableModels}
              value={model}
              onChange={(v) => { markDirty(); setModel(v) }}
              placeholder="Provider default"
              providerGroups={providerGroups}
              onUnknownModel={(m) => addToast({
                message: `"${m}" isn't listed by any connected provider — saving anyway, but the call may not work.`,
                variant: 'warning',
              })}
            />
            {primaryModelUnresolved && (
              <p
                data-testid="primary-model-unresolved"
                role="status"
                className="flex items-start gap-1.5 text-[11px] text-[var(--color-warning)] leading-snug"
              >
                <WarningCircle size={12} weight="fill" className="shrink-0 mt-0.5" aria-hidden="true" />
                <span>
                  Model not in any connected provider — calls will fail until you add a provider that supports this model.
                </span>
              </p>
            )}
            {isLocked && (
              <div className="space-y-1.5">
                <p className="text-xs text-[var(--color-muted)]">Fallback models</p>
                <div
                  data-testid="fallback-summary-locked"
                  className="space-y-2 p-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]"
                >
                  <div className="flex items-center gap-2 text-[var(--color-muted)]">
                    <Lock size={12} weight="fill" aria-hidden="true" />
                    <p className="text-[11px]">
                      Locked: fallback models are inherited from the locked core config.
                    </p>
                  </div>
                  {fallbackModels.length === 0 ? (
                    <p className="text-xs text-[var(--color-muted)]">No fallback chain configured.</p>
                  ) : (
                    <ol className="space-y-1" data-testid="fallback-summary-locked-list">
                      {fallbackModels.map((entry, idx) => (
                        <li
                          key={entry.model}
                          className="flex items-center gap-2 text-xs font-mono text-[var(--color-secondary)]"
                        >
                          <span className="text-[var(--color-muted)] w-4 shrink-0 text-right">{idx + 1}.</span>
                          <span
                            data-testid={`fallback-summary-provider-${entry.model}`}
                            className="inline-flex items-center px-1.5 rounded text-[10px] font-semibold"
                            style={{
                              backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                              color: 'var(--color-accent)',
                              border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                            }}
                          >
                            {entry.provider || '—'}
                          </span>
                          <span data-testid={`fallback-summary-model-${entry.model}`}>{entry.model}</span>
                        </li>
                      ))}
                    </ol>
                  )}
                </div>
              </div>
            )}
            {/* Sampling parameters — collapsed disclosure */}
            {canEdit && (
              <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
                <button
                  type="button"
                  onClick={() => setAdvancedOpen((o) => !o)}
                  className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
                >
                  <span>Sampling parameters</span>
                  {advancedOpen ? <CaretUp size={13} /> : <CaretDown size={13} />}
                </button>
                {advancedOpen && (
                  <div className="px-3 pb-3 space-y-4 border-t border-[var(--color-border)]">
                    <RangeField
                      label="Temperature"
                      caption="Higher = more creative / less predictable (0–2, default 1)"
                      value={temperature}
                      min={0}
                      max={2}
                      step={0.05}
                      onChange={(v) => { markDirty(); setTemperature(v) }}
                      format={(v) => v.toFixed(2)}
                    />
                    <RangeField
                      label="Max tokens"
                      caption="Maximum length of each reply"
                      value={maxTokens}
                      min={256}
                      max={32768}
                      step={256}
                      onChange={(v) => { markDirty(); setMaxTokens(v) }}
                      format={(v) => v.toLocaleString()}
                    />
                    <RangeField
                      label="Top P"
                      caption="Nucleus sampling mass — 1.0 disables it (default 1)"
                      value={topP}
                      min={0}
                      max={1}
                      step={0.01}
                      onChange={(v) => { markDirty(); setTopP(v) }}
                      format={(v) => v.toFixed(2)}
                    />
                  </div>
                )}
              </div>
            )}
          </section>

          {/* Sandbox — editable for custom agents, read-only for locked core
              agents, hidden for native workers (delegation-only labour
              agents; sandbox is inherited from the caller). */}
          {!isNativeWorkerAgent && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Sandbox</p>
                {(sandboxProfile === 'workspace+net' || sandboxProfile === 'off') && (
                  <span
                    data-testid="sandbox-accordion-widening-badge"
                    className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-amber-500/20 text-amber-400 border border-amber-500/40"
                  >
                    Widened
                  </span>
                )}
                <SandboxInfoTooltip />
              </div>
              {isLocked ? (
                <div className="space-y-3">
                  <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3">
                    <p className="text-[10px] uppercase tracking-wider text-[var(--color-muted)] mb-1">
                      Profile
                    </p>
                    <p className="text-sm font-medium text-[var(--color-secondary)]">
                      {sandboxProfile
                        ? `${formatSandboxProfileLabel(sandboxProfile)} (built-in, locked)`
                        : 'Built-in (locked)'}
                    </p>
                    <p className="text-xs text-[var(--color-muted)] mt-2">
                      Locked core agents use a built-in sandbox profile that cannot be changed from
                      the UI. To adjust the global default for new custom agents, see{' '}
                      <strong>Settings → Security</strong>.
                    </p>
                  </div>
                  {shellDenyPatterns.length > 0 && (
                    <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3">
                      <p className="text-[10px] uppercase tracking-wider text-[var(--color-muted)] mb-1.5">
                        Shell deny patterns
                      </p>
                      <ul className="space-y-1">
                        {shellDenyPatterns.map((p, i) => (
                          <li key={i} className="font-mono text-xs text-[var(--color-secondary)]">
                            {p}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>
              ) : (
                <div className="space-y-4">
                  {isWorkerAgent && executor?.kind === 'external-cli' && sandboxProfile && sandboxProfile !== 'off' && (
                    <div
                      data-testid="sandbox-external-cli-ignored-callout"
                      role="note"
                      aria-live="polite"
                      className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2.5"
                    >
                      <WarningCircle size={14} weight="fill" className="text-amber-400 shrink-0 mt-0.5" aria-hidden="true" />
                      <p className="text-[11px] text-amber-200 leading-snug">
                        Sandbox profile is ignored when executor.kind=external-cli.
                        The external CLI manages its own isolation.
                      </p>
                    </div>
                  )}
                  <SandboxProfileSelector
                    value={sandboxProfile}
                    agentName={name || agent.name}
                    godModeAvailable={appState?.god_mode_available ?? false}
                    godModeOptedIn={appState?.god_mode_opted_in ?? false}
                    onChange={(p) => { markDirty(); setSandboxProfile(p) }}
                  />
                  <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
                    <button
                      type="button"
                      onClick={() => setShellAdvancedOpen((o) => !o)}
                      className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
                      aria-expanded={shellAdvancedOpen}
                    >
                      <span className="text-xs">Shell deny patterns</span>
                      {shellAdvancedOpen ? <CaretUp size={13} /> : <CaretDown size={13} />}
                    </button>
                    {shellAdvancedOpen && (
                      <div className="px-3 pb-3 border-t border-[var(--color-border)]">
                        <ShellDenyPatternsEditor
                          value={shellDenyPatterns}
                          onChange={(patterns) => { markDirty(); setShellDenyPatterns(patterns) }}
                        />
                      </div>
                    )}
                  </div>
                </div>
              )}
            </section>
          )}
        </TabsContent>

        {/* ── PERSONALITY TAB ────────────────────────────────────────────
            BehaviorFields (SOUL.md / Task prompt + Additional Instructions
            + Voice), and the Heartbeat sub-block for base agents (workers
            are delegation-only labour agents and never run on a schedule).
            The Execution params (timeout / max_iter / steering) live in
            the Advanced tab per the spec matrix. */}
        <TabsContent value="personality" className="space-y-5">
          <BehaviorFields
            isWorker={isWorkerAgent}
            soul={soul}
            setSoul={(v) => { markDirty(); setSoul(v) }}
            instructions={instructions}
            setInstructions={(v) => { markDirty(); setInstructions(v) }}
            voice={voice}
            setVoice={(v) => { markDirty(); setVoice(v) }}
            renderUploadButton={(_, onUpload) => <UploadButton onUpload={onUpload} />}
          />

          {/* Heartbeat — base-only. Workers never run on a schedule. */}
          {!isWorkerAgent && (
            <>
              <Separator />
              <div className="space-y-2">
                <p className="text-xs font-medium text-[var(--color-secondary)]">Background tasks / periodic instructions</p>
                <p className="text-xs text-[var(--color-muted)]">
                  Instructions the agent runs on a recurring schedule — check queues, summarize,
                  or perform any background work. Stored as <span className="font-mono text-[11px]">HEARTBEAT.md</span>.
                </p>
                <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 space-y-3">
                  <div className="flex items-center justify-between">
                    <div>
                      <p className="text-sm text-[var(--color-secondary)]">Enable heartbeat</p>
                      <p className="text-xs text-[var(--color-muted)]">Run on a recurring schedule</p>
                    </div>
                    <Switch
                      checked={heartbeatEnabled}
                      onCheckedChange={(v) => { markDirty(); setHeartbeatEnabled(v) }}
                    />
                  </div>
                  {heartbeatEnabled && (
                    <div className="flex items-center gap-3 pt-1 border-t border-[var(--color-border)]">
                      <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">Interval (seconds)</label>
                      <Input
                        type="number"
                        min={1}
                        value={heartbeatInterval}
                        onChange={(e) => { markDirty(); setHeartbeatInterval(Number(e.target.value)) }}
                        className="text-xs h-8"
                      />
                    </div>
                  )}
                </div>
                <Textarea
                  value={heartbeat}
                  onChange={(e) => { markDirty(); setHeartbeat(e.target.value) }}
                  placeholder="# Heartbeat&#10;&#10;Write persistent context for this agent..."
                  rows={4}
                  className="text-xs font-mono resize-none"
                />
                <UploadButton onUpload={setHeartbeat} />
              </div>
            </>
          )}
        </TabsContent>

        {/* ── TOOLS TAB ─────────────────────────────────────────────────
            Tool policy editor + Skills picker. Native workers (no
            user-added overrides) collapse the editor to a read-only
            summary; external-cli workers see the full editor. The
            fallback models editor stays here too — FR-007 says fallbacks
            are part of the tool chain. */}
        <TabsContent value="tools" className="space-y-6">
          {/* Fallback models */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Fallback models</p>
            <p className="text-xs text-[var(--color-muted)]">Tried in order if the primary model fails.</p>
            {isLocked ? (
              <div
                data-testid="fallback-summary-locked"
                className="space-y-2 p-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]"
              >
                <div className="flex items-center gap-2 text-[var(--color-muted)]">
                  <Lock size={12} weight="fill" aria-hidden="true" />
                  <p className="text-[11px]">
                    Locked: fallback models are inherited from the locked core config.
                  </p>
                </div>
                {fallbackModels.length === 0 ? (
                  <p className="text-xs text-[var(--color-muted)]">No fallback chain configured.</p>
                ) : (
                  <ol className="space-y-1" data-testid="fallback-summary-locked-list">
                    {fallbackModels.map((entry, idx) => (
                      <li
                        key={entry.model}
                        className="flex items-center gap-2 text-xs font-mono text-[var(--color-secondary)]"
                      >
                        <span className="text-[var(--color-muted)] w-4 shrink-0 text-right">{idx + 1}.</span>
                        <span
                          data-testid={`fallback-summary-provider-${entry.model}`}
                          className="inline-flex items-center px-1.5 rounded text-[10px] font-semibold"
                          style={{
                            backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                            color: 'var(--color-accent)',
                            border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                          }}
                        >
                          {entry.provider || '—'}
                        </span>
                        <span data-testid={`fallback-summary-model-${entry.model}`}>{entry.model}</span>
                      </li>
                    ))}
                  </ol>
                )}
              </div>
            ) : (
              <div className="flex flex-wrap gap-1.5 p-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] min-h-[36px]">
                {fallbackModels.map((entry, idx) => {
                  const providerMissing = entry.provider === ''
                  const providerLabel = providerMissing
                    ? '—'
                    : (connectedProviders.find((p) => p.id === entry.provider)?.display_name
                        ?? connectedProviders.find((p) => p.id === entry.provider)?.name
                        ?? entry.provider)
                  return (
                    <span
                      key={entry.model}
                      data-testid={`fallback-chip-model-${entry.model}`}
                      className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-mono bg-[var(--color-surface-2)] text-[var(--color-secondary)] border border-[var(--color-border)]"
                    >
                      <span
                        data-testid={`fallback-chip-provider-${entry.model}`}
                        className="inline-flex items-center px-1 rounded text-[9px] font-semibold"
                        style={{
                          backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                          color: 'var(--color-accent)',
                          border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                        }}
                      >
                        {providerLabel}
                      </span>
                      <span className="relative inline-block">
                        <select
                          data-testid={`fallback-provider-select-${entry.model}`}
                          aria-label={`Provider for fallback ${entry.model}`}
                          value={entry.provider}
                          onChange={(e) => { markDirty(); setFallbackProvider(entry.model, e.target.value) }}
                          className="appearance-none bg-transparent text-[var(--color-muted)] hover:text-[var(--color-secondary)] pl-1 pr-3 py-0 text-[9px] focus:outline-none focus-visible:ring-1 focus-visible:ring-[var(--color-accent)] rounded cursor-pointer"
                        >
                          <option value="" data-testid={`fallback-provider-option-empty-${entry.model}`}>—</option>
                          {connectedProviders.map((p) => (
                            <option
                              key={p.id}
                              value={p.id}
                              data-testid={`fallback-provider-option-${p.id}-${entry.model}`}
                            >
                              {p.display_name ?? p.name ?? p.id}
                            </option>
                          ))}
                        </select>
                        <CaretDown
                          size={9}
                          className="pointer-events-none absolute right-0.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)]"
                          aria-hidden="true"
                        />
                      </span>
                      <span>{entry.model}</span>
                      <button
                        type="button"
                        data-testid={`fallback-chip-up-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} up`}
                        disabled={idx === 0}
                        onClick={() => moveFallback(entry.model, -1)}
                        className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:text-[var(--color-muted)]"
                      >
                        <ArrowUp size={10} />
                      </button>
                      <button
                        type="button"
                        data-testid={`fallback-chip-down-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} down`}
                        disabled={idx === fallbackModels.length - 1}
                        onClick={() => moveFallback(entry.model, 1)}
                        className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:text-[var(--color-muted)]"
                      >
                        <ArrowDown size={10} />
                      </button>
                      <button
                        type="button"
                        data-testid={`fallback-chip-remove-${entry.model}`}
                        aria-label={`Remove fallback ${entry.model}`}
                        onClick={() => removeFallback(entry.model)}
                        className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
                      >
                        <X size={10} />
                      </button>
                      {providerMissing && (
                        <span
                          data-testid={`fallback-chip-warning-${entry.model}`}
                          role="img"
                          aria-label="Provider not connected — fallback will not be used at runtime"
                          title="Provider not connected — fallback will not be used at runtime"
                          className="inline-flex items-center text-amber-400"
                        >
                          <Warning size={11} weight="fill" />
                        </span>
                      )}
                    </span>
                  )
                })}
                <div className="min-w-[220px] flex-1">
                  <ModelSelector
                    models={availableModels}
                    value=""
                    onChange={(v) => { markDirty(); addFallbackFromSelector(v) }}
                    placeholder="Add fallback…"
                    providerGroups={providerGroups}
                    triggerTestId="fallback-add-trigger"
                    itemTestIdPrefix="fallback-add-item-"
                    disabled={availableModels.length === 0 && (!providerGroups || providerGroups.every((g) => g.models.length === 0))}
                  />
                </div>
              </div>
            )}
          </section>

          {/* Tools & Permissions */}
          {(!isNativeWorkerAgent || Object.keys(toolsCfg.builtin?.policies ?? {}).length > 0) && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Tools &amp; Permissions</p>
                {(() => {
                  const overrideCount = Object.keys(toolsCfg.builtin?.policies ?? {}).length
                  if (overrideCount === 0) return null
                  return (
                    <span className="text-xs text-[var(--color-muted)] font-normal">
                      {overrideCount} overrides
                    </span>
                  )
                })()}
              </div>
              {(() => {
                const overrideCount = Object.keys(toolsCfg.builtin?.policies ?? {}).length
                const collapseToReadOnly = isNativeWorkerAgent && overrideCount === 0
                if (!collapseToReadOnly) {
                  return (
                    <ToolsAndPermissions
                      agentId={agentId}
                      agentType={agent.type}
                      isLocked={isLocked}
                      tools={toolsCfg}
                      onChange={setToolsCfg}
                    />
                  )
                }
                return (
                  <div
                    data-testid="native-worker-tools-readonly"
                    className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
                  >
                    <p className="text-sm text-[var(--color-secondary)]">
                      Built-in tool policies (read-only)
                    </p>
                    <p className="text-[11px] text-[var(--color-muted)] leading-snug mt-0.5">
                      Native workers run with a compiled allow/deny rail — the seeded
                      system and memory entries cannot be edited from the UI. Add
                      explicit overrides only if your task requires non-default behaviour;
                      otherwise leave this empty to inherit the inherited rail.
                    </p>
                  </div>
                )
              })()}
            </section>
          )}

          {/* Skills — hidden for native workers (M11). */}
          {!isNativeWorkerAgent && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Skills</p>
                {agentSkills.length > 0 && (
                  <span className="text-xs text-[var(--color-muted)] font-normal">
                    {agentSkills.length} granted
                  </span>
                )}
              </div>
              <div className="space-y-3">
                {isLocked ? (
                  <p className="text-xs text-[var(--color-muted)]">
                    Skill assignment is read-only for locked core agents.
                  </p>
                ) : (
                  <p className="text-xs text-[var(--color-muted)]">
                    Grant specific installed skills to this agent. Only skills listed here
                    are available during this agent's runs. Empty means no skills.
                  </p>
                )}
                {availableSkills.length === 0 ? (
                  <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-4 text-center">
                    <Sparkle size={16} className="text-[var(--color-muted)] mx-auto mb-1.5" />
                    <p className="text-xs text-[var(--color-muted)]">No skills installed.</p>
                    <p className="text-xs text-[var(--color-muted)]/70 mt-0.5">
                      Install skills from the Skills &amp; Tools screen.
                    </p>
                  </div>
                ) : (
                  <div className="space-y-1.5">
                    {availableSkills.map((skill) => {
                      const granted = agentSkills.includes(skill.id)
                      return (
                        <label
                          key={skill.id}
                          className={`flex items-start gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5 transition-colors ${isLocked ? 'cursor-not-allowed opacity-60' : 'cursor-pointer hover:bg-[var(--color-surface-2)]'}`}
                        >
                          <input
                            type="checkbox"
                            checked={granted}
                            disabled={isLocked}
                            onChange={isLocked ? undefined : (e) => {
                              markDirty()
                              if (e.target.checked) {
                                setAgentSkills((prev) => [...prev, skill.id])
                              } else {
                                setAgentSkills((prev) => prev.filter((s) => s !== skill.id))
                              }
                            }}
                            className="mt-0.5 shrink-0 accent-[var(--color-accent)] disabled:opacity-50"
                            data-testid={`skill-checkbox-${skill.id}`}
                          />
                          <div className="min-w-0">
                            <p className="text-sm font-medium text-[var(--color-secondary)] leading-tight">
                              {skill.name}
                            </p>
                            {skill.description && (
                              <p className="text-[11px] text-[var(--color-muted)] mt-0.5 leading-snug">
                                {skill.description}
                              </p>
                            )}
                            <div className="flex items-center gap-2 mt-1">
                              <span className="text-[10px] font-mono text-[var(--color-muted)]/70">
                                {skill.id}
                              </span>
                              {skill.verified && (
                                <span className="text-[9px] px-1 rounded bg-emerald-500/20 text-emerald-400 border border-emerald-500/30">
                                  verified
                                </span>
                              )}
                            </div>
                          </div>
                        </label>
                      )
                    })}
                  </div>
                )}
              </div>
            </section>
          )}
        </TabsContent>

        {/* ── RUNTIME TAB (subagent_3p only) ─────────────────────────────
            Spec-4 / §6.4: the Runtime tab is rendered for
            `subagent_3p` agents only (external CLI workers). The CLI
            itself is shown as a read-only chip (locked concept — the
            runtime kind is a property of the agent, not editable in
            v0.1.0), while cli_path / env_overrides / cli_args are the
            operator-tunable inputs (F-14). */}
        {agent.type === 'subagent_3p' && (
          <TabsContent value="runtime" className="space-y-5">
            <section className="space-y-3">
              <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Runtime</p>
              {/* CLI — read-only badge. The kind+cli tuple is the agent's
                  defining property; the operator can change which CLI is
                  used by recreating the agent (post v0.3 the wizard will
                  surface this, per the spec matrix). */}
              <div
                data-testid="profile-cli-locked"
                className="flex items-center gap-2 px-3 py-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]"
              >
                <span className="text-[10px] uppercase tracking-wider text-[var(--color-muted)]">CLI</span>
                <span className="font-mono text-xs text-[var(--color-secondary)]">
                  {executor?.cli ?? 'claude-code'}
                </span>
                <span className="text-[10px] text-[var(--color-muted)]">(locked)</span>
              </div>
              <div
                data-testid="profile-cli-path"
                className="flex items-center gap-3"
              >
                <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">CLI path</label>
                <Input
                  value={executor?.cli_path ?? ''}
                  onChange={(e) => {
                    markDirty()
                    setExecutor((prev) => ({ ...(prev ?? { kind: 'external-cli', cli: executor?.cli ?? 'claude-code' }), cli_path: e.target.value }))
                  }}
                  placeholder="/usr/local/bin/claude"
                  className="text-xs h-8 font-mono"
                  disabled={isLocked}
                />
              </div>
              <div data-testid="profile-env-overrides" className="space-y-2">
                <label className="text-xs text-[var(--color-muted)]">Environment overrides</label>
                <p className="text-[11px] text-[var(--color-muted)] leading-snug">
                  KEY=value pairs passed to the CLI process. Empty means no overrides.
                </p>
                <EnvironmentOverridesEditor
                  value={executor?.env_overrides ?? {}}
                  onChange={(next) => {
                    markDirty()
                    setExecutor((prev) => ({
                      ...(prev ?? { kind: 'external-cli', cli: executor?.cli ?? 'claude-code' }),
                      env_overrides: next,
                    }))
                  }}
                  disabled={isLocked}
                />
              </div>
              <div data-testid="profile-cli-args" className="flex items-center gap-3">
                <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">CLI arguments</label>
                <Input
                  value={executor?.cli_args ?? ''}
                  onChange={(e) => {
                    markDirty()
                    setExecutor((prev) => ({ ...(prev ?? { kind: 'external-cli', cli: executor?.cli ?? 'claude-code' }), cli_args: e.target.value }))
                  }}
                  placeholder="--no-update-check"
                  className="text-xs h-8 font-mono"
                  disabled={isLocked}
                />
              </div>
            </section>
          </TabsContent>
        )}

        {/* ── ADVANCED TAB ──────────────────────────────────────────────
            Rate limits, Execution params (timeout / max_iter / steering —
            Main only per the spec matrix), Executor summary (workers
            only; subagent_3p gets the full editor in the Runtime tab),
            Sessions, Schedules (base-only), Activity. The Executor
            here is a compact summary for native workers; subagent_3p's
            editor is in Runtime. */}
        <TabsContent value="advanced" className="space-y-6">
          {/* Rate Limits — editable for unlocked agents, hidden for locked. */}
          {!isLocked && (
            <section className="space-y-3">
              <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Rate Limits</p>
              <div className="space-y-3">
                <div className="flex items-center justify-between py-1">
                  <div>
                    <p className="text-sm text-[var(--color-secondary)]">Use global defaults</p>
                    <p className="text-xs text-[var(--color-muted)]">Inherit rate limits from global settings</p>
                  </div>
                  <Switch
                    checked={useGlobalRateLimits}
                    onCheckedChange={(v) => { markDirty(); setUseGlobalRateLimits(v) }}
                    disabled={!canEdit}
                  />
                </div>
                {!useGlobalRateLimits && (
                  <div className="space-y-2">
                    <div className="flex items-center gap-3">
                      <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">LLM calls / hour</label>
                      <Input
                        type="number"
                        min={0}
                        value={maxLlmCallsPerHour}
                        onChange={(e) => { markDirty(); setMaxLlmCallsPerHour(e.target.value === '' ? '' : Number(e.target.value)) }}
                        placeholder="Unlimited"
                        className="text-xs h-8"
                        disabled={!canEdit}
                      />
                    </div>
                    <div className="flex items-center gap-3">
                      <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">Tool calls / minute</label>
                      <Input
                        type="number"
                        min={0}
                        value={maxToolCallsPerMinute}
                        onChange={(e) => { markDirty(); setMaxToolCallsPerMinute(e.target.value === '' ? '' : Number(e.target.value)) }}
                        placeholder="Unlimited"
                        className="text-xs h-8"
                        disabled={!canEdit}
                      />
                    </div>
                    <div className="flex items-center gap-3">
                      <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">Max cost / day ($)</label>
                      <Input
                        type="number"
                        min={0}
                        step={0.01}
                        value={maxCostPerDay}
                        onChange={(e) => { markDirty(); setMaxCostPerDay(e.target.value === '' ? '' : Number(e.target.value)) }}
                        placeholder="Unlimited"
                        className="text-xs h-8"
                        disabled={!canEdit}
                      />
                    </div>
                  </div>
                )}
              </div>
            </section>
          )}

          {/* Execution — base agents and external-cli workers. Locked core
              agents skip this sub-block (they run on a built-in policy). */}
          {!isLocked && (
            <section className="space-y-3">
              <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Execution</p>
              <div className="space-y-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4">
                <div className="flex items-center gap-3">
                  <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                    Turn timeout
                    <span className="block text-[10px] text-[var(--color-muted)]/70">
                      Max seconds per turn. 0 = no limit.
                    </span>
                  </label>
                  <Input
                    type="number"
                    min={0}
                    value={timeoutSeconds}
                    onChange={(e) => { markDirty(); setTimeoutSeconds(Number(e.target.value)) }}
                    className="text-xs h-8"
                  />
                </div>
                <div className="flex items-center gap-3">
                  <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                    Max tool calls per turn
                    <span className="block text-[10px] text-[var(--color-muted)]/70">
                      Stops runaway loops. Default: 50.
                    </span>
                  </label>
                  <Input
                    type="number"
                    min={1}
                    value={maxToolIterations}
                    onChange={(e) => { markDirty(); setMaxToolIterations(Number(e.target.value)) }}
                    className="text-xs h-8"
                  />
                </div>
                {/* Steering mode — Main only. Workers and subagent_3p do
                    not have a chat surface that consumes concurrent
                    messages, so steering is a Main concept. */}
                {!isWorkerAgent && (
                  <div className="flex items-center gap-3">
                    <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                      Message handling
                      <span className="block text-[10px] text-[var(--color-muted)]/70">
                        How concurrent messages are processed.
                      </span>
                    </label>
                    <SmartSelect
                      value={steeringMode}
                      onValueChange={(v) => { markDirty(); setSteeringMode(v as 'one-at-a-time' | 'queue-and-process') }}
                      triggerClassName="text-xs h-8"
                      items={[
                        { value: 'one-at-a-time', label: 'One at a time' },
                        { value: 'parallel', label: 'Parallel' },
                        { value: 'queue', label: 'Queue' },
                      ]}
                    />
                  </div>
                )}
              </div>
            </section>
          )}

          {/* Executor summary — all workers (base + external). subagent_3p's
              full editor is in the Runtime tab. Locked core workers are
              handled by their locked-banner and field-level disable. The
              selector itself shows "native" for native workers; the
              native-worker-delegation-callout at the top of the body
              already explains why the editable Tools / Skills / Sandbox
              accordions are collapsed to a summary. */}
          {isWorkerAgent && agent.type !== 'subagent_3p' && (
            <section className="space-y-3">
              <div className="flex items-center gap-2">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Executor</p>
                {(executor?.kind === 'external-cli' || executor?.kind === 'remote-a2a') && (
                  <span className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-[var(--color-surface-3)] text-[var(--color-muted)] border border-[var(--color-border)]">
                    {executor.kind === 'external-cli' ? (executor.cli ?? 'external') : 'A2A'}
                  </span>
                )}
                {(!executor || executor.kind === 'native') && (
                  <span
                    data-testid="executor-native-badge"
                    className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-[var(--color-surface-3)] text-[var(--color-muted)] border border-[var(--color-border)]"
                  >
                    Native (in-process)
                  </span>
                )}
              </div>
              <ExecutorSelector
                value={executor}
                agentId={resolvedAgentId}
                disabled={isLocked}
                onChange={(next) => { markDirty(); setExecutor(next) }}
              />
            </section>
          )}

          {/* Sessions */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Sessions</p>
            {sessionsError ? (
              <p className="text-sm text-[var(--color-error)]">Failed to load sessions</p>
            ) : recentSessions.length > 0 ? (
              <div className="space-y-1">
                {recentSessions.map((s) => (
                  <SessionRow key={s.id} session={s} />
                ))}
              </div>
            ) : (
              <p className="text-xs text-[var(--color-muted)]">No sessions yet.</p>
            )}
          </section>

          {/* Schedules — base-only. Workers are delegation-only labour
              agents and never own a schedule. */}
          {!isWorkerAgent && (
            <section className="space-y-3">
              <div className="flex items-center justify-between">
                <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Schedules</p>
                <button
                  type="button"
                  onClick={() => setCreatingSchedule(true)}
                  className="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs font-medium text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors"
                >
                  <Plus size={13} />
                  New schedule
                </button>
              </div>
              <SchedulesList agentId={resolvedAgentId} />
            </section>
          )}

          {/* Activity */}
          <section className="space-y-3">
            <p className="font-headline font-semibold text-[14px] text-[var(--color-secondary)]">Activity</p>
            {agent.stats && (
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                <StatCard label="Sessions" value={agent.stats.total_sessions.toString()} />
                <StatCard
                  label="Tokens"
                  value={
                    agent.stats.total_tokens >= 1000
                      ? `${(agent.stats.total_tokens / 1000).toFixed(1)}k`
                      : agent.stats.total_tokens.toString()
                  }
                />
                <StatCard label="Cost" value={`$${agent.stats.total_cost.toFixed(4)}`} />
                <StatCard
                  label="Last active"
                  value={
                    agent.stats.last_active
                      ? new Date(agent.stats.last_active).toLocaleDateString()
                      : '—'
                  }
                />
              </div>
            )}
            {activityError ? (
              <p className="text-sm text-[var(--color-error)]">Failed to load activity</p>
            ) : recentActivity.length === 0 ? (
              <p className="text-xs text-[var(--color-muted)]">No recent activity for this agent.</p>
            ) : (
              <div className="space-y-1">
                {recentActivity.map((event) => (
                  <ActivityRow key={event.id} event={event} />
                ))}
              </div>
            )}
          </section>
        </TabsContent>
      </Tabs>
      </div>
      </div>

      {/* Sticky footer — Wave 5 / spec §6.1: the footer carries the auto-save
          indicator (left, data-testid="last-saved-indicator") and a
          destructive Delete button (right, data-testid="delete-agent-button").
          Per spec there is NO Apply button (autosave-only) and no separate
          Close button — the Radix X in the top-right corner is the dismiss
          affordance, and SheetContent's onOpenChange wires it back here. */}
      <div className="px-8 py-4 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] shrink-0 flex items-center justify-between gap-3">
        <div data-testid="last-saved-indicator">
          <AutoSaveIndicator status={saveStatus} error={saveError} />
        </div>
        {!isLocked && (
          <Button
            variant="destructive"
            data-testid="delete-agent-button"
            onClick={() => setDeleteOpen(true)}
            className="ml-auto"
          >
            <Trash size={13} className="mr-1.5" />
            Delete agent
          </Button>
        )}
      </div>

      {/* Wave 5 / spec §6.1 BDD #15: Delete confirmation dialog. Mirrors the
          SchedulesList pattern (`AlertDialog` + `AlertDialogAction`) so the
          destructive-confirm flow is identical across the app. The confirm
          fires the deleteAgentMutation; on success the slide-over closes
          and the agent is removed from the list cache. */}
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {formData.name || agent.name}?</AlertDialogTitle>
            <AlertDialogDescription>This cannot be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteAgentMutation.isPending}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleteAgentMutation.isPending}
              onClick={() => {
                if (agentId) deleteAgentMutation.mutate(agentId)
              }}
            >
              {deleteAgentMutation.isPending ? 'Deleting…' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* New schedule slide-over — owner pre-filled to this agent (#264) */}
      {creatingSchedule && (
        <ScheduleFormSheet
          open={true}
          defaultOwnerAgentId={resolvedAgentId}
          onOpenChange={(open) => {
            if (!open) setCreatingSchedule(false)
          }}
        />
      )}
    </ProfileSheet>
  )
}

/** Local wrapper for the slide-over primitives. Keeps the three call sites
 *  (loading / not-found / main) in sync on `open`, `onOpenChange`, side, and
 *  the column layout that lets the body and footer stick inside the sheet. */
function ProfileSheet({
  isOpen,
  onClose,
  title,
  /** W6-B1 / I7: forwarded to SheetContent's onOpenAutoFocus so the parent
   *  can capture the trigger element before Radix shifts focus. Used by
   *  AgentProfile to restore focus to the AgentCard on close. */
  onOpenAutoFocus,
  children,
}: {
  isOpen: boolean
  onClose: () => void
  /**
   * Accessible title for screen readers. Rendered as a visually-hidden
   * SheetTitle so Radix Dialog's aria-labelledby points at something
   * meaningful — the visible h1 in the body is a sibling, not the
   * Dialog.Title, so without this sr-only label the dialog opens
   * with no announced name.
   */
  title: string
  onOpenAutoFocus?: (event: Event) => void
  children: React.ReactNode
}) {
  return (
    <Sheet open={isOpen} onOpenChange={(o) => { if (!o) onClose() }}>
      {/* W6-B1 / I2: dropped the `widthClass="w-full sm:max-w-3xl"` literal so
          this picks up sheet.tsx's right-side default (`w-[90vw] sm:max-w-2xl`,
          i.e. 90vw on mobile, 672 px on desktop). Caps the Edit slide-over at
          ~32rem/90vw instead of 768 px / 47% of a 1440 viewport. */}
      <SheetContent
        side="right"
        className="flex flex-col gap-0 p-0"
        onOpenAutoFocus={onOpenAutoFocus}
      >
        <SheetTitle className="sr-only">{title}</SheetTitle>
        {children}
      </SheetContent>
    </Sheet>
  )
}

function SandboxInfoTooltip() {
  const [visible, setVisible] = useState(false)
  return (
    <span className="relative inline-block">
      {/* W6-A1 / I6: 44x44 px tap target (WCAG 2.5.8 AA, token
          --spacing-tap-target-min). The Info icon is rendered at 13 px
          but centered inside a 44 px button so the touch surface meets
          the AA minimum without growing the visual glyph. */}
      <button
        type="button"
        className="min-h-tap-target-min min-w-tap-target-min flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-surface-1)]"
        onMouseEnter={() => setVisible(true)}
        onMouseLeave={() => setVisible(false)}
        onFocus={() => setVisible(true)}
        onBlur={() => setVisible(false)}
        tabIndex={0}
        aria-label="Sandbox profile information"
        onClick={(e) => e.stopPropagation()}
      >
        <Info size={13} />
      </button>
      {visible && (
        <span
          role="tooltip"
          className="absolute left-0 top-full mt-1 z-50 w-80 rounded border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-[10px] text-[var(--color-muted)] shadow-lg pointer-events-none whitespace-normal font-normal"
        >
          <strong className="text-[var(--color-secondary)] block mb-1">Sandbox profiles</strong>
          Profiles set the kernel-level security boundary for this agent.{' '}
          <strong>none</strong> inherits the global default.{' '}
          <strong>workspace</strong> and <strong>workspace+net</strong> restrict file access to the agent&apos;s working directory.{' '}
          <strong>host</strong> applies full Landlock enforcement on the host filesystem.{' '}
          <strong>off</strong> removes all kernel isolation — only available when the gateway is started with --allow-god-mode.
        </span>
      )}
    </span>
  )
}

/** W6-C1 / G5: friendly label for a SandboxProfile wire value. Shared by
 *  the editable branch (SandboxProfileSelector's selected chip) and the
 *  locked branch (read-only summary), so the two surfaces never disagree
 *  on how to spell "workspace+net". The wire enum is `workspace`,
 *  `workspace+net`, `host`, `off`; "none" is the UI-only sentinel that
 *  means "inherit global default" (stripped from the wire payload before
 *  PUT — see `formData`). */
function formatSandboxProfileLabel(profile: string): string {
  if (profile === 'workspace+net') return 'Workspace + Net'
  return profile.charAt(0).toUpperCase() + profile.slice(1)
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 text-center">
      <div className="font-headline font-bold text-base text-[var(--color-secondary)]">{value}</div>
      <div className="text-xs text-[var(--color-muted)] mt-0.5">{label}</div>
    </div>
  )
}

function SessionRow({ session }: { session: AgentSession }) {
  const date = new Date(session.updated_at ?? session.created_at)
  return (
    <div className="flex items-center justify-between px-3 py-2 rounded-md hover:bg-[var(--color-surface-1)] transition-colors">
      <span className="text-xs text-[var(--color-secondary)] truncate flex-1 min-w-0 mr-3">
        {session.title || 'Untitled session'}
      </span>
      <span className="text-[10px] text-[var(--color-muted)] shrink-0">
        {date.toLocaleDateString()}
      </span>
    </div>
  )
}

interface RangeFieldProps {
  label: string
  /** #335: plain language caption shown below the label */
  caption?: string
  value: number
  min: number
  max: number
  step: number
  onChange: (v: number) => void
  format: (v: number) => string
}

function RangeField({ label, caption, value, min, max, step, onChange, format }: RangeFieldProps) {
  return (
    <div className="space-y-1 pt-3">
      <div className="flex items-center justify-between">
        <div>
          <span className="text-xs text-[var(--color-muted)]">{label}</span>
          {caption && (
            <p className="text-[10px] text-[var(--color-muted)]/70 leading-snug mt-0.5">{caption}</p>
          )}
        </div>
        <span className="text-xs font-mono text-[var(--color-secondary)]">{format(value)}</span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full h-1.5 rounded-full appearance-none cursor-pointer"
        style={{
          background: `linear-gradient(to right, var(--color-accent) 0%, var(--color-accent) ${((value - min) / (max - min)) * 100}%, var(--color-border) ${((value - min) / (max - min)) * 100}%, var(--color-border) 100%)`,
        }}
      />
    </div>
  )
}

function ActivityRow({ event }: { event: ActivityEvent }) {
  const date = new Date(event.timestamp)
  return (
    <div className="flex items-start gap-3 px-3 py-2 rounded-md hover:bg-[var(--color-surface-1)] transition-colors">
      <span className="text-xs text-[var(--color-secondary)] flex-1 min-w-0 truncate">
        {event.summary}
      </span>
      <span className="text-[10px] text-[var(--color-muted)] shrink-0 mt-0.5">
        {date.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
      </span>
    </div>
  )
}

// Wave 5 / spec §6.4: KEY=value editor for `ExecutorConfig.env_overrides`.
// The wire shape is `Record<string, string>` (per ExecutorConfig.yaml).
// Each row is two inputs (key, value) plus a remove button. New rows are
// appended; empty rows are dropped on save. Locked agents get a read-only
// static list. Renders inline within the Runtime tab.
function EnvironmentOverridesEditor({
  value,
  onChange,
  disabled,
}: {
  value: Record<string, string>
  onChange: (next: Record<string, string>) => void
  disabled?: boolean
}) {
  const entries = Object.entries(value)
  return (
    <div className="space-y-1.5">
      {entries.length === 0 ? (
        <p className="text-[11px] text-[var(--color-muted)] italic">No overrides configured.</p>
      ) : (
        entries.map(([k, v], idx) => (
          <div
            key={`${k}-${idx}`}
            className="flex items-center gap-2"
            data-testid={`profile-env-row-${idx}`}
          >
            <Input
              value={k}
              onChange={(e) => {
                const nextKey = e.target.value
                onChange(Object.fromEntries(entries.map(([ek], i) => [i === idx ? nextKey : ek, v])))
              }}
              placeholder="KEY"
              className="text-xs h-8 font-mono flex-1"
              disabled={disabled}
            />
            <span className="text-[var(--color-muted)]">=</span>
            <Input
              value={v}
              onChange={(e) => {
                onChange({ ...value, [k]: e.target.value })
              }}
              placeholder="value"
              className="text-xs h-8 font-mono flex-1"
              disabled={disabled}
            />
            <button
              type="button"
              onClick={() => onChange(Object.fromEntries(entries.filter((_, i) => i !== idx)))}
              className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors disabled:opacity-50"
              aria-label={`Remove env override ${k}`}
              disabled={disabled}
            >
              <X size={12} />
            </button>
          </div>
        ))
      )}
      {!disabled && (
        <button
          type="button"
          data-testid="profile-env-add"
          onClick={() => onChange({ ...value, '': '' })}
          className="text-[10px] text-[var(--color-accent)] hover:underline"
        >
          + Add override
        </button>
      )}
    </div>
  )
}
