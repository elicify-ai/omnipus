// agentKind — the single place that answers "what kind of agent is this?"
// for UI gating, per the authoritative field allocation in
// docs/internal/architecture/agent-types-field-matrix.md.
//
// Four kinds: built-in `core` (locked roster), `Main` (chat colleague),
// `Subagent` (native delegation-only worker), `subagent_3p` (delegation-only
// worker on an external CLI). The legacy `worker` type is the build-time/seed
// config constant, not emitted by the gateway; it is treated as a worker
// defensively (mirroring `isWorker` in src/lib/api.ts) and classified
// native-vs-external by its executor kind, since the type alone can't tell.

/** Delegation-only labour agent (never a chat target): Subagent or subagent_3p. */
export function isWorkerType(type: string | null | undefined): boolean {
  return type === 'Subagent' || type === 'subagent_3p' || type === 'worker'
}

/** Worker that runs on an external CLI (claude-code / codex / opencode). */
export function isExternalType(type: string | null | undefined): boolean {
  return type === 'subagent_3p'
}

/** Worker that runs on the Omnipus engine. */
export function isNativeWorkerType(type: string | null | undefined): boolean {
  return isWorkerType(type) && !isExternalType(type)
}

export interface AgentKindFlags {
  /** Built-in roster agent (Mia/Jim/Ava/Ray) — identity/prompt/skills locked. */
  isLocked: boolean
  /** Subagent, subagent_3p, or legacy worker — delegation-only, never a chat target. */
  isWorker: boolean
  /** Runs on an external CLI: subagent_3p, or a legacy worker with an external-cli executor. */
  isExternal: boolean
  /** Worker on the Omnipus engine (worker && !external). */
  isNativeWorker: boolean
}

/**
 * Classify an agent for UI gating. Accepts a loose shape (like `isWorker` in
 * src/lib/api.ts) so it works on partial agent objects and on the legacy
 * `worker` type, which the generated Agent enum no longer carries. Legacy
 * `worker` is the only case where the executor is consulted — the modern
 * wire enum encodes the runner in the type.
 */
export function agentKindFlags(agent: {
  type?: string | null
  locked?: boolean | null
  executor?: { kind?: string | null } | null
}): AgentKindFlags {
  const type = agent.type
  const isWorkerKind = isWorkerType(type)
  const isExternal =
    isExternalType(type) ||
    (type === 'worker' && agent.executor?.kind === 'external-cli')
  return {
    isLocked: agent.locked === true,
    isWorker: isWorkerKind,
    isExternal,
    isNativeWorker: isWorkerKind && !isExternal,
  }
}
