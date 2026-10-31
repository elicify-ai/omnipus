# Feature Specification: Built-in office-document preview (`preview_document`)

Status: Draft

| Field | Value |
|---|---|
| Created / revised | 2026-10-30 (architect, plan-spec) / fix round 1 (grill 1: BLOCK 1C/10M/4m) / RQ update (native pdfium program, gpt-6-luna row) / **final correction after grill 2 (BLOCK 1C/12M/2m/2obs) — no third grill** / **2026-10-31 architect amendment after the O3 Linux measurement (§3.1): Linux memory limit = data limit, launcher order unchanged, LibreOffice exit-81 retry, shared pdfium library (D1 A)** |
| Size | Feature (tools, agent media path, providers, sandbox, packaging, chat UI, skills) → RED/GREEN/CHECK, 5-reviewer gate, hands-on UAT (touches `pkg/agent`) |
| Baseline | `origin/release/v0.1.1` at `4cad412bf` (read-only); grill 2 used `b7d4483c9`. backend-lead re-checks every cited symbol on the planning commit |
| Supersedes | T11 spec (`landing/t11:docs/internal/specs/t11-documents-and-3p-agents-spec-full.md`) §2.4: the on-hold `convert_document` design (#1489) is retired, not built |
| Issues | #1489 (re-scoped here); #1506 (Omnipus signing: gates the trimmed macOS LibreOffice and the renderer's signing) |
| Previous versions | `.bak-round1` (before fix round 1), `.bak-rq` (before RQ update), `.bak-round2` (before this final correction) |
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
- **Writer exclusion (R2-MAJ-012).** A component is usable only while its root is not inside, and does not contain, any write root of **any** agent: workspaces, mounts, operator allowances and grants from configuration. Checked at gateway start, on every configuration reload and mount/grant approval, and per call against the calling turn. If any overlap exists the component is disabled for everyone: "unsafe location", plus a `doctor` line naming the overlapping grant. Not re-hashed per call: the bundle's libraries and configuration; their protection is this exclusion plus the kernel sandbox where active. **Honest boundary:** on Windows (Q5) and in unconfined mode (FQ2 r1) agents with `bash` are not kernel-prevented from writing an install folder; operator-registered components rest on the operator's registration. Documented.

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

**AD-7 Converter sandbox.** Converter-specific input to the existing policy derivation and hardened spawn: working folder = scratch; writes only scratch and (macOS) the socket folder; none of the caller's write roots; reads per the platform's per-turn read model plus the component root; environment built from nothing (`PATH` minimal, `LANG`, `HOME`/`TMPDIR` in scratch, macOS `URE_MORE_SERVICES` and `OSL_SOCKET_PATH` once). macOS: production per-turn profile + exactly the 3 rules of `converter-additions.sb` for a per-call 0700 `/private/tmp/o-<8 random>` (< 104 chars), network denied except that socket (T-30; if it breaks conversion, team-lead escalates); no new Mach service. Linux: Landlock with the same file rules; TCP bind/connect denied where Landlock supports it (not UDP — documented). Windows: Job Object only (Q5). LibreOffice profile: macros never run, links never updated, formulas recalculated on load. `omnipus-pdfium` and `extract-text`: no writes, no network. **Unconfined (FQ2 r1):** if macOS/Linux cannot set up this sandbox, the programs still run with AD-6 limits; result text `converted without sandbox: <reason>`, audit field, `doctor` line.

**AD-8 Scratch and recovery (R2-MAJ-003).** Per call `$OMNIPUS_HOME/tmp/preview/<id>/` (macOS socket folder separate).
1. Write `owner.json` atomically: state `preparing`, gateway PID + start time.
2. Spawn `run-limited` (it waits for the go-byte; if stdin closes first it exits without starting anything).
3. Record child identity atomically: state `running`, launcher PID + process start time + executable, process-group ID, intended target executable, socket folder.
4. Send the go-byte. 5. On exit: state `done`, then remove folders. A second start within the same call (LibreOffice exit-81 retry, §3.1 D3) repeats steps 2–4 for the new launcher after the first one's exit and group kill.
- **Startup:** for each folder whose owner gateway is dead (PID + start time): `preparing` → nothing can have started; remove. `running` → for each recorded process, kill only if PID, start time **and** executable (launcher, `omnipus-pdfium`, `soffice`/`soffice.bin` of the verified component) all match; then confirm termination (poll ≤ 5 s) before removing. Unconfirmed, unreadable record, or a live owner gateway (two gateways sharing a home) → keep, warn in log and `doctor`. Windows: the gateway's death already closed the job; only folders remain.

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

Status: decided 2026-10-31 (architect). Amends AD-2, AD-4, AD-6, AD-8 above; adds T-76…T-80. AD-6 delegated the Linux choice "address-space (or data) limit chosen in S-1, proven by allocation bombs" to measurement; this records that choice. No founder decision is reopened: budgets, FQ1 (r2) "Linux keeps enforced limits" and the go-byte rule stand.

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

**Affected components.** `pkg/preview/launcher/child_linux.go::applyLimits`, `pkg/preview/launcher/launcher.go::Limits` / `::parseArgs` / `Limits.args`, `pkg/preview/launcher/child_unix.go` (order unchanged), `pkg/preview/programs.go` (office conversion retry; limits), the Linux `doctor` and result-text disclosure path used for `unconfined`, `docs/documents.md` and `docs/operations/sandbox-limitations.md` (§17).

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
| `openapi.yaml` new route | `POST /sessions/{session_id}/previews/{tool_call_id}/retry` → 202 `{status: started}`; 403 no access to the session or source; 404 unknown preview; 409 `preview_source_changed` (current file digest ≠ recorded version); 503 `preview_renderer_unavailable`. The result arrives as a new `MediaFrame` with the same `tool_call_id`. Same limits and admission as the tool; authorization = the requesting person's access to the session and the source. |

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

**Prohibitions.** Run a program an agent names or places; search `PATH`; load LibreOffice or pdfium into the gateway; keep a long-running converter; retry unfiltered or via another program; add Mach services or LaunchServices; write PNG/PDF into the workspace (except upload materialization, §4.1); put thumbnails into model media; send thumbnails to non-web channels; trust exit codes; download components at runtime; give the renderer files or network; claim a hard memory cap on macOS; claim rendered pages when none were rendered; let an adapter silently drop a native PDF; keep the old skill names or the PDF model list; use Poppler; add OCR, workbook writeback or a Library preview.

**MV-1 messages** (prefix `preview_document: `): `unsupported file type; supported: .docx, .xlsx, .pptx, .pdf` · `file content does not match its .<ext> extension` · `file exceeds the 50 MiB preview limit` · `pages must be "N" or "N-M" with 1 <= N <= M <= 9999 and at most 20 pages` · `the document has <N> pages` · `password-protected documents cannot be previewed` · `Office preview is not installed. Re-run the Omnipus installer with the Office preview component, or register LibreOffice with "omnipus office register" (PDF preview works without it).` · `Office preview is damaged (<reason>). Reinstall or re-register the Office preview component.` · `<component> is in a folder an agent can write (<grant>); disabled until that access is removed.` · `timed out after 120 s` · `stopped` · `conversion failed: LibreOffice produced no readable PDF` + filtered stderr tail · `the PDF could not be opened (damaged or unsupported)` · `rendering stopped: <limit> exceeded` · `page images unavailable: <reason>` · `thumbnails stored: <k> of <N>` · `thumbnails unavailable: storage failed` · `[no extractable text — the PDF may be scanned]` · read denied = `read_file`'s result.
**MV-2** converter environment = AD-7 list. **MV-3** macOS profile = per-turn + 3 rules (+ network); Mach = 2. **MV-4** bash composition byte-identical except AD-3. **MV-5** AD-6 budgets. **MV-6** renderer/extract open no file and no socket. **MV-7** protocol v1 rules.

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
| T-11 | Real Seatbelt child; profile diff; Mach = 2 | Integration (Mac) | MV-3 |
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
| T-66 | Retry route: unchanged → new frame same group; changed → 409; no access → 403; limits apply | Integration | B-42 |
| T-67 | Windows guard proven: exclude one family → job fails while read-boundary tests still pass | CI | B-50 |
| T-68 | Windows clean machine registration, tamper, unregister | Integration (Windows) | B-49 |
| T-69 | Writer exclusion: other agent's mount/grant covering the root disables for all; approval-time check | Integration | B-48, B-18 |
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
| FR-048 | MUST state the macOS best-effort memory boundary in UI text, `doctor` and docs, with no hard-cap claim. |

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
| FR-012 | US-5 | B-21, B-22, B-24, B-25, B-53, B-55 | T-12, T-35, T-36, T-37, T-38, T-39, T-40, T-76, T-79, T-80 |
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
| `docs/security.md`, `docs/operations/sandbox-limitations.md` | separate programs and limits; macOS best effort; writer exclusion; unconfined and Windows exposure; Linux memory limit counts the program's working memory (data limit), and the `memory limit not enforced: <reason>` disclosure on kernels older than 4.7 or with `ignore_rlimit_data` (§3.1 D1.4; also one sentence in `docs/documents.md` "Limits and isolation") |
| `docs/providers-and-models.md` | catalog decides native PDF; size limit; paging |
| `docs/operations/platform-support.md` | per platform/route |

## 18. Work breakdown

| # | Owner | Work |
|---|---|---|
| W0 | architect; team-lead | Dated amendment of **Built-in agent configuration, skills, and visual file reading** (D5 components, D6 tool + thumbnails + native-PDF rule, native-component exception); root `CLAUDE.md` ADR-090 paragraph; T11 §2.4 pointer |
| W1 | qa-lead (A) | RED for every row T-01…T-75, fixtures, Windows inventory |
| W2 | backend-lead | §5.2 contract first; S-1, S-2; renderer + CI builds; launcher; protocol; tool; converter, sandbox, budgets, recovery; §4 routing and serializers; thumbnails, display field, replay, Retry route; registration commands; policies; packaging; readiness and runtime changes; notices; docs drafts |
| W3 | frontend-lead | §5.3–5.5 after the contract lands; viewer focus; docs draft |
| W4 | prometheus-prompt-engineer | `elicify-office` per AD-14/§6; prompts; tool description |
| W4b | backend-lead (script code) + prometheus (text) | inspector fixes that T-72 proves necessary |
| W5 | qa-lead (B) | CHECK |
| W6 | docs-verifier | §17 audit |
| W7 | uat-tester + uat-validator | §16 rows on the exact candidate |

Order: contract → RED ∥ S-1/S-2 → GREEN (W2 ∥ W3 ∥ W4 ∥ W4b) → CHECK → 5-reviewer gate → candidate CI → UAT → founder yes.

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

None open. FQ1 (r2) = B and FQ2 (r2) = A are applied. Two architect decisions the founder may veto: (1) Windows and dev Macs use an operator registration command instead of an environment variable (R2-MAJ-005); (2) a component that any agent can write is disabled for everyone until that access is removed (R2-MAJ-012).

## 22. Evidence used
Code reads on `origin/release/v0.1.1` (`4cad412bf`): thumbnail storage (`normalization.go::storeInlineDataURL`, `media/store.go::SessionInlineScopePrefix`, `CleanExpired`, `rest_sessions.go`), delivery (`webchat_channel.go::SendMedia`, `replay.go::buildMediaFrame`, `MediaFrame.yaml`, `MediaPart.yaml`), chat UI (`ChatScreen.tsx::InlineMedia`, `ChatImage.tsx`, `MediaLightbox.tsx`, `image-lightbox.tsx`), PDF routing (`loop_media.go::resolveMediaRefsWithOffload`, `pdfCapableModel`, `encodePDFToDataURL`, `media_present.go`), runtime (`documentruntime/provision.go::ProvisionFirstParty`, `shell_env.go::documentEnvTurn`), packaging (`package_chrome.go`, `.goreleaser.yaml`, `Dockerfile.goreleaser`, `install.sh`), CI (`cross-platform.yml::windows-tools-tests`). Grill round 2 report (its provider-serializer, launcher, recovery, viewer and extractor citations at `b7d4483c9` are relied on as reviewer-verified, not re-read here), founder answers, skills review, spike, review B, T11 files read first-hand on 2026-10-30.
