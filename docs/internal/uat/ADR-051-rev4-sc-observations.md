# ADR-051 Rev 4 — Per-SC Observation Log

**Date:** 2026-07-23
**Evidence Source:** CI run 30029156405 (22/22 passed) + integration tests + reviewer gate reports
**Operator/Agent:** Daniel Piatkowski / orchestrator

---

## SC-001: 100% of uploads across all formats return HTTP 200 — zero format-rejection errors
- **Verification:** TestWorkspaceLibrary_Store_AnyFormat_Succeeds iterates all 11 matrix formats (PNG, JPEG, WebP, BMP, TIFF, GIF, SVG, AVIF, HEIC, ICO, PDF) and asserts HTTP 200 / no format-rejection error
- **File path evidence:** pkg/media/library/library_test.go + CI Tests job (22/22 passed)
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-002: 100% of sha256 mismatches are detected on read and route to step 7 — zero corrupt bytes reaching image.Decode
- **Verification:** TestWorkspaceLibrary_Read_VerifiesSHA256_TamperDetected asserts ErrIntegrityCheckFailed on tampered file. FR-020a + FR-023a ensure corrupt bytes never reach decode.
- **File path evidence:** pkg/media/library/library_test.go + CI Tests job
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-003: 0 dead turns caused by media rejections — every format×model combination produces a useful result
- **Verification:** TestPresentation_Step1Gate_TextOnlyModel_RoutesToOffload, TestPresentation_Step1Gate_VisionModel_Proceeds, TestStep4_OutcomeRelabel_OnSuccessfulRetry, TestPresentation_Step5Offload_CopiesToWorkDir_InjectsPath — all PASS. Full capability gate (Catalog.Resolve) + outcome-based retry + step-5 offload chain verified.
- **File path evidence:** pkg/agent/loop_media_present_test.go, pkg/agent/media_outcome_retry_test.go
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-004: The DecodeConfig pre-flight catches 100% of images exceeding maxImagePixels — zero OOM events from decode-bombs
- **Verification:** TestEncodeImageToDataURL_DecodeConfigGuard_PixelBomb asserts 16MP pixel guard fires before image.Decode. Enforced at loop_media.go:481.
- **File path evidence:** pkg/agent/loop_media_resize_test.go
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-005: Every provider 4xx in the exclusion set does NOT trigger strip-retry — zero non-media errors masked
- **Verification:** TestClassifier_StillReturnsProviderRejected_ForKnownRejections (12 known-shape cases), TestOutcomeFallback_AcceptsCodeUnknown_Only (11 codes). outcomeFallbackEligible gate accepts ONLY CodeUnknown.
- **File path evidence:** pkg/agent/media_outcome_retry_test.go
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-006: Every step-5 offload injects a filesystem path under workspaces/<ws>/work/ — zero media:// refs injected
- **Verification:** TestPresentation_Step5Offload_CopiesToWorkDir_InjectsPath asserts offload copies to work/ dir with sha256-derived safe name. FR-020a enforces filepath.Clean/Join containment.
- **File path evidence:** pkg/agent/loop_media_offload_test.go
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-007: Agent-generated media (tool:inline:session:) appears in 0 workspace library directories
- **Verification:** TestAgentMedia_StaysSessionScoped_NotInWorkspaceLibrary asserts screenshot/chart media stays in session scope. FR-005 enforces two-mechanism split.
- **File path evidence:** integration test + loop_media_test.go
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-008: Legacy media://<uuid> refs from pre-Rev4 sessions resolve via the global registry fallback — zero regressions
- **Verification:** TestResolver_LegacyCallSites_UnaffectedByNilableContextParam, TestResolver_LegacyRefFallback_AfterNamespaceSplit. The new resolver tries workspace first, then legacy global (FR-028).
- **File path evidence:** pkg/media/resolve_test.go
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-009: Capability catalog pull failure results in 0 gateway boot failures — last-known-good retained
- **Verification:** TestCatalog_Refresh_PullFailure_RetainsLastKnownGood, TestCatalog_Refresh_NonFatalOnPullFailure. FR-025 mandates non-fatal with last-known-good retention.
- **File path evidence:** pkg/providers/capabilities/catalog_test.go
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z

## SC-010: Behavioral invariants of resolveMediaRefs/TryMediaDowngrade preserved; existing tests updated to assert same observable outcome via orchestrator
- **Verification:** CI Tests job (22/22 passed) confirms all existing tests in svg_raster_test.go, loop_media_normalization_test.go, loop_media_test.go, runturn_redo_test.go, translate_error_test.go pass without regression. See CI evidence run 30029156405.
- **File path evidence:** docs/internal/uat/ADR-051-rev4-ci-evidence.md
- **Result:** PASS
- **Timestamp:** 2026-07-23T16:01Z