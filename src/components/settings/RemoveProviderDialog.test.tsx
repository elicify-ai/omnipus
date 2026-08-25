/**
 * RemoveProviderDialog.test.tsx — ADR-068 TDD row 28 (US-3).
 *
 * Scenarios covered (docs/internal/specs/adr-068-providers-ux-spec.md):
 *   - Remove an unused provider after one confirmation (dialog half)
 *   - Dependents are listed and left without a model (dialog half)
 *   - Fallback references are removed and listed (dialog half)
 *   - Passthrough-resolved agents are dependents (dialog half)
 *   - Default-backing provider requires an inline new default (dialog half)
 *   - Other provider in error state still offered as new default (dialog half)
 *   - Removing the only provider is refused (dialog half of
 *     TestDeleteProvider_OnlyProviderRefused409, TDD row 15)
 *   - No Undo exists after removal (SC-009: the word never renders here)
 *   - Accessibility: role="alertdialog", aria-labelledby / aria-describedby,
 *     focus on Cancel, Esc = Cancel
 *
 * The Radix popover is stubbed so the ModelSelector's list is queryable
 * without driving a portal — the selector itself is the real component.
 */

import * as React from 'react'
import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'

vi.mock('@/components/ui/popover', () => ({
  Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
  PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild && React.isValidElement(children) ? children : React.createElement('div', null, children),
  PopoverContent: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
  PopoverAnchor: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
}))

import { RemoveProviderDialog, ONLY_PROVIDER_COPY } from './RemoveProviderDialog'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import type { Provider, ProviderDependent } from '@/lib/api/generated/openapi-types'

// cmdk (the ModelSelector's list) needs ResizeObserver and scrollIntoView,
// neither of which jsdom provides.
beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

const BASE = {
  auth_method: 'api_key' as const,
  dependents: [] as ProviderDependent[],
  backs_default: false,
  models: [] as string[],
}

function provider(over: Partial<Provider> & { id: string }): Provider {
  return {
    ...BASE,
    name: over.id,
    display_name: over.id,
    status: 'connected',
    ...over,
  } as Provider
}

const OPENROUTER = provider({ id: 'openrouter', display_name: 'OpenRouter' })
const ANTHROPIC = provider({ id: 'anthropic', display_name: 'Anthropic' })

const onConfirm = vi.fn()
const onOpenChange = vi.fn()

