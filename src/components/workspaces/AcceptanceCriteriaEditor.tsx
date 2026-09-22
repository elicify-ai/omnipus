import { useState } from 'react'
import { Plus, Trash } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { formatVerifiesVia } from '@/components/shared/CriteriaBreakdown'
import type { AcceptanceCriterion } from '@/lib/api'

type BehaviorScope = NonNullable<AcceptanceCriterion['behavior']>['scope']
type Judgment = AcceptanceCriterion['judgment']

const DEFAULT_INPUT_ARIA_LABEL = 'What must be true when this is done?'

interface AcceptanceCriteriaEditorProps {
  criteria: AcceptanceCriterion[]
  onChange: (criteria: AcceptanceCriterion[]) => void
  /** Author identity stamped on newly-added criteria (ADR-049 D2 rule 3 — mandatory). */
  currentAuthor: { kind: 'agent' | 'user'; id: string }
  /** Shown when the collection is empty (D5 soft-tier hint for human/UI creation). */
  emptyHint?: string
  /**
   * Overrides the accessible name of the plain-language text input.
   * `DefinitionOfDoneEditor.tsx` sets this to "Definition of Done item"
   * (matches `task-form-criteria-dod-demo.html`) so that when Acceptance
   * criteria and Definition of Done both render on the same form, their two
   * text inputs are not indistinguishable to a screen reader — the visible
   * placeholder text stays identical in both places. Defaults to the
   * original wording.
   */
  inputAriaLabel?: string
}

/**
 * Integer-parse helper shared by the exit-code and count validators.
 *
 * `parseInt` silently truncates a fractional string ("3.5" -> 3) instead of
 * rejecting it (planning-goals-spec.md "Dataset: Criteria editor validation"
 * #2), so `Number(...)` + `Number.isInteger` rejects both non-numeric ("abc")
 * and non-integer ("3.5") input; the empty string coerces to `0` under
 * `Number('')`, so callers check emptiness first.
 */
function parseIntStrict(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '') return null
  const n = Number(trimmed)
  return Number.isInteger(n) ? n : null
}

/**
 * Definition-of-Done criteria editor — judgment-first (ADR-074 D5.1, spec
 * US-7; extends ADR-049 D2/D5/FR-3, SD-C13). Reused by Create Task, Task
 * detail, and the Create/Edit Plan slide-over's DoD editor.
 *
 * The primary input is a single plain-language field ("What must be true when
 * this is done?"). A criterion added with nothing else attached is `prose` —
 * there is NO kind selector as the lead control. Two quiet expanders,
 * "+ Add technical check" and "+ Add action-count check", optionally attach a
 * `check` (command + expected exit code) or `behavior` (tool + min/max count +
 * scope) payload to the criterion being added. Added criteria render as
 * cards: text primary, a mono "verifies via:" chip when a payload is
 * attached, and the author stamp. No kind classification label is shown
 * (spec §4: no user-facing `[kind]` tokens).
 *
 * ADR-080 D-TYPES: every criterion also carries a REQUIRED `judgment` (what
 * SHAPE of claim it is — `boolean`/`quantitative`/`artifact` — orthogonal to
 * `kind`, which mechanism verifies it). This is the human authoring surface,
 * so a small "Judgment" selector lets the author set it explicitly; a
 * criterion added with no interaction defaults to `boolean` (the catch-all
 * for yes/no and honestly-subjective outcomes), matching the server's own
 * `InferJudgment` default for `prose`.
 *
 * The judgment selector is COUPLED to the payload expander so the author can
 * never build a kind/judgment pair the server's `InferJudgment`
 * (pkg/task/criterion.go) hard-rejects: a technical check (`kind: 'check'`)
 * is always `boolean`, an action-count check (`kind: 'behavior'`) is always
 * `quantitative` — the selector is locked to that value and disabled while
 * either expander is open, with a hint explaining why. Only a plain
 * (`prose`) criterion keeps the free boolean/quantitative/artifact choice.
 */
