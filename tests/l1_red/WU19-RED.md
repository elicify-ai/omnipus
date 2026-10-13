# WU-19 RED handoff

Correction: the first hook control compared HOME with the parent's fixture HOME. That was wrong because the agent package's TestMain replaces HOME in every child. The corrected control asserts exact USER, PATH and opt-in marker values; HOME and OMNIPUS_HOME remain temporary. The corrected run proves leakage with both controls passing.

Five top-level tests observed red against production baseline 8754a1a4aae309dff4484537d20d5089237540e2. Four behavioral probes start real children through the production spawn path. Browser substitutes the executable only. Fixtures contain no real secrets. All narrow runs use the shared slot runner, CGO_ENABLED=0, goolm,stdjson, -count=1, -v, -p 1 and one anchored test name. No full local suite. No production edits.

The inventory finds 30 production constructors. It catches raw/omitted/conditional assignments and follows local identifier copies; its synthetic unsafe cases and scrubbed positive pass. It is not an interprocedural proof. Explicit helper/parameter handoffs remain subject to behavioral review. Inventory findings outside the four confirmed probes are **not** all confirmed vulnerabilities: some nested assignments/helper handoffs need classification, and CLI daemon/launcher ownership must be checked by the implementing lead. No source guard may be used as a substitute for real-child evidence.

| Claim | Evidence | Certainty |
|---|---|---|
| Browser leaks denied fixture keys, empty and nonempty additions; controls pass | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu19/browser-red.log`; anchored TestWU19_BrowserChildEnvironment command; exit=1; OPENROUTER_API_KEY reached browser child; 2 positive_control PASS | Verified |
| Hook leaks denied fixture keys, empty and nonempty additions; controls pass | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu19/hook-red-final.log`; anchored TestWU19_ProcessHookChildEnvironment command; exit=1; gateway-only key reached hook child; 2 positive_control PASS | Verified |
| Worktree git leaks denied fixture keys; controls pass | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu19/git-red.log`; anchored TestWU19_WorktreeGitChildEnvironment command; exit=1; gateway-only key reached git child; positive_control PASS | Verified |
| Proxy nil environment leaks denied fixture keys; preseeded environment is a passing control | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu19/proxy-red.log`; anchored TestWU19_ExecProxyChildEnvironment command; exit=1; preseeded_false FAIL, preseeded_true PASS | Verified |
| Inventory observes unsafe constructors, its instrument works | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu19/inventory-red-final.log`; anchored TestWU19_SpawnSiteInventory command; exit=1; inventoried 30; instrument PASS | Verified |
| Linux/Windows vet of the five touched packages passes | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu19/linux-vet.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu19/windows-vet.log`; GOOS=linux/windows CGO_ENABLED=0 go vet -tags goolm,stdjson -p 1; exit=0 each | Verified |
| New tests only; graph analysis unavailable | Caller search + Git status; no existing production symbol edited, no GitNexus MCP or checkout index runner | Inferred (scope from diff, not graph) |
| GREEN, mutation proof, full CI | Deferred to GREEN/CHECK and CI; author never CHECKs its own suite | Unknown |
| Self-check | Final logs re-read; corrected hook and inventory re-run red with controls green; final formatting and scoped diff checked before commit | Verified |

skills: omnipus-shared-rules, elicify-test-writing
