import { useState } from 'react'
import { Plus, Trash } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { AcceptanceCriterion } from '@/lib/api'

type CriterionKind = AcceptanceCriterion['kind']

interface AcceptanceCriteriaEditorProps {
  criteria: AcceptanceCriterion[]
  onChange: (criteria: AcceptanceCriterion[]) => void
  /** Author identity stamped on newly-added criteria (ADR-049 D2 rule 3 — mandatory). */
  currentAuthor: { kind: 'agent' | 'user'; id: string }
  /** Shown when the collection is empty (D5 soft-tier hint for human/UI creation). */
  emptyHint?: string
}

/**
 * Definition-of-Done criteria editor (ADR-049 D2/D5/FR-3, SD-C13). Reused by
 * Create Task, Task detail, and the Create/Edit Plan slide-over's DoD editor
 * — reuses the removable-row + add-button grammar of the existing
 * todos/dependencies editors (`CreateTaskSlideOver.tsx:593-643`).
 *
 * `kind: check` reveals command + expected-exit-code fields; `kind: prose`
 * reveals a single text field. Each saved criterion shows its author identity
 * as a read-only label.
 */
export function AcceptanceCriteriaEditor({ criteria, onChange, currentAuthor, emptyHint }: AcceptanceCriteriaEditorProps) {
  const [kind, setKind] = useState<CriterionKind>('check')
  const [text, setText] = useState('')
  const [command, setCommand] = useState('')
  const [exitCode, setExitCode] = useState('0')
  const [error, setError] = useState('')

  function addCriterion() {
    const trimmedText = text.trim()
    if (!trimmedText) {
      setError(kind === 'check' ? 'A description of what the check verifies is required' : 'Criterion text is required')
      return
    }
    if (kind === 'check') {
      if (!command.trim()) {
        setError('Command is required')
        return
      }
      const code = parseInt(exitCode, 10)
      if (!Number.isFinite(code)) {
        setError('Exit code must be an integer')
        return
      }
      onChange([
        ...criteria,
        {
          kind: 'check',
          text: trimmedText,
          check: { command: command.trim(), expected_exit_code: code },
          author: currentAuthor,
          status: 'pending',
        },
      ])
      setCommand('')
      setExitCode('0')
    } else {
      onChange([
        ...criteria,
        { kind: 'prose', text: trimmedText, author: currentAuthor, status: 'pending' },
      ])
    }
    setError('')
    setText('')
  }

  function removeCriterion(idx: number) {
    onChange(criteria.filter((_, i) => i !== idx))
  }

  return (
    <div className="flex flex-col gap-2">
      {criteria.length === 0 && emptyHint && (
        <p className="text-xs text-[var(--color-muted)]">{emptyHint}</p>
      )}

      {criteria.length > 0 && (
        <ul className="space-y-1.5">
          {criteria.map((c, idx) => (
            <li
              key={c.id ?? idx}
              className="flex items-start gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs"
            >
              <div className="flex-1 min-w-0">
                <p className="text-[var(--color-secondary)]">
                  <span className="uppercase text-[9px] font-semibold text-[var(--color-muted)] mr-1">
                    {c.kind}
                  </span>
                  {c.text}
                </p>
                {c.kind === 'check' && c.check && (
                  <p className="font-mono text-[10px] text-[var(--color-muted)] truncate">
                    $ {c.check.command} (exit {c.check.expected_exit_code})
                  </p>
                )}
                <p className="text-[10px] text-[var(--color-muted)]">
                  by {c.author.kind}:{c.author.id}
                </p>
              </div>
              <button tabIndex={0}
                type="button"
                onClick={() => removeCriterion(idx)}
                aria-label={`Remove criterion ${c.text}`}
                className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
              >
                <Trash size={12} />
              </button>
            </li>
          ))}
        </ul>
      )}

      <div className="flex flex-col gap-1.5 rounded-md border border-dashed border-[var(--color-border)] p-2">
        <div className="flex items-center gap-2">
          <Select value={kind} onValueChange={(v) => { setKind(v as CriterionKind); setError('') }}>
            <SelectTrigger aria-label="Criterion kind" className="h-8 text-xs w-28 bg-[var(--color-surface-1)] border-[var(--color-border)] text-[var(--color-secondary)]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="check" className="text-xs">Check</SelectItem>
              <SelectItem value="prose" className="text-xs">Prose</SelectItem>
            </SelectContent>
          </Select>
          <Input
            aria-label={kind === 'check' ? 'Check description' : 'Criterion text'}
            value={text}
            onChange={(e) => { setText(e.target.value); setError('') }}
            placeholder={kind === 'check' ? 'What does this check verify?' : 'Criterion statement'}
            maxLength={500}
            className="text-xs flex-1"
          />
        </div>
        {kind === 'check' && (
          <div className="flex items-center gap-2">
            <Input
              aria-label="Command"
              value={command}
              onChange={(e) => { setCommand(e.target.value); setError('') }}
              placeholder="go test ./... -run TestX"
              maxLength={2000}
              className="text-xs font-mono flex-1"
            />
            <Input
              aria-label="Expected exit code"
              type="number"
              value={exitCode}
              onChange={(e) => { setExitCode(e.target.value); setError('') }}
              className="text-xs w-24"
            />
          </div>
        )}
        {error && <p className="text-xs text-[var(--color-error)]">{error}</p>}
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-8 gap-1 self-start"
          onClick={addCriterion}
        >
          <Plus size={12} /> Add criterion
        </Button>
      </div>
    </div>
  )
}
