import { makeAssistantToolUI as makeNativeToolUI, type AssistantToolUIProps } from '@assistant-ui/react'

/** AssistantUI derives part status from the whole answer/result. Our UI-only
 * artifact preserves each recorded per-call state without changing the answer,
 * parameters, result, visibility policy, or any gateway payload.
 */
export function resolvedToolPartStatus<T extends { type: string }>(status: T, artifact: unknown) {
  if (artifact && typeof artifact === 'object' && 'omnipusToolStatus' in artifact) {
    if (artifact.omnipusToolStatus === 'running') return { type: 'running' } as const
    if (artifact.omnipusToolStatus === 'success') return { type: 'complete' } as const
    if (artifact.omnipusToolStatus === 'error') return { type: 'incomplete', reason: 'error' } as const
    if (artifact.omnipusToolStatus === 'cancelled') return { type: 'incomplete', reason: 'cancelled' } as const
  }
  return status
}

/** The call's recorded error text, carried in the same UI-only artifact as its
 * status (omnipus-runtime.ts::buildContentParts). Undefined when none was recorded.
 */
export function resolvedToolPartError(artifact: unknown): string | undefined {
  if (artifact && typeof artifact === 'object' && 'omnipusToolError' in artifact) {
    const error = artifact.omnipusToolError
    if (typeof error === 'string') return error
  }
  return undefined
}

/** Keep native registrations on one outcome adapter, including legacy aliases. */
export function makeAssistantToolUI<TArgs, TResult>(tool: AssistantToolUIProps<TArgs, TResult>) {
  const Render = tool.render
  return makeNativeToolUI<TArgs, TResult>({
    ...tool,
    render: props => <Render {...props} status={resolvedToolPartStatus(props.status, props.artifact)} />,
  })
}
