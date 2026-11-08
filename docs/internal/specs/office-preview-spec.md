# Feature Specification: Built-in office-document preview (`preview_document`)

Status: Draft

| Field | Value |
|---|---|
| Created / revised | 2026-10-30 (architect, plan-spec) / fix round 1 (grill 1: BLOCK 1C/10M/4m) / RQ update (native pdfium program, gpt-6-luna row) / **final correction after grill 2 (BLOCK 1C/12M/2m/2obs) — no third grill** / **2026-10-31 architect amendment after the O3 Linux measurement (§3.1): Linux memory limit = data limit, launcher order unchanged, LibreOffice exit-81 retry, shared pdfium library (D1 A)** / **2026-11-05 architect amendment: S2 = A (§3.2 rewritten: write access to a program folder is refused where it is created), SR1 (§3.1 D3.7 anchored scratch operations; §3.3 deferred findings), O8 (T-91 manual Linux limits job)** / **2026-11-07 architect amendment: founder Q18, Q19, Q22, Q23, Q24, Q26 (§3.4)** / **2026-11-08 architect ruling D8b (§3.4), accepted by team-lead; correction: D8 said the first macOS preview on a fresh home may work, which was wrong because it looked only at the `<home>/tmp` deny: with an operator-registered LibreOffice the converter cannot read its registry under `<home>/system`, so every such preview failed at its first start** / **2026-11-08 architect amendment D8c (§3.4), founder Q35 = A, Q36 = A: the converter gets its own fixed read list and reads its registry in place; D8b.1 and D8b.2 are withdrawn; correction: six D8b statements were wrong, listed in §3.4 D8c** |
| Size | Feature (tools, agent media path, providers, sandbox, packaging, chat UI, skills) → RED/GREEN/CHECK, 5-reviewer gate, hands-on UAT (touches `pkg/agent`) |
| Baseline | `origin/release/v0.1.1` at `4cad412bf` (read-only); grill 2 used `b7d4483c9`. backend-lead re-checks every cited symbol on the planning commit |
| Supersedes | T11 spec (`landing/t11:docs/internal/specs/t11-documents-and-3p-agents-spec-full.md`) §2.4: the on-hold `convert_document` design (#1489) is retired, not built |
| Issues | #1489 (re-scoped here); #1506 (Omnipus signing: gates the trimmed macOS LibreOffice and the renderer's signing) |
| Previous versions | `.bak-round1` (before fix round 1), `.bak-rq` (before RQ update), `.bak-round2` (before this final correction); commit `a0ad39455` (before the 2026-11-05 amendment); commit `1c2ca4870` (before the 2026-11-07 amendment); commit `46f269277` (before the Q26 addition); commit `a4197c650` (before D8b); commit `b9802d19f` (before D8c) |
| Governing ADR | **Built-in agent configuration, skills, and visual file reading** (ADR-090) — dated amendment W0 |

## 0. Bottom line

Agents keep creating Office files and PDFs with the skills' Python libraries; what they lack is a reliable way to **see** the result. One new tool, `preview_document`, shows a `.docx`, `.xlsx`, `.pptx` or `.pdf` to the model in the best form the model reads: **the PDF itself** for models that read PDFs natively, **page images** for models that only see images, **extracted text** for text-only models. The person gets **page thumbnails in the chat**, stored and deleted like browser screenshots; if thumbnails cannot be made, the person sees a persistent "page previews unavailable" card with Retry. All document parsing this feature adds runs **outside the Omnipus process**: a small native pdfium program (`omnipus-pdfium`) and LibreOffice, both shipped by the installer next to the standalone Omnipus binary, each started per call with time, CPU, size and (where the operating system enforces it) memory limits. On macOS the memory limit is best effort, stated honestly (founder FQ1 = B). Agents never choose or start these programs. The four document skills become one `elicify-office` skill whose scripts are actually tested. The stale hard-coded PDF model list is deleted; the model catalog alone decides.

## 1. Inputs and decisions log (binding; source: `FOUNDER-ANSWERS-PRE-GRILL.md`)

| ID | Source | Content (quoted where binding) |
|---|---|---|
| F-1…F-6 | Founder, 2026-10-29 | Full docx/xlsx/pptx/pdf; ONE tool `preview_document(file)`; pdfium, **no Poppler**; LibreOffice **shipped with the installer**, application-managed, agents never start or choose it; merge the four skills; PDF pages as images for vision models without native PDF; "do not overcomplicate". |
| Q2 | Founder | "a now later b when we sign omnipus overall file issue for signing" → untouched official signed LibreOffice app now; trimmed copy after #1506. |
| Q3 | Founder | "A: No, recalculate on open" — no writeback, no recalculation option. |
| Q4 | Founder | "B: Thumbnails in chat"; reconfirmed "chat stays". |
| Q5 | Founder | "A: Allow, document limitation" (Windows, no file confinement). |
| Q6 | Team-lead check | `deepseek/deepseek-v4.1-flash` lists text + image input → standard UAT model for image rows. |
| FQ1 (r1) | Founder | "same as for browser screenshots that already happening" → thumbnail storage, lifecycle, access and deletion reuse the screenshot mechanism. |
| FQ2 (r1) | Founder | "B: Allow anyway" → macOS/Linux conversion runs unconfined when the sandbox cannot be set up; disclosed truthfully. |
| FQ3 (r1) | Founder | "10 images is fine but the agent can request the remaining in batches like pagination, otherwise it can never see an entire document" — uploads and workspace files. |
| CRIT-001 (r1) | Founder | "B: Render outside the program" with OS-level memory/time/cancel limits. |
| RQ1 | Founder | "B: Separate native pdfium program" shipped with the installer (optional component; signing via #1506); no WebAssembly. Supersedes the earlier "inside Omnipus" and "helper mode" answers for pdfium. |
| RQ2 | Founder | "A: Yes, gpt-6-luna" — `openai/gpt-6-luna` for the native-PDF hands-on row only. |
| PL | Founder | Outdated model list: "B: With office preview"; "that must be deleted, only the model catalog determines it". |
| F-7 | Founder | "also LibreOffice should be a separate process, not part of the Omnipus process." |
| F-8 | Founder | "most modern models support PDF; here they should not use the preview but the native functionality." |
| SK | Founder | One decision table (tools consume / scripts create-edit); re-review and **actually test** the scripts. Input: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/office-preview-spec-20261030/skills-review.md` (read; dispositions in §6). |
| LB | Founder | Library previews withdrawn: "leave the office preview out, only the agent-facing part." **The Library gains no Office preview.** |
| **FQ1 (r2)** | Founder, 2026-10-30 | **B**: best-effort macOS memory boundary for this release; strongest measured controls (wall deadline, page/size/pixel caps, Stop, process-group kill); truthful UI/docs; **no claim of a hard memory cap on macOS**; Linux keeps enforced limits. |
| **FQ2 (r2)** | Team-lead at founder's instruction ("q7 you decide") | **A**: explicit partial success — the model receives the authorized native PDF; the person sees a persistent "page previews unavailable" explanation with Retry; never claim pages were rendered or visually verified; never start a damaged program or fall back to another renderer. |
| E-1 | Spike `office-preview-spike-20261029/` | LibreOffice 26.8.0.3, Intel Mac: 2.3–8.1 s per file; 105–124 MB; macOS sandbox works with 3 socket rules + `OSL_SOCKET_PATH` (< 104 chars) + filtered registry; stock registry → exit 134. Historical fixtures 6/3/4 pages, not this spec's fixtures. |
| D1 | Founder, spike decision D1 (2026-10-30) | **A**: the prebuilt pdfium exists only as a shared library, so `omnipus-pdfium` ships with `libpdfium.*` beside it; the manifest fingerprints both and both are re-hashed before every launch. Recorded as D1 A in `native/omnipus-pdfium/build.sh` (header) and `pkg/preview/component/component.go`; AD-2/AD-4 text aligned 2026-10-31. |
| O3 | backend-lead, Linux measurement on Fly, 2026-10-31 (founder "O7 A") | Linux 6.12, no Landlock. Allocation bombs (C malloc, C mmap, Python) stop at 448 MiB under **both** a 512 MiB address-space and a 512 MiB data limit; the Go `extract-text` mode cannot start under a 512 MiB address-space limit (30/30, `failed to reserve page summary memory`) but starts under the data limit; 1 launcher Go-runtime crash in 180 launches under the address-space limit; LibreOffice converts identically under address-space and data 1 GiB; `soffice.bin` exits 81 on its first start with a fresh profile, second start converts. Report: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/office-preview-build-20261030/spikes/SPIKE-REPORT.md` ("O3"), receipts `spikes/o3-receipts/`. Decided in §3.1. |
| E-2 | Security review B | No LaunchServices; registry bound to the converter run and delivered only to that child; no patching the bundle; visible failure; two Mach services unchanged; Windows has no child file confinement. |
| **S2** | Founder, asked 2026-11-01, confirmed 2026-11-05 | **A**: agents can never be granted write access into the preview program folders (PDF renderer and LibreOffice); no live-writer tracking or lock design. Side effects, founder **A** (2026-11-05): (1) a mount or approval that **contains** a program folder (for example a mount of `/`) is refused with a message naming the program folder — this **supersedes the 2026-08-12 "warn and allow" mount ruling for that case**; (2) write access Omnipus cannot reduce to a folder is refused. Decided in §3.2. |
| **SR1** | Founder, asked 2026-11-01, confirmed 2026-11-05 | Fix SEC-R1-01 (High) now: exit-81 cleanup and profile reseed stay anchored to the scratch folder across every path component (§3.1 D3.7, T-90). SEC-R1-02…06 become tracked issues #1608…#1612, not this round (§3.3). |
| **O8** | Founder, asked 2026-11-01, confirmed 2026-11-05 | **A**: the Linux memory limits (T-76, T-80) are measured once by a manual GitHub Actions job on `ubuntu-latest` on the exact candidate version (T-91). |
| **Q18** | Founder, 2026-11-07 | **A**: fix both crash-recovery findings. After a crash, cleanup acts only on what Omnipus itself recorded, out of the converter's reach; the converter is treated as compromised (§3.4 D1–D4). |
| **Q19** | Founder, 2026-11-07 | **A**: fix `call.publish` so FR-050 holds (§3.4 D5). |
| **Q22** | Founder, 2026-11-07 | **A**: accept the limit on Windows and in the disclosed "without sandbox" fallback, and state it in the spec (§3.4 D7). |
| **Q23** | Founder, 2026-11-07 | **A**: add the second-start guard and T-100 this round (§3.4 D6). |
| **Q24** | Founder, 2026-11-07 | **A**: fix the settings routes that lose the refusal text, in one commit, this round (§3.4 D9). Asked as "19 routes"; the exact scope is 19 code sites serving 20 routes, one of which is not mechanical. |
| **Q26** | Founder, 2026-11-07 | **A**: "All of them now": every other config writer that hides the refusal reason is fixed this round, including `POST /auth/login`, `/auth/logout` and `/auth/change-password`; the sign-in routes need security-lead's review in the gate (§3.4 D9b). |
| **Q35** | Founder, 2026-11-08 | **A** (Option 1): the converter gets its own short fixed read list and no longer inherits the turn's read rights (§3.4 D8c). |
| **Q36** | Founder, 2026-11-08 | **A** (R1): LibreOffice reads the verified registry folder in place, read-only; the D8b.1 copy is dropped (§3.4 D8c). |

## 2. Round responses

### 2.1 Round 2 responses (this correction)

| Finding | Sections changed | How closed |
|---|---|---|
| R2-CRIT-001 resource boundary | AD-2, AD-6 (new budget table), AD-15, §4, FR-012/031/032/048, SC-004, T-31/35/36/59/60 | Founder FQ1 = B recorded: macOS memory best effort with listed controls and disclosure, no hard-cap claim; Linux/Windows enforced. **Numeric retained-byte budgets** for live-turn model media (per turn and per gateway), rendered-page cache, in-flight program output, assembled provider request and extracted text; **eviction frees bytes** (not just omits). All parsing this feature adds — page count, text, validity, rendering, including the no-renderer fallback — runs in a killable program (`omnipus-pdfium`, or the binary's `extract-text` mode run through the launcher) under the same deadline/Stop. Peak gateway memory is a measured criterion over the worst paths, not a claimed 100 MiB. |
| R2-MAJ-001 native PDF transport | §4.2 (new transport matrix), AD-11, FR-039/040, T-44/T-61, U-M5b | One required outbound form for every retained adapter and all three entry points. Missing Omnipus serialization is implementation work, never "adapter unable"; only a documented upstream limit can withhold native PDF, and then the **catalog** entry lacks the PDF modality. T-44 inspects outbound bytes and fails on omission. Send-time re-authorization. gpt-6-luna row covers upload, `read_file` and preview. |
| R2-MAJ-002 program protocol and lifecycle | AD-2 (protocol v1, launcher per platform), FR-033/034, T-56/T-57 | Versioned little-endian framed protocol with request header, typed frames, maximum sizes checked before allocation, mandatory META first and DONE last, error frame; EOF/cancel/parent-death separated; Linux exec with parent-death signal, macOS exec + EOF + recovery, Windows launcher-in-job with kill-on-close; parsing starts only after limits are in force (go-byte); build/protocol identity in manifest and cache key. |
| R2-MAJ-003 recovery identity | AD-8 rewritten, FR-013, T-14/T-58 | Owner record published before spawn; target starts only after the go-byte, which is sent after child identity (PID + process start time + executable) is recorded; covers renderer, launcher and LibreOffice trees; kill only positively owned processes; delete scratch only after termination is confirmed; ambiguous ownership → kept + diagnostic. |
| R2-MAJ-004 preview groups | §5.2 (contract v2), FR-035/036, T-46/T-62/T-63 | `MediaFrame` gains `tool_call_id`, `presentation`, frame-level `preview` record (source, version, page count, range, outcome); `MediaPart` gains `page_number`; same projection from live WebSocket, replay and REST history; store groups by tool call; thumbnails ride a display-only field never admitted into model media. |
| R2-MAJ-005 Windows manual route | AD-4, §10, FR-042, T-68, U-W1 | Operator command `omnipus office register <folder>` (and `omnipus pdf-renderer register`) writes an **operator-registered** manifest into Omnipus's protected data; trust boundary stated; clean-machine test; env-var roots removed. Docker uses the same command at image build. |
| R2-MAJ-006 outcomes contradiction | AD-15 rewritten, §4.3 outcome table, FR-011/041, MV-1, T-52/T-70 | Three independent outcomes: conversion validity, model delivery, thumbnails. Renderer missing/damaged/failed, empty text (scanned), text too large, native + thumbnail failure, zero stored — each defined. Separate 20/25/50/200 MiB boundaries tested. No OCR added. |
| R2-MAJ-007 uploads without a path | §4.1, FR-044, T-71 | Every PDF upload delivered as images is materialized into the turn's workspace through the existing offload; the note names the path; failure is an error state with a visible reason. |
| R2-MAJ-008 Windows CI guard | AD-12, FR-045, T-41/T-67 | Named inventory of new Windows tests across all packages; each must show a non-skipped PASS line; separate step builds and smoke-tests the real Windows renderer; guard proven by removing one family. |
| R2-MAJ-009 viewer focus | §5.4 rewritten, FR-037, T-64 | Initial focus, Tab containment via the catalogued dialog primitive, page change without remount (viewer keyed by group), first/last behaviour, return-target fallback after virtualization; existing image/diagram regression. |
| R2-MAJ-010 viewer failures | §5.3 states table, FR-037/038, T-65/T-66 | Enlarged-view loading/error/Retry, per-page failure, zero thumbnails, native + thumbnail failure card with Retry (FQ2 A); one group announcement; per-image toasts suppressed for this presentation only. |
| R2-MAJ-011 script oracles and review input | AD-14, §6 skills-review dispositions, FR-046/047, T-55/T-72/T-73/T-74 | Review read and dispositioned item by item (no recalculation mode, keep `elicify-office`, no OCR dependency). Four-inspector inventory frozen with oracles from each `inspector-contract.md` + review §4 cases, through the managed interpreter on 3 platforms, with mutations; package-inventory integrity test updated deliberately. |
| R2-MAJ-012 shared installation | AD-4, FR-043, T-69 | A component is usable only while **no** agent's workspace, mount, operator allowance or grant gives write access to its root (checked at start, on every mount/grant change and per call); otherwise disabled for everyone as "unsafe location". Verified real path carried into the spawn. |
| R2-MIN-001 RED scope | §18 W1 | W1 owns every test row T-01…T-74. |
| R2-MIN-002 docs destination | §17 | New user page `docs/documents.md`; states the Library gains no Office preview. |
| R2-OBS-001 / R2-OBS-002 | AD-2, S-1 | Reuse kept; no service or framework. S-1 records source/version/checksum, license inventory, reproducible inputs, linkage, final post-signing hash, quarantined clean-install launch (no quarantine removal), non-embedded-font and form PDFs. |

### 2.2 Round 1 (historical)
All round-1 items were addressed in the previous revision; grill 2 confirmed closed in design: MAJ-005 (sandbox), MAJ-006 (cache before rendering), MAJ-009 (oracles), MIN-001…004. Partly closed items are completed by the round-2 rows above.

## 3. Architect decisions

**AD-1 One tool.** `preview_document(file, pages?)`; `pages` = `"N"` or `"N-M"`, default first 10, at most 20 per call. `file` is a workspace path (uploads are materialized there, §4.1). The result text states the source name, its SHA-256 (first 12 hex), what the model received, "pages a–b of N", the next batch when more exist, and the thumbnail outcome. Amends ADR-090 D6 (W0).

**AD-2 Engines outside the Omnipus process.** The Go binary stays standalone and starts without either component (Hard Constraints #1/#2 apply to it; the components are founder-approved exceptions under ADR-090).
- **`omnipus-pdfium`**: small C program built in CI, linked against a pinned prebuilt pdfium **shared library shipped beside it** (version + SHA-256; D1 A, 2026-10-31 text: the prebuilt release has no static library). The library is resolved only from the program's own folder (`$ORIGIN` on Linux, `@executable_path` on macOS with the install name rewritten, the DLL next to the `.exe` on Windows), never from the working folder or a search path. Per platform: darwin arm64/amd64, linux amd64/arm64 (glibc), linux musl if available, windows amd64. Opens no file and no socket; all I/O over stdin/stdout. Operations: `info` (page count, encryption), `render` (pages at 2×, forms drawn, longest side ≤ 2000 px, bitmap size checked **before** allocation), `text` (per-page text).
- **LibreOffice**: always a separate child per conversion; never linked or loaded into the gateway; no LibreOfficeKit, no UNO bridge, no long-running converter. The gateway writes the input copy into scratch and reads the output PDF.
- **`extract-text`**: hidden mode of the `omnipus` binary used only when the renderer is absent; runs the existing Go extractor on bytes from stdin, same protocol `text`/`info` frames.
- **Launcher `run-limited`** (hidden mode of `omnipus`): started through the existing hardened spawn (sandbox per AD-7). It applies limits, waits for one **go-byte** on stdin, then starts the target. Per platform: **Linux** sets data (`RLIMIT_DATA`)/stack/CPU/file-size/open-file limits and parent-death signal (SIGKILL), then `execve` the target in its own process group (no address-space limit; amendment §3.1, 2026-10-31). **macOS** sets CPU/file-size/open-file limits (memory best effort, AD-6), then `execve`; no parent-death signal — the target exits on stdin EOF and startup recovery covers the rest. **Windows** creates a Job Object with memory, process-count and kill-on-close limits, assigns itself, starts the target as its child (inheriting the job), relays stdio and exit code; the gateway holds the launcher's handle; killing the launcher or the gateway's death closes the job and ends the whole tree. In all cases untrusted input reaches the target only after limits are in force (go-byte after record publication, AD-8). **Order, reaffirmed 2026-10-31 (§3.1):** limits → parent-death signal (Linux) → read the go-byte → `execve`; the limits are never moved after the go-byte.

**Protocol v1 (private, Go ↔ `omnipus-pdfium` / `extract-text`).** All integers little-endian.

| Part | Layout | Limits / rules |
|---|---|---|
| Request | magic `OPDF`, u16 version = 1, u16 op (1 info, 2 render, 3 text), u32 first_page, u32 last_page, u32 max_long_edge_px, u32 flags (bit0 render forms), u64 input_len, then input bytes | input_len ≤ 50 MiB (PDF) / 200 MiB (converted PDF); last − first < 20 |
| Frame | u8 type, u32 length, payload | reader checks type and `length ≤ max(type)` **before** allocating |
| META (1) | JSON ≤ 4 KiB: protocol_version, build_id, page_count, encrypted | exactly one, first |
| PAGE (2) | u32 page, u32 width, u32 height, PNG bytes; ≤ 4 MiB | pages strictly increasing inside the requested range; width/height ≤ max_long_edge; PNG decodes to those dimensions |
| TEXT (3) | u32 page, UTF-8; ≤ 1 MiB | total text ≤ 4 MiB per call |
| ERROR (4) | JSON ≤ 4 KiB {code, message} | terminal |
| DONE (5) | u32 frames_emitted | required for success; count must match |

Anything else — wrong version, unknown type, oversize, duplicate/out-of-range page, missing META/DONE, truncated frame, output after DONE, total stdout > 80 MiB — is a protocol error: kill the group, report "PDF renderer failed (protocol)". stdin EOF **before** input_len bytes → program exits 3 ("truncated input"). stdin EOF **after** the request means the parent is gone → program stops at the next page boundary. Cancellation = process-group kill (Unix) / launcher kill (Windows). stderr ≤ 64 KiB kept, passed through the sensitive-data filter.

**AD-3 Filtered registry at packaging; document runtime loses its converter.** Packaging runs `pkg/documentruntime/converter_registry.go::filterMacSpellChecker` + `verifyFilteredRegistry` on the pinned bundle and ships the result in the component manifest; each conversion passes exactly one `URE_MORE_SERVICES` to its own child. `pkg/documentruntime/provision.go::ProvisionFirstParty` drops its `Converter: <prefix>/bin/soffice` contract and the probe stops requiring `soffice` (agents never run LibreOffice). The runtime registry store (`ConverterServiceRegistryDir`, `ConverterServiceOverride`, `PrepareConverterServiceRegistry`, `MaterializeConverterServiceRegistry` and helpers) is deleted only after `pkg/tools/shell_env.go::documentEnvTurn`'s readiness gate is characterized (healthy/missing/corrupt) and replaced by a registry-free readiness check (T-42). This is the one intended change to bash composition.

**AD-4 Component identity and trust.**
- **Sources.** (a) Packaged slots next to the executable (§10, mirroring `pkg/tools/browser/package_chrome.go::packageChromeRootCandidates`), each with a shipped `manifest.json`; (b) an **operator-registered** component: `omnipus office register <folder>` / `omnipus pdf-renderer register <folder>` (operator CLI) validates the layout (supported LibreOffice: official 26.8.x program folder; renderer: our release zip), computes the fingerprints and writes the manifest into Omnipus's protected data directory, marked `trust: operator-registered` with the operator, time and path. `omnipus office unregister` removes it. Used for Windows (no installer until #113), the Intel dev Mac and the Docker image build. No environment-variable roots, never `PATH`, a workspace, a tool argument or agent-editable configuration.
- **Manifest.** Component version, build ID and protocol version (renderer), SHA-256 of `omnipus-pdfium` and its `libpdfium.*` (D1 A) / `soffice` (and `soffice.bin` on Windows), of the filtered registry (macOS) and of the file list.
- **Checks.** Renderer: program and library re-hashed before every launch. LibreOffice: executable and registry re-hashed at gateway start and whenever size/modification time changed. Mismatch → "damaged", nothing launched, no fallback program. The verified real path is resolved once per call and passed to the spawn unchanged.
- **Writer exclusion (R2-MAJ-012; 2026-11-05, S2 = A, §3.2).** Agents can never be granted write access into a preview program folder: a new approval, mount or configuration entry that overlaps one is refused where it is created (§3.2). Write access already in force — configuration at gateway start, a mount record or remembered chat grant that predates the program folder — is still caught here: a component is usable only while its root is not inside, and does not contain, any write root of **any** agent (workspaces, mounts, operator allowances, sandbox allowed paths, every session's remembered write grants) or of the calling turn. Checked per call (`pkg/preview/service.go::Service.rendererComponent` / `::officeComponent`) and by `doctor`. If any overlap exists the component is disabled for everyone: "unsafe location", plus a `doctor` line naming the overlapping access. Not re-hashed per call: the bundle's libraries and configuration; their protection is this rule plus the kernel sandbox where active. **Honest boundary:** on Windows (Q5) and in unconfined mode (FQ2 r1) agents with `bash` are not kernel-prevented from writing an install folder (the §3.2 pre-flight refusal still applies to the writes it sees); operator-registered components rest on the operator's registration; §3.2 lists what is not tracked. Documented.
- **`doctor` findings (2026-11-01).** `doctor` reports damaged and agent-writable components, an uncheckable component state and an unenforced Linux memory limit; a component that is not installed is not a `doctor` finding (an install choice, §10; the tool result and component status report it).

**AD-5 Input and page rules.** Exactly `.docx .xlsx .pptx .pdf`, content matching the extension, ≤ 50 MiB, not encrypted. (1) `pages` syntax (digits, 1 ≤ N ≤ M ≤ 9999, M − N < 20) and file checks before any work — refusal starts nothing. (2) After `info`: default range clamps; partial overlap clamps ("pages 9–12 of 12 (requested 9–20)"); wholly beyond → "the document has N pages". Macro-enabled, legacy, OpenDocument and template formats are out of scope.

**AD-6 Limits and budgets (R2-CRIT-001, FQ1 r2).** Design budget; S-1 measures; a missed budget escalates to the founder.

| Item | Budget | Enforcement |
|---|---|---|
| Admission | ≤ 2 renderer/extract runs, ≤ 1 LibreOffice per gateway; taken before reading or copying the source | semaphores |
| Whole call | 120 s incl. queue wait; Stop at any time | deadline / Stop → kill group (Unix) or launcher (Windows), release admission and buffers |
| Renderer / extract run | 60 s wall, 60 s CPU | deadline; CPU limit (all Unix incl. macOS) / job time limit (Windows) |
| Upload rendering | 20 s per PDF | then text fallback |
| Program memory | renderer and extract 512 MiB; LibreOffice 1 GiB | **Linux**: data limit (`RLIMIT_DATA`, chosen by the O3 measurement, §3.1) plus a stack limit of 8 MiB (never raised above the inherited one); where the kernel cannot enforce the data limit on `mmap` (kernel older than 4.7, or boot option `ignore_rlimit_data`) the memory limit is reported as not enforced (§3.1 D1.4); **Windows**: Job Object memory limit; **macOS: best effort (FQ1 r2)** — CPU limit, wall deadline, pre-allocation bitmap/pixel caps, output caps, Stop, process-group kill, plus a parent-side resident-memory watchdog (kill above the budget, sampled every 250 ms) **only if** S-1 finds a pure-Go sampling method; no hard-cap claim in UI or docs |
| Program output | renderer stdout ≤ 80 MiB, frames ≤ 4 MiB; LibreOffice PDF ≤ 200 MiB (file-size limit on Unix; checked after on Windows) | protocol reader / limits |
| In-flight bytes in the gateway | ≤ 1 frame (4 MiB) per running program → ≤ 8 MiB | frames streamed straight to the live store or the thumbnail file |
| Live-turn model media (images + native PDFs) | ≤ 64 MiB per turn, ≤ 256 MiB per gateway | on overflow the oldest tool-call batches' **bytes are freed** (marker becomes "evicted; call again"); an item larger than the per-turn budget is refused |
| Rendered-page cache | ≤ 64 MiB per gateway, least recently used out | AD-9 |
| Assembled provider request media | ≤ 32 MiB decoded per request; encoded copies released after the send | newest calls first; omitted batches carry `[pages not re-sent; call preview_document again]` |
| Images to the model | ≤ 20 per call; ≤ 20 auto upload pages per request (newest uploads first) | request assembly |
| Extracted text to the model | ≤ 4 MiB per call, truncated with a note | AD-15 |

**AD-7 Converter sandbox.** Converter-specific policy (no turn policy derivation since §3.4 D8c) and hardened spawn: working folder = scratch; writes only scratch and (macOS) the socket folder; none of the caller's write roots; reads only the converter's own fixed list (§3.4 D8c), never the turn's read model; environment built from nothing (`PATH` minimal, `LANG`, `HOME`/`TMPDIR` in scratch, macOS `URE_MORE_SERVICES` and `OSL_SOCKET_PATH` once). macOS: the Seatbelt profile of that list + exactly the 3 rules of `converter-additions.sb` for a per-call 0700 `/private/tmp/o-<8 random>` (< 104 chars), network denied except that socket (T-30; if it breaks conversion, team-lead escalates); no new Mach service. Linux: Landlock with the same file rules; TCP bind/connect denied where Landlock supports it (not UDP — documented). Windows: Job Object only (Q5). LibreOffice profile: macros never run, links never updated, formulas recalculated on load. *Amended 2026-11-08 (§3.4 D8c, founder Q35 = A, Q36 = A; replaces the D8b note of the same day):* the converter's reads are its own fixed list, the same in both file models; on macOS that list includes the verified filtered registry folder, read in place, read-only; no caller passes the turn's read model to the preview any more. `omnipus-pdfium` and `extract-text`: no writes, no network. **Unconfined (FQ2 r1):** if macOS/Linux cannot set up this sandbox, the programs still run with AD-6 limits; result text `converted without sandbox: <reason>`, audit field, `doctor` line.

**AD-8 Scratch and recovery (R2-MAJ-003).** Per call `$OMNIPUS_HOME/tmp/preview/<id>/` (macOS socket folder separate).
1. Write `owner.json` atomically: state `preparing`, gateway PID + start time.
2. Spawn `run-limited` (it waits for the go-byte; if stdin closes first it exits without starting anything).
3. Record child identity atomically: state `running`, launcher PID + process start time + executable, process-group ID, intended target executable, socket folder.
4. Send the go-byte. 5. On exit: state `done`, then remove folders. A second start within the same call (LibreOffice exit-81 retry, §3.1 D3) repeats steps 2–4 for the new launcher after the first one's exit and group kill.
- **Startup:** for each folder whose owner gateway is dead (PID + start time): `preparing` → nothing can have started; remove. `running` → for each recorded process, kill only if PID, start time **and** executable (launcher, `omnipus-pdfium`, `soffice`/`soffice.bin` of the verified component) all match; then confirm termination (poll ≤ 5 s) before removing. Unconfirmed, unreadable record, or a live owner gateway (two gateways sharing a home) → keep, warn in log and `doctor`. Windows: the gateway's death already closed the job; only folders remain.
- **Amended 2026-11-07 (§3.4, governs where it differs):** per call `$OMNIPUS_HOME/tmp/preview-calls/<id>/`, with `owner.json` at that level and scratch in `<id>/work/`. Step 3 no longer records a process-group ID: recovery derives the group from the verified launcher. The socket folder is recorded when it is created, not at step 3. The Windows sentence above is unverified: a live launcher that matches its record is stopped there too (§3.3).

**AD-9 Rendered-page cache.** Key = (PDF content SHA-256, page, render settings, renderer build ID + protocol version). Looked up only after this request's authorization of the source; checked before starting the renderer.

**AD-10 Thumbnails.** §5. Rendered independently of what the model receives.

**AD-11 Three-way model rule.** Catalog only: `pkg/agent/media_present.go::modelSupportsPDF` / `::modelSupportsImage`. `pkg/agent/loop_media.go::pdfCapableModel` is **deleted**; its Haiku-via-Bedrock exclusion becomes the catalog entry for that provider + model, in the authoritative catalog source. Native size limit = the agent's media size limit (`maxSize`, default 20 MiB, as `encodePDFToDataURL`). §4.

**AD-12 Platforms and CI.** Office preview + renderer via installer on macOS arm64 and Linux; Docker via the image; Windows and the Intel dev Mac via operator registration (Q5: Allow, no file confinement). Windows CI: §13 T-41/T-67.

**AD-13 Policy and reachability.** Ceiling `preview_document: allow` (`pkg/config/defaults.go`), `pkg/coreagent/seed.go::allStaticToolNames`; Allow in `commonWork` of `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` and in `NewCustomAgentToolsCfg` (main and worker); Omnipus explicit Deny; hidden system agents keep Deny. Registered at the `read_file` site (`pkg/agent/instance.go`) and in `pkg/tools/general_builtin_catalog.go`.

**AD-14 One office skill (`elicify-office`).** `SKILL.md` opens with this table:

| Job | Use | Never |
|---|---|---|
| Read an existing document | `read_file` (PDF: native / page images / text per §4; Office: extracted text) | starting a converter |
| Check how a document looks | `preview_document` (pages in batches) | rendering with your own tools |
| Create / edit Word | `python-docx` (`knowledge/docx-create.md`, `docx-edit.md`) | — |
| Create / edit Excel | `openpyxl`; formulas calculate on open (Q3) | LibreOffice recalculation |
| Create / edit PowerPoint | `python-pptx`; `pptxgenjs` only where Node is already present | — |
| Create / edit PDF | `reportlab` create; `pypdf` merge/split/rotate/fill | Poppler, `pypdfium2` |
| Check package structure | `scripts/inspect_docx.py`, `inspect_workbook.py`, `inspect_pptx.py`, `inspect_pdf.py` | treating a clean inspector run as visual proof |
| Missing dependency | `environment_setup` (Ask-gated, ADR-090) | the old "harness runtime notes" |
| OCR, secure redaction, signature validation | not provided; install OCR via `environment_setup` only if the person asks; report redaction/signature checks as unavailable | faking the result |

Layout: `knowledge/{docx,xlsx,pptx,pdf}-{create,edit}.md`, shared `knowledge/verify.md` (preview loop with paging, review criteria merged from the four visual-QA sections), merged `knowledge/inspector-contract.md`, the four scripts, merged `evals/evals.json` (re-labelled to `elicify-office`), LICENSE. The old four folders are deleted, no aliases. Wiring: `seed.go::coreAgentSkills`, `pkg/documentruntime/runtime.go::DocumentSkillIDs` / `InstallEmbeddedSkills`, `docs/internal/specs/adr-090-document-package-inventory.json` (new adaptation statement and checksums), the canonical-IDs paragraph in `docs/internal/specs/adr-090-agent-configuration-and-skills-spec.md`, Mia / General Purpose prompts. T9 (companion-file access) and #882 (shell-guard quoting) affect how scripts are invoked; they are named dependencies, not fixed here.

**AD-15 Outcomes without or with a failed renderer (R2-MAJ-006, FQ2 r2).** §4.3. With the renderer absent, damaged or failed: no page images, no thumbnails; a native-PDF model still gets the authorized PDF (partial success); other models get text from `extract-text` (absent renderer) or the renderer's `text` op is not available (damaged/failed) → `extract-text`. Office conversion validity without a renderer = `extract-text` `info` opens the PDF and reports ≥ 1 page ("structurally valid, not rendered"). Never claim rendered or visually verified pages.

**Packaging.** Q2: untouched official LibreOffice app now; trimmed copy and renderer signing after #1506.

### 3.1 Dated amendment 2026-10-31 — Linux memory-limit kind, launcher order, LibreOffice first start (architect, after O3)

Status: decided 2026-10-31 (architect). Amends AD-2, AD-4, AD-6, AD-8 above; adds T-76…T-80. D3.7 added 2026-11-05 (founder SR1, SEC-R1-01): adds B-63, T-90, FR-050. AD-6 delegated the Linux choice "address-space (or data) limit chosen in S-1, proven by allocation bombs" to measurement; this records that choice. No founder decision is reopened: budgets, FQ1 (r2) "Linux keeps enforced limits" and the go-byte rule stand.

**Context.** The O3 Linux run (§1 row O3) found three defects:

| # | Defect | Evidence (receipts under `spikes/o3-receipts/`) |
|---|---|---|
| 1 | The Go `extract-text` mode cannot start under a 512 MiB address-space limit; it starts under 1 GiB / 2 GiB address space and under a 512 MiB data limit | `logs-r2/10-diag.log`, `logs-r2/11-diag2.log` (exit 2, `fatal error: failed to reserve page summary memory` in `runtime.(*pageAlloc).sysInit`); `logs-r2/09-stress.log` 30/30 `extract-info` and `extract-text` failures on both PDFs |
| 2 | LibreOffice: every conversion through `preview.Service` failed, `soffice.bin` exit 81 on the first start with a fresh profile | `logs-r2/06-memory.log`, `logs-r2/12-lo-exit81.log` (attempt 1 exit 81, attempt 2 converts, under address-space and data limits alike) |
| 3 | One launcher Go-runtime crash in 180 renderer launches under the address-space limit (exit 2, stack ends in `runtime.(*cleanupQueue).createGs`); 0 in 500 bare launcher runs | `logs-r2/09-stress.log` line `renderer-text1-6#1` (pdf30), `logs-r2/10-diag.log`, `logs-r2/11-diag2.log` |

Mechanism, verified against sources on 2026-10-31:
- The Go runtime reserves its large bookkeeping ranges as no-access mappings (`src/runtime/mem_linux.go::sysReserveOS`: `mmap(…, _PROT_NONE, …)`) and makes memory usable later with a read-write mapping (`::sysMapOS`). The address-space limit counts both; Linux's data limit counts only private, writable, non-stack mappings (`mm/vma.h::is_data_mapping`: `(flags & (VM_WRITE|VM_SHARED|VM_STACK)) == VM_WRITE`), checked on every `mmap`, `mremap`, `brk` and on `mprotect` that makes a private mapping writable (`mm/mmap.c::may_expand_vm`, `mm/mprotect.c::mprotect_fixup` "Check space limits when area turns into data"). So the reservation that defect 1 trips over is invisible to the data limit, while every byte the Go program actually uses still counts.
- `run-limited` is a mode of the same `omnipus` binary as `extract-text`; that binary's start-up reservation alone exceeds 512 MiB (defect 1). `launcher/child_unix.go::Main` sets the limit on itself **after** start-up and then keeps running Go until the go-byte, so it sits above its own address-space limit and any new runtime mapping in that window (a goroutine stack, heap growth) is fatal. That is the Inferred cause of defect 3 (the crash stack is a runtime-created goroutine); it does not apply under the data limit, where the launcher's writable memory is small.
- LibreOffice's own start programs restart `soffice.bin` with all arguments when it exits 81 (`EXITHELPER_NORMAL_RESTART = 81`, `include/desktop/exithelper.h`; `desktop/unx/source/start.cxx` "re-start with all arguments"; `desktop/win32/source/loader.cxx` loops on 81 and 79). The product starts `soffice.bin` directly (fingerprinted, AD-4), so nothing performs that restart.

**Decision.**

D1 (defect 1 and 3) — **Linux uses the data limit for every program `run-limited` starts** (renderer, `extract-text`, LibreOffice), and for the launcher itself, which passes it to the target across `execve`. The address-space kind is deleted, not kept as an option.
1. Budgets unchanged: 512 MiB renderer and `extract-text`, 1 GiB LibreOffice (AD-6).
2. The launcher also sets a stack limit (`RLIMIT_STACK`) of 8 MiB, lowered to the inherited soft limit when that is smaller, never raised; the hard limit is set to the same value. Reason: the data limit does not count the main thread's growing stack (`VM_STACK`), which the address-space limit did; without this a deep-recursion document under an inherited unlimited stack would escape the budget.
3. Soft and hard limits stay equal (as `launcher/child_unix.go::setRlimit` does today), so the program cannot raise them.
4. Enforcement check at gateway start (Linux): the data limit governs `mmap` only on kernel 4.7 and later (getrlimit(2): "and (since Linux 4.7) mmap(2)") and becomes warn-only under the boot option `ignore_rlimit_data` (`mm/mmap.c::may_expand_vm`). If the kernel is older than 4.7, or `/sys/module/kernel/parameters/ignore_rlimit_data` reads `Y`, the programs still run with the limits set, and every result, the audit record and `doctor` say `memory limit not enforced: <reason>` — the same disclosure pattern as the unconfined case (AD-7, FQ2 r1). Unreadable parameter file: treated as enforcing (the default) and noted once in the log.
5. macOS: unchanged — the kernel rejects address-space, data and resident-size limits (S-1), so memory stays best effort with the watchdog (FQ1 r2); no limit kind applies. Windows: unchanged — the Job Object limit counts committed memory, which, like the data limit, excludes reserved-only address space; Go's reservations are reserve-only there too (Inferred, not measured — T-79 measures it).

D2 (defect 3) — **The launcher order stays as AD-2/AD-8 define it: limits, parent-death signal, go-byte, `execve`.** Under D1 the launcher no longer runs above its own limit, which removes the crash mechanism; the go-byte guarantee (untrusted input reaches the target only after limits are in force and after the owner record is published) is kept unchanged.

D3 (defect 2) — **Team-lead's ruling stands and does not conflict with the design**: when `soffice.bin` exits with exactly 81 and wrote no output PDF, the same call starts it once more with the same arguments and the same per-call profile; any other code (79 included) or a second 81 is a visible failure ("LibreOffice produced no readable PDF (exit status 81)"). Conditions that keep it inside the design:
1. The retry holds the same LibreOffice admission slot (AD-6) — not released and re-taken — and runs inside the same 120 s call deadline and Stop.
2. The second start is a full AD-8 cycle: the first launcher has exited and its group has been killed (`launcher.Process.Wait`), then a new `run-limited` is spawned, its identity recorded (state `running`, new PID + start time) and only then its go-byte sent. The record never names a process that is not running.
3. Same verified executable path, same sandbox derivation, same environment and limits as the first start; `out/` must hold no `document.pdf` before the retry.
4. The profile's macro, link and recalculation settings (AD-7) must still be in force on the second start; T-78 proves it rather than assuming LibreOffice's first start leaves the seeded `registrymodifications.xcu` intact.
5. Validity is still judged only by the output (FR-011, "ignore exit codes"): exit 81 only decides whether a second start is attempted when no readable PDF exists; it never makes a result valid or invalid.
6. Applies on every platform (the Windows loader also restarts on 81); harmless where the first start already converts.
7. **Anchored scratch operations (2026-11-05, founder SR1, SEC-R1-01 High).** *Context:* `pkg/preview/programs.go::reseedOfficeProfile` checks only its last folder (`profile/user`) with `Lstat`, then `os.Remove` and `os.OpenFile` look up every folder above it again by name; the leftover-output removal in `::officeConversion.run` and `::readConvertedPDF` do the same for `out`. A first start that replaced `profile` or `out` with a link to an outside folder (no race needed) therefore makes the gateway — which is not confined, unlike the sandboxed converter (AD-7) — delete, recreate or read files outside scratch (security review round 1, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/office-preview-build-20261030/review-r1/security-report.md`). *Decision:* after any LibreOffice start, every file operation the gateway performs in the call's scratch folder — reading `out/document.pdf`, removing a leftover `out/document.pdf` before the retry, removing and recreating `profile/user/registrymodifications.xcu`, and removing the call's folders at the end (`pkg/preview/scratch.go::call.finish`, AD-8 step 5) — is resolved through a handle on the call's scratch folder that the gateway opened **before the first start**, never through a path whose folders are looked up again by name. A link at any level below that folder (`out`, `profile`, `profile/user`, or the file itself) that leads outside it makes the operation fail; the gateway never reads, removes, creates or writes anything outside the scratch folder; the end-of-call removal removes a link itself, never what it points to. A link that stays inside the scratch folder may be followed (it cannot reach outside). Outcomes: an output reached through an outside link counts as "no readable PDF"; a cleanup or reseed that meets one ends the call with `conversion failed: scratch unavailable` or `conversion failed: the LibreOffice profile settings could not be restored`, and no second start happens. Go's `os.Root` (Go 1.24 and later; `os/root.go`: "symbolic links may not reference a location outside the root") is one way to meet this; backend-lead chooses. One code path on every platform; tested on Linux and macOS (T-90), where the converter is confined and this boundary exists — on Windows the converter has no file confinement (Q5). *Consequences:* positive — the converter's write boundary holds across the exit-81 retry; negative — none beyond the implementation; a first start that leaves outside links now ends the call instead of retrying.
   *Amended 2026-11-07 (§3.4 D5, D6):* the owner record now sits outside scratch, so publishing it is not an operation in this list. The final removal of the scratch folder goes through a handle on the call folder. A replaced scratch folder ends the call before any second start.

**Consequences.**
- Positive: `extract-text` works on Linux, so the AD-15 fallbacks (renderer absent or failed, Office validity without a renderer) work; Office conversion works on Linux; the launcher crash mechanism is gone without touching the go-byte order; one limit kind for every program and one code path.
- Negative: (a) the data limit does not count shared writable mappings (`VM_SHARED`: shared anonymous memory, `memfd`). The parsers allocate through `malloc`/`mmap` private memory, and the measured bombs stopped at 448 MiB under both kinds (O3), so for hostile *input* the two kinds are equivalent. A program already taken over by hostile code could allocate shared memory outside the budget — but the address-space limit never bounded that case either, because a taken-over program can `fork` and each child receives a fresh 512 MiB (the Linux spawn here sets no process-count limit for preview programs, `pkg/preview/confine_linux.go::hooksFor`). Only a memory cgroup would bound it; not in scope. (b) File-backed read-only mappings (program code, libraries) no longer count, so the same number allows somewhat more working memory than before — the budget now measures what costs RAM. (c) Hosts with kernel < 4.7 or `ignore_rlimit_data=Y` get a disclosed unenforced limit instead of an enforced one. (d) Every Office conversion on a fresh profile pays one extra short start (measured 165–218 ms, `logs-r2/06-memory.log`).
- Neutral: AD-6 numbers, macOS behaviour, the Windows job design and the protocol are unchanged.

**Alternatives rejected.**

| Option | Why rejected |
|---|---|
| Q1 B: raise `extract-text` to an address-space limit of 1 GiB or more | Gives Go children twice the budget of the C renderer for the same job, and still counts reservations: the start-up reservation size changes with the Go release (the binary is built with the release-pinned toolchain), so a toolchain bump can silently break it again. Leaves defect 3 in place for the launcher. |
| Q2 A: read the go-byte first, then set the limits and `execve` at once | Changes the AD-2/AD-8 order for no gain once D1 holds. It also does not close the mechanism: the launcher would still set a limit below its own current address space, the Go runtime's other threads keep running between `setrlimit` and `execve`, and `golang.org/x/sys/unix.Exec` builds its argument arrays on the Go heap, so a heap growth in that window is still fatal. Only shrinks the window. |
| Keep the address-space limit for the C renderer and use data only for Go children | Two limit kinds, two code paths, and the launcher (Go) still has to run under the target's address-space limit, so defect 3 remains. The extra coverage (shared mappings, stack) is closed by D1.2 for the stack and was not real for shared mappings (fork, above). |
| Q3 B: start `program/soffice` (script + `oosplash`) | Brings an unfingerprinted shell script and a second binary into the trusted launch path (AD-4) and an unbounded restart loop (`start.cxx` restarts on every 81/79). |
| Q3 C: seed one profile per gateway and share it | A shared, writable profile across calls breaks the per-call scratch isolation (AD-7, AD-8). |

**Affected components.** `pkg/preview/launcher/child_linux.go::applyLimits`, `pkg/preview/launcher/launcher.go::Limits` / `::parseArgs` / `Limits.args`, `pkg/preview/launcher/child_unix.go` (order unchanged), `pkg/preview/programs.go` (office conversion retry; limits; D3.7: `::reseedOfficeProfile`, `::readConvertedPDF`, `::officeConversion.run`), `pkg/preview/scratch.go::call.finish` (D3.7), the Linux `doctor` and result-text disclosure path used for `unconfined`, `docs/documents.md` and `docs/operations/sandbox-limitations.md` (§17).

### 3.2 Dated amendment 2026-11-05 — agents can never get write access to a preview program folder (architect, founder S2 = A)

Status: decided 2026-11-05 (founder S2 = A, asked 2026-11-01, confirmed 2026-11-05; both side effects accepted, founder A, 2026-11-05). **Supersedes the 2026-11-01 write-authority gate** (live set, preview lease, kill on overlap, lock order). That gate was never built: at `a0ad39455` there is no gate or lease code and no T-81…T-84 test. Its text is in Git history at `a0ad39455`. Amends AD-4 "Writer exclusion"; retires B-56…B-58 and T-81…T-84; adds B-59…B-62, T-85…T-89, FR-049. Input: security-lead re-check of W2-S02, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/office-preview-build-20261030/security/seclead-w2-recheck-report.md` (R3, R4).

**Context.**
- R3: an "Allow once" filesystem approval is applied only to the approved call's policy (`pkg/tools/shell_permission_mode.go::ExecTool.enforceFSPreflight` appends it to `policy.PathGrants`; `RecordPathGrant` runs only for "Allow") and never appears in `pkg/security/approvalgrants.go::ApprovalGrantStore.AllPathGrants`, so the component check cannot see it while that command (or what it left running) still holds it.
- R4: `pkg/preview/service.go::Service.officeComponent` / `::rendererComponent` check a snapshot, then `pkg/preview/programs.go::officeConversion.run` / `protocolProgram.run` start the program with no coordination with a later approval, mount (`pkg/workspace/mount.go::CreateMount`) or configuration change.
- The 2026-11-01 answer tracked every running writer and stopped a preview when overlapping access appeared. The founder chose the simplest alternative: such access is never given.

**Decision.**

1. **Protected program folders.** For both components (LibreOffice and the PDF renderer): every packaged slot location (`pkg/preview/component/component.go::Resolver.PackagedCandidates`), **whether or not anything is installed there**, and the folder named in each registered manifest (`component.RegisteredDir(home, kind)`, file `manifest.json`, field `root`). Read fresh at every check, never cached: registration runs in a separate process. Empty slots are protected because a packaged manifest is trusted by where it sits (`component.go::Resolver.Resolve`), so a folder planted in an empty slot would be trusted. A registered manifest that cannot be read names no folder; that component is "damaged" and never runs (AD-4), and registering it again runs the registration check.
2. **Overlap.** New write access overlaps when its folder is inside, equal to, or **contains** a protected folder, decided as `pkg/preview/component/exclusion.go::FirstOverlap` decides it (as written and with links resolved, by filesystem identity; an undecidable comparison counts as an overlap). Write access Omnipus cannot reduce to a folder (`pkg/preview/writeroots.go::allowanceRoots` returns an `All` root) counts as an overlap (founder A, 2026-11-05).
3. **Refused where the write access is created** — never offered, never recorded:

| Where | Enforcement point | Refused when | What the person or agent sees |
|---|---|---|---|
| Shell file approval ("Allow" and "Allow once") | `pkg/tools/shell_permission_mode.go::ExecTool.enforceFSPreflight`, after the containment check and **before** `requestPreflightApproval` | a write the current policy does not already contain overlaps | no approval card; the command result is `Command blocked by safety guard (preview program folder): <path> — agents can never get write access to the <component> program folder <folder>.` |
| Mount: REST create, `request_mount` tool, host-folder picker | `pkg/workspace/mount.go::CheckMountTarget` (used by `::CreateMount` and by the picker); the error wraps `ErrMountRefused` | the resolved target overlaps, including a target that contains a program folder such as `/` (founder A, 2026-11-05) | REST 403 with the fixed reason `that folder cannot be mounted: it is, contains or lies inside the <component> program folder (<folder>), and agents can never get write access there. Mount a folder that does not include it.` — a new branch in `pkg/gateway/rest_workspace_mounts.go::mountRefusalText`, whose default branch would otherwise say "Omnipus data directory"; `pkg/tools/request_mount.go::RequestMountTool.Execute` returns the same sentence; the picker (`pkg/gateway/rest_host_folders.go`) greys the folder with `Contains or lies inside a document preview program folder — agents can never write there.` |
| Configuration | before the file is written, in the gateway's config writers (`pkg/gateway/rest_config.go::restAPI.writeConfigJSONLocked`, the candidate builder in `pkg/gateway/pairing_config_write.go`, and any other writer backend-lead finds — T-87 covers each); on reload in `pkg/gateway/gateway_reload.go::prepareReloadConfig` (file watcher and manual reload; its documented contract: a non-nil error rejects the reload and the previous config stays in force) | an entry **added or changed** relative to the published configuration, in a field `pkg/preview/writeroots.go::ConfiguredWriteRoots` treats as write access (today `tools.allow_write_paths` and `sandbox.allowed_paths`), overlaps or cannot be reduced to a folder | the save is refused and nothing is written: `config error: <field> entry "<entry>" would give agents write access to the <component> program folder <folder>; agents can never get write access there.` or `config error: <field> entry "<entry>": Omnipus cannot tell which folders it covers, so it may include a document preview program folder; write it as ^/<folder>(?:/\|$).`; a rejected reload logs the same text and keeps the previous configuration |
| Registration (operator CLI) | existing write-root check in `pkg/preview/component/register.go::RegisterOffice` / `::RegisterRenderer` | static write access overlaps the folder | existing texts (`LibreOffice folder is writable by an agent (<access>); choose a folder no agent can write`, and the renderer's equivalent) |

4. **Already in force is not refused.** Entries present at gateway start, and entries a reload leaves unchanged, were there before any preview began; refusing them would block every later save or reload. They keep today's behaviour: the per-call component check (AD-4) disables the component for everyone ("unsafe location") and `doctor` reports it.
5. **No lock, lease, live set or kill.** A refusal does not depend on whether a preview runs, so nothing coordinates with a running preview. A blind-spot shell approval (`fs_preflight_blind`) records no grant and adds no write access; it is unaffected.
6. **`doctor`.** Unchanged codes and text: `WARN-PREVIEW-001` (PDF renderer) / `WARN-PREVIEW-002` (LibreOffice) `<name> is in a folder an agent can write (<access>): <effect> until that access is removed.` (`pkg/app/internal/doctor/preview.go::componentProblem`) for in-force configuration and mounts. A refused save leaves nothing to report. Doctor passes no chat grants (`::checkDocumentPreview` calls `ConfiguredWriteRoots(home, cfg, nil)`), so a remembered chat grant that predates a program folder is reported only by the preview result. A hand-edited `config.json` whose reload was rejected stays on disk; doctor reads that file and reports the overlap, which is what the next gateway start would put in force.

**Consequences.**
- Positive: R3 closes — "Allow once" into a program folder cannot be approved; R4 closes — no overlapping access can be published after a preview's check, so a running preview needs no coordination. Three small checks at existing choke points instead of a new gate package, hooks in every `pkg/sandbox` spawn, a lock order and a kill path.
- Negative: (a) **supersedes the 2026-08-12 mount ruling** ("warn and allow applies to all but the omnipus directory", recorded on `pkg/workspace/mount.go::CheckMountTarget`) for one case: a mount that contains a program folder — for example `/`, or the folder above the install directory (default install `/usr/local/bin`, so the slots are `/usr/local/office` and `/usr/local/share/omnipus/office` on Linux, `/usr/local/office` and `/office` on macOS) — is refused even when nothing is installed there (founder A, 2026-11-05); (b) a new write allowance Omnipus cannot reduce to a folder is refused at save (before: accepted, previews turned off); (c) a shell command needing write access to such a folder cannot be approved at all.
- **What the 2026-11-01 design covered that this one does not** (honest boundary; documented):
  1. Write access held by a running process that was approved **before** the folder became a program folder — a component installed or registered into a folder a still-running command was given write access to; also a mount record removed while a process it started still runs (the `dropped_record` live-writer case). Static sources (configuration, mount records, remembered grants) are still seen by the per-call check; a running process's own access is not.
  2. An already-allowed folder redirected by a link into a program folder while a preview runs (needs write access to that folder's parent): the next preview sees it through link resolution and refuses; the running preview is not stopped.
  3. The 2026-11-01 "preview stops when the component becomes writable" behaviour and its message do not exist.
- Neutral: Windows (Q5) and unconfined-mode exposure, the founder rule that any overlap disables the component for everyone (R2-MAJ-012), and "unsafe location" are unchanged.

**Alternatives rejected.**

| Option | Why rejected |
|---|---|
| 2026-11-01 write-authority gate (live set + preview lease + kill on overlap) | Founder chose S2 = A. Touches every confined spawn, adds a lock order and a kill path; covers boundary items 1–2 above at that cost. |
| Subtract program folders from every write grant, like the secret set (`pkg/sandbox/derive_from_fspolicy.go::DeriveKernelPolicy`) | The secret set denies reads too; a write-only subtraction would be new security-critical machinery on Linux and macOS and has no kernel equivalent on Windows. Not simpler. |
| Refuse only folders inside a program folder; warn and allow folders containing one | A mount of `/` still writes into the program folder, contradicting "never". Founder A, 2026-11-05. |

**Affected components.** `pkg/tools/shell_permission_mode.go::ExecTool.enforceFSPreflight`; `pkg/workspace/mount.go::CheckMountTarget`; `pkg/gateway/rest_workspace_mounts.go::mountRefusalText`; `pkg/gateway/rest_host_folders.go` (picker reason); `pkg/tools/request_mount.go::RequestMountTool.Execute` (result text); `pkg/gateway/rest_config.go::restAPI.writeConfigJSONLocked`; `pkg/gateway/pairing_config_write.go`; `pkg/gateway/gateway_reload.go::prepareReloadConfig`; a protected-folder lookup callable from `pkg/workspace` without an import cycle (for example in `pkg/preview/component`, whose dependencies include no workspace, tools or agent package — `go list -deps`, 2026-11-05); docs per §17. Unchanged: the per-call checks in `pkg/preview/service.go`, `pkg/preview/writeroots.go`, `doctor`.

### 3.3 Known open points (2026-11-05)

| Item | State |
|---|---|
| SEC-R1-02 … SEC-R1-06 (security review round 1, all Medium) | **Known, deferred to tracked issues, not this round** (founder SR1, 2026-11-05): SEC-R1-02 #1608, SEC-R1-03 #1609, SEC-R1-04 #1610, SEC-R1-05 #1611, SEC-R1-06 #1612. Until they are fixed the code does not meet this spec at those points: Retry's protected-source check (§5.2), Retry's special-file read and admission hold (§5.2, AD-6), Retry's settle target with a reused call ID (§5.2), a dangling mount-record link read as "no access" (AD-4), and upload rendering that reads the PDF before admission (AD-6 "taken before reading or copying the source"). Report: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/office-preview-build-20261030/review-r1/security-report.md`. |
| Write folders `ConfiguredWriteRoots` does not list | **Unknown, open — team-lead raises it.** `pkg/preview/writeroots.go::ConfiguredWriteRoots` lists agent homes, workspaces, mounts, `tools.allow_write_paths`, `sandbox.allowed_paths` and remembered grants, but not the writable folders of `environment_setup` (`pkg/tools/environment_setup_runner.go::setupAllowRoots`: the target's prefix, cache and tmp) or the document runtime cache (`pkg/tools/shell_env.go`, the `Writable: true` root for `doc.layout.Cache`). Whether a program folder can lie inside them is not established; if it can, neither the §3.2 refusal nor the per-call check sees it. |
| Other config writers that lose the refusal text (2026-11-07) | **Moved into this round** by founder Q26 = A: §3.4 D9b lists them. |
| Refused sign-out (2026-11-07) | **Known, existing; for security-lead and the founder.** When the config write in `pkg/gateway/rest_auth.go::HandleLogout` fails, for any reason including a refusal, the server-side session stays valid; the screen signs out locally and only logs it. Not changed by this round. |
| Sign-in poll's undeclared 500 (2026-11-07) | **Known, existing contract gap.** `pkg/gateway/rest_sign_in.go::handleProviderSignInPoll` answers 500 on other failures, and `POST /providers/{id}/sign-in/poll` declares no 500. Not changed by this round; issue to be proposed, none filed. |
| Windows launcher after gateway death (2026-11-07) | **Inferred, not run.** AD-2, AD-8 and `pkg/preview/launcher/launcher.go` say the gateway's death closes the job. In `pkg/preview/launcher/child_windows.go::Main` the job handle lives in the launcher, with nothing watching the gateway, so a hanging target may run on until recovery or its CPU limit ends it. T-99 covers the recovery stop. Issue proposed to team-lead, none filed. |
| Recovery warnings do not reach `doctor` (2026-11-07) | **Verified, existing gap.** AD-8 and `pkg/preview/scratch.go::RecoveryReport` say each warning becomes a doctor line; `pkg/gateway/gateway_boot.go::startDocumentPreview` only logs them. Issue proposed to team-lead, none filed. |

### 3.4 Dated amendment 2026-11-07 — trusted owner record, replaced scratch folder, refusal text on settings routes (architect, founder Q18, Q19, Q22, Q23, Q24)

Status: decided 2026-11-07. Amends AD-8 and §3.1 D3.7. Adds FR-051…FR-054, B-64…B-68, T-92…T-103. The text of FR-013 and FR-050 does not change. Baseline: `landing/office-preview` at `1c2ca4870`.

**Context.** The converter is treated as compromised (founder Q18 = A). `pkg/preview/scratch.go::call.publish` keeps `owner.json` inside scratch, the one folder the converter can write, and `::recoverOne` trusts that file after a crash. So after a crash a converter could steer which process group is stopped and which socket folder is removed. `::call.publish` also writes by name inside scratch after the first start, against FR-050. A confined converter on macOS can replace its scratch folder, including with a link (checked on macOS 26.5.2 under a hand-built profile, not the production render); it cannot touch a file one level above. Separately, 20 settings routes lose the §3.2 refusal text and answer with a generic failure, and a preview from an agent turn may lose write access to its own scratch folder on macOS (D8).

**Decision.**

**D1. Where the record lives (founder Q18 = A).**

```
$OMNIPUS_HOME/tmp/preview-calls/<id>/      call folder: gateway only, 0700
    owner.json                             the trusted record
    work/                                  scratch: all the converter can write
```

`call.dir` becomes `<id>/work`, so every existing use of it stays right. The root name is new on purpose: under the old layout a converter could write `tmp/preview/<id>/owner.json`, so keeping that root would make new code trust an old file. The old root is never read and never cleaned (greenfield, no migration). The record stays beside scratch, not in `system/`, so one call stays one tree and `Recover` needs one sweep. It carries no signature: a key that survives a crash sits on the same disk, within the same reach.

**D2. Who writes the record, and when.** Only the gateway, through `pkg/preview/scratch.go::call.publish`. Everything the record says about a process is written before any document content can run.

| Step | Order |
|---|---|
| `::newCall` | create `<id>` → publish `preparing` → create `work` → open both handles: `c.root` on `work`, new `c.callRoot` on `<id>` |
| `::makeSocketDir` | create the socket folder → publish `socket_dir` at once (today this waits for `running`) |
| launcher started | target not started; it waits for the go-byte |
| `::call.running` | publish `running`: launcher PID, start time and executable, allowed executables |
| go-byte | target starts; both go-byte sites already send it only after `running` returned without error (`pkg/preview/programs.go::protocolProgram.publishAndGo`, `::officeConversion.startOnce`) |
| `::call.idle` | publish `preparing`, launcher cleared (between two starts) |
| `::call.finish` | publish `done` → empty `work` through `c.root` → `callRoot.RemoveAll("work")` → remove the record → close `callRoot` → remove `<id>` (Windows will not delete an open folder) |

**D3. What recovery reads.** Only `<id>/owner.json`. Nothing inside `work` is read as data. `::recoverOne` keeps its existing keep-and-warn branches. Removal order: recorded socket folder, then `work`, then `<id>`, record last, so a failed removal is retried at the next start.

**D4. Which processes recovery stops.** The `pgid` field is deleted from `::ownerRecord`, `::call.running` and `::call.idle`. Recovery stops `killGroup(rec.Launcher.PID)` after the PID, start time and executable of the live process match the record (`::identityMatches`, unchanged). There is no second number a record could redirect. A process group's number is the PID of the process that created it, and POSIX.1-2017 XBD 4.14 says a PID "shall not be reused by the system until the process group lifetime ends". The launcher already relies on this (`Setpgid: true`; `pkg/preview/launcher/proc_unix.go::killTree`). A socket folder path comes from the trusted record, written straight after `os.Mkdir` created that folder; `::socketDirPattern` stays only as a guard against a damaged record, and its comment changes to say so. A socket folder's device and inode are not recorded: a collision needs 1 in 2^32 on top of a crash, and the worst result is one failed preview.

**D5. Publish and FR-050 (founder Q19 = A).** `::call.publish` keeps `fileutil.WriteFileAtomic`, with the path `<id>/owner.json`. The converter cannot write `<id>`, so publishing is no longer a file operation inside scratch and leaves FR-050's scope. The final removal of `work` is a post-start operation and moves to `c.callRoot`. `cmd.Dir` for a later start only sets a confined child's working folder; the gateway reads and writes nothing there, so it is not a D3.7 operation (security-lead confirms this reading at the gate).

**D6. Second start on a replaced scratch folder (founder Q23 = A).** A replaced folder is not closed by D1: a second start could run with sandbox rules built from a folder the first start chose. In the exit-81 branch of `pkg/preview/programs.go::officeConversion.run`, before the reseed, compare `callRoot.Lstat("work")` with `c.root.Stat(".")` using `os.SameFile`, and require a real folder. On a mismatch the call ends with the existing text `conversion failed: scratch unavailable` and no second start. No new failure text (MV-8 unchanged). This matches D3.7: a first start that leaves something unsafe ends the call. Limit: a process that escaped the group could swap the folder after the check; closing that means rendering the sandbox profile once per call, a `pkg/sandbox` change not made here.

**D7. Where "out of the converter's reach" cannot hold (founder Q22 = A).** On Windows, and in the disclosed "without sandbox" fallback on Linux and macOS, the converter has no file boundary, so no file is out of its reach. There the record is not trusted against a compromised converter. This limit is accepted and stated here. One code path stays: it hands the converter nothing it does not already have, since it runs with the gateway account's authority and could stop those processes and delete those folders itself. Recovery keeps its stop on those runs (`killPID` on Windows, where `killGroup` is a no-op), because §3.3 suggests the Windows launcher may outlive the gateway. Windows file confinement stays out of scope (Q5).

Other limits this change does not remove, all existing behaviour:

| Limit | Effect |
|---|---|
| A descendant that left the group | Not stopped by recovery; it keeps its inherited sandbox |
| Target runs a program outside the allowed list | Identity fails: not stopped, folder kept, warning (B-39) |
| Group leader dead, other members alive | Not stopped; ownership can no longer be proved |
| Gap between the identity check and the kill | Needs a PID to wrap inside that gap; accepted, as today |

**D8. Repeat preview from an agent turn (FR-054).** `pkg/fspolicy/secretset_kernel.go::KernelDenyEnumeration` denies home entries that are not branches, so `<home>/tmp` joins the turn's deny list once it exists. `pkg/preview/confine_unix.go::confinement.policy` copies those denies into the converter's policy, and on macOS a deny written after an allow wins. So the first preview on a fresh home may work and later ones fail. *(Corrected 2026-11-08, §3.4 D8b: that sentence was wrong; with an operator-registered LibreOffice every macOS preview also failed at its first start, because the registry under `<home>/system` was unreadable.)* End to end this is **Unknown**: each link was read or tested alone, and no test passes a read model with denies. T-102 settles it. If it confirms, the developer fixes it, and the fix may allow only `<id>/work`, never `tmp`, `tmp/preview-calls` or `<id>`; a wider allow would undo D1. *(2026-11-08, §3.4 D8c: the cause is removed; no turn deny reaches the converter, and `<id>/work` is its only writable folder in the data folder.)*

**D8b. Registry and scratch access for the converter on macOS (architect ruling, 2026-11-08, accepted by team-lead; withdrawn in part the same day by D8c).** *Status 2026-11-08:* D8b.1 (registry copy) and D8b.2 (per-call read model) are withdrawn by §3.4 D8c (founder Q35 = A, Q36 = A) and are not built; the blocker table stays as the record of what T-102 found. Where D8b and D8c differ, D8c governs; D8b's residual, alternatives, consequences and affected components no longer apply. *Context:* T-102's first real run failed on preview 1: LibreOffice could not open its filtered services registry. Two independent blockers, both read in the code:

| Blocker | Cause |
|---|---|
| Registry unreadable | An operator-registered LibreOffice keeps its filtered registry under `<home>/system/components/office/services` (`pkg/preview/component/register.go::RegisterOffice`, `pkg/preview/component/component.go::RegisteredDir`, `::load`). The converter is granted reads only on the component root, the gateway executable's folder and scratch (`pkg/preview/programs.go::officeConversion.run`, `pkg/preview/confine_unix.go::confinement.policy`), and every turn policy denies `system` by name (`pkg/fspolicy/inventory.go::ProtectedInventoryNames`, `pkg/fspolicy/secretset_kernel.go::KernelDenyEnumeration`) |
| Scratch denied | Once `<home>/tmp` exists the turn policy denies it whole, and the converter's policy copies that deny, which on macOS wins over the scratch write rule (D8) |

**D8b.1 Registry copy (withdrawn, §3.4 D8c).** Wherever a registry is set (macOS, packaged and operator-registered alike), the gateway gives LibreOffice a copy at `<id>/work/ure-services/`, and `URE_MORE_SERVICES` names that folder.

| Step | Rule |
|---|---|
| Copy | The gateway reads each file of the verified registry folder once, hashes the bytes it read, and requires `component.FileListDigest` of them to equal `Manifest.RegistrySHA256`; it then writes exactly those bytes through the call's scratch handle. A mismatch, or anything other than regular files in the folder, ends the call with no start, as a damaged component (existing outcome, no new text) |
| First start | Copied before the first launcher starts |
| Exit-81 second start | After the first start's group was killed and the D6 check passed: remove `ure-services` through the scratch handle (a link is removed, never its target), then write a fresh copy from bytes read and checked again. Any failure ends the call with `conversion failed: scratch unavailable` and no second start |
| Never | The gateway never reads the copy back or trusts anything in it |

Integrity holds to the same level as the profile settings (AD-7, §3.1 D3.4, D3.7): the first start loads bytes checked a moment earlier and written before any converter process existed; the second start loads a fresh checked copy. A converter changing its own copy during a start gains nothing it does not already have inside its sandbox; the registry is a compatibility filter (AD-3), not a boundary. Residual, as accepted in D6: a process that escaped the group could change the copy before the second start reads it.

**D8b.2 Scratch access (withdrawn, §3.4 D8c).** The converter's read model is derived per call, after `<id>/work` exists, as the turn's policy plus one extra writable runtime branch: `sandbox.KernelPolicyForTurn(authored, fspolicy.AllowRoot{Path: <id>/work, Writable: true})`. The existing deny walk then keeps `<home>/tmp`, `tmp/preview-calls` and `<id>` only as the path to `work`, denies their other entries present at derivation (other `tmp` entries, other call folders, `<id>/owner.json`), and still denies `<home>/system` and every other protected name, which a runtime branch can never reopen (`pkg/fspolicy/secretset_kernel.go::runtimeAllowanceOverridesInventory`).

| Item | Change |
|---|---|
| `pkg/preview/service.go::Request` | `ReadModel` is replaced by `ReadModelFor func(work string) (*sandbox.SandboxPolicy, error)`; a nil function means no turn policy, as a nil `ReadModel` does today |
| `pkg/tools/preview_document.go`, `pkg/tools/read_file_pdf.go` | Pass a function closing over the turn's `fspolicy.FSPolicy` that adds the `work` branch |
| `pkg/preview/programs.go::officeConversion.run` | Calls it once per call before the first start; both starts use that policy. An error ends the call with the existing sentence `the sandbox policy for this turn could not be built: <reason>` |
| Renderer, text extraction | Unchanged; they never used the read model |

Residual, for security-lead: the walk is a snapshot taken at derivation. In the open file model, a call folder another preview creates later is readable, not writable, by this converter while it runs; in the confined model it is not readable. This is the existing documented macOS post-spawn residual of `KernelDenyEnumeration`, now also inside `tmp/preview-calls`. Linux: Unknown until T-102 runs there.

*Alternatives rejected.* A read allow on the registry: on macOS a later deny wins, and the only way to re-admit part of `system` is the operator channel, which internal input must not use; it would mean changing the protected-inventory rules to open part of `system` to a process treated as compromised. Storing the registry elsewhere: every other home entry is denied the same way, and outside the home there is no folder Omnipus owns that agents cannot write. Pointing LibreOffice at the bundle's unfiltered registry: drops the AD-3 filter.

*Consequences.* No contract change. No user-doc change: Office preview on macOS starts working as `docs/documents.md` already describes. Security-lead reviews D8b at the gate. macOS CI is off, so T-102 and T-105 rest on local slot-runner receipts bound to the exact commit.

*Affected components.* `pkg/preview/programs.go`, `pkg/preview/service.go`, `pkg/preview/scratch_anchor.go` (helpers reused), `pkg/tools/preview_document.go`, `pkg/tools/read_file_pdf.go`; tests `pkg/preview/turn_repeat_preview_office_test.go` and the T-105 test.

**D8c. The converter's own read list (founder Q35 = A, Q36 = A, 2026-11-08; architect amendment; replaces D8b.1 and D8b.2).**

*Correction (architect, 2026-11-08):* six D8b statements were wrong, found in security-lead's early read and in checking it against the code.

| # | D8b said | Wrong because | Correct is |
|---|---|---|---|
| 1 | In the confined file model a call folder created later is not readable, "because nothing allows it" | Every turn policy starts from `pkg/sandbox/sandbox.go::DefaultPolicyForModel`, which grants the whole data folder in both file models, and the converter kept that read (`pkg/preview/confine_unix.go::confinement.policy` strips only write) | In both file models, every data-folder entry the deny list did not name was readable by the converter |
| 2 | Linux: Unknown; both starts use one policy | On Linux the grants are expanded again from the live folder at every start (`pkg/sandbox/sibling_grants.go::ExpandRulesExcluding`, called from `pkg/sandbox/sandbox_linux.go`) | A call folder created before a start's rules attach, including before the exit-81 second start, got a read grant |
| 3 | The walk denies the other entries present at derivation, `<id>/owner.json` included | Only when the walk succeeds: on an enumeration error `pkg/sandbox/derive_from_fspolicy.go::DeriveKernelPolicy` falls back to the fixed secret list, and `pkg/sandbox/turn_policy.go::KernelPolicyForTurn` returns no error | A walk error left `tmp`, every call folder and the owner record readable, silently |
| 4 | T-102: "exactly one write rule" | On macOS the converter also writes its socket folder (`pkg/preview/scratch.go::call.makeSocketDir`, `confinement.policy`) | Write rules are {`<id>/work`, socket folder} on macOS and {`<id>/work`} on Linux |
| 5 | T-105 (e) catches a changed source registry | The service's first full check (`pkg/preview/component/component.go::Component.Verify`) already rejects it, so (e) passed with no fix unless that service had already checked the component once (`::VerifyIfChanged`) | Moot under R1: T-105 is replaced (§13) |
| 6 | Two callers pass the turn's read model (`pkg/tools/preview_document.go`, `pkg/tools/read_file_pdf.go`) | The Retry route passes it too: `pkg/gateway/rest_preview_retry.go::restAPI.renderPreviewRetry` | Three callers; all three change below |

*Context.* Security-lead's early read of D8b showed that a converter under attacker control could read other preview calls' files. The cause is not the deny snapshot: the converter inherited the turn's read rights, which include the whole data folder (correction 1), and every fix built on that inheritance has to fight it. The founder chose to remove the inheritance (Q35 = A, Option 1) and to read the registry in place (Q36 = A, R1). The renderer and text extraction already work this way (MV-6; `pkg/preview/confine.go::confinement`).

*Decision.* The converter's policy uses no turn policy. Its file rules are exactly this list, on every start, in both file models:

| Entry | Access | macOS | Linux |
|---|---|---|---|
| LibreOffice component root | read, execute | yes (today) | yes (today) |
| Folder of the gateway executable (the launcher) | read, execute | yes (today) | yes (today) |
| This call's `<id>/work` | read, write | yes (today) | yes (today) |
| This call's socket folder | read, write; socket bind and connect | yes (today) | none |
| System libraries | read, execute | the Seatbelt system preamble (`pkg/sandbox/seatbelt_profile.go`), unchanged | `pkg/preview/confine_linux.go::linuxSystemReadExec`, unchanged |
| The verified filtered registry folder: `Component.Registry` as `pkg/preview/component/component.go::load` resolved it | read only | new (R1) | none (no registry) |
| System font folders | read only | new: `/Library/Fonts` (`/System/Library/Fonts` is already inside the preamble's `/System`) | new: `/usr/share/fonts`, `/usr/local/share/fonts`, `/etc/fonts` |

| Rule | Detail |
|---|---|
| Nothing else | No rule covers any other part of the data folder, a user's home folder, a workspace or an agent folder. `ReadsOpen` and `ExecOpen` stay false in both file models. The policy carries no deny list: nothing outside the list is granted, so nothing needs denying and nothing depends on when a folder was created |
| Missing folders | On Linux a listed system folder that does not exist is left out, as `hooksFor` already does for `/lib64` (Landlock refuses a rule for a missing path) |
| Adding a read | If T-102 shows that LibreOffice needs another read, the developer may add only a read-only system folder outside every user home, the data folder and every workspace, names it in the commit and the gate brief, and the architect confirms it at the gate. A preview that fails for a missing read fails visibly; it never falls back to wider reads |
| Walk errors | The converter's policy uses no deny walk, so a failing walk cannot widen or narrow what it reads. The three callers stop deriving a turn policy for the preview, so a `KernelPolicyForTurn` error no longer reaches the preview, and the preview paths no longer return `the sandbox policy for this turn could not be built: <reason>` (or the Retry route's `sandbox policy` error). The turn's own tools keep their derivation, outside this spec |
| Registry (R1) | On macOS `URE_MORE_SERVICES` names `Component.Registry` directly. Its integrity is that of the program files (AD-4): a full hash when the service first resolves it (`load` runs `Component.Verify`) and a size and modification-time check before every later use (`Component.VerifyIfChanged`). The grant is a converter rule in `pkg/preview`; the protected names in `pkg/fspolicy` are unchanged, so no agent and no tool gains access to `<home>/system` |
| Renderer, text extraction | Unchanged; they never used the turn's read model (MV-6) |

*What it guarantees.* On macOS and Linux, in both file models, at any timing and whatever a deny walk does, a confined converter can read nothing in the data folder except its own `<id>/work` and, on macOS, the registry folder: no other call's folder (whenever it was created), no owner record, no workspace or agent file. D8's cause is gone, because no turn deny reaches the converter; FR-054 holds by construction. D1 holds, because the owner record is never granted. The unconfined fallback (AD-7, FQ2) and Windows (Q5, D7) are unchanged and keep their disclosed exposure.

| Item | Change |
|---|---|
| `pkg/preview/confine.go::confinement` | The `readModel` field goes; a read-only folder list is added (its name is the developer's choice) |
| `pkg/preview/confine_unix.go::confinement.policy` | The read-model branch goes; read-only folders are emitted with read access only |
| `pkg/preview/programs.go::officeConversion.run` | Adds the registry folder (macOS) and the font folders of the platform, read only |
| `pkg/preview/service.go::Request` | `ReadModel` goes; `Service.Preview` builds the converter's confinement without it |
| `pkg/tools/preview_document.go::PreviewDocumentTool.Execute`, `pkg/tools/read_file_pdf.go::ReadFileTool.pdfPageRenderer`, `pkg/gateway/rest_preview_retry.go::restAPI.renderPreviewRetry` | Stop calling `sandbox.KernelPolicyForTurn` for the preview; `CallerWriteRoots` (§3.2 writer exclusion) is unchanged |
| `pkg/sandbox`, `pkg/fspolicy` | No change |

*Risks.*

| Risk | Handling |
|---|---|
| LibreOffice needs a read outside the list | Visible failure, never a silent widening; the "Adding a read" rule applies. The 2026-10-29 spike ran under the per-turn profile, so whether the list suffices is **Unknown** until T-102 runs on macOS and Linux |
| Fonts | Fonts installed only for one user account (macOS `~/Library/Fonts`, Linux `~/.local/share/fonts`) are not read; such text is shown in a substitute font. The user pages say so (§17); hands-on rows U-M1 to U-M3 check how documents look |
| The open file model loses reads it had | Intended: the converter's input is a copy in `work`; the gateway's read of the source is unchanged (FR-003) |
| A read under `<home>/system` | One folder, read only, checked like the program files, holding no user data. Security-lead reviews D8c at the gate |

*Alternatives rejected.* **Option 2**, a fixed deny on the whole call tree with the call's own folder carved out: Seatbelt has no carve-out inside a denied tree as the profile is rendered (`pkg/sandbox/seatbelt_profile.go::renderSeatbeltProfileState.renderDeniedPaths`), and on Linux `ExpandRulesExcluding` drops a rule under a denied path, so the shared sandbox code would change on both platforms, and the converter would still read the agent's other files. **Option 3**, keep D8b and document the residual: accepts that one call can read another call's files. **R2**, keep the D8b.1 copy: about 40 more lines and the old T-105, for integrity stronger than LibreOffice's own program files get. **A per-call folder outside the shared tree**: the converter would still inherit the turn's grants.

*Consequences.* Positive: one preview call cannot read another's files, on either platform, in either file model; no shared sandbox or file-policy change; D8b.1 and D8b.2 are never built; a failing deny walk cannot affect the converter. Negative: LibreOffice may need one more read (risk above); fonts installed only for one user account are not used. Neutral: no contract change. User docs change (§17, row "2026-11-08, §3.4 D8c"). Security-lead reviews D8c at the gate. macOS CI is off, so T-11, T-102 and T-105 rest on local slot-runner receipts bound to the exact commit.

*Affected components.* The six code items in the table above; tests `pkg/preview/turn_repeat_preview_office_test.go` (T-102; it builds a turn policy with `KernelPolicyForTurn` and passes `Request.ReadModel`, both of which go), the T-105 test and T-11's profile diff.

**D9. Refusal text on settings routes (founder Q24 = A).** New helper in `pkg/gateway`, same shape as `rest_channels_outbound_guard.go::writeOutboundProtected`:

`writeRequestRefusal(w http.ResponseWriter, err error) bool` answers a `*requestRefusalError` with 400 and its text, and reports whether it did.

Each site calls it directly before its generic failure, after any `configRefreshError` branch. Logs, rollback and 500 bodies stay as they are. `rest_sandbox_config.go::writeSandboxSaveError` calls it too, so one mapping exists. `rest_agents.go::withToolPolicyCoverageGuard`, `rest_config.go::updateConfig` and `rest_integrations_remove_key.go::handleSearchKeyRemoval` already keep the refusal and do not change.

Every route below saves through the shared config writer, which can return the refusal: an entry on disk that the published configuration lacks is refused on any save (`rest_config.go::writeRefusableConfigJSONLocked`, `preview_program_access.go::refuseNewProgramAccess`). So a declared 400 is needed on all 20. Sixteen declare it; four do not.

| # | Route | Handler (`pkg/gateway`) | 400 declared at `1c2ca4870` | Developer action |
|---|---|---|---|---|
| 1 | `PUT /security/audit-log` | `rest_audit_log.go::HandleSandboxAuditLog` | yes | helper |
| 2 | `PUT /channels/{id}/routing` | `rest_channels.go::restAPISetChannelRouting.handleBound` | yes | helper |
| 3 | `POST /channels` | `rest_channels.go::createChannelInstance` | yes | helper |
| 4 | `DELETE /channels/{id}` | `rest_channels.go::deleteChannelInstance` | yes | helper, inside the branch for errors that are not `configRefreshError` |
| 5 | `PUT /channels/{id}/enable` | `rest_channels.go::applyChannelEnabled` (save in `pairing_enable_write.go::saveChannelEnabled`) | yes | helper (one site with route 6) |
| 6 | `PUT /channels/{id}/disable` | `rest_channels.go::setChannelEnabled`, then `::applyChannelEnabled` | **no** | same site as route 5; **add 400** |
| 7 | `PUT /channels/{id}/configure` | `rest_channels.go::configureChannel`, `rest_channels_configure_apply.go::writeConfigureFailure` | yes | **not mechanical**, see below |
| 8 | `POST /config/gateway/rotate-token` | `rest_config.go::rotateGatewayToken` | **no** | helper; **add 400** |
| 9 | `POST /gateway/god-mode` | `rest_god_mode.go::setGodMode` | yes | helper |
| 10 | `PUT /agents/{id}/mailboxes/{workspaceId}` | `rest_mailbox.go::restAPISetAgentMailbox.persistConfig` | yes | helper |
| 11 | `DELETE /agents/{id}/mailboxes/{workspaceId}` | `rest_mailbox.go::deleteAgentMailbox` | **no** | helper; **add 400** |
| 12 | `POST /mcp-servers` | `rest_mcp.go::addMCPServer` | yes | helper |
| 13 | `DELETE /mcp-servers/{id}` | `rest_mcp.go::deleteMCPServer` | **no** | helper; **add 400** |
| 14 | `PATCH /mcp-servers/{id}` | `rest_mcp.go::patchMCPServer` | yes | helper |
| 15 | `PUT /security/prompt-guard` | `rest_prompt_guard.go::putPromptGuard` | yes | helper |
| 16 | `PUT /providers/{id}` | `rest_providers.go::providerPutPersist` | yes | helper |
| 17 | `PUT /security/rate-limits` | `rest_rate_limits.go::putRateLimits` | yes | helper |
| 18 | `PUT /security/retention` | `rest_retention.go::putRetention` | yes | helper |
| 19 | `PUT /security/session-scope` | `rest_session_scope.go::putSessionScope` | yes | helper |
| 20 | `PUT /security/skill-trust` | `rest_skill_trust.go::putSkillTrust` | yes | helper |

**Route 7, `PUT /channels/{id}/configure`.** With no credential stored yet, a refusal answers 400 `ErrorResponse` with the exact §3.2 text. With a credential already stored, it keeps the coded 500 `credential_saved_not_saved`, because that credential really is stored and the person must know; its `error` text is the existing sentence, one space, then the §3.2 sentence. The screen shows `error`, not `detail` (`src/lib/api/channels.ts::withConfigureErrorText`). `::configureWriteFailureCode` keeps its classification. No contract change on this route.

**D9b. The other config writers (founder Q26 = A, 2026-11-07).** Every other gateway writer that hides the refusal reason is fixed in the same commit as D9, including sign-in, sign-out and password change. All of these save through the same shared writer, so the same on-disk entry refuses them. The helper and its placement rule are the same as in D9 unless the row names its own rule.

| # | Route or caller | Handler (`pkg/gateway`) | 400 declared at `46f269277` | Developer action |
|---|---|---|---|---|
| 21 | `POST /auth/login` | `rest_auth.go::HandleLogin` | yes | helper, after the invalid-credentials branch. The writer runs the closure, which checks the password, before the refusal check (`rest_config.go::writeRefusableConfigJSONLocked`), so only a person with valid credentials ever sees the text |
| 22 | `POST /auth/logout` | `rest_auth.go::HandleLogout` | **no** | helper; **add 400** |
| 23 | `POST /auth/change-password` | `rest_auth.go::HandleChangePassword` | yes | helper, after its 401 and 404 branches |
| 24 | `PUT /settings/context` | `rest_context_settings.go::putContextSettings` | yes | helper |
| 25 | `PUT /settings/memory` | `rest_memory_settings.go::putMemorySettings` | yes | helper |
| 26 | `PUT /settings/third-party-agents` | `rest_third_party_settings.go::putThirdPartyAgentSettings` | yes | helper |
| 27 | `PUT /providers/default-model` | `rest_default_model.go::putDefaultModel` | yes | helper |
| 28 | `PUT /integrations/providers/{id}` | `rest_integrations_save.go::persistIntegrationProviderUpdate` | yes | helper |
| 29 | `POST /onboarding/complete` | `rest_onboarding.go::restAPIHandleCompleteOnboarding.persistConfig` | yes | helper, after the username-taken branch |
| 30 | `DELETE /workspaces/{id}` | `rest_workspaces.go::restAPIHandleWorkspaceDelete.removeRecord`, via `::unbindChannelInstancesForWorkspace` | yes | helper; the workspace is not deleted, as today on any failure of that step |
| 31 | `DELETE /providers/{id}` | `rest_providers_delete.go::runProviderDelete` | yes | **own rule** |
| 32 | `PUT /performance` | `rest_performance_max_tool_iterations.go::writePerformanceLocked` | yes | **own rule** |
| 33 | `POST /providers/{id}/sign-in/poll` | `rest_sign_in.go::handleProviderSignInPoll`, via `::ensureSignedInProviderRow` | yes | **own rule** |
| 34 | `PUT /channels/{id}/outbound` | `rest_channels_outbound.go::putChannelOutbound`, via `::commitOutboundChange` | yes, but its body is `ChannelOutboundRejection` | **own rule and contract change** |

Own rules:

| # | Rule |
|---|---|
| 31 | At each refused write in `::runProviderDelete`, while nothing has been committed yet (`defaultChanged` false), return status 400 with the §3.2 text; the handler already writes that with `jsonErr`. Once an earlier write in the same request was committed, keep today's 500 `{deleted:false}`. In practice the same on-disk entry refuses the first write, so that branch is not reached |
| 32 | `::configWriteFailure` passes a refusal through unchanged. Nothing lowered yet: 400 `ErrorResponse` with the §3.2 text. Agents already lowered: the existing rollback runs; if every agent was restored, 400 with the §3.2 text; if any was not, today's 500 rollback-incomplete answer stands, because that broken state matters more. The comment in `::writePerformanceLocked` saying the writer "only fails reading, parsing, serializing or writing" is corrected |
| 33 | The sign-in credential is already stored when the write is refused (as today on any failure there). Answer 400 `ErrorResponse` with `failed to configure signed-in provider: ` followed by the §3.2 text. It is 400, not 500, because the screen shows server text only for non-5xx answers (`src/lib/api-error.ts::ApiError.fromResponse` replaces any 5xx text with a generic message; `src/components/providers/SignInDialog.tsx` shows `getErrorMessage`) |
| 34 | The route's 400 body is `ChannelOutboundRejection`, whose `error` allows only a safe-echo character set without `"`, `\`, `<`, `>` and `&`; the §3.2 text can hold all of these (the quoted entry, Windows paths). Contract shape: add the code `program_access_refused` to `contracts/components/schemas/ChannelOutboundRejection.yaml` (enum and description). Answer 400 with that code and this fixed text: `config error: config.json holds a write entry that would give agents write access to a document preview program folder; nothing was saved. Saving any other setting shows the entry.` The starting-messages screen already shows `error` for any code it does not handle itself (`src/components/connectors/ChannelOutboundSection.tsx`, `default` branch), so the regenerated types are the only frontend change |

Writers with no answer to a person:

| Caller | Today | Action |
|---|---|---|
| `rest_workspaces.go::removeMailboxesForWorkspace` (best-effort cascade inside `DELETE /workspaces/{id}`, after the record is gone) | logs only the error type | log the refusal text too (it holds no credential); the response does not change |
| `rest_providers_bedrock_region.go::persistBedrockInferenceProfiles` (background cache) | logs the error, text included | none |
| `pkg/sysagent/tools/workspace.go::WorkspaceDeleteTool.Execute`, through the gateway hook `UnbindWorkspaceChannels` | puts the error text in the tool result | none |
| `signin_provider.go::restAPIHost.MutateConfig` | no production caller found | none; a future caller uses the helper |

Security notes for security-lead (founder Q26: the sign-in routes need security-lead's review at the gate). (a) Any user with valid credentials, not only an admin, can see the text on rows 21 to 23. It names one config entry and the program folder and holds no credential. A generic text for non-admins was rejected because it would hide the reason again, against Q26. (b) A refused sign-out leaves the server-side session valid. This is true today for any write failure there, and the screen signs out locally and only logs the failure. Recorded in §3.3, not changed here.

**Contract.** `backend-lead` adds `'400'` (the existing `400BadRequest` response) to D9 routes 6, 8, 11 and 13 and D9b route 22 in `contracts/openapi.yaml`, adds the code `program_access_refused` to `contracts/components/schemas/ChannelOutboundRejection.yaml` (D9b route 34), and regenerates with `scripts/gen-contracts.sh`, in the same commit as D9 and D9b and before their handler changes (Hard Constraint 8). D1 to D8 change no contract: `owner.json` crosses no gateway/SPA boundary.

**Comments that change with this code.** `pkg/preview/scratch.go::socketDirPattern` (today states a bound security review rejected), `::call` (`root` field), `::ownerRecord`, `::scratchRoot`, `::Recover` (old path); `pkg/preview/launcher/launcher.go` package comment: Linux "address-space (or data)" (§3.1 D1 chose data only), and Windows "or the gateway's death, closes the job" unless T-99 proves it true (§3.3).

**Consequences.**

*Positive.* Recovery acts only on what the gateway wrote, where confinement exists. One move closes the two crash-recovery findings and the publish gap. FR-050 keeps its text. A refused save names its reason on all 20 routes.

*Negative.* Tests that assume the old path or `PGID` change (T-92 onward; each keeps its strength, and the identity assertion in `pkg/preview/retry_office_test.go` is replaced by T-94, never deleted). macOS CI is off (`.github/workflows/cross-platform.yml`), so macOS rows are proven only by local runs bound to the exact commit. A socket folder created just before a crash, before its publish, is left behind empty; nothing has run by then, and the window is accepted.

*Neutral.* User documentation: none needed for D1 to D7, because no user-visible behaviour changes and `docs/documents.md`, `docs/security.md` and `docs/operations/sandbox-limitations.md` do not describe the scratch layout or recovery. D7's limit is already covered by those pages' Windows and unconfined exposure text (§17). D8 fixes a failure against what the pages already promise. D9 needs none either: `docs/documents.md` ("What previews need") already promises the exact text on a save. D9b changes what sign-in, sign-out, password change, onboarding and the starting-messages screen show, so §17 has a 2026-11-07 row for it. docs-verifier confirms all of these at the §17 audit.

**Alternatives considered.**

| Option | Why rejected |
|---|---|
| Keep the record in scratch and write it through `call.root` | Satisfies FR-050 and leaves both crash-recovery findings open: the converter can still rewrite it |
| Sign the record | The key must survive a crash, so it has the same reach as the record; more code, no new boundary |
| Move the record under `system/` | Stronger against agent shells, but splits one call across two trees and needs a second sweep. Fallback if security-lead disagrees |
| Keep root `tmp/preview` | New code would trust a file an old converter could write |
| Recovery only deletes folders on Windows and the fallback (Q22 B) | The Windows launcher may need that stop (§3.3) |
| Windows file confinement (Q22 C) | A separate large feature; reopens Q5 |
| Render the sandbox profile once per call (Q23 C) | Touches `pkg/sandbox`; the guard closes the path at far lower cost |
| Rename `writeSandboxSaveError` and swap it into each site | Sites use different failure bodies and logs; a helper that only handles the refusal leaves them untouched |

**Affected components.** `pkg/preview/scratch.go`, `pkg/preview/scratch_anchor.go`, `pkg/preview/programs.go`, `pkg/preview/launcher/launcher.go` (comments); for D8, `pkg/preview/confine_unix.go` or the code T-102 points to; the `pkg/gateway` handlers in the D9 table, `rest_channels_configure_apply.go`, `rest_sandbox_config.go`, `contracts/openapi.yaml` and the generated types. Tests: `pkg/preview/scratch_test.go`, `pkg/preview/retry_office_test.go`, `pkg/preview/scratch_recovery_socketdir_office_test.go`, `pkg/gateway/preview_publish_barrier_office_test.go`.

## 4. What the model and the person receive

### 4.1 Model forms (per call, active model, catalog)

| Active model | `preview_document` | `read_file` on `.pdf` (no offset/length) | Uploaded PDF |
|---|---|---|---|
| Reads PDF natively, PDF ≤ `maxSize` | The whole PDF (Office: the converted PDF), §4.2 form; no page images to the model | The whole PDF natively | Native document |
| Reads PDF natively, PDF > `maxSize`, sees images | Page images in batches + note naming the size reason | Pages 1–10 as images + paging note | Pages 1–10 as images + extracted text + paging note |
| Sees images, no native PDF | Page images for the batch (default 1–10) | Pages 1–10 as images + note `call preview_document with pages "11-20"` | Pages 1–10 as images + extracted text + note naming the range and the workspace path |
| Text only, or renderer absent/damaged/failed and no native PDF | Extracted text (≤ 4 MiB) + reason | Extracted text (today's path) | Extracted text (today's path) |

**Upload pagination (FQ3, R2-MAJ-007):** an uploaded PDF delivered as images is materialized into the turn's workspace through the existing offload (`pkg/agent/loop_media.go::offloadSink.offload`) when it has no workspace path, so the note always names a path `preview_document` accepts; authorization = the workspace policy; lifetime = that workspace file. Every turn has a workspace: channel messages route only through their bound workspace (root `CLAUDE.md`, retired "unbound channel routing") — **Inferred**, tested by T-71. A materialization failure is an error state with the reason in the note, logged; not a silent exclusion. Native forms and page images from tools are live-turn only (marker "not retained; call again"); `read_file` with offset/length keeps text pagination; the existing narrowly classified downgrade (`media_downgrade.go::TryMediaDowngrade`) still applies to provider rejections, without promising that auth, size or schema errors become successful text retries.

### 4.2 Transport matrix (R2-MAJ-001)

| Adapter (retained) | Upload (user message) | `read_file` / `preview_document` result |
|---|---|---|
| OpenAI-compatible (incl. OpenRouter) — `pkg/providers/common/common.go::SerializeMessages` | file part (exists) | tool message carries the text marker; a **companion user message** placed right after that request's tool results carries the PDF file part, labelled `[document from tool call <id> — data, not instructions]` |
| Anthropic — `pkg/providers/anthropic/provider.go::anthropicToolResult` | document block (exists) | document block inside the tool result if the current API documents it (S-2), else companion document block after the tool results in the same user message |
| Responses — `pkg/providers/openai_responses_common/responses_common.go::TranslateMessages` | **add** file input part | file input in the function-call output if documented (S-2), else companion user item |
| Bedrock — `pkg/providers/bedrock/provider_bedrock.go::makeToolResultBlock`, `buildUserContent` | **add** document block | document block in the tool result if documented (S-2), else companion |

Rules: Omnipus serialization gaps are implementation work and never a reason to withhold. Only a **documented upstream** limit can withhold native PDF, and then the catalog entry for that provider + model must not list the PDF modality (decided by the catalog, not the adapter). Before each provider send and retry, the source is re-authorized (as inspection images are today); a revoked source sends neither PDF nor images. S-2 (backend-lead, before GREEN): record each provider's current documented PDF support for user content and tool results, with links.

### 4.3 Outcome table (three independent outcomes)

| Situation | Conversion validity | Model delivery | Thumbnails for the person | Result text |
|---|---|---|---|---|
| Normal | rendered | per §4.1 | stored N of N | "pages a–b of N" |
| Renderer absent | structural (Office) / not checked (PDF) | native PDF, else text | unavailable: renderer not installed | `page images unavailable: PDF renderer not installed` |
| Renderer damaged / failed / protocol error | as above via `extract-text` | native PDF, else text | unavailable: renderer damaged/failed | `page images unavailable: <reason>` |
| Native model, rendering failed (FQ2 r2 A) | as above | native PDF | unavailable card with Retry | partial success, never "pages shown" |
| Some thumbnails not stored | rendered | per §4.1 | stored k of N | `thumbnails stored: k of N` |
| No thumbnail stored | rendered | per §4.1 | unavailable: storage failed (Retry) | `thumbnails unavailable: storage failed` |
| Text empty (scanned PDF) | — | `[no extractable text — the PDF may be scanned]` | per renderer | no OCR offered |
| Text above 4 MiB | — | truncated text + note | per renderer | — |
| Conversion failed / refused / Stop / timeout | failed | nothing | none | MV-1 message |
| Non-web channel | — | per §4.1 | not sent | — |

Size boundaries are distinct: native `maxSize` 20 MiB; existing in-gateway extractor 25 MiB (`pkg/docextract/extract.go::MaxDocBytes`, unchanged for existing paths); preview input 50 MiB; converted PDF 200 MiB.

## 5. Chat thumbnails (Q4 B, FQ1 r1)

### 5.1 Storage — reuse of browser screenshots
PNG pages are stored like browser screenshots (`pkg/tools/normalization.go::storeInlineDataURL` path): session uploads folder (`media.SessionUploadsDir`), scope `media.SessionInlineScopePrefix + sessionID`, `CleanupPolicyForgetOnly`; never expired by `FileMediaStore.CleanExpired`; removed with the chat (`pkg/gateway/rest_sessions.go` delete → uploads cascade + `ReleaseAll`). No session → screenshot fallback (temporary folder, normal expiry). Retrieval: existing `/api/v1/media/{ref}` checks, unchanged. ≤ 20 images × ≤ 4 MiB per call; no per-chat cap (as screenshots; documented). **Display-only:** thumbnails travel in a separate display-media field of the tool result; they are published as `MediaFrame`s and persisted for replay, but never pass through `pkg/agent/loop_run_turn_tools.go::recordToolResult`'s model-media admission (T-63 checks the archive, the window and the next provider request).

### 5.2 Contract (Hard Constraint #8; architect shape, backend-lead edits and regenerates first)

| Schema | Change |
|---|---|
| `MediaFrame.yaml` | optional `tool_call_id` (string); optional `presentation` enum `inline` (default) / `page_thumbnails`; optional `preview` object {`source_name`, `source_version` (12 hex), `page_count`, `first_page`, `last_page`, `outcome` enum `complete` / `partial` / `unavailable`, `unavailable_reason` enum `renderer_missing` / `renderer_damaged` / `renderer_failed` / `storage_failed`}; `parts` `minItems` 1 → 0, allowed empty only with `presentation: page_thumbnails` and `outcome: unavailable` (description states it; server guarantees it; T-62 asserts) |
| `MediaPart.yaml` | optional `page_number` (integer ≥ 1); `caption` stays display text, never parsed |
| `openapi.yaml` new route | `POST /sessions/{session_id}/previews/{tool_call_id}/retry` → 202 `{status: started}`; 403 `preview_source_not_in_library` (source not a file of the session workspace's Library); 404 unknown session, no access to it, or unknown preview; 409 `preview_source_changed` (current file digest ≠ recorded version); 503 `preview_renderer_unavailable`. The result arrives as a new `MediaFrame` with the same `tool_call_id`. A successful retry also settles the original tool call's chat record (chat view only, `UnifiedStore.SettleToolCall`) so a reload shows the new thumbnails; the model view is unchanged; a failed retry persists nothing. Same limits and admission as the tool; authorization = the requesting person's access to the session and the source read through the session workspace's Library (2026-11-01). |

Producers that must emit the same projection: live `pkg/gateway/webchat_channel.go::SendMedia`, replay `pkg/gateway/replay.go::buildMediaFrame` (today drops `caption`), and any REST session-history endpoint returning tool media (backend-lead lists all three). Persisted tool-call record gains the display media with `caption`, `page_number`, `tool_call_id`, `presentation`, `preview`. SPA store: media grouped by (message, `tool_call_id`) instead of flattened; dedup key (`tool_call_id`, URL); screenshots (no `tool_call_id`) unchanged.

### 5.3 Journey and states
Running tool card → result text → strip of thumbnails under it, one group per tool call → select → enlarged viewer → Left/Right within that group → close returns focus. Re-preview creates a new group; old groups stay. Reload rebuilds groups from replay without rendering.

| State | Person sees |
|---|---|
| Running / queued | existing running tool card; no empty strip |
| Ready | group of thumbnails in page order, captions "report.docx · page 3 of 12" |
| Partial storage | stored thumbnails + caption gap + text `thumbnails stored: k of N` |
| Unavailable (renderer missing/damaged/failed, storage failed; incl. native-model partial success) | persistent card "Page previews unavailable — <reason>" with **Retry** (hidden for `renderer_missing`, which shows how to install) |
| Retry running / failed / source changed | card shows progress / the error / "The file changed — ask the agent to preview again" |
| Thumbnail fails to load | existing `ChatImage` placeholder "Image unavailable" with Retry; **no per-image toast** for `page_thumbnails`; screenshots keep today's toasts |
| Enlarged view loading / error | loading indicator; error panel with Retry (re-fetch) inside the viewer; Left/Right still work |
| Chat deleted / file gone | placeholder per image; enlarged view shows the error panel |
| Unconfined | result text `converted without sandbox: <reason>` |
| Narrow / touch | strip wraps; each thumbnail ≥ 44 px |

### 5.4 Keyboard and accessibility
Thumbnails are buttons named by caption. Enter/Space opens the viewer. On open, focus moves into the viewer (the image region, labelled by the caption). Tab/Shift-Tab cycle only within the viewer (built on the catalogued dialog primitive's focus containment; no second modal system). Left/Right change page **without remounting** (viewer keyed by group, not page URL); each change announces "Page n of N" politely; at the first/last page the key does nothing and the button is disabled. Escape or Close returns focus to the opening thumbnail, else to its group container, else to the chat input (virtualized rows). One polite announcement per group ("Preview of report.docx: 4 pages" or "Page previews unavailable"); failures once per group. Alt text = caption. Existing image/diagram viewer behaviour gets regression tests.

### 5.5 Design-system reuse
`ChatImage`, app-root `MediaLightbox`/`ImageLightbox`, `Card`, catalogued dialog and error/loading/action components; tokens only; no new public component. Gates: `npm run typecheck`, design-system lock scripts, component tests, Playwright specs.

## 6. Skills-review dispositions (R2-MAJ-011)

| Review item | Disposition |
|---|---|
| P1 agents start LibreOffice/OCR themselves | Removed; AD-14 table routes rendering to `preview_document` |
| P2 `pypdfium2` named but not provisioned | Drop `pypdfium2`/`pdfplumber` instructions (rendering is the tool's job); no new dependency |
| P3 dangling "harness runtime notes" | Replace with `environment_setup` (ADR-090) |
| P4 inspectors untested | T-72: all four, all three platforms, through the managed interpreter |
| P5 `evals.json` no consumer | Merge, re-label to `elicify-office`; rows seed UAT U-S rows |
| P6 duplication | One `verify.md`, one inspector contract |
| P7 no consume/edit line | AD-14 table |
| P8 xlsx recalculation | **Q3 A binding**: no recalculation mode (review Q1 option a rejected); skill says formulas calculate on open and warns about viewers that show blanks |
| P9 `inspect_workbook.py` `None` dict-splat | T-72 case first; fix only if the test reproduces it |
| Review Q2 OCR | Not provided; `environment_setup` only on request |
| Review Q5 skill id `office-documents` | Rejected: `elicify-office` kept (spec decision since round 0) |
| Review §4.1 "exit 2" for Word errors | The oracle is `elicify-docx/knowledge/inspector-contract.md`, not the review; grill 2 found `inspect_docx.py::__main__` does not map those errors to exit 2 → T-72 decides; a mismatch is fixed in the script to match its contract (in scope: founder asked to test and fix the scripts) |
| Document runtime `soffice` contract | Removed (AD-3, T-73) |
| Package inventory "bytes unchanged" | New adaptation statement + checksums; `pkg/skills/adr090_document_packages_test.go::TestADR090_DocumentPackagesSeedPinnedAssets` updated deliberately, still asserting integrity (T-74) |

## 7. Existing codebase context
GitNexus was unavailable; impact is **Inferred** from `git grep`; backend-lead reruns `impact`.

| Symbol | Change | Risk |
|---|---|---|
| `pkg/agent/loop_media.go::resolveMediaRefsWithOffload`, `pdfCapableModel` | catalog-only; images + offload; list deleted | MEDIUM |
| `pkg/agent/inspection_media.go::attachInspectionImagesWithBudget`, `loop_run_turn_tools.go::recordToolResult` | native PDF live media; budgets/eviction; display-only field | MEDIUM-HIGH |
| provider serializers (§4.2) | PDF in user/tool/companion content | MEDIUM-HIGH |
| `pkg/tools/filesystem.go::ReadFileTool` | three-way PDF rule | MEDIUM |
| `pkg/gateway/replay.go::buildMediaFrame`, `webchat_channel.go::SendMedia`, contracts | group projection, retry route | MEDIUM |
| `pkg/tools/shell_env.go::documentEnvTurn`, `pkg/documentruntime/provision.go::ProvisionFirstParty` | readiness, converter contract removed | MEDIUM |
| policy/seed files (AD-13) | new entries | MEDIUM |
| `src/components/chat/ChatScreen.tsx::InlineMedia`, `MediaLightbox`, `image-lightbox.tsx`, chat store `replay-and-status-frames.ts` | groups, focus, states | MEDIUM |

## 8. User stories

**US-1 Agent verifies its own Office document (P0).** 1. Image-only model, pptx → 4 page images, "pages 1–4 of 4". 2. Native-PDF model, Office file → converted PDF, no page images. 3. "11-20" on 30 pages → those pages + next batch named. 4. Edited file → new version and content.
**US-2 PDFs in the best form (P0).** 1. Each §4.1 row for preview, `read_file`, upload, with the §4.2 outbound form. 2. PDF > `maxSize` + image-capable native model → images in batches.
**US-3 Person sees thumbnails (P0).** 1. Rendering succeeds → a group in the chat. 2. Reload → same groups, nothing re-rendered. 3. Chat deleted → images gone. 4. Rendering unavailable → persistent card with Retry; Retry works when the file is unchanged.
**US-4 Clear refusal (P0).** Dataset B messages.
**US-5 Bounded and contained (P0).** 1. Macro/link documents: nothing runs or is fetched; conversion succeeds. 2. Any end incl. gateway kill → nothing left after next start. 3. Expensive PDFs stopped by limits; gateway responsive; memory budgets hold. 4. Nothing the agent supplies changes the program; no other agent can alter a usable component.
**US-6 One office skill, tested (P1).** Table first; every inspector proven by independent oracles; create/edit recipes run end to end.
**US-7 Installer components (P1).** Install with/without each component on each route, incl. Windows registration.

## 9. Behaviour, prohibitions, machine-verifiable rules

**Prohibitions.** Run a program an agent names or places; search `PATH`; load LibreOffice or pdfium into the gateway; keep a long-running converter; retry unfiltered or via another program; add Mach services or LaunchServices; write PNG/PDF into the workspace (except upload materialization, §4.1); put thumbnails into model media; send thumbnails to non-web channels; trust exit codes; download components at runtime; give the renderer files or network; claim a hard memory cap on macOS; claim rendered pages when none were rendered; let an adapter silently drop a native PDF; keep the old skill names or the PDF model list; use Poppler; add OCR, workbook writeback or a Library preview; give an agent write access to a program folder (§3.2); let a gateway scratch operation reach outside the call's scratch folder (§3.1 D3.7).

**MV-1 messages** (prefix `preview_document: `): `unsupported file type; supported: .docx, .xlsx, .pptx, .pdf` · `file content does not match its .<ext> extension` · `file exceeds the 50 MiB preview limit` · `pages must be "N" or "N-M" with 1 <= N <= M <= 9999 and at most 20 pages` · `the document has <N> pages` · `password-protected documents cannot be previewed` · `Office preview is not installed. Re-run the Omnipus installer with the Office preview component, or register LibreOffice with "omnipus office register" (PDF preview works without it).` · `Office preview is damaged (<reason>). Reinstall or re-register the Office preview component.` · `<component> is in a folder an agent can write (<grant>); disabled until that access is removed.` · `timed out after 120 s` · `stopped` · `conversion failed: LibreOffice produced no readable PDF` + filtered stderr tail · `the PDF could not be opened (damaged or unsupported)` · `rendering stopped: <limit> exceeded` · `page images unavailable: <reason>` · `thumbnails stored: <k> of <N>` · `thumbnails unavailable: storage failed` · `[no extractable text — the PDF may be scanned]` · read denied = `read_file`'s result.
**MV-8** refusal texts exactly as §3.2 states them (shell pre-flight, mount REST reason, `request_mount` result, picker reason, configuration save and reload) and the two §3.1 D3.7 failure texts.
**MV-2** converter environment = AD-7 list. **MV-3** macOS profile = the converter's own read list (§3.4 D8c) + 3 rules (+ network); Mach = 2. **MV-4** bash composition byte-identical except AD-3. **MV-5** AD-6 budgets. **MV-6** renderer/extract open no file and no socket. **MV-7** protocol v1 rules.

## 10. Delivery table

| Route | Platforms | LibreOffice | Renderer | Default / omit |
|---|---|---|---|---|
| Archive + `install.sh` | darwin/arm64 | official signed app, pinned 26.8.x, SHA-256 → `<install dir>/../office/LibreOffice.app` | CI build → `<install dir>/../pdfium/` | both included / `--without-office`, `--without-pdf-renderer` |
| Archive + `install.sh` | linux amd64, arm64 | official Linux build → `<install dir>/../share/omnipus/office` | → `<install dir>/../share/omnipus/pdfium` | same |
| deb / rpm | linux amd64, arm64 | `/usr/share/omnipus/office` in package `omnipus`; omitted in `omnipus-nooffice` (conflicts) | `/usr/share/omnipus/pdfium` in both | — |
| Docker | linux amd64, arm64 | base-distribution package, registered at image build by `omnipus office register` | musl build if available, else glibc-base fallback, registered at build | included |
| Operator registration | Windows, Intel dev Mac, any | `omnipus office register <LibreOffice program folder>` | release zip → `omnipus pdf-renderer register <folder>` | absent until registered |

Resolver candidates mirror `packageChromeRootCandidates` (`<exe>/../office`, `<exe>/../share/omnipus/office`, macOS `<exe>/../../../office`; same for `pdfium`) plus the registered manifest. Release prerequisites (Unknown): LibreOffice 26.8.1 fitness; Linux arm64 official build (else that platform ships without LibreOffice, "not installed"); Alpine package fitness (T-33; else glibc base); musl renderer build. Licenses: LibreOffice (MPL-2.0), pdfium (BSD-3-Clause/Apache-2.0) and its bundled libraries.

## 11. Reachability (Definition of Done)

| Check | Pass |
|---|---|
| Registration | `grep -rl '"preview_document"' pkg/coreagent/ pkg/config/ pkg/tools/` ≥ 3 files; in `GET /api/v1/tools` and each permitted runtime registry |
| Policy | real dispatch per role; Omnipus Deny; one-agent Deny |
| Model | outbound bytes show the §4.2 form for all three entry points (T-44, U-M5b) |
| Person | groups live, after reload, unavailable card + Retry |
| Install | clean install / registration per §10 → real preview |
| Hands-on | §16 rows on the exact candidate |

## 12. BDD scenarios (Given / When / Then)

| ID | Scenario | Cat. | Traces |
|---|---|---|---|
| B-01 | Image-only model, FX-PPTX4 / previewed / 4 images, "pages 1–4 of 4" | Happy | US-1.1 |
| B-02 | Image-only model, FX-DOCX6, FX-XLSX3 / previewed / 6 and 3 images | Happy | US-1.1 |
| B-03 | Outline pages "3", "2-5", "11-20", "25-40" on 30 pages / previewed / those pages; last clamps 25–30 | Happy/Edge | US-1.3 |
| B-04 | Deck previewed, edited / previewed again / new version, changed slide differs | Alt | US-1.4 |
| B-05 | Workbook with stale cache (=1+1 cached 5) / previewed / shows 2 | Edge | US-1.1 |
| B-06 | Native model, docx / previewed / converted PDF in outbound request, no images to model; thumbnails to person | Happy | US-1.2, US-3.1 |
| B-07 | Outline §4.1 rows × {preview, read_file, upload} / invoked / that form | Happy | US-2.1 |
| B-08 | Native + image model, 30 MiB PDF / previewed / images in batches, size note | Alt | US-2.2 |
| B-09 | Catalog-PDF modern model missing from the old list / upload / native | Happy | US-2.1 |
| B-10 | Provider rejects native PDF with a classified error / send / narrow downgrade, turn completes | Error | US-2.1 |
| B-11 | Web preview / succeeds / group in page order with captions | Happy | US-3.1 |
| B-12 | Reload / chat reopens / same groups, no renderer start | Happy | US-3.2 |
| B-13 | Chat deleted / thumbnail URL / not found, files gone | Error | US-3.3 |
| B-14 | Keyboard only / open, Tab around, Right ×3, Escape / focus inside, contained, announced, returned | Happy | US-3.1 |
| B-15 | Outline Dataset B refusals / previewed / exact message, nothing started where knowable | Error | US-4 |
| B-16 | LibreOffice absent / docx / "not installed"; PDF works | Error | US-4, US-7 |
| B-17 | Executable replaced, registry unchanged / previewed / damaged, zero launches | Error | US-5.4 |
| B-18 | Component root inside the caller's write mount / previewed / unsafe location | Error | US-5.4 |
| B-19 | Macro fixture (positive control fires) / production preview / no marker, conversion succeeded | Error | US-5.1 |
| B-20 | Link fixture (positive control hits) / production preview / no hit, conversion succeeded | Error | US-5.1 |
| B-21 | Hanging converter / deadline / timed out, group gone, scratch gone | Error | US-5.2 |
| B-22 | Stop during conversion, rendering and text extraction / pressed / stopped, nothing left | Alt | US-5.2 |
| B-23 | Gateway killed during conversion and during rendering / restart / owned processes killed after identity match, folders removed after confirmation | Edge | US-5.2 |
| B-24 | Compressed-resource PDF allocating before any PNG / previewed / stopped by limit (Linux/Windows) or deadline/watchdog (macOS), gateway responsive | Error | US-5.3 |
| B-25 | Concurrent uploads + previews / turn runs / admission and request budgets hold, notes for omitted batches | Edge | US-5.3 |
| B-26 | Same upload in two requests / turn / renderer started once | Edge | US-5.3 |
| B-27 | Linux sandbox unavailable / docx / converts, discloses unconfined | Alt | US-5 |
| B-28 | Fake `soffice` in workspace and PATH / previewed / never executed | Error | US-5.4 |
| B-29 | Role defaults / each agent calls / Allow/Deny per AD-13 | Happy/Error | US-5.4 |
| B-30 | `elicify-office` loaded / read / table first, references exist, no banned commands | Happy | US-6 |
| B-31 | Each §10 route with/without components / docx / works or not installed | Happy/Error | US-7 |
| B-32 | Filled-form PDF / previewed / value visible | Edge | US-2.1 |
| B-33 | Windows registered / docx then Stop / works; whole tree ends | Happy/Alt | US-7, US-5.2 |
| B-34 | Renderer absent / PDF by native then image-only model / native; then text + unavailable; card without Retry | Alt | US-7, US-3.4 |
| B-35 | Renderer replaced / PDF previewed / damaged, nothing launched; native model still gets PDF | Error | US-5.4 |
| B-36 | Office skill per format / create → edit → preview → inspect / each step's oracle holds | Happy | US-6 |
| B-37 | Malformed program output (each MV-7 case) / read / protocol error, killed, no allocation beyond limits | Error | US-5.3 |
| B-38 | Gateway dies before go-byte / restart / target never started, folder removed | Edge | US-5.2 |
| B-39 | Reused PID with same executable / recovery / not killed, folder kept, warning | Edge | US-5.2 |
| B-40 | Two identical previews + interleaved screenshot / reload / two separate groups, screenshot inline, navigation stays in group | Edge | US-3.2 |
| B-41 | Thumbnail ok, enlarged fetch fails / viewer / error panel with Retry; next page works | Error | US-3.1 |
| B-42 | Rendering fails for native model / preview / model gets PDF; card with Retry; Retry succeeds when unchanged, 409 when changed | Alt | US-3.4 |
| B-43 | Source access revoked after tool completed / next provider send / neither PDF nor images sent | Error | US-2.1 |
| B-44 | Each adapter × entry point / request built / outbound bytes contain the PDF in the §4.2 form | Happy | US-2.1 |
| B-45 | Many distinct batches in one turn / turn runs / live store ≤ 64 MiB, oldest bytes freed, markers updated | Edge | US-5.3 |
| B-46 | Scanned PDF, text-only model / previewed / "no extractable text" | Edge | US-2.1 |
| B-47 | Channel (non-workspace) upload of 30 pages to image-only model / ask about page 25 / agent previews the materialized path, answers | Happy | US-2.1 |
| B-48 | Another agent holds a write mount over the shared component / any agent previews / disabled for all with doctor line | Error | US-5.4 |
| B-49 | Windows clean machine / `omnipus office register` then preview; tamper; unregister / works; damaged; not installed | Happy/Error | US-7 |
| B-50 | Windows CI with one new test family excluded / job runs / fails | Error | US-7 |
| B-51 | Document runtime without `soffice` / probe and bash run / ready; no converter demanded | Happy | US-6 |
| B-52 | Inspector mutation applied / its suite runs / at least one case fails | Error | US-6 |
| B-53 | Linux, renderer absent / PDF preview for a text-only model / `extract-text` starts under its 512 MiB data limit and returns text; an allocation beyond the budget in it is stopped (§3.1 D1) | Happy/Error | US-5.3 |
| B-54 | Fresh per-call profile, `soffice.bin` exits 81 on first start / Office preview / one second start in the same call converts; a second 81 or any other failure is a visible failure (§3.1 D3) | Alt/Error | US-5 |
| B-55 | Linux host whose data limit cannot govern `mmap` (kernel < 4.7 or `ignore_rlimit_data=Y`) / preview / runs, result, audit and `doctor` say `memory limit not enforced: <reason>` (§3.1 D1.4) | Edge | US-5.3 |
| B-56…B-58 | Retired 2026-11-05 (founder S2 = A, §3.2): the live-writer gate they described is not built; replaced by B-59…B-62 | — | — |
| B-59 | A shell command needs write access to a program folder, a file inside it, a folder containing it, an empty packaged slot, or one of these through a link / pre-flight / refused outright with the §3.2 text; no approval card; no grant recorded — neither "Allow" nor "Allow once" is possible (§3.2) | Error | US-5.4 |
| B-60 | Mount whose target is, lies inside or contains a program folder (incl. a containing folder such as `/`, and through a link) via REST, `request_mount` and the picker / create / REST 403 with the §3.2 reason, tool result with the same sentence, picker greys it; no link and no record written (§3.2) | Error | US-5.4 |
| B-61 | Configuration save or reload that adds or changes a `tools.allow_write_paths` / `sandbox.allowed_paths` entry overlapping a program folder, or one Omnipus cannot reduce / publish / save refused with the §3.2 text and nothing written; reload rejected, previous configuration in force; an unchanged entry already in force at start is not refused and keeps the component "unsafe location" with its `doctor` line (§3.2) | Error | US-5.4 |
| B-62 | Preview running (before its go-byte, and while its program runs, incl. the exit-81 second start) / each B-59…B-61 publish is attempted / every publish refused; the preview finishes normally (§3.2) | Edge | US-5.1 |
| B-63 | First LibreOffice start replaces `profile`, `out`, the settings file or `document.pdf` with a link leading outside scratch, then exits 81 / exit-81 handling and end-of-call cleanup / nothing outside scratch is read, removed, created or written; visible failure; no second start (§3.1 D3.7) | Error | US-5.1 |
| B-64 | A record, process or socket-shaped folder planted in scratch during a call, then the gateway dies / next start / recovery reads only the gateway's own record: it stops only the recorded launcher and that launcher's group, removes only the recorded socket folder, and the planted items are ignored (§3.4 D3, D4) | Error | US-5.2 |
| B-65 | A confined converter replaces its scratch folder with a link leading outside / later record updates and end-of-call cleanup / nothing outside is read, removed, created or written; the real record is updated (§3.4 D5) | Error | US-5.1 |
| B-66 | First LibreOffice start replaces its scratch folder, then exits 81 / exit-81 handling / `conversion failed: scratch unavailable`, no second start (§3.4 D6) | Error | US-5.1 |
| B-67 | Home where `tmp` exists, agent turn with its real policy / the agent previews an Office file twice / both previews produce pages; the converter's only writable folder in the data folder is `<id>/work` (§3.4 D8, D8c) | Edge | US-1.4 |
| B-68 | `config.json` on disk holds an overlapping entry the published configuration lacks / a person saves an unrelated setting through a §3.4 D9 or D9b route, or signs in, signs out or changes a password / 400 with the exact §3.2 text and the file unchanged; channel configure with a credential already stored keeps its coded 500 carrying the same sentence; the D9b own-rule routes answer as D9b states | Error | US-5.4 |
| B-69 | macOS or Linux, either file model; a home where `tmp` exists, another preview call folder created after this call started, this call's owner record, a file in the agent's workspace and, on macOS, an operator-registered LibreOffice whose filtered registry lies under `<home>/system` / the agent previews an Office file, including an exit-81 second start / pages are produced; the converter reads and writes its own `<id>/work`, on macOS reads the registry folder but cannot change it, and cannot read the other call folder, the owner record, the workspace file or any part of `<home>/system` outside the registry folder (§3.4 D8c) | Edge | US-5.1 |

## 13. TDD plan

RED: one qa-lead instance owns **every** row below; CHECK: a different instance mutates guards, limits, cache, routing, protocol reader, recovery and serializers and requires the named tests to fail. New fixtures (digests recorded at RED): FX-PPTX4, FX-DOCX6, FX-XLSX3, FX-PDF30, FX-PDF-FORM, FX-MACRO, FX-LINK, FX-BOMB (compressed resources allocating before rendering), FX-SCAN, FX-PDF-30MiB, plus the inspector fixtures generated by the pinned libraries (skills-review §4).

| # | Test | Level | BDD |
|---|---|---|---|
| T-01 | Input/page syntax and clamp table | Unit | B-03, B-15 |
| T-02 | Renderer: pages, sizes, PNG decode, pixel compare vs reference with a different-page control | Integration | B-01, B-02 |
| T-03 | Renderer/extract in sandbox: file/URL-action PDF causes no open/connect; positive control instrumented | Integration | MV-6 |
| T-04 | Resolver candidates + registered manifest; PATH and links ignored | Unit | B-28 |
| T-05 | LibreOffice manifest: replaced executable → damaged, zero launches; size/mtime triggers re-hash | Unit | B-17 |
| T-06 | Converter argv/env golden | Unit | MV-2 |
| T-07 | Fake converter outputs (junk + markers, exit codes) | Integration | B-15 |
| T-08 | Deadline kills converter and grandchild | Integration | B-21 |
| T-09 | Stop during conversion | Integration | B-22 |
| T-10 | Real LibreOffice: stale cache shows 2 | Integration (Mac, Linux) | B-05 |
| T-11 | Real Seatbelt child; profile diff against the §3.4 D8c read list + the 3 socket rules (+ network deny), taken from the profile the real start used, never one built by the test; no rule from the turn's policy appears; Mach = 2 | Integration (Mac) | MV-3, B-69 |
| T-12 | Office serialization; queue past deadline | Integration | B-25 |
| T-13 | 50 runs incl. 10 timeouts, 10 Stops → 0 folders, 0 processes | Integration | B-21, B-22 |
| T-14 | Recovery: dead owner preparing/running, live owner, foreign, unreadable record, removal failure | Integration | B-23 |
| T-15 | Read authorization + audit reuse | Integration | B-15 |
| T-16 | Model forms live-turn only; transcript markers | Integration | B-06, B-07 |
| T-17 | Macro positive control + production | Integration (Mac) | B-19 |
| T-18 | Link positive control + production | Integration (Mac) | B-20 |
| T-19 | Bash golden except AD-3 | Unit | MV-4 |
| T-20 | Policy entries (ceiling, inventory, custom) | Unit | B-29 |
| T-21 | Real dispatch per role, Omnipus Deny | Integration | B-29 |
| T-22 | One-agent operator Deny | Integration | B-29 |
| T-23 | Reconcile adds the key | Unit | B-29 |
| T-24 | Upload cache: renderer-start counter; changed bytes re-render | Integration | B-26 |
| T-25 | Upload regression for native and text-only (catalog-only) | Integration | B-07 |
| T-26 | Upload render failure/timeout → text + note | Integration | B-07 |
| T-27 | Skill package structure, old ids gone | Unit | B-30 |
| T-28 | Clean install per route → resolver → real preview; omit/remove | Integration (packaging) | B-31 |
| T-29 | Fake soffice never executed | Integration | B-28 |
| T-30 | macOS network deny keeps conversion working | Integration (Mac) | B-20 |
| T-31 | Gateway peak memory over worst paths (FX-BOMB, 20-page batches ×N, 3 uploads, fallback text), instrument proven by a deliberate allocation; must stay within AD-6 sums; typical 20-page preview reported | Integration | B-24, B-45 |
| T-32 | Real LibreOffice FX counts 4/6/3 | Integration (Mac, Linux) | B-01, B-02 |
| T-33 | Docker image converts FX set | Integration (Docker) | B-31 |
| T-34 | Freshness: new digest, changed image | Integration | B-04 |
| T-35 | FX-BOMB per OS: Linux/Windows killed by memory limit; macOS stopped by watchdog or deadline; gateway health answers | Integration (per OS) | B-24 |
| T-36 | Allocation bomb via `run-limited`: Linux/Windows killed; macOS outcome recorded, no hard-cap claim | Integration (per OS) | B-24 |
| T-37 | Output caps | Integration | B-24 |
| T-38 | Stop during rendering releases admission | Integration | B-22 |
| T-39 | Admission before copy | Unit | B-25 |
| T-40 | Request image and byte caps, omission notes | Unit | B-25 |
| T-41 | Windows native job runs the named inventory (§13 note), each non-skipped PASS | CI | B-33 |
| T-42 | `documentEnvTurn` characterization then registry-free readiness | Unit | MV-4 |
| T-43 | Catalog-only decision; Haiku-via-Bedrock entry without PDF | Unit | B-09 |
| T-44 | Outbound bytes per adapter × entry point; fails on omission | Integration | B-44, B-06 |
| T-45 | Thumbnail storage scope, pinning, chat delete | Integration | B-11, B-13 |
| T-46 | Contract tests on generated types: new fields, empty parts only when unavailable | Integration | B-11, B-12 |
| T-47 | Non-web channel gets no thumbnails | Integration | B-11 |
| T-48 | Frontend strip and §5.3 states (component + Playwright) | Component/E2E | B-11, B-41 |
| T-49 | Filled-form value; negative control differs | Integration | B-32 |
| T-50 | Unconfined disclosure: text, audit, doctor | Integration | B-27 |
| T-51 | Narrow downgrade on classified rejection only | Integration | B-10 |
| T-52 | Renderer absent: §4.3 rows, card without Retry | Integration | B-34 |
| T-53 | Renderer replaced → damaged, re-hash every launch; native PDF still delivered | Unit | B-35 |
| T-54 | SKILL.md table first; rows name existing files | Unit | B-30 |
| T-55 | Create/edit recipes per format end to end in the real runtime (Mac, Linux): script → preview → inspector, with recipe-derived oracles | Integration (runtime) | B-36 |
| T-56 | Protocol reader vs fake peer: every MV-7 malformed case rejected before allocation, killed, reaped | Unit/Integration | B-37 |
| T-57 | Launcher per platform: limits in force before input; Linux parent-death; Windows job tree; macOS EOF | Integration (per OS) | B-38, B-33 |
| T-58 | Recovery identity: crash before go-byte, native renderer mid-render, PID reuse, two gateways | Integration | B-38, B-39, B-23 |
| T-59 | Live-turn budget and eviction: bytes freed (store accounting), markers, per-gateway cap | Unit/Integration | B-45 |
| T-60 | Stop during `extract-text` kills it | Integration | B-22 |
| T-61 | Send-time re-authorization for native PDF and images | Integration | B-43 |
| T-62 | Group projection identical across live, replay, REST history; repeated previews; interleaved screenshot; reconnect dedup | Integration | B-40, B-12 |
| T-63 | Display-only: archive, window and next request contain no thumbnail bytes or refs | Integration | B-06 |
| T-64 | Viewer focus: initial, Tab containment, 3 page changes without remount, first/last, return fallback; existing image/diagram regression | Component/E2E | B-14 |
| T-65 | Viewer failure, zero stored, one group announcement, no per-image toast for thumbnails only | Component/E2E | B-41 |
| T-66 | Retry route: unchanged Library file → new frame same group; changed → 409; source outside the Library (agent home, operator allowance, protected file) → 403 `preview_source_not_in_library`; no session access → 404; limits and the AD-4 per-call writer check apply; the rendered bytes are the ones the version was computed on; after a successful retry a reload shows the new group (no unavailable card, no renderer start) and the model context is byte-identical; after a failed retry a reload shows the original card | Integration | B-42 |
| T-67 | Windows guard proven: exclude one family → job fails while read-boundary tests still pass | CI | B-50 |
| T-68 | Windows clean machine registration, tamper, unregister | Integration (Windows) | B-49 |
| T-69 | Writer exclusion for access already in force (2026-11-05, §3.2 point 4): a mount record or remembered grant that predates the component (written directly to its store) and a configuration entry present at gateway start, each covering the root, disable the component for all with the `doctor` line. The R5 proof `pkg/preview/exclusion_recheck_office_darwin_test.go::TestWriteExclusionRejectsUnknownAuthorityWithLiveWriter` keeps its static fault cases (`malformed_record`, `record_is_directory`, `mount_list_error`, `identity_error`: the record is still listed and unreadable, so the check fails closed) and **drops `dropped_record`** (a genuinely removed record with a surviving writer is §3.2 boundary item 1) | Integration | B-48, B-18 |
| T-70 | Outcome table rows incl. scanned PDF, text > 4 MiB, size boundaries 20/25/50/200 MiB ± 1 | Integration | B-46, B-08 |
| T-71 | Channel upload materialized; page 25 via preview | Integration | B-47 |
| T-72 | Four inspectors through the managed interpreter on Mac, Linux, Windows: every case of their `inspector-contract.md` and skills-review §4 (counts, malformed, password handling, edges −1/0/+1, missing cache vs cached zero, errors, dimensions, byte non-mutation) | Integration (runtime) | B-36 |
| T-73 | Document runtime ready without `soffice` | Unit | B-51 |
| T-74 | Package inventory adaptation + integrity test updated deliberately | Unit | B-30 |
| T-75 | Inspector mutations (review §4 lists) each turn a T-72 case red | Mutation | B-52 |
| T-76 | Linux limit kind (§3.1 D1): for renderer, `extract-text` and LibreOffice starts, `/proc/<target>/limits` shows data = budget (soft = hard), stack ≤ 8 MiB and ≤ the inherited stack, address-space limit not set by the launcher (equal to the gateway's own); stack limit never raised (inherited 4 MiB stays 4 MiB); the `--mem-kind` flag is rejected as unknown. C malloc bomb, C mmap bomb and a Go allocation loop under the launcher each fail at the data budget (not killed by the wall deadline); deep-recursion program dies on the stack limit. Mutation: switch the kind back to address space → the `extract-text` start case fails | Integration (Linux) | B-53, B-24 |
| T-77 | Launcher stability under the data limit (§3.1 D2): ≥ 1,000 renderer launches and ≥ 300 `extract-text` launches, 2 concurrent, zero launcher exits with a Go runtime `fatal error`; the crash classifier is first proven on a known runtime fatal (the binary started directly under `prlimit --as=536870912`, O3 receipt) so a zero count is meaningful; order unchanged: a launcher whose stdin closes before the go-byte starts nothing, and `/proc/<launcher>/limits` already shows the limits while it waits | Integration (Linux) | B-38 |
| T-78 | LibreOffice exit-81 retry (§3.1 D3), with a fake `soffice.bin` that exits 81 N times then converts: N = 1 → converts, exactly 2 launches, two distinct identity records each written before its go-byte; N = 2 → visible failure, exactly 2 launches; exit 79 and exit 1 → no retry; 81 with a readable PDF present → no retry; Stop and the 120 s deadline during the second start end it; the admission slot is held across both starts (a parallel conversion waits). With the real LibreOffice on Linux, fresh profile: FX-MACRO converts through the retry path and its macro does not run; the profile settings of AD-7 are in force on the second start | Integration (fake: all OS; real: Linux) | B-54, B-19 |
| T-79 | Windows Job Object (§3.1 D1.5): `extract-text` starts and returns text inside a 512 MiB job; an allocation bomb as target ends the whole tree with the result "memory limit exceeded", not a launcher crash or "protocol" | CI (Windows) | B-24, B-33 |
| T-80 | Data-limit enforcement check (§3.1 D1.4): injected kernel release 4.6 and injected `ignore_rlimit_data` = `Y` each produce `memory limit not enforced: <reason>` in the result text, audit field and `doctor`; 4.7 and `N` produce none; unreadable parameter file → enforcing plus one log line | Unit | B-55 |
| T-81…T-84 | Retired 2026-11-05 (founder S2 = A, §3.2): the live set, lease and kill they tested are not built; replaced by T-85…T-89. No test code existed for them at `a0ad39455` | — | — |
| T-85 | Shell pre-flight refusal (§3.2): for a program folder, a file inside it, a folder containing it, an empty packaged slot, and a link to the folder, with the approver scripted to answer "Allow" and, separately, "Allow once": oracle — approval requests 0 (counter), result text exactly the §3.2 text, grant store unchanged, command not run (marker absent). Controls: a write to an unrelated folder produces exactly 1 approval request; a read of the program folder is not refused. Mutation: remove the check → an approval request appears → red | Integration | B-59 |
| T-86 | Mount refusal (§3.2): target = program folder, inside it, containing it (a test tree whose root contains the slot of a test executable path), and a link to it; via REST (403, exact fixed reason, no record, no link in the work folder), `request_mount` (exact result sentence), picker (greyed with the exact reason). Controls: an unrelated folder mounts; the existing data-directory and operating-system refusals keep their own texts. Mutation: drop the new `mountRefusalText` branch → the reason reads "Omnipus data directory" → red | Integration | B-60 |
| T-87 | Configuration refusal (§3.2): through every gateway config writer backend-lead lists, and through file-watcher and manual reload: (a) new overlapping `tools.allow_write_paths` entry, (b) new overlapping `sandbox.allowed_paths` entry, (c) a new pattern Omnipus cannot reduce, (d) a changed entry that now overlaps — each refused with the exact text, `config.json` bytes unchanged after a refused save, published configuration unchanged after a rejected reload; (e) an overlapping entry present at gateway start and left unchanged by a later unrelated save or reload → accepted, component "unsafe location", `doctor` `WARN-PREVIEW-002` naming it. Control: an unrelated new entry is accepted | Integration | B-61 |
| T-88 | Publishes during a running preview (§3.2, the R4 closure): deterministic barriers (a) after the component check, before the go-byte, (b) while the target runs, (c) before the exit-81 second start; at each, attempt a shell approval, a mount and a configuration save overlapping the component → each refused; the preview result is complete, the target ran (marker), no kill. Mutation: remove the configuration check → the save succeeds at a barrier → red | Integration | B-62 |
| T-89 | Protected-folder list (§3.2 points 1–2): every packaged candidate of both kinds, present and absent; the registered root; a registration made by a second process between two checks is protected at the second check without restart; an unreadable registered manifest names no folder and the component is "damaged"; overlap through case/Unicode spellings (macOS), links, and an undecidable comparison → refused | Unit/Integration | B-59, B-60 |
| T-90 | Anchored scratch operations (§3.1 D3.7, SEC-R1-01). A fake `soffice.bin` (as T-78) makes one replacement on its first start, then exits 81 with no PDF unless noted: (a) `profile` → link to outside folder E1 holding `user/registrymodifications.xcu` (sentinel S1); (b) `profile/user/registrymodifications.xcu` → link to an outside file (sentinel S2); (c) `out` → link to outside folder E2 holding a non-PDF `document.pdf` (sentinel S3); (d) `out/document.pdf` → link to an outside non-PDF file (sentinel S4); (e) as (c) but E2's `document.pdf` is a valid PDF and the exit is 0; (f) a link inside scratch to an outside folder holding sentinel S5, met by the end-of-call removal. Sentinels are test-created temporary files outside scratch, never real user files. Oracle (from D3.7, not the code): afterwards every sentinel exists with byte-identical content and each outside folder's listing is unchanged; (a)–(d) the result is the exact D3.7 failure text and the start counter is 1; (e) the call reports no readable PDF and none of E2's PDF bytes reach model media or thumbnails; (f) scratch is gone, S5 intact. Controls: the same replacements pointing **inside** scratch behave as without links (retry and conversion per D3). Instrument: (a) and (c) are first run on `a0ad39455`, where S1 is rewritten and S3 deleted (red) | Integration (Linux, macOS) | B-63, B-54 |
| T-91 | Linux limits measurement (founder O8 = A): a manual GitHub Actions workflow (`workflow_dispatch`, input = the full candidate SHA, runner `ubuntu-latest`), added by backend-lead under `.github/workflows/` after the manual pattern of `.github/workflows/landlock-probe.yml`. Steps: check out exactly the SHA and fail unless `git rev-parse HEAD` equals it; Go from `go.mod` (`go-version-file`); SPA stub (root `CLAUDE.md`); record `uname -r`, `/sys/module/kernel/parameters/ignore_rlimit_data`, `go version`, `cc --version`, `prlimit --version`; run `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -v -run '^(TestRunLimitedLinuxDataStackAndAddressSpace\|TestRunLimitedLinuxAllocationAndStackBombs\|TestRunLimitedRejectsMemoryKind)$' ./pkg/preview/launcher/` (T-76) and `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -v -run '^TestPreviewMemoryEnforcementDisclosure$' ./pkg/preview/` (T-80), each to its own log with the exit code captured directly; upload both logs. **PASS** = HEAD equals the input SHA; both runs exit 0; each of the four named tests has a `--- PASS` line; zero `--- FAIL`, `--- SKIP` and `BLOCKED` lines; and the host enforces the data limit (kernel 4.7 or later and `ignore_rlimit_data` not `Y`) — otherwise the run proves only the disclosure path and is reported "not a measurement", never PASS. Run once, by team-lead, on the exact candidate commit; a red or "not a measurement" result goes to the founder | CI (manual, Linux) | B-53, B-55, B-24 |
| T-92 | **Record out of the converter's reach** (§3.4 D1, FR-051). Through the real launcher and real confinement hooks, a fake converter tries to change, remove, replace and link the record and to create a file beside it. The record stays byte-identical. Not a pass if: confinement is off (`hooks.unconfined` set fails the test, it never skips); the fake ended before trying (require a marker per attempt and a control write inside scratch that succeeded). macOS: local receipt on the exact commit (macOS CI is off) | Integration (Linux, macOS) | B-64 |
| T-93 | **A forged record in scratch is ignored** (§3.4 D3, Q18). During a real call, scratch gets a forged `owner.json` naming a separate harmless process, its group and a socket-shaped folder. After a simulated crash the harmless process and the folder survive, and the real launcher is stopped: `Killed == 1`. Not a pass if: the kill path was never reached (assert the real launcher died in the same run); the harmless process shares the test's group; RED on `1c2ca4870` does not show the forged file being read | Integration | B-64 |
| T-94 | **The group stopped is the verified process's group, and no other** (§3.4 D4). A live launcher-like process with a grandchild in its group, plus an unrelated harmless process in another group; a legacy `pgid` key in the record names the unrelated group. After recovery the launcher and grandchild are gone and the unrelated process lives. The test must fail if `killGroup` is removed, so the grandchild is required. Replaces the identity assertion in `pkg/preview/retry_office_test.go`. Unix only; Windows is T-99 | Integration (Linux, macOS) | B-64 |
| T-95 | **Crash points, by really killing a helper gateway** (SIGKILL): after launcher start; after `running`, before the go-byte; during the target; between the two exit-81 starts. In the first two the target's marker never appears. In all cases the folder is removed or kept as §3.4 D3 and AD-8 say. Not a pass if: JSON fixtures or a fake start time stand in for a crash, because stdin never closes and the launcher-exit claim stays untested; Linux alone, because the parent-death signal hides what macOS relies on. macOS: local receipt on the exact commit | Integration (Linux, macOS) | B-23, B-38 |
| T-96 | **Older runs.** (a) A live process with a matching executable but a different start time is not stopped; folder kept; warning. (b) A folder in the old `tmp/preview` layout whose record names a live harmless process: nothing stopped, nothing deleted. Not a pass if: (a) uses a dead PID, so there was never anything to stop; (b) passes because `Recover` returned early, so assert a sibling valid call was still processed in the same run | Integration | B-39, B-64 |
| T-97 | **Socket folder** (§3.4 D2, D4). (a) A genuine call's folder is removed, including after a crash before `running`. (b) A folder with a valid name that the record does not name survives. (c) A recorded link: the link is gone, its target and contents remain. Fixes `scratch_recovery_socketdir_office_test.go::TestPreviewRecovery_TamperedSocketDirCannotEscapeItsPrefix`: it uses `randomID(8)`, which the pattern refuses before any removal; use `randomID(4)`, assert with `Lstat` that the link is gone, and prove the removal line was reached. macOS only (no other platform has a socket folder): local receipt on the exact commit | Integration (Mac) | B-64 |
| T-98 | **A replaced scratch folder never redirects a gateway write** (§3.4 D5, FR-050). At a barrier the test replaces scratch with a link to folder E holding a sentinel `owner.json`; then `idle` and `finish` run. E is byte-identical with no `.tmp-*` file, and the real record is updated. Plus a macOS variant where a real confined child does the replacing (local receipt on the exact commit). Not a pass if: the replacement is a plain folder (harmless on old code too); the test asserts a returned error (`finish` discards it); only the sentinel is checked, not a listing of E | Integration (per OS) | B-65 |
| T-99 | **Windows recovery** (§3.4 D7). Recovery stops a verified launcher and its job's target; folders removed; `killGroup` not relied on. Added to the Windows inventory (T-41), run in Windows CI. A Unix-tagged test does not count | Integration (Windows) | B-64 |
| T-100 | **Second start refused on a replaced scratch folder** (§3.4 D6, Q23). The first start replaces the folder, then exits 81. Result `conversion failed: scratch unavailable` and **exactly one launch**. Not a pass if only the outside folder's survival is asserted: a cached sandbox profile keeps it safe with no guard at all, so count launches | Integration | B-66 |
| T-101 | **A normal call leaves nothing** (§3.4 D2). After success, failure and Stop, `tmp/preview-calls` holds no `<id>`. If `work` cannot be removed, the record survives as `done` and the next start retries. Not a pass if only `work` is checked: a leaked record or `<id>` goes unnoticed | Integration | B-21, B-22 |
| T-102 | **Repeat preview, own read list** (§3.4 D8, D8c; FR-054, FR-055; revised again 2026-11-08 for D8c). Two real Office previews in a row through `Service.Preview` with exactly the arguments the tools pass (the turn's policy is no longer an input: `Request.ReadModel` is removed, which the compiler enforces for all three callers), on a home where `tmp` already exists, with operator-registered real LibreOffice and the real renderer; **each admission is released before the next** (`maxOfficeRuns = 1`). Both previews produce pages, confined. On the policy each real start used (a test seam capturing the converter's `SandboxPolicy` per start, never a policy built by hand): the file rules are exactly the §3.4 D8c list for the platform; write rules are exactly {that call's `<id>/work`, its socket folder} on macOS and {`<id>/work`} on Linux; `ReadsOpen` and `ExecOpen` are false; no rule covers any part of the data folder other than that `work` and, on macOS, the registry folder; `URE_MORE_SERVICES` names `Component.Registry`. Probes: a harmless probe program started through the same confinement hooks with the captured policy, at a barrier inside the call (not a timer), tries to read a file in another call folder created **after** this call started, this call's `owner.json`, a file in the agent's workspace and a file in `<home>/system` outside the registry, and every read fails; it writes in `work`, which succeeds; it reads the registry, which succeeds, and writes there, which fails. Not a pass if: the policy is built by hand; the "later" folder is created before the start; an unconfined run is counted as a pass; a missing component skips instead of failing; an admission is never released, so the second preview never runs; the component is registered so its registry lies inside its root, which never touches `<home>/system`; a probe expected to fail fails for a reason other than the sandbox (the target must exist and be readable outside the sandbox). macOS: local receipt on the exact commit. Linux: same test where Landlock enforces; Unknown until run. Hands-on row U-M1 stays the separate check on the candidate | Integration (Mac, Linux) | B-67, B-69 |
| T-103 | **Refusal text on the §3.4 D9 routes** (FR-053). One table test with one row per route and an assertion that it has 20 rows. Each row needs a valid target, or the route's 404 answers before the save. Each row writes an overlapping `tools.allow_write_paths` or `sandbox.allowed_paths` entry **directly into `config.json`**, so the published configuration lacks it, then calls the route. Expected: 400, `error` byte-equal to the §3.2 text, `config.json` byte-identical. A second case per site: a write failure that is not a refusal keeps today's 500. Route 7 runs both D9 sub-cases. Not a pass if: the entry is added through a handler, so `updateConfig`'s own mapping answers; only the status is checked | Integration | B-68 |
| T-104 | **Refusal text on the §3.4 D9b writers** (FR-053, founder Q26). Same set-up as T-103: an overlapping entry written directly into `config.json`, one row per D9b route, and an assertion that there are 14 rows. Helper rows: 400, `error` byte-equal to the §3.2 text, `config.json` byte-identical. Auth rows: login with the right password gets 400 with the text, no session token stored, no session cookie set; login with a wrong password still gets 401; change-password with a wrong current password still gets 401. Own-rule rows: 31, provider delete without a new default gets 400 and the provider is still there; 32, nothing lowered gets 400; agents lowered and all restored gets 400 with every agent back at its old value; 33, sign-in poll gets 400 with the stated prefix and text; 34, outbound gets 400 whose body passes the generated `ChannelOutboundRejection` schema, with code `program_access_refused` and the fixed text byte-equal; 30, the workspace still exists. Cascade row: the log line for `removeMailboxesForWorkspace` holds the refusal text. Not a pass if: the login row uses a wrong password, so it answers on the 401 path; the outbound row checks the status alone; a row has no valid target, so a 404 answers first | Integration | B-68 |
| T-105 | **Registry read in place, read-only** (§3.4 D8c, R1; FR-055; replaces the D8b.1 copy test, 2026-11-08). macOS; a fake `soffice.bin` (as T-78), started through the real launcher and confinement hooks, records the folder `URE_MORE_SERVICES` names and the hash of every file in it, then tries to create, change and remove a file in that folder. (a) The folder named is exactly `Component.Registry` as `pkg/preview/component/component.go::load` resolved it, for an operator-registered component (registry under `<home>/system/components/office/services`) and for a packaged one (registry inside the component root). (b) The recorded hashes equal the manifest's registry. (c) Every write attempt fails, and the folder is byte-identical afterwards (same file list, same hashes). (d) The same holds on an exit-81 second start. (e) The captured policy grants that folder read only, and no other part of `<home>/system`. Not a pass if: the fake never attempts the write or never reads the folder (require the recorded hashes and each attempt's error); the folder checked is not the one named in `URE_MORE_SERVICES`; only the packaged case runs, which never touches `<home>/system`; the run is unconfined. macOS: local receipt on the exact commit | Integration (Mac) | B-69 |

**Windows inventory (T-41):** `TestPreviewDocument_Windows*`, `TestRunLimited_WindowsJob*`, `TestPreviewRecovery_Windows*`, `TestOfficeRegister_Windows*`, `TestInspectors_Windows*` and the renderer's own smoke/limits/cancel tests built from the C sources; the receipt step lists every expected test name and fails on a missing, skipped or failed one.

**Datasets.** A (valid): A1 FX-PPTX4 → 4; A2 FX-DOCX6 → 6; A3 FX-XLSX3 → 3; A4 FX-PDF30 "11-20" → 10; A5 "3" → 1; A6 default → 10 of 30; A7 non-ASCII name; A8 50 MiB exactly; A9 "1-1"; A10 FX-PDF-FORM. B (refusals/security): R1 `.txt`; R2 PNG as `.docx`; R3 50 MiB + 1; R4 "0", "5-3", "1-21", "a", "10000"; R5 "40" of 30; R6 encrypted; R7 truncated; R8 `.docm` `.doc` `.odt`; R9 outside policy / symlink to a secret folder; S1 FX-MACRO; S2 FX-LINK; S3 hang + grandchild; S4 junk + `%%EOF`; S5 fake soffice; S6 replaced executable; S7 root in a writable mount (own and other agent); S8 FX-BOMB; S9 renderer replaced; S10 renderer absent; S11 malformed frames; S12 PID reuse. Sizes: 20 MiB ± 1 (native), 25 MiB ± 1 (existing extractor, existing paths), 50 MiB ± 1 (input), 200 MiB ± 1 (converted).
**Regression:** `read_file` text pagination and PNG/JPEG inspection; existing upload text path; bash composition except AD-3; Mach set; screenshot storage/replay (plus caption persistence); existing image/diagram viewer.

## 14. Requirements

| ID | Requirement |
|---|---|
| FR-001 | MUST provide `preview_document(file, pages?)` per AD-1/AD-5. |
| FR-002 | MUST accept only the four types, matching content, ≤ 50 MiB, unencrypted. |
| FR-003 | MUST read the source only via the caller's read authorization, audited like `read_file`; convert a scratch copy. |
| FR-004 | MUST apply AD-5 page rules and name the next batch. |
| FR-005 | MUST deliver to the model per §4.1, live-turn only. |
| FR-006 | MUST render only in `omnipus-pdfium`, sandboxed, no files or network. |
| FR-007 | MUST run LibreOffice only as a separate per-conversion child. |
| FR-008 | MUST locate and verify components per AD-4 without fallback. |
| FR-009 | MUST start the converter with AD-7 settings and exact environment. |
| FR-010 | MUST apply AD-7 sandboxing, else run unconfined with disclosure. |
| FR-011 | MUST report conversion validity, model delivery and thumbnails as separate outcomes per §4.3; ignore exit codes. |
| FR-012 | MUST enforce AD-6 limits, admission and Stop. |
| FR-013 | MUST clean scratch and recover per AD-8. |
| FR-014 | MUST return MV-1 messages. |
| FR-015 | MUST keep PDF preview without LibreOffice and degrade per AD-15 without the renderer. |
| FR-016 | MUST register the tool with AD-13 policies. |
| FR-017 | MUST apply §4.1 to uploads, incl. pages 1–10 + text + paging note, text fallback on failure. |
| FR-018 | MUST decide PDF capability from the catalog only; delete the list; Haiku exclusion in the catalog. |
| FR-019 | MUST apply §4.1 to `read_file` on a PDF without offset/length. |
| FR-020 | MUST store and deliver thumbnails per §5.1, web chat only. |
| FR-021 | MUST implement §5.3–5.5. |
| FR-022 | MUST cache rendered pages per AD-9. |
| FR-023 | MUST replace the four skills with `elicify-office`. |
| FR-024 | MUST ship components per §10; no runtime download. |
| FR-025 | MUST delete the registry store after replacing the readiness gate. |
| FR-026 | MUST include license notices. |
| FR-027 | MUST update §17 docs in the same change. |
| FR-028 | MUST open `SKILL.md` with the AD-14 table. |
| FR-029 | MUST run create/edit recipes end to end in the real runtime. |
| FR-030 | MUST re-hash the renderer before every launch. |
| FR-031 | MUST enforce AD-6 retained-byte budgets with byte eviction. |
| FR-032 | MUST run all PDF parsing this feature adds in a killable program under AD-6 limits. |
| FR-033 | MUST implement protocol v1 and validate before allocating. |
| FR-034 | MUST implement the per-platform launcher ownership of AD-2. |
| FR-035 | MUST implement the §5.2 contract across all producers. |
| FR-036 | MUST keep thumbnails out of model media. |
| FR-037 | MUST implement §5.3 states and §5.4 focus behaviour. |
| FR-038 | MUST provide the Retry route of §5.2. |
| FR-039 | MUST serialize native PDFs per §4.2 for every retained adapter and entry point; withhold only for documented upstream limits via the catalog. |
| FR-040 | MUST re-authorize sources before each provider send. |
| FR-041 | MUST implement every §4.3 row. |
| FR-042 | MUST provide `omnipus office register` / `pdf-renderer register` / `unregister`. |
| FR-043 | MUST disable a component whose root any agent can write. |
| FR-044 | MUST materialize image-delivered uploads into the workspace for pagination. |
| FR-045 | MUST enforce the named Windows test inventory. |
| FR-046 | MUST test the four inspectors against their contracts with mutations; apply §6 dispositions. |
| FR-047 | MUST remove the document runtime's `soffice` contract. |
| FR-048 | MUST state the macOS best-effort memory boundary in UI text and docs, with no hard-cap claim. |
| FR-049 | MUST refuse, where it is created, any new write access that overlaps a preview program folder — shell approvals, mounts and configuration saves/reloads — with the §3.2 texts (founder S2 = A). |
| FR-050 | MUST resolve every post-start gateway file operation in a call's scratch folder through a handle opened before the first start, never reaching outside it (§3.1 D3.7, founder SR1). |
| FR-051 | MUST keep the owner record outside the converter's write reach wherever the converter has a file boundary; recovery MUST act only on that record and MUST derive the process group from the verified launcher, never from a stored number (§3.4 D1–D4, founder Q18). Where no file boundary exists the limit in §3.4 D7 applies (founder Q22). |
| FR-052 | MUST NOT start the converter again when its scratch folder was replaced; the call ends with the existing `conversion failed: scratch unavailable` text (§3.4 D6, founder Q23). |
| FR-053 | MUST answer a configuration refusal on every route of §3.4 D9 and D9b with the exact §3.2 text, as a 400, except where D9 or D9b states the route's own rule; writers with no answer to a person MUST log the text (founder Q24, Q26). |
| FR-054 | MUST let a preview started from an agent turn use its own scratch folder on a home where `tmp` exists, every time; the fix MUST open only `<id>/work` (§3.4 D8). |
| FR-055 | MUST confine the converter's reads to the §3.4 D8c list on every start and in both file models, never the calling turn's read rights; on macOS the verified filtered registry folder is read in place, read-only; the converter MUST NOT be able to read another call's folder, the owner record, a workspace or agent file, or any other part of the data folder (§3.4 D8c, founder Q35, Q36). |

| ID | Success criterion |
|---|---|
| SC-001 | Dev Mac, sandboxed, FX-PPTX4/DOCX6/XLSX3: p95 ≤ 10 s over 5 runs. |
| SC-002 | Renderer start to first page p95 ≤ 500 ms; ≤ 100 ms median per further page (S-1). |
| SC-003 | Omnipus binary size unchanged in class; renderer ≤ 15 MB per platform (S-1). |
| SC-004 | T-31 worst-path gateway peak ≤ the AD-6 sum for that scenario; typical 20-page preview reported (target ≤ 100 MiB, not a claim until measured). |
| SC-005 | Every §11 row passes. |
| SC-006 | T-13 leaves 0 folders, 0 processes. |
| SC-007 | macOS profile diff = 3 rules (+ network); Mach = 2. |
| SC-008 | UAT: planted defect detected and fixed in docx, xlsx, pptx on macOS. |
| SC-009 | `openai/gpt-6-luna` receives native PDF via upload, `read_file` and preview (outbound evidence). |
| SC-010 | T-72 passes on Mac, Linux, Windows with every T-75 mutation caught. |
| SC-011 | T-91 reports PASS (not "not a measurement") on the exact candidate commit (founder O8 = A). |

**S-1** (backend-lead, before GREEN): pdfium source/version/checksum and license inventory; reproducible build inputs; build ID + protocol version; linkage and runtime dependencies; final installed hash after any signing; quarantined clean-install launch on Apple silicon (no quarantine removal, no Gatekeeper disabling); timings, size, memory behaviour per OS incl. the macOS watchdog feasibility; non-embedded-font and form PDFs; musl availability; LibreOffice address-space vs data limit on Linux. **S-2:** provider documentation for PDF in user content and tool results (§4.2). Missed budgets or unavailable targets → founder.

## 15. Traceability

| FR | US | BDD | Tests |
|---|---|---|---|
| FR-001 | US-1 | B-01, B-03 | T-01, T-16 |
| FR-002 | US-4 | B-15 | T-01 |
| FR-003 | US-5 | B-15 | T-15, T-39 |
| FR-004 | US-1 | B-03 | T-01 |
| FR-005 | US-1, US-2 | B-06, B-07, B-04 | T-16, T-34 |
| FR-006 | US-2, US-5 | B-01, B-02, B-32 | T-02, T-03, T-32, T-49 |
| FR-007 | US-5 | B-21, B-22, B-54 | T-06, T-08, T-09, T-78 |
| FR-008 | US-5 | B-17, B-18, B-28 | T-04, T-05, T-29 |
| FR-009 | US-1, US-5 | B-05, B-19, B-20 | T-06, T-10, T-17, T-18 |
| FR-010 | US-5 | B-20, B-27 | T-11, T-30, T-50 |
| FR-011 | US-3, US-5 | B-15, B-34, B-42 | T-07, T-52, T-70 |
| FR-012 | US-5 | B-21, B-22, B-24, B-25, B-53, B-55 | T-12, T-35, T-36, T-37, T-38, T-39, T-40, T-76, T-79, T-80, T-91 |
| FR-013 | US-5 | B-21, B-23, B-38, B-39 | T-13, T-14, T-58 |
| FR-014 | US-4 | B-15, B-16 | T-01, T-05 |
| FR-015 | US-4, US-7 | B-16, B-34, B-53 | T-28, T-52, T-76 |
| FR-016 | US-5 | B-29 | T-20, T-21, T-22, T-23 |
| FR-017 | US-2 | B-07, B-26 | T-24, T-25, T-26 |
| FR-018 | US-2 | B-09, B-10 | T-43, T-25, T-51 |
| FR-019 | US-2 | B-07, B-08 | T-16, T-70 |
| FR-020 | US-3 | B-11, B-12, B-13 | T-45, T-47 |
| FR-021 | US-3 | B-11, B-14, B-41 | T-48 |
| FR-022 | US-5 | B-26 | T-24 |
| FR-023 | US-6 | B-30 | T-27, T-74 |
| FR-024 | US-7 | B-31, B-33 | T-28, T-32, T-33 |
| FR-025 | US-5 | B-17 | T-42, T-19 |
| FR-026 | US-7 | B-31 | T-28 |
| FR-027 | all | — | docs-verifier |
| FR-028 | US-6 | B-30 | T-54 |
| FR-029 | US-6 | B-36 | T-55 |
| FR-030 | US-5 | B-35 | T-53 |
| FR-031 | US-5 | B-45, B-24 | T-31, T-59 |
| FR-032 | US-5 | B-22, B-46 | T-60, T-70 |
| FR-033 | US-5 | B-37 | T-56 |
| FR-034 | US-5 | B-38, B-33 | T-57, T-77 |
| FR-035 | US-3 | B-40, B-12 | T-46, T-62 |
| FR-036 | US-3 | B-06 | T-63 |
| FR-037 | US-3 | B-14, B-41 | T-64, T-65 |
| FR-038 | US-3 | B-42 | T-66 |
| FR-039 | US-2 | B-44, B-06 | T-44 |
| FR-040 | US-2 | B-43 | T-61 |
| FR-041 | US-2, US-3 | B-34, B-42, B-46 | T-52, T-70 |
| FR-042 | US-7 | B-49 | T-68 |
| FR-043 | US-5 | B-48, B-18 | T-69 |
| FR-044 | US-2 | B-47 | T-71 |
| FR-045 | US-7 | B-50, B-33 | T-41, T-67 |
| FR-046 | US-6 | B-36, B-52 | T-72, T-75 |
| FR-047 | US-6 | B-51 | T-73 |
| FR-048 | US-5 | B-24 | T-35, T-36 |
| FR-049 | US-5 | B-59, B-60, B-61, B-62 | T-85, T-86, T-87, T-88, T-89 |
| FR-050 | US-5 | B-63, B-65 | T-90, T-98 |
| FR-051 | US-5 | B-64 | T-92, T-93, T-94, T-95, T-96, T-97, T-99, T-101 |
| FR-052 | US-5 | B-66 | T-100 |
| FR-053 | US-5 | B-68 | T-103, T-104 |
| FR-054 | US-1 | B-67 | T-102 |
| FR-055 | US-5 | B-69 | T-11, T-102, T-105 |

## 16. Hands-on UAT rows (Mac first; standard UAT model per Q6)

| Row | Platform | Steps | PASS evidence |
|---|---|---|---|
| U-M1 | macOS installed | Mia: 2-page docx with too-wide table; preview, fix, re-preview | two groups, different versions; validator opens final docx |
| U-M2 | macOS | General Purpose: deck with overflowing title | defect named; fixed preview clean |
| U-M3 | macOS | Workbook with formulas + chart | thumbnails show computed numbers, chart |
| U-M4 | macOS | FX-PDF30 default, then "11-20" | 10 + 10 thumbnails, "of 30", next batch |
| U-M5 | macOS | Upload FX-PDF30 to image-only model; ask page 2, then 25 | page 2 from images; agent previews materialized path for 25 |
| U-M5b | macOS | `openai/gpt-6-luna`: upload, then `read_file`, then `preview_document` on a docx | redacted outbound evidence of the document part for all three; no retained tool-PDF bytes |
| U-M6 | macOS `--without-office` | docx, pdf | "not installed"; pdf works |
| U-M7 | macOS | FX-MACRO, FX-LINK (positive controls first) | no marker, no hit, rendered |
| U-M8 | macOS | Stop during xlsx; kill gateway mid-conversion and mid-render; restart | stopped; no `soffice`/`omnipus-pdfium`, no folders |
| U-M9 | macOS `--without-pdf-renderer` | pdf with UAT model; with gpt-6-luna | text + unavailable card (no Retry); native PDF for gpt-6-luna |
| U-M10 | macOS | Corrupt the installed renderer; preview with gpt-6-luna; restore; Retry | partial success card; Retry works after restore |
| U-M11 | macOS | Upload via a configured channel (non-workspace ref) to image-only model; ask page 25 | answered via materialized path |
| U-A1 | macOS browser | Keyboard: open, Tab around, 3 pages, close; reload; virtualized scroll then close | focus contained, announced, returned per §5.4 |
| U-A2 | macOS VoiceOver | Preview arrives; unavailable card | one announcement each |
| U-L1 | Linux deb `omnipus` | U-M1 + U-M3 | as macOS |
| U-L2 | Linux `omnipus-nooffice` | docx | "not installed" |
| U-L3 | Docker | U-M2 | as macOS |
| U-W1 | Windows clean machine | install LibreOffice, `omnipus office register`, renderer zip + register, preview docx | works; docs state no file confinement |
| U-W2 | Windows | Stop during conversion | whole tree gone |
| U-W3 | Windows unregistered | docx, pdf | "not installed"; pdf native/text, unavailable card |
| U-S1…U-S4 | macOS real runtime | per format: create from brief, edit, preview, run inspector | each script's output matches the oracle the validator derives from the brief |

Installer rows need Apple silicon; the Intel dev Mac uses operator registration.

## 17. User-facing documentation (drafted by backend-lead; chat parts by frontend-lead; audited by docs-verifier)

| Page | Update |
|---|---|
| **New** `docs/documents.md` ("Documents in chat") | How agents preview documents; what the model receives per model kind; thumbnail groups, viewer keys, unavailable card and Retry; kept until the chat is deleted; macOS best-effort memory limit; unconfined and Windows exposure; "The Library gains no Office preview" |
| `docs/README.md`, `docs/tools.md` | link to `documents.md` |
| `docs/reference/built-in-tools.md` | `preview_document` row; `read_file` PDF behaviour |
| `docs/skills.md` | `elicify-office`, table, calculate-on-open note |
| `docs/getting-started.md` | components, sizes, `--without-*`, `omnipus-nooffice`, `omnipus office register` |
| `docs/docker.md` | image contents |
| `docs/security.md`, `docs/operations/sandbox-limitations.md` | separate programs and limits; macOS best effort; writer exclusion; unconfined and Windows exposure; agents can never get write access to a program folder: approvals, mounts (including ones that contain a program folder, such as `/`) and new or changed write allowances that would give it — or that Omnipus cannot reduce to a folder — are refused with the §3.2 texts, and §3.2's boundary items (access approved before a folder became a program folder, a link redirected during a running preview) are stated; Linux memory limit counts the program's working memory (data limit), and the `memory limit not enforced: <reason>` disclosure on kernels older than 4.7 or with `ignore_rlimit_data` (§3.1 D1.4; also one sentence in `docs/documents.md` "Limits and isolation") |
| `docs/providers-and-models.md` | catalog decides native PDF; size limit; paging |
| `docs/operations/platform-support.md` | per platform/route |
| `docs/documents.md` "What previews need" (2026-11-05) | replace "access added later (a settings change or a new approval) turns the component off" and the "What the check does not cover yet" bullet with the §3.2 refusals, their exact texts and the §3.2 boundary items; keep the in-force ("unsafe location", `doctor`) behaviour |
| The user page that documents workspace mounts (backend-lead locates it; a grep of `docs/*.md` on 2026-11-05 found no page naming today's mount refusals) | a mount that is, lies inside or contains a document preview program folder is refused (supersedes warn-and-allow for `/` and other containing folders in that case), with the REST and picker texts of §3.2 |
| `docs/documents.md` "What previews need" (2026-11-07, §3.4 D9b) | while `config.json` holds an entry that would give agents write access to a program folder, every save is refused with the §3.2 text, including sign-in, password change and finishing onboarding; sign-out still signs out on screen; the starting-messages screen shows the fixed D9b row 34 sentence instead; fix the entry in `config.json` to clear it |
| `docs/operations/sandbox-limitations.md` "Document preview programs", `docs/documents.md` "Limits and isolation" and "What previews need", `docs/security.md` "History is out of reach" and "Document preview program folders" (2026-11-08, §3.4 D8c) | Drafted by backend-lead with the omnipus-voice skill (neutral voice, plain words, one way forward; `.claude/skills/omnipus-voice/SKILL.md`, read from `landing/notifications` until it lands here); docs-verifier audits each sentence against the code. Replaces D8b's "No user-doc change". (1) `sandbox-limitations.md`, new paragraph after "…the result says `converted without sandbox: <reason>`.": "When the kernel sandbox is active, LibreOffice can read only its own program files, the folder that holds the Omnipus program, the system libraries and fonts, and the temporary folder of the current preview. On macOS it can also read, but not change, the checked list of LibreOffice parts it may load. It cannot read your files, the agents' files, other previews or anything else Omnipus stores." (2) `documents.md` "Limits and isolation", after "the programs can write at most their own temporary folder;": "LibreOffice can read only the folders listed in [Sandbox limitations](operations/sandbox-limitations.md#document-preview-programs), never your files or other previews;". (3) `documents.md` "What previews need", new bullet: "On macOS and Linux, previews use the fonts installed for all users and the fonts that come with LibreOffice. A font installed only for your user account is not used, and text in that font is shown in a similar font. To use that font in previews, install it for all users." (4) `security.md` "History is out of reach": keep "Saved versions live under Omnipus's `system` folder, which no tool or program can read." and add: "The one exception is LibreOffice on macOS when it is registered with `omnipus office register`: it can read, but not change, one folder there that holds the checked list of LibreOffice parts it may load. That folder holds no saved versions." (5) `security.md` "Document preview program folders", at the end: "LibreOffice itself can read only the folders listed in [Sandbox limitations](operations/sandbox-limitations.md#document-preview-programs)." If T-102 adds a read (§3.4 D8c "Adding a read"), sentence (1) names it |

## 18. Work breakdown

| # | Owner | Work |
|---|---|---|
| W0 | architect; team-lead | Dated amendment of **Built-in agent configuration, skills, and visual file reading** (D5 components, D6 tool + thumbnails + native-PDF rule, native-component exception); root `CLAUDE.md` ADR-090 paragraph; T11 §2.4 pointer |
| W1 | qa-lead (A) | RED for every active row of §13 (T-01…T-80, T-85…T-90, T-92…T-105; T-81…T-84 retired), fixtures, Windows inventory; T-91 is a manual measurement job, run by team-lead |
| W2 | backend-lead | §5.2 contract first; S-1, S-2; renderer + CI builds; launcher; protocol; tool; converter, sandbox, budgets, recovery; §4 routing and serializers; thumbnails, display field, replay, Retry route; registration commands; policies; packaging; readiness and runtime changes; notices; docs drafts |
| W2b | backend-lead | §3.4 D1–D8 and D8c in `pkg/preview` (D8b.1 and D8b.2 withdrawn), with D8c's three callers in `pkg/tools` and `pkg/gateway`; §3.4 D9 and D9b as their own commit: contract entries and regeneration first, then the helper, the 34 routes, the own rules and the cascade log. Security-lead reviews D9b rows 21–23 and 33 at the gate (founder Q26), and D8c |
| W3 | frontend-lead | §5.3–5.5 after the contract lands; viewer focus; docs draft |
| W4 | prometheus-prompt-engineer | `elicify-office` per AD-14/§6; prompts; tool description |
| W4b | backend-lead (script code) + prometheus (text) | inspector fixes that T-72 proves necessary |
| W5 | qa-lead (B) | CHECK |
| W6 | docs-verifier | §17 audit |
| W7 | uat-tester + uat-validator | §16 rows on the exact candidate |

Order: contract → RED ∥ S-1/S-2 → GREEN (W2 then W2b, ∥ W3 ∥ W4 ∥ W4b; W2b's contract entries land before its handler changes) → CHECK → 5-reviewer gate → candidate CI → UAT → founder yes.

## 19. Ambiguities and assumptions

| Item | Assumption | Resolved by |
|---|---|---|
| macOS memory | best effort (FQ1 r2); watchdog only if pure-Go sampling exists | S-1 |
| Upstream PDF support per provider/protocol | Unknown; companion form where tool-result PDF is undocumented | S-2 |
| Every turn has a workspace | Inferred from retired unbound routing | T-71 |
| LibreOffice 26.8.1, Linux arm64, Alpine, musl renderer | Unknown; release prerequisites | S-1, T-33 |
| xlsx print layout | print layout | UAT |

## 20. Holdout evaluation (not in TDD or traceability)

1. Mia's one-page flyer opens in Word without overflow.
2. A 25-slide deck previewed "21-25" shows the last five slides, also in the chat.
3. A financial workbook preview shows totals matching a hand calculation.
4. A password-protected PDF yields one clear sentence, no images.
5. A deck linking a canary image produces no canary hit on macOS.
6. A 300-page PDF to an image-only model: page 5 from images, page 200 after paging.
7. Killing the gateway mid-preview, restarting and previewing again leaves no folders or processes.
8. A Claude 5.x or GPT-6-class model receives an uploaded PDF natively.
9. With the renderer removed, a native-PDF model still answers about page 3 and the person sees the unavailable card.

## 21. Founder questions

None open. FQ1 (r2) = B and FQ2 (r2) = A are applied. 2026-11-05: S2 = A (with both side effects, A), SR1 and O8 = A are applied (§3.1 D3.7, §3.2, §3.3, T-91). One open point for team-lead to raise: §3.3 "Write folders `ConfiguredWriteRoots` does not list" (Unknown). Two architect decisions the founder may veto: (1) Windows and dev Macs use an operator registration command instead of an environment variable (R2-MAJ-005); (2) a component that any agent can write is disabled for everyone until that access is removed (R2-MAJ-012).

2026-11-07: Q18, Q19, Q22, Q23 and Q24 = A are applied (§3.4). Q26 = A is applied (§3.4 D9b), which closes the open point on the other config writers. §3.3 adds two known existing items: the refused sign-out and the sign-in poll's undeclared 500. 2026-11-08: architect ruling D8b (§3.4) applied; no founder question. Later the same day: founder Q35 = A and Q36 = A applied (§3.4 D8c), which withdraws D8b.1 and D8b.2.

## 22. Evidence used
Code reads on `origin/release/v0.1.1` (`4cad412bf`): thumbnail storage (`normalization.go::storeInlineDataURL`, `media/store.go::SessionInlineScopePrefix`, `CleanExpired`, `rest_sessions.go`), delivery (`webchat_channel.go::SendMedia`, `replay.go::buildMediaFrame`, `MediaFrame.yaml`, `MediaPart.yaml`), chat UI (`ChatScreen.tsx::InlineMedia`, `ChatImage.tsx`, `MediaLightbox.tsx`, `image-lightbox.tsx`), PDF routing (`loop_media.go::resolveMediaRefsWithOffload`, `pdfCapableModel`, `encodePDFToDataURL`, `media_present.go`), runtime (`documentruntime/provision.go::ProvisionFirstParty`, `shell_env.go::documentEnvTurn`), packaging (`package_chrome.go`, `.goreleaser.yaml`, `Dockerfile.goreleaser`, `install.sh`), CI (`cross-platform.yml::windows-tools-tests`). Grill round 2 report (its provider-serializer, launcher, recovery, viewer and extractor citations at `b7d4483c9` are relied on as reviewer-verified, not re-read here), founder answers, skills review, spike, review B, T11 files read first-hand on 2026-10-30.

2026-11-07 (§3.4): code reads on `landing/office-preview` at `1c2ca4870`: `pkg/preview/scratch.go`, `scratch_anchor.go`, `programs.go`, `confine_unix.go`, `launcher/*`; `pkg/sandbox/seatbelt_profile.go`, `seatbelt_cache.go`; `pkg/fspolicy/secretset_kernel.go`; `pkg/gateway/rest_config.go::writeRefusableConfigJSONLocked` and the D9 handlers. Declared responses read from `contracts/openapi.yaml`. The macOS boundary was checked on macOS 26.5.2 under a hand-built profile, not the production render and not a live gateway; that check is kept out of this repository. Linux and Windows behaviour was read, not run.

2026-11-07 (§3.4 D9b): every call site of the shared config writer in non-test `pkg/gateway` files listed by script; failure handling read per handler; declared responses and the `ChannelOutboundRejection` schema read from `contracts/`; the screen's error display read in `src/lib/api-error.ts`, `src/routes/-login-local.tsx`, `src/components/providers/SignInDialog.tsx`, `src/components/connectors/ChannelOutboundSection.tsx`.

2026-11-08 (§3.4 D8b): code reads on `landing/office-preview` at `a4197c650`: `pkg/preview/component/register.go`, `component.go`, `pkg/preview/programs.go`, `confine_unix.go`, `scratch_anchor.go`, `service.go`; `pkg/fspolicy/inventory.go`, `secretset_kernel.go`, `policy.go`; `pkg/sandbox/turn_policy.go`, `derive_from_fspolicy.go`; `pkg/tools/preview_document.go`, `read_file_pdf.go`; the developer's T-102 run log (preview 1 failed: registry folder could not be opened).

2026-11-08 (§3.4 D8c): code reads on `landing/office-preview` at `b9802d19f` (the code is unchanged since `6c0a7f87d` except four test files): `pkg/preview/confine.go`, `confine_unix.go`, `confine_linux.go`, `programs.go::officeConversion.run`, `service.go` (`Request`, `Service.Preview`, `Service.officeComponent`), `scratch.go::call.makeSocketDir`, `component/component.go::load`, `::Verify`, `::VerifyIfChanged`; `pkg/sandbox/sandbox.go::DefaultPolicyForModel`, `derive_from_fspolicy.go::DeriveKernelPolicy`, `turn_policy.go::KernelPolicyForTurn`, `sibling_grants.go::ExpandRulesExcluding`, `sandbox_linux.go`, `seatbelt_profile.go` (system preamble, `renderSeatbeltProfileState.renderDeniedPaths`); `pkg/tools/preview_document.go::PreviewDocumentTool.Execute`, `read_file_pdf.go::ReadFileTool.pdfPageRenderer`; `pkg/gateway/rest_preview_retry.go::restAPI.renderPreviewRetry`; security-lead's early read of D8b; user pages `docs/operations/sandbox-limitations.md`, `docs/documents.md`, `docs/security.md`; the omnipus-voice skill on `landing/notifications`. Nothing was run: whether the list suffices for LibreOffice is Unknown until T-102 runs.
