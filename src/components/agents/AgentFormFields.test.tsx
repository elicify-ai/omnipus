/**
 * AgentFormFields.test.tsx
 *
 * Tests for the identity primitives the wizard and the edit slide-over share:
 *   - <AvatarColorPicker>  — 10-colour identity palette (Azure..Grey) with semantic aria-labels
 *   - <AgentLookPicker>    — the ONE avatar editor (preview, 5 figure pictures,
 *                            searchable Role badge dropdown, 10 colour dots)
 *
 * <IconPicker> and <AvatarHeader> tested deleted components and were removed
 * with the legacy icon system (founder 2026-10-10: one mark everywhere); the
 * AvatarColorPicker coverage below is unchanged.
 *
 * Traces:
 *   - wave5a-wire-ui-spec.md — Scenario: wizard and profile share identity widgets
 *   - wave5a-wire-ui-spec.md — US-7 AC1: Agent profile renders identity section
 *   - MESSAGES.md founder decisions 2026-10-10 (avatar consistency + agent form simplification)
 */

// jsdom does not implement ResizeObserver (used by cmdk inside SmartSelect's
// searchable popover branch). Polyfill a noop — the Role badge dropdown lists
// 31 entries, which crosses SmartSelect's SEARCHABLE_THRESHOLD of 5, so the
// popover+Command path is exercised and would otherwise throw on render.
import { describe, it, expect, vi } from 'vitest'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { render, screen, fireEvent, within } from '@testing-library/react'
import { AvatarColorPicker, AgentLookPicker } from './AgentFormFields'

// SPEC "Locked identity vocabulary" ordered palette (Azure..Grey).
// Not src/lib/constants.ts AVATAR_COLORS (the retired eight brand swatches)
// and not a value read off the picker.
const IDENTITY_PALETTE = [
  { name: 'Azure', hex: '#3B82F6' },
  { name: 'Sky', hex: '#38BDF8' },
  { name: 'Cyan', hex: '#22D3EE' },
  { name: 'Indigo', hex: '#818CF8' },
  { name: 'Violet', hex: '#A78BFA' },
  { name: 'Purple', hex: '#C084FC' },
  { name: 'Fuchsia', hex: '#E879F9' },
  { name: 'Pink', hex: '#F472B6' },
  { name: 'Orange', hex: '#FB923C' },
  { name: 'Grey', hex: '#9CA3AF' },
] as const

const RETIRED_SWATCH_NAMES = ['Verdant', 'Amethyst', 'Saffron', 'Ember', 'Crimson', 'Slate', 'Forge Gold'] as const

describe('AvatarColorPicker', () => {
  it('renders one button per identity palette colour (Azure through Grey) and not the retired swatches', () => {
    // Traces: navigation spec FR-017 / BDD-05.2 — the editor offers the ten
    // ordered palette colours, not the previous eight brand swatches.
    const onChange = vi.fn()
    render(<AvatarColorPicker value={IDENTITY_PALETTE[0].hex} onChange={onChange} />)
    for (const { name, hex } of IDENTITY_PALETTE) {
      expect(
        screen.getByRole('button', { name }),
        `missing swatch for ${name} (${hex})`,
      ).toBeInTheDocument()
    }
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(IDENTITY_PALETTE.length)
    for (const retired of RETIRED_SWATCH_NAMES) {
      expect(screen.queryByRole('button', { name: retired }), retired).not.toBeInTheDocument()
    }
  })

  it('calls onChange with the canonical Sky hex when Sky is clicked', () => {
    // Traces: FR-017 — the second palette entry is Sky #38BDF8, uppercase.
    const onChange = vi.fn()
    const sky = IDENTITY_PALETTE[1]
    render(<AvatarColorPicker value={IDENTITY_PALETTE[0].hex} onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: sky.name }))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith(sky.hex)
  })

  it('marks the selected swatch with aria-pressed=true (and others false)', () => {
    // Traces: the selected palette swatch is distinguishable. aria-pressed
    // is the signal for a toggle button. Fuchsia is palette entry 7.
    const onChange = vi.fn()
    const selected = IDENTITY_PALETTE[6]
    render(<AvatarColorPicker value={selected.hex} onChange={onChange} />)
    for (const { name, hex } of IDENTITY_PALETTE) {
      const btn = screen.getByRole('button', { name })
      expect(btn).toHaveAttribute('aria-pressed', hex === selected.hex ? 'true' : 'false')
    }
  })

  it('honours the testIdPrefix prop when generating data-testid values', () => {
    // Traces: wave5a-wire-ui-spec.md — wizard and profile can share the
    // component without colliding on test ids. The profile uses
    // "avatar-color" (default), so we pick a different prefix here to
    // assert the prefix actually flows through. Azure is palette entry 1.
    const onChange = vi.fn()
    const azure = IDENTITY_PALETTE[0]
    render(
      <AvatarColorPicker
        value={azure.hex}
        onChange={onChange}
        testIdPrefix="wizard-color"
      />,
    )
    expect(screen.getByTestId(`wizard-color-${azure.name}`)).toBeInTheDocument()
  })
})

