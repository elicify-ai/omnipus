import { createFileRoute } from '@tanstack/react-router'
import { OmnipusChatScreen } from '@/components/chat/OmnipusChatScreen'

// /omnipus — Omnipus's one conversation (omnipus-system-agent FR-100). Opened
// from the fixed sidebar entry in any workspace; the active workspace stays
// as it is. autoCodeSplitting extracts this component into its own chunk.
export const Route = createFileRoute('/_app/omnipus')({
  component: OmnipusChatScreen,
})
