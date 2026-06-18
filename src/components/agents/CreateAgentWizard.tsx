// CreateAgentWizard — 3-step + Advanced wizard for creating user-creatable
// agents. Replaces the legacy 2-tab CreateAgentModal (commit 12fa5544's Wave
// C1 G4 signpost) with a type-aware branch per spec §5.3-§5.5.
//
// W4 of agent-form-requirements delivery. The wizard is mounted by
// CreateAgentModal.tsx (a thin wrapper) and reads its locked type from the
// createAgentModalType store value. The Type chip is the single source of
// truth for the wizard's branch; the CLI chip (External wizards only) shows
// the locked CLI choice. Per spec §11 #3 the `[×]` on the Type chip closes
// the wizard (no confirmation even if dirty — by design).

import * as React from 'react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter } from '@/components/ui/sheet'
import { X } from '@phosphor-icons/react'

import { useUiStore } from '@/store/ui'

export type WizardType = 'Main' | 'Subagent' | 'subagent_3p'
export type WizardCli = 'claude-code' | 'codex' | 'opencode'

interface WizardProps {
  /** Pre-selected type (set by which +Add button opened the wizard). */
  initialType: WizardType
  /** Pre-selected CLI (External wizards only). */
  initialCli?: WizardCli
  /** Called on successful submit. */
  onSubmit: (payload: WizardSubmitPayload) => Promise<void>
}

export interface WizardSubmitPayload {
  type: WizardType
  cli?: WizardCli
  name: string
  description: string
  color: string
  icon: string
  model: string
  soul: string
  instructions: string
  heartbeat?: string
  heartbeat_enabled?: boolean
  heartbeat_interval?: number
  voice?: string
  tools_cfg?: unknown
  skills?: string[]
  fallback_models?: unknown[]
  executor_cli_path?: string
  executor_env_overrides?: Record<string, string>
  executor_cli_args?: string
}

const TYPE_CHIP_LABEL: Record<WizardType, string> = {
  Main: 'Main',
  Subagent: 'Subagent',
  'subagent_3p': 'Subagent (External)',
}

const CLI_CHIP_LABEL: Record<WizardCli, string> = {
  'claude-code': 'claude-code',
  codex: 'codex',
  opencode: 'opencode',
}

