// AcceptanceCriteriaEditor.test.tsx — judgment-first Definition-of-Done
// criteria editor (ADR-074 D5.1, judgment-first spec US-7 / TDD test 20;
// extends ADR-049 D2/D5/FR-3/FR-015, SD-C13).
//
// Covers: the prose-first default (a criterion added with no expander open is
// `prose`, and there is NO kind selector as the lead control), the two quiet
// payload expanders ("+ Add technical check" / "+ Add action-count check")
// producing correct `check` / `behavior` payloads (explicit 0 preserved,
// empty max = absent key), the exit-code integer + range validator
// (planning-goals-spec.md "Dataset: Criteria editor validation" #2/#3), the
// mono "verifies via:" chip on cards with a payload, the absence of
// user-facing `[kind]` labels (spec §4 prohibition), the author stamp, and
// the D5 zero-criteria soft hint.
//
// Radix Select (the behavior scope control) is mocked with a plain
// always-rendered option list (mirrors ConnectorsScreen.test.tsx's
// convention) to avoid jsdom's missing `hasPointerCapture`.

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

const TEXT_FIELD = 'What must be true when this is done?'

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

describe('AcceptanceCriteriaEditor — prose-first default (US-7 S1)', () => {
  it('adds a PROSE criterion when nothing but the plain-language field is used', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'The report reads clearly')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith([
      { kind: 'prose', judgment: 'boolean', text: 'The report reads clearly', author: AUTHOR, status: 'pending' },
    ])
  })

  it('has NO kind selector as the lead control, and no payload fields until an expander opens', () => {
    renderEditor()
    // The pre-ADR-074 editor led with a Check/Prose kind dropdown — gone.
    expect(screen.queryByLabelText('Criterion kind')).not.toBeInTheDocument()
    // Payload fields stay hidden behind their quiet expanders.
    expect(screen.queryByLabelText('Command')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Expected exit code')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Tool name')).not.toBeInTheDocument()
    // The plain-language field and both expanders are the visible surface.
    expect(screen.getByLabelText(TEXT_FIELD)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '+ Add technical check' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '+ Add action-count check' })).toBeInTheDocument()
  })

  it('shows an inline error and does not call onChange when the text is empty', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('Criterion text is required')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })
})

// ADR-080 D-TYPES — the editor is the human authoring surface for
// `judgment`: a small selector lets the author pick boolean/quantitative/
// artifact explicitly; a criterion added with no interaction defaults to
// `boolean`.
describe('AcceptanceCriteriaEditor — judgment selector (ADR-080 D-TYPES)', () => {
  it('shows a Judgment selector defaulting to boolean', () => {
    renderEditor()
    expect(screen.getByLabelText('Judgment')).toBeInTheDocument()
  })

  it('an author-selected judgment (quantitative) is carried on the added criterion', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'at least 3 sources cited')
    await user.click(screen.getByRole('option', { name: /Quantitative/i }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.judgment).toBe('quantitative')
  })

  it('an author-selected judgment (artifact) is carried on the added criterion', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'a signed release artifact exists')
    await user.click(screen.getByRole('option', { name: /Artifact/i }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.judgment).toBe('artifact')
  })

  it('resets to boolean after a successful add', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'first')
    await user.click(screen.getByRole('option', { name: /Artifact/i }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect((onChange.mock.calls[0][0] as AcceptanceCriterion[])[0].judgment).toBe('artifact')

    await user.type(screen.getByLabelText(TEXT_FIELD), 'second')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    const secondCall = onChange.mock.calls[1][0] as AcceptanceCriterion[]
    expect(secondCall[secondCall.length - 1].judgment).toBe('boolean')
  })

  it('renders a judgment badge on an existing criterion card', () => {
    renderEditor({
      criteria: [{ kind: 'prose', judgment: 'quantitative', text: 'x', author: AUTHOR, status: 'pending' }],
    })
    expect(screen.getByTestId('criterion-judgment-badge')).toHaveTextContent('quantitative')
  })
})

