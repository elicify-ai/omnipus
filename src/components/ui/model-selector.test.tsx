import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { ModelSelector, type ModelGroup, type ModelCatalogGroup } from './model-selector'
import type { CatalogModel } from '@/lib/api/generated/openapi-types'

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
  // jsdom reports every element as 0x0, and a virtualiser with a zero-height
  // viewport renders zero rows — it would "pass" a row-count assertion for the
  // wrong reason. Give the cmdk list a real viewport: the popover fixture is
  // 480 px tall and CommandList caps its own scroll area at max-h-[300px], so
  // 300 px is the height the virtualiser actually sees in the browser.
  // @tanstack/react-virtual measures the scroll element with offsetWidth /
  // offsetHeight, which jsdom hard-codes to 0 — so these are the properties to
  // give a size, not getBoundingClientRect.
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.hasAttribute('cmdk-list') ? 300 : 0
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
    configurable: true,
    get(this: HTMLElement) {
      return this.hasAttribute('cmdk-list') ? 320 : 0
    },
  })
})

/** The trigger button. cmdk's search input is also a `combobox`, so the role
 *  alone is ambiguous once the popover content is rendered inline. */
const triggerButton = () =>
  screen.getAllByRole('combobox').find((el) => el.tagName === 'BUTTON') as HTMLElement

// ModelSelector test suite
// Covers: search-by-model-name, flat list (1 provider), grouped headers (≥2 providers), text-input fallback

// Stub Popover/Command to render inline so we can interact with items without
// a real portal. We keep the real Command semantics for filtering but render
// the content unconditionally (no Radix portal).
vi.mock('@/components/ui/popover', () => {
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
  { providerName: 'Anthropic', providerId: 'anthropic', models: ['claude-3-haiku', 'claude-3-sonnet'] },
  { providerName: 'OpenAI', providerId: 'openai', models: ['gpt-4o', 'gpt-4o-mini'] },
]

const ONE_PROVIDER_GROUPS: ModelGroup[] = [
  { providerName: 'Anthropic', providerId: 'anthropic', models: ['claude-3-haiku', 'claude-3-sonnet'] },
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
// WebKit tabbability — trigger must be reachable via Tab even when the
// `tabIndex` prop is omitted (regression: every historical call site
// omitted it, and `tabIndex={undefined}` on a native <button> omits the
// attribute entirely, making it unreachable via Tab on Safari/WebKit —
// this soft-locked the required Model field on agent-creation Step 1).
// =====================================================================

describe('ModelSelector — WebKit tabbability', () => {
  // Query by data-testid rather than role="combobox": cmdk's CommandInput
  // (rendered inside the always-mounted PopoverContent stub above) ALSO
  // carries role="combobox", so getByRole would match two elements.
  it('trigger carries tabindex="0" by default when tabIndex prop is omitted', () => {
    render(
      <ModelSelector models={FLAT_MODELS} value="" onChange={vi.fn()} triggerTestId="model-trigger" />,
    )
    expect(screen.getByTestId('model-trigger')).toHaveAttribute('tabindex', '0')
  })

  it('caller-supplied tabIndex still overrides the default', () => {
    render(
      <ModelSelector models={FLAT_MODELS} value="" onChange={vi.fn()} tabIndex={-1} triggerTestId="model-trigger" />,
    )
    expect(screen.getByTestId('model-trigger')).toHaveAttribute('tabindex', '-1')
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

// =====================================================================
// UAT model-catalog fix — constrained dropdown (no unresolved badge)
// =====================================================================

describe('ModelSelector — constrainToCatalog (UAT model-catalog fix)', () => {
  it('does NOT render an unresolved chip for an unknown slug when constrained', () => {
    // Without constraint this slug would flag "Unresolved"; constrained, it must not.
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value="gpt-9000-ultra"
        onChange={vi.fn()}
        constrainToCatalog
      />,
    )
    expect(screen.queryByText(/unresolved/i)).not.toBeInTheDocument()
  })

  it('does NOT render the free-text "Use <slug>" row when constrained', () => {
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
      />,
    )
    const input = screen.getByPlaceholderText(/search models/i)
    fireEvent.change(input, { target: { value: 'totally-made-up' } })
    // The catalogue models still filter; the custom "Use …" escape hatch is gone.
    expect(screen.queryByText(/^Use /)).not.toBeInTheDocument()
    expect(screen.queryByText(/totally-made-up/)).not.toBeInTheDocument()
  })

  it('still lets the user pick a catalogue model when constrained', () => {
    const onChange = vi.fn()
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value=""
        onChange={onChange}
        constrainToCatalog
      />,
    )
    fireEvent.click(screen.getByText('claude-3-haiku'))
    expect(onChange).toHaveBeenCalledWith('claude-3-haiku')
  })

  it('renders a disabled "no models" state (not a free-text input) when constrained and empty', () => {
    render(
      <ModelSelector
        models={[]}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
        triggerTestId="composer-model-selector"
        emptyCatalogHint="Connect a provider to pick a model"
      />,
    )
    // No free-text input — the operator decision is "non-catalogue model not selectable".
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.getByText(/connect a provider to pick a model/i)).toBeInTheDocument()
  })

  it('allows free-text bootstrap (no warning) when constrained, empty, and allowFreeTextWhenEmpty', () => {
    const onChange = vi.fn()
    render(
      <ModelSelector
        models={[]}
        value="my-first-slug"
        onChange={onChange}
        constrainToCatalog
        allowFreeTextWhenEmpty
      />,
    )
    // Onboarding manual-provider bootstrap: a text input, but never an
    // "unresolved" warning (the typed slug becomes the catalogue).
    expect(screen.getByRole('textbox')).toBeInTheDocument()
    expect(screen.queryByText(/not found in any connected provider/i)).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'glm-5.2' } })
    expect(onChange).toHaveBeenCalledWith('glm-5.2')
  })
})

