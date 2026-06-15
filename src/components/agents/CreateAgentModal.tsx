import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
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
  CaretDown,
  CaretUp,
} from '@phosphor-icons/react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { ModelSelector } from '@/components/ui/model-selector'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { useUiStore } from '@/store/ui'
import { createAgent, fetchProviders, fetchRegistryTools, fetchSkills, isApiError } from '@/lib/api'
import type { Agent, AgentCreateRequest, AgentToolsCfg, ExecutorConfig, Skill } from '@/lib/api'
import { AVATAR_COLORS } from '@/lib/constants'
import { ToolPolicyEditor } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import { applyRolePreset } from '@/lib/toolPolicyPresets'
import { ExecutorSection, getCreateAgentFormCopy } from './AgentFormFields'

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

function getIconComponent(name: IconName) {
  return ICON_OPTIONS.find((o) => o.name === name)?.component ?? Robot
}

// ── CreateAgentToolsTab ────────────────────────────────────────────────────────
//
// #334 (US-D1): Renders the shared ToolPolicyEditor inside the Create-Agent
// modal. Fetches the tool registry itself (no agentId → no per-agent tools
// endpoint needed). Handles loading and error states inline.

function CreateAgentToolsTab({
  value,
  onChange,
}: {
  value: ToolPolicyValue
  onChange: (next: ToolPolicyValue) => void
}) {
  const { data: registryTools = [], isLoading, isError } = useQuery({
    queryKey: ['registry-tools'],
    queryFn: fetchRegistryTools,
  })

  if (isLoading) {
    return (
      <div className="space-y-2 py-4">
        {[1, 2, 3].map((i) => (
          <div key={i} className="h-9 rounded-md bg-[var(--color-surface-2)] animate-pulse" />
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <p className="text-xs text-[var(--color-error)] py-4">
        Failed to load tool list. Check that the backend is running.
      </p>
    )
  }

  return (
    <ToolPolicyEditor
      tools={registryTools}
      value={value}
      onChange={onChange}
    />
  )
}

interface CreateAgentModalProps {
  /** Override modal open state (optional — defaults to Zustand store) */
  open?: boolean
  /** Override close handler (optional — defaults to Zustand store) */
  onClose?: () => void
  /** Override create handler (optional — defaults to REST API) */
  onCreate?: (data: AgentCreateRequest) => Promise<void>
  /**
   * Initial tier preset for the modal. When `open` is supplied, this drives
   * the modal type instead of the Zustand store. Defaults to 'custom' so the
   * prop-only path stays simple for tests that don't care about tier.
   */
  initialType?: 'custom' | 'worker'
}

export function CreateAgentModal({ open: openProp, onClose: onCloseProp, onCreate: onCreateProp, initialType }: CreateAgentModalProps) {
  const { createAgentModalOpen, createAgentModalType, closeCreateAgentModal } = useUiStore()
  const queryClient = useQueryClient()

  const isOpen = openProp !== undefined ? openProp : createAgentModalOpen
  const handleClose = onCloseProp ?? closeCreateAgentModal
  // Type-preset: 'custom' (default) or 'worker'. When the prop-only path is
  // taken (open supplied), the tier comes from `initialType` (defaulting to
  // 'custom'). Otherwise the Zustand store drives the tier.
  const modalType = openProp !== undefined ? (initialType ?? 'custom') : createAgentModalType
  const isWorkerModal = modalType === 'worker'
  const formCopy = getCreateAgentFormCopy(modalType)

  const { data: providersData, isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
    enabled: isOpen,
  })

  // US-E6: fetch installed skills so the picker can show available options.
  const { data: availableSkills = [] } = useQuery<Skill[]>({
    queryKey: ['skills'],
    queryFn: fetchSkills,
    enabled: isOpen,
    staleTime: 60_000,
  })
  const providers = Array.isArray(providersData) ? providersData : []
  const connectedProviders = providers.filter((p) => p.status === 'connected')
  const availableModels = connectedProviders.flatMap((p) => p.models ?? [])
  const providerGroups = connectedProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({ providerName: p.display_name ?? p.name ?? p.id, models: p.models ?? [] }))

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('')
  const [color, setColor] = useState(AVATAR_COLORS[0])
  const [icon, setIcon] = useState<IconName>('Robot')
  const [temperature, setTemperature] = useState(1.0)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [nameError, setNameError] = useState('')
  // US-E6: per-agent skill assignment; new agent defaults to none (opt-in).
  const [selectedSkills, setSelectedSkills] = useState<string[]>([])
  // Spec-4 FR-4.1: sub-agent executor; new agent defaults to native (undefined).
  const [executor, setExecutor] = useState<ExecutorConfig | undefined>(undefined)
  // Worker-only: optional task prompt (SOUL.md for the worker runner).
  const [soul, setSoul] = useState('')
  // Worker-only: validation state for the required executor. Workers must
  // carry an executor — they cannot be created without one.
  const [executorError, setExecutorError] = useState('')

  // #334 (US-D1): new agent defaults to Balanced (not default_policy:'allow').
  const BALANCED_DEFAULT: ToolPolicyValue = applyRolePreset('balanced')
  const [toolsPolicyValue, setToolsPolicyValue] = useState<ToolPolicyValue>(BALANCED_DEFAULT)

  const resetForm = () => {
    setName('')
    setDescription('')
    setModel('')
    setColor(AVATAR_COLORS[0])
    setIcon('Robot')
    setTemperature(1.0)
    setAdvancedOpen(false)
    setNameError('')
    setSelectedSkills([])
    setExecutor(undefined)
    setSoul('')
    setExecutorError('')
    setToolsPolicyValue(applyRolePreset('balanced'))
  }

  // Reset form state whenever the modal opens so stale values are not shown
  useEffect(() => {
    if (isOpen) {
      resetForm()
    }
    // resetForm references stable setState callbacks — isOpen is the only meaningful dep
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen])

  const AvatarIcon = getIconComponent(icon)

  const { mutate: doCreate, isPending } = useMutation({
    mutationFn: async (data: AgentCreateRequest) => {
      if (onCreateProp) {
        await onCreateProp(data)
        return data as Agent
      }
      return createAgent(data)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      handleClose()
      resetForm()
    },
    onError: (err: Error) => {
      useUiStore.getState().addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to create agent', variant: 'error' })
    },
  })

  const handleCreate = () => {
    if (!name.trim()) {
      setNameError('Name is required')
      return
    }
    // Worker-only validation: an executor is required (a worker without a
    // runtime is not a worker — it's just a missing-config row).
    // The ExecutorSection is always visible for workers now, so the
    // validation message renders next to the field.
    if (isWorkerModal && !executor) {
      setExecutorError('Worker requires an executor')
      return
    }
    setExecutorError('')
    // Build the AgentToolsCfg wire shape from the ToolPolicyValue editor state.
    const toolsCfg: AgentToolsCfg = {
      builtin: {
        default_policy: toolsPolicyValue.default_policy,
        policies: toolsPolicyValue.policies,
      },
    }
    doCreate({
      // Tier preset — 'custom' for base-agent modal, 'worker' for the
      // worker-section "New worker" button. The backend contract (AgentCreateRequest)
      // accepts either; the tier drives the response shape.
      type: modalType,
      name: name.trim(),
      description: description || undefined,
      model: model || undefined,
      color,
      icon,
      model_params: { temperature },
      tools_cfg: toolsCfg,
      // US-E6: only include skills when at least one is selected (opt-in, default none).
      skills: selectedSkills.length > 0 ? selectedSkills : undefined,
      // Spec-4 FR-4.1: send the executor when present. For workers it is
      // required (validated above) and is always sent (including native,
      // since the worker concept needs a real runtime assignment).
      // For base agents, omit native (preserves the existing payload shape).
      executor: isWorkerModal
        ? executor
        : executor && executor.kind !== 'native' ? executor : undefined,
      // Worker-only: SOUL.md content (the task prompt). Empty is valid for
      // workers (per the locked concept) — omitted for base agents, which
      // expect a later SOUL.md write via the profile edit flow.
      soul: isWorkerModal ? (soul.trim() || undefined) : undefined,
    })
  }

  return (
    <DialogPrimitive.Root open={isOpen} onOpenChange={(open) => !open && handleClose()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
        <DialogPrimitive.Content className="fixed left-[50%] top-[50%] z-50 w-full sm:max-w-lg max-h-[calc(100vh-4rem)] translate-x-[-50%] translate-y-[-50%] border border-[var(--color-border)] bg-[var(--color-surface-1)] rounded-xl p-6 shadow-xl flex flex-col data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95">
          <DialogPrimitive.Title
            data-testid={formCopy.testId}
            className="font-headline text-lg font-bold text-[var(--color-secondary)] mb-1"
          >
            {formCopy.title}
          </DialogPrimitive.Title>
          <DialogPrimitive.Description className="text-sm text-[var(--color-muted)] mb-5">
            {formCopy.description}
          </DialogPrimitive.Description>

          <Tabs defaultValue="general" className="flex-1 min-h-0 flex flex-col">
            <TabsList className="shrink-0 mb-3">
              <TabsTrigger value="general">General</TabsTrigger>
              <TabsTrigger value="tools">Tools &amp; Permissions</TabsTrigger>
            </TabsList>

            <TabsContent value="general" className="flex-1 overflow-y-auto min-h-0 mt-0">
              <div className="space-y-4 pr-1">
                {/* Avatar preview + color + icon */}
                <div>
                  <label className="text-xs font-medium text-[var(--color-muted)] mb-2 block">
                    Avatar
                  </label>
                  <div className="flex items-start gap-4">
                    <div
                      className="w-12 h-12 rounded-full flex items-center justify-center shrink-0"
                      style={{ backgroundColor: color }}
                    >
                      <AvatarIcon size={20} className="text-[var(--color-primary)]" />
                    </div>
                    <div className="flex-1 space-y-3">
                      <div>
                        <p className="text-[10px] text-[var(--color-muted)] mb-1.5">Color</p>
                        <div className="flex gap-2 flex-wrap">
                          {AVATAR_COLORS.map((c) => (
                            <button
                              key={c}
                              type="button"
                              onClick={() => setColor(c)}
                              className={`w-6 h-6 rounded-full transition-transform ${color === c ? 'ring-2 ring-[var(--color-secondary)] ring-offset-2 ring-offset-[var(--color-surface-1)] scale-110' : 'hover:scale-110'}`}
                              style={{ backgroundColor: c }}
                              aria-label={`Select color ${c}`}
                            />
                          ))}
                        </div>
                      </div>
                      <div>
                        <p className="text-[10px] text-[var(--color-muted)] mb-1.5">Icon</p>
                        <div className="grid grid-cols-5 gap-1.5">
                          {ICON_OPTIONS.map(({ name: iconName, component: IconComp }) => (
                            <button
                              key={iconName}
                              type="button"
                              onClick={() => setIcon(iconName)}
                              title={iconName}
                              className={`flex items-center justify-center w-8 h-8 rounded-md transition-colors ${
                                icon === iconName
                                  ? 'bg-[var(--color-accent)] text-[var(--color-primary)]'
                                  : 'bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
                              }`}
                            >
                              <IconComp size={16} />
                            </button>
                          ))}
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Name */}
                <div>
                  <label htmlFor="agent-name" className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
                    Name <span className="text-[var(--color-error)]">*</span>
                  </label>
                  <Input
                    id="agent-name"
                    value={name}
                    onChange={(e) => {
                      setName(e.target.value)
                      if (e.target.value.trim()) setNameError('')
                    }}
                    placeholder="e.g. Research Assistant"
                    className={nameError ? 'border-[var(--color-error)]' : ''}
                    autoFocus
                  />
                  {nameError && (
                    <p className="mt-1 text-xs text-[var(--color-error)]">{nameError}</p>
                  )}
                </div>

                {/* Description */}
                <div>
                  <label htmlFor="agent-description" className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
                    Description
                  </label>
                  <Textarea
                    id="agent-description"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    placeholder="What does this agent do?"
                    rows={2}
                  />
                </div>

                {/* Model */}
                <div>
                  <label className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block">
                    Model
                  </label>
                  {providersError && (
                    <div className="mb-2 rounded-md border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-3 py-2">
                      <p className="text-xs text-[var(--color-error)] font-medium">Provider list unavailable</p>
                      <p className="text-xs text-[var(--color-error)]/80 mt-0.5">
                        Could not load connected providers. Verify your provider settings before creating this agent.
                      </p>
                    </div>
                  )}
                  <ModelSelector
                    models={availableModels}
                    value={model}
                    onChange={setModel}
                    placeholder="Use provider default"
                    providerGroups={providerGroups}
                  />
                </div>

                {/* Worker-only: optional task prompt (SOUL.md).
                    Lives at the top level for workers because the worker form
                    shape is executor-first, no heartbeat, no schedules — the
                    task prompt is the primary "personality" affordance.
                    Mirrors the worker profile's "Task prompt (optional)" block
                    in AgentFormFields. */}
                {isWorkerModal && (
                  <div>
                    <label
                      htmlFor="worker-task-prompt"
                      className="text-xs font-medium text-[var(--color-muted)] mb-1.5 block"
                    >
                      Task prompt <span className="text-[var(--color-muted)] font-normal">(optional)</span>
                    </label>
                    <p className="text-[11px] text-[var(--color-muted)] mb-1.5">
                      Optional system prompt for the worker&apos;s runner. Composed with any
                      caller-supplied task prompt at run time. Stored as{' '}
                      <span className="font-mono text-[10px]">SOUL.md</span>. Leave empty to use
                      the executor&apos;s default behaviour.
                    </p>
                    <Textarea
                      id="worker-task-prompt"
                      data-testid="create-worker-task-prompt"
                      value={soul}
                      onChange={(e) => setSoul(e.target.value)}
                      placeholder="# Task prompt (optional)&#10;&#10;Define how this worker should approach its delegated task..."
                      rows={5}
                      className="text-xs font-mono resize-none"
                      required={false}
                      aria-required={false}
                    />
                  </div>
                )}

                {/* Spec-4 FR-4.1: Executor runtime selector — ALWAYS visible
                    for workers (it's a required field). For base agents
                    the executor is optional and the row stays compact.
                    Sourced from AgentFormFields (the shared form-shape
                    split) so the ExecutorSelector import stays in this
                    chunk — Vite was tree-shaking the previous direct
                    import. */}
                <ExecutorSection
                  isWorker={isWorkerModal}
                  value={executor}
                  onChange={(next) => {
                    setExecutor(next)
                    if (next) setExecutorError('')
                  }}
                  error={executorError}
                />

                {/* Advanced model params + skills (executor moved out). */}
                <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
                  <button
                    type="button"
                    onClick={() => setAdvancedOpen((o) => !o)}
                    className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
                  >
                    <span>Advanced</span>
                    {advancedOpen ? <CaretUp size={13} /> : <CaretDown size={13} />}
                  </button>
                  {advancedOpen && (
                    <div className="px-3 pb-3 border-t border-[var(--color-border)] pt-3 space-y-3">
                      {/* Temperature */}
                      <div className="space-y-1">
                        <div className="flex items-center justify-between">
                          <span className="text-xs text-[var(--color-muted)]">Temperature</span>
                          <span className="text-xs font-mono text-[var(--color-secondary)]">{temperature.toFixed(2)}</span>
                        </div>
                        <input
                          type="range"
                          min={0}
                          max={2}
                          step={0.05}
                          value={temperature}
                          onChange={(e) => setTemperature(Number(e.target.value))}
                          className="w-full h-1.5 rounded-full appearance-none cursor-pointer"
                          style={{
                            background: `linear-gradient(to right, var(--color-accent) 0%, var(--color-accent) ${(temperature / 2) * 100}%, var(--color-border) ${(temperature / 2) * 100}%, var(--color-border) 100%)`,
                          }}
                        />
                      </div>

                      {/* US-E6: Skills picker — opt-in, default none */}
                      {availableSkills.length > 0 && (
                        <div className="space-y-1.5 pt-1 border-t border-[var(--color-border)]">
                          <p className="text-xs font-medium text-[var(--color-secondary)] pt-1">
                            Skills
                            <span className="ml-1.5 font-normal text-[var(--color-muted)]">(opt-in)</span>
                          </p>
                          <p className="text-[11px] text-[var(--color-muted)]">
                            Grant installed skills to this agent. Unselected = no skills.
                          </p>
                          <div className="space-y-1">
                            {availableSkills.map((skill) => (
                              <label
                                key={skill.id}
                                className="flex items-center gap-2 text-xs cursor-pointer py-0.5"
                              >
                                <input
                                  type="checkbox"
                                  checked={selectedSkills.includes(skill.id)}
                                  onChange={(e) => {
                                    if (e.target.checked) {
                                      setSelectedSkills((prev) => [...prev, skill.id])
                                    } else {
                                      setSelectedSkills((prev) => prev.filter((s) => s !== skill.id))
                                    }
                                  }}
                                  className="accent-[var(--color-accent)]"
                                  data-testid={`create-skill-checkbox-${skill.id}`}
                                />
                                <span className="text-[var(--color-secondary)]">{skill.name}</span>
                              </label>
                            ))}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              </div>
            </TabsContent>

            <TabsContent value="tools" className="flex-1 overflow-y-auto min-h-0 mt-0 pr-1">
              {/* #334 (US-D1): shared ToolPolicyEditor with Balanced default.
                  Tool list from registry — new agent has no agentId yet. */}
              <CreateAgentToolsTab
                value={toolsPolicyValue}
                onChange={setToolsPolicyValue}
              />
            </TabsContent>
          </Tabs>

          {/* Actions */}
          <div className="flex justify-end gap-2 mt-6">
            <Button
              variant="outline"
              onClick={() => { handleClose(); resetForm() }}
              disabled={isPending}
            >
              Cancel
            </Button>
            <Button
              onClick={handleCreate}
              disabled={isPending}
              data-testid="create-agent-submit"
            >
              {isPending ? 'Creating...' : formCopy.submitLabel}
            </Button>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
