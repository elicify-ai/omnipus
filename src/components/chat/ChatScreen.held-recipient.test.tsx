// R2-S3-01: the "Waiting" row for a held @-message names the ADDRESSED agent
// (the one that is stopped), not the chat's own agent.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { makeAgent } from '@/test/factories'
import { VirtualUserMessageRow } from './ChatScreen'
import type { ChatMessage } from '@/store/chat'

if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

const row = (content: string, deliveryStatus: ChatMessage['deliveryStatus']): ChatMessage =>
  ({ id: `m-${content.length}`, role: 'user', content, timestamp: '2026-10-12T10:00:00Z', status: 'done', deliveryStatus }) as ChatMessage

function renderRow(message: ChatMessage, chatAgent = 'Jim') {
  return render(
    <QueryClientProvider client={queryClient}>
      <VirtualUserMessageRow message={message} skills={[]} commandLabels={[]} agentName={chatAgent} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  queryClient.setQueryData(['agents'], [
    makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'idle' }),
    makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'active' }),
  ])
})

describe('held row names the addressed agent', () => {
  it('"@Mia ..." typed in Jim\'s chat waits for Mia, not Jim', () => {
    renderRow(row('@Mia please review X', 'held'))
    expect(screen.getByLabelText('Waiting — delivered when Mia runs again')).toBeInTheDocument()
    expect(screen.queryByLabelText(/Jim runs again/)).toBeNull()
  })

  it('an address the roster no longer matches says "the addressed agent", never the chat\'s own agent', () => {
    renderRow(row('@Zed hello', 'held'))
    expect(screen.getByLabelText('Waiting — delivered when the addressed agent runs again')).toBeInTheDocument()
    expect(screen.queryByLabelText(/Jim/)).toBeNull()
  })

  it('negative control: other ticks still use the chat\'s own agent name', () => {
    renderRow(row('@Mia please review X', 'working'))
    expect(screen.getByLabelText('Jim is working on it')).toBeInTheDocument()
  })
})