function renderDialog(over: Partial<React.ComponentProps<typeof RemoveProviderDialog>> = {}) {
  return render(
    <RemoveProviderDialog
      open
      onOpenChange={onOpenChange}
      provider={OPENROUTER}
      displayName="OpenRouter"
      otherProviders={[]}
      catalog={PROVIDERS_CATALOG}
      onConfirm={onConfirm}
      {...over}
    />,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ---------------------------------------------------------------------------
// Happy path — one confirmation, nothing else
// ---------------------------------------------------------------------------

describe('RemoveProviderDialog — one confirmation', () => {
  it('titles the dialog with the exact sentence and confirms with no new default', () => {
    renderDialog()

    expect(screen.getByText('Remove OpenRouter? Its key will be deleted.')).toBeInTheDocument()

    const remove = screen.getByTestId('remove-provider-confirm')
    expect(remove).not.toBeDisabled()
    fireEvent.click(remove)

    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onConfirm).toHaveBeenCalledWith(undefined)
  })

  it('offers exactly Cancel and Remove for an unused provider — no new-default selector', () => {
    renderDialog()

    expect(screen.getByTestId('remove-provider-cancel')).toBeInTheDocument()
    expect(screen.getByTestId('remove-provider-confirm')).toBeInTheDocument()
    expect(screen.queryByTestId('new-default-section')).not.toBeInTheDocument()
    expect(screen.queryByTestId('remove-provider-dependents')).not.toBeInTheDocument()
  })

  it('Cancel closes without confirming', () => {
    renderDialog()
    fireEvent.click(screen.getByTestId('remove-provider-cancel'))

    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('disables Remove while the DELETE is in flight', () => {
    renderDialog({ isRemoving: true })
    expect(screen.getByTestId('remove-provider-confirm')).toBeDisabled()
  })
})

// ---------------------------------------------------------------------------
// FR-017 / SC-009 — there is no Undo, ever
// ---------------------------------------------------------------------------

describe('RemoveProviderDialog — no Undo (FR-017, SC-009)', () => {
  it('renders no element whose text is Undo, in any state', () => {
    const { container, unmount } = renderDialog({
      provider: provider({
        id: 'openrouter',
        display_name: 'OpenRouter',
        backs_default: true,
        dependents: [{ id: 'ava', name: 'Ava', role: 'primary' }],
      }),
      otherProviders: [ANTHROPIC],
    })
    expect(screen.queryByText(/undo/i)).not.toBeInTheDocument()
    expect(container.ownerDocument.body.textContent ?? '').not.toMatch(/undo/i)
    unmount()

    renderDialog()
    expect(screen.queryByText(/undo/i)).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// FR-012 — dependents, grouped by role
// ---------------------------------------------------------------------------

describe('RemoveProviderDialog — dependents grouped by role (FR-012)', () => {
  const withDependents = (dependents: ProviderDependent[]) =>
    provider({ id: 'openrouter', display_name: 'OpenRouter', dependents })

  it('lists primaries under "These agents will be left without a model"', () => {
    renderDialog({
      provider: withDependents([
        { id: 'ava', name: 'Ava', role: 'primary' },
        { id: 'scout', name: 'Scout', role: 'primary' },
      ]),
    })

    const group = screen.getByTestId('dependent-group-primary')
    expect(within(group).getByText('These agents will be left without a model')).toBeInTheDocument()
    expect(within(group).getByText('Ava')).toBeInTheDocument()
    expect(within(group).getByText('Scout')).toBeInTheDocument()
  })

  it('lists a fallback-only reference under "uses as fallback"', () => {
    renderDialog({ provider: withDependents([{ id: 'jim', name: 'Jim', role: 'fallback' }]) })

    const group = screen.getByTestId('dependent-group-fallback')
    expect(within(group).getByText('uses as fallback')).toBeInTheDocument()
    expect(within(group).getByText('Jim')).toBeInTheDocument()
    // Jim is not in the "left without a model" group — he keeps his primary.
    expect(screen.queryByTestId('dependent-group-primary')).not.toBeInTheDocument()
  })

  it('lists a passthrough-resolved agent under "resolved through <Name>"', () => {
    renderDialog({ provider: withDependents([{ id: 'ray', name: 'Ray', role: 'passthrough' }]) })

    const group = screen.getByTestId('dependent-group-passthrough')
    expect(within(group).getByText('resolved through OpenRouter')).toBeInTheDocument()
    expect(within(group).getByText('Ray')).toBeInTheDocument()
  })

  it('lists a recap-fallback setting under its own heading', () => {
    renderDialog({
      provider: withDependents([
        { id: 'agents.defaults.recap_fallback_models', name: 'Recap fallback', role: 'recap' },
      ]),
    })

    const group = screen.getByTestId('dependent-group-recap')
    expect(within(group).getByText('used as the recap model')).toBeInTheDocument()
    expect(within(group).getByText('Recap fallback')).toBeInTheDocument()
  })

  it('renders every dependent of a 50-agent list (dataset row 6)', () => {
    const many: ProviderDependent[] = Array.from({ length: 50 }, (_, i) => ({
      id: `agent-${i}`,
      name: `Agent ${String(i).padStart(2, '0')}`,
      role: 'primary' as const,
    }))
    renderDialog({ provider: withDependents(many) })

    const group = screen.getByTestId('dependent-group-primary')
    expect(within(group).getAllByTestId(/^dependent-/)).toHaveLength(50)
    expect(within(group).getByText('Agent 49')).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// FR-016 — the default-backing provider needs an inline new default
// ---------------------------------------------------------------------------

describe('RemoveProviderDialog — inline new default (FR-016)', () => {
  const BACKS_DEFAULT = provider({
    id: 'openrouter',
    display_name: 'OpenRouter',
    backs_default: true,
  })

  it('keeps Remove disabled until a new default model is chosen, then confirms with the pair', async () => {
    renderDialog({ provider: BACKS_DEFAULT, otherProviders: [ANTHROPIC] })

    expect(screen.getByTestId('new-default-section')).toBeInTheDocument()
    const remove = screen.getByTestId('remove-provider-confirm')
    expect(remove).toBeDisabled()

    // Only Anthropic's models are offered — OpenRouter is the row being removed.
    const options = screen.getAllByRole('option').map((el) => el.getAttribute('data-model'))
    expect(options).toContain('claude-sonnet-4-5')
    expect(options).not.toContain('anthropic/claude-3.5-haiku')

    fireEvent.click(screen.getByTestId('new-default-model-claude-sonnet-4-5'))

    await waitFor(() => expect(screen.getByTestId('remove-provider-confirm')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('remove-provider-confirm'))

    expect(onConfirm).toHaveBeenCalledWith({ provider: 'anthropic', model: 'claude-sonnet-4-5' })
  })

  it('offers an error-state provider as the new default, with its state shown (MAJ-011)', async () => {
    const broken = provider({ id: 'anthropic', display_name: 'Anthropic', status: 'error' })
    renderDialog({ provider: BACKS_DEFAULT, otherProviders: [broken] })

    const chip = screen.getByTestId('new-default-provider-anthropic')
    expect(within(chip).getByText('Anthropic')).toBeInTheDocument()
    expect(within(chip).getByText('Error')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('new-default-model-claude-sonnet-4-5'))
    await waitFor(() => expect(screen.getByTestId('remove-provider-confirm')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('remove-provider-confirm'))

    expect(onConfirm).toHaveBeenCalledWith({ provider: 'anthropic', model: 'claude-sonnet-4-5' })
  })

  it('excludes an unknown-provider row from the new-default candidates', () => {
    const unknown = provider({ id: 'mystery', display_name: 'mystery', status: 'unknown-provider' })
    renderDialog({ provider: BACKS_DEFAULT, otherProviders: [ANTHROPIC, unknown] })

    expect(screen.getByTestId('new-default-provider-anthropic')).toBeInTheDocument()
    expect(screen.queryByTestId('new-default-provider-mystery')).not.toBeInTheDocument()
  })

  it('switches the offered models when another candidate provider is chosen', async () => {
    const groq = provider({ id: 'groq', display_name: 'Groq' })
    renderDialog({ provider: BACKS_DEFAULT, otherProviders: [ANTHROPIC, groq] })

    // The first candidate is pre-selected, so its models are the ones offered.
    expect(screen.getByTestId('new-default-provider-anthropic')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByTestId('new-default-model-llama-4-maverick')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('new-default-provider-groq'))

    await waitFor(() => expect(screen.getByTestId('new-default-model-llama-4-maverick')).toBeInTheDocument())
    expect(screen.queryByTestId('new-default-model-claude-sonnet-4-5')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('new-default-model-llama-4-maverick'))
    await waitFor(() => expect(screen.getByTestId('remove-provider-confirm')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('remove-provider-confirm'))
    expect(onConfirm).toHaveBeenCalledWith({ provider: 'groq', model: 'llama-4-maverick' })
  })

  it('drops a pending model pick when the candidate provider changes', async () => {
    const groq = provider({ id: 'groq', display_name: 'Groq' })
    renderDialog({ provider: BACKS_DEFAULT, otherProviders: [ANTHROPIC, groq] })

    fireEvent.click(screen.getByTestId('new-default-model-claude-sonnet-4-5'))
    await waitFor(() => expect(screen.getByTestId('remove-provider-confirm')).not.toBeDisabled())

    fireEvent.click(screen.getByTestId('new-default-provider-groq'))
    await waitFor(() => expect(screen.getByTestId('remove-provider-confirm')).toBeDisabled())
  })
})

// ---------------------------------------------------------------------------
// FR-016 — the only provider can never be removed
// ---------------------------------------------------------------------------

describe('RemoveProviderDialog — the only provider is refused (TDD row 15, dialog half)', () => {
  const ONLY = provider({ id: 'openrouter', display_name: 'OpenRouter', backs_default: true })

  it('shows the only-provider copy, no selector, and a permanently disabled Remove', () => {
    renderDialog({ provider: ONLY, otherProviders: [] })

    expect(screen.getByTestId('remove-provider-only-copy')).toHaveTextContent(ONLY_PROVIDER_COPY)
    expect(screen.queryByTestId('new-default-section')).not.toBeInTheDocument()

    const remove = screen.getByTestId('remove-provider-confirm')
    expect(remove).toBeDisabled()
    fireEvent.click(remove)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('treats an unknown-provider-only roster as having no other provider', () => {
    const unknown = provider({ id: 'mystery', display_name: 'mystery', status: 'unknown-provider' })
    renderDialog({ provider: ONLY, otherProviders: [unknown] })

    expect(screen.getByTestId('remove-provider-only-copy')).toBeInTheDocument()
    expect(screen.getByTestId('remove-provider-confirm')).toBeDisabled()
  })
})

// ---------------------------------------------------------------------------
// Accessibility (FR-041)
// ---------------------------------------------------------------------------

describe('RemoveProviderDialog — accessibility', () => {
  it('is an alertdialog labelled by its title and described by its body', () => {
    renderDialog()

    const dialog = screen.getByRole('alertdialog')
    const labelledBy = dialog.getAttribute('aria-labelledby')
    const describedBy = dialog.getAttribute('aria-describedby')
    expect(labelledBy).toBeTruthy()
    expect(describedBy).toBeTruthy()

    const doc = dialog.ownerDocument
    expect(doc.getElementById(labelledBy as string)?.textContent).toBe(
      'Remove OpenRouter? Its key will be deleted.',
    )
    expect(doc.getElementById(describedBy as string)?.textContent).toBeTruthy()
  })

  it('puts initial focus on Cancel, not on the destructive action', async () => {
    renderDialog()
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByTestId('remove-provider-cancel')),
    )
  })

  it('Escape cancels — it closes and never confirms', () => {
    renderDialog()
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape', code: 'Escape' })

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(onConfirm).not.toHaveBeenCalled()
  })
})
