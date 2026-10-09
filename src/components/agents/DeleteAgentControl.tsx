import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { deleteAgent } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { ConfigurationSaveError } from '@/lib/api/configuration'
import { useUiStore } from '@/store/ui'

/**
 * The Delete control for the agent profile — the UI half of C-DELETE (FR-037).
 *
 * Double confirmation (founder 2026-10-07 Q6): the destructive action is
 * confirmed twice inside one catalogued `ConfirmDialog` before the delete is
 * sent, and dismissing either step fires nothing. The agent tool and the REST
 * API keep their single approval — this two-step gate is UI-only, over the one
 * same deletion cascade.
 *
 * Partly-deleted (FR-037 / BDD-11.2/11.3/11.5): when the cascade reports
 * `persistence_status: partial`, the agent record stays visible and the same
 * Delete retries it. Both the list and the single-agent query are invalidated
 * so the retry carries the current revision — the same retry also works after
 * a restart, when the still-present record reloads with its own revision.
 *
 * A `persistence_status: complete` failure is the opposite case: the record is
 * gone from the next read and only live activation failed, so the row is
 * dropped and the delete is reported incomplete rather than clean.
 */
export interface DeleteAgentControlProps {
  agentId: string
  agentName: string
  /** The revision the operator reviewed; required by the delete precondition. */
  revision: string | undefined
}

export function DeleteAgentControl({ agentId, agentName, revision }: DeleteAgentControlProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const closeEditAgentSlideOver = useUiStore((s) => s.closeEditAgentSlideOver)
  // 0 = closed, 1 = first confirmation, 2 = final confirmation.
  const [step, setStep] = useState<0 | 1 | 2>(0)

  const dropFromList = () => {
    // Drop the deleted agent from the list cache immediately so no per-id GET
    // refetch fires for a resource that no longer exists.
    queryClient.setQueryData(['agents'], (prev: unknown) => {
      if (!Array.isArray(prev)) return prev
      return prev.filter((a) => (a as { id?: string }).id !== agentId)
    })
    queryClient.invalidateQueries({ queryKey: ['agents'] })
    queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
  }

  const mutation = useMutation({
    mutationFn: () => {
      if (!revision) throw new Error('Agent has no reviewed revision. Reload before deleting.')
      return deleteAgent(agentId, revision)
    },
    onSuccess: () => {
      dropFromList()
      setStep(0)
      closeEditAgentSlideOver()
      addToast({ message: 'Agent deleted', variant: 'success' })
    },
    onError: (err: unknown) => {
      setStep(0)
      if (err instanceof ConfigurationSaveError) {
        const { persistence_status } = err.state
        if (persistence_status === 'complete') {
          // Persistence is authoritative for what the next read will return.
          // Discard the stale deleted resource even though live activation
          // failed, then force both views to reconcile with stored state.
          dropFromList()
          closeEditAgentSlideOver()
          addToast({ message: `Delete incomplete: ${err.message}`, variant: 'error' })
          return
        }
        if (persistence_status === 'partial') {
          // Partly deleted: some owned data was removed but the record still
          // exists. Keep the agent visible, refresh so the next Delete retries
          // with the current revision, and never claim a clean success.
          queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
          queryClient.invalidateQueries({ queryKey: ['agents'] })
          addToast({
            message: `Partly deleted: some of ${agentName}'s data was removed but the agent still exists. Press Delete again to finish.`,
            variant: 'warning',
          })
          return
        }
        // persistence_status === 'none': nothing was saved. Keep the editor open
        // on the same agent so the operator can retry from the same state.
        addToast({ message: `Delete failed: ${err.message}`, variant: 'error' })
        return
      }
      const msg = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : 'Delete failed'
      addToast({ message: `Delete failed: ${msg}`, variant: 'error' })
    },
  })

  const isFinal = step === 2

  return (
    <>
      <Button
        variant="destructive"
        data-testid="delete-agent-button"
        onClick={() => setStep(1)}
        className="ml-auto"
      >
        <Trash size={13} className="mr-[var(--space-1)]" />
        Delete agent
      </Button>

      {/* Wave 5 / spec §6.1 BDD #15 + FR-037 Q6: one catalogued `ConfirmDialog`
          carries the two-step confirmation. The first step warns about the
          deletion cascade; the second is the final confirmation. Dismissing
          either step fires nothing. */}
      <ConfirmDialog
        open={step > 0}
        onOpenChange={(open) => {
          if (!open) setStep(0)
        }}
        title={isFinal ? `Permanently delete ${agentName}?` : `Delete ${agentName}?`}
        description={
          isFinal
            ? 'Final confirmation. This cannot be undone.'
            : 'This permanently deletes this agent and all of its chats, memory and related data. This cannot be undone. You will be asked to confirm once more.'
        }
        confirmLabel={mutation.isPending ? 'Deleting…' : isFinal ? 'Delete permanently' : 'Continue'}
        destructive
        pending={mutation.isPending}
        onConfirm={() => {
          if (isFinal) mutation.mutate()
          else setStep(2)
        }}
      />
    </>
  )
}
