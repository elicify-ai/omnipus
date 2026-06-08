import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the Tasks (GTD Kanban) screen is heavy and only needed when this
// route is visited. The screen body lives in @/components/screens/TasksScreen.
const TasksScreen = lazy(() =>
  import('@/components/screens/TasksScreen').then((m) => ({ default: m.TasksScreen })),
)

function TasksRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <TasksScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/tasks')({
  component: TasksRoute,
})
