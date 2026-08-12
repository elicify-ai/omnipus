import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { PencilSimple, Check, X } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
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
    mutationFn: (name: string) => updateWorkspace(workspace.id, { name }),
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
    <div className="px-4 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]">
      {/* Workspace name row */}
      <div className="flex items-center gap-2 mb-1">
        {editingName ? (
          <div className="flex items-center gap-2 flex-1">
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
              className="h-7 px-2 gap-1 text-xs"
              onClick={handleSaveName}
              disabled={updateMutation.isPending}
            >
              <Check size={12} weight="bold" />
              Save
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs"
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
            <button tabIndex={0}
              type="button"
              onClick={() => {
                setNameDraft(workspace.name)
                setEditingName(true)
              }}
              aria-label="Edit workspace name"
              className="p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex-shrink-0"
            >
              <PencilSimple size={14} />
            </button>
          </>
        )}
      </div>

      {/* Description */}
      <div className="flex items-center gap-4 flex-wrap mb-2">
        {workspace.description && (
          <p className="text-xs text-[var(--color-muted)] flex-shrink-0 max-w-xl">
            {workspace.description}
          </p>
        )}
        <span className={cn(
          'text-xs text-[var(--color-muted)] flex-shrink-0',
          workspace.task_count === 0 && 'hidden',
        )}>
          {workspace.task_count} task{workspace.task_count !== 1 ? 's' : ''}
        </span>
      </div>
    </div>
  )
}