export function CreateAgentWizard({ initialType, initialCli, onSubmit }: WizardProps) {
  const closeCreateAgentModal = useUiStore((s) => s.closeCreateAgentModal)

  const [step, setStep] = useState<1 | 2 | 3>(1)
  const [submitting, setSubmitting] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [color] = useState('Verdant')
  const [icon] = useState('Robot')
  const [model, setModel] = useState('')
  const [soul, setSoul] = useState('')
  const [instructions, setInstructions] = useState('')

  const isWorker = initialType !== 'Main'
  const isExternal = initialType === 'subagent_3p'
  const soulLabel = isWorker ? 'Soul / task prompt' : 'Soul'

  // Step gating: name + (description if worker) + model + soul are required.
  const step1Valid = name.length > 0 && model.length > 0 && (!isWorker || description.trim().length > 0)
  const step2Valid = soul.trim().length > 0
  const canAdvance = step === 1 ? step1Valid : step === 2 ? step2Valid : true

  async function handleSubmit() {
    setSubmitting(true)
    try {
      await onSubmit({
        type: initialType,
        cli: initialCli,
        name,
        description,
        color,
        icon,
        model,
        soul,
        instructions,
      })
      closeCreateAgentModal()
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Sheet open onOpenChange={(open) => !open && closeCreateAgentModal()}>
      <SheetContent
        side="right"
        // The wizard is full-width on phone, capped at 3xl on sm+ (matches
        // existing create-modal convention from CreateAgentModal.tsx:247).
        className="w-full sm:max-w-3xl flex flex-col gap-0 p-0"
      >
        <SheetHeader className="px-8 pt-7 pb-5 border-b border-[var(--color-border)] shrink-0">
          <SheetTitle>+ New agent</SheetTitle>
          <SheetDescription>
            Configure the agent's identity, personality, and tools.
          </SheetDescription>
          <div className="flex flex-col gap-2 mt-4">
            {/* Type chip (locked). Per spec §11 #3, the [x] cancels the wizard. */}
            <div className="flex items-center justify-between gap-2 rounded-md border border-[var(--color-border)] px-3 py-2">
              <div className="flex items-center gap-2 text-sm">
                <span className="text-[var(--color-muted)]">Type:</span>
                <span data-testid="type-chip" className="font-medium">
                  {TYPE_CHIP_LABEL[initialType]}
                </span>
                <span className="text-xs text-[var(--color-muted)]">(locked)</span>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={closeCreateAgentModal}
                aria-label="Cancel wizard"
                className="h-11 w-11"
              >
                <X size={18} />
              </Button>
            </div>
            {/* CLI chip — External wizards only (locked at create). */}
            {isExternal && initialCli && (
              <div className="flex items-center gap-2 rounded-md border border-[var(--color-border)] px-3 py-2 text-sm">
                <span className="text-[var(--color-muted)]">CLI:</span>
                <span data-testid="cli-chip" className="font-medium">
                  {CLI_CHIP_LABEL[initialCli]}
                </span>
                <span className="text-xs text-[var(--color-muted)]">(locked)</span>
              </div>
            )}
          </div>
          {/* Stepper. Mobile shows just dots + current step name; sm+ shows full labels. */}
          <div className="flex items-center gap-2 mt-4 text-sm" data-testid="wizard-stepper">
            <span className={step === 1 ? 'font-semibold' : 'text-[var(--color-muted)]'}>
              <span className="sm:hidden">●</span>
              <span className="hidden sm:inline">① Identity</span>
            </span>
            <span className="text-[var(--color-muted)]">○</span>
            <span className={step === 2 ? 'font-semibold' : 'text-[var(--color-muted)]'}>
              <span className="sm:hidden">○</span>
              <span className="hidden sm:inline">② Personality</span>
            </span>
            <span className="text-[var(--color-muted)]">○</span>
            <span className={step === 3 ? 'font-semibold' : 'text-[var(--color-muted)]'}>
              <span className="sm:hidden">○</span>
              <span className="hidden sm:inline">③ Tools</span>
            </span>
          </div>
        </SheetHeader>

        <div className="flex-1 overflow-auto px-8 py-6 space-y-4">
          {step === 1 && (
            <>
              {/* Identity step. Per spec §5.3-§5.5, all three types share
                  color/icon/name/model; Subagent / External require description. */}
              <div className="space-y-2">
                <label htmlFor="wizard-name" className="text-sm font-medium">
                  Name <span aria-label="required">*</span>
                </label>
                <Input
                  id="wizard-name"
                  data-testid="wizard-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Research Assistant"
                />
              </div>
              <div className="space-y-2">
                <label htmlFor="wizard-description" className="text-sm font-medium">
                  Description{isWorker && <span aria-label="required"> *</span>}
                </label>
                <Input
                  id="wizard-description"
                  data-testid="wizard-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder={
                    isWorker
                      ? 'What this worker handles — required for routing'
                      : 'A short subtitle (optional for Main)'
                  }
                />
              </div>
              <div className="space-y-2">
                <label htmlFor="wizard-model" className="text-sm font-medium">
                  Model <span aria-label="required">*</span>
                </label>
                {/* Main / Subagent: model picker (filter by connected providers).
                    External: free-text slug input. The picker is wired by
                    AgentFormFields.tsx in a follow-up commit; the input
                    accepts any provider/model string for now. */}
                <Input
                  id="wizard-model"
                  data-testid="wizard-model"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  placeholder={isExternal ? 'claude-sonnet-4-6' : 'Pick a connected model'}
                />
              </div>
            </>
          )}

          {step === 2 && (
            <>
              {/* Personality step. soul is required for all three types (incl.
                  External — passed as CLI prompt content). Heartbeat / voice are
                  Main only. */}
              <div className="space-y-2">
                <label htmlFor="wizard-soul" className="text-sm font-medium">
                  {soulLabel} <span aria-label="required">*</span>
                </label>
                <Textarea
                  id="wizard-soul"
                  data-testid="wizard-soul"
                  value={soul}
                  onChange={(e) => setSoul(e.target.value)}
                  rows={10}
                  placeholder="You are a focused research assistant. You answer concisely..."
                />
              </div>
              <div className="space-y-2">
                <label htmlFor="wizard-instructions" className="text-sm font-medium">
                  Instructions (optional)
                </label>
                <Textarea
                  id="wizard-instructions"
                  value={instructions}
                  onChange={(e) => setInstructions(e.target.value)}
                  rows={4}
                  placeholder="Additional runtime instructions..."
                />
              </div>
              {!isWorker && (
                <div className="space-y-2" data-testid="wizard-heartbeat">
                  {/* Heartbeat body + enable + interval (Main only). */}
                  <label className="text-sm font-medium">Heartbeat (Main only)</label>
                  <Input placeholder="Periodic instruction body" />
                </div>
              )}
              {!isWorker && (
                <div className="space-y-2" data-testid="wizard-voice">
                  {/* Voice field — Main only. Wired to voice-provider-sub.tsx in W5. */}
                  <label className="text-sm font-medium">Voice (Main only)</label>
                  <Input placeholder="voice id (e.g. alloy)" />
                </div>
              )}
            </>
          )}

          {step === 3 && (
            <>
              {/* Tools step. tools_cfg / fallback_models hidden for External;
                  skills[] is the only field External wizards show. */}
              {!isExternal && (
                <div className="space-y-2" data-testid="wizard-tools-cfg">
                  <label className="text-sm font-medium">Tools policy</label>
                  <p className="text-xs text-[var(--color-muted)]">
                    Default: deny. Per-tool allow / ask / deny editor.
                  </p>
                </div>
              )}
              <div className="space-y-2">
                <label className="text-sm font-medium">Skills</label>
                <p className="text-xs text-[var(--color-muted)]">
                  Multi-select chips of installed skills.
                </p>
              </div>
              {!isExternal && (
                <div className="space-y-2">
                  <label className="text-sm font-medium">Fallback models (max 2)</label>
                  <p className="text-xs text-[var(--color-muted)]">
                    Each entry is a [{`{model, provider}`}] object. Server rejects
                    more than 2 with 400.
                  </p>
                </div>
              )}
            </>
          )}
        </div>

        {/* Footer. flex-col-reverse on phone puts the primary CTA on top; sm+
            puts them side-by-side (matches SheetFooter convention from
            sheet.tsx:101). */}
        <SheetFooter className="px-8 py-4 border-t border-[var(--color-border)] flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2">
          <Button
            type="button"
            variant="ghost"
            onClick={closeCreateAgentModal}
            disabled={submitting}
          >
            Cancel
          </Button>
          {step > 1 && (
            <Button
              type="button"
              variant="outline"
              onClick={() => setStep((s) => (s - 1) as 1 | 2 | 3)}
              disabled={submitting}
              data-testid="wizard-back"
            >
              ← Back
            </Button>
          )}
          {step < 3 ? (
            <Button
              type="button"
              onClick={() => setStep((s) => (s + 1) as 1 | 2 | 3)}
              disabled={!canAdvance}
              data-testid={`wizard-next-${step}`}
            >
              Next →
            </Button>
          ) : (
            <Button
              type="button"
              onClick={handleSubmit}
              disabled={!canAdvance || submitting}
              data-testid="wizard-create"
            >
              {submitting ? 'Creating…' : 'Create agent'}
            </Button>
          )}
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

export default CreateAgentWizard