// =====================================================================
// catalogStatus — the four-state provider-catalog fix.
//
// Bug: the picker used to collapse "still loading", "fetch failed", and
// "genuinely no provider connected" into one "connect a provider" message
// — true for only the last case. Motivating incident: CI logged the
// gateway's upstream fetch to openrouter.ai failing 9 times with
// `context canceled` (zero successes) while /providers itself was healthy
// (curl from the same worker: 200 in 0.46s) — and the picker still told
// the user no provider was connected.
// =====================================================================

describe('ModelSelector — catalogStatus (four-state provider-catalog fix)', () => {
  it('renders a distinct loading state, not the "connect a provider" empty-catalog message', () => {
    render(
      <ModelSelector
        models={[]}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
        catalogStatus="loading"
        triggerTestId="model-trigger"
        emptyCatalogHint="Connect a provider in Settings to pick a model"
      />,
    )
    // The trigger stays a REAL combobox while loading (see the swallowed-click
    // regression below); the loading feedback moved inside the popover, so it
    // is reached by opening the trigger rather than rendered beside it.
    const trigger = screen.getByTestId('model-trigger')
    expect(trigger).toHaveAttribute('role', 'combobox')
    expect(trigger).toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText(/connect a provider/i)).not.toBeInTheDocument()

    fireEvent.click(trigger)
    expect(screen.getByText(/loading models/i)).toBeInTheDocument()
  })

  // REGRESSION — the swallowed click (root-caused 2026-08-14, reproduced
  // against a real gateway before this fix). The loading state used to render
  // a non-interactive <div role="status"> wearing triggerTestId. A click
  // during the catalog fetch (0.13s idle, 1.2-4.5s under a loaded e2e shard)
  // hit that div, did nothing, and was LOST: when the catalog arrived the real
  // combobox replaced it and nothing re-opened the popover. The user got no
  // feedback and had to click again. It is the true cause of the create-agent
  // e2e failure that was misdiagnosed twice — once as OpenRouter latency, once
  // as a provider-configuration problem.
  it('does not swallow a click made while the catalog is still loading', () => {
    const { rerender } = render(
      <ModelSelector
        models={[]}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
        catalogStatus="loading"
        triggerTestId="model-trigger"
      />,
    )

    // The user clicks "Model" while the fetch is still in flight.
    fireEvent.click(screen.getByTestId('model-trigger'))
    expect(screen.getByText(/loading models/i)).toBeInTheDocument()
    expect(screen.queryByRole('option')).not.toBeInTheDocument()

    // The catalog lands. The popover the user already opened must now show the
    // models — WITHOUT a second click, which is exactly what used to be needed.
    rerender(
      <ModelSelector
        models={['gpt-4o', 'claude-3-haiku']}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
        catalogStatus="ready"
        triggerTestId="model-trigger"
      />,
    )
    expect(screen.getAllByRole('option').length).toBeGreaterThan(0)
    expect(screen.getByText('gpt-4o')).toBeInTheDocument()
    expect(screen.queryByText(/loading models/i)).not.toBeInTheDocument()
  })

  it('renders the real error message and a working Retry, not the "connect a provider" empty-catalog message', () => {
    const onRetryCatalog = vi.fn()
    render(
      <ModelSelector
        models={[]}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
        triggerTestId="wizard-model"
        catalogStatus="error"
        catalogErrorMessage='Get "https://openrouter.ai/api/v1/models": context canceled'
        onRetryCatalog={onRetryCatalog}
        emptyCatalogHint="Connect a provider in Settings to pick a model"
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(/context canceled/)
    expect(screen.queryByText(/connect a provider/i)).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('wizard-model-retry'))
    expect(onRetryCatalog).toHaveBeenCalledTimes(1)
  })

  it('omits the Retry control when onRetryCatalog is not provided', () => {
    render(
      <ModelSelector
        models={[]}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
        triggerTestId="wizard-model"
        catalogStatus="error"
        catalogErrorMessage="network error"
      />,
    )
    expect(screen.queryByTestId('wizard-model-retry')).not.toBeInTheDocument()
  })

  it('defaults catalogStatus to "ready" so pre-existing call sites are unaffected', () => {
    render(
      <ModelSelector
        models={[]}
        value=""
        onChange={vi.fn()}
        constrainToCatalog
        emptyCatalogHint="Connect a provider in Settings to pick a model"
      />,
    )
    expect(screen.getByText(/connect a provider in settings/i)).toBeInTheDocument()
    expect(screen.queryByText(/loading models/i)).not.toBeInTheDocument()
  })
})

