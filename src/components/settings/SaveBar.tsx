// SaveBar — the Settings Save bar (Settings redesign #1519, FR-010 to
// FR-020) and the plain setting rows a simple section is made of.
//
// Three pieces:
//   - SaveBarPanel: the floating bar itself — "N unsaved changes", how many
//     need a restart or the password, Discard and Save changes; the four
//     outcome lines; "Saved at HH:MM" for 8 seconds. A screen layout
//     composed from catalogued components (Card-like surface, Button), not a
//     new kit component (spec "Design-System Components").
//   - SettingRow: one labelled control (select, number, text, switch) with its
//     Needs restart / Password badges and its per-field message.
//   - SaveBar: a whole simple section — its rows plus the panel — driven by
//     useSaveBar. Sections with richer controls use useSaveBar, SettingRow and
//     SaveBarPanel directly.

import { ArrowClockwise, Lock, Warning } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import { useSettingsShell } from './SettingsShellContext'
import {
  fieldDomId,
  plural,
  useSaveBar,
  type SaveBarField,
  type SaveBarPanelProps,
  type SaveBarState,
  type UseSaveBarOptions,
} from './useSaveBar'

export function RestartBadge() {
  return (
    <Badge variant="outline" className="gap-[var(--space-1)] text-[var(--color-warning)]" title="Saved changes to this setting take effect after a gateway restart">
      <ArrowClockwise size={12} aria-hidden="true" />
      Needs restart
    </Badge>
  )
}

export function PasswordBadge() {
  return (
    <Badge variant="secondary" className="gap-[var(--space-1)]" title="Saving this setting asks for your password">
      <Lock size={12} aria-hidden="true" />
      Password
    </Badge>
  )
}

export function SaveBarPanel({
  visible,
  count,
  restartCount,
  passwordCount,
  invalidCount,
  failedCount,
  unknownCount,
  saving,
  savedAt,
  onSave,
  onDiscard,
  onRetry,
}: SaveBarPanelProps) {
  if (!visible) return null
  if (count === 0 && unknownCount === 0) {
    return (
      <div
        data-testid="settings-save-bar"
        className="sticky bottom-[var(--space-3)] z-10 mt-[var(--space-4)] flex items-center justify-end rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)] shadow-[var(--elevation-floating)]"
      >
        <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Saved at {savedAt}</span>
      </div>
    )
  }
  let headline = plural(count, 'unsaved change', 'unsaved changes')
  let detail: string
  if (unknownCount > 0) {
    headline = `${plural(unknownCount, 'change', 'changes')} may not have been saved`
    detail = 'The gateway did not confirm the save. Check that it is running, then retry.'
  } else if (failedCount > 0) {
    headline = `${plural(failedCount, 'change', 'changes')} could not be saved`
    detail = 'The reason is shown under each setting.'
  } else if (invalidCount > 0) {
    detail = `Fix ${plural(invalidCount, 'value', 'values')} before saving`
  } else {
    detail =
      [
        restartCount ? `${restartCount} ${restartCount === 1 ? 'needs' : 'need'} a gateway restart` : '',
        passwordCount ? `${passwordCount} ${passwordCount === 1 ? 'needs' : 'need'} your password` : '',
      ]
        .filter(Boolean)
        .join(' · ') || 'Nothing here needs a restart.'
  }
  return (
    <div
      data-testid="settings-save-bar"
      className="sticky bottom-[var(--space-3)] z-10 mt-[var(--space-4)] flex flex-wrap items-center justify-between gap-[var(--space-2)] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)] shadow-[var(--elevation-floating)]"
    >
      <div aria-live="polite" className="min-w-0">
        <div className="font-semibold text-[var(--color-secondary)]">{headline}</div>
        <div
          id="settings-save-bar-detail"
          className={cn(
            'text-[length:var(--type-body-compact-size)]',
            invalidCount > 0 && unknownCount === 0 && failedCount === 0 ? 'text-[var(--color-error)]' : 'text-[var(--color-muted)]',
          )}
        >
          {detail}
        </div>
      </div>
      <div className="flex items-center gap-[var(--space-2)]">
        {unknownCount > 0 && (
          <Button variant="outline" onClick={() => void onRetry()}>
            Retry
          </Button>
        )}
        <Button variant="ghost" onClick={onDiscard} disabled={saving}>
          Discard
        </Button>
        <Button
          variant="default"
          onClick={() => void onSave()}
          aria-disabled={invalidCount > 0 || saving ? true : undefined}
          aria-describedby={invalidCount > 0 ? 'settings-save-bar-detail' : undefined}
        >
          Save changes
        </Button>
      </div>
    </div>
  )
}

