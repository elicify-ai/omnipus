# D16 backend RED execution evidence

## T38 — recorded session usage (FR-027, B55/B56)

Production input: normal merge093ac7a6af2c45b20c936f87e3d3c8cf65d98cd5, containing published contract be95728784b792b343027e1253ad3c4c3abcc324. QA changes only tests/support and this plan/evidence. No production mutation or fix.

All commands below ran serially in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be through /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh, with `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -p 1` against the single gateway package. These are narrow local nonrace runs, not full-suite, Linux/kernel, browser or CI evidence.

| Exact subtest under TestSessionTokens_ReloadAbsoluteTotalParity | Saved raw exit / actual failure | Absolute artifact |
|---|---|---|
| recorded_provider_usage_survives_fresh_REST_detail_and_snapshot | 1; missing known SessionTokensTotal on first/second live done, bounded REST and scoped attach/retry. Real1200+45123 accounting/stats reopen, exact40 rows, selected-page sum45123 and existing detail total46323 succeeded before/alongside the new-field failures. | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/legacy-qa-20261022/d16-t38-streaming-fixture.log |
| held_done_and_snapshot_share_absolute_authority | 1; missing known absolute on actual current state, third live done, held live done and subsequent REST. Real third delta700/stored47023, one held done after catch_up_complete with seq>W and provider count3 succeeded. | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/legacy-qa-20261022/d16-t38-remaining-red.log |
| stale_page_does_not_change_newer_accounting | 1; missing absolute46323 on the actually prepared stale page and47023 on newer live/REST/retry. Real outward response hold/release/join and saved47023 accounting succeeded. This tests server authority/carriers, not frontend stale-response application. | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/legacy-qa-20261022/d16-t38-remaining-red.log |
| real_zero_unknown_and_other_session_are_distinct | 1; omitted known46323, genuinely unused0 and known attach baseline. A fresh real empty session is the0 control; removing only a previously existing historical aggregate makes the separate historical session unknown. Unknown omission/isolation and no model work remain asserted. | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/legacy-qa-20261022/d16-t38-zero-live-only-red.log |
| known_usage_is_preserved_in_live_only_state | 1; omitted known46323 on actual authorized live_only state and healthy Retry. Real required-storage fault produced the existing typed unavailable/no-wipe/no-done/live_only lifecycle; cached saved usage and provider count2 stayed intact. | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/legacy-qa-20261022/d16-t38-zero-live-only-red.log |

Selectors were respectively `^TestSessionTokens_ReloadAbsoluteTotalParity$/^recorded_provider_usage_survives_fresh_REST_detail_and_snapshot$`, `^TestSessionTokens_ReloadAbsoluteTotalParity$/^(held_done_and_snapshot_share_absolute_authority|stale_page_does_not_change_newer_accounting|real_zero_unknown_and_other_session_are_distinct)$`, and `^TestSessionTokens_ReloadAbsoluteTotalParity$/^(real_zero_unknown_and_other_session_are_distinct|known_usage_is_preserved_in_live_only_state)$`.

Excluded setup failures are preserved, not silently counted as RED: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/legacy-qa-20261022/d16-t38-first.log (exit1, nonstreaming external-provider fixture caused socket timeout; corrected only the external fixture interface). The first zero case in the remaining-red artifact incorrectly required a lazily created stats file for an unused session; corrected to actual empty-session metadata/history checks and observed the required-field failure in the final artifact. Neither timeout nor file-setup failure is behavioral proof.

T38 named behavioral RED: **5/5 subcases**, across the source-qualified narrow receipts above. This is not GREEN. Step4 items2/3, mutation and Proof-of-failability checklist are **deferred to fresh CHECK**. Existing detached reconciliation CI remains pinned to bfd63fecfa9d5edb4a95c379b0a1fc0b21fc5ba0 and cannot cover D16.

## T37 — cancellation durability (FR-026, B52–B54)

Planned in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/tests/chat-history-paging-be/D16-RED-PLAN.md. Not yet authored/executed at this incremental T38 commit; no T37 acceptance or RED claim.