// =====================================================================
// O3 two-field model selector — onPairChange callback
// =====================================================================

describe('ModelSelector — O3 onPairChange ({model, provider} pair)', () => {
  it('calls onPairChange with model + provider when picking from a grouped dropdown', () => {
    const onChange = vi.fn()
    const onPairChange = vi.fn()
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    render(
      <ModelSelector
        models={allModels}
        value=""
        onChange={onChange}
        onPairChange={onPairChange}
        providerGroups={TWO_PROVIDER_GROUPS}
      />,
    )
    // Pick claude-3-haiku (in the Anthropic group, providerId='anthropic').
    fireEvent.click(screen.getByText('claude-3-haiku'))
    expect(onChange).toHaveBeenCalledWith('claude-3-haiku')
    expect(onPairChange).toHaveBeenCalledWith({ model: 'claude-3-haiku', provider: 'anthropic' })
  })

  it('resolves the provider from the correct group for OpenAI models', () => {
    const onChange = vi.fn()
    const onPairChange = vi.fn()
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    render(
      <ModelSelector
        models={allModels}
        value=""
        onChange={onChange}
        onPairChange={onPairChange}
        providerGroups={TWO_PROVIDER_GROUPS}
      />,
    )
    fireEvent.click(screen.getByText('gpt-4o'))
    expect(onChange).toHaveBeenCalledWith('gpt-4o')
    expect(onPairChange).toHaveBeenCalledWith({ model: 'gpt-4o', provider: 'openai' })
  })

  it('does NOT call onPairChange for free-text "Use <slug>" picks', () => {
    const onChange = vi.fn()
    const onPairChange = vi.fn()
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    render(
      <ModelSelector
        models={allModels}
        value=""
        onChange={onChange}
        onPairChange={onPairChange}
        providerGroups={TWO_PROVIDER_GROUPS}
      />,
    )
    const input = screen.getByPlaceholderText(/search models/i)
    fireEvent.change(input, { target: { value: 'my-custom-model' } })
    // Click the "Use my-custom-model" option.
    const customItem = screen.getByText(/my-custom-model/)
    fireEvent.click(customItem)
    // onChange fires for the free-text slug.
    expect(onChange).toHaveBeenCalledWith('my-custom-model')
    // onPairChange must NOT be called for custom (unresolvable) picks.
    expect(onPairChange).not.toHaveBeenCalled()
  })

  it('passes empty provider string when group has no providerId', () => {
    const onChange = vi.fn()
    const onPairChange = vi.fn()
    const groupsWithoutId: ModelGroup[] = [
      { providerName: 'Legacy Provider', models: ['legacy-model-1'] },
    ]
    render(
      <ModelSelector
        models={['legacy-model-1']}
        value=""
        onChange={onChange}
        onPairChange={onPairChange}
        providerGroups={groupsWithoutId}
      />,
    )
    fireEvent.click(screen.getByText('legacy-model-1'))
    expect(onPairChange).toHaveBeenCalledWith({ model: 'legacy-model-1', provider: '' })
  })

  it('works without onPairChange (backward compatibility — does not throw)', () => {
    const onChange = vi.fn()
    const allModels = TWO_PROVIDER_GROUPS.flatMap((g) => g.models)
    expect(() => {
      render(
        <ModelSelector
          models={allModels}
          value=""
          onChange={onChange}
          providerGroups={TWO_PROVIDER_GROUPS}
        />,
      )
      fireEvent.click(screen.getByText('claude-3-haiku'))
    }).not.toThrow()
    expect(onChange).toHaveBeenCalledWith('claude-3-haiku')
  })
})

