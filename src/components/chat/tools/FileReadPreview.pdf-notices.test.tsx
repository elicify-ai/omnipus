/**
 * Issue #1968 T26 (FR-8, S1) — a PDF read through `read_file` or
 * `library_read` persists its notices BEFORE the body (spec
 * docs/specs/read-file-native-pdf.md §5.6, FR-8). The chat card must show
 * them on a result longer than 20 lines, live and after a reload, for both
 * tools:
 *
 *   - read_file    → FileReadBlock (live: FileReadPreviewUI registration;
 *                    replay: renderHistoricalToolCall's read_file branch).
 *                    FileReadBlock shows only the first 20 lines, so this is
 *                    the case where a notice could be cut off.
 *   - library_read → no dedicated renderer: GenericToolCall (live: what
 *                    ChatScreen's FallbackToolUI renders; replay:
 *                    renderHistoricalToolCall's generic fallback).
 *
 * Fixtures mirror the persisted shapes the Go T7/T8 tests pin
 * (pkg/tools/read_file_pdf_item_buildfail_test.go::TestReadFile_PDFItem):
 * header + R1 + text; N1 + R1; N3 + R1; header + L1 + text. The notice
 * strings are the fixed §5.6 content, written out here from the spec.
 *
 * Kind G (guard): today's renderers already show these lines; the test
 * fails if a renderer ever drops or truncates a leading notice.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, screen } from '@testing-library/react'
import type { MessagePartStatus } from '@assistant-ui/react'
import type { ToolCall } from '@/lib/api'
import { useChatPreferencesStore } from '@/store/chatPreferences'

type RenderFn = (props: {
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => React.ReactNode

const captured = vi.hoisted((): Record<string, RenderFn> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as RenderFn
      }
      return config
    },
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

// Static imports after the mocks: registration runs through the mock above.
import { FileReadPreviewUI } from './FileReadPreview'
import { GenericToolCall } from './GenericToolCall'
import { renderHistoricalToolCall } from './HistoricalToolCallBlock'

// ── Spec §5.6 fixed notice content ───────────────────────────────────────────

const R1 =
  '[PDF: sent natively to models that support PDFs, within their limits, this turn only; later turns see this text]'

function header(name: string, a: number, b: number, n: number, pages: string): string {
  return `[document: ${name} | extracted text | chars ${a}-${b} of ${n} | ${pages} pages]`
}

function n1(name: string): string {
  return `[document: ${name} | no extractable text (likely scanned) | readable natively by PDF-capable models; otherwise convert pages to PNG/JPEG]`
}

function n3(name: string, reason: 'encrypted' | 'unsupported version' | 'malformed'): string {
  return `[document: ${name} | could not be read locally (${reason}) | sent as is to PDF-capable models]`
}

function l1(maxMediaSize: string): string {
  return `[PDF over the ${maxMediaSize} media size setting; not sent natively]`
}

// 30 body lines: the whole result is well over FileReadBlock's 20-line preview.
const BODY_LINES = Array.from({ length: 30 }, (_, i) => `PAGE 1 text line ${i + 1}`)
const BODY = BODY_LINES.join('\n')
const LAST_BODY_LINE = BODY_LINES[BODY_LINES.length - 1]

interface PDFCase {
  label: string
  file: string
  result: string
  notices: string[]
  /** true when the result is longer than 20 lines (the FileReadBlock cut). */
  long: boolean
}

const CASES: PDFCase[] = [
  {
    label: 'text PDF: header + R1 + text',
    file: 'report.pdf',
    result: [header('report.pdf', 0, BODY.length - 1, BODY.length, '1'), R1, BODY].join('\n'),
    notices: [header('report.pdf', 0, BODY.length - 1, BODY.length, '1'), R1],
    long: true,
  },
  {
    label: 'scanned PDF: N1 + R1',
    file: 'scan.pdf',
    result: [n1('scan.pdf'), R1].join('\n'),
    notices: [n1('scan.pdf'), R1],
    long: false,
  },
  {
    label: 'encrypted PDF: N3 + R1',
    file: 'secure.pdf',
    result: [n3('secure.pdf', 'encrypted'), R1].join('\n'),
    notices: [n3('secure.pdf', 'encrypted'), R1],
    long: false,
  },
  {
    label: 'PDF over max_media_size: header + L1 + text',
    file: 'big.pdf',
    result: [header('big.pdf', 0, BODY.length - 1, BODY.length, 'unknown'), l1('16-byte'), BODY].join('\n'),
    notices: [header('big.pdf', 0, BODY.length - 1, BODY.length, 'unknown'), l1('16-byte')],
    long: true,
  },
]

