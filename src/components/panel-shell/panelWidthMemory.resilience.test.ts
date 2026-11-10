import { afterEach, describe, expect, it, vi } from 'vitest'
import { deletePanelWidth, prunePanelWidths, readPanelWidth } from './panelWidthMemory'

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('panel width storage failure degradation', () => {
  it('returns an unset width when localStorage read fails', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    expect(readPanelWidth('dana', 'library', { workspaceId: 'ws-1' })).toBeNull()
  })

  it('does not throw when reset cannot delete localStorage', () => {
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    expect(() => deletePanelWidth('dana', 'library', { workspaceId: 'ws-1' })).not.toThrow()
  })

  it('returns zero removals when storage enumeration fails', () => {
    vi.spyOn(Storage.prototype, 'length', 'get').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    expect(prunePanelWidths('dana', () => false)).toBe(0)
  })
})
