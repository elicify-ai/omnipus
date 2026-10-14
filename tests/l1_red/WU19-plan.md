# WU-19 RED plan

Behaviour: every gateway child starts from the generic child-environment allowlist, plus explicit site additions; external-program workers keep the runner-specific credential path.

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-red-s1/docs/internal/specs/omnipus-system-agent-spec.md` §15 WU-19, FR-239, §6.1 and §6.6 environment row; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-red-s1/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md` dated Opus-review note “Gateway environment”.

| Case | Inputs | Independent expectation |
|---|---|---|
| Hook, browser, git, proxy | Innocuous fixture values under gateway/provider/mailbox/unknown keys | No such key in the actual child's snapshot (FR-239) |
| Hook/browser options empty | No explicit environment additions | Still scrubbed, not inherited (FR-239) |
| Hook/browser additions | One explicit site option | Exact option retained (FR-239) |
| All children positive control | Temporary HOME, PATH, OMNIPUS_CHILD_WU19_CONTROL | Exact values retained; child execution proven |
| Git/proxy site additions | Noninteractive git settings / proxy addresses | Exact site additions retained |
| Inventory | Parse all non-test production Go files, all platforms | Unsafe raw-parent assignments and unassigned child environments produce findings (FR-239) |
| Inventory instrument | Synthetic omitted, nil, parent-copy, indirect-copy and conditional-only environment assignments | All produce findings; unconditional scrub + explicit append does not |

REAL: production hook handshake, browser launch.start, runGit, ExecProxy.PrepareCmd and child processes. The browser process edge is a test executable instead of Chromium; only launch environment is under test. Fixture snapshots include only named test keys, never the full environment. No credentials or actual data-home access. Filesystem/test binary and local loopback proxy are process edges, not mocked policy.

No existing symbols edited. GitNexus MCP unavailable and checkout has no .gitnexus runner. Caller search was used; impact is inferred to be new test symbols only.

Planned CHECK mutations (deferred, not executed by RED): omit cmd.Env; replace scrubbed base with os.Environ; discard site additions. Full suite, GREEN and proof-of-failability mutation checklist deferred to CHECK/CI. Browser pipe launch test is build-tagged Linux/macOS; Windows receives inventory and native hook/git/proxy test compilation. The source inventory is a structural guard, not a substitute for the four behavioral child probes; source-flow inference is conservative and unresolved helper paths are reported rather than asserted safe.