const COMPLETE: MessagePartStatus = { type: 'complete' }

beforeEach(() => {
  // Default user view: verbose chat off.
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
})

function savedCall(tool: string, path: string, result: string): ToolCall {
  return {
    id: `tc_${tool}_${path}`,
    tool,
    params: { path },
    result,
    status: 'success',
  } as ToolCall
}

function renderReplay(tc: ToolCall): void {
  render(
    <>
      {renderHistoricalToolCall({
        tc,
        callId: tc.id,
        inlineEvents: undefined,
        activeSessionId: 'sess-1968',
        verboseChatEnabled: false,
        goalRecordEmpty: false,
        liteMode: false,
      })}
    </>,
  )
}

/** Expand the row and return the visible result text. */
function expandAndReadResult(toggleTestId: string): string {
  fireEvent.click(screen.getByTestId(toggleTestId))
  const pres = Array.from(document.querySelectorAll('pre'))
  expect(pres.length, 'expanded row must show a result pane').toBeGreaterThan(0)
  return pres.map((p) => p.textContent ?? '').join('\n')
}

function expectNoticesFirst(visible: string, c: PDFCase): void {
  let previous = -1
  for (const notice of c.notices) {
    const at = visible.indexOf(notice)
    expect(at, `${c.label}: notice must be visible in full: ${notice}`).toBeGreaterThanOrEqual(0)
    expect(at, `${c.label}: notices must keep their order before the body`).toBeGreaterThan(previous)
    previous = at
  }
  if (c.long) {
    expect(visible.indexOf('PAGE 1 text line 1'), `${c.label}: body follows the notices`).toBeGreaterThan(previous)
  }
}

// ── read_file → FileReadBlock ────────────────────────────────────────────────

describe('issue #1968 T26 — read_file PDF notices in FileReadBlock', () => {
  it('registers the live read_file renderer', () => {
    expect(FileReadPreviewUI).toBeDefined()
    expect(captured['read_file']).toBeTypeOf('function')
  })

  describe.each(CASES)('$label', (c) => {
    it('live: notices visible ahead of the body within the 20-line preview', () => {
      const element = captured['read_file']({ args: { path: `/workspace/docs/${c.file}` }, result: c.result, status: COMPLETE })
      render(element as React.ReactElement)
      const visible = expandAndReadResult('file-read-toggle')
      expectNoticesFirst(visible, c)
      if (c.long) {
        // The preview cut really applied: the last body line is hidden and
        // the "more lines" hint is shown — yet every notice stayed visible.
        expect(visible).not.toContain(LAST_BODY_LINE)
        expect(visible).toMatch(/\(\d+ more lines\)/)
      }
    })

    it('replay: the reloaded read_file row shows the same notices', () => {
      renderReplay(savedCall('read_file', `/workspace/docs/${c.file}`, c.result))
      const visible = expandAndReadResult('file-read-toggle')
      expectNoticesFirst(visible, c)
      if (c.long) {
        expect(visible).not.toContain(LAST_BODY_LINE)
      }
    })
  })
})

// ── library_read → GenericToolCall ───────────────────────────────────────────

describe('issue #1968 T26 — library_read PDF notices in the generic tool row', () => {
  describe.each(CASES)('$label', (c) => {
    it('live: the generic row shows every notice ahead of the body', () => {
      // What ChatScreen's FallbackToolUI renders for a tool without its own UI.
      render(
        <GenericToolCall
          toolName="library_read"
          args={{ path: c.file }}
          result={c.result}
          status={COMPLETE}
          isError={false}
          sessionId="sess-1968"
        />,
      )
      const visible = expandAndReadResult('tool-call-toggle')
      expectNoticesFirst(visible, c)
      if (c.long) {
        // The generic row keeps the whole result (scrollable pane).
        expect(visible).toContain(LAST_BODY_LINE)
      }
    })

    it('replay: the reloaded library_read row shows the same notices', () => {
      renderReplay(savedCall('library_read', c.file, c.result))
      expect(screen.getByTestId('tool-call-badge').getAttribute('data-tool')).toBe('library_read')
      const visible = expandAndReadResult('tool-call-toggle')
      expectNoticesFirst(visible, c)
      if (c.long) {
        expect(visible).toContain(LAST_BODY_LINE)
      }
    })
  })
})
