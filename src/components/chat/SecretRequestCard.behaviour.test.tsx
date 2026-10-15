// SecretRequestCard.behaviour.test.tsx — the secure secret card's observable
// contract (omnipus-system-agent FR-061, FR-071, FR-074, FR-075, FR-109;
// BDD-6.1, BDD-6.7, BDD-12.9; spec §6.3 row "Secret card").
// Oracle: the spec rows, not the implementation. Boundary: the REST functions
// (submitSecretRequest / getSecretRequest) are doubled; the store and the
// component are real.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@/lib/api-error'
import type { SecretRequestFrame } from '@/lib/api/generated/asyncapi-types'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useSessionStore } from '@/store/session'
import { SecretRequestThreadTail } from './SecretRequestCard'

const api = vi.hoisted(() => ({ submit: vi.fn(), get: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  submitSecretRequest: api.submit,
  getSecretRequest: api.get,
}))

const SID = 'main-omnipus'
const SENTINEL_A = 'xoxb-sentinel-AAAA-1111'
const SENTINEL_B = 'xapp-sentinel-BBBB-2222'

function frame(overrides: Partial<SecretRequestFrame> = {}): SecretRequestFrame {
  return {
    type: 'secret_request',
    request_id: 'req-1',
    session_id: SID,
    acting_agent_id: 'omnipus',
    acting_agent_name: 'Omnipus',
    purpose: 'Connect Slack for the Sales team',
    recipient: { kind: 'channel_credential', lines: ['Slack · slack-sales', 'bot token and app token'] },
    fields: [
      { key: 'bot_token', label: 'Bot token' },
      { key: 'app_token', label: 'App-level token' },
    ],
    ...overrides,
  }
}

function setup(f = frame()) {
  act(() => {
    useSecretRequestStore.getState().reset()
    useSessionStore.setState({ activeSessionId: SID })
    useSecretRequestStore.getState().upsertFromFrame(f)
  })
  return render(<SecretRequestThreadTail />)
}

