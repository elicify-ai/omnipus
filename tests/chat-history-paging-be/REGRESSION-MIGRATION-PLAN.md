# Existing Go regression carrier migration — plan before assertions

## Outcome and authority

Preserve every existing content, identity, provenance, special-payload, schema, isolation and privacy oracle while migrating only the intentionally replaced history carriers. Real handlers/store/replay remain real; no compatibility response, fake reader, skipped assertion or array-to-empty fallback is introduced.

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md`::D3/D7a/D10/D15/Regression Test Requirements, plus the explicit user batch and team-lead Q2 decision. The current pre-implementation RED base is `dab7aa61d0dfdd972e58d6ed594068055f822c0f`; do not merge GREEN implementation to make a RED result disappear.

## Unit boundary and independent expectations

| Cases | Preserved oracle / replacement carrier | Source |
|---|---|---|
| Last clear, REST/detail/snapshot | Exactly marker,new-q; old question/answer absent, new question present; earlier=true with next_before at marker; no later continuation | Existing clear assertions; D3/D10; clear regression entry |
| No clear / empty detail / snapshot | Exactly q where seeded; earlier=false/next_before absent; no legacy flag; valid available page metadata, not default-zero fields from absent metadata | Existing assertions; D10/D15 |
| Explicit crossing before marker | Exactly old-q,old-a,clear-row; older mode, terminal earlier=false, retained later=true; marker/new-q not repeated | D3 exclusive before/closed clear; required added crossing |
| Delegated-child detail | Same single child content/id/optional parent id; generated available SessionDetail with accurate page fields | Existing drill-down/unfiltered assertions; D10/D15 |
| Delegate provenance on messages | **Pending architect ruling; leave the complete original test unchanged.** Its wire parent_spawn_call_id assertion cannot be silently dropped or asserted through an uncontracted raw field. Known Message control search0; field search1 in Message.yaml/generated Go. | Team-lead Q1 hold; Hard Constraint8 |
| Verdict/malformed verdict, tool/interrupted/cancel/truncation/disposition/projection | Keep every original per-entry value and absence/schema assertion. Decode generated envelope first; retain raw nested entry bytes/maps only for the existing actual-wire schema/omission assertions, never as a hand-written replacement DTO. | Existing tests; D4/D10/D15 |
| Q33 classified notice | Same type, complete notice, id/timestamp/author/turn and both kind classes; only wrap generated Message[] in the new generated envelope | Existing ADR-066/Q33 oracles; D10 |
| Rename persistence | Exact persisted title remains; parse generated available detail instead of a hand-written detail struct | Existing rename oracle; D10 |
| Authorized unreadable transcript | Metadata-only generated detail200 with same authorized session; unavailable state and no messages/page_info/flags/cursors. Physical paths/raw error details remain banned globally; ws/mia/transcript remain banned from the sanitized history message. Messages still fixed500. | D7a/D10/D15; explicit team-lead Q2 A |
| Unauthorized unreadable transcript | Existing access refusal/no identity, no metadata-only bypass | D7a access first; explicit team-lead Q2 control |

Defaults are limit40/min_rows20/soft budget4194304; every fixture in this migration is smaller than the page cap. Context arrays are non-null, retention generation nonempty, positions opaque47 characters and continuation iff matching actual retained neighbor. Available/unavailable field sets are mutually exclusive. These values come from the spec/generated canonical contracts, not observed output.

## Per-file inspection boundary

| File | Initial disposition |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/clear_view_test.go` | Migrate all six old carrier checks; retain exact strings/IDs and legacy-field absence assertions; add explicit crossing. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/rest_adr057_test.go` | Migrate only real child detail wire decoder; lists/delete/static filter checks retain meaning. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/rest_delegate_child_filter_test.go` | Unchanged: canonical seed helper only, no HTTP response decoder or legacy field read. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/rest_gw_gate_test.go` | Unchanged: inspected message/detail accesses assert status/access/isolation, not old bare-array/header/detail flag. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/rest_sessions_test.go` | Migrate old message/detail decoders and preserve raw per-message schema/value checks; hold the one uncontracted wire provenance test pending architect. List/control metadata-only map checks stay unless they actually depend on the old shape. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/sessioncore_sf1_session_leak_test.go` | Q2 authorized-unavailable detail/security migration and added unauthorized control; other storage/metadata faults retain old refusal/privacy rules. Requires security-lead oracle confirmation before acceptance. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/sessions_wire_test.go` | Unchanged: Message schema/YAML loader helpers only; no old history response decoder. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/context_window_notice_replay_q33_test.go` | Migrate REST envelope only; durable fixture, complete payload/identity and separate replay test assertions stay. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/projection_frame_test.go` | Migrate only REST reload decoder; both exact projected/kept tool values and the independent live-frame isolation assertions stay. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/websocket_discarded_input_test.go` | Migrate only REST disposition carrier; preserve complete discarded map, retained text, omission and every Message schema assertion. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/api/generated/contract_test.go` | Initially check-only; team-lead explicitly expanded scope to its single stale cap test after the reported finding. Extract/strengthen it under the same name: valid100, specific messages/maxItems errors at101 and retained100001, independent in-memory no-cap fixture control. No production schema/generated-type edit; actual production-cap mutation remains fresh CHECK. Details: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/tests/chat-history-paging-be/SESSION-DETAIL-CAP-PLAN.md`. |

## Impact, proof and deliberate gaps

GitNexus tools are unavailable. Run direct caller sweeps for each existing helper/test symbol before editing and report impact as Inferred. No production symbol/schema/generated-type changes belong to QA. The existing messageIDs helper has exactly three current call sites, all in clear_view_test.go; its carrier type may change with those callers, not through a blind rename.

Freeze the batch commit before exact tagged/CGO0 serial slotrun RED proofs. Every touched behavioral test gets an exact named run with its raw failure, code SHA and full log; a parse/required-new-metadata failure on the old producer is the intended RED, never a compile/no-match failure. Keep the full old-assertion→new-assertion mapping in the handoff and identify held/unchanged files individually.

Independent CHECK must mutate: restore the old array/header reader; drop the clear marker/content or cross inclusively; strip verdict/notice/disposition/projection/truncation payload; emit a fake empty available detail on storage failure; or leak a path/unauthorized identity. This RED instance does not run implementation mutations, GREEN or self-CHECK. Security-lead confirmation of the changed SF1 privacy oracle and any architect provenance decision remain explicit gates, not assumed approvals.