// =====================================================================
// variant="ghost" — flat/compact trigger for the chat header
// =====================================================================

describe('ModelSelector — variant="ghost"', () => {
  it('renders a trigger without a border class in ghost mode', () => {
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value="claude-3-haiku"
        onChange={vi.fn()}
        variant="ghost"
        triggerTestId="ghost-trigger"
      />,
    )
    const trigger = screen.getByTestId('ghost-trigger')
    // Ghost trigger must NOT carry the form-field border class.
    expect(trigger.className).not.toContain('border')
    // Ghost trigger must carry the compact height class (composer context-row h-7).
    expect(trigger.className).toContain('h-7')
    // Ghost trigger must carry the compact padding class.
    expect(trigger.className).toContain('px-1.5')
  })

  it('still renders the selected value and caret in ghost mode', () => {
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value="claude-3-haiku"
        onChange={vi.fn()}
        variant="ghost"
        triggerTestId="ghost-trigger"
      />,
    )
    // The popover mock renders content inline, so the model text may appear
    // in both the trigger span and the list. Use getAllByText and verify at
    // least one instance is present (the trigger display value).
    expect(screen.getAllByText('claude-3-haiku').length).toBeGreaterThanOrEqual(1)
    // CommandInput also carries role="combobox" in cmdk; confirm the trigger
    // button itself is present by test id.
    expect(screen.getByTestId('ghost-trigger')).toBeInTheDocument()
  })

  it('opens the popover and lists models in ghost mode', () => {
    const onChange = vi.fn()
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value=""
        onChange={onChange}
        variant="ghost"
        triggerTestId="ghost-trigger"
      />,
    )
    // Popover content is rendered by our mock; all models visible.
    for (const model of FLAT_MODELS) {
      expect(screen.getByText(model)).toBeInTheDocument()
    }
    // Picking a model calls onChange.
    fireEvent.click(screen.getByText('gpt-4o'))
    expect(onChange).toHaveBeenCalledWith('gpt-4o')
  })

  it('still shows the unresolved chip on the ghost trigger for unknown slugs', () => {
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value="totally-unknown-model"
        onChange={vi.fn()}
        variant="ghost"
        triggerTestId="ghost-trigger"
      />,
    )
    expect(screen.getByText('Unresolved')).toBeInTheDocument()
    const trigger = screen.getByTestId('ghost-trigger')
    expect(trigger.getAttribute('data-unresolved')).toBe('true')
  })

  it('default variant (omitted) preserves the bordered form-field look', () => {
    // Regression guard: omitting variant must not regress existing styled triggers.
    render(
      <ModelSelector
        models={FLAT_MODELS}
        value="claude-3-haiku"
        onChange={vi.fn()}
        triggerTestId="default-trigger"
      />,
    )
    const trigger = screen.getByTestId('default-trigger')
    // Default trigger carries the border class.
    expect(trigger.className).toContain('border')
    // Default trigger uses the taller h-10 class.
    expect(trigger.className).toContain('h-10')
  })
})