describe('SecretRequestCard', () => {
  beforeEach(() => {
    api.submit.mockReset()
    api.get.mockReset()
  })

  it('shows the asking agent, purpose, destination and one labelled password field per value (BDD-6.1)', () => {
    setup()
    expect(screen.getByTestId('secret-request-asker')).toHaveTextContent('Omnipus needs a secret')
    expect(screen.getByTestId('secret-request-purpose')).toHaveTextContent('Connect Slack for the Sales team')
    expect(screen.getByTestId('secret-request-recipient')).toHaveTextContent('Slack · slack-sales')
    const bot = screen.getByLabelText(/Bot token/)
    expect(bot).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText(/App-level token/)).toHaveAttribute('type', 'password')
  })

  it('keyboard: focus starts on the first field, Enter moves on, Enter on the last submits the exact values once', async () => {
    api.submit.mockResolvedValue({
      kind: 'accepted',
      state: { request_id: 'req-1', state: 'submitted', reference_names: ['slack_bot.v1', 'slack_app.v1'], updated_at: '2026-10-14T00:00:00Z' },
    })
    setup()
    const user = userEvent.setup()
    expect(screen.getByLabelText(/Bot token/)).toHaveFocus()
    await user.keyboard(`${SENTINEL_A}{Enter}`)
    expect(api.submit).not.toHaveBeenCalled()
    expect(screen.getByLabelText(/App-level token/)).toHaveFocus()
    await user.keyboard(`${SENTINEL_B}{Enter}`)
    await waitFor(() => expect(screen.getByTestId('secret-request-result')).toHaveTextContent('Saved'))
    expect(api.submit).toHaveBeenCalledTimes(1)
    expect(api.submit).toHaveBeenCalledWith('req-1', { values: { bot_token: SENTINEL_A, app_token: SENTINEL_B } })
    expect(screen.getByTestId('secret-request-result')).toHaveTextContent('slack_bot.v1')
    // Values never land in shared state.
    expect(JSON.stringify(useSecretRequestStore.getState())).not.toContain('sentinel')
  })

  it('keystrokes inside the card never reach window listeners (the composer)', async () => {
    setup()
    const seen = vi.fn()
    window.addEventListener('keydown', seen)
    await userEvent.setup().keyboard('abc')
    window.removeEventListener('keydown', seen)
    expect(seen).not.toHaveBeenCalled()
  })

  it('a value under 4 bytes is refused on its field and nothing is sent', async () => {
    setup()
    const user = userEvent.setup()
    await user.keyboard('abc')
    await user.click(screen.getByTestId('secret-request-submit'))
    expect(api.submit).not.toHaveBeenCalled()
    expect(screen.getAllByRole('alert')[0]).toHaveTextContent(/at least 4/)
  })

  it('a lost answer becomes "submission unknown", reads the true state and never re-sends the values (BDD-6.7)', async () => {
    api.submit.mockRejectedValue(new ApiError(0))
    api.get.mockResolvedValue({ request_id: 'req-1', state: 'submitted', reference_names: ['slack_bot.v1'], updated_at: '2026-10-14T00:00:01Z' })
    setup()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/Bot token/), SENTINEL_A)
    await user.type(screen.getByLabelText(/App-level token/), SENTINEL_B)
    await user.click(screen.getByTestId('secret-request-submit'))
    await waitFor(() => expect(screen.getByTestId('secret-request-result')).toHaveTextContent('Saved'))
    expect(api.get).toHaveBeenCalledWith('req-1')
    expect(api.submit).toHaveBeenCalledTimes(1)
  })

  it('a 409 shows the request\'s current state instead of an error', async () => {
    api.submit.mockResolvedValue({ kind: 'not-pending', state: { request_id: 'req-1', state: 'stopped', updated_at: '2026-10-14T00:00:02Z' } })
    setup()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/Bot token/), SENTINEL_A)
    await user.type(screen.getByLabelText(/App-level token/), SENTINEL_B)
    await user.click(screen.getByTestId('secret-request-submit'))
    await waitFor(() => expect(screen.getByTestId('secret-request-result')).toHaveTextContent('Stopped — nothing was stored.'))
  })

  it('Cancel sends only the cancel body', async () => {
    api.submit.mockResolvedValue({ kind: 'accepted', state: { request_id: 'req-1', state: 'cancelled', updated_at: '2026-10-14T00:00:03Z' } })
    setup()
    await userEvent.setup().click(screen.getByTestId('secret-request-cancel'))
    expect(api.submit).toHaveBeenCalledWith('req-1', { cancel: true })
    await waitFor(() => expect(screen.getByTestId('secret-request-result')).toHaveTextContent('Cancelled'))
  })

  it('the private kind names the owner pair and carries promise wording A', () => {
    setup(frame({
      recipient: { kind: 'agent_private', lines: ['Sales · Mia · WEATHER_TOKEN'] },
      owner: { workspace_id: 'ws-sales', agent_id: 'mia' },
      fields: [{ key: 'value', label: 'WEATHER_TOKEN' }],
    }))
    expect(screen.getByTestId('secret-request-owner')).toHaveTextContent('Belongs to mia in ws-sales')
    expect(screen.getByTestId('secret-request-promise')).toHaveTextContent('a command you let the agent run can still reveal it')
  })

  it('renders only in the chat its root session names', () => {
    act(() => {
      useSecretRequestStore.getState().reset()
      useSessionStore.setState({ activeSessionId: 'some-other-chat' })
      useSecretRequestStore.getState().upsertFromFrame(frame({ session_id: 'helper-1', root_session_id: SID }))
    })
    const { rerender } = render(<SecretRequestThreadTail />)
    expect(screen.queryByTestId('secret-request-card')).toBeNull()
    act(() => { useSessionStore.setState({ activeSessionId: SID }) })
    rerender(<SecretRequestThreadTail />)
    expect(screen.getByTestId('secret-request-card')).toBeInTheDocument()
  })
})
