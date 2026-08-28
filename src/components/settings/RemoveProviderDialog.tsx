// RemoveProviderDialog.tsx — ADR-068 US-3 (FR-012, FR-016, FR-017).
//
// The ONE gate between "Remove provider" and a DELETE that destroys the stored
// key. It confirms once, says what will break, and — when this provider backs
// the global default model — makes the operator name the replacement inline.
//
// Three rules this file exists to enforce, all of them deliberate:
//
//  1. There is NO Undo (FR-017, SC-009). An Undo would have to retain the key
//     the operator just asked us to destroy, so the word does not appear in
//     this file, in a toast, or anywhere downstream. The confirmation IS the
//     safety net.
//  2. The DELETE response, not this dialog, is authoritative (FR-012). The
//     dependents rendered here come from the advisory `GET /providers` row and
//     may be stale by the time Remove is pressed; the server recomputes them
//     under the config lock. This is a preview, not a promise.
//  3. A provider that backs the default is never removed while it backs it, so
//     the LAST connected provider can never be removed at all (resolution #4).
//     That case gets its own copy and a permanently disabled Remove — not a
//     hopeful button that 409s.

import * as React from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { ModelSelector } from '@/components/ui/model-selector'
import { buildProviderModelGroups } from '@/lib/providerModelGroups'
import { isEligibleNewDefault, providerStatusLabel } from '@/lib/providerStatus'
import type {
  DefaultModelUpdateRequest,
  Provider,
  ProviderDependent,
  ProvidersCatalog,
} from '@/lib/api/generated/openapi-types'

/**
 * The copy shown when this provider backs the default model and there is no
 * other configured provider to hand the default to (resolution #4). Verbatim
 * from the spec — exported so the e2e row and T068-31 assert the same string.
 */
export const ONLY_PROVIDER_COPY =
  'This is your only provider and backs the default model; connect another provider and make it the default before removing this one.'

/** Role → the sentence that heads that group of dependents (FR-012). */
export function dependentGroupHeading(
  role: ProviderDependent['role'],
  providerName: string,
): string {
  switch (role) {
    case 'primary':
      return 'These agents will be left without a model'
    case 'fallback':
      return 'uses as fallback'
    case 'passthrough':
      return `resolved through ${providerName}`
    case 'recap':
      return 'used as the recap model'
    case 'image':
      return 'used as the image model'
    case 'voice':
      return 'used as the voice model'
  }
}

/** Render order of the dependent groups — most consequential first. */
const DEPENDENT_ROLE_ORDER: readonly ProviderDependent['role'][] = [
  'primary',
  'fallback',
  'passthrough',
  'recap',
  'image',
  'voice',
]

export interface RemoveProviderDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The configured row about to be removed. */
  provider: Provider
  /** What the title calls it — the catalog label where one resolved. */
  displayName: string
  /** Every OTHER configured row; the caller filters this provider out. */
  otherProviders: readonly Provider[]
  /** Registry-fed catalog, for the new-default selector's model lists. */
  catalog?: ProvidersCatalog | null
  /**
   * Fired on *Remove*. Carries the inline new default when this provider backs
   * the current one, and `undefined` when it does not.
   */
  onConfirm: (newDefault?: DefaultModelUpdateRequest) => void
  /** True while the DELETE is in flight — Remove is disabled, not hidden. */
  isRemoving?: boolean
}

