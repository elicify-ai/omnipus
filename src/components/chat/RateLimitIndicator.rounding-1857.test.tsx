// #1857 X1 (diagnosis fix D): the server sends retry_after_seconds as a float
// (for example 3591.75184). The banner must show whole seconds, never a decimal
// point. The expected format is the component's existing integer case: 90
// seconds reads "1m 30s", 120 reads "2m", 45 reads "45s".

import { afterEach, describe, it, expect, vi } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import { RateLimitIndicator } from './RateLimitIndicator'

afterEach(() => cleanup())

function renderBanner(retryAfterSeconds: number) {
  const { container } = render(
    <RateLimitIndicator
      scope="agent"
      resource="llm_call"
      policyRule="agent_max_llm_per_hour"
      retryAfterSeconds={retryAfterSeconds}
      onDismiss={vi.fn()}
    />,
  )
  return container.querySelector('[role="status"]') as HTMLElement
}

describe('#1857 RateLimitIndicator shows whole seconds', () => {
  it('control: the integer case reads minutes and seconds', () => {
    expect(renderBanner(90).textContent).toContain('1m 30s')
  })

  it('shows no decimal point when the server sends a fractional wait', () => {
    const banner = renderBanner(3591.75184)
    const text = banner.textContent ?? ''
    expect(text, `banner text must carry no decimal point, saw "${text}"`).not.toContain('.')
    // 3591.75 s is 59 minutes and 51.75 s: whole seconds, rounded either way.
    expect(text, `banner must show the wait as whole minutes and seconds, saw "${text}"`).toMatch(/59m 5[12]s/)
  })
})
