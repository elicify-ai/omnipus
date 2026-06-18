import { useState, useEffect, useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CaretDown, CaretUp } from '@phosphor-icons/react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { ModelSelector } from '@/components/ui/model-selector'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { FormError } from '@/components/ui/FormError'
import { useUiStore } from '@/store/ui'
import { createAgent, fetchProviders, fetchRegistryTools, fetchSkills, isApiError } from '@/lib/api'
import type { Agent, AgentCreateRequest, AgentToolsCfg, ExecutorConfig, Skill } from '@/lib/api'
import { AVATAR_COLORS, AVATAR_COLORS_BY_NAME } from '@/lib/constants'
import { ToolPolicyEditor } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import { applyRolePreset } from '@/lib/toolPolicyPresets'
import { ICON_OPTIONS, getIconComponent, type IconName } from '@/lib/agentIcons'
import { ExecutorSection, getCreateAgentFormCopy } from './AgentFormFields'

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

  // W6-A3 / I7 (WCAG 2.4.3): restore focus to the element that triggered the
  // modal (typically the "New agent" / "New worker" button) on close.
  //
  // The capture happens in two places:
  //   1. `handleOpenAutoFocus` fires inside `<SheetContent>` (via the
  //      `onOpenAutoFocus` prop below) BEFORE Radix moves focus into
  //      the dialog — so `document.activeElement` is still the trigger
  //      button at capture time, not the dialog body or the autoFocus'd
  //      Name input. This is the load-bearing fix for click-opens (the
  //      autoFocus on Name + Radix's own focus shift would otherwise
  //      steal `document.activeElement` before the useEffect ran).
  //   2. The useEffect here is a fallback for programmatic opens where
  //      `onOpenAutoFocus` was bypassed.
  const triggerRef = useRef<HTMLElement | null>(null)
  const prevOpenRef = useRef(isOpen)
  const handleOpenAutoFocus = (_e: Event) => {
    // Capture before Radix shifts focus. Don't preventDefault — Radix's
    // focus management (focus first focusable = Name input) is desired.
    const active = document.activeElement
    if (active instanceof HTMLElement && active !== document.body) {
      triggerRef.current = active
    }
  }
  useEffect(() => {
    if (isOpen && !prevOpenRef.current) {
      // Fallback capture if onOpenAutoFocus didn't fire (e.g. prop-only path).
      if (!triggerRef.current) {
        const active = document.activeElement
        if (active instanceof HTMLElement && active !== document.body) {
          triggerRef.current = active
        }
      }
    } else if (!isOpen && prevOpenRef.current) {
      // Modal just closed — restore focus to the captured trigger.
      const trigger = triggerRef.current
      triggerRef.current = null
      if (trigger && typeof trigger.focus === 'function' && document.contains(trigger)) {
        // Defer to next frame so Radix has finished its own teardown focus
        // (Radix moves focus to the body on unmount). Wrap in try/catch so a
        // detached-node `focus()` (route-change race) doesn't crash React's
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
        <SheetContent
          side="right"
          widthClass="w-full sm:max-w-3xl"
          className="flex flex-col gap-0 p-0"
          onOpenAutoFocus={handleOpenAutoFocus}
        >
          <SheetHeader className="px-8 pt-7 pb-5 border-b border-[var(--color-border)] shrink-0">
            <SheetTitle
              data-testid={formCopy.testId}
              className="font-headline text-xl font-bold text-[var(--color-secondary)] tracking-tight"
            >
              {formCopy.title}
            </SheetTitle>
            <SheetDescription className="text-sm text-[var(--color-muted)] mt-1.5 leading-relaxed">
              {formCopy.description}
            </SheetDescription>
          </SheetHeader>

          <Tabs defaultValue="general" className="flex-1 min-h-0 flex flex-col">
            <TabsList className="shrink-0 px-8 mb-4 mt-2">
              <TabsTrigger value="general">General</TabsTrigger>
              <TabsTrigger value="tools">Tools &amp; Permissions</TabsTrigger>
            </TabsList>

            <TabsContent value="general" className="flex-1 overflow-y-auto min-h-0 mt-0">
              <div className="space-y-5 px-8 pb-8">
                {/* Avatar preview + color + icon */}
                <div>
                  <label className="text-sm font-medium text-[var(--color-secondary)] mb-2 block">
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
                        <p className="text-xs font-medium text-[var(--color-muted)] mb-1.5">Color</p>
                        {/* W6-A1 / C3: 40x40 tap target (WCAG 2.5.8 AA). Visual swatch stays
                            full-bleed; the button's extra padding gives the tap surface. */}
                        <div className="flex gap-2 flex-wrap">
                          {AVATAR_COLORS.map((c) => {
                            // W6-B4 / M7: aria-label uses the semantic name
                            // (e.g. "Forge Gold") instead of the raw hex.
                            // The `title` attribute is shown on hover and
                            // mirrors the aria-label so sighted users get
                            // the same readable string.
                            const name = AVATAR_COLORS_BY_NAME[c] ?? c
                            return (
                              <button
                                key={c}
                                type="button"
                                onClick={() => setColor(c)}
                                className={`min-h-tap-target-comfortable min-w-tap-target-comfortable rounded-full p-0 transition-transform ${color === c ? 'ring-2 ring-[var(--color-secondary)] ring-offset-2 ring-offset-[var(--color-surface-1)] scale-110' : 'hover:scale-110'}`}
                                style={{ backgroundColor: c }}
                                aria-label={`Select color ${name}`}
                                title={name}
                                data-testid={`avatar-color-${name.toLowerCase().replace(/\s+/g, '-')}`}
                              />
                            )
                          })}
                        </div>
                      </div>
                      <div>
                        <p className="text-xs font-medium text-[var(--color-muted)] mb-1.5">Icon</p>
                        {/* W6-A1 / C3: 44x44 tap target (WCAG 2.5.8 AA, token
                            --spacing-tap-target-min). grid-cols-5 + gap-2
                            keeps the row from wrapping awkwardly on the
                            modal's 3xl width. */}
                        <div className="grid grid-cols-5 gap-2">
                          {ICON_OPTIONS.map(({ name: iconName, component: IconComp }) => (
                            <button
                              key={iconName}
                              type="button"
                              onClick={() => setIcon(iconName)}
                              title={iconName}
                              className={`min-h-tap-target-min min-w-tap-target-min rounded-md transition-colors flex items-center justify-center ${
                                icon === iconName
                                  ? 'bg-[var(--color-accent)] text-[var(--color-primary)]'
                                  : 'bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
                              }`}
                            >
                              <IconComp size={18} />
                            </button>
                          ))}
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Name */}
                <div>
                  <label htmlFor="agent-name" className="text-sm font-medium text-[var(--color-secondary)] mb-1.5 block">
                    Name <span className="text-[var(--color-error)]" aria-hidden="true">*</span>
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
                    required
                    aria-required="true"
                    aria-invalid={nameError ? true : undefined}
                    aria-describedby={nameError ? 'agent-name-error' : undefined}
                  />
                  <FormError id="agent-name-error" error={nameError} />
                </div>

                {/* Description */}
                <div>
                  <label htmlFor="agent-description" className="text-sm font-medium text-[var(--color-secondary)] mb-1.5 block">
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
                  <label className="text-sm font-medium text-[var(--color-secondary)] mb-1.5 block">
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
                      className="text-sm font-medium text-[var(--color-secondary)] mb-1.5 block"
                    >
                      Task prompt <span className="text-[var(--color-muted)] font-normal">(optional)</span>
                    </label>
                    <p className="text-xs text-[var(--color-muted)] mb-1.5">
                      Optional system prompt for the worker&apos;s runner. Composed with any
                      caller-supplied task prompt at run time. Stored as{' '}
                      <span className="font-mono text-xs">SOUL.md</span>. Leave empty to use
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
                          <p className="text-sm font-medium text-[var(--color-secondary)] pt-1">
                            Skills
                            <span className="ml-1.5 font-normal text-[var(--color-muted)]">(opt-in)</span>
                          </p>
                          <p className="text-xs text-[var(--color-muted)]">
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

            <TabsContent value="tools" className="flex-1 overflow-y-auto min-h-0 mt-0 px-8 pb-8">
              {/* #334 (US-D1): shared ToolPolicyEditor with Balanced default.
                  Tool list from registry — new agent has no agentId yet. */}
              <CreateAgentToolsTab
                value={toolsPolicyValue}
                onChange={setToolsPolicyValue}
              />
            </TabsContent>
          </Tabs>

          {/* Sticky footer actions (slideout-friendly) */}
          <div className="flex justify-end gap-3 px-8 py-5 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] shrink-0">
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
        </SheetContent>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
