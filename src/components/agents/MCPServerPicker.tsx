import { Switch } from '@/components/ui/switch'
import type { McpServer, AgentToolsCfg } from '@/lib/api'

interface MCPServerPickerProps {
  servers: McpServer[]
  mcpConfig: AgentToolsCfg['mcp']
  onChange: (mcp: AgentToolsCfg['mcp']) => void
}

export function MCPServerPicker({ servers, mcpConfig, onChange }: MCPServerPickerProps) {
  if (servers.length === 0) {
    return (
      <p className="text-xs text-[var(--color-muted)] py-2">
        No MCP servers configured. Add servers in Settings.
      </p>
    )
  }

  const enabledIds = new Set(mcpConfig?.servers?.map((s) => s.id) ?? [])

  function toggleServer(serverId: string, enabled: boolean) {
    const current = mcpConfig?.servers ?? []
    const next = enabled
      ? [...current, { id: serverId }]
      : current.filter((s) => s.id !== serverId)
    onChange({ servers: next })
  }

  return (
    <div className="space-y-1">
      {servers.map((server) => {
        const isEnabled = enabledIds.has(server.id)
        return (
          <div
            key={server.id}
            className="flex items-center justify-between px-3 py-2.5 rounded-md bg-[var(--color-surface-1)] border border-[var(--color-border)]"
          >
            <div className="min-w-0 flex-1 mr-4">
              <p className="text-sm text-[var(--color-secondary)] font-medium truncate">
                {server.name}
              </p>
              <p className="text-[10px] text-[var(--color-muted)]">
                {server.tool_count} tool{server.tool_count !== 1 ? 's' : ''}
                {server.transport === 'stdio' ? ' — local program' : ' — network'}
              </p>
            </div>
            <Switch
              checked={isEnabled}
              onCheckedChange={(checked) => toggleServer(server.id, checked)}
            />
          </div>
        )
      })}
    </div>
  )
}
