# pkg/workspace — workspace records, team, delegation, mounts

## Delegation trust is workspace-scoped (ADR-037)

`delegation.go`'s per-workspace `Delegation[]` edge list is the SOLE runtime
authority for who may delegate to whom. The global Delegation Graph and
`AgentConfig.DelegationPolicy` are deleted. Delegation is configured
exclusively through a workspace's own Team tab — do not add a global trust
surface back. The same agent sits on several workspaces with different
rosters and different trust in each; a global flag cannot express that, which
is why the deleted screen saved fine while enforcing nothing.

## DelegationMode here is deliberately NOT config.DelegationMode

`delegation.go::DelegationMode` is the edge's typed mode vocabulary, collapsed
to `direct | task`: an edge that authorises direct delegation authorises both
the synchronous and the background call pattern, so the edge no longer
distinguishes them. `config.DelegationMode` (`pkg/config`) keeps its 3 values
(Await / Background / Task) because it describes the delegate tool's real
per-call runtime parameter. They look unify-able and are not:
`loop_delegation.go::EdgeModeCategory` maps the tool's 3 values down to the
edge's 2 at the enforcement gate, and `loop_env.go::wireDelegationInjectors`
expands back to 3 when advertising modes in the system prompt. Merging the
types re-couples standing trust to a per-call flag.