// ── AgentLookPicker — the ONE avatar editor (founder 2026-10-10) ─────────────
//
// Oracle (MESSAGES.md, FOUNDER DECISIONS 2026-10-10): "agent form 'Pick by
// picture': one big live preview; ONE row of five figure thumbnails drawn in
// the chosen colour and badge, order Omnipus, Man, Robot, Woman, Monogram;
// ONE row of ten colour dots" and "the role badge is ONE searchable dropdown
// labelled 'Role badge'; EACH OPTION SHOWS ITS BADGE ICON; default General".
// Figure order and the 31 role vocabulary come from the ARCH-DECISIONS spec
// table, never from the component.

const FIGURE_ORDER = ['Omnipus', 'Man', 'Robot', 'Woman', 'Monogram'] as const

/** The 31 role slugs with their spec labels (ARCH-DECISIONS "Locked identity vocabulary"). */
const ROLE_LABELS: ReadonlyArray<readonly [slug: string, label: string]> = [
  ['writer', 'Writer'], ['designer', 'Designer'], ['image', 'Image creator'],
  ['video', 'Video producer'], ['audio', 'Audio and voice'], ['social', 'Social media'],
  ['developer', 'Developer'], ['data', 'Data engineer'], ['analyst', 'Data analyst'],
  ['itops', 'IT and operations'], ['automation', 'Automation'], ['security', 'Security'],
  ['quality', 'Quality and QA'], ['science', 'Science and lab'], ['orchestrator', 'Orchestrator'],
  ['project', 'Project manager'], ['product', 'Product manager'], ['sales', 'Sales'],
  ['marketing', 'Marketing'], ['finance', 'Finance'], ['legal', 'Legal and compliance'],
  ['support', 'Customer support'], ['documents', 'Documents'], ['researcher', 'Researcher'],
  ['people', 'People and HR'], ['tutor', 'Tutor'], ['knowledge', 'Knowledge and library'],
  ['translator', 'Translator'], ['general', 'General assistant'], ['personal', 'Personal assistant'],
  ['office', 'Office assistant'],
]

async function renderLookPicker(overrides: Partial<Parameters<typeof AgentLookPicker>[0]> = {}) {
  const onFigureChange = vi.fn()
  const onRoleChange = vi.fn()
  const onColorChange = vi.fn()
  render(
    <AgentLookPicker
      name="Rivet"
      figure="Omnipus"
      role="general"
      color={IDENTITY_PALETTE[1].hex /* Sky */}
      onFigureChange={onFigureChange}
      onRoleChange={onRoleChange}
      onColorChange={onColorChange}
      {...overrides}
    />,
  )
  return { onFigureChange, onRoleChange, onColorChange }
}

