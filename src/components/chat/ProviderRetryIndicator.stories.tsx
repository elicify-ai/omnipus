import type { Meta, StoryObj } from '@storybook/react-vite'
import { ProviderRetryIndicator } from './ProviderRetryIndicator'

// The RetryingNow story is the component's canonical (clock-stable) render:
// its retry moment sits an hour in the past, so the line shows the terminal
// "Retrying now…" state and re-renders byte-identically every second — the
// waiting state's mm:ss ticks, which would make any pixel capture
// non-deterministic.
const PAST = new Date(Date.now() - 1000 * 60 * 60).toISOString()
const RECEIVED_AT = new Date().toISOString()

const meta = {
  title: 'Design System/ProviderRetryIndicator',
  component: ProviderRetryIndicator,
  render: (args) => <ProviderRetryIndicator {...args} />,
  parameters: {
    designSystem: {
      forcedColorTargets: ['[data-testid="provider-retry-indicator"]'],
      forcedColors: {
        states: [
          {
            selector: '[data-testid="provider-retry-indicator"]',
            attribute: 'data-state',
            value: 'retrying-now',
          },
        ],
      },
      reflowExemptions: [],
      browserAssertions: [
        {
          selector: '[data-testid="provider-retry-indicator"]',
          attribute: 'data-state',
          value: 'retrying-now',
        },
      ],
    },
  },
} satisfies Meta<typeof ProviderRetryIndicator>

export default meta
type Story = StoryObj<typeof meta>

export const RetryingNow: Story = {
  args: {
    provider: 'OpenRouter',
    model: 'model-b',
    retryAt: PAST,
    sentAt: PAST,
    receivedAt: RECEIVED_AT,
    attempt: 3,
    maxAttempts: 3,
  },
}

export const Waiting: Story = {
  args: {
    provider: 'OpenRouter',
    model: 'model-b',
    retryAt: new Date(Date.now() + 120_000).toISOString(),
    sentAt: PAST,
    receivedAt: RECEIVED_AT,
    attempt: 2,
    maxAttempts: 3,
  },
}
