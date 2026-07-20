import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task } from '@/lib/api'

interface TaskDetailSlideOverProps {
  task: Task | null
  onClose: () => void
}

/** Wraps TaskDetailPanel in a Sheet slide-over for the workspace board/list views. */
export function TaskDetailSlideOver({ task, onClose }: TaskDetailSlideOverProps) {
  return (
    <Sheet open={task != null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="w-full sm:w-[420px] md:w-[480px] overflow-y-auto p-0">
        <SheetHeader className="px-6 pr-14">
          <SheetTitle>
            {task?.title ?? ''}
          </SheetTitle>
        </SheetHeader>
        <div className="px-6 py-4">

        {task && (
          <TaskDetailPanel
            task={task}
            onClose={onClose}
          />
        )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
