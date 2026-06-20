import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { PencilSimple, Check, X, Link } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { MilestoneProgressBar } from './MilestoneProgressBar'
import { fetchMilestones, updateWorkspace, milestonesQueryKeys, workspacesQueryKeys, isApiError } from '@/lib/api'
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

  // Fetch milestones for progress bars
  const { data: milestones = [] } = useQuery({
    queryKey: milestonesQueryKeys.list(workspace.id),
    queryFn: () => fetchMilestones(workspace.id),
    staleTime: 30_000,
  })

  const updateMutation = useMutation({
    mutationFn: (name: string) => updateWorkspace(workspace.id, { name }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.detail(workspace.id) })
      addToast({ message: 'Workspace updated', variant: 'success' })
      setEditingName(false)
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update workspace'
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
            <button
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

      {/* Description + repo */}
      <div className="flex items-center gap-4 flex-wrap mb-2">
        {workspace.description && (
          <p className="text-xs text-[var(--color-muted)] flex-shrink-0 max-w-xl">
            {workspace.description}
          </p>
        )}
        {workspace.repository && (() => {
          // SEC-5: Only render an anchor when the URL has a safe http/https protocol.
          // javascript: and data: URIs would execute arbitrary code as the user.
          let isSafeUrl = false
          try {
            const proto = new URL(workspace.repository).protocol
            isSafeUrl = proto === 'http:' || proto === 'https:'
          } catch {
            // Unparseable URL — fall through to plain text.
          }
          return isSafeUrl ? (
            <a
              href={workspace.repository}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1 text-xs text-[var(--color-accent)] hover:underline flex-shrink-0"
            >
              <Link size={12} />
              Repository
            </a>
          ) : (
            <span className="flex items-center gap-1 text-xs text-[var(--color-muted)] flex-shrink-0">
              <Link size={12} />
              {workspace.repository}
            </span>
          )
        })()}
        <span className={cn(
          'text-xs text-[var(--color-muted)] flex-shrink-0',
          workspace.task_count === 0 && 'hidden',
        )}>
          {workspace.task_count} task{workspace.task_count !== 1 ? 's' : ''}
        </span>
      </div>

      {/* Milestone progress bars — hidden when no milestones */}
      {milestones.length > 0 && (
        <div className="flex flex-col gap-2 pt-2 border-t border-[var(--color-border)]/50">
          {milestones.map((m) => (
            <MilestoneProgressBar key={m.id} milestone={m} />
          ))}
        </div>
      )}
    </div>
  )
}
