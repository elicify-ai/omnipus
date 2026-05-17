import { useState, useEffect, useRef, useMemo, KeyboardEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  Robot,
  Brain,
  Lightbulb,
  MagnifyingGlass,
  PencilSimple,
  Code,
  Chat,
  Gear,
  Shield,
  Rocket,
  X,
  CaretDown,
  CaretUp,
  Scroll,
  NotePencil,
  UploadSimple,
  Info,
} from '@phosphor-icons/react'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { useNavigate } from '@tanstack/react-router'
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
import {
  fetchAgent,
  fetchAppState,
  updateAgent,
  fetchProviders,
  fetchAgentSessions,
  fetchActivity,
  type AgentSession,
  type ActivityEvent,
  type AgentToolsCfg,
  type SandboxProfile,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { AVATAR_COLORS } from '@/lib/constants'

const ICON_OPTIONS = [
  { name: 'Robot', component: Robot },
  { name: 'Brain', component: Brain },
  { name: 'Lightbulb', component: Lightbulb },
  { name: 'MagnifyingGlass', component: MagnifyingGlass },
  { name: 'PencilSimple', component: PencilSimple },
  { name: 'Code', component: Code },
  { name: 'Chat', component: Chat },
  { name: 'Gear', component: Gear },
  { name: 'Shield', component: Shield },
  { name: 'Rocket', component: Rocket },
] as const

type IconName = typeof ICON_OPTIONS[number]['name']

function getIconComponent(name: string | undefined) {
  const match = ICON_OPTIONS.find((o) => o.name === name)
  if (!match && name) {
    console.warn('[AgentProfile] Unknown icon:', name)
  }
  return match?.component ?? Robot
}

interface AgentProfileProps {
  agentId: string
}

export function AgentProfile({ agentId }: AgentProfileProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { addToast } = useUiStore()

  const { data: agent, isLoading, isError } = useQuery({
    queryKey: ['agent', agentId],
    queryFn: () => fetchAgent(agentId),
  })

  const { data: providers = [], isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  const { data: agentSessions = [], isError: sessionsError } = useQuery({
    queryKey: ['agent-sessions', agentId],
    queryFn: () => fetchAgentSessions(agentId),
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

  const recentActivity = allActivity
    .filter((e) => e.agent_id === agentId)
    .slice(0, 5)

  const availableModels = providers.filter((p) => p.status === 'connected').flatMap((p) => p.models ?? [])

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

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('')
  const [selectedColor, setSelectedColor] = useState<string | undefined>(undefined)
  const [selectedIcon, setSelectedIcon] = useState<IconName>('Robot')
  const [fallbackModels, setFallbackModels] = useState<string[]>([])
  const [fallbackInput, setFallbackInput] = useState('')
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
  const [heartbeat, setHeartbeat] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState(0)
  const [maxToolIterations, setMaxToolIterations] = useState(50)
  const [steeringMode, setSteeringMode] = useState('one-at-a-time')
  const [toolFeedback, setToolFeedback] = useState(false)
  const [heartbeatEnabled, setHeartbeatEnabled] = useState(false)
  const [heartbeatInterval, setHeartbeatInterval] = useState(30)
  const [toolsCfg, setToolsCfg] = useState<AgentToolsCfg>({
    builtin: { default_policy: 'allow' },
  })
  const [sandboxProfile, setSandboxProfile] = useState<SandboxProfile | undefined>(undefined)
  const [shellDenyPatterns, setShellDenyPatterns] = useState<string[]>([])
  const [shellAdvancedOpen, setShellAdvancedOpen] = useState(false)

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
    setFallbackModels(agent.fallback_models ?? [])
    setTemperature(agent.model_params?.temperature ?? 1.0)
    setMaxTokens(agent.model_params?.max_tokens ?? 4096)
    setTopP(agent.model_params?.top_p ?? 1.0)
    setUseGlobalRateLimits(agent.rate_limits?.use_global_defaults ?? true)
    setMaxLlmCallsPerHour(agent.rate_limits?.max_llm_calls_per_hour ?? '')
    setMaxToolCallsPerMinute(agent.rate_limits?.max_tool_calls_per_minute ?? '')
    setMaxCostPerDay(agent.rate_limits?.max_cost_per_day ?? '')
    setSoul(agent.soul ?? '')
    setInstructions(agent.instructions ?? '')
    setHeartbeat(agent.heartbeat ?? '')
    setTimeoutSeconds(agent.timeout_seconds ?? 0)
    setMaxToolIterations(agent.max_tool_iterations ?? 50)
    setSteeringMode(agent.steering_mode ?? 'one-at-a-time')
    setToolFeedback(agent.tool_feedback ?? false)
    setHeartbeatEnabled(agent.heartbeat_enabled ?? false)
    setHeartbeatInterval(agent.heartbeat_interval ?? 30)
    setSandboxProfile(agent.sandbox_profile)
    setShellDenyPatterns(agent.shell_policy?.custom_deny_patterns ?? [])
    if (agent.tools_cfg) setToolsCfg((prev) => ({
      builtin: agent.tools_cfg?.builtin ?? prev.builtin,
      mcp: agent.tools_cfg?.mcp as AgentToolsCfg['mcp'],
    }))
    hasHydrated.current = true
  }, [agentId, agent])

  const formData = useMemo(() => ({
    name,
    description,
    model,
    color: selectedColor,
    icon: selectedIcon,
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
    heartbeat,
    timeout_seconds: timeoutSeconds > 0 ? timeoutSeconds : undefined,
    max_tool_iterations: maxToolIterations,
    steering_mode: steeringMode,
    tool_feedback: toolFeedback,
    heartbeat_enabled: heartbeatEnabled,
    heartbeat_interval: heartbeatInterval,
    sandbox_profile: sandboxProfile,
    shell_policy: {
      custom_deny_patterns: shellDenyPatterns.filter((p) => p.trim() !== ''),
    },
    tools_cfg: toolsCfg,
  }), [
    name, description, model, selectedColor, selectedIcon, fallbackModels,
    temperature, maxTokens, topP, useGlobalRateLimits, maxLlmCallsPerHour,
    maxToolCallsPerMinute, maxCostPerDay, soul, instructions, heartbeat,
    timeoutSeconds, maxToolIterations, steeringMode, toolFeedback,
    heartbeatEnabled, heartbeatInterval, sandboxProfile, shellDenyPatterns,
    toolsCfg,
  ])

  const { status: saveStatus, error: saveError } = useAutoSave(
    formData,
    async (data) => {
      // Guard: do not save before the server data has been hydrated into state.
      // Saving before hydration would overwrite real data with empty defaults.
      if (!hasHydrated.current) return
      // Locked agents: strip every field the backend treats as immutable for
      // the locked roster (Mia/Jim/Ava/Ray/Max). Identity fields plus the
      // sandbox profile, shell policy, and tools_cfg are all built-in for
      // these agents — sending them yields either a 400 from the locked-field
      // validator or silently no-ops, and either way the autosave indicator
      // would surface a spurious error.
      const payload = agent?.locked
        ? (({
            name: _n, description: _d, soul: _s, color: _c, icon: _i,
            heartbeat: _h, instructions: _ins, sandbox_profile: _sp,
            shell_policy: _shp, tools_cfg: _tc, ...rest
          }) => rest)(data)
        : data
      await updateAgent(agentId, payload)
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
    // Locked agents can still save model and tool changes — do not disable auto-save
  )

  function addFallbackModel() {
    const trimmed = fallbackInput.trim()
    if (!trimmed || fallbackModels.includes(trimmed)) return
    setFallbackModels((prev) => [...prev, trimmed])
    setFallbackInput('')
  }

  function handleFallbackKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault()
      addFallbackModel()
    } else if (e.key === 'Backspace' && fallbackInput === '' && fallbackModels.length > 0) {
      setFallbackModels((prev) => prev.slice(0, -1))
    }
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
      <div className="flex items-center justify-center h-full text-[var(--color-muted)] text-sm">
        Loading agent...
      </div>
    )
  }

  if (isError || !agent) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-4">
        <p className="text-[var(--color-muted)] text-sm">Agent not found</p>
        <a href="/#/agents" className="text-sm text-[var(--color-muted)] underline hover:text-[var(--color-secondary)]">
          Back to Agents
        </a>
      </div>
    )
  }

  const isLocked = agent.locked === true
  const canEdit = !isLocked

  return (
    <div className="absolute inset-0 overflow-y-auto">
    <div className="max-w-2xl mx-auto px-4 py-6 space-y-4">
      {/* Header */}
      <div className="flex items-center gap-3">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => navigate({ to: '/agents' })}
          className="gap-1 text-[var(--color-muted)]"
        >
          <ArrowLeft size={14} /> Agents
        </Button>
      </div>

      <div className="flex items-center gap-4">
        <div
          className="w-14 h-14 rounded-full flex items-center justify-center shrink-0"
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

      <Separator />

      <Accordion
        type="multiple"
        defaultValue={['identity']}
        className="rounded-lg border border-[var(--color-border)] divide-y divide-[var(--color-border)] overflow-hidden"
      >
        {/* Identity — always rendered; read-only for locked (core) agents */}
        <AccordionItem value="identity" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-bold text-sm">
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
              {canEdit && (
                <div className="space-y-1.5">
                  <p className="text-xs text-[var(--color-muted)]">Avatar color</p>
                  <div className="flex gap-2">
                    {AVATAR_COLORS.map((color) => (
                      <button
                        key={color}
                        type="button"
                        onClick={() => { markDirty(); setSelectedColor(color) }}
                        className="w-7 h-7 rounded-full transition-transform hover:scale-110 focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)] focus:ring-offset-1 focus:ring-offset-[var(--color-primary)]"
                        style={{
                          backgroundColor: color,
                          boxShadow: selectedColor === color ? `0 0 0 2px var(--color-primary), 0 0 0 4px ${color}` : undefined,
                        }}
                        aria-label={color}
                      />
                    ))}
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
            <AccordionTrigger className="px-4 font-headline font-bold text-sm">
              <div className="flex items-center gap-2">
                <span>Sandbox</span>
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
            <AccordionTrigger className="px-4 font-headline font-bold text-sm">
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

        {/* Model Configuration — default CLOSED */}
        <AccordionItem value="model" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-bold text-sm">
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
              />
              {canEdit && (
                <div className="space-y-1.5">
                  <p className="text-xs text-[var(--color-muted)]">Fallback models (tried in order if primary fails)</p>
                  <div className="flex flex-wrap gap-1.5 p-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] min-h-[36px]">
                    {fallbackModels.map((m) => (
                      <span
                        key={m}
                        className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-mono bg-[var(--color-surface-2)] text-[var(--color-secondary)] border border-[var(--color-border)]"
                      >
                        {m}
                        <button
                          type="button"
                          onClick={() => setFallbackModels((prev) => prev.filter((x) => x !== m))}
                          className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
                        >
                          <X size={10} />
                        </button>
                      </span>
                    ))}
                    <input
                      value={fallbackInput}
                      onChange={(e) => { markDirty(); setFallbackInput(e.target.value) }}
                      onKeyDown={handleFallbackKeyDown}
                      onBlur={addFallbackModel}
                      placeholder={fallbackModels.length === 0 ? 'Type a model name, press Enter' : ''}
                      className="flex-1 min-w-[160px] bg-transparent text-xs text-[var(--color-secondary)] outline-none placeholder:text-[var(--color-muted)]"
                    />
                  </div>
                </div>
              )}
              {canEdit && (
                <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
                  <button
                    type="button"
                    onClick={() => setAdvancedOpen((o) => !o)}
                    className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
                  >
                    <span>Advanced parameters</span>
                    {advancedOpen ? <CaretUp size={13} /> : <CaretDown size={13} />}
                  </button>
                  {advancedOpen && (
                    <div className="px-3 pb-3 space-y-4 border-t border-[var(--color-border)]">
                      <RangeField
                        label="Temperature"
                        value={temperature}
                        min={0}
                        max={2}
                        step={0.05}
                        onChange={(v) => { markDirty(); setTemperature(v) }}
                        format={(v) => v.toFixed(2)}
                      />
                      <RangeField
                        label="Max tokens"
                        value={maxTokens}
                        min={256}
                        max={32768}
                        step={256}
                        onChange={(v) => { markDirty(); setMaxTokens(v) }}
                        format={(v) => v.toLocaleString()}
                      />
                      <RangeField
                        label="Top P"
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
            <AccordionTrigger className="px-4 font-headline font-bold text-sm">
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

        {/* Behavior — default CLOSED (SOUL + Instructions + Heartbeat + Execution) */}
        {canEdit && (
          <AccordionItem value="behavior" className="border-0">
            <AccordionTrigger className="px-4 font-headline font-bold text-sm">
              Behavior
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4 space-y-5">
                {/* SOUL.md */}
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <Scroll size={13} className="text-[var(--color-accent)]" />
                    <p className="text-xs font-medium text-[var(--color-secondary)]">SOUL.md</p>
                  </div>
                  <p className="text-xs text-[var(--color-muted)]">
                    The agent's personality and system prompt.
                  </p>
                  <Textarea
                    value={soul}
                    onChange={(e) => { markDirty(); setSoul(e.target.value) }}
                    placeholder={"# Soul\n\nDefine this agent's personality, expertise, and behavioral guidelines..."}
                    rows={6}
                    className="text-xs font-mono resize-none"
                  />
                  <UploadButton onUpload={setSoul} />
                </div>

                <Separator />

                {/* Additional Instructions */}
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <NotePencil size={13} className="text-[var(--color-accent)]" />
                    <p className="text-xs font-medium text-[var(--color-secondary)]">Additional Instructions</p>
                  </div>
                  <p className="text-xs text-[var(--color-muted)]">
                    Extra instructions appended to the agent's context.
                  </p>
                  <Textarea
                    value={instructions}
                    onChange={(e) => { markDirty(); setInstructions(e.target.value) }}
                    placeholder="Add specific instructions, constraints, or domain knowledge..."
                    rows={4}
                    className="text-xs font-mono resize-none"
                  />
                  <UploadButton onUpload={setInstructions} />
                </div>

                <Separator />

                {/* HEARTBEAT.md */}
                <div className="space-y-2">
                  <p className="text-xs font-medium text-[var(--color-secondary)]">HEARTBEAT.md</p>
                  <p className="text-xs text-[var(--color-muted)]">
                    The agent's persistent context — goals, preferences, and working memory.
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

                <Separator />

                {/* Execution */}
                {!isLocked && (
                  <div className="space-y-2">
                    <p className="text-xs font-medium text-[var(--color-secondary)]">Execution</p>
                    <div className="space-y-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4">
                      <div className="flex items-center gap-3">
                        <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">
                          Turn timeout (seconds)
                          <span className="block text-[10px] text-[var(--color-muted)]/70">0 = no limit</span>
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
                        <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">Max tool iterations</label>
                        <Input
                          type="number"
                          min={1}
                          value={maxToolIterations}
                          onChange={(e) => { markDirty(); setMaxToolIterations(Number(e.target.value)) }}
                          className="text-xs h-8"
                        />
                      </div>
                      <div className="flex items-center gap-3">
                        <label className="text-xs text-[var(--color-muted)] w-44 shrink-0">Message handling</label>
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
                          <p className="text-sm text-[var(--color-secondary)]">Tool progress feedback</p>
                          <p className="text-xs text-[var(--color-muted)]">Show tool call status while running</p>
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
            <AccordionTrigger className="px-4 font-headline font-bold text-sm">
              <span>Tools &amp; Permissions</span>
              {toolsCfg.builtin.policies && Object.keys(toolsCfg.builtin.policies).length > 0 && (
                <span className="text-xs text-[var(--color-muted)] font-normal ml-2">
                  {Object.keys(toolsCfg.builtin.policies).length} overrides
                </span>
              )}
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-4">
                <ToolsAndPermissions
                  agentId={agentId}
                  agentType={agent.type}
                  tools={toolsCfg}
                  onChange={setToolsCfg}
                />
              </div>
            </AccordionContent>
          </AccordionItem>

        {/* Sessions — default CLOSED */}
        <AccordionItem value="sessions" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-bold text-sm">
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

        {/* Activity — default CLOSED */}
        <AccordionItem value="activity" className="border-0">
          <AccordionTrigger className="px-4 font-headline font-bold text-sm">
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
  )
}

function SandboxInfoTooltip() {
  const [visible, setVisible] = useState(false)
  return (
    <span className="relative inline-block">
      <button
        type="button"
        className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors focus:outline-none"
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
  value: number
  min: number
  max: number
  step: number
  onChange: (v: number) => void
  format: (v: number) => string
}

function RangeField({ label, value, min, max, step, onChange, format }: RangeFieldProps) {
  return (
    <div className="space-y-1 pt-3">
      <div className="flex items-center justify-between">
        <span className="text-xs text-[var(--color-muted)]">{label}</span>
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
