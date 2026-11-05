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
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'
import { catalogEntryById, catalogLabel } from '@/lib/catalogDisplay'
import { buildProviderModelGroups, providerDisplayName } from '@/lib/providerModelGroups'
import { isEligibleNewDefault, providerStatusLabel } from '@/lib/providerStatus'
import type {
  DefaultModel,
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

/**
 * Role → the sentence that heads that group of dependents (FR-012).
 * Issue #1620: a known default on another provider is a fallback, not an
 * agent left without a model. No such default keeps the original sentence.
 */
export function dependentGroupHeading(
  role: ProviderDependent['role'],
  providerName: string,
  primaryFallback?: { providerLabel: string; model: string },
): string {
  switch (role) {
    case 'primary':
      return primaryFallback
        ? `These agents will fall back to the default provider ${primaryFallback.providerLabel}, using ${primaryFallback.model}`
        : 'These agents will be left without a model'
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

/**
 * The installation default, when it is a complete pair on a provider other
 * than the one being removed. The model is the selected pair, never the
 * first model on that provider's list. The name is the configured row's
 * display name, then its name, then the catalog label.
 */
function knownDefaultOnAnotherProvider(
  removedId: string,
  defaultModel: DefaultModel | null | undefined,
  otherProviders: readonly Provider[],
  catalog: ProvidersCatalog | null | undefined,
): { providerLabel: string; model: string } | null {
  const providerId = defaultModel?.provider.trim() ?? ''
  const model = defaultModel?.model.trim() ?? ''
  if (!providerId || !model || providerId === removedId) return null

  const row = otherProviders.find((candidate) => candidate.id === providerId)
  const entry = catalogEntryById(catalog?.providers ?? [], providerId)
  const fromRow = row?.display_name?.trim() || row?.name?.trim()
  const providerLabel = fromRow
    ? fromRow
    : row
      ? providerDisplayName(row, entry)
      : entry
        ? catalogLabel(entry)
        : providerId
  return { providerLabel, model }
}

/** Stronger warning for a custom or local endpoint (issue #1620). */
function conversationDestinationCopy(providerLabel: string): string {
  return `Their conversations will go to the default provider ${providerLabel} instead.`
}

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
   * The installation's current default pair, from the parent's
   * `['default-model']` query. Absent or incomplete means there is no
   * default to name.
   */
  defaultModel?: DefaultModel | null
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
  defaultModel,
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
  // Issue #1620: name the default these agents inherit, when it lives on
  // another provider. The pair being removed is not a destination.
  const inheritedDefault = knownDefaultOnAnotherProvider(
    provider.id,
    defaultModel,
    otherProviders,
    catalog,
  )
  const movesConversations =
    inheritedDefault !== null &&
    (provider.custom === true || provider.locality === 'local') &&
    (provider.dependents ?? []).some((dependent) => dependent.role === 'primary')
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
            className="text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]"
          >
            {ONLY_PROVIDER_COPY}
          </p>
        )}

        {grouped.length > 0 && (
          <div className="space-y-[var(--space-2-5)]" data-testid="remove-provider-dependents">
            {grouped.map(({ role, dependents }) => (
              <div key={role} data-testid={`dependent-group-${role}`} className="space-y-[var(--space-1)]">
                <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">
                  {dependentGroupHeading(
                    role,
                    displayName,
                    role === 'primary' ? inheritedDefault ?? undefined : undefined,
                  )}
                </p>
                <ul className="max-h-40 overflow-y-auto space-y-[var(--space-0-5)]">
                  {dependents.map((dependent) => (
                    <li
                      key={`${role}:${dependent.id}`}
                      data-testid={`dependent-${role}-${dependent.id}`}
                      className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
                    >
                      {dependent.name}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}

        {movesConversations && inheritedDefault && (
          <p
            data-testid="remove-provider-conversation-warning"
            className="text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]"
          >
            {conversationDestinationCopy(inheritedDefault.providerLabel)}
          </p>
        )}

        {needsNewDefault && !onlyProvider && (
          <div className="space-y-[var(--space-2)]" data-testid="new-default-section">
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">New default model</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              {displayName} backs the default model. Choose the model that takes over before
              removing it.
            </p>
            <SegmentedControl
              aria-label="New default model provider"
              value={activeCandidateId}
              onValueChange={(candidateIdValue) => {
                setCandidateId(candidateIdValue)
                // A model belongs to the provider it was picked under —
                // carrying it across would submit a pair that does not exist.
                setModel('')
              }}
              className="h-auto flex-wrap gap-[var(--space-1)] border-0 bg-transparent p-0"
            >
              {candidates.map((candidate) => {
                const pressed = candidate.id === activeCandidateId
                return (
                  <SegmentedControlItem
                    key={candidate.id}
                    value={candidate.id}
                    data-testid={`new-default-provider-${candidate.id}`}
                    className={[
                      'h-auto gap-[var(--space-1)] rounded border px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)] shadow-none text-[var(--color-secondary)]',
                      pressed
                        ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]'
                        : 'border-[var(--color-border)] bg-transparent hover:bg-transparent hover:text-[var(--color-secondary)]',
                    ].join(' ')}
                  >
                    <span>{candidate.display_name ?? candidate.name ?? candidate.id}</span>
                    <span className="text-[var(--color-muted)]">
                      {providerStatusLabel(candidate.status)}
                    </span>
                  </SegmentedControlItem>
                )
              })}
            </SegmentedControl>
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
