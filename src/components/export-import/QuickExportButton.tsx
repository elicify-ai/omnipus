// QuickExportButton — the outline **Export** button in an agent's edit sheet
// (spec US-3): closes the sheet and opens the export wizard's Choose step
// with the agent already chosen. Not rendered for built-in agents (locked,
// core or system) or outside the self-hosted (local) edition.
import { useNavigate } from '@tanstack/react-router'
import { currentIdentityMode } from '@/lib/api/identityMode'
import { Button } from '@/components/ui/button'
import { EXPORT_IMPORT_PATH } from './shared'

export interface QuickExportButtonProps {
  agent: { id: string; name: string; type: 'core' | 'system' | 'Main' | 'Subagent' | 'subagent_3p'; locked: boolean }
  onClose?: () => void
}

export function QuickExportButton({ agent, onClose }: QuickExportButtonProps) {
  const navigate = useNavigate()
  if (currentIdentityMode() !== 'local') return null
  if (agent.locked || agent.type === 'core' || agent.type === 'system') return null
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={() => {
        onClose?.()
        void navigate({
          to: EXPORT_IMPORT_PATH,
          search: { wizard: 'export', step: 'choose', scope: 'agents', preselect: `agent:${agent.id}` },
        })
      }}
    >
      Export
    </Button>
  )
}
