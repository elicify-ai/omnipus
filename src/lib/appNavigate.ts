// #1428 step 2 (DEFECT 2): navigation to a workspace chat for code that is not
// a route component. Shared composer pieces (UnsentNotice's Open) must not call
// useNavigate themselves: many component tests mock the router module without
// it. The workspace chat route registers a navigate while mounted; with nothing
// registered, appNavigate does nothing and says so.

/** Opens workspace `workspaceId`'s chat route naming chat `open` (#1437). */
export type AppNavigate = (target: { workspaceId: string; open: string }) => void

let current: AppNavigate | null = null

/** Registers `navigate`; the returned function unregisters it (if still current). */
export function registerAppNavigate(navigate: AppNavigate): () => void {
  current = navigate
  return () => {
    if (current === navigate) current = null
  }
}

/** Navigates with the registered navigate; false when none is registered. */
export function appNavigate(target: { workspaceId: string; open: string }): boolean {
  if (!current) return false
  current(target)
  return true
}
