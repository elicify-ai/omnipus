import { useState, useEffect, useMemo } from 'react'
import { ArrowsClockwise, LockKey } from '@phosphor-icons/react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Slider } from '@/components/ui/slider'
import { Textarea } from '@/components/ui/textarea'
import { SmartSelect } from '@/components/ui/smart-select'
import { Separator } from '@/components/ui/separator'
import { useUiStore } from '@/store/ui'
import { fetchUserContext, updateUserContext, changePassword, getErrorMessage } from '@/lib/api'

const TIMEZONES = [
  'UTC',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Anchorage',
  'Pacific/Honolulu',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Moscow',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Bangkok',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Australia/Sydney',
  'Pacific/Auckland',
]

const LS_PREFIX = 'omnipus_pref_'

function loadPref<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(`${LS_PREFIX}${key}`)
    return raw !== null ? (JSON.parse(raw) as T) : fallback
  } catch (err) {
    console.warn('[profile] localStorage read failed:', key, err)
    return fallback
  }
}

function savePref(key: string, value: unknown): boolean {
  try {
    localStorage.setItem(`${LS_PREFIX}${key}`, JSON.stringify(value))
    return true
  } catch (err) {
    console.warn('[profile] localStorage write failed:', key, err)
    return false
  }
}

export function ProfileSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [name, setName] = useState(() => loadPref<string>('name', ''))
  const [timezone, setTimezone] = useState(() => loadPref<string>('timezone', 'UTC'))
  const [fontSize, setFontSize] = useState(() => {
    const v = loadPref<number>('font_size', 14)
    // Persist the loaded (or default) value immediately so the preference is
    // always present in localStorage — avoids a gap where the auto-save hasn't
    // fired yet (it skips the initial render by design).
    savePref('font_size', v)
    return v
  })
  const [userContent, setUserContent] = useState('')

  // Change password form state
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [passwordError, setPasswordError] = useState<string | null>(null)

  const { data: userContextData, isError: userContextError, refetch: refetchUserContext } = useQuery({
    queryKey: ['user-context'],
    queryFn: fetchUserContext,
  })

  useEffect(() => {
    if (userContextData) {
      setUserContent(userContextData.content)
    }
  }, [userContextData])

  const { status: contextSaveStatus, error: contextSaveError } = useAutoSave(
    userContent,
    async (content) => {
      await updateUserContext(content)
      queryClient.invalidateQueries({ queryKey: ['user-context'] })
    },
    { disabled: userContextError },
  )

  const { mutate: submitPasswordChange, isPending: isChangingPassword } = useMutation({
    mutationFn: () => changePassword(currentPassword, newPassword),
    onSuccess: () => {
      addToast({ message: 'Password changed successfully', variant: 'success' })
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setPasswordError(null)
    },
    onError: (err: Error) => {
      setPasswordError(getErrorMessage(err, 'Password change failed'))
    },
  })

  function handlePasswordChange() {
    setPasswordError(null)
    if (!currentPassword) {
      setPasswordError('Current password is required.')
      return
    }
    if (newPassword.length < 8) {
      setPasswordError('New password must be at least 8 characters.')
      return
    }
    if (newPassword !== confirmPassword) {
      setPasswordError('New passwords do not match.')
      return
    }
    submitPasswordChange()
  }

  // Keep document font-size in sync for preview
  useEffect(() => {
    document.documentElement.style.setProperty('--user-font-size', `${fontSize}px`)
  }, [fontSize])

  const prefsData = useMemo(() => ({ name, timezone, fontSize }), [name, timezone, fontSize])

  const { status: prefSaveStatus, error: prefSaveError } = useAutoSave(
    prefsData,
    async (prefs) => {
      const ok =
        savePref('name', prefs.name) &&
        savePref('timezone', prefs.timezone) &&
        savePref('font_size', prefs.fontSize)
      if (!ok) {
        throw new Error('Some preferences could not be saved — storage may be full or restricted.')
      }
    },
  )

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Profile & Preferences</h2>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            Personal preferences stored in this browser.
          </p>
        </div>
        <AutoSaveIndicator status={prefSaveStatus} error={prefSaveError} />
      </div>

      {/* Identity */}
      <section className="space-y-3">
        <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Identity</h3>
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
          <div className="flex items-center justify-between gap-4">
            <Label htmlFor="pref-name" className="text-sm text-[var(--color-secondary)] shrink-0">
              Display name
            </Label>
            <Input
              id="pref-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Your name"
              className="h-8 text-xs max-w-xs"
            />
          </div>
        </div>
      </section>

      {/* Locale */}
      <section className="space-y-3">
        <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Locale</h3>
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-[var(--color-secondary)]">Timezone</p>
            </div>
            <SmartSelect
              value={timezone}
              onValueChange={setTimezone}
              triggerClassName="w-[200px] h-8 text-xs font-mono"
              items={TIMEZONES.map((tz) => ({ value: tz, label: tz }))}
            />
          </div>

        </div>
      </section>

      {/* Appearance */}
      <section className="space-y-3">
        <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Appearance</h3>
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm text-[var(--color-secondary)]">Theme</p>
              <p className="text-xs text-[var(--color-muted)]">Omnipus uses the Sovereign Deep dark theme.</p>
            </div>
            <span className="text-sm text-[var(--color-secondary)]">
              Dark <span className="text-xs text-[var(--color-muted)]">(only dark theme is supported)</span>
            </span>
          </div>

          <Separator />

          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <p className="text-sm text-[var(--color-secondary)]">Font size</p>
              <span className="text-xs font-mono text-[var(--color-muted)]">{fontSize}px</span>
            </div>
            <Slider
              min={12}
              max={20}
              step={1}
              value={[fontSize]}
              onValueChange={([v]) => setFontSize(v)}
              className="w-full"
            />
            <div className="flex justify-between text-[10px] text-[var(--color-muted)]">
              <span>12px</span>
              <span>20px</span>
            </div>
          </div>
        </div>
      </section>

      {/* Change Password */}
      <section className="space-y-3">
        <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Security</h3>
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
          <div className="space-y-1">
            <h4 className="text-sm font-medium text-[var(--color-secondary)]">Change Password</h4>
            <p className="text-xs text-[var(--color-muted)]">Update your login password. Must be at least 8 characters.</p>
          </div>

          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="current-password" className="text-xs text-[var(--color-secondary)]">
                Current password
              </Label>
              <Input
                id="current-password"
                type="password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                placeholder="Current password"
                className="h-8 text-xs"
                autoComplete="current-password"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="new-password" className="text-xs text-[var(--color-secondary)]">
                New password
              </Label>
              <Input
                id="new-password"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                placeholder="New password (min 8 chars)"
                className="h-8 text-xs"
                autoComplete="new-password"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="confirm-password" className="text-xs text-[var(--color-secondary)]">
                Confirm new password
              </Label>
              <Input
                id="confirm-password"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                placeholder="Confirm new password"
                className="h-8 text-xs"
                autoComplete="new-password"
              />
            </div>

            {passwordError && (
              <p className="text-xs text-[var(--color-error)]">{passwordError}</p>
            )}

            <Button
              size="sm"
              onClick={handlePasswordChange}
              disabled={isChangingPassword}
              className="gap-1.5"
            >
              <LockKey size={13} weight="bold" />
              {isChangingPassword ? 'Changing...' : 'Change Password'}
            </Button>
          </div>
        </div>
      </section>

      {/* Workspace Context (USER.md) */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">
            Workspace Context
          </h3>
          {!userContextError && (
            <AutoSaveIndicator status={contextSaveStatus} error={contextSaveError} />
          )}
        </div>
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3">
          <p className="text-xs text-[var(--color-muted)]">
            Shared context available to all agents — your role, preferences, and workspace information.
          </p>
          {userContextError ? (
            <div className="flex flex-col items-center gap-3 py-4 text-center">
              <p className="text-sm text-[var(--color-error)]">Could not load workspace context.</p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => refetchUserContext()}
                className="gap-1.5"
              >
                <ArrowsClockwise size={13} />
                Retry
              </Button>
            </div>
          ) : (
            <Textarea
              value={userContent}
              onChange={(e) => setUserContent(e.target.value)}
              placeholder={"# About Me\n\nDescribe your role, expertise, and preferences..."}
              rows={8}
              className="text-xs font-mono resize-none"
            />
          )}
        </div>
      </section>
    </div>
  )
}