export interface SettingRowProps { // not-wire-format: React props
  state: SaveBarState
  field: SaveBarField
  /** Replaces the default control; receives the Field's control props. */
  children?: (control: { id: string; fieldId: string; value: SaveBarField['value']; setValue: (v: SaveBarField['value']) => void; describedBy?: string; invalid: boolean }) => React.ReactNode
}

export function SettingRow({ state, field, children }: SettingRowProps) {
  const id = fieldDomId(state.section, field.key)
  const value = state.value(field.key)
  const msg = state.message(field.key)
  const label = (
    <span className="inline-flex flex-wrap items-center gap-[var(--space-2)]">
      {field.label}
      {field.restart && <RestartBadge />}
      {field.password && <PasswordBadge />}
    </span>
  )
  const setValue = (v: SaveBarField['value']) => state.setDraft(field.key, v)
  return (
    <div className="py-[var(--space-2)]" data-setting-key={field.key}>
      <Field
        label={label}
        description={field.description}
        error={msg?.tone === 'error' ? msg.text : undefined}
      >
        {(cp) => {
          const controlId = cp.id ?? id
          if (children) {
            return children({ id: controlId, fieldId: id, value, setValue, describedBy: cp['aria-describedby'], invalid: !!cp['aria-invalid'] })
          }
          switch (field.kind) {
            case 'switch':
              return (
                <Switch
                  id={controlId}
                  data-settings-field={id}
                  checked={value === true}
                  onCheckedChange={(c) => setValue(c)}
                  disabled={field.disabled}
                  aria-describedby={cp['aria-describedby']}
                />
              )
            case 'select':
              return (
                <select tabIndex={0}
                  id={controlId}
                  data-settings-field={id}
                  value={value == null ? '' : String(value)}
                  onChange={(e) => setValue(e.target.value)}
                  disabled={field.disabled}
                  aria-describedby={cp['aria-describedby']}
                  aria-invalid={cp['aria-invalid']}
                  className="flex h-9 w-full max-w-xs rounded-md border border-[var(--color-border)] bg-transparent px-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]"
                >
                  {(field.options ?? []).map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              )
            default:
              return (
                <Input
                  id={controlId}
                  data-settings-field={id}
                  type={field.kind === 'number' ? 'number' : 'text'}
                  inputMode={field.kind === 'number' ? 'numeric' : undefined}
                  value={value == null ? '' : String(value)}
                  onChange={(e) => setValue(e.target.value)}
                  disabled={field.disabled}
                  aria-describedby={cp['aria-describedby']}
                  aria-invalid={cp['aria-invalid']}
                  className="max-w-xs"
                />
              )
          }
        }}
      </Field>
      {msg?.tone === 'warning' && (
        <div className="mt-[var(--space-1)] flex items-start gap-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]">
          <Warning size={14} aria-hidden="true" className="mt-[var(--space-0-5)] shrink-0" />
          <div>
            <div>{msg.text}</div>
            {msg.keys && msg.keys.length > 0 && (
              <div className="text-[var(--color-muted)]">Not applied: {msg.keys.join(', ')}</div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

export type SaveBarProps = UseSaveBarOptions // not-wire-format: React props

/** A simple section: its setting rows plus the Save bar. */
export function SaveBar(options: SaveBarProps) {
  const state = useSaveBar(options)
  return (
    <div>
      {options.fields.map((f) => (
        <SettingRow key={f.key} state={state} field={f} />
      ))}
      <SaveBarPanel {...state.bar} />
    </div>
  )
}

/** Variant for a primary section action while the Save bar shows (FR-017). */
export function usePrimaryVariant(): 'default' | 'outline' {
  return useSettingsShell().saveBarVisible ? 'outline' : 'default'
}

export default SaveBar
