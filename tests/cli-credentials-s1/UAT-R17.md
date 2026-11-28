# S1 contention integration rows — R17 extension, 2026-11-22

Spec oracle: `/workspace/wt/a-1960-s1/docs/internal/specs/cli-credentials-s1-s2-spec.md` revision 8, S-136, S-146, H-17. This is a plan, not executed product evidence and not a replacement for the missing automated Unix CLI/gateway tests.

## Version, ownership and evidence

- Team-lead records the exact cut SHA, binary digest, embedded-SPA version and rebuild script/receipt for each standing instance before execution. HEAD of an author's worktree is not the cut receipt.
- Unix lane: tester account `s1-contention-unix-tester`, validator `s1-contention-unix-validator`. Windows lane: tester `s1-contention-win-tester`, validator `s1-contention-win-validator`. Separate instances, browsers, ephemeral homes and sessions; no shared-account cookies.
- Start tester/validator together through team-lead. Each uses a scripted model (no paid calls). Team-lead hands off tester evidence; testers and validators do not confer.
- Each screenshot shows the account/role badge (exact names above), workspace and row/step. CLI captures include the same lane/role label and version receipt. Never capture actual credentials; use only synthetic spec tokens and redact encrypted-store paths/values when sharing.
- Evidence root: `/workspace/logs/1960/uat/<cut-sha>/<unix|windows>/<tester|validator>/<row>/<step>-<screen|page|cli|receipt>`. Screenshots and redacted snapshots are paired per step; logs record real child exits, not SSH/wrapper status.

| Row | Acceptance / touched area | Steps | Expected result | Validator critical path |
|---|---|---|---|---|
| CLI-S136 | S-136; credentials CLI, transaction admission/read, gateway save, card metadata | One fresh home, real gateway and actual `omnipus credentials set OTHER y` binary; connect GitHub and record generation/receipt; hold CLI after in-lock read; paste replacement; verify pending + unchanged generation; release at one second; join both | Actual CLI exit 0; paste succeeds; exact new token/generation/metadata/receipt and OTHER preserved; no stale overwrite | Independently rebuild same cut; repeat held-read/paste/release; inspect disk with a new Store/process, not the tester's screenshot |
| CLI-S146 | S-146; ordinary CLI busy path and process-shared lock | Hold actual writer six seconds; run actual CLI Set; time raw child exit; release; wait two seconds; repeat with one-second release | Busy in 4.5–5.5 seconds, nonzero real CLI exit, exact busy message, no write then or two seconds after release; positive succeeds | Re-drive both six/one-second paths with distinct synthetic values and fresh state; verify raw exit and disk |
| API-S146 | S-146; gateway busy save/check admission, same- and cross-process holders | Connected GitHub with metadata/receipt; hold same-process then actual separate CLI writer; paste replacement under six-second holder; release/wait two seconds; repeat one-second positive | HTTP 503 + exact busy message in 4.5–5.5 seconds, old value/generation/receipt intact, zero check start and no late write; one-second case succeeds as generation 2 | Repeat both holder kinds and both durations; prove check-start counter positive before attack and unchanged after refused paste |

Select only rows whose touched area changes on a recut; re-drive on the exact new cut. Stable rows should become automated end-to-end tests when the test-build CLI barrier is available. No PASS is implied by this plan, fixture or rebuild receipt.

## Post-landing inside-product row

Because the train changes agent tools, after landing and the Mac's exact live update, team-lead asks the Omnipus Test Lab lead `omnipus-tester` to connect/check/disconnect a synthetic program through the supported person-only flow and verify an enabled agent sees the intended availability and value-free messages. The Lab records the landed version and files findings through chief. This post-landing lane is not a gate and never substitutes for exact-cut external lanes. Real lock fault injection belongs to the isolated lanes, not the founder's live credential store.