export function AcceptanceCriteriaEditor({ criteria, onChange, currentAuthor, emptyHint, inputAriaLabel }: AcceptanceCriteriaEditorProps) {
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  // Which payload expander is open — at most one; null = plain prose add.
  const [expander, setExpander] = useState<'check' | 'behavior' | null>(null)
  // Technical-check payload fields.
  const [command, setCommand] = useState('')
  const [exitCode, setExitCode] = useState('0')
  // Action-count payload fields. Min defaults to 1 (the wire default); an
  // explicit '0' is preserved as `min_count: 0` (with max 0 = "never call
  // this tool" — ADR-052 FR-034). Max left empty = ABSENT (no upper bound),
  // never coerced to 0.
  const [tool, setTool] = useState('')
  const [minCount, setMinCount] = useState('1')
  const [maxCount, setMaxCount] = useState('')
  const [scope, setScope] = useState<BehaviorScope>('task_session')
  // ADR-080 D-TYPES — what SHAPE of claim this criterion is. Defaults to the
  // catch-all `boolean`; the author picks a different value explicitly. Only
  // meaningful for `prose` — a technical expander overrides it, see below.
  const [judgment, setJudgment] = useState<Judgment>('boolean')

  // A technical check is always boolean, an action-count check is always
  // quantitative (matches task.InferJudgment's hard rule) — locked/non-null
  // whenever a payload expander is open. `null` while authoring plain prose,
  // where the free selector applies.
  const lockedJudgment: Judgment | null =
    expander === 'check' ? 'boolean' : expander === 'behavior' ? 'quantitative' : null
  const effectiveJudgment: Judgment = lockedJudgment ?? judgment

  function toggleExpander(next: 'check' | 'behavior') {
    setExpander((cur) => (cur === next ? null : next))
    setError('')
  }

  function addCriterion() {
    const trimmedText = text.trim()
    if (!trimmedText) {
      setError('Criterion text is required')
      return
    }

    if (expander === 'check') {
      if (!command.trim()) {
        setError('Command is required')
        return
      }
      const code = parseIntStrict(exitCode)
      if (code === null) {
        setError('Exit code must be an integer')
        return
      }
      // FR-015: "A check criterion MUST require ... expected_exit_code ∈
      // [0,255]" — enforced client-side too so an out-of-range value is
      // caught inline instead of round-tripping to a 400 from the backend.
      if (code < 0 || code > 255) {
        setError('Exit code must be between 0 and 255')
        return
      }
      onChange([
        ...criteria,
        {
          kind: 'check',
          judgment: effectiveJudgment,
          text: trimmedText,
          check: { command: command.trim(), expected_exit_code: code },
          author: currentAuthor,
          status: 'pending',
        },
      ])
      setCommand('')
      setExitCode('0')
    } else if (expander === 'behavior') {
      if (!tool.trim()) {
        setError('Tool name is required')
        return
      }
      const min = parseIntStrict(minCount)
      if (min === null || min < 0) {
        setError('Min count must be a non-negative integer')
        return
      }
      let max: number | undefined
      if (maxCount.trim() !== '') {
        const parsed = parseIntStrict(maxCount)
        if (parsed === null || parsed < 0) {
          setError('Max count must be a non-negative integer')
          return
        }
        if (parsed < min) {
          setError('Max count must be greater than or equal to min count')
          return
        }
        max = parsed
      }
      onChange([
        ...criteria,
        {
          kind: 'behavior',
          judgment: effectiveJudgment,
          text: trimmedText,
          behavior: {
            tool: tool.trim(),
            min_count: min,
            ...(max !== undefined ? { max_count: max } : {}),
            scope,
          },
          author: currentAuthor,
          status: 'pending',
        },
      ])
      setTool('')
      setMinCount('1')
      setMaxCount('')
      setScope('task_session')
    } else {
      onChange([
        ...criteria,
        { kind: 'prose', judgment: effectiveJudgment, text: trimmedText, author: currentAuthor, status: 'pending' },
      ])
    }
    setError('')
    setText('')
    setExpander(null)
    setJudgment('boolean')
  }

  function removeCriterion(idx: number) {
    onChange(criteria.filter((_, i) => i !== idx))
  }

  return (
    <div className="flex flex-col gap-[var(--space-2)]">
      {criteria.length === 0 && emptyHint && (
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{emptyHint}</p>
      )}

      {criteria.length > 0 && (
        <ul className="space-y-[var(--space-1)]">
          {criteria.map((c, idx) => {
            const verifiesVia = formatVerifiesVia(c)
            return (
              <li
                key={c.id ?? idx}
                className="flex items-start gap-[var(--space-2)] px-[var(--space-2)] py-[var(--space-1)] rounded-md bg-[var(--color-surface-2)] text-[length:var(--type-utility-xs-size)]"
              >
                <div className="flex-1 min-w-0 space-y-[var(--space-0-5)]">
                  <div className="flex items-start gap-[var(--space-1)]">
                    {c.judgment && (
                      <span
                        data-testid="criterion-judgment-badge"
                        className="mt-[var(--border-width-hairline)] shrink-0 rounded border border-[var(--color-border)] px-[var(--space-1)] py-[var(--border-width-hairline)] text-[length:var(--type-caption-size)] uppercase tracking-wide text-[var(--color-muted)]"
                      >
                        {c.judgment}
                      </span>
                    )}
                    <p className="text-[var(--color-secondary)] flex-1 min-w-0">{c.text}</p>
                  </div>
                  {verifiesVia && (
                    <p className="inline-flex max-w-full items-baseline gap-[var(--space-1)] rounded bg-[var(--color-surface-1)] px-[var(--space-1)] py-[var(--space-0-5)] font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                      <span className="shrink-0">verifies via:</span>
                      <span className="truncate">{verifiesVia}</span>
                    </p>
                  )}
                  <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                    by {c.author.kind}:{c.author.id}
                  </p>
                </div>
                <IconButton
                  onClick={() => removeCriterion(idx)}
                  aria-label={`Remove criterion ${c.text}`}
                  variant="ghost"
                  size="sm"
                  className="h-auto w-auto shrink-0 p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-error)]"
                >
                  <Trash size={12} />
                </IconButton>
              </li>
            )
          })}
        </ul>
      )}

      <div className="flex flex-col gap-[var(--space-1)] rounded-md border border-dashed border-[var(--color-border)] p-[var(--space-2)]">
        <Input
          aria-label={inputAriaLabel ?? DEFAULT_INPUT_ARIA_LABEL}
          value={text}
          onChange={(e) => { setText(e.target.value); setError('') }}
          placeholder="What must be true when this is done?"
          maxLength={1000}
          className="text-[length:var(--type-utility-xs-size)]"
        />

        <div className="flex items-center gap-[var(--space-2-5)]">
          <Button
            variant="ghost"
            aria-expanded={expander === 'check'}
            onClick={() => toggleExpander('check')}
            className="h-auto p-0 text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
          >
            + Add technical check
          </Button>
          <Button
            variant="ghost"
            aria-expanded={expander === 'behavior'}
            onClick={() => toggleExpander('behavior')}
            className="h-auto p-0 text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
          >
            + Add action-count check
          </Button>
        </div>

        {/* ADR-080 D-TYPES — judgment selector. Every criterion carries a
            required judgment (what SHAPE of claim it is); this is the human
            authoring surface, so the author picks it explicitly. Defaults to
            `boolean` (the catch-all for yes/no and subjective outcomes).
            Locked + disabled while a technical-check or action-count
            expander is open — task.InferJudgment hard-rejects any other
            pairing, so the mismatch can never be built in the first place. */}
        <div className="flex items-center gap-[var(--space-2)]">
          <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Judgment</span>
          <Select
            value={effectiveJudgment}
            disabled={lockedJudgment !== null}
            onValueChange={(v) => { setJudgment(v as Judgment); setError('') }}
          >
            <SelectTrigger aria-label="Judgment" className="h-8 text-[length:var(--type-utility-xs-size)] w-40 bg-[var(--color-surface-1)] border-[var(--color-border)] text-[var(--color-secondary)]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="boolean" className="text-[length:var(--type-utility-xs-size)]">Boolean (yes/no)</SelectItem>
              <SelectItem value="quantitative" className="text-[length:var(--type-utility-xs-size)]">Quantitative (value vs. threshold)</SelectItem>
              <SelectItem value="artifact" className="text-[length:var(--type-utility-xs-size)]">Artifact (a thing exists)</SelectItem>
            </SelectContent>
          </Select>
          {lockedJudgment === 'boolean' && (
            <span className="text-[length:var(--type-caption-size)] italic text-[var(--color-muted)]">checks are always boolean</span>
          )}
          {lockedJudgment === 'quantitative' && (
            <span className="text-[length:var(--type-caption-size)] italic text-[var(--color-muted)]">action-count checks are always quantitative</span>
          )}
        </div>

        {expander === 'check' && (
          <div className="flex items-center gap-[var(--space-2)]">
            <Input
              aria-label="Command"
              value={command}
              onChange={(e) => { setCommand(e.target.value); setError('') }}
              placeholder="go test ./... -run TestX"
              maxLength={2000}
              className="text-[length:var(--type-utility-xs-size)] font-mono flex-1"
            />
            <Input
              aria-label="Expected exit code"
              type="number"
              value={exitCode}
              onChange={(e) => { setExitCode(e.target.value); setError('') }}
              className="text-[length:var(--type-utility-xs-size)] w-24"
            />
          </div>
        )}

        {expander === 'behavior' && (
          <div className="flex flex-wrap items-center gap-[var(--space-2)]">
            <Input
              aria-label="Tool name"
              value={tool}
              onChange={(e) => { setTool(e.target.value); setError('') }}
              placeholder="search_web"
              maxLength={200}
              className="text-[length:var(--type-utility-xs-size)] font-mono flex-1 min-w-32"
            />
            <Input
              aria-label="Min count"
              type="number"
              value={minCount}
              onChange={(e) => { setMinCount(e.target.value); setError('') }}
              className="text-[length:var(--type-utility-xs-size)] w-20"
            />
            <Input
              aria-label="Max count"
              type="number"
              value={maxCount}
              onChange={(e) => { setMaxCount(e.target.value); setError('') }}
              placeholder="no max"
              className="text-[length:var(--type-utility-xs-size)] w-20"
            />
            <Select value={scope} onValueChange={(v) => { setScope(v as BehaviorScope); setError('') }}>
              <SelectTrigger aria-label="Count scope" className="h-8 text-[length:var(--type-utility-xs-size)] w-32 bg-[var(--color-surface-1)] border-[var(--color-border)] text-[var(--color-secondary)]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="task_session" className="text-[length:var(--type-utility-xs-size)]">Whole session</SelectItem>
                <SelectItem value="attempt" className="text-[length:var(--type-utility-xs-size)]">Per attempt</SelectItem>
              </SelectContent>
            </Select>
          </div>
        )}

        {error && <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]">{error}</p>}
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-8 gap-[var(--space-1)] self-start"
          onClick={addCriterion}
        >
          <Plus size={12} /> Add criterion
        </Button>
      </div>
    </div>
  )
}