describe('AgentLookPicker — pick by picture (founder 2026-10-10)', () => {
  it('shows the big live preview drawn with the current figure, badge and colour', () => {
    // Oracle: "one big live preview" of the mark. The preview is the 48px
    // mark inside the look block; it carries the chosen figure/role/colour.
    renderLookPicker({ figure: 'Robot', role: 'developer' })
    const preview = screen.getByTestId('avatar-look-preview')
    const mark = within(preview).getByTestId('agent-icon')
    expect(mark).toHaveAttribute('data-figure', 'Robot')
    expect(mark.querySelector('g[data-role="developer"]')).not.toBeNull()
    expect(mark).toHaveStyle({ color: 'rgb(56, 189, 248)' }) // Sky #38BDF8
  })

  it('renders exactly five Look thumbnails in the founder order, each drawn in the chosen colour', () => {
    // Oracle: ONE row of five figure thumbnails, order Omnipus, Man, Robot,
    // Woman, Monogram, each drawn in the chosen colour and badge.
    renderLookPicker({ role: 'security' })
    const group = screen.getByRole('group', { name: 'Look' })
    const thumbs = within(group).getAllByTestId(/^avatar-figure-/)
    expect(thumbs.map((t) => t.getAttribute('data-testid'))).toEqual(
      FIGURE_ORDER.map((figure) => `avatar-figure-${figure}`),
    )
    for (const [index, figure] of FIGURE_ORDER.entries()) {
      const mark = within(thumbs[index]).getByTestId('agent-icon')
      expect(mark).toHaveAttribute('data-figure', figure)
      expect(mark.querySelector('g[data-role="security"]')).not.toBeNull()
      expect(mark).toHaveStyle({ color: 'rgb(56, 189, 248)' }) // Sky — the chosen colour
    }
  })

  it('clicking a Look thumbnail calls onFigureChange with that figure', async () => {
    const { onFigureChange } = await renderLookPicker()
    fireEvent.click(screen.getByTestId('avatar-figure-Robot'))
    expect(onFigureChange).toHaveBeenCalledTimes(1)
    expect(onFigureChange).toHaveBeenCalledWith('Robot')
  })

  it('labels the role control "Role badge" and lists all 31 roles, each option showing its own badge glyph', async () => {
    // Oracle: ONE searchable dropdown labelled "Role badge"; EACH OPTION
    // SHOWS ITS BADGE ICON. Every spec role must be offered exactly once,
    // with a badge glyph whose data-role-badge equals that option's role.
    await renderLookPicker()
    const trigger = screen.getByRole('combobox', { name: 'Role badge' })
    expect(trigger).toBeInTheDocument()
    fireEvent.click(trigger)
    const listbox = await screen.findByRole('listbox')
    for (const [slug, label] of ROLE_LABELS) {
      const option = within(listbox).getByRole('option', { name: label })
      const badge = option.querySelector('svg[data-role-badge]')
      expect(badge, `option ${label} shows a badge glyph`).not.toBeNull()
      expect(badge).toHaveAttribute('data-role-badge', slug)
    }
    expect(within(listbox).getAllByRole('option')).toHaveLength(ROLE_LABELS.length)
  })

  it('searches as you type: "dev" leaves Developer', async () => {
    // Oracle: "the role badge is ONE searchable dropdown" (founder Q2:
    // type to search; docs/agents.md: 'for example "dev" finds Developer').
    await renderLookPicker()
    fireEvent.click(screen.getByRole('combobox', { name: 'Role badge' }))
    const input = await screen.findByPlaceholderText('Search...')
    fireEvent.change(input, { target: { value: 'dev' } })
    const listbox = screen.getByRole('listbox')
    const remaining = within(listbox).getAllByRole('option')
    expect(remaining).toHaveLength(1)
    expect(remaining[0]).toHaveTextContent('Developer')
  })

  it('selecting a role option calls onRoleChange with that role slug', async () => {
    const { onRoleChange } = await renderLookPicker()
    fireEvent.click(screen.getByRole('combobox', { name: 'Role badge' }))
    fireEvent.click(await screen.findByRole('option', { name: 'Security' }))
    expect(onRoleChange).toHaveBeenCalledTimes(1)
    expect(onRoleChange).toHaveBeenCalledWith('security')
  })

  it('offers exactly the ten palette colour dots in the published order', () => {
    // Oracle: ONE row of ten colour dots (ARCH-DECISIONS palette order).
    renderLookPicker()
    const dots = screen.getAllByTestId(/^avatar-color-/)
    expect(dots.map((dot) => dot.getAttribute('data-testid'))).toEqual([
      'avatar-color-Azure', 'avatar-color-Sky', 'avatar-color-Cyan', 'avatar-color-Indigo',
      'avatar-color-Violet', 'avatar-color-Purple', 'avatar-color-Fuchsia', 'avatar-color-Pink',
      'avatar-color-Orange', 'avatar-color-Grey',
    ])
  })

  it('locks each field independently when its built-in lock is set', () => {
    // Oracle: docs/agents.md "Built-in identity is locked" — the role badge,
    // look, and colour choices remain visible but cannot be changed, per field.
    renderLookPicker({ disabled: { figure: true, role: true, color: true } })
    expect(screen.getByRole('combobox', { name: 'Role badge' })).toBeDisabled()
    for (const figure of FIGURE_ORDER) {
      expect(screen.getByTestId(`avatar-figure-${figure}`)).toBeDisabled()
    }
    expect(screen.getByTestId('avatar-color-Azure')).toBeDisabled()
    // Choices stay visible: the preview and all ten dots are still rendered.
    expect(screen.getByTestId('avatar-look-preview')).toBeInTheDocument()
    expect(screen.getAllByTestId(/^avatar-color-/)).toHaveLength(10)
  })

  it('colour stays clickable while the figure row and role badge are locked', async () => {
    // Per-field locks: only the locked fields are disabled, never the editor.
    const { onColorChange } = await renderLookPicker({ disabled: { figure: true } })
    expect(screen.getByTestId('avatar-figure-Man')).toBeDisabled()
    expect(screen.getByRole('combobox', { name: 'Role badge' })).toBeEnabled()
    fireEvent.click(screen.getByTestId('avatar-color-Orange'))
    expect(onColorChange).toHaveBeenCalledWith('#FB923C')
  })
})