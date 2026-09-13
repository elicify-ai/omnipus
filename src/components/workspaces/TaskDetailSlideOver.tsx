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
          // `key` is load-bearing, not a React-list formality. The sheet stays
          // MOUNTED when the selected task changes (both tasks are non-null,
          // so `open` never goes false), and the panel's subtree holds
          // per-task local state its own effects do not reach — most sharply
          // the write-set ChipListInput's in-progress path draft and its
          // inline validation
          // error. Without this, typing a path into task A and clicking task B
          // shows A's draft and A's error sitting in B's panel, and pressing
          // Add commits that path to the WRONG task. Keying on the id remounts
          // the subtree, which is the only thing that clears state the panel
          // does not own.
          <TaskDetailPanel
            key={task.id}
            task={task}
            onClose={onClose}
          />
        )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
