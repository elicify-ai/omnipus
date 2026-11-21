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

import { useId, useState } from 'react'
import { useBlocker, useRouter } from '@tanstack/react-router'
import { ArrowClockwise, Lock, Warning } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Label } from '@/components/ui/label'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import { useSettingsShell } from './SettingsShellContext'
import {
  fieldDomId,
  plural,
  useSaveBar,
  type FieldMessage,
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

interface SettingFieldControl {
  id: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean
}

/**
 * One labelled setting. Same layout and ids as the catalogued Field, but the
 * Needs restart and Password badges (FR-025, FR-037) sit next to the label,
 * not inside it, so the control's name is the setting's name alone.
 */
function SettingField({
  label,
  restart,
  password,
  description,
  error,
  children,
}: {
  label: string
  restart?: boolean
  password?: boolean
  description?: React.ReactNode
  error?: React.ReactNode
  children: (control: SettingFieldControl) => React.ReactNode
}) {
  const generatedId = useId()
  const controlId = `${generatedId}-control`
  const descriptionId = `${generatedId}-description`
  const errorId = `${generatedId}-error`
  const describedBy = [description ? descriptionId : null, error ? errorId : null].filter(Boolean).join(' ') || undefined
  return (
    <div className="space-y-[var(--space-2)]">
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <Label htmlFor={controlId}>{label}</Label>
        {restart && <RestartBadge />}
        {password && <PasswordBadge />}
      </div>
      {children({ id: controlId, 'aria-describedby': describedBy, 'aria-invalid': error ? true : undefined })}
      {description && <div id={descriptionId} className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{description}</div>}
      {error && <div id={errorId} role="alert" className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">{error}</div>}
    </div>
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
  // FR-012: while a result is unknown, Discard first says the server may
  // already hold the new value.
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  if (!visible) return null
  if (count === 0 && unknownCount === 0 && invalidCount === 0) {
    return (
      <div
        data-testid="settings-save-bar"
        className="sticky bottom-[var(--space-3)] z-10 mt-[var(--space-4)] flex items-center justify-end rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-3)] py-[var(--space-2)] shadow-[var(--elevation-floating)]"
      >
        <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Saved at {savedAt}</span>
      </div>
    )
  }
  let headline = count > 0 ? plural(count, 'unsaved change', 'unsaved changes') : 'A saved value is out of range'
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
        <Button variant="ghost" onClick={() => (unknownCount > 0 ? setConfirmDiscard(true) : onDiscard())} disabled={saving}>
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
      <ConfirmDialog
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        title="Discard changes?"
        description="The gateway did not confirm the save, so the server may already hold the new value. Discard only resets this page; it does not undo a stored value."
        cancelLabel="Keep changes"
        confirmLabel="Discard"
        destructive
        onConfirm={() => {
          setConfirmDiscard(false)
          onDiscard()
        }}
      />
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
  const setValue = (v: SaveBarField['value']) => state.setDraft(field.key, v)
  const disabled = state.isDisabled(field.key)
  return (
    <div className="py-[var(--space-2)]" data-setting-key={field.key}>
      <SettingField
        label={field.label}
        restart={field.restart}
        password={field.password}
        description={field.description}
        error={msg?.tone === 'error' ? msg.text : undefined}
      >
        {(cp) => {
          const controlId = cp.id
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
                  disabled={disabled}
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
                  disabled={disabled}
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
                  disabled={disabled}
                  aria-describedby={cp['aria-describedby']}
                  aria-invalid={cp['aria-invalid']}
                  className="max-w-xs"
                />
              )
          }
        }}
      </SettingField>
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

/**
 * FR-014 for a Save bar outside the Settings shell (the shell asks for the
 * whole page): leaving through the app with unsaved changes asks first.
 */
function StandaloneLeaveGuard({ count }: { count: number }) {
  const blocker = useBlocker({
    shouldBlockFn: () => count > 0,
    withResolver: true,
    // useSaveBar registers the browser's own prompt while drafts exist.
    enableBeforeUnload: false,
  })
  return (
    <ConfirmDialog
      open={blocker.status === 'blocked'}
      onOpenChange={(open) => {
        if (!open && blocker.status === 'blocked') blocker.reset()
      }}
      title={`Leave without saving ${plural(count, 'change', 'changes')}?`}
      description={`${plural(count, 'unsaved change', 'unsaved changes')} on this page will be lost.`}
      cancelLabel="Stay"
      confirmLabel="Leave"
      destructive
      onConfirm={() => {
        if (blocker.status === 'blocked') blocker.proceed()
      }}
    />
  )
}

/** A simple section: its setting rows plus the Save bar. */
export function SaveBar(options: SaveBarProps) {
  const state = useSaveBar(options)
  const shell = useSettingsShell()
  const router = useRouter({ warn: false })
  return (
    <div>
      {options.fields.map((f) => (
        <SettingRow key={f.key} state={state} field={f} />
      ))}
      <SaveBarPanel {...state.bar} />
      {shell.standalone && router && <StandaloneLeaveGuard count={state.bar.count} />}
    </div>
  )
}

/** Variant for a primary section action while the Save bar shows (FR-017). */
export function usePrimaryVariant(): 'default' | 'outline' {
  return useSettingsShell().saveBarVisible ? 'outline' : 'default'
}

export default SaveBar

export interface FormRowProps { // not-wire-format: React props
  label: string
  description?: React.ReactNode
  error?: string | null
  restart?: boolean
  password?: boolean
  /** data-settings-field id, used to move focus to the first invalid value. */
  fieldId: string
  children: (control: { id: string; describedBy?: string; invalid: boolean; fieldId: string }) => React.ReactNode
}

/** One labelled setting of a form section (Limits, Third-party agents), with its badges and error. */
export function FormRow({ label, description, error, restart, password, fieldId, children }: FormRowProps) {
  return (
    <div className="py-[var(--space-2)]">
      <SettingField label={label} restart={restart} password={password} description={description} error={error || undefined}>
        {(cp) => children({ id: cp.id, describedBy: cp['aria-describedby'], invalid: !!cp['aria-invalid'], fieldId })}
      </SettingField>
    </div>
  )
}

/** The latest outcome line of one part of a form section. */
export function SaveOutcomeLine({ message }: { message: FieldMessage | null }) {
  if (!message) return null
  if (message.tone === 'error') {
    return (
      <p role="alert" className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
        {message.text}
      </p>
    )
  }
  return (
    <div className="flex items-start gap-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]">
      <Warning size={14} aria-hidden="true" className="mt-[var(--space-0-5)] shrink-0" />
      <div>
        <div>{message.text}</div>
        {message.keys && message.keys.length > 0 && <div className="text-[var(--color-muted)]">Not applied: {message.keys.join(', ')}</div>}
      </div>
    </div>
  )
}