describe('ModelSelector — search field focus ring (operator request 2026-08-13)', () => {
  // globals.css applies ONE gold focus ring to every focusable element and
  // forbids per-component ring utilities; the only sanctioned escape is the
  // data-no-focus-ring attribute, for composite widgets whose focus is shown
  // by a parent surface. The search input qualifies: it is auto-focused the
  // instant the popover opens and is the only focusable thing inside it, so
  // the ring fired on every single open and boxed a field the bordered
  // popover already frames.
  //
  // Pinned as a test because the attribute is one easily-dropped word in a
  // JSX prop list, and losing it brings the ring back with no other symptom.
  it('opts the search input out of the global focus ring', () => {
    renderSelector(FLAT_MODELS, '', vi.fn())
    const input = screen.getByPlaceholderText(/search models/i)
    expect(input).toHaveAttribute('data-no-focus-ring')
  })

  it('does not opt any OTHER control out of the ring', () => {
    // Guards the opposite failure: a blanket application of the attribute
    // would silently strip focus visibility from the whole widget, which is
    // an accessibility regression rather than a cosmetic one.
    renderSelector(FLAT_MODELS, '', vi.fn())
    const optedOut = document.querySelectorAll('[data-no-focus-ring]')
    expect(optedOut).toHaveLength(1)
  })
})

// =====================================================================
// ADR-068 T068-22 — catalog mode: vendor/date ordering, Recommended chips,
// virtualisation above 100 rows, the onboarding label, provider filtering.
//
// FR-030 (order + chips + virtualisation), FR-029 (label, empty value,
// nothing pre-selected), FR-019 (`filterProviders`, connected-only).
// =====================================================================

function catalogModel(over: Partial<CatalogModel> & { id: string }): CatalogModel {
  return {
    ...over,
    id: over.id,
    name: over.name ?? over.id,
    context_window: over.context_window ?? 200000,
    max_output_tokens: over.max_output_tokens ?? 8192,
    input_modalities: over.input_modalities ?? ['text'],
    tool_call: over.tool_call ?? true,
    status: over.status ?? 'active',
  }
}

/** The FR-030 ordering fixture, deliberately in the WRONG order on input. */
const ORDERING_CATALOG: CatalogModel[] = [
  catalogModel({ id: 'openai/gpt-5.4', release_date: '2026-03-01' }),
  catalogModel({ id: 'anthropic/claude-3.5-haiku', release_date: '2024-10-01' }),
  catalogModel({ id: 'x/nodate' }),
  catalogModel({ id: 'x/dated', release_date: '2025-01-01' }),
  catalogModel({ id: 'anthropic/claude-sonnet-4.6', release_date: '2026-02-01' }),
]

function bulkCatalog(n: number): CatalogModel[] {
  return Array.from({ length: n }, (_, i) =>
    // Zero-padded so the id order is the render order inside the one vendor group.
    catalogModel({ id: `acme/model-${String(i).padStart(3, '0')}`, tool_call: false }),
  )
}

function renderCatalog(
  groups: ModelCatalogGroup[],
  extra: Partial<React.ComponentProps<typeof ModelSelector>> = {},
) {
  return render(
    <ModelSelector
      models={[]}
      value=""
      onChange={vi.fn()}
      constrainToCatalog
      catalogGroups={groups}
      {...extra}
    />,
  )
}

