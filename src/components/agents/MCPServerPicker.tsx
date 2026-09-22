import { Switch } from '@/components/ui/switch'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'
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
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] py-[var(--space-2)]">
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
    <div className="space-y-[var(--space-2)]" data-testid="mcp-server-picker">
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
            className="rounded-md bg-[var(--color-surface-1)] border border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-2)] space-y-[var(--space-2)]"
            data-testid={`mcp-binding-${server.id}`}
          >
            <div className="flex items-center justify-between gap-[var(--space-2-5)]">
              <div className="min-w-0 flex-1">
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] font-medium truncate">
                  {server.name}
                </p>
                <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
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
              <fieldset className="space-y-[var(--space-1)]" disabled={disabled}>
                <legend className="sr-only">Tools from {server.name}</legend>
                <RadioGroup
                  value={mode}
                  onValueChange={(value) => {
                    if (value === 'all') setBinding(server.id, { id: server.id })
                    else if (value === 'none') setBinding(server.id, { id: server.id, tools: [] })
                    else {
                      const names = (binding?.tools && binding.tools.length > 0)
                        ? binding.tools
                        : catalog
                      setBinding(server.id, { id: server.id, tools: [...names] })
                    }
                  }}
                  aria-label={`Tools from ${server.name}`}
                  orientation="vertical"
                  className="gap-[var(--space-1)]"
                >
                  {([
                    ['all', 'All tools', false],
                    ['selected', 'Selected tools', selectedDisabled],
                    ['none', 'No tools', false],
                  ] as const).map(([value, label, optionDisabled]) => {
                    const checked = mode === value
                    return (
                      <RadioGroupItem
                        key={value}
                        value={value}
                        disabled={optionDisabled}
                        data-testid={`mcp-mode-${server.id}-${value}`}
                        className="h-auto w-auto justify-start gap-[var(--space-2)] border-0 bg-transparent p-0 text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] text-[var(--color-secondary)] hover:bg-transparent hover:text-[var(--color-secondary)]"
                      >
                        <span
                          className={cn(
                            'inline-block h-[9px] w-[9px] shrink-0 rounded-full border-2 transition-colors',
                            checked ? 'border-[var(--color-accent)] bg-[var(--color-accent)]' : 'border-[var(--color-border)]',
                          )}
                          aria-hidden="true"
                        />
                        {label}
                      </RadioGroupItem>
                    )
                  })}
                </RadioGroup>
                {mode === 'selected' && (
                  <div className="ml-[var(--space-4)] space-y-[var(--space-1)]" data-testid={`mcp-selected-${server.id}`}>
                    {catalog.length === 0 ? (
                      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">No catalog tools listed for this server.</p>
                    ) : catalog.map((toolName) => {
                      const checked = (binding?.tools ?? []).includes(toolName)
                      const toolCheckboxId = `mcp-tool-${server.id}-${toolName}`
                      return (
                        <div key={toolName} className="flex items-center gap-[var(--space-2)]">
                          <Checkbox
                            id={toolCheckboxId}
                            checked={checked}
                            data-testid={toolCheckboxId}
                            onCheckedChange={(next) => {
                              const current = binding?.tools ?? []
                              const nextTools = next === true
                                ? uniqueNames([...current, toolName])
                                : current.filter((name) => name !== toolName)
                              setBinding(server.id, { id: server.id, tools: nextTools })
                            }}
                          />
                          <Label htmlFor={toolCheckboxId} className="cursor-pointer text-[length:var(--type-caption-size)] font-mono font-[var(--font-weight-regular)] text-[var(--color-muted)]">
                            {toolName}
                          </Label>
                        </div>
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
