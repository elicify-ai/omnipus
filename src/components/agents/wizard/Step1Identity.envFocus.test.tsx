// Step1Identity.envFocus.test.tsx — FW-4 fix: the `ExecutorInputs` env
// overrides editor keyed each row by its live KEY text (`key={k}`), so every
// keystroke in the KEY field changed the row's React key — remounting the
// input and dropping focus mid-word — and renaming a key to collide with an
// existing one silently MERGED the two rows (no error, one row just
// vanished). Rows are now keyed by a synthetic id that never changes for the
// row's lifetime; the KEY rename only commits to the payload on blur, and
// only when it does not produce a duplicate.
//
// Driven through the full `CreateAgentWizard` (the `ExecutorInputs`
// sub-component only mounts inside a subagent_3p wizard flow), mirroring the
// render harness in `Step1Identity.prefillValidate.test.tsx`.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CreateAgentWizard } from '../CreateAgentWizard'
import { useUiStore } from '@/store/ui'
import { act } from 'react'
import { fetchCliDetect } from '@/lib/api'
import type { CliDetect } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchCliDetect: vi.fn() }
})

beforeEach(() => {
  act(() => {
    useUiStore.setState({ createAgentModalOpen: false, toasts: [] })
  })
  vi.mocked(fetchCliDetect).mockReset().mockResolvedValue({
    claude: { installed: true, path: '/usr/local/bin/claude', source: 'path' },
    codex: { installed: false, path: null, source: null },
    opencode: { installed: false, path: null, source: null },
  } as CliDetect)
})

function renderWizard() {
  return render(
    <CreateAgentWizard
      initialType="subagent_3p"
      initialCli="claude-code"
      onSubmit={vi.fn().mockResolvedValue(undefined)}
      onClose={vi.fn()}
    />,
  )
}

describe('Step1Identity / ExecutorInputs — env override rows (FW-4 focus-loss fix)', () => {
  it('typing multiple characters into a KEY field keeps DOM focus on the same node', () => {
    renderWizard()
    fireEvent.click(screen.getByTestId('wizard-env-overrides-add'))

    const keyInput = screen.getByPlaceholderText('KEY') as HTMLInputElement
    keyInput.focus()
    expect(document.activeElement).toBe(keyInput)

    for (const next of ['F', 'FO', 'FOO', 'FOOX', 'FOOXY']) {
      fireEvent.change(keyInput, { target: { value: next } })
      // The SAME DOM node must stay focused after every keystroke — the old
      // bug keyed the row by its live KEY text, remounting it on rename.
      expect(document.activeElement).toBe(keyInput)
    }
    expect(keyInput).toHaveValue('FOOXY')
  })

  it('flags a duplicate key inline on blur and does not silently merge the two rows', () => {
    renderWizard()
    fireEvent.click(screen.getByTestId('wizard-env-overrides-add'))
    fireEvent.click(screen.getByTestId('wizard-env-overrides-add'))

    const keyInputs = screen.getAllByPlaceholderText('KEY') as HTMLInputElement[]
    expect(keyInputs).toHaveLength(2)
    const valueInputs = screen.getAllByPlaceholderText('VALUE') as HTMLInputElement[]

    fireEvent.change(keyInputs[0], { target: { value: 'FOO' } })
    fireEvent.blur(keyInputs[0])
    fireEvent.change(valueInputs[0], { target: { value: 'bar' } })

    fireEvent.change(keyInputs[1], { target: { value: 'BAZ' } })
    fireEvent.blur(keyInputs[1])
    fireEvent.change(valueInputs[1], { target: { value: 'qux' } })

    // Rename row 2's key to collide with row 1's — this must NOT merge.
    fireEvent.change(keyInputs[1], { target: { value: 'FOO' } })
    fireEvent.blur(keyInputs[1])

    // Inline duplicate error shown on BOTH offending rows (row 1's original
    // FOO and row 2's just-renamed FOO), both rows still present and distinct.
    expect(screen.getAllByText(/duplicate key/i)).toHaveLength(2)
    const keyInputsAfter = screen.getAllByPlaceholderText('KEY') as HTMLInputElement[]
    expect(keyInputsAfter).toHaveLength(2)
    const valueInputsAfter = screen.getAllByPlaceholderText('VALUE') as HTMLInputElement[]
    expect(valueInputsAfter.map((i) => i.value)).toEqual(['bar', 'qux'])
  })

  it('removes only the targeted row (stable id, not the live key text)', () => {
    renderWizard()
    fireEvent.click(screen.getByTestId('wizard-env-overrides-add'))
    fireEvent.click(screen.getByTestId('wizard-env-overrides-add'))

    const keyInputs = screen.getAllByPlaceholderText('KEY') as HTMLInputElement[]
    fireEvent.change(keyInputs[0], { target: { value: 'ALPHA' } })
    fireEvent.blur(keyInputs[0])
    fireEvent.change(keyInputs[1], { target: { value: 'BETA' } })
    fireEvent.blur(keyInputs[1])

    const removeButtons = screen.getAllByRole('button', { name: /^remove/i })
    fireEvent.click(removeButtons[0])

    const remainingKeys = screen.getAllByPlaceholderText('KEY') as HTMLInputElement[]
    expect(remainingKeys).toHaveLength(1)
    expect(remainingKeys[0]).toHaveValue('BETA')
  })
})
