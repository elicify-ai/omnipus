import { Outlet, createFileRoute } from '@tanstack/react-router'
import { authenticatedBeforeLoad } from './-authenticatedBeforeLoad'

/** Authenticated, chrome-less layout for shell-owned full-screen panels. */
export const Route = createFileRoute('/_fullscreen')({
  beforeLoad: authenticatedBeforeLoad,
  component: Outlet,
})
