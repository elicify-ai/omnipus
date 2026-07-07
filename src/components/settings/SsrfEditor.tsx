import { CaretDown, CaretUp, Trash, Plus } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

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
  return (
    <div className="space-y-2 border-t border-[var(--color-border)] pt-3">
      <p className="text-xs font-semibold text-[var(--color-secondary)]">
        SSRF internal-network policy
      </p>

      <div className="flex flex-wrap gap-2">
        {SSRF_PRESETS.map((preset, idx) => (
          <button
            key={preset.label}
            type="button"
            onClick={() => onPresetClick(idx)}
            className={[
              'rounded border px-3 py-1 text-xs transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] cursor-pointer',
              activePreset === idx
                ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
                : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:border-[var(--color-accent)]/50',
            ].join(' ')}
            aria-pressed={activePreset === idx}
          >
            {preset.label}
          </button>
        ))}
      </div>

      <button
        type="button"
        onClick={onAdvancedToggle}
        className="flex items-center gap-1 text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors focus:outline-none"
        aria-expanded={advancedOpen}
      >
        {advancedOpen ? <CaretUp size={10} /> : <CaretDown size={10} />}
        Advanced (custom list)
      </button>

      {advancedOpen && (
        <div className="space-y-1 pl-3 border-l border-[var(--color-border)]">
          {list.length === 0 && (
            <p className="text-xs text-[var(--color-muted)] italic">Empty — all internal traffic blocked.</p>
          )}
          {list.map((entry, i) => (
            <div key={i} className="flex flex-col gap-0.5">
              <div className="flex items-center gap-2 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1">
                <span className="flex-1 text-xs font-mono text-[var(--color-secondary)] break-all">
                  {entry}
                </span>
                <button
                  type="button"
                  aria-label={`Delete SSRF entry ${entry}`}
                  className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
                  onClick={() => onDeleteAdvanced(i)}
                >
                  <Trash size={12} />
                </button>
              </div>
              {advancedErrors[i] && (
                <p className="text-[10px] text-[var(--color-error)] pl-2">{advancedErrors[i]}</p>
              )}
            </div>
          ))}

          <div className="space-y-1 pt-1">
            <div className="flex items-center gap-2">
              <Input
                value={newSsrfEntry}
                onChange={(e) => onNewSsrfEntryChange(e.target.value)}
                placeholder="10.0.0.0/8 or internal.corp"
                className="h-7 text-xs font-mono flex-1"
                aria-label="New SSRF allow entry"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); onAddSsrfEntry() }
                }}
              />
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-7 px-2 gap-1 text-xs shrink-0"
                onClick={onAddSsrfEntry}
                aria-label="Add SSRF entry"
              >
                <Plus size={11} />
                Add
              </Button>
            </div>
            {ssrfAddError && (
              <p className="text-[10px] text-[var(--color-error)]">{ssrfAddError}</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
