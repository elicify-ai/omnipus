import { makeAssistantToolUI as makeNativeToolUI, type AssistantToolUIProps } from '@assistant-ui/react'

/** AssistantUI derives part status from the whole answer/result. Our UI-only
 * artifact preserves each recorded terminal outcome without changing the answer,
 * parameters, result, visibility policy, or any gateway payload.
 */
export function resolvedToolPartStatus<T extends { type: string }>(status: T, artifact: unknown) {
  if (artifact && typeof artifact === 'object' && 'omnipusToolStatus' in artifact) {
    if (artifact.omnipusToolStatus === 'success') return { type: 'complete' } as const
    if (artifact.omnipusToolStatus === 'error') return { type: 'incomplete', reason: 'error' } as const
    if (artifact.omnipusToolStatus === 'cancelled') return { type: 'incomplete', reason: 'cancelled' } as const
  }
  return status
}

/** Keep native registrations on one outcome adapter, including legacy aliases. */
export function makeAssistantToolUI<TArgs, TResult>(tool: AssistantToolUIProps<TArgs, TResult>) {
  const Render = tool.render
  return makeNativeToolUI<TArgs, TResult>({
    ...tool,
    render: props => <Render {...props} status={resolvedToolPartStatus(props.status, props.artifact)} />,
  })
}
