// AgentSkillsGroup — the Skills group of an agent's Tools tab (Skills &
// Tools C-4, FR-024, S-22, S-24): each granted skill with Allow / Ask / Deny.
// An agent can only be stricter than the global value (strictest wins), so a
// looser choice is disabled and says where the global value is changed.
// Omnipus takes no per-agent Ask (founder R2).

import { PolicyBadge, type ToolPolicy } from '@/components/shared/PolicyBadge'
import type { AgentSkillEntry } from '@/lib/api/generated/openapi-types'
import { OMNIPUS_AGENT_ID } from '@/lib/omnipus'

const RANK: Record<ToolPolicy, number> = { allow: 0, ask: 1, deny: 2 }
const POLICIES: ToolPolicy[] = ['allow', 'ask', 'deny']

export const NO_SKILLS_SENTENCE = "No skills granted. Grant skills on this agent's Skills tab."
export const OMNIPUS_NO_ASK_SENTENCE = 'Omnipus takes no per-agent Ask; set Ask globally or use Deny.'

function lockSentence(global: ToolPolicy): string {
  return global === 'ask'
    ? 'The global setting is Ask. Set it to Allow on Skills & Tools first.'
    : 'The global setting is Deny. Change it on Skills & Tools first.'
}

interface AgentSkillsGroupProps {
  agentId: string
  skills: readonly AgentSkillEntry[]
  /** The agent's own overrides after local edits; a key not listed follows the global value. */
  overrides: Record<string, ToolPolicy>
  /** God Mode lifts a global Ask (never a Deny) for the display and the locks. */
  godMode: boolean
  disabled: boolean
  onChange: (key: string, policy: ToolPolicy) => void
}

export function AgentSkillsGroup({ agentId, skills, overrides, godMode, disabled, onChange }: AgentSkillsGroupProps) {
  const isOmnipus = agentId === OMNIPUS_AGENT_ID
  return (
    <section data-testid="skills-group" aria-labelledby="agent-skills-group-title" className="space-y-[var(--space-2)]">
      <p id="agent-skills-group-title" className="text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-muted)]">Skills</p>
      {skills.length === 0 ? (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{NO_SKILLS_SENTENCE}</p>
      ) : (
        skills.map((skill) => {
          const global: ToolPolicy = godMode && skill.global_policy === 'ask' ? 'allow' : skill.global_policy
          const own = overrides[skill.key] ?? skill.configured_policy ?? undefined
          const shown = own && RANK[own] > RANK[global] ? own : global
          const lockId = `skill-lock-${skill.key}`
          const omnipusAskId = `skill-omnipus-ask-${skill.key}`
          return (
            <div key={skill.key} data-testid={`agent-skill-row-${skill.key}`} className="flex items-center justify-between gap-[var(--space-2)] py-[var(--space-1)]">
              <span className="min-w-0 flex-1 truncate font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]" title={skill.key}>
                {skill.display_name || skill.name}
              </span>
              <div className="flex shrink-0 items-center gap-[var(--space-1)]">
                {POLICIES.map((policy) => {
                  const locked = RANK[policy] < RANK[global]
                  const omnipusAsk = isOmnipus && policy === 'ask'
                  return (
                    <PolicyBadge
                      key={policy}
                      policy={policy}
                      active={shown === policy}
                      disabled={disabled || locked || omnipusAsk}
                      describedBy={omnipusAsk ? omnipusAskId : locked ? lockId : undefined}
                      onClick={() => onChange(skill.key, policy)}
                    />
                  )
                })}
                {global !== 'allow' && <span id={lockId} className="sr-only">{lockSentence(global)}</span>}
                {isOmnipus && <span id={omnipusAskId} className="sr-only">{OMNIPUS_NO_ASK_SENTENCE}</span>}
              </div>
            </div>
          )
        })
      )}
    </section>
  )
}
