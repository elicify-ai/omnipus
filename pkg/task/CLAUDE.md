# pkg/task — work: tasks, goals, plans

One module, three packages: `pkg/task` (task store, runs, criteria, claims),
`pkg/goal` (goal records, phases, status), `pkg/plan`. This is the only
CLAUDE.md of the three — `pkg/goal` and `pkg/plan` carry no module-local rules
beyond their in-code headers, so this file points at them rather than padding
two near-empty twins.

## The canonical striped lock lives here

`lock.go::StripedLock` (with the process-wide `TaskFileLock`) is the canonical
shared mutex pool for file-store read-modify-write paths; `pkg/memory` and
`pkg/session` delegate to it rather than rolling their own. A new store that
needs sharded locking uses this one — a second pool defeats the sharing the
sharding exists for.

## Claim sentinels are control flow, not validation errors

`claim.go::ErrAlreadyClaimed` (and its siblings) mean "not in a dispatchable
state — already in progress, terminal, or claimed by a concurrent caller".
Handle them with `errors.Is` and map to the right wire status at the gateway;
treating them as input-validation failures misreports races as bad requests.

## normalizeCriteria must not mutate the caller's slice

`criterion_no_mutation_test.go` pins this after a real data race: the store
used to write server-set IDs through the caller's slice, racing goroutines
that shared a backing array, invisible to both CI race gates. Every call site
assigns the returned slice; nothing ever wanted in-place behaviour — do not
bring it back as an "optimisation".