const optionTexts = () => screen.getAllByRole('option').map((el) => el.textContent ?? '')

describe('ModelSelector — FR-030 vendor/date ordering', () => {
  it('groups by vendor and orders each group by release date desc, undated last', () => {
    renderCatalog([{ providerId: 'openrouter', providerName: 'OpenRouter', status: 'connected', models: ORDERING_CATALOG }])

    // Vendor groups are alphabetical, so anthropic then openai then x.
    const headings = screen
      .getAllByTestId('model-selector-vendor-heading')
      .map((el) => el.textContent)
    expect(headings).toEqual(['anthropic', 'openai', 'x'])

    const texts = optionTexts()
    // Within Anthropic: 2026-02 before 2024-10.
    expect(texts[0]).toContain('anthropic/claude-sonnet-4.6')
    expect(texts[1]).toContain('anthropic/claude-3.5-haiku')
    expect(texts[2]).toContain('openai/gpt-5.4')
    // Undated is the LAST row of its own group, below the dated sibling.
    expect(texts[3]).toContain('x/dated')
    expect(texts[4]).toContain('x/nodate')
  })

  it('orders same-date models by id ascending (dataset row 8)', () => {
    renderCatalog([
      {
        providerId: 'openrouter',
        providerName: 'OpenRouter',
        models: [
          catalogModel({ id: 'anthropic/zeta', release_date: '2026-02-01' }),
          catalogModel({ id: 'anthropic/alpha', release_date: '2026-02-01' }),
        ],
      },
    ])
    const texts = optionTexts()
    expect(texts[0]).toContain('anthropic/alpha')
    expect(texts[1]).toContain('anthropic/zeta')
  })
})

describe('ModelSelector — FR-030 Recommended for chat chips', () => {
  it('marks at most three per provider and never a 127,999-window model', () => {
    renderCatalog([
      {
        providerId: 'openrouter',
        providerName: 'OpenRouter',
        models: [
          catalogModel({ id: 'a/one', context_window: 200000, release_date: '2026-05-01' }),
          catalogModel({ id: 'a/two', context_window: 200000, release_date: '2026-04-01' }),
          catalogModel({ id: 'a/three', context_window: 128000, release_date: '2026-03-01' }),
          catalogModel({ id: 'a/four', context_window: 128000, release_date: '2026-02-01' }),
          catalogModel({ id: 'a/five', context_window: 127999, release_date: '2026-06-01' }),
        ],
      },
    ])

    expect(screen.getAllByText('Recommended for chat')).toHaveLength(3)

    const chipped = screen
      .getAllByRole('option')
      .filter((el) => el.textContent?.includes('Recommended for chat'))
      .map((el) => el.getAttribute('data-model'))
    // Newest three eligible; the oldest eligible (a/four) drops off.
    expect(chipped).toEqual(['a/one', 'a/two', 'a/three'])
    // The 127,999 window is below the bar even though it is the newest model.
    expect(chipped).not.toContain('a/five')
  })

  it('does not chip a model that cannot call tools, whatever its window', () => {
    renderCatalog([
      {
        providerId: 'p',
        providerName: 'P',
        models: [catalogModel({ id: 'a/huge', context_window: 1000000, tool_call: false })],
      },
    ])
    expect(screen.queryByText('Recommended for chat')).not.toBeInTheDocument()
  })

  it('counts the three per PROVIDER, not across the whole list', () => {
    renderCatalog([
      { providerId: 'p1', providerName: 'P1', models: bulkEligible('p1') },
      { providerId: 'p2', providerName: 'P2', models: bulkEligible('p2') },
    ])
    expect(screen.getAllByText('Recommended for chat')).toHaveLength(6)
  })
})

function bulkEligible(prefix: string): CatalogModel[] {
  return Array.from({ length: 5 }, (_, i) =>
    catalogModel({ id: `${prefix}/m${i}`, release_date: `2026-0${i + 1}-01` }),
  )
}

