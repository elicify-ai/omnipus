// agentKind — the single place that answers "what kind of agent is this?"
// for UI gating, per the authoritative field allocation in
// docs/internal/architecture/agent-types-field-matrix.md.
//
// Four kinds: built-in `core` (locked roster), `Main` (chat colleague),
// `Subagent` (native delegation-only worker), `subagent_3p` (delegation-only
// worker on an external CLI). Classification is TYPE-based only: the two
// modern wire enum worker types carry the runner, so neither the legacy
// `worker` constant nor `executor.kind` is consulted (DEL-F04).

/** Delegation-only labour agent (never a chat target): Subagent or subagent_3p. */
export function isWorkerType(type: string | null | undefined): boolean {
  return type === 'Subagent' || type === 'subagent_3p'
}

/**
 * Locked System Agent (Judge, ADR-049 D3) — never a chat target, never a
 * delegation/team-picker candidate, never deletable/disable-able/★-eligible.
 * Distinct from `core` (the built-in Main-agent roster): a `system` agent is
 * excluded from `mainAgents` entirely and rendered in its own locked section
 * (SD-C16).
 */
export function isSystemType(type: string | null | undefined): boolean {
  return type === 'system'
}

/**
 * Worker that runs on an external CLI (claude-code / codex / opencode).
 *
 * TYPE-STRING-ONLY check — it can only ever be true for `subagent_3p`, which
 * is the only type the modern wire enum encodes as external. Callers holding a
 * full agent object use `agentKindFlags`, which classifies by the same type.
 */
export function isExternalType(type: string | null | undefined): boolean {
  return type === 'subagent_3p'
}

export interface AgentKindFlags { // not-wire-format: UI-only kind classification derived from an Agent client-side; never serialized or sent over the gateway boundary
  /** Built-in roster agent (Mia/Jim/Ava/Ray) — identity/prompt/skills locked. */
  isLocked: boolean
  /** Subagent or subagent_3p — delegation-only, never a chat target. */
  isWorker: boolean
  /** Runs on an external CLI (subagent_3p). */
  isExternal: boolean
  /** Worker on the Omnipus engine (worker && !external). */
  isNativeWorker: boolean
  /** Locked System Agent (Judge) — ADR-049 D3. Never a chat target, never a default-agent candidate. */
  isSystem: boolean
}

/**
 * Classify an agent for UI gating. Accepts a loose shape (like `isWorker` in
 * src/lib/api.ts) so it works on partial agent objects. Classification is
 * type-based only — the modern wire enum (`Subagent` / `subagent_3p`) encodes
 * the runner in the type, so `executor.kind` is never consulted and the legacy
 * `worker` constant is not a recognised kind.
 */
export function agentKindFlags(agent: {
  type?: string | null
  locked?: boolean | null
  executor?: { kind?: string | null } | null
}): AgentKindFlags {
  const type = agent.type
  const isWorkerKind = isWorkerType(type)
  const isExternal = isExternalType(type)
  return {
    isLocked: agent.locked === true,
    isWorker: isWorkerKind,
    isExternal,
    isNativeWorker: isWorkerKind && !isExternal,
    isSystem: isSystemType(type),
  }
}
