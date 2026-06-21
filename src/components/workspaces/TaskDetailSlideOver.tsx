import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { TaskDetailPanel } from '@/components/command-center/TaskDetailPanel'
import type { Task } from '@/lib/api'

interface TaskDetailSlideOverProps {
  task: Task | null
  onClose: () => void
}

/** Wraps TaskDetailPanel in a Sheet slide-over for the workspace board/list views. */
export function TaskDetailSlideOver({ task, onClose }: TaskDetailSlideOverProps) {
  return (
    <Sheet open={task != null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="w-full sm:w-[420px] md:w-[480px] overflow-y-auto">
        <SheetHeader className="mb-5">
          <SheetTitle className="pr-6 leading-snug font-headline text-[var(--color-secondary)]">
            {task?.title ?? ''}
          </SheetTitle>
        </SheetHeader>

        {task && (
          <TaskDetailPanel
            task={task}
            onClose={onClose}
          />
        )}
      </SheetContent>
    </Sheet>
  )
}