describe('ModelSelector — FR-030 virtualisation threshold', () => {
  it('renders nothing but a "No models" empty state for 0 models', () => {
    renderCatalog([])
    expect(screen.queryAllByRole('option')).toHaveLength(0)
    expect(screen.getByText(/No models/i)).toBeInTheDocument()
  })

  it('renders the single row for 1 model', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(1) }])
    expect(screen.getAllByRole('option')).toHaveLength(1)
  })

  it('renders all 100 rows at the threshold (100 is NOT virtualised)', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(100) }])
    expect(screen.getAllByRole('option')).toHaveLength(100)
  })

  it('virtualises at 101 — far fewer rows than models, but never zero', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(101) }])
    const rows = screen.getAllByRole('option')
    expect(rows.length).toBeGreaterThanOrEqual(10)
    expect(rows.length).toBeLessThanOrEqual(22)
  })

  it('virtualises the full 359-model OpenRouter catalog the same way', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(359) }])
    const rows = screen.getAllByRole('option')
    // Lower bound as well as upper: a virtualiser handed a zero-height viewport
    // renders ONE row and would sail past a "fewer than 359" assertion.
    expect(rows.length).toBeGreaterThanOrEqual(10)
    expect(rows.length).toBeLessThanOrEqual(22)
  })

  it('keeps the virtualised rows in catalog order from the top of the list', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(359) }])
    expect(optionTexts()[0]).toContain('acme/model-000')
  })
})

describe('ModelSelector — aria-setsize / aria-posinset', () => {
  it('numbers every row against the full list when the list is whole', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(100) }])
    const rows = screen.getAllByRole('option')
    expect(rows[0]).toHaveAttribute('aria-posinset', '1')
    expect(rows[0]).toHaveAttribute('aria-setsize', '100')
    expect(rows[99]).toHaveAttribute('aria-posinset', '100')
    expect(rows[99]).toHaveAttribute('aria-setsize', '100')
  })

  it('still reports the FULL set size on a virtualised list (only a window is in the DOM)', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(359) }])
    const rows = screen.getAllByRole('option')
    expect(rows.length).toBeGreaterThanOrEqual(10)
    expect(rows.length).toBeLessThan(359)
    for (const row of rows) expect(row).toHaveAttribute('aria-setsize', '359')
    expect(rows[0]).toHaveAttribute('aria-posinset', '1')
  })
})

describe('ModelSelector — FR-029 onboarding label and empty value', () => {
  it('uses the caller label verbatim as the accessible name when there is no value', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(3) }], {
      label: 'Model for your first agent',
    })
    expect(screen.getByRole('combobox', { name: 'Model for your first agent' })).toBeInTheDocument()
  })

  it('keeps the label at the front of the accessible name once a model is chosen', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(3) }], {
      label: 'Model for your first agent',
      value: 'acme/model-001',
    })
    const trigger = triggerButton()
    expect(trigger.getAttribute('aria-label')).toMatch(/^Model for your first agent/)
    expect(trigger.getAttribute('aria-label')).toContain('acme/model-001')
  })

  it('pre-selects nothing: no row is marked chosen and the trigger shows the placeholder', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(3) }], {
      label: 'Model for your first agent',
      placeholder: 'Select a model',
    })
    expect(screen.getAllByRole('option').filter((el) => el.hasAttribute('data-chosen'))).toHaveLength(0)
    expect(triggerButton()).toHaveTextContent('Select a model')
  })

  it('marks exactly the chosen row when a value IS set', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(3) }], {
      value: 'acme/model-001',
    })
    const chosen = screen.getAllByRole('option').filter((el) => el.hasAttribute('data-chosen'))
    expect(chosen).toHaveLength(1)
    expect(chosen[0]).toHaveAttribute('data-model', 'acme/model-001')
  })

  it('falls back to the historical accessible name when no label is given', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: bulkCatalog(3) }])
    expect(triggerButton().getAttribute('aria-label')).toMatch(/^Model selector/)
  })

  it('emits the picked model and its provider id', () => {
    const onChange = vi.fn()
    const onPairChange = vi.fn()
    renderCatalog([{ providerId: 'openrouter', providerName: 'OpenRouter', models: bulkCatalog(3) }], {
      onChange,
      onPairChange,
    })
    fireEvent.click(screen.getByRole('option', { name: /acme\/model-001/ }))
    expect(onChange).toHaveBeenCalledWith('acme/model-001')
    expect(onPairChange).toHaveBeenCalledWith({ model: 'acme/model-001', provider: 'openrouter' })
  })
})

