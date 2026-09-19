import { Switch } from '@/components/ui/switch'
import type { McpServer, AgentToolsCfg } from '@/lib/api'

type McpBinding = NonNullable<NonNullable<AgentToolsCfg['mcp']>['servers']>[number]

interface MCPServerPickerProps {
  servers: McpServer[]
  mcpConfig: AgentToolsCfg['mcp']
  onChange: (mcp: NonNullable<AgentToolsCfg['mcp']>) => void
  disabled?: boolean
  /** Catalog tool names for each installed server id. */
  serverToolNames?: Record<string, string[]>
}

type BindingMode = 'all' | 'selected' | 'none'

function bindingFor(mcpConfig: AgentToolsCfg['mcp'], serverId: string): McpBinding | undefined {
  return mcpConfig?.servers?.find((server) => server.id === serverId)
}

function modeOf(binding: McpBinding | undefined): BindingMode | 'unassigned' {
  if (!binding) return 'unassigned'
  if (binding.tools === undefined) return 'all'
  if (binding.tools.length === 0) return 'none'
  return 'selected'
}

function uniqueNames(names: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const name of names) {
    if (seen.has(name)) continue
    seen.add(name)
    out.push(name)
  }
  return out
}

export function MCPServerPicker({
  servers,
  mcpConfig,
  onChange,
  disabled = false,
  serverToolNames = {},
}: MCPServerPickerProps) {
  if (servers.length === 0) {
    return (
      <p className="text-xs text-[var(--color-muted)] py-2">
        No MCP servers configured. Add servers on the Skills &amp; Tools screen.
      </p>
    )
  }

  function writeServers(next: McpBinding[]) {
    onChange({ servers: next })
  }

  function setBinding(serverId: string, next: McpBinding | null) {
    const current = mcpConfig?.servers ?? []
    const without = current.filter((server) => server.id !== serverId)
    writeServers(next ? [...without, next] : without)
  }

  return (
    <div className="space-y-2" data-testid="mcp-server-picker">
      {servers.map((server) => {
        const binding = bindingFor(mcpConfig, server.id)
        const mode = modeOf(binding)
        const isEnabled = mode !== 'unassigned'
        const catalog = uniqueNames([
          ...(serverToolNames[server.id] ?? []),
          ...(binding?.tools ?? []),
        ])
        const selectedDisabled = catalog.length === 0
        return (
          <div
            key={server.id}
            className="rounded-md bg-[var(--color-surface-1)] border border-[var(--color-border)] px-3 py-2.5 space-y-2"
            data-testid={`mcp-binding-${server.id}`}
          >
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0 flex-1">
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
                disabled={disabled}
                onCheckedChange={(checked) => setBinding(server.id, checked ? { id: server.id } : null)}
                aria-label={`${isEnabled ? 'Unassign' : 'Assign'} ${server.name}`}
              />
            </div>
            {isEnabled && (
              <fieldset className="space-y-1" disabled={disabled}>
                <legend className="sr-only">Tools from {server.name}</legend>
                {([
                  ['all', 'All tools', false],
                  ['selected', 'Selected tools', selectedDisabled],
                  ['none', 'No tools', false],
                ] as const).map(([value, label, optionDisabled]) => (
                  <label
                    key={value}
                    className="flex items-center gap-2 text-[11px] text-[var(--color-secondary)]"
                  >
                    <input tabIndex={0}
                      type="radio"
                      name={`mcp-mode-${server.id}`}
                      value={value}
                      checked={mode === value}
                      disabled={optionDisabled}
                      data-testid={`mcp-mode-${server.id}-${value}`}
                      onChange={() => {
                        if (value === 'all') setBinding(server.id, { id: server.id })
                        else if (value === 'none') setBinding(server.id, { id: server.id, tools: [] })
                        else {
                          const names = (binding?.tools && binding.tools.length > 0)
                            ? binding.tools
                            : catalog
                          setBinding(server.id, { id: server.id, tools: [...names] })
                        }
                      }}
                    />
                    {label}
                  </label>
                ))}
                {mode === 'selected' && (
                  <div className="pl-5 space-y-1" data-testid={`mcp-selected-${server.id}`}>
                    {catalog.length === 0 ? (
                      <p className="text-[10px] text-[var(--color-muted)]">No catalog tools listed for this server.</p>
                    ) : catalog.map((toolName) => {
                      const checked = (binding?.tools ?? []).includes(toolName)
                      return (
                        <label key={toolName} className="flex items-center gap-2 text-[11px] font-mono text-[var(--color-muted)]">
                          <input tabIndex={0}
                            type="checkbox"
                            checked={checked}
                            data-testid={`mcp-tool-${server.id}-${toolName}`}
                            onChange={(event) => {
                              const current = binding?.tools ?? []
                              const nextTools = event.target.checked
                                ? uniqueNames([...current, toolName])
                                : current.filter((name) => name !== toolName)
                              setBinding(server.id, { id: server.id, tools: nextTools })
                            }}
                          />
                          {toolName}
                        </label>
                      )
                    })}
                  </div>
                )}
              </fieldset>
            )}
          </div>
        )
      })}
    </div>
  )
}
