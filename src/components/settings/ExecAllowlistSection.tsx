import { useState, useEffect } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Terminal, Plus, Trash } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { fetchExecAllowlist, updateExecAllowlist } from '@/lib/api'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'

export function ExecAllowlistSection(): React.ReactElement {
  const queryClient = useQueryClient()

  const { data, isLoading, isError } = useQuery({
    queryKey: ['exec-allowlist'],
    queryFn: fetchExecAllowlist,
  })

  const [patterns, setPatterns] = useState<string[]>([])
  const [isDirty, setIsDirty] = useState(false)
  const [newPattern, setNewPattern] = useState('')
  const [addError, setAddError] = useState('')
  const [restartRequired, setRestartRequired] = useState(false)
  // Mirror-image D3 defect fix: `disabled: !isDirty` used to overload the
  // dirty flag as the useAutoSave readiness signal. Per useAutoSave.ts's
  // "skip first render" logic, whichever commit is the FIRST one where
  // `disabled` is false gets captured as the baseline ("already saved"),
  // not persisted. `handleAdd`/`handleRemove` set `patterns` AND
  // `isDirty(true)` in the SAME synchronous update, so the user's very
  // FIRST add/remove was itself the commit that flipped `disabled` false —
  // useAutoSave adopted that already-containing-the-edit state as its
  // baseline instead of saving it. `hasPendingChanges()` then read false,
  // so not even the unmount flush caught it: the first edit of a session
  // was silently never sent to the server.
  //
  // Fix: a DEDICATED readiness flag, decoupled from `isDirty`. It flips
  // true exactly once, the moment the server's `data` has been loaded into
  // `patterns` (regardless of whether the user has edited anything yet) —
  // the same "hydration readiness, not dirtiness" pattern used everywhere
  // else in this fix. `isDirty` keeps its original, separate job below
  // (blocking a background refetch from clobbering an in-progress local
  // edit) — it is no longer read by useAutoSave's `disabled` at all.
  const [patternsHydrated, setPatternsHydrated] = useState(false)

  useEffect(() => {
    if (!data || isDirty) return
    // Pull from the server response — the source of truth for what's on disk.
    setPatterns(data.allowed_binaries ?? [])
    // Reset input/error state when the query refreshes externally so the
    // component doesn't carry stale add-errors across refetches.
    setNewPattern('')
    setAddError('')
    setPatternsHydrated(true)
  }, [data, isDirty])

  const { status: saveStatus, error: saveError } = useAutoSave(
    patterns,
    async (data) => {
      const serverResp = await updateExecAllowlist(data)
      setPatterns(serverResp.allowed_binaries ?? [])
      setIsDirty(false)
      if (serverResp.restart_required) {
        setRestartRequired(true)
      }
      queryClient.setQueryData(['exec-allowlist'], serverResp)
    },
    { disabled: !patternsHydrated },
  )

  function handleAdd() {
    const trimmed = newPattern.trim()
    if (!trimmed) {
      setAddError('Pattern cannot be empty.')
      return
    }
    if (patterns.includes(trimmed)) {
      setAddError('Pattern already exists.')
      return
    }
    setPatterns((prev) => [...prev, trimmed])
    setNewPattern('')
    setAddError('')
    setIsDirty(true)
  }

  function handleRemove(pattern: string) {
    setPatterns((prev) => prev.filter((p) => p !== pattern))
    setIsDirty(true)
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') {
      e.preventDefault()
      handleAdd()
    }
  }

  if (isLoading) {
    return (
      <div className="text-sm text-[var(--color-muted)] py-2">
        Loading allowlist...
      </div>
    )
  }

  if (isError) {
    return (
      <p className="text-sm text-red-400">
        Failed to load exec allowlist. Please try again.
      </p>
    )
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-medium text-[var(--color-secondary)] flex items-center gap-1.5">
            <Terminal size={14} className="text-[var(--color-muted)]" />
            Command Binary Allowlist
            {restartRequired && (
              <span className="ml-2 text-[10px] uppercase tracking-wider text-[var(--color-warning)] border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 rounded px-1.5 py-0.5">
                Restart required
              </span>
            )}
          </h3>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            Glob patterns for binaries that bash may run.
            E.g. <span className="font-mono">git *</span>,{' '}
            <span className="font-mono">npm run *</span>. When non-empty, bash
            denies any command that does not match a pattern.
          </p>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3">
        {patterns.length === 0 ? (
          <p className="text-xs text-[var(--color-muted)] italic">
            No patterns configured. Bash runs without the binary allowlist restriction
            (existing deny-pattern safety checks still apply).
          </p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {patterns.map((pattern) => (
              <div
                key={pattern}
                className="flex items-center gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2.5 py-1"
              >
                <Badge
                  variant="secondary"
                  className="font-mono text-xs px-0 py-0 bg-transparent border-0 text-[var(--color-secondary)]"
                >
                  {pattern}
                </Badge>
                <button tabIndex={0}
                  type="button"
                  aria-label={`Remove pattern ${pattern}`}
                  onClick={() => handleRemove(pattern)}
                  className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
                >
                  <Trash size={12} />
                </button>
              </div>
            ))}
          </div>
        )}

        <div className="flex items-center gap-2 pt-1">
          <Input
            value={newPattern}
            onChange={(e) => {
              setNewPattern(e.target.value)
              setAddError('')
            }}
            onKeyDown={handleKeyDown}
            placeholder="e.g. git or npm run *"
            className="h-7 text-xs font-mono flex-1"
            aria-label="New binary pattern"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={handleAdd}
            className="h-7 px-2 gap-1 text-xs shrink-0"
          >
            <Plus size={11} weight="bold" />
            Add
          </Button>
        </div>
        {addError && (
          <p className="text-xs text-[var(--color-error)]">{addError}</p>
        )}
      </div>
    </section>
  )
}
