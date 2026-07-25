// Tests for the workspace-library attachment store (ADR-051 Rev 4 / Slice H).
//
// Covers: ref construction, add/remove/clear/take drain semantics, and the
// reactive useLibraryAttachments hook (useSyncExternalStore).

import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import {
  addLibraryAttachment,
  removeLibraryAttachment,
  clearLibraryAttachments,
  takeLibraryAttachments,
  useLibraryAttachments,
  buildWorkspaceMediaRef,
  mediaRefURL,
  // NOTE: reset the module-level store between tests via clearLibraryAttachments.
} from './library-attachment'

describe('buildWorkspaceMediaRef', () => {
  it('produces the canonical workspace-scoped ref shape', () => {
    expect(buildWorkspaceMediaRef('01KY6SHW', '550e8400')).toBe(
      'media://workspace/01KY6SHW/550e8400',
    )
  })
})

describe('mediaRefURL', () => {
  // Regression coverage for the BLOCK finding: omnipus-runtime.ts used to
  // build the library-attachment preview URL from lib.mediaId alone
  // (`/api/v1/media/${lib.mediaId}`), which 404s for workspace-scoped
  // entries — HandleMedia (rest.go:9131) is a legacy-registry lookup keyed
  // by the FULL ref string and rejects any id containing "/", so a
  // workspace-scoped id can never resolve through it. mediaRefURL mirrors
  // pkg/gateway/webchat_channel.go::mediaRefURL exactly — see the Go-side
  // TestWebchatChannel_MediaRefURL and TestReplay_MediaRefURL cases, which
  // assert the same two input/output pairs. Keep the two in lockstep.
  it('routes a workspace-scoped ref through /api/v1/media/workspace/<ws>/<id>', () => {
    expect(mediaRefURL('media://workspace/ws-1/abc')).toBe('/api/v1/media/workspace/ws-1/abc')
  })

  it('routes a legacy media://<id> ref through /api/v1/media/<id>', () => {
    expect(mediaRefURL('media://uuid-1')).toBe('/api/v1/media/uuid-1')
  })

  it('round-trips a ref built by buildWorkspaceMediaRef', () => {
    const ref = buildWorkspaceMediaRef('ws-42', 'm-7')
    expect(mediaRefURL(ref)).toBe('/api/v1/media/workspace/ws-42/m-7')
  })
})

describe('library attachment store', () => {
  beforeEach(() => {
    clearLibraryAttachments()
  })

  function entry(mediaId = 'm-1', filename = 'diagram.png') {
    return {
      mediaId,
      ref: buildWorkspaceMediaRef('ws-1', mediaId),
      filename,
      contentType: 'image/png',
    }
  }

  it('add then take drains the staged attachment', () => {
    addLibraryAttachment(entry())
    const drained = takeLibraryAttachments()
    expect(drained).toHaveLength(1)
    expect(drained[0].ref).toBe('media://workspace/ws-1/m-1')
    expect(drained[0].filename).toBe('diagram.png')
    // Drain clears the store.
    expect(takeLibraryAttachments()).toHaveLength(0)
  })

  it('take is a no-op (and does not emit) when the store is empty', () => {
    expect(takeLibraryAttachments()).toEqual([])
  })

  it('remove drops a specific attachment by id, leaving others', () => {
    const idA = addLibraryAttachment(entry('m-a', 'a.png'))
    addLibraryAttachment(entry('m-b', 'b.png'))
    removeLibraryAttachment(idA)
    const remaining = takeLibraryAttachments()
    expect(remaining).toHaveLength(1)
    expect(remaining[0].filename).toBe('b.png')
  })

  it('remove is a no-op for an unknown id', () => {
    addLibraryAttachment(entry())
    removeLibraryAttachment('does-not-exist')
    expect(takeLibraryAttachments()).toHaveLength(1)
  })

  it('clear empties the store', () => {
    addLibraryAttachment(entry('a', 'a.png'))
    addLibraryAttachment(entry('b', 'b.png'))
    clearLibraryAttachments()
    expect(takeLibraryAttachments()).toHaveLength(0)
  })

  it('clear is a no-op when already empty', () => {
    expect(() => clearLibraryAttachments()).not.toThrow()
  })

  it('assigns unique client-side ids across rapid adds', () => {
    const ids = new Set<string>()
    for (let i = 0; i < 5; i++) ids.add(addLibraryAttachment(entry(`m-${i}`, `f-${i}.png`)))
    expect(ids.size).toBe(5)
  })
})

describe('useLibraryAttachments', () => {
  beforeEach(() => {
    clearLibraryAttachments()
  })

  it('reflects add/remove reactively', () => {
    const { result, rerender } = renderHook(() => useLibraryAttachments())
    expect(result.current).toHaveLength(0)

    let id = ''
    act(() => {
      id = addLibraryAttachment({
        mediaId: 'm-1',
        ref: buildWorkspaceMediaRef('ws-1', 'm-1'),
        filename: 'a.png',
        contentType: 'image/png',
      })
    })
    rerender()
    expect(result.current).toHaveLength(1)
    expect(result.current[0].filename).toBe('a.png')

    act(() => removeLibraryAttachment(id))
    rerender()
    expect(result.current).toHaveLength(0)
  })

  it('reflects take draining the store', () => {
    const { result, rerender } = renderHook(() => useLibraryAttachments())
    act(() => {
      addLibraryAttachment({
        mediaId: 'm-1',
        ref: buildWorkspaceMediaRef('ws-1', 'm-1'),
        filename: 'a.png',
        contentType: 'image/png',
      })
    })
    rerender()
    expect(result.current).toHaveLength(1)

    act(() => {
      takeLibraryAttachments()
    })
    rerender()
    expect(result.current).toHaveLength(0)
  })
})
