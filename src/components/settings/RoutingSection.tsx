import { useState, useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Input } from '@/components/ui/input'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { SmartSelect } from '@/components/ui/smart-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { fetchChannels, configureChannel, fetchAgents } from '@/lib/api'

type DmPolicy = 'allow' | 'deny' | 'known_only'

interface ChannelRoute {
  id: string
  name: string
  allow_from: string
  dm_policy: DmPolicy
  default_agent_id: string
}

export function RoutingSection() {
  const queryClient = useQueryClient()
  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  const [routes, setRoutes] = useState<ChannelRoute[]>([])

  const { data: channels, isLoading } = useQuery({
    queryKey: ['channels'],
    queryFn: fetchChannels,
  })

  const { data: agents } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  useEffect(() => {
    if (!channels) return
    if (isDirtyRef.current) return
    setRoutes(
      channels.map((ch) => ({
        id: ch.id,
        name: ch.name,
        allow_from: '',
        dm_policy: 'allow' as DmPolicy,
        default_agent_id: '',
      }))
    )
  }, [channels])

  const { status: saveStatus, error: saveError } = useAutoSave(
    routes,
    async (currentRoutes) => {
      await Promise.all(
        currentRoutes.map((r) =>
          configureChannel(r.id, {
            allow_from: r.allow_from
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean),
            dm_policy: r.dm_policy,
            default_agent_id: r.default_agent_id || null,
          })
        )
      )
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['channels'] })
    },
    { disabled: routes.length === 0 },
  )

  function updateRoute(id: string, field: keyof ChannelRoute, value: string) {
    markDirty()
    setRoutes((prev) =>
      prev.map((r) => (r.id === id ? { ...r, [field]: value } : r))
    )
  }

  if (isLoading) return <div className="text-sm text-[var(--color-muted)]">Loading...</div>

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Routing & Policies</h2>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            Control which users can send messages per channel and how DMs are handled.
          </p>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      {routes.length === 0 ? (
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-8 text-center">
          <p className="text-sm text-[var(--color-muted)]">No channels configured.</p>
          <p className="text-xs text-[var(--color-muted)] mt-1">Enable channels in the Providers tab first.</p>
        </div>
      ) : (
        <div className="rounded-lg border border-[var(--color-border)] overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow className="border-[var(--color-border)] hover:bg-transparent">
                <TableHead className="text-xs text-[var(--color-muted)]">Channel</TableHead>
                <TableHead className="text-xs text-[var(--color-muted)]">Default Agent</TableHead>
                <TableHead className="text-xs text-[var(--color-muted)]">
                  Allow From
                  <span className="ml-1 font-normal">(comma-separated IDs)</span>
                </TableHead>
                <TableHead className="text-xs text-[var(--color-muted)]">DM Policy</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {routes.map((route) => (
                <TableRow key={route.id} className="border-[var(--color-border)]">
                  <TableCell className="text-sm text-[var(--color-secondary)] font-medium">
                    {route.name}
                  </TableCell>
                  <TableCell>
                    <SmartSelect
                      value={route.default_agent_id || '__none__'}
                      onValueChange={(v) => updateRoute(route.id, 'default_agent_id', v === '__none__' ? '' : v)}
                      triggerClassName="w-[160px] h-7 text-xs"
                      placeholder="(global default)"
                      items={[
                        { value: '__none__', label: '(global default)' },
                        ...(agents ?? []).map((a) => ({ value: a.id, label: a.name })),
                      ]}
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      value={route.allow_from}
                      onChange={(e) => updateRoute(route.id, 'allow_from', e.target.value)}
                      placeholder="Leave empty to allow all"
                      className="h-7 text-xs font-mono w-full max-w-xs"
                    />
                  </TableCell>
                  <TableCell>
                    <SmartSelect
                      value={route.dm_policy}
                      onValueChange={(v) => updateRoute(route.id, 'dm_policy', v)}
                      triggerClassName="w-[130px] h-7 text-xs"
                      items={[
                        { value: 'allow', label: 'Allow all' },
                        { value: 'known_only', label: 'Known users only' },
                        { value: 'deny', label: 'Deny all' },
                      ]}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <p className="text-xs text-[var(--color-muted)]">
        Changes take effect immediately for new messages. Existing sessions are not affected.
      </p>
    </div>
  )
}
