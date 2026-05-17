import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  PuzzlePiece,
  HardDrives,
  Hash,
  Wrench,
  Shield,
  Trash,
  Plus,
  MagnifyingGlass,
  CaretDown,
  CaretUp,
  Gear,
} from '@phosphor-icons/react'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  fetchSkills,
  fetchMcpServers,
  fetchTools,
  fetchChannels,
  deleteSkill,
  deleteMcpServer,
  enableChannel,
  disableChannel,
  isApiError,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SkillBrowser } from '@/components/skills/SkillBrowser'
import { McpServerModal } from '@/components/skills/McpServerModal'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'

function SkillsScreen() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [skillBrowserOpen, setSkillBrowserOpen] = useState(false)
  const [mcpModalOpen, setMcpModalOpen] = useState(false)
  const [confirmDeleteSkill, setConfirmDeleteSkill] = useState<string | null>(null)
  const [confirmDeleteMcp, setConfirmDeleteMcp] = useState<string | null>(null)
  const [expandedMcp, setExpandedMcp] = useState<string | null>(null)
  const [expandedTool, setExpandedTool] = useState<string | null>(null)
  const [configuringChannel, setConfiguringChannel] = useState<{ id: string; name: string } | null>(null)

  const { data: rawSkills = [], isLoading: skillsLoading, isError: skillsError } = useQuery({
    queryKey: ['skills'],
    queryFn: fetchSkills,
  })

  const skills = rawSkills

  const { data: mcpServers = [], isLoading: mcpLoading, isError: mcpError } = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: fetchMcpServers,
  })

  const { data: tools = [], isLoading: toolsLoading, isError: toolsError } = useQuery({
    queryKey: ['tools'],
    queryFn: fetchTools,
  })

  const { data: channels = [], isLoading: channelsLoading, isError: channelsError } = useQuery({
    queryKey: ['channels'],
    queryFn: fetchChannels,
  })

  const { mutate: doDeleteSkill } = useMutation({
    mutationFn: (name: string) => deleteSkill(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['skills'] })
      addToast({ message: 'Skill removed', variant: 'success' })
      setConfirmDeleteSkill(null)
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
      setConfirmDeleteSkill(null)
    },
  })

  const { mutate: doDeleteMcp } = useMutation({
    mutationFn: (id: string) => deleteMcpServer(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      addToast({ message: 'MCP server removed', variant: 'success' })
      setConfirmDeleteMcp(null)
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
      setConfirmDeleteMcp(null)
    },
  })

  const { mutate: doToggleChannel } = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      enabled ? disableChannel(id) : enableChannel(id),
    onSuccess: (_, { enabled }) => {
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      addToast({ message: enabled ? 'Channel disabled' : 'Channel enabled', variant: 'success' })
    },
    onError: (err: Error) => addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' }),
  })

  return (
    <div className="absolute inset-0 overflow-y-auto">
    <div className="max-w-4xl mx-auto px-4 py-6">
      <div className="mb-6">
        <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Skills & Tools</h1>
        <p className="text-sm text-[var(--color-muted)] mt-0.5">
          Manage agent capabilities — skills, MCP servers, channels, and built-in tools.
        </p>
      </div>

      <Tabs defaultValue="skills">
        <TabsList className="mb-6">
          <TabsTrigger value="skills" className="gap-1.5">
            <PuzzlePiece size={13} /> Installed Skills
          </TabsTrigger>
          <TabsTrigger value="mcp" className="gap-1.5">
            <HardDrives size={13} /> MCP Servers
          </TabsTrigger>
          <TabsTrigger value="channels" className="gap-1.5">
            <Hash size={13} /> Channels
          </TabsTrigger>
          <TabsTrigger value="builtins" className="gap-1.5">
            <Wrench size={13} /> Built-in Tools
          </TabsTrigger>
        </TabsList>

        {/* Installed Skills */}
        <TabsContent value="skills">
          <div className="flex justify-end mb-3">
            <Button size="sm" className="gap-1.5" onClick={() => setSkillBrowserOpen(true)}>
              <MagnifyingGlass size={13} /> Browse Skills
            </Button>
          </div>
          {skillsError ? (
            <ErrorState message="Could not load skills." />
          ) : skillsLoading ? (
            <SkeletonList />
          ) : skills.length === 0 ? (
            <EmptyState icon={<PuzzlePiece size={40} weight="thin" />} message="No skills installed." />
          ) : (
            <div className="space-y-2">
              {skills.map((skill) => (
                <div
                  key={skill.id}
                  className="flex items-start gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-medium text-sm text-[var(--color-secondary)]">{skill.name}</span>
                      <span className="font-mono text-[10px] text-[var(--color-muted)]">v{skill.version}</span>
                      {skill.verified && (
                        <Badge variant="success" className="gap-1 text-[10px]">
                          <Shield size={9} weight="fill" /> Verified
                        </Badge>
                      )}
                      <Badge
                        variant={skill.status === 'active' ? 'success' : skill.status === 'error' ? 'error' : 'muted'}
                        className="text-[10px]"
                      >
                        {skill.status}
                      </Badge>
                    </div>
                    <p className="text-xs text-[var(--color-muted)] mt-1">{skill.description}</p>
                    <div className="flex items-center gap-3 mt-1.5 text-[10px] text-[var(--color-muted)]">
                      <span>by {skill.author}</span>
                      {skill.agent_assignment && <span>→ {skill.agent_assignment}</span>}
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => setConfirmDeleteSkill(skill.name)}
                    className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors p-1 rounded shrink-0"
                    aria-label={`Remove ${skill.name}`}
                  >
                    <Trash size={14} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </TabsContent>

        {/* MCP Servers */}
        <TabsContent value="mcp">
          <div className="flex justify-end mb-3">
            <Button size="sm" className="gap-1.5" onClick={() => setMcpModalOpen(true)}>
              <Plus size={13} /> Add Server
            </Button>
          </div>
          {mcpError ? (
            <ErrorState message="Could not load MCP servers." />
          ) : mcpLoading ? (
            <SkeletonList />
          ) : mcpServers.length === 0 ? (
            <EmptyState icon={<HardDrives size={40} weight="thin" />} message="No MCP servers connected." />
          ) : (
            <div className="space-y-2">
              {mcpServers.map((server) => {
                const isExpanded = expandedMcp === server.id
                return (
                  <div
                    key={server.id}
                    className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
                  >
                    <div className="flex items-center gap-3 p-4">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-sm text-[var(--color-secondary)]">{server.name}</span>
                          <Badge variant="outline" className="text-[10px] font-mono">{server.transport}</Badge>
                          <Badge
                            variant={server.status === 'connected' ? 'success' : 'error'}
                            className="text-[10px]"
                          >
                            {server.status}
                          </Badge>
                        </div>
                      </div>
                      <div className="flex items-center gap-2 shrink-0">
                        <span className="text-xs text-[var(--color-muted)]">
                          {server.tool_count} tools
                        </span>
                        <button
                          type="button"
                          onClick={() => setExpandedMcp(isExpanded ? null : server.id)}
                          className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors p-1 rounded"
                          aria-label="Toggle tool list"
                        >
                          {isExpanded ? <CaretUp size={13} /> : <CaretDown size={13} />}
                        </button>
                        <button
                          type="button"
                          onClick={() => setConfirmDeleteMcp(server.id)}
                          className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors p-1 rounded"
                          aria-label={`Remove ${server.name}`}
                        >
                          <Trash size={14} />
                        </button>
                      </div>
                    </div>
                    {isExpanded && (
                      <div className="px-4 pb-4 border-t border-[var(--color-border)]">
                        {server.tools && server.tools.length > 0 ? (
                          <div className="pt-3 flex flex-wrap gap-1.5">
                            {server.tools.map((tool) => (
                              <span
                                key={tool}
                                className="font-mono text-[10px] px-2 py-0.5 rounded bg-[var(--color-surface-2)] border border-[var(--color-border)] text-[var(--color-muted)]"
                              >
                                {tool}
                              </span>
                            ))}
                          </div>
                        ) : (
                          <p className="pt-3 text-xs text-[var(--color-muted)]">No tool details available.</p>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </TabsContent>

        {/* Channels */}
        <TabsContent value="channels">
          {channelsError ? (
            <ErrorState message="Could not load channels." />
          ) : channelsLoading ? (
            <SkeletonList />
          ) : channels.length === 0 ? (
            <EmptyState icon={<Hash size={40} weight="thin" />} message="No channels configured." />
          ) : (
            <div className="space-y-2">
              {channels.map((channel) => {
                const connectionStatus = channel.enabled
                  ? 'enabled'
                  : 'unconfigured'
                return (
                  <div
                    key={channel.id}
                    className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
                  >
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-medium text-sm text-[var(--color-secondary)]">{channel.name}</span>
                        <Badge variant="outline" className="text-[10px] font-mono">{channel.transport}</Badge>
                        <Badge
                          variant={connectionStatus === 'enabled' ? 'success' : 'muted'}
                          className="text-[10px]"
                        >
                          {connectionStatus === 'enabled' ? 'Enabled' : 'Not configured'}
                        </Badge>
                      </div>
                    </div>
                    <div className="flex items-center gap-2 shrink-0">
                      <button
                        type="button"
                        onClick={() => setConfiguringChannel({ id: channel.id, name: channel.name })}
                        className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors font-medium"
                        aria-label={`Configure ${channel.name}`}
                      >
                        <Gear size={13} />
                        Configure
                      </button>
                      <button
                        type="button"
                        onClick={() => doToggleChannel({ id: channel.id, enabled: channel.enabled })}
                        className="text-xs text-[var(--color-accent)] hover:text-[var(--color-accent-hover)] transition-colors font-medium"
                      >
                        {channel.enabled ? 'Disable' : 'Enable'}
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </TabsContent>

        {/* Built-in tools */}
        <TabsContent value="builtins">
          {toolsError ? (
            <ErrorState message="Could not load tools." />
          ) : toolsLoading ? (
            <SkeletonList />
          ) : tools.length === 0 ? (
            <EmptyState icon={<Wrench size={40} weight="thin" />} message="No tools available." />
          ) : (
            <div className="space-y-1.5">
              {tools.map((tool) => {
                const isExpanded = expandedTool === tool.name
                return (
                  <div
                    key={tool.name}
                    className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
                  >
                    <button
                      type="button"
                      onClick={() => setExpandedTool(isExpanded ? null : tool.name)}
                      className="flex items-center gap-3 px-4 py-3 w-full text-left hover:bg-[var(--color-surface-2)] transition-colors"
                    >
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-xs text-[var(--color-secondary)]">{tool.name}</span>
                          <Badge variant="muted" className="text-[10px]">{tool.category}</Badge>
                        </div>
                      </div>
                      {isExpanded ? (
                        <CaretUp size={12} className="text-[var(--color-muted)] shrink-0" />
                      ) : (
                        <CaretDown size={12} className="text-[var(--color-muted)] shrink-0" />
                      )}
                    </button>
                    {isExpanded && (
                      <div className="px-4 pb-3 border-t border-[var(--color-border)]">
                        <p className="text-xs text-[var(--color-muted)] mt-2">{tool.description}</p>
                        <div className="flex items-center gap-2 mt-2">
                          <span className="text-[10px] text-[var(--color-muted)]">Status:</span>
                          <Badge variant="success" className="text-[10px]">enabled</Badge>
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </TabsContent>
      </Tabs>

      {/* Skill browser modal */}
      <SkillBrowser open={skillBrowserOpen} onOpenChange={setSkillBrowserOpen} />

      {/* MCP server add modal */}
      <McpServerModal open={mcpModalOpen} onOpenChange={setMcpModalOpen} />

      {/* Skill delete confirmation */}
      <Dialog open={confirmDeleteSkill !== null} onOpenChange={(o) => !o && setConfirmDeleteSkill(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Remove skill</DialogTitle>
            <DialogDescription>
              Remove <span className="font-medium text-[var(--color-secondary)]">{confirmDeleteSkill}</span>? This cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setConfirmDeleteSkill(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => confirmDeleteSkill && doDeleteSkill(confirmDeleteSkill)}
            >
              Remove
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* MCP server delete confirmation */}
      <Dialog open={confirmDeleteMcp !== null} onOpenChange={(o) => !o && setConfirmDeleteMcp(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Remove MCP server</DialogTitle>
            <DialogDescription>
              Remove this MCP server? Any agents using it will lose access to its tools.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setConfirmDeleteMcp(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => confirmDeleteMcp && doDeleteMcp(confirmDeleteMcp)}
            >
              Remove
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Channel config slide-over */}
      {configuringChannel && (
        <ChannelConfigPanel
          channelId={configuringChannel.id}
          channelName={configuringChannel.name}
          open={configuringChannel !== null}
          onOpenChange={(open) => {
            if (!open) setConfiguringChannel(null)
          }}
        />
      )}
    </div>
    </div>
  )
}

function SkeletonList() {
  return (
    <div className="space-y-2">
      {[1, 2, 3].map((i) => (
        <div key={i} className="h-16 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse" />
      ))}
    </div>
  )
}

function EmptyState({ icon, message }: { icon: React.ReactNode; message: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 gap-3 text-center">
      <div className="text-[var(--color-border)]">{icon}</div>
      <p className="text-sm text-[var(--color-muted)]">{message}</p>
    </div>
  )
}

function ErrorState({ message }: { message: string }) {
  return (
    <div className="flex justify-center py-8">
      <p className="text-sm text-[var(--color-error)]">{message}</p>
    </div>
  )
}

export const Route = createFileRoute('/_app/skills')({
  component: SkillsScreen,
})
