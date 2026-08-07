// AcceptanceCriteriaEditor.test.tsx — Definition-of-Done criteria editor
// (ADR-049 D2/D5/FR-3/FR-015, SD-C13). Covers the add-check-row /
// add-prose-row flows, the exit-code integer + range validator (planning-
// goals-spec.md "Dataset: Criteria editor validation" #2/#3 — untested
// LOGIC, not just render), the author stamp, and the D5 zero-criteria soft
// hint.
//
// Radix Select is mocked with a plain always-rendered option list (mirrors
// ConnectorsScreen.test.tsx's convention) to avoid jsdom's missing
// `hasPointerCapture`.

import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AcceptanceCriteriaEditor } from './AcceptanceCriteriaEditor'
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

function renderEditor(overrides: Partial<Parameters<typeof AcceptanceCriteriaEditor>[0]> = {}) {
  const onChange = vi.fn()
  const props = {
    criteria: [] as AcceptanceCriterion[],
    onChange,
    currentAuthor: AUTHOR,
    ...overrides,
  }
  const utils = render(<AcceptanceCriteriaEditor {...props} />)
  return { onChange, ...utils }
}

describe('AcceptanceCriteriaEditor — add-check-row', () => {
  it('adds a check criterion with command + default exit code + author stamp', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText('Check description'), 'tests pass')
    await user.type(screen.getByLabelText('Command'), 'go test ./...')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith([
      {
        kind: 'check',
        text: 'tests pass',
        check: { command: 'go test ./...', expected_exit_code: 0 },
        author: AUTHOR,
        status: 'pending',
      },
    ])
  })

  it('shows an inline error and does not call onChange when the command is empty', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText('Check description'), 'tests pass')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('Command is required')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('shows an inline error when the check description is empty', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('A description of what the check verifies is required')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('clears the field values after a successful add, ready for the next criterion', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.type(screen.getByLabelText('Check description'), 'tests pass')
    await user.type(screen.getByLabelText('Command'), 'go test')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(screen.getByLabelText('Check description')).toHaveValue('')
    expect(screen.getByLabelText('Command')).toHaveValue('')
    expect(screen.getByLabelText('Expected exit code')).toHaveValue(0)
  })
})

describe('AcceptanceCriteriaEditor — add-prose-row', () => {
  it('adds a prose criterion once the kind is switched to Prose', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.click(screen.getByRole('option', { name: 'Prose' }))
    await user.type(screen.getByLabelText('Criterion text'), 'Docs updated')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledWith([
      { kind: 'prose', text: 'Docs updated', author: AUTHOR, status: 'pending' },
    ])
  })

  it('shows an inline error when prose text is empty', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.click(screen.getByRole('option', { name: 'Prose' }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('Criterion text is required')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('switching back to Check hides the prose-only field and shows command/exit-code again', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.click(screen.getByRole('option', { name: 'Prose' }))
    expect(screen.queryByLabelText('Command')).not.toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: 'Check' }))
    expect(screen.getByLabelText('Command')).toBeInTheDocument()
  })
})

