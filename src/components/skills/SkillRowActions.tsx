// SkillRowActions — More actions on a skill row of Skills & Tools (spec
// User Story 7 scenario 8, S-58): Make a copy; Remove for a skill that is not
// built-in; "Choose category" while the skill is Uncategorised (C-21, once);
// "Approve this version" for an unverified skill under trust Ask (C-24).

import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { DotsThree } from '@phosphor-icons/react'
import { approveSkillVersion, deleteSkill, isApiError, setSkillCategory, type Skill } from '@/lib/api'
import { ConfigurationSaveError } from '@/lib/api/configuration'
import { serverErrorText } from '@/lib/api/permissions'
import type { SkillCategory } from '@/lib/api/generated/openapi-types'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useUiStore } from '@/store/ui'
import { SkillCopyDialog } from './SkillCopyDialog'
import { SKILL_CATEGORY_WORDS } from './skillsToolsModel'

/** Categories a person can choose; Uncategorised is where the skill is now. */
const CHOOSABLE = (Object.keys(SKILL_CATEGORY_WORDS) as SkillCategory[]).filter((value) => value !== 'uncategorised')

function reasonOf(err: unknown): string {
  if (isApiError(err)) return serverErrorText(err.body) ?? err.userMessage
  return err instanceof Error ? err.message : String(err)
}

interface SkillRowActionsProps {
  skill: Skill
  /** Every installed skill, for the copy name check. */
  skills: readonly Skill[]
  /** The stored "Unverified skills" answer. */
  trust: string | undefined
  disabled: boolean
}

export function SkillRowActions({ skill, skills, trust, disabled }: SkillRowActionsProps) {
  const client = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [copying, setCopying] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [choosing, setChoosing] = useState(false)
  const [category, setCategory] = useState<SkillCategory | null>(null)
  const [categoryError, setCategoryError] = useState<string | null>(null)

  const builtin = skill.source === 'builtin'
  const uncategorised = (skill.category ?? 'uncategorised') === 'uncategorised'
  const approvable = !skill.verified && trust === 'ask'
  const refreshSkills = () => client.invalidateQueries({ queryKey: ['skills'] })

  const remove = useMutation({
    mutationFn: () => deleteSkill(skill.id, skill.revision),
    onSuccess: () => {
      void refreshSkills()
      addToast({ message: `Removed ${skill.name}.`, variant: 'success' })
    },
    onError: (err: unknown) => {
      // Published but not fully saved: the list must show what is there now.
      if (err instanceof ConfigurationSaveError && err.state.persistence_status === 'complete') void refreshSkills()
      addToast({ message: `Could not remove ${skill.name}: ${reasonOf(err).replace(/\.+$/, '')}.`, variant: 'error' })
    },
  })

  const approve = useMutation({
    mutationFn: () => approveSkillVersion(skill.id, skill.revision),
    onSuccess: () => {
      void refreshSkills()
      addToast({ message: `Approved this version of ${skill.name}.`, variant: 'success' })
    },
    onError: (err: unknown) => {
      if (isApiError(err) && err.status === 409) void refreshSkills()
      addToast({ message: `Not approved: ${reasonOf(err).replace(/\.+$/, '')}.`, variant: 'error' })
    },
  })

  const saveCategory = useMutation({
    mutationFn: (value: SkillCategory) => setSkillCategory(skill.id, value),
    onSuccess: (_saved, value) => {
      void refreshSkills()
      setChoosing(false)
      addToast({ message: `${skill.name} is now under ${SKILL_CATEGORY_WORDS[value]}.`, variant: 'success' })
    },
    onError: (err: unknown) => {
      if (isApiError(err) && err.status === 409) void refreshSkills()
      setCategoryError(reasonOf(err))
    },
  })

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <IconButton
            aria-label={`More actions for ${skill.name}`}
            data-testid={`skill-actions-${skill.id.toLowerCase()}`}
            disabled={disabled}
            className="h-7 w-7 shrink-0 text-[var(--color-muted)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
          >
            <DotsThree size={18} weight="bold" />
          </IconButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={() => setCopying(true)}>Make a copy</DropdownMenuItem>
          {uncategorised && (
            <DropdownMenuItem
              onSelect={() => {
                setCategory(null)
                setCategoryError(null)
                setChoosing(true)
              }}
            >
              Choose category
            </DropdownMenuItem>
          )}
          {approvable && (
            <DropdownMenuItem disabled={approve.isPending} onSelect={() => approve.mutate()}>Approve this version</DropdownMenuItem>
          )}
          {!builtin && (
            <DropdownMenuItem className="text-[var(--color-error)]" onSelect={() => setRemoving(true)}>Remove</DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <SkillCopyDialog source={copying ? skill : null} skills={skills} onOpenChange={(open) => { if (!open) setCopying(false) }} />

      <ConfirmDialog
        open={removing}
        onOpenChange={(open) => { if (!open) setRemoving(false) }}
        title={`Remove ${skill.name}?`}
        description="Agents that use it can no longer load it. This cannot be undone."
        confirmLabel="Remove"
        destructive
        onConfirm={() => {
          setRemoving(false)
          remove.mutate()
        }}
      />

      <Dialog open={choosing} onOpenChange={(open) => { if (!open) setChoosing(false) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{`Choose a category for ${skill.name}`}</DialogTitle>
            <DialogDescription>You can choose it once. After that the category cannot be changed.</DialogDescription>
          </DialogHeader>
          <Select value={category ?? undefined} onValueChange={(value) => { setCategory(value as SkillCategory); setCategoryError(null) }}>
            <SelectTrigger aria-label="Category" className="w-full">
              <SelectValue placeholder="Choose a category" />
            </SelectTrigger>
            <SelectContent>
              {CHOOSABLE.map((value) => (
                <SelectItem key={value} value={value}>{SKILL_CATEGORY_WORDS[value]}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          {categoryError && (
            <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{categoryError}</p>
          )}
          <DialogFooter>
            <Button variant="ghost" onClick={() => setChoosing(false)}>Cancel</Button>
            <Button
              disabled={saveCategory.isPending}
              onClick={() => {
                if (!category) {
                  setCategoryError('Choose a category.')
                  return
                }
                saveCategory.mutate(category)
              }}
            >
              Save category
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
