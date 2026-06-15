import { Scroll, NotePencil } from '@phosphor-icons/react'
import { Textarea } from '@/components/ui/textarea'
import { Separator } from '@/components/ui/separator'

// ── AgentFormFields ──────────────────────────────────────────────────────────
//
// Shared worker-vs-base form split for the create modal AND the agent profile.
// The locked concept (`.preview-doc/agents.html`) defines workers as
// delegation-only labour agents — never a chat target, no heartbeat, never
// the default, optional soul ("task prompt"), and they carry an executor.
// The behavioural split (soul/instructions/heartbeat + execution params) is
// rendered by the same components in both the modal (tab layout) and the
// profile (accordion layout); only the surrounding chrome differs.
//
// Two consumers, two layouts — a single shared `BehaviorFields` component
// keeps the split in ONE place so the modal and profile cannot drift.

export interface BehaviorFieldsProps {
  /** True when rendering the worker (delegation-only labour) form shape. */
  isWorker: boolean
  /** The current SOUL.md content. For workers this is the task prompt. */
  soul: string
  onSoulChange: (next: string) => void
  /** The current AGENT.md instructions content. Optional in both tiers. */
  instructions: string
  onInstructionsChange: (next: string) => void
  /**
   * Optional upload button — the profile renders one, the modal does not
   * (the modal has no file upload affordance for soul/instructions).
   */
  renderUploadButton?: (target: 'soul' | 'instructions', onUpload: (content: string) => void) => React.ReactNode
}

/**
 * Renders the "Behavior" form section: SOUL.md (with worker-relabelled
 * "Task prompt" copy when `isWorker`) + Additional Instructions. Heartbeat
 * is intentionally NOT included — the modal never shows it, and the profile
 * renders heartbeat only for base agents. Keeping the field set tight lets
 * the modal and profile share this exact block.
 */
export function BehaviorFields({
  isWorker,
  soul,
  onSoulChange,
  instructions,
  onInstructionsChange,
  renderUploadButton,
}: BehaviorFieldsProps) {
  return (
    <div className="space-y-5">
      {/* SOUL.md / Task prompt — relabelled for workers, optional in both tiers.
          Workers: empty is valid (per the locked concept, soul is optional).
          Base: empty at create time is also valid (the agent starts in "draft"). */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Scroll size={13} className="text-[var(--color-accent)]" />
          <p className="text-xs font-medium text-[var(--color-secondary)]">
            {isWorker ? (
              <>
                Task prompt <span className="text-[var(--color-muted)] font-normal">(optional)</span>
              </>
            ) : (
              'Personality & instructions'
            )}
          </p>
        </div>
        <p className="text-xs text-[var(--color-muted)]">
          {isWorker ? (
            <>
              Optional system prompt for the worker&apos;s runner. Composed with
              any caller-supplied task prompt at run time. Stored as{' '}
              <span className="font-mono text-[11px]">SOUL.md</span>. Leave empty
              to use the executor&apos;s default behaviour.
            </>
          ) : (
            <>
              Defines this agent&apos;s character, expertise, and behavioural
              guidelines. Stored as <span className="font-mono text-[11px]">SOUL.md</span>{' '}
              in the agent workspace.
            </>
          )}
        </p>
        <Textarea
          data-testid={isWorker ? 'worker-task-prompt' : 'agent-soul'}
          value={soul}
          onChange={(e) => onSoulChange(e.target.value)}
          placeholder={
            isWorker
              ? "# Task prompt (optional)\n\nDefine how this worker should approach its delegated task..."
              : "# Soul\n\nDefine this agent's personality, expertise, and behavioural guidelines..."
          }
          rows={6}
          className="text-xs font-mono resize-none"
          // Workers: explicitly NOT required. Empty is valid.
          required={false}
          aria-required={false}
        />
        {renderUploadButton?.('soul', onSoulChange)}
      </div>

      <Separator />

      {/* Additional Instructions — same in both tiers, optional. */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <NotePencil size={13} className="text-[var(--color-accent)]" />
          <p className="text-xs font-medium text-[var(--color-secondary)]">
            Additional Instructions
          </p>
        </div>
        <p className="text-xs text-[var(--color-muted)]">
          Extra instructions appended to the {isWorker ? "worker's" : "agent's"} context.
        </p>
        <Textarea
          value={instructions}
          onChange={(e) => onInstructionsChange(e.target.value)}
          placeholder="Add specific instructions, constraints, or domain knowledge..."
          rows={4}
          className="text-xs font-mono resize-none"
        />
        {renderUploadButton?.('instructions', onInstructionsChange)}
      </div>
    </div>
  )
}

// ── Modal title + description copy (tier-branched) ───────────────────────────

export interface AgentFormCopy {
  title: string
  description: string
  /** Test id for the modal title heading (used by tier-preset tests). */
  testId: string
  /** Submit-button label shown while the mutation is pending + the final label. */
  submitLabel: string
}

/**
 * Returns the title/description/submit-label copy for the create modal,
 * branched by tier. Single source of truth — both the modal header and any
 * test that asserts on the title go through this helper.
 */
export function getCreateAgentFormCopy(type: 'custom' | 'worker'): AgentFormCopy {
  if (type === 'worker') {
    return {
      title: 'New sub-agent worker',
      description:
        'Configure a delegation-only labour agent. Workers are invoked by other agents — they are not chat targets and never run on a schedule.',
      testId: 'create-worker-modal-title',
      submitLabel: 'Create worker',
    }
  }
  return {
    title: 'New custom agent',
    description: 'Configure a new custom agent with a persona, model, and tools.',
    testId: 'create-custom-modal-title',
    submitLabel: 'Create agent',
  }
}