describe('ModelSelector — FR-019 filterProviders', () => {
  const TWO_CATALOG_GROUPS: ModelCatalogGroup[] = [
    {
      providerId: 'openrouter',
      providerName: 'OpenRouter',
      status: 'connected',
      models: [catalogModel({ id: 'a/live' })],
    },
    {
      providerId: 'openai',
      providerName: 'OpenAI',
      status: 'disconnected',
      models: [catalogModel({ id: 'b/dead' })],
    },
    {
      providerId: 'openai-chatgpt',
      providerName: 'ChatGPT',
      status: 'signed_in',
      models: [catalogModel({ id: 'c/session' })],
    },
  ]

  it('shows every provider when the prop is omitted', () => {
    renderCatalog(TWO_CATALOG_GROUPS)
    expect(screen.getAllByRole('option')).toHaveLength(3)
  })

  it('keeps only connected and signed-in providers when filtered by status', () => {
    renderCatalog(TWO_CATALOG_GROUPS, { filterProviders: { statuses: ['connected', 'signed_in'] } })
    const models = screen.getAllByRole('option').map((el) => el.getAttribute('data-model'))
    expect(models).toEqual(['a/live', 'c/session'])
  })

  it('narrows to a single provider by id', () => {
    renderCatalog(TWO_CATALOG_GROUPS, { filterProviders: { providerIds: ['openai'] } })
    const models = screen.getAllByRole('option').map((el) => el.getAttribute('data-model'))
    expect(models).toEqual(['b/dead'])
  })

  it('applies both filters together, and an empty result is the "No models" state', () => {
    renderCatalog(TWO_CATALOG_GROUPS, {
      filterProviders: { providerIds: ['openai'], statuses: ['connected'] },
    })
    expect(screen.queryAllByRole('option')).toHaveLength(0)
    expect(screen.getByText(/No models/i)).toBeInTheDocument()
  })

  it('also narrows the legacy string providerGroups by id', () => {
    render(
      <ModelSelector
        models={[]}
        value=""
        onChange={vi.fn()}
        providerGroups={TWO_PROVIDER_GROUPS}
        filterProviders={{ providerIds: ['anthropic'] }}
      />,
    )
    expect(screen.getByText('Anthropic')).toBeInTheDocument()
    expect(screen.queryByText('OpenAI')).not.toBeInTheDocument()
    expect(screen.queryByText('gpt-4o')).not.toBeInTheDocument()
  })
})

describe('ModelSelector — catalog mode search', () => {
  it('filters the catalog rows by id substring and renumbers the set', () => {
    renderCatalog([{ providerId: 'p', providerName: 'P', models: ORDERING_CATALOG }])
    fireEvent.change(screen.getByPlaceholderText('Search models...'), {
      target: { value: 'claude' },
    })
    const rows = screen.getAllByRole('option')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveAttribute('aria-setsize', '2')
    expect(rows[1]).toHaveAttribute('aria-posinset', '2')
  })

  it('matches the display name as well as the id', () => {
    renderCatalog([
      {
        providerId: 'p',
        providerName: 'P',
        models: [catalogModel({ id: 'a/one', name: 'Sonnet Ultra' }), catalogModel({ id: 'a/two', name: 'Other' })],
      },
    ])
    fireEvent.change(screen.getByPlaceholderText('Search models...'), {
      target: { value: 'sonnet' },
    })
    const rows = screen.getAllByRole('option')
    expect(rows).toHaveLength(1)
    expect(rows[0]).toHaveAttribute('data-model', 'a/one')
  })
})
