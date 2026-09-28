/**
 * BashOutputBlock — collapse behavior (toolui-analysis item 1, founder-approved
 * 2026-09-26): the block starts COLLAPSED; the header line alone carries
 * "bash · first ~60 chars of the command (single line) · Done/Failed"; the
 * full command + output panel live in the expandable body.
 *
 * Direct-renders the exported BashOutputBlock (unlike BashOutput.edge.test.tsx,
 * which captures the makeAssistantToolUI render fns through a mock).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BashOutputBlock, headerCommandSummary } from './BashOutput'

// Registrations at module scope call makeAssistantToolUI — stub it so the
// module import never needs an AssistantUI runtime.
vi.mock('@assistant-ui/react', () => ({
  makeAssistantToolUI: () => () => null,
}))

function renderBlock(overrides: Partial<Parameters<typeof BashOutputBlock>[0]> = {}) {
  return render(
    <BashOutputBlock
      toolName="bash"
      args={{ command: 'echo hello' }}
      result="hello\n"
      isRunning={false}
      {...overrides}
    />,
  )
}

describe('BashOutputBlock — collapsed by default', () => {
  it('renders only the one-line header; the command and output are absent until expanded', () => {
    const { container } = renderBlock()
    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // Header present…
    expect(toggle.textContent).toContain('echo hello')
    expect(toggle.textContent).toContain('Done')
    // …body absent.
    expect(screen.queryByText('hello')).not.toBeInTheDocument()
    expect(container.querySelector('[aria-label="Command"]')).toBeNull()
    expect(container.querySelectorAll('pre')).toHaveLength(0)
  })

  it('expands on click: full command and output render; aria-expanded flips', () => {
    const { container } = renderBlock({ args: { command: 'echo hello' }, result: 'hello\n' })
    const toggle = screen.getByTestId('bash-output-toggle')
    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const commandPre = container.querySelector('[aria-label="Command"]')
    expect(commandPre?.textContent).toBe('echo hello')
    expect(commandPre?.tagName).toBe('PRE')
    // Output panel — the second <pre> is the output.
    const pres = container.querySelectorAll('pre')
    expect(pres).toHaveLength(2)
    expect(pres[1].textContent).toBe('hello\n')
  })

  it('collapses again on a second click', () => {
    const { container } = renderBlock()
    const toggle = screen.getByTestId('bash-output-toggle')
    fireEvent.click(toggle)
    expect(container.querySelectorAll('pre')).toHaveLength(2)
    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(container.querySelectorAll('pre')).toHaveLength(0)
  })

  it('toggle is a native, focusable <button> (keyboard activation via the catalogued Button)', () => {
    const { container } = renderBlock()
    const toggle = container.querySelector('[data-testid="bash-output-toggle"]')
    expect(toggle?.tagName).toBe('BUTTON')
  })
})

// replay-fidelity.spec.ts test (f) locates the toggle button itself
// ([data-testid="bash-output-toggle"], which DisclosureRow forwards onto the
// rendered <button>) and reads data-tool off THAT matched element — so
// data-tool must sit on the toggle, not on the block's outer wrapper div.
describe('BashOutputBlock — data-tool placement (replay-fidelity test (f) contract)', () => {
  it('the toggle button itself carries data-tool="bash"', () => {
    renderBlock() // toolName="bash"
    expect(screen.getByTestId('bash-output-toggle')).toHaveAttribute('data-tool', 'bash')
  })

  it('data-tool forwards the actual toolName, not a hardcoded name (legacy alias case)', () => {
    renderBlock({ toolName: 'workspace_shell' })
    expect(screen.getByTestId('bash-output-toggle')).toHaveAttribute('data-tool', 'workspace_shell')
  })
})

describe('BashOutputBlock — header command summary (one line, ~60 chars)', () => {
  it('short command shows verbatim', () => {
    renderBlock({ args: { command: 'git status' } })
    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle.textContent).toContain('git status')
  })

  it('a very long command truncates to 59 chars + ellipsis in the header (full text only in body)', () => {
    const long = 'echo ' + 'x'.repeat(200)
    const { container } = renderBlock({ args: { command: long }, result: 'out' })
    const toggle = screen.getByTestId('bash-output-toggle')
    const expected = headerCommandSummary(long)
    expect(expected).toHaveLength(60)
    expect(expected.endsWith('…')).toBe(true)
    // Header carries exactly the capped summary — never the full 205-char command.
    expect(toggle.textContent).toContain(expected)
    expect(toggle.textContent).not.toContain(long)
    // Collapsed: the full command is nowhere in the DOM yet.
    expect(container.textContent).not.toContain(long)
    // Expanded: the full command renders in the body.
    fireEvent.click(toggle)
    expect(container.querySelector('[aria-label="Command"]')?.textContent).toBe(long)
  })

  it('a multi-line command renders as ONE line in the header (whitespace collapsed, no line breaks)', () => {
    const multi = 'echo one\necho two\necho three with a very long tail that pushes past the sixty character cap for sure'
    const { container } = renderBlock({ args: { command: multi }, result: '' })
    const toggle = screen.getByTestId('bash-output-toggle')
    const expected = 'echo one echo two echo three with a very long tail that pushes past the sixty character cap for sure'.slice(0, 59) + '…'
    expect(toggle.textContent).toContain(expected)
    expect(toggle.textContent).not.toContain('\n')
    // Expanded: the raw multi-line command is preserved in the body.
    fireEvent.click(toggle)
    expect(container.querySelector('[aria-label="Command"]')?.textContent).toBe(multi)
  })
})

describe('BashOutputBlock — status header while collapsed', () => {
  it('error outcome: "Failed" label + error dot, body still absent', () => {
    const { container } = renderBlock({ result: 'error: command not found', isError: true })
    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle.textContent).toContain('Failed')
    const indicator = toggle.children[0] as HTMLElement
    expect(indicator.className).toContain('bg-[var(--color-error)]')
    // Body stays collapsed.
    expect(container.querySelectorAll('pre')).toHaveLength(0)
  })

  it('cancelled outcome: muted dot + "Cancelled", body still absent', () => {
    const { container } = renderBlock({ result: null, isCancelled: true })
    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle.textContent).toContain('Cancelled')
    const indicator = toggle.children[0] as HTMLElement
    expect(indicator.className).toContain('bg-[var(--color-muted)]')
    expect(container.querySelectorAll('pre')).toHaveLength(0)
  })

  it('running outcome: "Running..." header; expanding shows the "Executing..." spinner row', () => {
    const { container } = renderBlock({ result: null, isRunning: true })
    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle.textContent).toContain('Running...')
    expect(container.querySelectorAll('pre')).toHaveLength(0)
    fireEvent.click(toggle)
    expect(screen.getByText('Executing...')).toBeInTheDocument()
  })
})

describe('headerCommandSummary — unit cases', () => {
  it('leaves a short command untouched', () => {
    expect(headerCommandSummary('ls -la')).toBe('ls -la')
  })

  it('collapses every whitespace run (newlines, tabs, repeats) to single spaces', () => {
    expect(headerCommandSummary('a\n\nb   c\td')).toBe('a b c d')
  })

  it('caps at 60 chars total: 59 + ellipsis', () => {
    expect(headerCommandSummary('x'.repeat(61))).toHaveLength(60)
    expect(headerCommandSummary('x'.repeat(61))!.endsWith('…')).toBe(true)
  })

  it('does not cap a command of exactly 60 chars', () => {
    expect(headerCommandSummary('y'.repeat(60))).toHaveLength(60)
    expect(headerCommandSummary('y'.repeat(60)).endsWith('…')).toBe(false)
  })
})
