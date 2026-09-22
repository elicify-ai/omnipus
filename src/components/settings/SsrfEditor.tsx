import { useEffect, useId, useRef } from 'react'
import { CaretDown, CaretUp, Trash, Plus } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'

// SSRF preset definitions — also consumed by SandboxSection for the
// configData→state hydration effect and the re-auth-cancel-revert helper
// (matching the active preset against the saved server list), which is why
// this lives here and is exported rather than kept module-private.
export const SSRF_PRESETS = [
  { label: 'Block all', list: [] as string[] },
  { label: 'Allow loopback only', list: ['127.0.0.1', '::1'] },
  {
    label: 'Allow RFC1918 + loopback',
    list: ['127.0.0.1', '::1', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', 'fc00::/7'],
  },
] as const

interface SsrfEditorProps {
  list: string[]
  activePreset: number | null
  advancedOpen: boolean
  onAdvancedToggle: () => void
  onPresetClick: (idx: number) => void
  advancedErrors: Record<number, string>
  onDeleteAdvanced: (idx: number) => void
  newSsrfEntry: string
  onNewSsrfEntryChange: (v: string) => void
  onAddSsrfEntry: () => void
  ssrfAddError: string | null
}

export function SsrfEditor({
  list,
  activePreset,
  advancedOpen,
  onAdvancedToggle,
  onPresetClick,
  advancedErrors,
  onDeleteAdvanced,
  newSsrfEntry,
  onNewSsrfEntryChange,
  onAddSsrfEntry,
  ssrfAddError,
}: SsrfEditorProps) {
  const addErrorId = useId()
  const deleteButtonRefs = useRef<Array<HTMLButtonElement | null>>([])
  const newEntryInputRef = useRef<HTMLInputElement | null>(null)
  // Index the delete button was clicked at — consumed by the effect below to
  // land focus on the control that now occupies that slot (or the "add"
  // input when the list becomes empty), instead of letting focus fall
  // through to <body> when the deleted row's button unmounts.
  const pendingDeleteFocusRef = useRef<number | null>(null)

  useEffect(() => {
    if (pendingDeleteFocusRef.current === null) return
    const idx = pendingDeleteFocusRef.current
    pendingDeleteFocusRef.current = null
    if (list.length === 0) {
      newEntryInputRef.current?.focus()
    } else {
      const nextIdx = Math.min(idx, list.length - 1)
      deleteButtonRefs.current[nextIdx]?.focus()
    }
  }, [list])

  function handleDeleteAdvanced(idx: number) {
    pendingDeleteFocusRef.current = idx
    onDeleteAdvanced(idx)
  }

  return (
    <div className="space-y-[var(--space-2)] border-t border-[var(--color-border)] pt-[var(--space-2-5)]">
      <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">
        SSRF internal-network policy
      </p>

      <SegmentedControl
        aria-label="SSRF internal-network preset"
        value={activePreset === null ? '' : String(activePreset)}
        onValueChange={(value) => onPresetClick(Number(value))}
        className="h-auto flex-wrap gap-[var(--space-2)] border-0 bg-transparent p-0"
      >
        {SSRF_PRESETS.map((preset, idx) => (
          <SegmentedControlItem
            key={preset.label}
            value={String(idx)}
            className={[
              'h-auto rounded border px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)] shadow-none',
              activePreset === idx
                ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)] hover:bg-[var(--color-accent)]/10 hover:text-[var(--color-accent)]'
                : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:border-[var(--color-accent)]/50 hover:bg-[var(--color-surface-2)] hover:text-[var(--color-muted)]',
            ].join(' ')}
          >
            {preset.label}
          </SegmentedControlItem>
        ))}
      </SegmentedControl>

      <Button
        variant="ghost"
        type="button"
        onClick={onAdvancedToggle}
        className="h-auto w-auto gap-[var(--space-1)] p-0 text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
        aria-expanded={advancedOpen}
      >
        {advancedOpen ? <CaretUp size={10} /> : <CaretDown size={10} />}
        Advanced (custom list)
      </Button>

      {advancedOpen && (
        <div className="space-y-[var(--space-1)] pl-[var(--space-2-5)] border-l border-[var(--color-border)]">
          {list.length === 0 && (
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] italic">Empty — all internal traffic blocked.</p>
          )}
          {list.map((entry, i) => {
            const entryErrorId = advancedErrors[i] ? `ssrf-entry-error-${i}` : undefined
            return (
              <div key={i} className="flex flex-col gap-[var(--space-0-5)]">
                <div className="flex items-center gap-[var(--space-2)] rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-[var(--space-2)] py-[var(--space-1)]">
                  <span className="flex-1 text-[length:var(--type-utility-xs-size)] font-mono text-[var(--color-secondary)] break-all">
                    {entry}
                  </span>
                  <IconButton
                    variant="ghost"
                    ref={(el) => { deleteButtonRefs.current[i] = el }}
                    type="button"
                    aria-label={`Delete SSRF entry ${entry}`}
                    aria-describedby={entryErrorId}
                    className="h-auto w-auto p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-error)]"
                    onClick={() => handleDeleteAdvanced(i)}
                  >
                    <Trash size={12} />
                  </IconButton>
                </div>
                {entryErrorId && (
                  <p id={entryErrorId} className="text-[length:var(--type-caption-size)] text-[var(--color-error)] pl-[var(--space-2)]">{advancedErrors[i]}</p>
                )}
              </div>
            )
          })}

          <div className="space-y-[var(--space-1)] pt-[var(--space-1)]">
            <div className="flex items-center gap-[var(--space-2)]">
              <Input
                ref={newEntryInputRef}
                value={newSsrfEntry}
                onChange={(e) => onNewSsrfEntryChange(e.target.value)}
                placeholder="10.0.0.0/8 or internal.corp"
                className="h-7 text-[length:var(--type-utility-xs-size)] font-mono flex-1"
                aria-label="New SSRF allow entry"
                aria-invalid={ssrfAddError ? true : undefined}
                aria-describedby={ssrfAddError ? addErrorId : undefined}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); onAddSsrfEntry() }
                }}
              />
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] shrink-0"
                onClick={onAddSsrfEntry}
                aria-label="Add SSRF entry"
              >
                <Plus size={11} />
                Add
              </Button>
            </div>
            {ssrfAddError && (
              <p id={addErrorId} className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{ssrfAddError}</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
