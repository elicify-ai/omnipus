// Independent oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// Decision #1105 and "Silent-failure and code review of PR #1116 — rulings"
// (SF-2, SF-3, I1), including "SF-2 ... is fixed on the SERVER", plus the
// dispatch's no-key-change visibility control. The revised SF-2 frontend
// oracle is HTTP 409 guidance, NOT the original held-final-GET scenario.
// Real section, hook, API adapter, generated validators and query client;
// only the HTTP edge is replaced. Replies are held, never timed with sleeps.
// Scope: credential/result ownership and gateway guidance, not the already
// covered 30-second boundary or other diagnostic outcomes. GREEN and mutation
// proof are deferred to an independent CHECK instance.

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import type { SearchProviderCheckResponse } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import {
  heldReply,
  jsonReply,
  searchSettingsHarness,
} from '../../../tests/helpers/search-settings-fixture'

type Harness = ReturnType<typeof searchSettingsHarness> // not-wire-format: test harness
const mountedHarnesses: Harness[] = []
const cataloguePath = '/api/v1/integrations/providers'
const successText = 'Connection works.'
const rejectedKeyText = 'The service rejected your key. Edit it and try again.'
const genericServerText = 'The server is unavailable. Please try again in a moment.'
const pendingText = 'Checking connection…'

async function mountServices(h = searchSettingsHarness()) {
  mountedHarnesses.push(h)
  h.render()
  return {
    h,
    tavily: await screen.findByTestId('search-row-tavily'),
    exa: screen.getByTestId('search-row-exa'),
  }
}

function checkReply(
  id: SearchProviderCheckResponse['provider_id'],
  status: SearchProviderCheckResponse['status'] = 'success',
) {
  return jsonReply({
    provider_id: id,
    status,
    // Fixed valid completion time; this test makes no clock/date assertion.
    checked_at: '2026-10-01T12:00:00Z',
  })
}

function startCheck(row: HTMLElement) {
  fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
  expect(within(row).getByRole('status').textContent).toBe(pendingText)
}

async function waitForFinishedCheck(h: Harness, row: HTMLElement, catalogueRequests: number) {
  // Await actual completion, not merely receipt of the POST or start of a GET.
  await waitFor(() => {
    expect(h.calls('GET', cataloguePath)).toHaveLength(catalogueRequests)
    expect(h.client.getQueryState(['integrations'])?.fetchStatus).toBe('idle')
    expect(within(row).queryByText(pendingText)).not.toBeInTheDocument()
  })
}

afterEach(() => {
  cleanup()
  for (const h of mountedHarnesses.splice(0)) h.client.clear()
  vi.unstubAllGlobals()
  useUiStore.setState({ toasts: [] })
  document.cookie = 'csrf=; Max-Age=0; Path=/'
})

