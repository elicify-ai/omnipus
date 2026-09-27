// fixtures.ts — static fixture data for the wave-0 side-panel-shell demo
// (side-panel-shell-spec.md §9): REAL components (LibraryExplorer, the
// browser tab strip) over FIXTURE data, no gateway. Wire shapes are the
// GENERATED types (constraint #8) — the fixtures must satisfy the same
// Zod schemas the real SPA validates responses with, or the explorer
// reports a schema error. Writes are not served (see fixtureApi.ts) — a
// demo save fails visibly, which is the honest failure for a fixture.

import type {
  KnowledgeBaseInfo,
  LibraryContentResponse,
  LibraryEntry,
  LibraryWorkspaceNode,
} from '@/lib/api/generated/openapi-types'
import type { BrowserTabStripState } from '@/components/browser/browserLiveViewModel'

export const DEMO_USERNAME = 'demo-user'

export const DEMO_WORKSPACES: LibraryWorkspaceNode[] = [
  { id: 'ws-alpha', name: 'Alpha Research', entry_count: 4 },
  { id: 'ws-beta', name: 'Beta Notes', entry_count: 3 },
]

const T = '2026-09-20T10:30:00Z'

/** Full recursive entry list per workspace — the interceptor derives one-level listings from this. */
export const DEMO_LIBRARY_ENTRIES: Record<string, LibraryEntry[]> = {
  'ws-alpha': [
    { name: 'notes', path: 'notes', is_dir: true, is_hidden: false, size: 0, modified_at: T, is_knowledge_base: true, is_text_editable: false },
    { name: 'meeting.md', path: 'notes/meeting.md', is_dir: false, is_hidden: false, size: 120, modified_at: T, is_text_editable: true, mime: 'text/markdown' },
    { name: 'plan.md', path: 'notes/plan.md', is_dir: false, is_hidden: false, size: 96, modified_at: T, is_text_editable: true, mime: 'text/markdown' },
    { name: 'README.md', path: 'README.md', is_dir: false, is_hidden: false, size: 64, modified_at: T, is_text_editable: true, mime: 'text/markdown' },
  ],
  'ws-beta': [
    { name: 'drafts', path: 'drafts', is_dir: true, is_hidden: false, size: 0, modified_at: T, is_text_editable: false },
    { name: 'budget.csv', path: 'drafts/budget.csv', is_dir: false, is_hidden: false, size: 48, modified_at: T, is_text_editable: true, mime: 'text/csv' },
    { name: 'todo.md', path: 'todo.md', is_dir: false, is_hidden: false, size: 80, modified_at: T, is_text_editable: true, mime: 'text/markdown' },
  ],
}

const FILE_BODY: Record<string, string> = {
  'notes/meeting.md': '# Meeting notes\n\n- demo fixture file\n- edit is not persisted in the wave-0 demo\n',
  'notes/plan.md': '# Plan\n\nA fixture document for the side-panel shell demo.\n',
  'README.md': '# Alpha Research\n\nFixture workspace for the wave-0 demo.\n',
  'drafts/budget.csv': 'item,amount\nseeds,12\ntools,7\n',
  'todo.md': '# TODO\n\n- [x] wave 0 demo\n',
}

export function fixtureEntry(workspaceId: string, path: string): LibraryEntry | undefined {
  return DEMO_LIBRARY_ENTRIES[workspaceId]?.find((e) => e.path === path)
}

export function fixtureContent(workspaceId: string, path: string): LibraryContentResponse {
  const entry = fixtureEntry(workspaceId, path)
  if (entry === undefined) {
    return { path, size: 0, is_text: false, too_large: false }
  }
  const body = FILE_BODY[path]
  return {
    path,
    ...(body !== undefined ? { content: body } : {}),
    size: entry.size,
    is_text: body !== undefined,
    too_large: false,
    ...(entry.mime !== undefined ? { mime: entry.mime } : {}),
  }
}

/** Marker-based knowledge detection over the fixtures (notes/ is a vault). */
export function fixtureKnowledgeInfo(workspaceId: string, path: string): KnowledgeBaseInfo {
  const isVault = workspaceId === 'ws-alpha' && path === 'notes'
  return {
    workspace_id: workspaceId,
    // The work-tree root is spelled "." — same normalization the gateway
    // handler does (pkg/gateway/rest_knowledge.go): root_path has minLength 1,
    // so '' would fail the generated Zod schema the explorer validates with.
    root_path: path === '' ? '.' : path,
    is_knowledge_base: isVault,
    marker: isVault ? 'omnipus_vault' : 'none',
    ...(isVault ? { collection_id: 'col-alpha-notes', display_name: 'Alpha Notes' } : {}),
  }
}

/** Static browser tab state for the Browser placeholder (no live WebRTC). */
export const DEMO_BROWSER_TABS: BrowserTabStripState = {
  activeIndex: 0,
  tabs: [
    { index: 0, title: 'Omnipus docs', url: 'https://omnipus.ai/docs', active: true },
    { index: 1, title: 'Example', url: 'https://example.com' },
  ],
}
