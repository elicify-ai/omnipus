# WU-23 / #1284 RED plan

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md` §2, D1–D9 and T-INV/T-FT/T-L/T-M/T-W. This amendment overrides the older secret-set and God Mode oracles.

Behaviour: no native agent tool or native agent-spawned child can discover/read/write internal gateway files, in any permission mode, while the explicit workspace/shared/runtime/document allowances remain usable.

| Requirement | Cases and oracle |
|---|---|
| D1/D2 inventory and future paths | One file per named entry/prefix; unknown future top-level and nested paths denied, including nonexistent paths; workspace control allowed |
| D3 allow-list, D4 own agent home | Workspace, external mounted/shared folder, media read, acting/root uploads read, helper asset read; no writes to read-only roots; other uploads, instructions, other agents/workspaces and own internal children denied |
| D5 single predicate/derivation | App predicate, resolver, preflight and kernel deny-set compared against independent inventory rows; no mounts/patterns/grants reopening inventory |
| D6 file tools and grep | Real read/write/append/edit/list and grep paths, normal and God policy contexts; direct paths, directory targets, workspace symlinks/hardlinks; exact unchanged target bytes after denial; workspace control; registry-driven path-tool coverage guard |
| D6 command check | Explicit read/list/write/delete operands refused even with restrict=false and God Mode; exact workspace positive; read-only no-sandbox list excludes protected operands; labelled dynamic-path residual on Windows |
| D7 Floor / three spawn sites | Missing Floor and AllowRoots seams fail BLOCKED, not compilation or skip; actual God Mode foreground/background/setup children probe protected content and free external writes; floor network remains open |
| T-L/T-M kernel | Real child per platform with same-child workspace controls; read/list/create/write/rename/unlink probes for every inventory class; agent-home protected children; runtime/document rx and cache rw; Linux post-spawn denied; macOS post-spawn residual readable |
| T-INV classification drift | Fresh bootstrap + agent/workspace/session/task records; every top-level entry explicitly classified; injected unclassified name trips classifier; independent future-path default-deny test |

REAL: production FSPolicy, ResolvePath/PathHandle, tool Execute, grep, preflight, DeriveKernelPolicy, spawn/hardening, kernel enforcement. No mock filesystem-policy functions. Setup target is a process-edge fixture only. Linux tests never confine the main test process: isolated child locked to its OS thread. All data rooted in temporary HOME/OMNIPUS_HOME; sentinel bytes are not credentials. No subagent_3p protection tests: accepted D9 exclusion.

Missing proposed symbols are tested with reflection against existing types where feasible; no production API invented. Narrow runs only, serial through slotrun. Linux execution and native Windows receipts belong to chief/CI; this lane vets both cross targets and executes real Seatbelt here. Platform filters/workflow changes belong to backend-lead (production/config ownership), reported explicitly in handoff. GREEN, at least three implementation mutations, reverted-mutation green and full-suite checklist are deferred to CHECK/CI, not claimed by RED.
