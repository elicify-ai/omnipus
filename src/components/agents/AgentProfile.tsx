import { useState, useEffect, useRef, useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  X,
  CaretDown,
  CaretUp,
  UploadSimple,
  Info,
  Plus,
  Sparkle,
  Star,
} from '@phosphor-icons/react'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { SmartSelect } from '@/components/ui/smart-select'
import { ModelSelector } from '@/components/ui/model-selector'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui/accordion'
import { ToolsAndPermissions } from './ToolsAndPermissions'
import { SandboxProfileSelector } from './SandboxProfileSelector'
import { ShellDenyPatternsEditor } from './ShellDenyPatternsEditor'
import { ExecutorSelector } from './ExecutorSelector'
import { BehaviorFields } from './AgentFormFields'
import { SchedulesList } from '@/components/command-center/SchedulesList'
import { ScheduleFormSheet } from '@/components/command-center/ScheduleFormSheet'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import {
  fetchAgent,
  fetchAppState,
  updateAgent,
  fetchProviders,
  fetchAgentSessions,
  fetchActivity,
  fetchSkills,
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
import { AVATAR_COLORS, AVATAR_COLORS_BY_NAME } from '@/lib/constants'
import type { FallbackModel } from '@/lib/api/generated/openapi-types'
import { ICON_OPTIONS, getIconComponent, type IconName } from '@/lib/agentIcons'

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

  // Model → provider lookup. The fallback editor attributes each chip to
  // the provider that owns it; if a model is listed by more than one
  // provider we keep the first match (consistent with ModelSelector's
  // rendering order). Used to narrow wire `provider?: string` to
  // required at hydration.
  const modelToProvider: Record<string, string> = {}
  for (const p of connectedProviders) {
    for (const m of p.models ?? []) {
      if (!(m in modelToProvider)) {
        modelToProvider[m] = p.display_name ?? p.name ?? p.id ?? ''
      }
    }
  }

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  // Tracks whether the initial hydration from the server has completed.
  // Guards auto-save from firing with default (empty) state before the first fetch resolves.
  const hasHydrated = useRef(false)

  // Reset flags when navigating to a different agent
  useEffect(() => {
    isDirtyRef.current = false
    hasHydrated.current = false
  }, [agentId])

  // W6-B1 / I7 (WCAG 2.4.3): restore focus to the element that triggered the
  // slide-over (typically the AgentCard button — also covers the TrustGraph
  // row click and the /agents/:id route mount) on close.
  //
  // The capture happens in two places, mirroring the Wave A CreateAgentModal
  // pattern (src/components/agents/CreateAgentModal.tsx:175-213):
  //   1. `handleOpenAutoFocus` fires inside <SheetContent> (via the
  //      `onOpenAutoFocus` prop below) BEFORE Radix moves focus into the
  //      dialog — so `document.activeElement` is still the trigger button
  //      at capture time, not the dialog body. This is the load-bearing
  //      fix for click-opens (Radix's first-focusable focus shift would
  //      otherwise steal activeElement before the useEffect ran).
  //   2. The useEffect here is a fallback for programmatic opens (e.g. the
  //      /agents/:id route mount via `openEditAgentSlideOver` from
  //      src/routes/_app/agents.$agentId.tsx:13) where onOpenAutoFocus
  //      was bypassed.
  const slideOverTriggerRef = useRef<HTMLElement | null>(null)
  const prevOpenRef = useRef(isOpen)
  const handleOpenAutoFocus = (_e: Event) => {
    // Capture before Radix shifts focus. Don't preventDefault — Radix's
    // focus management (focus first focusable inside the slide-over) is
    // desired; we only want the trigger reference for restore-on-close.
    const active = document.activeElement
    if (active instanceof HTMLElement && active !== document.body) {
      slideOverTriggerRef.current = active
    }
  }
  useEffect(() => {
    if (isOpen && !prevOpenRef.current) {
      // Fallback capture if onOpenAutoFocus didn't fire (e.g. programmatic
      // route mount via agents.$agentId.tsx).
      if (!slideOverTriggerRef.current) {
        const active = document.activeElement
        if (active instanceof HTMLElement && active !== document.body) {
          slideOverTriggerRef.current = active
        }
      }
    } else if (!isOpen && prevOpenRef.current) {
      // Slide-over just closed — restore focus to the captured trigger.
      const trigger = slideOverTriggerRef.current
      slideOverTriggerRef.current = null
      if (trigger && typeof trigger.focus === 'function' && document.contains(trigger)) {
        // Defer to next frame so Radix has finished its own teardown focus
        // (Radix moves focus to the body on unmount). Wrap in try/catch so a
        // detached-node focus() (route-change race) doesn't crash React's
        // commit phase.
        requestAnimationFrame(() => {
          try {
            trigger.focus()
          } catch {
            // Silent — focus restore is best-effort; users can re-tab.
          }
        })
      }
    }
    prevOpenRef.current = isOpen
  }, [isOpen])

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
  const [steeringMode, setSteeringMode] = useState('one-at-a-time')
  const [toolFeedback, setToolFeedback] = useState(false)
  const [heartbeatEnabled, setHeartbeatEnabled] = useState(false)
  const [heartbeatInterval, setHeartbeatInterval] = useState(30)
  const [creatingSchedule, setCreatingSchedule] = useState(false)
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
        provider: entry.provider || modelToProvider[entry.model] || '',
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
    setVoice(agent.voice ?? '')
    setHeartbeat(agent.heartbeat ?? '')
    setTimeoutSeconds(agent.timeout_seconds ?? 0)
    setMaxToolIterations(agent.max_tool_iterations ?? 50)
    setSteeringMode(agent.steering_mode ?? 'one-at-a-time')
    setToolFeedback(agent.tool_feedback ?? false)
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
    voice: voice !== '' ? voice : undefined,
    heartbeat,
    timeout_seconds: timeoutSeconds > 0 ? timeoutSeconds : undefined,
    max_tool_iterations: maxToolIterations,
    steering_mode: steeringMode,
    tool_feedback: toolFeedback,
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
    timeoutSeconds, maxToolIterations, steeringMode, toolFeedback,
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
    const knownProvider = modelToProvider[trimmed]
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
  const AvatarIcon = getIconComponent(selectedIcon)

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

  return (
    <ProfileSheet
      isOpen={isOpen}
      onClose={closeEditAgentSlideOver}
      title={`Edit ${agent.name}`}
      onOpenAutoFocus={handleOpenAutoFocus}
    >
      <SheetHeader className="px-8 pt-7 pb-5 border-b border-[var(--color-border)] shrink-0">
          <div className="flex items-center gap-4">
            <div
              className="w-12 h-12 rounded-full flex items-center justify-center shrink-0"
              style={{ backgroundColor: selectedColor ?? 'var(--color-surface-3)' }}
        >
          <AvatarIcon size={22} className="text-[var(--color-primary)]" />
        </div>
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
        <div className="ml-auto">
          <AutoSaveIndicator status={saveStatus} error={saveError} />
        </div>
      </div>
        </SheetHeader>

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
      <Accordion
        type="multiple"
        defaultValue={isWorkerAgent
          ? ['identity', 'sandbox', 'executor', 'tools']
          : ['identity', 'sandbox', 'model', 'behavior']}
        className="rounded-lg border border-[var(--color-border)] divide-y divide-[var(--color-border)] overflow-hidden"
      >
        {/* Identity — always rendered; read-only for locked (core) agents */}
        <AccordionItem value="identity" className="border-0">
          {/* W6-B1 / I3: 14 px / 600. Explicit `text-[14px]` (not `text-sm`) so
              the size cannot drift if Tailwind defaults change; `font-semibold`
              (600) per the spec — lighter than the prior 700/bold so the
              section heading reads as an H2, not a button label. */}
          <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
            Identity
          </AccordionTrigger>
          <AccordionContent>
            <div className="px-4 space-y-3">
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
              {/* W6-B4 / G3: Default agent toggle. The wire field `default` is
                  a boolean on Agent / AgentUpdateRequest; at most one agent
                  across the roster is default. Previously, the only way to
                  change the default was from the Agents list ("Set as default"
                  link on each card). This toggle brings the action into the
                  Edit profile so users do not have to leave the slide-over.
                  Hidden for locked core agents (locked roster: Mia is the
                  seeded default and the field is immutable for them). */}
              {canEdit && (
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
                      // W6-B4 / G3 (Reviewer 2): optimistic UI clear. The
                      // toggle change triggers the auto-save (500ms debounce)
                      // but the AgentListScreen / AgentCard stars depend on
                      // the `['agents']` query. Without this invalidation,
                      // the star would only move after the next list refetch.
                      // Fire the invalidation synchronously so the star
                      // transitions to the new state in real time across
                      // every visible card.
                      queryClient.invalidateQueries({ queryKey: ['agents'] })
                      // Surface the action so users get explicit feedback —
                      // a default toggle is a global roster change, not a
                      // local edit.
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
              {canEdit && (
                <div className="space-y-1.5">
                  <p className="text-xs text-[var(--color-muted)]">Avatar color</p>
                  <div className="flex gap-2">
                    {AVATAR_COLORS.map((color) => {
                      // W6-B4 / M7: aria-label and title use the semantic
                      // name (e.g. "Forge Gold") instead of the raw hex.
                      const name = AVATAR_COLORS_BY_NAME[color] ?? color
                      return (
                        <button
                          key={color}
                          type="button"
                          onClick={() => { markDirty(); setSelectedColor(color) }}
                          className="w-7 h-7 rounded-full transition-transform hover:scale-110 focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)] focus:ring-offset-1 focus:ring-offset-[var(--color-primary)]"
                          style={{
                            backgroundColor: color,
                            boxShadow: selectedColor === color ? `0 0 0 2px var(--color-primary), 0 0 0 4px ${color}` : undefined,
                          }}
                          aria-label={name}
                          title={name}
                        />
                      )
                    })}
                  </div>
                </div>
              )}
              {canEdit && (
                <div className="space-y-1.5">
                  <p className="text-xs text-[var(--color-muted)]">Avatar icon</p>
                  <SmartSelect
                    value={selectedIcon}
                    onValueChange={(v) => { markDirty(); setSelectedIcon(v as IconName) }}
                    triggerClassName="w-48"
                    items={ICON_OPTIONS.map(({ name: iconName }) => ({ value: iconName, label: iconName }))}
                  />
                </div>
              )}
            </div>
          </AccordionContent>
        </AccordionItem>

        {/* Sandbox — editable for custom agents, read-only for locked core agents */}
        {!isLocked ? (
          <AccordionItem value="sandbox" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
              <div className="flex items-center gap-2">
                <span>Sandbox</span>
                {/* #335 (US-D3): standing warning badge on accordion header when a widened profile is active */}
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
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4 space-y-4">
                <SandboxProfileSelector
                  value={sandboxProfile}
                  agentName={name || agent.name}
                  godModeAvailable={appState?.god_mode_available ?? false}
                  godModeOptedIn={appState?.god_mode_opted_in ?? false}
                  onChange={(p) => { markDirty(); setSandboxProfile(p) }}
                />

                {/* Advanced: shell deny patterns */}
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
            </AccordionContent>
          </AccordionItem>
        ) : (
          <AccordionItem value="sandbox" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
              <div className="flex items-center gap-2">
                <span>Sandbox</span>
                <SandboxInfoTooltip />
              </div>
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4 space-y-3">
                <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3">
                  <p className="text-[10px] uppercase tracking-wider text-[var(--color-muted)] mb-1">
                    Profile
                  </p>
                  <p className="text-sm font-medium text-[var(--color-secondary)]">
                    {sandboxProfile
                      ? sandboxProfile === 'workspace+net'
                        ? 'Workspace + Net'
                        : sandboxProfile.charAt(0).toUpperCase() + sandboxProfile.slice(1)
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
            </AccordionContent>
          </AccordionItem>
        )}

        {/* Executor — Spec-4 FR-4.1: sub-agent runtime selector + runner test.
            Worker-only. Base agents run native/in-process only — there is no
            third-party executor for them, so the entire accordion is omitted
            rather than rendered disabled. Read-only for locked core workers. */}
        {isWorkerAgent && (
          <AccordionItem value="executor" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
              <div className="flex items-center gap-2">
                <span>Executor</span>
                {(executor?.kind === 'external-cli' || executor?.kind === 'remote-a2a') && (
                  <span className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-[var(--color-surface-3)] text-[var(--color-muted)] border border-[var(--color-border)]">
                    {executor.kind === 'external-cli' ? (executor.cli ?? 'external') : 'A2A'}
                  </span>
                )}
              </div>
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4">
                <ExecutorSelector
                  value={executor}
                  agentId={resolvedAgentId}
                  disabled={isLocked}
                  onChange={(next) => { markDirty(); setExecutor(next) }}
                />
              </div>
            </AccordionContent>
          </AccordionItem>
        )}

        {/* Model Configuration — default CLOSED */}
        <AccordionItem value="model" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
            Model Configuration
          </AccordionTrigger>
          <AccordionContent>
            <div className="px-4 space-y-3">
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
              {canEdit && (
                <div className="space-y-1.5">
                  <p className="text-xs text-[var(--color-muted)]">Fallback models (tried in order if primary fails)</p>
                  <div className="flex flex-wrap gap-1.5 p-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] min-h-[36px]">
                    {fallbackModels.map((entry) => (
                      <span
                        key={entry.model}
                        data-testid={`fallback-chip-model-${entry.model}`}
                        className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-mono bg-[var(--color-surface-2)] text-[var(--color-secondary)] border border-[var(--color-border)]"
                      >
                        {/* Provider badge — color-coded by provider name hash so
                            visually distinct entries don't all look identical.
                            Empty provider is rendered as a muted dash so the
                            badge slot is always present (consistent height). */}
                        <span
                          data-testid={`fallback-chip-provider-${entry.model}`}
                          className="inline-flex items-center px-1 rounded text-[9px] font-semibold"
                          style={{
                            backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                            color: 'var(--color-accent)',
                            border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                          }}
                        >
                          {entry.provider || '—'}
                        </span>
                        <span>{entry.model}</span>
                        <button
                          type="button"
                          data-testid={`fallback-chip-remove-${entry.model}`}
                          aria-label={`Remove fallback ${entry.model}`}
                          onClick={() => removeFallback(entry.model)}
                          className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
                        >
                          <X size={10} />
                        </button>
                      </span>
                    ))}
                    {/* Add UI: a second <ModelSelector> dedicated to the
                        fallback list. It mounts as a separate combobox so the
                        primary model selector above stays untouched. The
                        add-trigger is identified by data-testid
                        `fallback-add-trigger` and each pickable item is
                        `fallback-add-item-<model>`. */}
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
                </div>
              )}
              {/* #335 (US-D3): temperature/top-p under "Sampling parameters" with plain captions */}
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
            </div>
          </AccordionContent>
        </AccordionItem>

        {/* Rate Limits — default CLOSED */}
        {!isLocked && (
          <AccordionItem value="rate-limits" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
              Rate Limits
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4 space-y-3">
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
            </AccordionContent>
          </AccordionItem>
        )}

        {/* Behavior — default CLOSED.
            Tier-branched: base agents keep the full set (SOUL persona +
            instructions + heartbeat + execution). Workers get a slimmer
            section — a relabeled "Task prompt (optional)" instead of the
            "Personality & instructions" framing, and NO heartbeat. The
            task prompt is optional (empty is valid) per the locked concept
            (`.preview-doc/agents.html`); the backend treats worker SOUL.md
            as optional. Both tiers still get Execution params — they're
            per-agent engine settings, not persona or scheduling. */}
        {canEdit && (
          <AccordionItem value="behavior" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
              Behavior
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4 space-y-5">
                {/* Shared "Behavior" block: SOUL/task-prompt + Additional Instructions.
                    Delegates to the same component used by the create modal so
                    the two surfaces cannot drift. The profile renders an Upload
                    button per field; the modal does not (it has no file-upload
                    affordance for soul/instructions). */}
                <BehaviorFields
                  isWorker={isWorkerAgent}
                  soul={soul}
                  setSoul={(v) => { markDirty(); setSoul(v) }}
                  instructions={instructions}
                  setInstructions={(v) => { markDirty(); setInstructions(v) }}
                  // W6-B4 / G1: per-agent persona voice (TTS voice name / model ID).
                  // Schema-pinned; not active until v0.2.0 TTS.
                  voice={voice}
                  setVoice={(v) => { markDirty(); setVoice(v) }}
                  renderUploadButton={(_, onUpload) => <UploadButton onUpload={onUpload} />}
                />

                {/* #335 (US-D3): relabeled HEARTBEAT.md → "Background tasks / periodic instructions".
                    Base-only. Workers never run on a schedule (delegation-only
                    labour agents), so this whole sub-block is omitted. */}
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

                <Separator />

                {/* Execution — both tiers. Per-agent engine settings, not
                    persona or scheduling. Locked core agents skip this
                    sub-block (they run on a built-in policy). */}
                {!isLocked && (
                  <div className="space-y-2">
                    <p className="text-xs font-medium text-[var(--color-secondary)]">Execution</p>
                    {/* #335 (US-D3): plain captions for execution parameters */}
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
                      <div className="flex items-center gap-3">
                        <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                          Message handling
                          <span className="block text-[10px] text-[var(--color-muted)]/70">
                            How concurrent messages are processed.
                          </span>
                        </label>
                        <SmartSelect
                          value={steeringMode}
                          onValueChange={(v) => { markDirty(); setSteeringMode(v) }}
                          triggerClassName="text-xs h-8"
                          items={[
                            { value: 'one-at-a-time', label: 'One at a time' },
                            { value: 'parallel', label: 'Parallel' },
                            { value: 'queue', label: 'Queue' },
                          ]}
                        />
                      </div>
                      <div className="flex items-center justify-between py-1">
                        <div>
                          <p className="text-sm text-[var(--color-secondary)]">Show tool progress</p>
                          <p className="text-xs text-[var(--color-muted)]">
                            Echo tool results back to the agent as it works.
                          </p>
                        </div>
                        <Switch
                          checked={toolFeedback}
                          onCheckedChange={(v) => { markDirty(); setToolFeedback(v) }}
                        />
                      </div>
                    </div>
                  </div>
                )}
              </div>
            </AccordionContent>
          </AccordionItem>
        )}

        {/* Tools & Permissions — default CLOSED */}
        <AccordionItem value="tools" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
              <span>Tools &amp; Permissions</span>
              {toolsCfg.builtin?.policies && Object.keys(toolsCfg.builtin.policies).length > 0 && (
                <span className="text-xs text-[var(--color-muted)] font-normal ml-2">
                  {Object.keys(toolsCfg.builtin.policies).length} overrides
                </span>
              )}
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4">
                {/* #332 (US-D5 / B-2): isLocked=true → read-only editor, no writes */}
                <ToolsAndPermissions
                  agentId={agentId}
                  agentType={agent.type}
                  isLocked={isLocked}
                  tools={toolsCfg}
                  onChange={setToolsCfg}
                />
              </div>
            </AccordionContent>
          </AccordionItem>

        {/* Skills — US-E6: per-agent skill assignment, opt-in, default none */}
        <AccordionItem value="skills" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
            <div className="flex items-center gap-2">
              <span>Skills</span>
              {agentSkills.length > 0 && (
                <span className="text-xs text-[var(--color-muted)] font-normal">
                  {agentSkills.length} granted
                </span>
              )}
            </div>
          </AccordionTrigger>
          <AccordionContent>
            <div className="px-4 space-y-3">
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
          </AccordionContent>
        </AccordionItem>

        {/* Sessions — default CLOSED */}
        <AccordionItem value="sessions" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
            Sessions
          </AccordionTrigger>
          <AccordionContent>
            <div className="px-4">
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
            </div>
          </AccordionContent>
        </AccordionItem>

        {/* Schedules — default CLOSED (#264). Base-only. Workers are
            delegation-only labour agents and never own a schedule, so the
            whole accordion is omitted. The schedule-owner picker on the
            create form also filters workers out (see ScheduleFormSheet). */}
        {!isWorkerAgent && (
          <AccordionItem value="schedules" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
              Schedules
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4 space-y-3">
                <div className="flex justify-end">
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
              </div>
            </AccordionContent>
          </AccordionItem>
        )}

        {/* Activity — default CLOSED */}
        <AccordionItem value="activity" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-semibold text-[14px]">
            Activity
          </AccordionTrigger>
          <AccordionContent>
            <div className="px-4">
              {agent.stats && (
                <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-4">
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
            </div>
          </AccordionContent>
        </AccordionItem>
      </Accordion>
        </div>
      </div>

      {/* Sticky footer — always present so the user has an explicit dismiss
          affordance; the Radix X in the top-right is also wired here via
          SheetContent's onOpenChange. */}
      <div className="px-8 py-4 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] shrink-0 flex items-center justify-between gap-3">
        <AutoSaveIndicator status={saveStatus} error={saveError} />
        <Button
          variant="outline"
          onClick={closeEditAgentSlideOver}
          data-testid="agent-profile-close"
        >
          Close
        </Button>
      </div>

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
