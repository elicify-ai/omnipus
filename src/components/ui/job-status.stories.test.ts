import { describe, expect, it } from 'vitest'

import meta from './job-status.stories'

describe('JobStatus story browser contract', () => {
  it('targets both caller-controlled actions without exempting component geometry', () => {
    const contract = meta.parameters.designSystem
    expect(contract.keyboard).toEqual([{
      trigger: '[data-testid="job-status"] button:first-of-type',
      key: 'Enter',
      expectFocus: '[data-testid="job-status"] button:first-of-type',
    }])
    expect(contract.pointerTargets).toEqual(['[data-testid="job-status"] button:first-of-type', '[data-testid="job-status"] button:nth-of-type(2)'])
    expect(contract.reflowExemptions).toEqual([])
  })
})
