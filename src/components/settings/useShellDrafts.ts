// useShellDrafts — one Save bar's link to the Settings shell (Settings
// redesign #1519, FR-014, FR-017). Reports this bar's unsaved-draft count
// under its own owner id (a section can hold more than one Save bar) so the
// shell's in-app leave prompt and gold-button rule see the page total, and
// registers the browser's own leave prompt while drafts exist.

import { useEffect, useId } from 'react'
import { useSettingsShell } from './SettingsShellContext'

export function useShellDrafts(count: number): void {
  const shell = useSettingsShell()
  const owner = useId()
  const { setDraftCount, setSaveBarVisible } = shell

  useEffect(() => {
    setDraftCount(owner, count)
    setSaveBarVisible(owner, count > 0)
  }, [owner, count, setDraftCount, setSaveBarVisible])

  useEffect(
    () => () => {
      setDraftCount(owner, 0)
      setSaveBarVisible(owner, false)
    },
    [owner, setDraftCount, setSaveBarVisible],
  )

  useEffect(() => {
    if (count === 0) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      // Older browsers need returnValue set to show the prompt.
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [count])
}
