// useFileUpload — drag/drop + paste + attach-picker file handling for the
// chat composer, including the harmful-file-extension double-confirm flow.
// Extracted out of OmnipusComposer's ~862-line body (Wave 3 structural
// refactor) — behavior is unchanged, only ownership moved.
//
// Deliberately does NOT gate on isStreaming/inputEnabled/isReplaying — the
// original composer never gated file drop/paste on those either (only the
// native `ComposerPrimitive.AddAttachment` button's `disabled` prop does,
// and that stays owned by the composer itself since it's a plain JSX
// expression over state the composer already has). Preserving that means
// this hook has no dependency on composer/turn state at all — it only needs
// the AssistantUI composer runtime to actually attach files.

import { useCallback, useState } from 'react'
import type { ComposerRuntime } from '@assistant-ui/react'
import { useUiStore } from '@/store/ui'
import { getErrorMessage } from '@/lib/api'
import { logError } from '@/lib/telemetry'

const HARMFUL_EXTENSIONS = ['.exe', '.bat', '.cmd', '.sh', '.ps1', '.dll', '.sys', '.msi', '.scr', '.com']

export interface HarmfulConfirmState {
  files: File[]
  harmfulNames: string[]
}

export interface UseFileUploadResult {
  isDragging: boolean
  onDragOver: (e: React.DragEvent) => void
  onDragLeave: () => void
  onDrop: (e: React.DragEvent) => void
  onPaste: (e: React.ClipboardEvent) => void
  /**
   * Non-null while the harmful-file double-confirm AlertDialog should be
   * open; the confirm target the dialog renders copy for.
   */
  harmfulConfirm: HarmfulConfirmState | null
  /** 1 = first warning, 2 = second ("confirm again") stage. Only meaningful while harmfulConfirm is non-null. */
  harmfulStage: 1 | 2
  /** Closes the dialog without attaching anything (Cancel / dismiss). */
  dismissHarmfulConfirm: () => void
  /** Stage 1 "Continue" — advances to the second confirmation. */
  advanceHarmfulStage: () => void
  /** Stage 2 "Upload" — commits the pending files and closes the dialog. */
  confirmHarmfulUpload: () => void
}

export function useFileUpload(composerRuntime: ComposerRuntime): UseFileUploadResult {
  const [isDragging, setIsDragging] = useState(false)
  // Harmful-file upload confirmation (replaces the two native
  // window.confirm calls). `harmfulConfirm` holds the files awaiting
  // confirmation plus the flagged names; it is the open/closed signal — the
  // dialog is closed when `harmfulConfirm` is null and open otherwise.
  // `harmfulStage` is typed 1 | 2 (never null) and only advances 1 -> 2
  // (double-confirm) before commit; it is reset to 1 each time a new
  // harmful set is queued (see handleFilesSelected).
  const [harmfulConfirm, setHarmfulConfirm] = useState<HarmfulConfirmState | null>(null)
  const [harmfulStage, setHarmfulStage] = useState<1 | 2>(1)

  // commitFiles hands each file to the native composer attachment system;
  // the AttachmentAdapter uploads on send and threads the media:// ref
  // through onNew. Used by drag-drop and image paste (directly, or via the
  // harmful-file confirm flow).
  const commitFiles = useCallback(
    (files: File[]) => {
      for (const file of files) {
        composerRuntime.addAttachment(file).catch((err: unknown) => {
          useUiStore.getState().addToast({
            message: getErrorMessage(err, `Could not attach "${file.name}"`),
            variant: 'error',
          })
          logError({
            event: 'attachmentUploadFailed',
            fileName: file.name,
            message: err instanceof Error ? err.message : String(err),
          })
        })
      }
    },
    [composerRuntime],
  )

  const handleFilesSelected = useCallback(
    (files: File[]) => {
      const harmful = files.filter((f) =>
        HARMFUL_EXTENSIONS.some((ext) => f.name.toLowerCase().endsWith(ext))
      )
      if (harmful.length > 0) {
        // Defer to the AlertDialog double-confirm flow; commit happens only
        // when the user clicks through both stages (preserves the original
        // semantics).
        setHarmfulStage(1)
        setHarmfulConfirm({ files, harmfulNames: harmful.map((f) => f.name) })
        return
      }
      commitFiles(files)
    },
    [commitFiles],
  )

  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    setIsDragging(true)
  }, [])

  const onDragLeave = useCallback(() => setIsDragging(false), [])

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault()
      setIsDragging(false)
      const droppedFiles = Array.from(e.dataTransfer.files)
      if (droppedFiles.length > 0) handleFilesSelected(droppedFiles)
    },
    [handleFilesSelected],
  )

  const onPaste = useCallback(
    (e: React.ClipboardEvent) => {
      const items = Array.from(e.clipboardData?.items ?? [])
      const imageItems = items.filter((item) => item.type.startsWith('image/'))
      const imageFiles = imageItems.map((item) => item.getAsFile()).filter(Boolean) as File[]
      if (imageFiles.length > 0) {
        e.preventDefault()
        handleFilesSelected(imageFiles)
      } else if (imageItems.length > 0) {
        // Images were in clipboard but couldn't be materialized as files
        e.preventDefault()
        useUiStore.getState().addToast({ message: 'Could not paste image — try saving it to a file first', variant: 'error' })
      }
    },
    [handleFilesSelected],
  )

  const dismissHarmfulConfirm = useCallback(() => setHarmfulConfirm(null), [])
  const advanceHarmfulStage = useCallback(() => setHarmfulStage(2), [])
  const confirmHarmfulUpload = useCallback(() => {
    if (harmfulConfirm) commitFiles(harmfulConfirm.files)
    setHarmfulConfirm(null)
  }, [harmfulConfirm, commitFiles])

  return {
    isDragging,
    onDragOver,
    onDragLeave,
    onDrop,
    onPaste,
    harmfulConfirm,
    harmfulStage,
    dismissHarmfulConfirm,
    advanceHarmfulStage,
    confirmHarmfulUpload,
  }
}
