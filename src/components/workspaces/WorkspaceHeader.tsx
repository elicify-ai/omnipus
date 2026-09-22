import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { PencilSimple, Check, X } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { updateWorkspace, workspacesQueryKeys, getErrorMessage } from '@/lib/api'
import type { Workspace } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

interface WorkspaceHeaderProps {
  workspace: Workspace
}

export function WorkspaceHeader({ workspace }: WorkspaceHeaderProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [editingName, setEditingName] = useState(false)
  const [nameDraft, setNameDraft] = useState(workspace.name)

  const updateMutation = useMutation({
    mutationFn: (name: string) => updateWorkspace(workspace.id, { revision: workspace.revision, name }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.detail(workspace.id) })
      addToast({ message: 'Workspace updated', variant: 'success' })
      setEditingName(false)
    },
    onError: (err) => {
      const msg = getErrorMessage(err, 'Failed to update workspace')
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleSaveName() {
    const trimmed = nameDraft.trim()
    if (!trimmed) return
    if (trimmed === workspace.name) {
      setEditingName(false)
      return
    }
    updateMutation.mutate(trimmed)
  }

  function handleCancelEdit() {
    setNameDraft(workspace.name)
    setEditingName(false)
  }

  return (
    <div className="px-[var(--space-3)] py-[var(--space-2-5)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)]">
      {/* Workspace name row */}
      <div className="flex items-center gap-[var(--space-2)] mb-[var(--space-1)]">
        {editingName ? (
          <div className="flex items-center gap-[var(--space-2)] flex-1">
            <Input
              value={nameDraft}
              onChange={(e) => setNameDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSaveName()
                if (e.key === 'Escape') handleCancelEdit()
              }}
              className="h-8 text-base font-headline font-bold bg-[var(--color-surface-2)]"
              autoFocus
              maxLength={200}
            />
            <Button
              size="sm"
              className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
              onClick={handleSaveName}
              disabled={updateMutation.isPending}
            >
              <Check size={12} weight="bold" />
              Save
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-[var(--space-2)] text-[length:var(--type-utility-xs-size)]"
              onClick={handleCancelEdit}
            >
              <X size={12} />
            </Button>
          </div>
        ) : (
          <>
            <h1 className="font-headline text-xl font-bold text-[var(--color-secondary)] flex-1 truncate">
              {workspace.name}
            </h1>
            <IconButton
              onClick={() => {
                setNameDraft(workspace.name)
                setEditingName(true)
              }}
              aria-label="Edit workspace name"
              variant="ghost"
              size="sm"
              className="h-auto w-auto flex-shrink-0 p-[var(--space-1)] text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
            >
              <PencilSimple size={14} />
            </IconButton>
          </>
        )}
      </div>

      {/* Description */}
      <div className="flex items-center gap-[var(--space-3)] flex-wrap mb-[var(--space-2)]">
        {workspace.description && (
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] flex-shrink-0 max-w-xl">
            {workspace.description}
          </p>
        )}
        <span className={cn(
          'text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] flex-shrink-0',
          workspace.task_count === 0 ? 'hidden' : undefined,
        )}>
          {workspace.task_count} task{workspace.task_count !== 1 ? 's' : ''}
        </span>
      </div>
    </div>
  )
}
