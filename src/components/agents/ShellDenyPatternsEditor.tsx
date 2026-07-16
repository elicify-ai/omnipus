/**
 * ShellDenyPatternsEditor — textarea (one regex per line) for shell deny patterns.
 *
 * - Validates each non-empty line with `new RegExp(line)`.
 * - Lines with invalid regex render with a red highlight + error message.
 * - Empty lines are stripped on commit.
 * - Parent receives the array immediately on change (backend revalidates).
 */

import { Textarea } from '@/components/ui/textarea'

interface Props {
  value: string[]
  onChange: (next: string[]) => void
}

interface LineState {
  raw: string
  error: string | null
}

function validateLine(line: string): string | null {
  if (!line.trim()) return null // empty lines are stripped, not invalid
  try {
    new RegExp(line)
    return null
  } catch (e) {
    return e instanceof Error ? e.message : 'Invalid regular expression'
  }
}

export function ShellDenyPatternsEditor({ value, onChange }: Props) {
  // Local textarea text so the user can type freely; we parse on change.
  const text = value.join('\n')

  function handleChange(raw: string) {
    // Split, strip trailing empty lines only on commit, but preserve mid-edit blank lines
    const lines = raw.split('\n')
    onChange(lines)
  }

  // Build per-line validation for display
  const lines = text.split('\n')
  const lineStates: LineState[] = lines.map((line) => ({
    raw: line,
    error: validateLine(line),
  }))

  const hasErrors = lineStates.some((l) => l.error !== null)
  const errorLines = lineStates.filter((l) => l.error !== null)

  return (
    <div className="space-y-2">
      {/* Info banner */}
      <div className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2">
        <span className="text-[10px] text-[var(--color-muted)] leading-relaxed">
          Patterns matching command text will be blocked. Empty list = no enforcement.
        </span>
      </div>

      {/* Textarea */}
      <Textarea
        value={text}
        onChange={(e) => handleChange(e.target.value)}
        placeholder={"^rm -rf\n.*--force.*\n# one pattern per line"}
        rows={6}
        data-testid="shell-deny-patterns-textarea"
        className={[
          'w-full rounded-md border bg-[var(--color-surface-1)] px-3 py-2 text-xs font-mono',
          'text-[var(--color-secondary)] placeholder:text-[var(--color-muted)]',
          'resize-y focus:outline-none',
          'transition-colors',
          hasErrors
            ? 'border-[var(--color-error)]/60'
            : 'border-[var(--color-border)]',
        ].join(' ')}
        spellCheck={false}
        aria-label="Shell deny patterns, one regex per line"
        aria-invalid={hasErrors}
        aria-describedby={hasErrors ? 'shell-deny-errors' : undefined}
      />

      {/* Per-line error messages */}
      {hasErrors && (
        <div id="shell-deny-errors" className="space-y-1" role="alert">
          {errorLines.map((l, idx) => (
            <p
              key={idx}
              className="text-[10px] text-[var(--color-error)] font-mono"
              data-testid="shell-deny-pattern-error"
            >
              Line &ldquo;{l.raw}&rdquo;: {l.error}
            </p>
          ))}
        </div>
      )}
    </div>
  )
}
