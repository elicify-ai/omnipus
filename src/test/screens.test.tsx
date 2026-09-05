import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as React from 'react'
import type { JSX } from 'react'

// jsdom doesn't implement scrollTo — mock it globally so ChatScreen useEffect doesn't throw
beforeEach(() => {
  HTMLElement.prototype.scrollTo = vi.fn()
})

// test_screen_empty_states (integration)
// Traces to: wave0-brand-design-spec.md Scenario: Each non-chat screen renders empty state (US-4 AC2, FR-009)

// Mock the API module so queries don't make real network requests
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchGatewayStatus: vi.fn().mockResolvedValue({ online: true, agent_count: 0, channel_count: 0, daily_cost: 0, version: '0.1.0' }),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchMcpServers: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
  }
})

// Mock TanStack Router — all route components use createFileRoute
vi.mock('@tanstack/react-router', () => ({
  createRootRoute: (opts: Record<string, unknown>) => ({ options: opts }),
  createFileRoute: () => (opts: Record<string, unknown>) => ({ options: opts }),
  Outlet: () => null,
  Link: ({ children, to, className }: { children: React.ReactNode; to: string; className?: string }) => (
    <a href={to} className={className}>{children}</a>
  ),
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  useParams: () => ({}),
  useSearch: () => ({}),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

// react-shiki ships a ./style.css side-effect import that Node ESM cannot load.
// Mock the whole module so the Chat screen test can import the route without error.
vi.mock('react-shiki', () => {
  return {
    ShikiHighlighter: ({ children }: { children?: React.ReactNode }) =>
      React.createElement('pre', {}, children ?? null),
    createJavaScriptRegexEngine: () => ({}),
  }
})

// react-shiki ships a CSS file that jsdom cannot handle (Unknown file extension ".css").
// Mock the entire shiki-highlighter wrapper so its transitive CSS import never fires
// when ChatScreen is loaded via @/routes/_app/index in the Chat screen describe block.
vi.mock('@/components/chat/shiki-highlighter', () => ({
  SyntaxHighlighter: () => null,
  CopyCodeHeader: () => null,
}))

// Wave C fix: ChatScreen uses AssistantUI hooks and primitives that require an
// AuiProvider runtime context. OmnipusRuntimeProvider is too heavy for unit tests
// (it opens a WebSocket connection). Mock @assistant-ui/react so all primitives
// render their children and hooks return minimal stubs. AuiIf is mocked to always
// render its children — matching the empty-state path where thread.isEmpty is true.
vi.mock('@assistant-ui/react', async () => {
  const React = await import('react')
  const passthrough = ({ children }: { children?: React.ReactNode }) =>
    React.createElement(React.Fragment, null, children ?? null)
  const passthroughFwd = React.forwardRef(
    ({ children, ...rest }: Record<string, unknown>, _ref: unknown) => {
      void rest
      return React.createElement(React.Fragment, null, children as React.ReactNode ?? null)
    }
  )
  return {
    // Primitives — render children as-is
    ThreadPrimitive: {
      Root: passthrough,
      Viewport: passthrough,
      Messages: (_: { children: (args: { message: unknown }) => React.ReactNode }) =>
        React.createElement(React.Fragment, null, null),
    },
    MessagePrimitive: {
      Root: passthrough,
      Parts: (_: { children: (args: { part: unknown }) => React.ReactNode }) =>
        React.createElement(React.Fragment, null, null),
    },
    MessagePartPrimitive: {
      InProgress: () => null,
    },
    ComposerPrimitive: {
      Root: passthrough,
      Input: passthroughFwd,
      Send: passthroughFwd,
      AddAttachment: passthroughFwd,
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: passthrough,
      Name: () => null,
      Remove: passthroughFwd,
      Thumb: () => null,
    },
    ActionBarPrimitive: {
      Root: passthrough,
      Copy: passthrough,
    },
    // AuiIf: always render children (covers the empty-state path where thread.isEmpty === true)
    AuiIf: ({ children }: { children?: React.ReactNode }) =>
      React.createElement(React.Fragment, null, children ?? null),
    // Hook stubs
    useComposerRuntime: () => ({
      send: vi.fn(),
      setText: vi.fn(),
      getText: vi.fn(),
      getState: () => ({ text: '' }),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    }),
    useMessage: () => ({
      content: [],
      role: 'assistant',
      status: { type: 'complete' },
      isCopied: false,
    }),
    makeAssistantToolUI: vi.fn(),
    useAssistantToolUI: vi.fn(),
    AssistantRuntimeProvider: passthrough,
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

describe('Agents screen — empty state', () => {
  let AgentsScreen: () => JSX.Element

  beforeAll(async () => {
    // The agents/ index route now lazy-loads its screen; import the screen module
    // directly so the test renders real content (not the Suspense fallback).
    const mod = await import('@/components/screens/AgentListScreen')
    AgentsScreen = mod.AgentListScreen as () => JSX.Element
  }, 60_000)

  it('renders "Agents" as h1 heading', async () => {
    render(<AgentsScreen />, { wrapper })
    const h1 = await screen.findByRole('heading', { level: 1 })
    expect(h1).toHaveTextContent('Agents')
  })
})

describe('Skills screen — empty state', () => {
  let SkillsScreen: () => JSX.Element

  beforeAll(async () => {
    // The skills route now lazy-loads its screen; import the screen module
    // directly so the test renders real content (not the Suspense fallback).
    const mod = await import('@/components/screens/SkillsScreen')
    SkillsScreen = mod.SkillsScreen as () => JSX.Element
  }, 60_000)

  it('renders "Skills & Tools" as h1 heading', () => {
    render(<SkillsScreen />, { wrapper })
    const heading = screen.getByRole('heading', { name: /Skills/i })
    expect(heading.textContent).toContain('Skills')
  })
})

describe('Settings screen — empty state', () => {
  let SettingsScreen: () => JSX.Element

  beforeAll(async () => {
    // The settings route now lazy-loads its screen; import the screen module
    // directly so the test renders real content (not the Suspense fallback).
    const mod = await import('@/components/screens/SettingsScreen')
    SettingsScreen = mod.SettingsScreen as () => JSX.Element
  }, 60_000)

  it('renders "Settings" as h1 heading', () => {
    render(<SettingsScreen />, { wrapper })
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Settings')
  })
})

// test_chat_empty_state (integration)
// Traces to: wave0-brand-design-spec.md Scenario: Chat empty state shows mascot (US-4 AC4, FR-010)
//
// IA reframe (Sprint 4): the chat empty state is no longer at "/" (which now
// redirects into the default workspace's Chat tab). It is rendered by
// ChatScreen — the workspace's front view. We exercise ChatScreen directly.
describe('Chat screen — empty state', () => {
  let ChatScreen: () => JSX.Element

  beforeAll(async () => {
    const mod = await import('@/components/chat/ChatScreen')
    ChatScreen = mod.ChatScreen as unknown as () => JSX.Element
  }, 60_000)

  it('renders "Welcome to omnipus.ai" heading (gold .ai split into its own span)', () => {
    render(<ChatScreen />, { wrapper })
    // M4: the heading now composes "Welcome to " with the shared <Wordmark>
    // component, which nests "omnipus" one level deeper (inside its own
    // <span>) than the old hand-rolled markup did — getByText's default
    // matcher only reads an element's OWN direct text nodes, so a plain
    // /Welcome to omnipus/ text query no longer finds a single matching
    // element even though the rendered text is unchanged. Assert on the
    // heading's full (recursive) textContent instead, plus confirm the
    // split-span brand survived the Wordmark adoption (".ai" still renders
    // in its own Forge Gold span, not flattened into a single text node).
    const heading = screen.getByRole('heading', { level: 1 })
    expect(heading.textContent).toMatch(/Welcome to omnipus\.ai/)
    expect(heading.querySelector('span')).not.toBeNull()
  })

  it('renders prompt to select an agent (no active agent in empty state)', () => {
    // Note: Spec says "Your agents are standing by" but implementation intentionally shows
    // "Select an agent in the session bar to get started." — more instructional for first-run UX.
    render(<ChatScreen />, { wrapper })
    expect(screen.getByText(/Select an agent/i)).toBeTruthy()
  })

  it('renders the brand mark image with alt text', () => {
    render(<ChatScreen />, { wrapper })
    const img = screen.queryByRole('img', { name: /omnipus\.ai mark/i })
    expect(img).not.toBeNull()
  })
})

// test_404_page (integration)
// Traces to: wave0-brand-design-spec.md Scenario: Unknown route shows branded 404 (FR-008)
describe('404 NotFound page — branded empty state', () => {
  let NotFoundPage: () => JSX.Element

  beforeAll(async () => {
    const mod = await import('@/routes/__root')
    NotFoundPage = mod.Route.options.notFoundComponent as () => JSX.Element
  }, 60_000)

  it('renders 404 heading', () => {
    render(<NotFoundPage />)
    expect(screen.getByText('404')).toBeTruthy()
  })

  it('renders "drifted into the deep" copy', () => {
    render(<NotFoundPage />)
    expect(screen.getByText(/drifted into the deep/i)).toBeTruthy()
  })

  it('renders a link back to Chat (/)', () => {
    render(<NotFoundPage />)
    const link = screen.queryByText(/Back to Chat/i)
    expect(link).not.toBeNull()
  })
})
