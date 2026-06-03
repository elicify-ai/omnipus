/**
 * McpServerModal.test.tsx — Issue #336 ACs.
 *
 * Covers:
 * 1. Network mode shows URL field; Add enabled with valid https URL.
 * 2. Internal/RFC1918 URL shows inline SSRF caution.
 * 3. Non-https/non-localhost-http scheme is rejected (Add stays disabled + error shown).
 * 4. Local-program mode shows confirm dialog on select.
 * 5. Saved stdio server shows standing badge after confirmation.
 * 6. Raw transport string is NOT shown in MCPServerPicker.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast }),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    addMcpServer: vi.fn().mockResolvedValue({ id: 'test', name: 'test', transport: 'sse', status: 'disconnected', tool_count: 0 }),
    isApiError: actual.isApiError,
  }
})

import { addMcpServer } from '@/lib/api'
import { McpServerModal } from './McpServerModal'
import { MCPServerPicker } from '@/components/agents/MCPServerPicker'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderModal(open = true) {
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider client={makeClient()}>
      <McpServerModal open={open} onOpenChange={onOpenChange} />
    </QueryClientProvider>
  )
  return { onOpenChange }
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('McpServerModal — network mode (US-E1)', () => {
  it('shows URL field when network mode is active (default)', async () => {
    renderModal()
    expect(screen.getByTestId('network-url')).toBeInTheDocument()
  })

  it('Add button is disabled when URL is empty', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'my-server')
    expect(screen.getByTestId('submit-add')).toBeDisabled()
  })

  it('Add button is enabled with a valid https URL', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'my-server')
    await user.type(screen.getByTestId('network-url'), 'https://mcp.example.com/sse')
    expect(screen.getByTestId('submit-add')).not.toBeDisabled()
  })

  it('network submit sends url field (not command) so the backend maps it to cfg.URL', async () => {
    // Critical: the MCP manager (pkg/mcp/manager.go ConnectServer) reads cfg.URL for sse/http.
    // The modal must POST {url: "...", transport: "sse"} — NOT {command: "...", transport: "sse"}.
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'my-net-server')
    await user.type(screen.getByTestId('network-url'), 'https://mcp.example.com/sse')
    await user.click(screen.getByTestId('submit-add'))
    await waitFor(() => {
      expect(vi.mocked(addMcpServer)).toHaveBeenCalledWith(
        expect.objectContaining({
          name: 'my-net-server',
          url: 'https://mcp.example.com/sse',
          transport: 'sse',
        })
      )
      // command must NOT be present in the network payload
      expect(vi.mocked(addMcpServer)).toHaveBeenCalledWith(
        expect.not.objectContaining({ command: expect.any(String) })
      )
    })
  })

  it('shows SSRF caution for an internal RFC1918 URL', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'internal')
    await user.type(screen.getByTestId('network-url'), 'https://192.168.1.1/sse')
    await waitFor(() => {
      expect(screen.getByTestId('ssrf-caution')).toBeInTheDocument()
    })
  })

  it('shows SSRF caution for a 10.x.x.x address', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByTestId('network-url'), 'https://10.0.0.1/mcp')
    await waitFor(() => {
      expect(screen.getByTestId('ssrf-caution')).toBeInTheDocument()
    })
  })

  it('shows SSRF caution for localhost URL', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByTestId('network-url'), 'http://localhost:3000/mcp')
    await waitFor(() => {
      expect(screen.getByTestId('ssrf-caution')).toBeInTheDocument()
    })
  })

  it('shows SSRF caution for IPv6 ULA address (fc00::/7)', async () => {
    // userEvent.type cannot handle brackets in URL strings (treats them as key modifiers).
    // Use fireEvent.change for raw IPv6 bracket notation.
    renderModal()
    fireEvent.change(screen.getByTestId('network-url'), {
      target: { value: 'https://[fd12:3456::1]/mcp' },
    })
    await waitFor(() => {
      expect(screen.getByTestId('ssrf-caution')).toBeInTheDocument()
    })
  })

  it('shows SSRF caution for IPv6 link-local address (fe80::/10)', async () => {
    renderModal()
    fireEvent.change(screen.getByTestId('network-url'), {
      target: { value: 'https://[fe80::1]/mcp' },
    })
    await waitFor(() => {
      expect(screen.getByTestId('ssrf-caution')).toBeInTheDocument()
    })
  })

  it('does not show SSRF caution for a public https URL', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByTestId('network-url'), 'https://mcp.example.com/sse')
    await waitFor(() => {
      expect(screen.queryByTestId('ssrf-caution')).not.toBeInTheDocument()
    })
  })

  it('rejects a non-https non-localhost URL and keeps Add disabled', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'bad-server')
    await user.type(screen.getByTestId('network-url'), 'ftp://example.com/mcp')
    expect(screen.getByTestId('submit-add')).toBeDisabled()
    expect(screen.getByText(/Use https:\/\//)).toBeInTheDocument()
  })

  it('rejects plain http for a non-localhost host', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'plain-http')
    await user.type(screen.getByTestId('network-url'), 'http://mcp.example.com/sse')
    expect(screen.getByTestId('submit-add')).toBeDisabled()
  })
})

describe('McpServerModal — local program mode (stdio safety gate, US-E1)', () => {
  it('clicking "A local program" opens the confirm dialog', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => {
      expect(screen.getByTestId('stdio-confirm-dialog')).toBeInTheDocument()
    })
  })

  it('cancelling the confirm dialog does not activate local mode', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => screen.getByTestId('stdio-confirm-dialog'))
    await user.click(screen.getByText('Use a network address instead'))
    // URL field should still be visible (still in network mode)
    await waitFor(() => {
      expect(screen.getByTestId('network-url')).toBeInTheDocument()
    })
  })

  it('confirming the dialog shows the command field and standing badge', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => screen.getByTestId('stdio-confirm-dialog'))
    await user.click(screen.getByTestId('stdio-confirm-accept'))
    await waitFor(() => {
      expect(screen.getByTestId('local-command')).toBeInTheDocument()
      expect(screen.getByTestId('stdio-standing-badge')).toBeInTheDocument()
    })
  })

  it('standing badge text mentions running a local program', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => screen.getByTestId('stdio-confirm-dialog'))
    await user.click(screen.getByTestId('stdio-confirm-accept'))
    await waitFor(() => {
      expect(screen.getByTestId('stdio-standing-badge')).toHaveTextContent(
        /runs a local program/i
      )
    })
  })

  it('Add is disabled without a command even after confirming stdio', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'my-local')
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => screen.getByTestId('stdio-confirm-dialog'))
    await user.click(screen.getByTestId('stdio-confirm-accept'))
    await waitFor(() => screen.getByTestId('local-command'))
    expect(screen.getByTestId('submit-add')).toBeDisabled()
  })

  it('Add is enabled with a name and command', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'my-local')
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => screen.getByTestId('stdio-confirm-dialog'))
    await user.click(screen.getByTestId('stdio-confirm-accept'))
    await waitFor(() => screen.getByTestId('local-command'))
    await user.type(screen.getByTestId('local-command'), 'npx my-server')
    expect(screen.getByTestId('submit-add')).not.toBeDisabled()
  })
})

describe('MCPServerPicker — no raw transport string (US-E1)', () => {
  it('does not show the raw transport value "stdio"', () => {
    render(
      <MCPServerPicker
        servers={[
          { id: 's1', name: 'my-stdio-server', transport: 'stdio', status: 'connected', tool_count: 3 },
          { id: 's2', name: 'my-sse-server', transport: 'sse', status: 'disconnected', tool_count: 0 },
        ]}
        mcpConfig={{ servers: [] }}
        onChange={vi.fn()}
      />
    )
    // Raw transport strings must not appear
    expect(screen.queryByText('stdio')).not.toBeInTheDocument()
    expect(screen.queryByText('sse')).not.toBeInTheDocument()
    expect(screen.queryByText('http')).not.toBeInTheDocument()
    // Human-friendly labels should appear
    expect(screen.getByText(/local program/i)).toBeInTheDocument()
    expect(screen.getByText(/network/i)).toBeInTheDocument()
  })
})
