// TagInput.test.tsx — free-form tag editor (ADR-049 — milestone
// replacement). The pure `validateTag` function already has its own unit
// tests (src/lib/tagValidation.test.ts) — these cover the COMPONENT's own
// behavior: keyboard-Enter commit, the remove button, add-disabled-when-
// empty, and that validation feedback actually reaches the DOM.
//
// NOTE: the draft <input> and the "+" Add button share the exact same
// aria-label ("Add tag" is both TagInput's default `ariaLabel` prop AND the
// button's fixed aria-label) — `getByLabelText('Add tag')` is therefore
// ambiguous (matches both). Queries below disambiguate via
// `getByRole('textbox'|'button', { name: 'Add tag' })`.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TagInput } from './TagInput'

function renderInput(overrides: Partial<Parameters<typeof TagInput>[0]> = {}) {
  const onChange = vi.fn()
  const props = { tags: [] as string[], onChange, ...overrides }
  const utils = render(<TagInput {...props} />)
  return { onChange, ...utils }
}

function draftInput() {
  return screen.getByRole('textbox', { name: 'Add tag' })
}

describe('TagInput — keyboard Enter commit', () => {
  it('pressing Enter with text in the input adds a tag', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput()
    await user.type(draftInput(), 'release{Enter}')
    expect(onChange).toHaveBeenCalledWith(['release'])
  })

  it('Enter normalises case + whitespace silently (no error) before committing', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput()
    await user.type(draftInput(), '  Q3 Release  {Enter}')
    expect(onChange).toHaveBeenCalledWith(['q3 release'])
  })

  it('clears the draft input after a successful Enter-commit', async () => {
    const user = userEvent.setup()
    renderInput()
    await user.type(draftInput(), 'release{Enter}')
    expect(draftInput()).toHaveValue('')
  })

  it('Enter on an empty input is a silent no-op (no chip, no error)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput()
    await user.type(draftInput(), '{Enter}')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('Enter does not re-add a tag that is already present (no-op, per validateTag)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput({ tags: ['release'] })
    await user.type(draftInput(), 'release{Enter}')
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('TagInput — Add button', () => {
  it('the Add button is disabled while the draft input is empty', () => {
    renderInput()
    expect(screen.getByRole('button', { name: 'Add tag' })).toBeDisabled()
  })

  it('the Add button enables once text is typed, and clicking it commits the tag', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput()
    await user.type(draftInput(), 'urgent')
    const addButton = screen.getByRole('button', { name: 'Add tag' })
    expect(addButton).not.toBeDisabled()
    await user.click(addButton)
    expect(onChange).toHaveBeenCalledWith(['urgent'])
  })

  it('the Add button disables again after commit clears the draft', async () => {
    const user = userEvent.setup()
    renderInput()
    await user.type(draftInput(), 'urgent{Enter}')
    expect(screen.getByRole('button', { name: 'Add tag' })).toBeDisabled()
  })

  it('whitespace-only input keeps the Add button disabled', async () => {
    const user = userEvent.setup()
    renderInput()
    await user.type(draftInput(), '   ')
    expect(screen.getByRole('button', { name: 'Add tag' })).toBeDisabled()
  })
})

describe('TagInput — remove button', () => {
  it('renders a remove button per tag with the exact aria-label convention', () => {
    renderInput({ tags: ['release', 'urgent'] })
    expect(screen.getByRole('button', { name: 'Remove tag release' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Remove tag urgent' })).toBeInTheDocument()
  })

  it('clicking a tag\'s remove button calls onChange with that tag filtered out', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput({ tags: ['release', 'urgent'] })
    await user.click(screen.getByRole('button', { name: 'Remove tag release' }))
    expect(onChange).toHaveBeenCalledWith(['urgent'])
  })

  it('renders no tag chips (and no remove buttons) when the tag list is empty', () => {
    renderInput({ tags: [] })
    expect(screen.queryByRole('button', { name: /Remove tag/ })).not.toBeInTheDocument()
  })
})

describe('TagInput — validation feedback reaches the DOM', () => {
  it('shows "Max 64 characters" inline when a tag exceeds the length cap', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput()
    const tooLong = 'a'.repeat(65)
    await user.type(draftInput(), `${tooLong}{Enter}`)
    expect(screen.getByText('Max 64 characters')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('accepts a tag exactly at the 64-character cap with no error', async () => {
    const user = userEvent.setup()
    const { onChange } = renderInput()
    const atCap = 'a'.repeat(64)
    await user.type(draftInput(), `${atCap}{Enter}`)
    expect(screen.queryByText('Max 64 characters')).not.toBeInTheDocument()
    expect(onChange).toHaveBeenCalledWith([atCap])
  })

  it('shows "Max 16 tags per task" inline when adding a 17th distinct tag', async () => {
    const user = userEvent.setup()
    const sixteen = Array.from({ length: 16 }, (_, i) => `tag${i}`)
    const { onChange } = renderInput({ tags: sixteen })
    await user.type(draftInput(), 'seventeenth{Enter}')
    expect(screen.getByText('Max 16 tags per task')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('the error clears once the user edits the draft input again', async () => {
    const user = userEvent.setup()
    renderInput()
    const tooLong = 'a'.repeat(65)
    await user.type(draftInput(), `${tooLong}{Enter}`)
    expect(screen.getByText('Max 64 characters')).toBeInTheDocument()

    await user.type(draftInput(), 'x')
    expect(screen.queryByText('Max 64 characters')).not.toBeInTheDocument()
  })
})

describe('TagInput — ariaLabel / placeholder overrides', () => {
  it('supports a custom ariaLabel + placeholder for reuse in different contexts', () => {
    renderInput({ ariaLabel: 'Add label', placeholder: 'Add a label…' })
    expect(screen.getByRole('textbox', { name: 'Add label' })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Add a label…')).toBeInTheDocument()
  })
})
