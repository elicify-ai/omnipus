# WU-4b gateway guard fixture addendum — 2026-10-16

**Old → new test:** `TestWU3ProductionWiringGuardsProviderCheck` keeps the same name and expected outbound-request-guard behavior. Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/q3-native-checkout-e6d451be1/pkg/gateway/wu3_fix_provider_guard_wiring_test.go.

| Aspect | Before | D2-A translation |
|---|---|---|
| Credential setup | Plaintext `api_key` passed into configure_provider | Prior real CommitCard publication with literal provider/endpoint/protocol/field binding; exact returned version is passed as `api_key_ref` |
| Production boundary | Boot from shipped seed/ceiling; actual wireSystemTools and URLChecker | Unchanged, never mocked or replaced |
| Required outcome | `saved but not working`; reason contains `outbound-request guard`; no value echo | Unchanged |
| Network instrument | Reason text implied the guard stopped the call | Added process-edge transport counter: exactly zero outbound calls; a bypass cannot masquerade as a guard refusal or contact a real provider |
| Stored/selected state | Not separately checked | Prior metadata/value remain unchanged and readable; real list_providers confirms the supplied reference is the saved consumer selection |

No plaintext-refusal assertion is relaxed: the original direct plaintext RED and the primary 26-row matrix remain unchanged. This is a positive setup translation so a correct D2 refusal does not mask the older real wiring regression.

Prior implementation-checkpoint CI receipts are retained at /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/dev4/resume/4b-pr-old-checkpoint-failed.log and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/dev4/resume/4b-xplat-old-checkpoint-failed.log: regular gateway, race gateway and both Linux architecture runs show expected `saved but not working`, actual `failed: nothing saved`, at the old plaintext input. These raw named failures were read first-hand; no whole-CI verdict is inferred from them. Source base for the adapted control is e6d451be181053f1cfe3fb01874d3b6ab0bc0f62 (provider references implemented; native turn/registry Q3 not yet changed).

Execution status at initial publication: adapted named control was not yet executed. The initial dispatcher batch failed compilation in a separate new QA echo fixture (missing Scope), so that attempt is excluded. After the helper correction, the exact named adapted control PASSED, exit0, on frozen provider-fixed HEAD62994a9cebc24b23ede27c8ef4dcc347798715c0. Raw receipt: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-dispatcher/q3r2-row4.log. This author read the named PASS and direct exit0 first-hand; the successful run includes the unchanged guard/outcome assertions, real prior bound-reference selection, zero outbound-request counter and unchanged stored value/binding checks. This is one named control, not whole gateway, platform, race or independent CHECK verification. No production files, shims or policy changes.

| Claim | Evidence | Certainty |
|---|---|---|
| The old plaintext input masked the intended guard check | Named CI excerpts in both raw receipts above; old test body read before editing | Verified (high) |
| Original outcome/guard/no-echo assertions preserved; new fixture is bound and network-safe | Source diff of the named test; literal CardRecipient, real CommitCard, original wireSystemTools/URLChecker, exact zero-request counter; corrected named dispatcher receipt above PASS/exit0 | Verified source/runtime (high), one named control |
| Caller impact disclosed | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/wu4b-gateway-adaptation-impact.log, caller search exit0; GitNexus unavailable | Inferred (medium), direct search not graph analysis |
| **Self-check** | Read original body, original CI failure and corrected dispatcher named PASS/exit0; rechecked unchanged expected outcome/reason, production wiring and zero outbound-request assertion; no skip, swallowed failure or widened tolerance. Broader gates and independent mutation are not claimed | Verified source/one named control; broader verification Unknown |

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis
