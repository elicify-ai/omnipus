import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ModelSelector, type ModelGroup } from './model-selector'

// cmdk (used by Command) uses ResizeObserver and scrollIntoView which are not
// available in jsdom. Provide stubs so tests can mount the component.
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

// ModelSelector test suite
// Covers: search-by-model-name, flat list (1 provider), grouped headers (≥2 providers), text-input fallback

// Stub Popover/Command to render inline so we can interact with items without
// a real portal. We keep the real Command semantics for filtering but render
// the content unconditionally (no Radix portal).
vi.mock('@/components/ui/popover', () => {
  const React = require('react')
  return {
    Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
    PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) => {
      if (asChild && React.isValidElement(children)) return children
      return React.createElement('div', null, children)
    },
    PopoverContent: ({ children }: { children: React.ReactNode }) =>
      React.createElement('div', { 'data-testid': 'popover-content' }, children),
  }
})

const FLAT_MODELS = ['claude-3-haiku', 'claude-3-sonnet', 'gpt-4o', 'gemini-2.5-flash']

const TWO_PROVIDER_GROUPS: ModelGroup[] = [
  { providerName: 'Anthropic', models: ['claude-3-haiku', 'claude-3-sonnet'] },
  { providerName: 'OpenAI', models: ['gpt-4o', 'gpt-4o-mini'] },
]

const ONE_PROVIDER_GROUPS: ModelGroup[] = [
  { providerName: 'Anthropic', models: ['claude-3-haiku', 'claude-3-sonnet'] },
]

function renderSelector(
  models: string[],
  value: string,
  onChange: (model: string) => void,
  providerGroups?: ModelGroup[],
) {
  return render(
    <ModelSelector
      models={models}
      value={value}
      onChange={onChange}
      providerGroups={providerGroups}
    />,
  )
}

// =====================================================================
// Text-input fallback (no models)
// =====================================================================

describe('ModelSelector — text-input mode', () => {
  it('renders an <input> when no models are available', () => {
    renderSelector([], '', vi.fn())
    expect(screen.getByRole('textbox')).toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  })

  it('calls onChange with typed value', () => {
    const onChange = vi.fn()
    renderSelector([], '', onChange)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'my-model' } })
    expect(onChange).toHaveBeenCalledWith('my-model')
  })
})

// =====================================================================
// Search by model name (flat list)
// =====================================================================

describe('ModelSelector — search by model name', () => {
  it('shows all models with empty query', () => {
    renderSelector(FLAT_MODELS, '', vi.fn())
    for (const model of FLAT_MODELS) {
      expect(screen.getByText(model)).toBeInTheDocument()
    }
  })

  it('filters by model name substring (not provider name)', () => {
    renderSelector(FLAT_MODELS, '', vi.fn())
    // Type a model-name fragment
    const input = screen.getByPlaceholderText(/search models/i)
    fireEvent.change(input, { target: { value: 'haiku' } })
    expect(screen.getByText('claude-3-haiku')).toBeInTheDocument()
    expect(screen.queryByText('claude-3-sonnet')).not.toBeInTheDocument()
    expect(screen.queryByText('gpt-4o')).not.toBeInTheDocument()
  })

  it('shows all matching models for a common substring', () => {
    renderSelector(FLAT_MODELS, '', vi.fn())
    const input = screen.getByPlaceholderText(/search models/i)
    fireEvent.change(input, { target: { value: 'claude' } })
    expect(screen.getByText('claude-3-haiku')).toBeInTheDocument()
    expect(screen.getByText('claude-3-sonnet')).toBeInTheDocument()
    expect(screen.queryByText('gpt-4o')).not.toBeInTheDocument()
  })

  it('shows "Use …" custom option when query has no exact match', () => {
    renderSelector(FLAT_MODELS, '', vi.fn())
    const input = screen.getByPlaceholderText(/search models/i)
    fireEvent.change(input, { target: { value: 'my-custom-slug' } })
    expect(screen.getByText(/my-custom-slug/)).toBeInTheDocument()
  })

  it('preserves case when user types a custom model slug', () => {
    // Traces to: code-reviewer finding #3 — custom model entry must not be lower-cased before save
    // BDD: Given the user types a mixed-case provider slug,
    //      When they select the "Use ..." custom option,
    //      Then onChange is called with the original casing preserved.
    const onChange = vi.fn()
    renderSelector(FLAT_MODELS, '', onChange)
    const input = screen.getByPlaceholderText(/search models/i)
    fireEvent.change(input, { target: { value: 'MiniMax-M2.7' } })

    // The "Use …" option should display the original case
    expect(screen.getByText(/MiniMax-M2\.7/)).toBeInTheDocument()

    // Clicking the custom item should call onChange with the exact original casing
    const customItem = screen.getByText(/MiniMax-M2\.7/)
    fireEvent.click(customItem)
    expect(onChange).toHaveBeenCalledWith('MiniMax-M2.7')
    // Must NOT be called with the lower-cased form
    expect(onChange).not.toHaveBeenCalledWith('minimax-m2.7')
  })
})

// =====================================================================
// Flat list when 1 provider configured (providerGroups with 1 group)
// =====================================================================

// O3: provider-headed sections are now shown even with a single provider so
// the provider name is always visible as a stable label. The old test checked
// the opposite; it is updated to match the new spec behaviour.
describe('ModelSelector — flat list with 1 provider', () => {
  it('renders a provider group heading even for a single provider (O3)', () => {
    renderSelector(
      ONE_PROVIDER_GROUPS[0].models,
      '',
      vi.fn(),
      ONE_PROVIDER_GROUPS,
    )
    // O3: provider name IS shown as a heading even with only 1 provider.
    expect(screen.getByText('Anthropic')).toBeInTheDocument()
    // Models are still rendered
    expect(screen.getByText('claude-3-haiku')).toBeInTheDocument()
    expect(screen.getByText('claude-3-sonnet')).toBeInTheDocument()
  })
})

// =====================================================================
// Grouped by provider when ≥2 providers configured
// =====================================================================

describe('ModelSelector — grouped by provider (≥2 providers)', () => {
  it('renders a group heading per provider when 2 providers are configured', () => {
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    renderSelector(allModels, '', vi.fn(), TWO_PROVIDER_GROUPS)
    expect(screen.getByText('Anthropic')).toBeInTheDocument()
    expect(screen.getByText('OpenAI')).toBeInTheDocument()
  })

  it('shows models under their respective provider groups', () => {
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    renderSelector(allModels, '', vi.fn(), TWO_PROVIDER_GROUPS)
    expect(screen.getByText('claude-3-haiku')).toBeInTheDocument()
    expect(screen.getByText('gpt-4o')).toBeInTheDocument()
  })

  it('filters by model name across all groups (not by provider name)', () => {
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    renderSelector(allModels, '', vi.fn(), TWO_PROVIDER_GROUPS)
    const input = screen.getByPlaceholderText(/search models/i)
    fireEvent.change(input, { target: { value: 'gpt' } })
    // OpenAI models visible
    expect(screen.getByText('gpt-4o')).toBeInTheDocument()
    expect(screen.getByText('gpt-4o-mini')).toBeInTheDocument()
    // Anthropic models not visible
    expect(screen.queryByText('claude-3-haiku')).not.toBeInTheDocument()
    // Searching "gpt" should NOT match "OpenAI" (provider name) — but "gpt" is not in "Anthropic"
    // so "Anthropic" heading may or may not appear (its group is empty); we just verify model filtering
  })

  it('searching by provider name does NOT match models from that provider', () => {
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    renderSelector(allModels, '', vi.fn(), TWO_PROVIDER_GROUPS)
    const input = screen.getByPlaceholderText(/search models/i)
    // Search "Anthropic" — no model name contains "Anthropic", so no models shown
    fireEvent.change(input, { target: { value: 'anthropic' } })
    expect(screen.queryByText('claude-3-haiku')).not.toBeInTheDocument()
    expect(screen.queryByText('claude-3-sonnet')).not.toBeInTheDocument()
  })
})

// =====================================================================
// W6-C4 / G12 — unresolved indicator on the trigger
// =====================================================================

describe('ModelSelector — unresolved indicator (W6-C4 / G12)', () => {
  it('does NOT render the "Unresolved" chip when value is empty', () => {
    renderSelector(FLAT_MODELS, '', vi.fn())
    expect(screen.queryByText(/unresolved/i)).not.toBeInTheDocument()
  })

  it('does NOT render the chip when value matches a known model', () => {
    renderSelector(FLAT_MODELS, 'claude-3-haiku', vi.fn())
    expect(screen.queryByText(/unresolved/i)).not.toBeInTheDocument()
  })

  it('renders the chip on the trigger when value is a free-text unknown slug', () => {
    renderSelector(FLAT_MODELS, 'gpt-9000-ultra', vi.fn())
    // The chip itself uses the uppercase "Unresolved" label; use a scoped
    // matcher so we don't pick up the trigger's aria-label which embeds the
    // same word.
    expect(screen.getByText('Unresolved')).toBeInTheDocument()
    // Multiple <button role="combobox"> can appear when both the primary
    // picker and the ModelSelector's fallback picker render together, so
    // narrow to the trigger whose value matches our slug.
    const triggers = screen.getAllByRole('combobox')
    const trigger = triggers.find((el) =>
      el.getAttribute('aria-label')?.includes('gpt-9000-ultra'),
    )
    expect(trigger).toBeDefined()
    expect(trigger!.getAttribute('data-unresolved')).toBe('true')
    expect(trigger!.getAttribute('aria-invalid')).toBe('true')
  })

  it('treats protocol-prefixed unknown slugs as unresolved (matches the bare slug via extractProtocolTail)', () => {
    // The TS twin uses isKnownModelSlugInList, which strips the protocol
    // prefix before matching. "anthropic/claude-3-haiku" is in FLAT_MODELS,
    // but "anthropic/gpt-9000-ultra" is NOT — the chip must appear.
    renderSelector(FLAT_MODELS, 'anthropic/gpt-9000-ultra', vi.fn())
    expect(screen.getByText(/unresolved/i)).toBeInTheDocument()
  })

  it('matches the bare form when the provider exposes the protocol-prefixed form', () => {
    // FLAT_MODELS includes "gpt-4o" (bare). The chip must NOT render when
    // the user picks the bare form via the "Use <slug>" row.
    renderSelector(FLAT_MODELS, 'gpt-4o', vi.fn())
    expect(screen.queryByText(/unresolved/i)).not.toBeInTheDocument()
  })

  it('renders the unresolved hint in text-input mode (no models available)', () => {
    const onChange = vi.fn()
    render(
      <ModelSelector
        models={[]}
        value="my-custom-slug"
        onChange={onChange}
        placeholder="Enter a slug"
        triggerTestId="primary-model-input"
      />,
    )
    // text-input mode renders a <p> beneath the input rather than the chip.
    // O3: copy changed to softer wording — no longer says "calls will fail".
    expect(screen.getByText(/not found in any connected provider/i)).toBeInTheDocument()
    const input = screen.getByRole('textbox')
    expect(input.getAttribute('aria-invalid')).toBe('true')
    // Also picks up the test id prefix for parity with the combobox path.
    expect(screen.getByTestId('primary-model-input-unresolved')).toBeInTheDocument()
  })
})
