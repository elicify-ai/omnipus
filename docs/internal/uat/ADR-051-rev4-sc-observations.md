# ADR-051 Rev 4 — Per-SC Observation Log

**Date:** 2026-07-23  
**Evidence Source:** CI runs (see `ADR-051-rev4-ci-evidence.md`) + integration/unit tests + live gateway UAT (`ADR-051-rev4-uat-deviations.md`)  
**Operator/Agent:** Daniel Piatkowski / orchestrator  

---

## SC-001: 100% of uploads across all formats return HTTP 200 — zero format-rejection errors
- **Verification:** Live UAT uploaded PNG, SVG, AVIF, PDF, traversal-named PNG, injection-named PNG — all HTTP 201. Unit: `TestWorkspaceLibrary_Store_AnyFormat_Succeeds` (11 matrix formats).
- **File path evidence:** `/tmp/adr051-uat-final/` upload responses; `pkg/media/library/library_test.go`; CI Tests job
- **Result:** PASS
- **Timestamp:** 2026-07-23T21:43Z

## SC-002: 100% of sha256 mismatches are detected on read and route to step 7 — zero corrupt bytes reaching image.Decode
- **Verification:** Live UAT-015 tampered on-disk bytes; gateway log `SHA-256 integrity check failed for workspace ref` with expected≠actual; turn survived without decode. Unit: `TestWorkspaceLibrary_Read_VerifiesSHA256_TamperDetected`, `TestVerifyFileIntegrity_Mismatch`.
- **File path evidence:** `/tmp/omnipus-gateway-uat.log`; `UAT-015.json`; `pkg/media/library/library_test.go`; `pkg/agent/loop_media_integration_seams_test.go`
- **Result:** PASS
- **Timestamp:** 2026-07-23T21:44Z

## SC-003: 0 dead turns caused by media rejections — every format×model combination produces a useful result
- **Verification:** Live UAT-001..005, 004 text-only, 003 AVIF all `done=true` with no WS error frame. UAT-004 log shows outcome-fallback strip-retry then completion. Units: `TestPresentation_Step1Gate_*`, `TestStep4_OutcomeRelabel_*`, `TestPresentation_Step5Offload_*`.
- **File path evidence:** `/tmp/adr051-uat-final/UAT-00*.json`; `pkg/agent/loop_media_present_test.go`; `pkg/agent/media_outcome_retry_test.go`
- **Result:** PASS
- **Timestamp:** 2026-07-23T21:44Z

## SC-004: The DecodeConfig pre-flight catches 100% of images exceeding maxImagePixels — zero OOM events from decode-bombs
- **Verification:** `TestEncodeImageToDataURL_DecodeConfigGuard_PixelBomb` asserts 16MP guard before `image.Decode`.
- **File path evidence:** `pkg/agent/loop_media_resize_test.go`; CI Tests job
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z (unit); reconfirmed present in HEAD

## SC-005: Every provider 4xx in the exclusion set does NOT trigger strip-retry — zero non-media errors masked
- **Verification:** `TestClassifier_StillReturnsProviderRejected_ForKnownRejections`, `TestOutcomeFallback_AcceptsCodeUnknown_Only` — outcome fallback only on `CodeUnknown`.
- **File path evidence:** `pkg/agent/media_outcome_retry_test.go`; CI Tests job
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-006: Every step-5 offload injects a filesystem path under workspaces/<ws>/work/ — zero media:// refs injected
- **Verification:** `TestPresentation_Step5Offload_CopiesToWorkDir_InjectsPath`; live UAT-003/004 produce guidance without injecting `media://` into model-visible path form for undecodable/offloaded cases.
- **File path evidence:** `pkg/agent/loop_media_offload_test.go`
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-007: Agent-generated media (tool:inline:session:) appears in 0 workspace library lists
- **Verification:** `TestAgentMedia_StaysSessionScoped_NotInWorkspaceLibrary`; live library list after chat shows only `source=user_upload` entries.
- **File path evidence:** library list JSON from UAT; unit tests
- **Result:** PASS
- **Timestamp:** 2026-07-23T21:43Z

## SC-008: Legacy media://<uuid> refs from pre-Rev4 sessions resolve via the global registry fallback — zero regressions
- **Verification:** `TestResolver_LegacyCallSites_UnaffectedByNilableContextParam`, `TestResolver_LegacyRefFallback_AfterNamespaceSplit`.
- **File path evidence:** `pkg/media/resolve_test.go`; CI Tests job
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-009: Capability catalog pull failure results in 0 gateway boot failures — last-known-good retained
- **Verification:** `TestCatalog_Refresh_PullFailure_RetainsLastKnownGood`, `TestCatalog_Refresh_NonFatalOnPullFailure`. Live gateway boots with embedded seed when puller idle.
- **File path evidence:** `pkg/providers/capabilities/catalog_test.go`; gateway boot log
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-010: Behavioral invariants of resolveMediaRefs/TryMediaDowngrade preserved; existing tests updated to assert same observable outcome via orchestrator
- **Verification:** CI Tests green on post-integration commits (`44da649e`, `5f93fb0a`); existing media test packages pass under orchestrator.
- **File path evidence:** `docs/internal/uat/ADR-051-rev4-ci-evidence.md`; CI run URLs therein
- **Result:** PASS
- **Timestamp:** 2026-07-23T21:30Z

---

## Live UAT cross-check (post D1/D2 fixes)

| Live scenario | SC linkage | Result |
|---|---|---|
| UAT-001 PNG vision → "Blue" | SC-001, SC-003 | PASS |
| UAT-002 SVG → "blue circle" | SC-003 | PASS |
| UAT-003 AVIF survives | SC-003, SC-006 | PASS |
| UAT-004 text-only + outcome-fallback | SC-003, SC-005 | PASS |
| UAT-009 delete | SC-001 | PASS |
| UAT-015 tamper integrity | SC-002 | PASS |

(End of file)
