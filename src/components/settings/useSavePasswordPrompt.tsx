// useSavePasswordPrompt — the one password prompt per Save (Settings redesign
// #1519, B4 / FR-026 to FR-029, FR-032).
//
// Local mode: the existing ReAuthDialog opens once, listing every
// password-marked setting of the Save by its visible label; the typed
// password is used for exactly one POST /auth/reauth and cleared by the
// dialog itself (FR-028). The single-use consent token is handed back to the
// Save bar, which sends it with the section's one protected write.
// Platform mode (sign-in through omnipus.ai): a confirmation listing the same
// settings, no password field and no reauth request — the server-side gate is
// a no-op there, so the token is empty (FR-032). Cancel resolves null: the
// Save bar then stores nothing and keeps every draft.

import { useCallback, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { fetchAppState } from '@/lib/api'
import { ReAuthDialog } from './ReAuthDialog'

export function useSavePasswordPrompt() {
  const { data: appState } = useQuery({ queryKey: ['app-state'], queryFn: fetchAppState })
  // Fail toward the stronger control until the mode is known (useStepUp's rule).
  const platform = appState?.identity?.mode === 'platform'
  const [items, setItems] = useState<string[] | null>(null)
  const resolver = useRef<((token: string | null) => void) | null>(null)

  const settle = useCallback((token: string | null) => {
    const r = resolver.current
    resolver.current = null
    setItems(null)
    r?.(token)
  }, [])

  const requestPassword = useCallback((labels: string[]) => {
    return new Promise<string | null>((resolve) => {
      resolver.current?.(null)
      resolver.current = resolve
      setItems(labels)
    })
  }, [])

  const dialog = platform ? (
    <ConfirmDialog
      open={items !== null}
      onOpenChange={(open) => {
        if (!open) settle(null)
      }}
      title="Save these settings?"
      description={`This saves: ${(items ?? []).join(', ')}.`}
      confirmLabel="Save"
      onConfirm={() => settle('')}
    />
  ) : (
    <ReAuthDialog
      open={items !== null}
      items={items ?? undefined}
      onOpenChange={(open) => {
        if (!open && resolver.current) settle(null)
      }}
      onConfirmed={(token) => settle(token)}
    />
  )

  return { requestPassword, dialog }
}