export function RemoveProviderDialog({
  open,
  onOpenChange,
  provider,
  displayName,
  otherProviders,
  catalog,
  onConfirm,
  isRemoving = false,
}: RemoveProviderDialogProps) {
  const cancelRef = React.useRef<HTMLButtonElement | null>(null)

  // Candidates for the inline new default: every other CONFIGURED row,
  // `error`/`expired` included (the operator's risk to take, MAJ-011), only
  // `unknown-provider` excluded — its model list is empty by construction.
  const candidates = React.useMemo(
    () => otherProviders.filter((p) => p.id !== provider.id && isEligibleNewDefault(p.status)),
    [otherProviders, provider.id],
  )

  // '' means "the operator has not chosen one" — the first candidate stands in
  // until they do. Deliberately NOT seeded from an effect keyed on
  // `candidates`: callers pass that array as a fresh literal on every render,
  // so such an effect would re-run constantly and wipe a pending model pick.
  const [candidateId, setCandidateId] = React.useState<string>('')
  const [model, setModel] = React.useState('')
  const activeCandidateId = candidateId || candidates[0]?.id || ''

  // A freshly opened dialog never inherits the previous provider's answers.
  React.useEffect(() => {
    if (!open) return
    setCandidateId('')
    setModel('')
  }, [open, provider.id])

  const groups = React.useMemo(
    () => buildProviderModelGroups({ providers: candidates, catalog }),
    [candidates, catalog],
  )

  const needsNewDefault = provider.backs_default
  const onlyProvider = needsNewDefault && candidates.length === 0
  const chosenPair: DefaultModelUpdateRequest | undefined =
    activeCandidateId && model ? { provider: activeCandidateId, model } : undefined
  const canRemove = !isRemoving && !onlyProvider && (!needsNewDefault || chosenPair !== undefined)

  const grouped = React.useMemo(() => {
    const byRole = new Map<ProviderDependent['role'], ProviderDependent[]>()
    for (const dependent of provider.dependents ?? []) {
      const list = byRole.get(dependent.role)
      if (list) list.push(dependent)
      else byRole.set(dependent.role, [dependent])
    }
    return DEPENDENT_ROLE_ORDER.filter((role) => byRole.has(role)).map((role) => ({
      role,
      dependents: byRole.get(role) as ProviderDependent[],
    }))
  }, [provider.dependents])

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent
        data-testid="remove-provider-dialog"
        // Focus the non-destructive choice, not the one that deletes a key.
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          cancelRef.current?.focus()
        }}
      >
        <AlertDialogHeader>
          <AlertDialogTitle data-testid="remove-provider-title">
            Remove {displayName}? Its key will be deleted.
          </AlertDialogTitle>
          <AlertDialogDescription data-testid="remove-provider-description">
            {onlyProvider
              ? ONLY_PROVIDER_COPY
              : 'The stored API key is deleted with the provider. This cannot be reversed — you would have to add the key again.'}
          </AlertDialogDescription>
        </AlertDialogHeader>

        {onlyProvider && (
          <p
            data-testid="remove-provider-only-copy"
            className="text-sm text-[var(--color-warning)]"
          >
            {ONLY_PROVIDER_COPY}
          </p>
        )}

        {grouped.length > 0 && (
          <div className="space-y-3" data-testid="remove-provider-dependents">
            {grouped.map(({ role, dependents }) => (
              <div key={role} data-testid={`dependent-group-${role}`} className="space-y-1">
                <p className="text-xs font-semibold text-[var(--color-secondary)]">
                  {dependentGroupHeading(role, displayName)}
                </p>
                <ul className="max-h-40 overflow-y-auto space-y-0.5">
                  {dependents.map((dependent) => (
                    <li
                      key={`${role}:${dependent.id}`}
                      data-testid={`dependent-${role}-${dependent.id}`}
                      className="text-xs text-[var(--color-muted)]"
                    >
                      {dependent.name}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}

        {needsNewDefault && !onlyProvider && (
          <div className="space-y-2" data-testid="new-default-section">
            <p className="text-xs font-semibold text-[var(--color-secondary)]">New default model</p>
            <p className="text-xs text-[var(--color-muted)]">
              {displayName} backs the default model. Choose the model that takes over before
              removing it.
            </p>
            <div className="flex flex-wrap gap-1.5">
              {candidates.map((candidate) => {
                const pressed = candidate.id === activeCandidateId
                return (
                  <button
                    key={candidate.id}
                    type="button"
                    tabIndex={0}
                    aria-pressed={pressed}
                    data-testid={`new-default-provider-${candidate.id}`}
                    onClick={() => {
                      setCandidateId(candidate.id)
                      // A model belongs to the provider it was picked under —
                      // carrying it across would submit a pair that does not exist.
                      setModel('')
                    }}
                    className="flex items-center gap-1.5 rounded border px-2 py-1 text-xs"
                    style={{
                      borderColor: pressed ? 'var(--color-accent)' : 'var(--color-border)',
                      color: 'var(--color-secondary)',
                      background: pressed ? 'var(--color-surface-2)' : 'transparent',
                    }}
                  >
                    <span>{candidate.display_name ?? candidate.name ?? candidate.id}</span>
                    <span className="text-[var(--color-muted)]">
                      {providerStatusLabel(candidate.status)}
                    </span>
                  </button>
                )
              })}
            </div>
            <ModelSelector
              models={[]}
              value={model}
              onChange={setModel}
              constrainToCatalog
              catalogGroups={groups}
              filterProviders={{ providerIds: activeCandidateId ? [activeCandidateId] : [] }}
              label="New default model"
              triggerTestId="new-default-model-select"
              itemTestIdPrefix="new-default-model-"
              emptyCatalogHint="This provider has no models to offer"
            />
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel ref={cancelRef} data-testid="remove-provider-cancel">
            Cancel
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={!canRemove}
            data-testid="remove-provider-confirm"
            onClick={() => {
              if (!canRemove) return
              onConfirm(chosenPair)
            }}
          >
            Remove
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
