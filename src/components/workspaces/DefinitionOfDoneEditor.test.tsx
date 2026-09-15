// DefinitionOfDoneEditor.test.tsx — the Definition-of-Done editor primitive
// (GOAL-FR-048; joint delivery plan C-48/C-82, plan test-matrix row 48
// "DoD editor renders and enforces on save").
//
// This component is a THIN WRAPPER around `AcceptanceCriteriaEditor` (C-82):
// it owns no criterion state and no editing logic of its own. These tests
// therefore cover exactly what the wrapper itself is responsible for — the
// DoD chrome (label, required asterisk, helper line) and delegation of every
// add/remove/onChange behaviour straight through to `AcceptanceCriteriaEditor`
// — rather than re-testing that editor's own internals (already covered by
// `AcceptanceCriteriaEditor.test.tsx`).
//
// Radix Select (the behavior-scope control, reachable via the "+ Add
// action-count check" expander) is mocked the same way
// `AcceptanceCriteriaEditor.test.tsx` mocks it, to avoid jsdom's missing
// `hasPointerCapture`.

import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DefinitionOfDoneEditor } from './DefinitionOfDoneEditor'
import type { AcceptanceCriterion } from '@/lib/api'

vi.mock('@/components/ui/select', () => {
  type SelectProps = { value?: string; onValueChange?: (value: string) => void; children?: React.ReactNode }
  type SelectItemProps = { value: string; children?: React.ReactNode }
  const SelectCtx = React.createContext<((v: string) => void) | undefined>(undefined)

  const Select = ({ onValueChange, children }: SelectProps) =>
    React.createElement(SelectCtx.Provider, { value: onValueChange }, children)
  const SelectTrigger = ({ children, ...rest }: { children?: React.ReactNode; [key: string]: unknown }) =>
    React.createElement('div', rest, children)
  const SelectValue = () => React.createElement('span', {})
  const SelectContent = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'listbox' }, children)
  const SelectItem = ({ value, children }: SelectItemProps) => {
    const onValueChange = React.useContext(SelectCtx)
    return React.createElement(
      'div',
      { role: 'option', 'data-value': value, onClick: () => onValueChange?.(value) },
      children,
    )
  }
  return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem }
})

const AUTHOR = { kind: 'user' as const, id: 'daniel' }
const DOD_ITEM_FIELD = 'Definition of Done item'

function renderEditor(overrides: Partial<Parameters<typeof DefinitionOfDoneEditor>[0]> = {}) {
  const onChange = vi.fn()
  const props = {
    dod: [] as AcceptanceCriterion[],
    onChange,
    currentAuthor: AUTHOR,
    ...overrides,
  }
  const utils = render(<DefinitionOfDoneEditor {...props} />)
  return { onChange, ...utils }
}

describe('DefinitionOfDoneEditor — DoD chrome (C-82)', () => {
  it('renders the "Definition of Done" label with a required asterisk matching the Title field', () => {
    renderEditor()
    expect(screen.getByText('Definition of Done')).toBeInTheDocument()
    expect(screen.getByText('*')).toBeInTheDocument()
  })

  it('renders the standing helper line below the editor', () => {
    renderEditor()
    expect(
      screen.getByText('Standing gates, judged on every attempt. Add at least one.'),
    ).toBeInTheDocument()
  })

  it('gives its text input a distinct accessible name from the Acceptance-criteria editor', () => {
    renderEditor()
    expect(screen.getByLabelText(DOD_ITEM_FIELD)).toBeInTheDocument()
    // The visible placeholder still reads the shared prompt — only the
    // accessible name differs (task-form-criteria-dod-demo.html).
    expect(screen.getByPlaceholderText('What must be true when this is done?')).toBeInTheDocument()
  })

  it('renders no `emptyHint`-style fallback sentence — the DoD rule has no soft-tier carve-out', () => {
    renderEditor()
    expect(screen.queryByText(/judged against its title and description/i)).not.toBeInTheDocument()
  })
})

describe('DefinitionOfDoneEditor — delegates add/remove to AcceptanceCriteriaEditor, no own logic (C-82)', () => {
  it('adding a DoD item via the input calls onChange with a prose criterion, unmodified by the wrapper', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(DOD_ITEM_FIELD), 'Nothing ships with a placeholder in it')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith([
      {
        kind: 'prose',
        judgment: 'boolean',
        text: 'Nothing ships with a placeholder in it',
        author: AUTHOR,
        status: 'pending',
      },
    ])
  })

  it('renders existing DoD items and removes one on click, via the shared onChange contract', async () => {
    const user = userEvent.setup()
    const keep: AcceptanceCriterion = { kind: 'prose', judgment: 'boolean', text: 'keep me', author: AUTHOR, status: 'pending' }
    const drop: AcceptanceCriterion = { kind: 'prose', judgment: 'boolean', text: 'drop me', author: AUTHOR, status: 'pending' }
    const { onChange } = renderEditor({ dod: [keep, drop] })

    expect(screen.getByText('keep me')).toBeInTheDocument()
    expect(screen.getByText('drop me')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Remove criterion drop me' }))
    expect(onChange).toHaveBeenCalledWith([keep])
  })

  it('does not add an item, and shows the inherited inline error, when the text is empty', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('Criterion text is required')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('stamps the passed-in author identity on a newly added item, not a hardcoded one', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor({ currentAuthor: { kind: 'agent', id: 'ray' } })
    await user.type(screen.getByLabelText(DOD_ITEM_FIELD), 'Ray added this gate')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.author).toEqual({ kind: 'agent', id: 'ray' })
  })
})