describe('PR #1116 review regressions through the real Integrations section', () => {
  it('SF-2: shows the server key-changed message from HTTP 409 and never shows a successful old-key result', async () => {
    const { h, tavily } = await mountServices()
    const reply = heldReply()
    const changedKeyText = 'The key changed while checking. Check again.'
    h.checkReply = () => reply.promise

    startCheck(tavily)
    expect(within(tavily).queryByText(successText)).not.toBeInTheDocument()
    // The server, not an invented catalogue field, invalidates the captured
    // credential snapshot. Its fixed non-secret error crosses the real adapter.
    await act(async () => { reply.resolve(jsonReply({ error: changedKeyText }, 409)) })
    await waitForFinishedCheck(h, tavily, 2)

    expect(within(tavily).getByRole('alert').textContent).toBe(changedKeyText)
    expect(within(tavily).queryByText(successText)).not.toBeInTheDocument()
    expect(h.calls('POST', `${cataloguePath}/tavily/check`)).toHaveLength(1)
  })

  it.each([
    [503, 'Could not read the saved key from the credential store. Unlock or repair it, then try again.'],
    [409, 'The saved key is not available to the running service. Reload the configuration, then try again.'],
  ] as const)('SF-3: preserves the exact recovery message from HTTP %s', async (status, serverMessage) => {
    const { h, tavily } = await mountServices()
    h.checkReply = () => jsonReply({ error: serverMessage }, status)

    startCheck(tavily)
    await waitForFinishedCheck(h, tavily, 2) // Initial catalogue + final diagnostic refresh.

    expect(within(tavily).getByRole('alert').textContent).toBe(serverMessage)
    expect(within(tavily).queryByText(genericServerText)).not.toBeInTheDocument()
    expect(h.calls('POST', `${cataloguePath}/tavily/check`)).toHaveLength(1)
  })

  it('SF-3 control: shows the generic unavailable message when the server supplies no message', async () => {
    const { h, tavily } = await mountServices()
    h.checkReply = () => jsonReply({}, 503)

    startCheck(tavily)
    await waitForFinishedCheck(h, tavily, 2)

    expect(within(tavily).getByRole('alert').textContent).toBe(genericServerText)
    expect(within(tavily).queryByText(successText)).not.toBeInTheDocument()
    expect(h.calls('POST', `${cataloguePath}/tavily/check`)).toHaveLength(1)
  })

  it('I1: keeps both overlapping service outcomes visible after each check refreshes the catalogue', async () => {
    const { h, tavily, exa } = await mountServices()
    const tavilyReply = heldReply()
    const exaReply = heldReply()
    h.checkReply = (id) => {
      if (id === 'tavily') return tavilyReply.promise
      if (id === 'exa') return exaReply.promise
      throw new Error(`Unexpected diagnostic service: ${id}`)
    }

    // Both diagnostics start at the same real cached catalogue state.
    startCheck(tavily)
    startCheck(exa)
    expect(h.calls('POST', `${cataloguePath}/tavily/check`)).toHaveLength(1)
    expect(h.calls('POST', `${cataloguePath}/exa/check`)).toHaveLength(1)

    await act(async () => { tavilyReply.resolve(checkReply('tavily')) })
    await waitForFinishedCheck(h, tavily, 2)
    expect(within(tavily).getByRole('status').textContent).toBe(successText)
    expect(within(exa).getByRole('status').textContent).toBe(pendingText)

    await act(async () => { exaReply.resolve(checkReply('exa', 'auth_error')) })
    await waitForFinishedCheck(h, exa, 3) // Initial GET + one GET per completed diagnostic.

    // Use distinct outcomes and row-scoped exact text so one result cannot
    // accidentally satisfy both services' expectations.
    expect(within(tavily).queryByText(successText), 'Tavily success must survive Exa completing').toBeInTheDocument()
    expect(within(exa).queryByText(rejectedKeyText), 'Exa must retain its own completed outcome').toBeInTheDocument()
    expect(within(tavily).queryByText(rejectedKeyText)).not.toBeInTheDocument()
    expect(within(exa).queryByText(successText)).not.toBeInTheDocument()
    expect(h.calls('POST', `${cataloguePath}/tavily/check`)).toHaveLength(1)
    expect(h.calls('POST', `${cataloguePath}/exa/check`)).toHaveLength(1)
  })

  it('I1: replacing only Tavily\'s key clears Tavily\'s result but preserves Exa\'s result', async () => {
    const { h, tavily, exa } = await mountServices()
    h.checkReply = (id) => {
      if (id === 'tavily') return checkReply('tavily')
      if (id === 'exa') return checkReply('exa', 'auth_error')
      throw new Error(`Unexpected diagnostic service: ${id}`)
    }

    startCheck(tavily)
    await waitForFinishedCheck(h, tavily, 2)
    expect(within(tavily).getByRole('status').textContent).toBe(successText)

    startCheck(exa)
    await waitForFinishedCheck(h, exa, 3)
    // Establish Exa's result immediately before the key write. Do not let
    // the earlier overlapping-check defect masquerade as the save regression.
    expect(within(exa).getByRole('alert').textContent).toBe(rejectedKeyText)

    fireEvent.click(within(tavily).getByRole('button', { name: 'Edit key' }))
    fireEvent.change(screen.getByTestId('key-input-tavily'), { target: { value: 'tavily-review-key-b' } })
    fireEvent.click(screen.getByTestId('save-tavily'))
    fireEvent.click(screen.getByRole('button', { name: 'Update integration' }))

    await waitFor(() => {
      expect(h.calls('PUT', `${cataloguePath}/tavily`)).toHaveLength(1)
      expect(h.calls('GET', cataloguePath)).toHaveLength(4) // Save triggers the fourth GET.
      expect(h.client.getQueryState(['integrations'])?.fetchStatus).toBe('idle')
      expect(screen.queryByTestId('key-input-tavily')).not.toBeInTheDocument()
    })
    expect(JSON.parse(String(h.calls('PUT', `${cataloguePath}/tavily`)[0][1]?.body)))
      .toEqual({ kind: 'search', api_key: 'tavily-review-key-b' })
    expect(within(tavily).queryByText(successText)).not.toBeInTheDocument()
    expect(within(exa).queryByText(rejectedKeyText), 'Changing Tavily must not clear unchanged Exa').toBeInTheDocument()
    expect(h.calls('PUT', `${cataloguePath}/exa`)).toHaveLength(0)
    expect(h.calls('POST', `${cataloguePath}/tavily/check`)).toHaveLength(1)
    expect(h.calls('POST', `${cataloguePath}/exa/check`)).toHaveLength(1)
  })

  it('visibility control: keeps a finished result visible when its final catalogue refresh has no key change', async () => {
    const { h, tavily } = await mountServices()

    startCheck(tavily)
    expect(await within(tavily).findByText(successText)).toBeInTheDocument()
    await waitForFinishedCheck(h, tavily, 2)

    expect(within(tavily).getByRole('status').textContent).toBe(successText)
    expect(within(tavily).getByText('Ready')).toBeInTheDocument()
    expect(h.calls('PUT', `${cataloguePath}/tavily`)).toHaveLength(0)
    expect(h.calls('POST', `${cataloguePath}/tavily/check`)).toHaveLength(1)
  })
})
