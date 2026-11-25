# S20 / FR-013 — exact real-loop test mapping

Original test-author reconciliation, 2026-11-24. Founder Q1 A chooses H48 B1: bash may start inside an operator-approved **write mount**. The workspace evidence repository remains protected. The implementing owner changes production; this reconciliation is test-only.

## Mandatory runtime leaf

Source: `/workspace/wt/a-1960-s1/pkg/agent/programs_git_https_push_test.go::TestProgramGitHelper_ResetChain/S20_no_token_in_config_or_arguments`.

From the exact integrated test worktree, execute this single leaf, serially:

```bash
/usr/local/bin/heavy env CGO_ENABLED=0 go test -tags goolm,stdjson,s1seams -p 1 -count=1 -v -run '^TestProgramGitHelper_ResetChain$/^S20_no_token_in_config_or_arguments$' ./pkg/agent/
```

The real loop and registered bash tool push a real prepared commit from the operator-approved project mount through the local verified-TLS smart-Git receiver. Assertions retain the exact destination ref/content, receiver account, one receive-pack and active config/global/XDG/remote/actual argv/trace2 readers. They do not replace a transfer with credential fill or a canned push result. Anonymous/wrong-password and Programs-off controls remain.

The same push from the workspace folder is refused by the evidence guard, without HTTP/account/pack/ref effects. The extra control is named **no write grant**, not read-only mount: ADR-063 D4 has no read-only Mount mode because reads are already open and every Mount is a write grant. The control creates a real readable project/symlink without any operator write grant, proves registered actual-loop read_file sees its prepared content, then requires bash cwd/push refusal and no receiver/ref effect. This is the chief-confirmed interpretation of the brief's read-only-mount wording, not an invented access-mode field or a stamped permissive policy.

## Original location is now only a loud selection check

`/workspace/wt/a-1960-s1/pkg/programs/env_modes_git_network_test.go::TestProgramGitHelper_ResetChain/S20_no_token_in_config_or_arguments` calls `/workspace/wt/a-1960-s1/pkg/programs/git_push_selection_test.go::requireS20RealLoopLeaf`.

It requires the actual agent source to be included in the Linux tagged build and to contain both exact test/scenario names. Positive and absent-top/absent-leaf/comment-only controls exercise its detector. Missing source, excluded build selection or missing exact names fail loudly. Its PASS means **static selection integrity only**: it never substitutes for executing the agent leaf. There is no empty successful wrapper or nested Go process.

This mapping and the original-marker replacement are committed together only after the actual fixed-target run was observed. The product transfer/account/config/URL/argv assertions were preserved, not weakened to match implementation output.

## RED then fixed-target observation

| Stage | Version | Evidence | Raw result |
|---|---|---|---|
| Fresh behavioural RED | Pre-fix integration `05ce8fbc230b7df7517d92b33990e8109eb3922b`, test-overlay tree `ed3ffb41e27cd3e474d9067f30ead676dcc18913` | `/workspace/logs/1960/s1-red/s20-q1a/S20-v1-red05ce8.log` |1/named FAIL: approved write mount rejected as cwd, after both refusal controls executed |
| Same runtime oracle on committed fix | Backend `cfcebae385381d4024380106b9a782fc6f5414d3`, overlay tree `f94962ebaa99fd6272bb26f5f981e13ed31233df` | `/workspace/logs/1960/s1-red/s20-q1a/S20-v1-targetcfce.log` |0/named PASS: full actual-loop transfer/ref/content/account/no-token assertions reached |

Saved logs contain exact commands, raw exits, backend pin, content-tree hash and actual Go1.27.4 linux/amd64. Separate receiver-only canned-helper instrument is explicitly not S20 product acceptance. Detailed current serial receipts and final source binding are in `/workspace/logs/1960/s1-red/s20-q1a/TRACEABILITY-20261124.md`.

These are original-author bounded observations, not independent CHECK. Independent GREEN verification, production mutation/Proof-of-failability and supported-platform/exact-cut UAT remain separate obligations. The Windows fixture remains loudly BLOCKED rather than silently skipped. Full CI is not run locally.

skills: omnipus-shared-rules, elicify-test-writing (retained).

| Claim | Evidence | Certainty |
|---|---|---|
| Exact original name now drives the real loop in agent | Actual source symbol above; RED1 then fixed-target0 named receipts | Verified bounded original-author observation |
| Original programs location cannot hide a missing runtime name | requireS20RealLoopLeaf actual tagged-file/name checks and independent detector controls | Static integrity only; runtime leaf remains mandatory |
| **Self-check** | Re-read original and successor assertions and the raw/named RED/fixed-target logs before marker replacement; unchanged transfer/account/config/argv oracle and both refusal controls retained; explicit mapping in the same test-only commit | Verified mapping; independent CHECK deferred |