// The judgment selector is COUPLED to the payload expander so the author can
// never build a kind/judgment pair task.InferJudgment (pkg/task/criterion.go)
// hard-rejects: kind:'check' is always judgment:'boolean', kind:'behavior' is
// always judgment:'quantitative'. The selector is locked to that value while
// either expander is open — attempting to pick a different judgment through
// it has no effect on what gets stamped.
describe('AcceptanceCriteriaEditor — judgment/kind coupling (server InferJudgment parity)', () => {
  it('a technical check always stamps judgment: boolean, even if a mismatched option was clicked first', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'tests pass')
    // Pick a mismatched judgment BEFORE opening the check expander.
    await user.click(screen.getByRole('option', { name: /Artifact/i }))
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    await user.type(screen.getByLabelText('Command'), 'go test ./...')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.kind).toBe('check')
    expect(added.judgment).toBe('boolean')
  })

  it('an action-count check always stamps judgment: quantitative, even if a mismatched option was clicked first', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'searched enough')
    // Pick a mismatched judgment BEFORE opening the behavior expander.
    await user.click(screen.getByRole('option', { name: /Artifact/i }))
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    await user.type(screen.getByLabelText('Tool name'), 'search_web')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.kind).toBe('behavior')
    expect(added.judgment).toBe('quantitative')
  })

  it('a prose criterion still honors the author-selected judgment (no coupling applies)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'a signed release artifact exists')
    await user.click(screen.getByRole('option', { name: /Artifact/i }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.kind).toBe('prose')
    expect(added.judgment).toBe('artifact')
  })

  it('shows the "checks are always boolean" hint while the check expander is open', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.click(screen.getByRole('option', { name: /Quantitative/i }))
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    expect(screen.getByText('checks are always boolean')).toBeInTheDocument()
  })

  it('the Judgment selector shows the locked value and hint while the action-count expander is open', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    expect(screen.getByText('action-count checks are always quantitative')).toBeInTheDocument()
  })

  it('closing an expander (back to prose) restores the free judgment choice', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    expect(screen.getByText('checks are always boolean')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '+ Add technical check' })) // collapse
    expect(screen.queryByText('checks are always boolean')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText(TEXT_FIELD), 'a signed artifact exists')
    await user.click(screen.getByRole('option', { name: /Artifact/i }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.judgment).toBe('artifact')
  })
})

describe('AcceptanceCriteriaEditor — technical-check expander', () => {
  it('reveals command + exit code, and produces a check payload', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'tests pass')
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    await user.type(screen.getByLabelText('Command'), 'go test ./...')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith([
      {
        kind: 'check',
        judgment: 'boolean',
        text: 'tests pass',
        check: { command: 'go test ./...', expected_exit_code: 0 },
        author: AUTHOR,
        status: 'pending',
      },
    ])
  })

  it('clicking the expander again collapses it — the next add is prose again', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    expect(screen.getByLabelText('Command')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    expect(screen.queryByLabelText('Command')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText(TEXT_FIELD), 'back to prose')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.kind).toBe('prose')
    expect(added.check).toBeUndefined()
  })

  it('opening the action-count expander closes the technical one (at most one payload)', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    expect(screen.queryByLabelText('Command')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Tool name')).toBeInTheDocument()
  })

  it('shows an inline error and does not call onChange when the command is empty', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'tests pass')
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('Command is required')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('clears the field values after a successful add and collapses the expander', async () => {
    const user = userEvent.setup()
    renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'tests pass')
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
    await user.type(screen.getByLabelText('Command'), 'go test')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(screen.getByLabelText(TEXT_FIELD)).toHaveValue('')
    expect(screen.queryByLabelText('Command')).not.toBeInTheDocument()
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
    await user.type(screen.getByLabelText(TEXT_FIELD), 'tests pass')
    await user.click(screen.getByRole('button', { name: '+ Add technical check' }))
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

describe('AcceptanceCriteriaEditor — action-count expander (ADR-052 FR-034 behavior kind)', () => {
  it('produces a behavior payload with the defaults: min 1, no max, whole-session scope', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'research actually searched the web')
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    await user.type(screen.getByLabelText('Tool name'), 'search_web')
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(onChange).toHaveBeenCalledTimes(1)
    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    // ADR-080/task.InferJudgment coupling: a behavior criterion is always
    // quantitative, regardless of what the (locked, disabled) selector shows.
    expect(added).toEqual({
      kind: 'behavior',
      judgment: 'quantitative',
      text: 'research actually searched the web',
      behavior: { tool: 'search_web', min_count: 1, scope: 'task_session' },
      author: AUTHOR,
      status: 'pending',
    })
    // Empty max = ABSENT (no upper bound), never a coerced 0.
    expect(Object.prototype.hasOwnProperty.call(added.behavior!, 'max_count')).toBe(false)
  })

  it('preserves an explicit 0/0 ("never call this tool") — 0 is not dropped or defaulted', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'never shells out')
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    await user.type(screen.getByLabelText('Tool name'), 'bash')
    fireEvent.change(screen.getByLabelText('Min count'), { target: { value: '0' } })
    fireEvent.change(screen.getByLabelText('Max count'), { target: { value: '0' } })
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.behavior).toEqual({ tool: 'bash', min_count: 0, max_count: 0, scope: 'task_session' })
  })

  it('produces a min/max range with the attempt scope', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'searches within bounds')
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    await user.type(screen.getByLabelText('Tool name'), 'search_web')
    fireEvent.change(screen.getByLabelText('Min count'), { target: { value: '3' } })
    fireEvent.change(screen.getByLabelText('Max count'), { target: { value: '5' } })
    await user.click(screen.getByRole('option', { name: 'Per attempt' }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    const [added] = onChange.mock.calls[0][0] as AcceptanceCriterion[]
    expect(added.behavior).toEqual({ tool: 'search_web', min_count: 3, max_count: 5, scope: 'attempt' })
  })

  it('rejects max < min (mirrors the wire 400)', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'bounded')
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    await user.type(screen.getByLabelText('Tool name'), 'search_web')
    fireEvent.change(screen.getByLabelText('Min count'), { target: { value: '3' } })
    fireEvent.change(screen.getByLabelText('Max count'), { target: { value: '1' } })
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))

    expect(screen.getByText('Max count must be greater than or equal to min count')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('rejects an empty tool name', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'bounded')
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('Tool name is required')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('rejects a fractional min count', async () => {
    const user = userEvent.setup()
    const { onChange } = renderEditor()
    await user.type(screen.getByLabelText(TEXT_FIELD), 'bounded')
    await user.click(screen.getByRole('button', { name: '+ Add action-count check' }))
    await user.type(screen.getByLabelText('Tool name'), 'search_web')
    fireEvent.change(screen.getByLabelText('Min count'), { target: { value: '1.5' } })
    await user.click(screen.getByRole('button', { name: /Add criterion/i }))
    expect(screen.getByText('Min count must be a non-negative integer')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('AcceptanceCriteriaEditor — cards: "verifies via:" chip, no [kind] labels', () => {
  const CARD_CRITERIA: AcceptanceCriterion[] = [
    { kind: 'prose', judgment: 'boolean', text: 'Reads clearly', author: AUTHOR, status: 'pending' },
    {
      kind: 'check',
      judgment: 'boolean',
      text: 'tests pass',
      check: { command: 'go test ./...', expected_exit_code: 0 },
      author: AUTHOR,
      status: 'pending',
    },
    {
      kind: 'behavior',
      judgment: 'quantitative',
      text: 'actually searched',
      behavior: { tool: 'search_web', min_count: 3, max_count: 5, scope: 'task_session' },
      author: AUTHOR,
      status: 'pending',
    },
  ]

  it('renders the chip for a technical check: command -> exit code', () => {
    renderEditor({ criteria: CARD_CRITERIA })
    expect(screen.getByText('go test ./... -> exit 0')).toBeInTheDocument()
  })

  it('renders the chip for an action-count check: tool xMin-Max', () => {
    renderEditor({ criteria: CARD_CRITERIA })
    expect(screen.getByText('search_web x3-5')).toBeInTheDocument()
  })

  it('renders NO chip on a plain prose criterion, and exactly one chip per technical card', () => {
    renderEditor({ criteria: CARD_CRITERIA })
    expect(screen.getAllByText('verifies via:')).toHaveLength(2)
  })

  it('shows no user-facing kind classification label on any card (spec §4)', () => {
    renderEditor({ criteria: CARD_CRITERIA })
    for (const label of ['prose', 'PROSE', 'check', 'CHECK', 'behavior', 'BEHAVIOR']) {
      expect(screen.queryByText(label)).not.toBeInTheDocument()
    }
  })
})

describe('AcceptanceCriteriaEditor — author stamp (FR-3 rule 3)', () => {
  it('shows the author identity on an existing criterion', () => {
    renderEditor({
      criteria: [
        { kind: 'prose', judgment: 'boolean', text: 'Reviewed by a human', author: { kind: 'user', id: 'daniel' }, status: 'pending' },
        {
          kind: 'check',
          judgment: 'boolean',
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
    await user.type(screen.getByLabelText(TEXT_FIELD), 'Ray added this')
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
      criteria: [{ kind: 'prose', judgment: 'boolean', text: 'x', author: AUTHOR, status: 'pending' }],
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
    const keep: AcceptanceCriterion = { kind: 'prose', judgment: 'boolean', text: 'keep me', author: AUTHOR, status: 'pending' }
    const drop: AcceptanceCriterion = { kind: 'prose', judgment: 'boolean', text: 'drop me', author: AUTHOR, status: 'pending' }
    const { onChange } = renderEditor({ criteria: [keep, drop] })

    await user.click(screen.getByRole('button', { name: 'Remove criterion drop me' }))
    expect(onChange).toHaveBeenCalledWith([keep])
  })
})
