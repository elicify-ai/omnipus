import { afterEach, describe, expect, it } from 'vitest'
import {
  confirmDiscardLibraryEdits,
  discardConfirmDialogHostUnmounted,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
  setLibraryEditorDirty,
} from './unsavedGuard'

afterEach(() => {
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(false)
})

describe('discard-confirmation dialog host lifecycle', () => {
  it('declines pending transitions and closes the external-store dialog when its host unmounts', async () => {
    setLibraryEditorDirty(true)
    const pending = confirmDiscardLibraryEdits()
    expect(getDiscardConfirmDialogOpen()).toBe(true)

    discardConfirmDialogHostUnmounted()

    await expect(pending).resolves.toBe(false)
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })
})
