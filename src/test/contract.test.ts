// Contract test: verify that the mock data shapes used in tests satisfy
// the TypeScript interfaces exported from api.ts.
//
// This is primarily a compile-time check: if any mock shape diverges from
// the interface, the `satisfies` operator causes a TypeScript error at build time.
//
// Runtime assertions are secondary — they confirm shapes are well-formed values.
//
// Traces to: wave5a-wire-ui-spec.md — Scenario: Frontend API type contract (E12)

import { describe, it, expect } from 'vitest'
import type {
  Agent,
  GatewayStatus,
  Task,
  Provider,
  Skill,
  Tool,
  Channel,
  McpServer,
  StorageStats,
  AppState,
  DoctorIssue,
  DoctorResult,
} from '@/lib/api'

describe('API contract: mock shapes satisfy TypeScript interfaces', () => {
  // ── GatewayStatus ──────────────────────────────────────────────────────────

  it('GatewayStatus mock shape satisfies interface', () => {
    const mock = {
      online: true,
      agent_count: 0,
      channel_count: 0,
      daily_cost: 0,
      version: '0.1.0',
    } satisfies GatewayStatus

    expect(typeof mock.online).toBe('boolean')
    expect(typeof mock.agent_count).toBe('number')
    expect(typeof mock.channel_count).toBe('number')
    expect(typeof mock.daily_cost).toBe('number')
  })

  it('GatewayStatus without optional version field satisfies interface', () => {
    const mock = {
      online: false,
      agent_count: 1,
      channel_count: 2,
      daily_cost: 0.5,
    } satisfies GatewayStatus

    expect(mock.online).toBe(false)
  })

  // ── Agent ─────────────────────────────────────────────────────────────────

  it('Agent (locked core type) mock shape satisfies interface', () => {
    const mock = {
      id: 'mia',
      name: 'Mia',
      description: 'Built-in core agent with compiled prompt',
      type: 'core' as const,
      locked: true,
      needs_model: false,
      model: 'claude-opus-4-6',
      status: 'active' as const,
      soul: '',
      timeout_seconds: 60,
      max_tool_iterations: 20,
      // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
      memory_enabled: true,
    } satisfies Agent

    expect(mock.id).toBe('mia')
    expect(mock.type).toBe('core')
    expect(mock.locked).toBe(true)
    expect(mock.status).toBe('active')
  })

  it('Agent (custom type) mock shape satisfies interface', () => {
    const mock = {
      id: 'my-agent',
      name: 'My Agent',
      description: '',
      type: 'Main' as const,
      locked: false,
      needs_model: false,
      model: 'claude-sonnet-4-6',
      status: 'idle' as const,
      soul: '',
      timeout_seconds: 60,
      max_tool_iterations: 20,
      // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
      memory_enabled: true,
    } satisfies Agent

    expect(mock.type).toBe('Main')
  })

  // ── Task (unified Sprint 2 model) ─────────────────────────────────────────

  it('Task mock shape satisfies interface', () => {
    const mock = {
      id: '550e8400-e29b-41d4-a716-446655440000',
      title: 'Test task',
      action: 'llm' as const,
      priority: 1,
      status: 'inbox' as const,
      workspace_id: 'ws-test',
      surface: 'user' as const,
      owner: 'alice',
      created_by: 'alice',
      created_at: '2026-06-20T10:00:00Z',
      updated_at: '2026-06-20T10:00:00Z',
    } satisfies Task

    expect(mock.id).toBeTruthy()
    expect(mock.status).toBe('inbox')
  })

  it('Task with 6-state status satisfies interface', () => {
    // Verify each of the 6 unified statuses is valid (ADR-051 D5 removed `planning`)
    const statuses: Task['status'][] = ['inbox', 'next', 'in_progress', 'blocked', 'done', 'failed']
    expect(statuses).toHaveLength(6)

    const mock = {
      id: '550e8400-e29b-41d4-a716-446655440001',
      title: 'Full task',
      action: 'llm' as const,
      prompt: 'Do the thing',
      priority: 5,
      status: 'in_progress' as const,
      workspace_id: 'ws-test',
      surface: 'user' as const,
      owner: 'alice',
      created_by: 'alice',
      created_at: '2026-06-20T10:00:00Z',
      updated_at: '2026-06-20T10:01:00Z',
      todos: [{ text: 'Step 1', status: 'pending' as const }, { text: 'Step 2', status: 'completed' as const }],
    } satisfies Task

    expect(mock.status).toBe('in_progress')
    expect(mock.todos).toHaveLength(2)
  })

  // ── Provider ──────────────────────────────────────────────────────────────

  it('Provider mock shape satisfies interface', () => {
    const mock = {
      id: 'anthropic',
      name: 'Anthropic',
      status: 'connected' as const,
      auth_method: 'api_key',
      dependents: [],
      backs_default: false,
      models: ['claude-sonnet-4-6', 'claude-opus-4-6'],
    } satisfies Provider

    expect(mock.status).toBe('connected')
  })

  it('Provider disconnected mock satisfies interface', () => {
    const mock = {
      id: 'default',
      name: 'Default',
      status: 'disconnected' as const,
      auth_method: 'api_key',
      dependents: [],
      backs_default: false,
      models: [],
    } satisfies Provider

    expect(mock.status).toBe('disconnected')
  })

  // ── Skill ─────────────────────────────────────────────────────────────────

  it('Skill mock shape satisfies interface', () => {
    const mock = {
      id: 'my-skill',
      name: 'My Skill',
      version: '1.0.0',
      description: 'A test skill',
      author: 'test',
      verified: false,
      status: 'active' as const,
    } satisfies Skill

    expect(mock.verified).toBe(false)
    expect(mock.status).toBe('active')
  })

  // ── Tool ──────────────────────────────────────────────────────────────────

  it('Tool mock shape satisfies interface', () => {
    const mock = {
      name: 'read_agent_metadata',
      scope: 'core' as const,
      category: 'agents',
      description: 'Read agent metadata',
      source: 'builtin' as const,
    } satisfies Tool

    expect(mock.category).toBe('agents')
  })

  // ── Channel ───────────────────────────────────────────────────────────────

  it('Channel mock shape satisfies interface', () => {
    const mock = {
      id: 'webchat' as const,
      name: 'Web Chat',
      transport: 'websocket' as const,
      enabled: true,
      description: 'Built-in browser chat',
    } satisfies Channel

    expect(mock.enabled).toBe(true)
  })

  // ── McpServer ─────────────────────────────────────────────────────────────

  it('McpServer mock shape satisfies interface', () => {
    const mock = {
      id: 'filesystem',
      name: 'Filesystem',
      transport: 'stdio' as const,
      status: 'connected' as const,
      tool_count: 5,
    } satisfies McpServer

    expect(mock.transport).toBe('stdio')
    expect(mock.tool_count).toBe(5)
  })

  // ── StorageStats ──────────────────────────────────────────────────────────

  it('StorageStats mock shape satisfies interface', () => {
    const mock = {
      workspace_size_bytes: 1024,
      session_count: 3,
      memory_entry_count: 10,
    } satisfies StorageStats

    expect(typeof mock.workspace_size_bytes).toBe('number')
  })

  // ── AppState ──────────────────────────────────────────────────────────────

  it('AppState (fresh install) mock shape satisfies interface', () => {
    const mock = {
      onboarding_complete: false,
    } satisfies AppState

    expect(mock.onboarding_complete).toBe(false)
  })

  it('AppState (completed) mock shape satisfies interface', () => {
    const mock = {
      onboarding_complete: true,
      last_doctor_run: '2026-03-29T00:00:00Z',
      last_doctor_score: 85,
    } satisfies AppState

    expect(mock.onboarding_complete).toBe(true)
    expect(mock.last_doctor_score).toBe(85)
  })

  // ── DoctorIssue / DoctorResult ────────────────────────────────────────────

  it('DoctorIssue mock shape satisfies interface', () => {
    const mock = {
      id: 'no-models',
      severity: 'high' as const,
      title: 'No LLM models configured',
      description: 'No models configured.',
      recommendation: 'Add a model.',
    } satisfies DoctorIssue

    expect(mock.severity).toBe('high')
  })

  it('DoctorResult mock shape satisfies interface', () => {
    const mock = {
      score: 80,
      issues: [
        {
          id: 'sandbox-disabled',
          severity: 'medium' as const,
          title: 'Sandbox disabled',
          description: 'Sandbox is not enabled.',
          recommendation: 'Enable sandbox.',
        },
      ],
      checked_at: '2026-03-29T00:00:00Z',
    } satisfies DoctorResult

    expect(mock.score).toBe(80)
    expect(mock.issues).toHaveLength(1)
  })
})
