/*
 * U7-3 / FR-070: in the agent Edit sheet footer at 390 coarse, the "Delete
 * agent" button sits beside the long autosave note. The tester's frame
 * (u7/finding-agent-coarse-clipped-model-final.png) shows the button box
 * 81 px wide (x 277 to 357) with its label cut at the right edge: the flex
 * row shrank the button below its label. The button must never shrink; the
 * note beside it wraps instead. jsdom has no layout: this pins the class;
 * the physical width stays a browser check.
 */
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DeleteAgentControl } from './DeleteAgentControl'

describe('U7-3 Delete agent keeps its full label in the sheet footer', () => {
  it('the button never shrinks below its label', () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <DeleteAgentControl agentId="a1" agentName="Retro Agent tester" revision="r1" />
      </QueryClientProvider>,
    )
    const button = screen.getByTestId('delete-agent-button')
    expect(button).toHaveTextContent('Delete agent')
    expect(button.className.split(/\s+/)).toContain('shrink-0')
  })
})
