/**
 * McpServerModal.test.tsx — Issue #336 + #356 ACs + G7/G8/G9.
 *
 * Covers:
 * 1. Network mode shows URL field; Add enabled with valid https URL.
 * 2. Internal/RFC1918 URL shows inline SSRF caution.
 * 3. Non-https/non-localhost-http scheme is rejected (Add stays disabled + error shown).
 * 4. Local-program mode shows confirm dialog on select.
 * 5. Saved stdio server shows standing badge after confirmation.
 * 6. Raw transport string is NOT shown in MCPServerPicker.
 * 7. FR-110 / US-7: modal renders as a Sheet (slide-out), not a Dialog.
 *    Focus-trap, ESC, and focus-restore are provided by Radix DialogPrimitive
 *    (the Sheet primitive) — tested at the integration level via dialog role.
 * 8. G8: edit mode pre-populates fields and submits via patchMcpServer.
 * 9. G9: new fields (headers, env_file, requires_admin_ask) included in payload.
 * 10. G7: Test button in SkillsScreen calls testMcpServer and toasts the result.
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
    patchMcpServer: vi.fn().mockResolvedValue({ id: 's1', name: 'my-sse-server', transport: 'sse', status: 'connected', tool_count: 3 }),
    testMcpServer: vi.fn().mockResolvedValue({ success: true, message: 'Connected', tool_count: 5, tools: ['a', 'b', 'c', 'd', 'e'] }),
    fetchMcpServers: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchTools: vi.fn().mockResolvedValue([]),
    deleteSkill: vi.fn().mockResolvedValue(undefined),
    deleteMcpServer: vi.fn().mockResolvedValue(undefined),
    isApiError: actual.isApiError,
  }
})

import { addMcpServer, patchMcpServer, testMcpServer } from '@/lib/api'
import { McpServerModal } from './McpServerModal'
import { MCPServerPicker } from '@/components/agents/MCPServerPicker'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderModal(open = true, props: Partial<React.ComponentProps<typeof McpServerModal>> = {}) {
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider client={makeClient()}>
      <McpServerModal open={open} onOpenChange={onOpenChange} {...props} />
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

// ── FR-110 / US-7: Sheet (slide-out) instead of Dialog ───────────────────────

describe('McpServerModal — FR-110: renders as a Sheet slide-out', () => {
  it('renders the sheet content when open', async () => {
    renderModal(true)
    await waitFor(() => {
      expect(screen.getByTestId('mcp-sheet')).toBeInTheDocument()
    })
  })

  it('sheet content contains the Add server heading', async () => {
    renderModal(true)
    await waitFor(() => {
      expect(screen.getByText('Add MCP Server')).toBeInTheDocument()
    })
  })

  it('does not render content when closed', () => {
    renderModal(false)
    // The sheet is closed — dialog role may still exist in portal but it should not be visible
    expect(screen.queryByTestId('mcp-sheet')).not.toBeInTheDocument()
  })

  it('has a dialog role (Radix DialogPrimitive backing the Sheet)', async () => {
    renderModal(true)
    await waitFor(() => {
      // Radix Dialog.Content renders role="dialog" for a11y
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })

  it('ESC key closes the sheet (Radix handles ESC natively)', async () => {
    const onOpenChange = vi.fn()
    render(
      <QueryClientProvider client={makeClient()}>
        <McpServerModal open={true} onOpenChange={onOpenChange} />
      </QueryClientProvider>
    )
    await waitFor(() => {
      expect(screen.getByTestId('mcp-sheet')).toBeInTheDocument()
    })
    const user = userEvent.setup()
    await user.keyboard('{Escape}')
    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })
})

// ── G8: Edit mode ─────────────────────────────────────────────────────────────

describe('McpServerModal — G8: edit mode', () => {
  const sseServer = {
    id: 's1',
    name: 'my-sse-server',
    transport: 'sse' as const,
    status: 'connected' as const,
    tool_count: 3,
    enabled: true,
  }

  const stdioServer = {
    id: 's2',
    name: 'my-stdio-server',
    transport: 'stdio' as const,
    status: 'disconnected' as const,
    tool_count: 0,
    enabled: true,
  }

  it('shows "Edit MCP server" title in edit mode', async () => {
    renderModal(true, { initialServer: sseServer })
    await waitFor(() => {
      expect(screen.getByText('Edit MCP server')).toBeInTheDocument()
    })
  })

  it('pre-populates name as display text in edit mode', async () => {
    renderModal(true, { initialServer: sseServer })
    await waitFor(() => {
      expect(screen.getByText('my-sse-server')).toBeInTheDocument()
    })
  })

  it('pre-selects network mode for sse server', async () => {
    renderModal(true, { initialServer: sseServer })
    await waitFor(() => {
      const networkBtn = screen.getByTestId('mode-network')
      expect(networkBtn).toHaveAttribute('aria-pressed', 'true')
    })
  })

  it('pre-selects local mode for stdio server (no confirm dialog required)', async () => {
    renderModal(true, { initialServer: stdioServer })
    await waitFor(() => {
      const localBtn = screen.getByTestId('mode-local')
      expect(localBtn).toHaveAttribute('aria-pressed', 'true')
      // Standing badge should be visible (already confirmed in edit mode)
      expect(screen.getByTestId('stdio-standing-badge')).toBeInTheDocument()
    })
  })

  it('submit in edit mode calls patchMcpServer (not addMcpServer)', async () => {
    renderModal(true, { initialServer: sseServer })
    const user = userEvent.setup()
    await waitFor(() => screen.getByTestId('network-url'))
    await user.type(screen.getByTestId('network-url'), 'https://new-endpoint.example.com/sse')
    await user.click(screen.getByTestId('submit-add'))
    await waitFor(() => {
      expect(vi.mocked(patchMcpServer)).toHaveBeenCalledWith(
        's1',
        expect.objectContaining({ url: 'https://new-endpoint.example.com/sse' })
      )
      expect(vi.mocked(addMcpServer)).not.toHaveBeenCalled()
    })
  })

  it('shows "Save changes" button text in edit mode', async () => {
    renderModal(true, { initialServer: sseServer })
    await waitFor(() => {
      expect(screen.getByTestId('submit-add')).toHaveTextContent('Save changes')
    })
  })
})

// ── G9: New fields (headers, env_file, requires_admin_ask) ───────────────────

describe('McpServerModal — G9: new fields', () => {
  it('adds headers to network-mode addMcpServer call', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'net-server')
    await user.type(screen.getByTestId('network-url'), 'https://mcp.example.com/sse')

    // Open Advanced disclosure to find header fields
    const advancedBtn = screen.getByText('Advanced')
    await user.click(advancedBtn)

    await waitFor(() => screen.getByTestId('header-key-0'))
    await user.type(screen.getByTestId('header-key-0'), 'Authorization')
    await user.type(screen.getByTestId('header-value-0'), 'Bearer sk-test')

    await user.click(screen.getByTestId('submit-add'))
    await waitFor(() => {
      expect(vi.mocked(addMcpServer)).toHaveBeenCalledWith(
        expect.objectContaining({
          headers: { Authorization: 'Bearer sk-test' },
        })
      )
    })
  })

  it('adds requires_admin_ask to network-mode addMcpServer call', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'net-server')
    await user.type(screen.getByTestId('network-url'), 'https://mcp.example.com/sse')

    const advancedBtn = screen.getByText('Advanced')
    await user.click(advancedBtn)

    await waitFor(() => screen.getByTestId('requires-admin-ask'))
    await user.type(screen.getByTestId('requires-admin-ask'), 'delete_record, drop_table')

    await user.click(screen.getByTestId('submit-add'))
    await waitFor(() => {
      expect(vi.mocked(addMcpServer)).toHaveBeenCalledWith(
        expect.objectContaining({
          requires_admin_ask: ['delete_record', 'drop_table'],
        })
      )
    })
  })

  it('adds env_file to stdio-mode addMcpServer call', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'local-server')
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => screen.getByTestId('stdio-confirm-dialog'))
    await user.click(screen.getByTestId('stdio-confirm-accept'))
    await waitFor(() => screen.getByTestId('local-command'))

    await user.type(screen.getByTestId('local-command'), 'npx my-server')
    await user.type(screen.getByTestId('env-file'), '/etc/omnipus/mcp.env')

    await user.click(screen.getByTestId('submit-add'))
    await waitFor(() => {
      expect(vi.mocked(addMcpServer)).toHaveBeenCalledWith(
        expect.objectContaining({
          env_file: '/etc/omnipus/mcp.env',
        })
      )
    })
  })

  it('adds requires_admin_ask to stdio-mode addMcpServer call', async () => {
    renderModal()
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText('my-mcp-server'), 'local-server')
    await user.click(screen.getByTestId('mode-local'))
    await waitFor(() => screen.getByTestId('stdio-confirm-dialog'))
    await user.click(screen.getByTestId('stdio-confirm-accept'))
    await waitFor(() => screen.getByTestId('local-command'))

    await user.type(screen.getByTestId('local-command'), 'npx my-server')
    // The stdio mode uses id="mcp-admin-ask-stdio" but data-testid="requires-admin-ask"
    await user.type(screen.getByTestId('requires-admin-ask'), 'dangerous_op')

    await user.click(screen.getByTestId('submit-add'))
    await waitFor(() => {
      expect(vi.mocked(addMcpServer)).toHaveBeenCalledWith(
        expect.objectContaining({
          requires_admin_ask: ['dangerous_op'],
        })
      )
    })
  })

  it('passes requires_admin_ask via patchMcpServer in edit mode', async () => {
    const sseServer = {
      id: 's1',
      name: 'my-sse-server',
      transport: 'sse' as const,
      status: 'connected' as const,
      tool_count: 3,
    }
    renderModal(true, { initialServer: sseServer })
    const user = userEvent.setup()

    const advancedBtn = screen.getByText('Advanced')
    await user.click(advancedBtn)

    await waitFor(() => screen.getByTestId('requires-admin-ask'))
    await user.type(screen.getByTestId('requires-admin-ask'), 'admin_tool')

    await user.click(screen.getByTestId('submit-add'))
    await waitFor(() => {
      expect(vi.mocked(patchMcpServer)).toHaveBeenCalledWith(
        's1',
        expect.objectContaining({ requires_admin_ask: ['admin_tool'] })
      )
    })
  })
})

// ── G7: Test button (via SkillsScreen) ───────────────────────────────────────

describe('SkillsScreen — G7: Test button calls testMcpServer and toasts result', () => {
  it('calls testMcpServer and shows success toast on success', async () => {
    vi.mocked(testMcpServer).mockResolvedValueOnce({
      success: true,
      message: 'Connected to my-server (sse); 5 tools.',
      tool_count: 5,
      tools: ['a', 'b', 'c', 'd', 'e'],
    })

    // SkillsScreen renders server list from fetchMcpServers
    const { SkillsScreen } = await import('@/components/screens/SkillsScreen')
    const { fetchMcpServers } = await import('@/lib/api')
    vi.mocked(fetchMcpServers).mockResolvedValue([
      { id: 's1', name: 'my-server', transport: 'sse', status: 'connected', tool_count: 5 },
    ])

    render(
      <QueryClientProvider client={makeClient()}>
        <SkillsScreen />
      </QueryClientProvider>
    )

    // Navigate to MCP tab
    const user = userEvent.setup()
    await user.click(screen.getByText('MCP Servers'))

    // Wait for server row to appear then click Test
    await waitFor(() => screen.getByTestId('test-mcp-s1'))
    await user.click(screen.getByTestId('test-mcp-s1'))

    await waitFor(() => {
      expect(vi.mocked(testMcpServer)).toHaveBeenCalledWith('s1')
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: expect.stringMatching(/Connected.*5 tool/i) })
      )
    })
  })

  it('calls testMcpServer and shows error toast on failure', async () => {
    vi.mocked(testMcpServer).mockResolvedValueOnce({
      success: false,
      message: 'Connection refused: dial tcp :3000',
    })

    const { SkillsScreen } = await import('@/components/screens/SkillsScreen')
    const { fetchMcpServers } = await import('@/lib/api')
    vi.mocked(fetchMcpServers).mockResolvedValue([
      { id: 's2', name: 'broken-server', transport: 'stdio', status: 'error', tool_count: 0 },
    ])

    render(
      <QueryClientProvider client={makeClient()}>
        <SkillsScreen />
      </QueryClientProvider>
    )

    const user = userEvent.setup()
    await user.click(screen.getByText('MCP Servers'))

    await waitFor(() => screen.getByTestId('test-mcp-s2'))
    await user.click(screen.getByTestId('test-mcp-s2'))

    await waitFor(() => {
      expect(vi.mocked(testMcpServer)).toHaveBeenCalledWith('s2')
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: 'Connection refused: dial tcp :3000' })
      )
    })
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

  it('(#437) pre-fills non-secret config fields from initialServer in edit mode', async () => {
    const stdioWithConfig = {
      id: 's2',
      name: 'my-stdio-server',
      transport: 'stdio' as const,
      status: 'disconnected' as const,
      tool_count: 0,
      enabled: true,
      command: 'npx',
      args: ['server-everything', '--verbose'],
      env_file: '/etc/mcp.env',
      requires_admin_ask: ['danger'],
      env_keys: ['API_KEY'],
    }
    renderModal(true, { initialServer: stdioWithConfig })
    await waitFor(() => {
      expect(screen.getByText('Edit MCP server')).toBeInTheDocument()
    })
    // Command + args + env_file + requires_admin_ask pre-filled (non-secret).
    expect(screen.getByDisplayValue('npx')).toBeInTheDocument()
    expect(screen.getByDisplayValue('server-everything, --verbose')).toBeInTheDocument()
    expect(screen.getByDisplayValue('/etc/mcp.env')).toBeInTheDocument()
    expect(screen.getByDisplayValue('danger')).toBeInTheDocument()
    // Secret env VALUE never pre-fills, but the set KEY is surfaced read-only.
    const envKeysNote = screen.getByTestId('env-keys-set')
    expect(envKeysNote).toHaveTextContent('API_KEY')
    expect(envKeysNote).toHaveTextContent(/values hidden/i)
  })

  it('(#437) surfaces header_names as "currently set" for a remote server in edit mode', async () => {
    const sseWithHeaders = {
      id: 's3',
      name: 'my-remote-server',
      transport: 'sse' as const,
      status: 'disconnected' as const,
      tool_count: 0,
      enabled: true,
      url: 'https://mcp.example.com/sse',
      header_names: ['Authorization'],
    }
    renderModal(true, { initialServer: sseWithHeaders })
    await waitFor(() => {
      expect(screen.getByText('Edit MCP server')).toBeInTheDocument()
    })
    expect(screen.getByDisplayValue('https://mcp.example.com/sse')).toBeInTheDocument()
    const headerNamesNote = screen.getByTestId('header-names-set')
    expect(headerNamesNote).toHaveTextContent('Authorization')
    expect(headerNamesNote).toHaveTextContent(/values hidden/i)
  })
})