// Untested LOGIC (not just render): the exit-code validator's integer +
// range checks. planning-goals-spec.md "Dataset: Criteria editor
// validation" #2 (non-integer -> inline "exit code must be an integer") and
// #3 (range, resolved here per FR-015's explicit `expected_exit_code ∈
// [0,255]` MUST). Round-1 review found `parseInt` silently truncated a
// fractional string ("3.5" -> 3) instead of rejecting it, and there was no
// range check at all — both fixed minimally in AcceptanceCriteriaEditor.tsx
// alongside this test.
describe('AcceptanceCriteriaEditor — exit-code integer/range validation', () => {
  async function fillCheckRow(user: ReturnType<typeof userEvent.setup>, exitCode: string) {
    await user.type(screen.getByLabelText('Check description'), 'tests pass')
    await user.type(screen.getByLabelText('Command'), 'go test')
    fireEvent.change(screen.getByLabelText('Expected exit code'), { target: { value: exitCode } })
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
  }

  it('rejects an empty exit code with "Exit code must be an integer"', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await fillCheckRow(user, '')
    expect(screen.getByText('Exit code must be an integer')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('rejects a fractional exit code ("3.5") — must not silently truncate to 3', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await fillCheckRow(user, '3.5')
    expect(screen.getByText('Exit code must be an integer')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('rejects an out-of-range exit code above 255 (FR-015)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await fillCheckRow(user, '256')
    expect(screen.getByText('Exit code must be between 0 and 255')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('rejects a negative exit code (FR-015)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await fillCheckRow(user, '-1')
    expect(screen.getByText('Exit code must be between 0 and 255')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('accepts the lower boundary (0)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await fillCheckRow(user, '0')
    expect(onChange).toHaveBeenCalledTimes(1)
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.check?.expected_exit_code).toBe(0)
  })

  it('accepts the upper boundary (255)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await fillCheckRow(user, '255')
    expect(onChange).toHaveBeenCalledTimes(1)
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.check?.expected_exit_code).toBe(255)
  })

  it('a validation error clears once the user edits the field again', async () => {
    const user = userEvent.setup()
    renderEditor()
    await fillCheckRow(user, '256')
    expect(screen.getByText('Exit code must be between 0 and 255')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Expected exit code'), { target: { value: '1' } })
    expect(screen.queryByText('Exit code must be between 0 and 255')).not.toBeInTheDocument()
  })
})

describe('AcceptanceCriteriaEditor — author stamp (FR-3 rule 3)', () => {
  it('shows the author identity on an existing criterion', () => {
    renderEditor({
      criteria: [
        { kind: 'prose', text: 'Reviewed by a human', author: { kind: 'user', id: 'daniel' }, status: 'pending' },
        {
          kind: 'check',
          text: 'lints clean',
          check: { command: 'golangci-lint run', expected_exit_code: 0 },
          author: { kind: 'agent', id: 'jim' },
          status: 'pending',
        },
      ],
    })
    expect(screen.getByText('by user:daniel')).toBeInTheDocument()
    expect(screen.getByText('by agent:jim')).toBeInTheDocument()
  })

  it('newly-added criteria are stamped with currentAuthor, not a hardcoded identity', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor({ currentAuthor: { kind: 'agent', id: 'ray' } })
    await user.click(screen.getByRole('option', { name: 'Prose' }))
    await user.type(screen.getByLabelText('Criterion text'), 'Ray added this')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.author).toEqual({ kind: 'agent', id: 'ray' })
  })
})

describe('AcceptanceCriteriaEditor — D5 zero-criteria soft hint', () => {
  it('shows the emptyHint when there are zero criteria', () => {
    renderEditor({ criteria: [], emptyHint: 'No criteria added — judged against title/description (D5).' })
    expect(screen.getByText('No criteria added — judged against title/description (D5).')).toBeInTheDocument()
  })

  it('does not show the hint once at least one criterion exists', () => {
    renderEditor({
      criteria: [{ kind: 'prose', text: 'x', author: AUTHOR, status: 'pending' }],
      emptyHint: 'No criteria added — judged against title/description (D5).',
    })
    expect(screen.queryByText('No criteria added — judged against title/description (D5).')).not.toBeInTheDocument()
  })

  it('renders no hint at all when emptyHint is not provided (optional prop)', () => {
    const { container } = renderEditor({ criteria: [] })
    expect(container.querySelector('ul')).not.toBeInTheDocument()
    expect(screen.queryByText(/No criteria/)).not.toBeInTheDocument()
  })
})

describe('AcceptanceCriteriaEditor — remove criterion', () => {
  it('clicking the remove button calls onChange with that criterion filtered out', async () => {
    const user = userEvent.setup()
    const keep: AcceptanceCriterion = { kind: 'prose', text: 'keep me', author: AUTHOR, status: 'pending' }
    const drop: AcceptanceCriterion = { kind: 'prose', text: 'drop me', author: AUTHOR, status: 'pending' }
    const { onChange } = renderEditor({ criteria: [keep, drop] })

    await user.click(screen.getByRole('button', { name: 'Remove criterion drop me' }))
    expect(onChange).toHaveBeenCalledWith([keep])
  })
})
