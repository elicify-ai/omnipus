// RED — IMPORTANT: deep-link mailbox switch ignored when Mail is already
// open for the same workspace.
//
// Spec source: usePanelDeepLink.ts::isAlreadyAdopted's OWN doc comment
// (lines 82-87): "F3: the panel-TYPE check alone (`current.id === named`) is
// not enough — it is blind to a same-page hash navigation that keeps
// `panel=mail` ... but changes `$workspaceId`. Without also comparing the
// workspace, re-requesting the SAME panel id for a DIFFERENT workspace is
// wrongly treated as 'already adopted'". F3 fixed exactly that one axis
// (the workspace comparison, `activePanelWorkspaceId(activePanel) ===
// workspaceId`, line 95). This test is the DIFFERENT, narrower gap the F3
// note does not claim to cover: the SAME workspace, a DIFFERENT mailbox —
// `isAlreadyAdopted` (lines 88-96) compares only `activePanel.id` and the
// workspace, never the mailbox/`agent` URL param that `adoptionContext`
// (lines 105-111) reads to build Mail's context. So when Mail is open for
// workspace X showing mailbox A, and the URL changes to
// `?panel=mail&agent=<mailbox B>` (same workspace, same panel, different
// mailbox), `isAlreadyAdopted` returns `true` and the adoption effect
// (usePanelDeepLink's main useEffect, line 169's early `return`) never
// calls `openPanel` with the new mailboxId — the panel keeps showing
// mailbox A despite the URL naming mailbox B.
//
// Oracle: derived directly from the SP-23 mailbox-directive contract
// documented on `adoptionContext` (lines 98-104: "the URL's `agent` param
// is the mailbox directive") — a URL naming a different mailbox for the
// SAME already-open Mail panel must adopt that mailbox, not silently keep
// showing the old one. Verified false by reading isAlreadyAdopted's current
// code (lines 88-96) myself, per the brief's explicit instruction not to
// assume F3 already covers this — it doesn't; F3's own axis is workspace,
// not mailbox.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act } from '@testing-library/react'

// ── Mocks ────────────────────────────────────────────────────────────────

let mockSearch: Record<string, unknown> = {}
const mockRouter = { get state() { return { location: { pathname: '/workspaces/ws-1/chat' } } } }
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => mockRouter,
  useNavigate: () => mockNavigate,
  useRouterState: <T,>({ select }: { select: (s: { location: { search: Record<string, unknown> } }) => T }) =>
    select({ location: { search: mockSearch } }),
  // MAJ-205's library-only blocker — irrelevant to this Mail-mailbox test,
  // no-op so the hook doesn't need a real router blocker context.
  useBlocker: () => undefined,
}))

let mockActivePanel: { id: string; context: Record<string, unknown> } | null = null
const mockOpenPanel = vi.fn()
const mockClosePanel = vi.fn()
vi.mock('@/store/ui', () => {
  const getUiState = () => ({
    activePanel: mockActivePanel,
    openPanel: mockOpenPanel,
    closePanel: mockClosePanel,
  })
  function useUiStore<T>(selector?: (s: ReturnType<typeof getUiState>) => T) {
    const state = getUiState()
    return selector ? selector(state) : state
  }
  useUiStore.getState = getUiState
  return { useUiStore }
})

// The gate always proceeds immediately — this test is about adoption
// matching, not about the leave-guard round trip (covered elsewhere).
vi.mock('@/components/panel-shell/leaveGate', () => ({
  leaveGateThen: (_outgoing: unknown, go: () => void) => go(),
}))

vi.mock('@/components/library/preview/unsavedGuard', () => ({
  confirmDiscardLibraryEdits: vi.fn(async () => true),
  isLibraryEditorDirty: () => false,
}))

// ── Hook under test ──────────────────────────────────────────────────────
import { usePanelDeepLink } from './usePanelDeepLink'

function Harness({ workspaceId, panel }: { workspaceId: string; panel: string | undefined }) {
  usePanelDeepLink(workspaceId, panel)
  return null
}

describe('usePanelDeepLink — same-workspace mailbox switch while Mail is already open', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockSearch = {}
    mockActivePanel = null
  })

  it('adopts mailbox B when the URL changes from ?panel=mail&agent=A to ?panel=mail&agent=B for the SAME workspace', async () => {
    // Given: Mail is open for workspace X, showing mailbox A.
    mockActivePanel = {
      id: 'mail',
      context: { workspaceId: 'ws-1', mailboxId: 'mailbox-a', folder: null, messageRef: null },
    }
    mockSearch = { panel: 'mail', agent: 'mailbox-a' }

    const { rerender } = render(<Harness workspaceId="ws-1" panel="mail" />)
    await act(async () => { /* flush mount effects */ })

    // isAlreadyAdopted was already true on mount (same id, same workspace) —
    // openPanel must not have fired for the unchanged mailbox-A URL.
    expect(mockOpenPanel).not.toHaveBeenCalled()

    // When: the URL changes to name mailbox B, same workspace, same panel —
    // a fresh object reference so the hook's `[rawSearch, ...]` effect dep
    // sees a change (mirrors a real router search-object replace).
    mockSearch = { panel: 'mail', agent: 'mailbox-b' }
    await act(async () => {
      rerender(<Harness workspaceId="ws-1" panel="mail" />)
    })

    // Then: the panel must adopt mailbox B — openPanel('mail', {...
    // mailboxId: 'mailbox-b' }) — not silently keep showing mailbox A.
    expect(mockOpenPanel).toHaveBeenCalledWith(
      'mail',
      expect.objectContaining({ workspaceId: 'ws-1', mailboxId: 'mailbox-b' }),
    )
  })
})
