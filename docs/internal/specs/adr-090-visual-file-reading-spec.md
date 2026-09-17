# Feature Specification: Visual file reading

**Created:** 2026-09-17
**Status:** Initial grill-spec passed after corrections; independent Opus review pending; implementation and runtime validation pending.
**Authority:** [ADR-090](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/docs/internal/architecture/ADR-090-built-in-agents-skills-and-visual-reading.md), decision D6 and §§6.6, 10, 13. Companion: [agent configuration and skills specification](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/docs/internal/specs/adr-090-agent-configuration-and-skills-spec.md).
**Requirements confirmation:** Founder explicitly selected image support in the existing readers on 2026-09-17. No separate image tool, browser prerequisite, or new automatic model switching.

## Problem, actors, and scope

Mia and General Purpose can produce document files without being able to inspect their rendered pages through the file reader. This specification makes supported local images available as real visual input to the current model. It applies to every agent already permitted to read the relevant file, including a Judge reading files within the reviewed workspace; it grants no new permissions. Judge file reading uses the existing workspace boundary, not a new per-task file allowlist. Only activity-record inspection retains its separate session scope.

In scope: image reading through `read_file` and `library_read`, private inspection, bounded normalization, model capability handling, provider conversion, replay, and document quality verification. Out of scope: a new viewer tool, Office generation engine, browser requirement, model-switching service, migrations, worktree isolation, new permission layers, or changes to ordinary file delivery. The companion specification supplies document skills and dependencies.

## User Stories & Acceptance Criteria

### User Story 1 — Read images using familiar tools (Priority: P0)

An agent reads a local page image or a library image and receives visual content, allowing it to assess appearance rather than relying on extracted text.

**Why this priority:** It is the entry point for the entire capability.
**Independent Test:** Read the same known PNG and JPEG through both readers and inspect their returned image content.

**Acceptance Scenarios:**

1. **Given** a readable valid PNG or JPEG and a vision-capable model, **When** the agent reads the image without pagination, **Then** it receives the image and its dimensions through either reader.
2. **Given** a readable image, **When** the caller explicitly supplies an offset or length, **Then** a numeric value receives an image-pagination refusal and a malformed type retains the existing argument-type error, with no partial image in either case.
3. **Given** existing text, supported Office/PDF documents, or opaque binary input, **When** the caller reads it, **Then** text pagination, document text extraction, and non-image binary refusal retain their existing results.

### User Story 2 — Keep image reading within existing access boundaries (Priority: P0)

An agent can inspect only images it is already authorized to read. A private inspection does not publish intermediate work or expand another agent's evidence access.

**Why this priority:** Reusing media handling must not weaken filesystem authorization.
**Independent Test:** Use an allowed workspace image, a forbidden image, and a Judge image inside the reviewed workspace while observing access outcomes and channel delivery.

**Acceptance Scenarios:**

1. **Given** a permitted workspace, mount, library, or Judge-evidence image, **When** the agent reads it, **Then** the allowed content is inspectable and the existing correlated read audit is retained without delivering a file to the user.
2. **Given** a missing, denied, out-of-scope, or metadata-protected target, **When** the agent requests it, **Then** the existing missing/access explanation is returned and no image content is disclosed.
3. **Given** a path changes during a read or existing access changes before a later candidate request, **When** the system presents that read result during the live turn, **Then** an allowed request uses only the bounded snapshot from the originally authorized regular file and a newly denied request reports the existing access refusal; later turns require a fresh authorized read.

### User Story 3 — Bound and explain image processing (Priority: P0)

An agent receives an appropriately sized image with enough information to know whether fine details need a crop. Invalid or oversized files produce useful limitations.

**Why this priority:** Large and corrupt image files are ordinary inputs, not exceptional system failures.
**Independent Test:** Exercise small, resized, corrupt, unsupported and limit-boundary images without contacting a model.

**Acceptance Scenarios:**

1. **Given** a supported valid image within input limits, **When** it is prepared for the chosen model, **Then** the model receives an image within its existing output limits, the original and presented dimensions, and a resize notice when dimensions changed.
2. **Given** corrupt image content or an unsupported image format, **When** the agent reads it, **Then** it receives a distinct invalid-image or unsupported-format explanation and no claim of inspection.
3. **Given** image bytes or dimensions at or beyond the configured limits, **When** the agent reads it, **Then** values within the limits can proceed and values beyond them are refused before unbounded reading or full decoding; cancellation or deadline expiry checked between bounded regular-file reads returns the interrupted outcome without image content.

### User Story 4 — Deliver images through the actual model adapter (Priority: P0)

An agent's provider receives the image associated with its read result. The system accurately distinguishes an unsupported model from a successful visual request.

**Why this priority:** A local image result is useless if the outgoing model request drops its image.
**Independent Test:** Capture real serialized requests for every supported adapter family, decode the image parts, and validate their correlation with the originating tool call.

**Acceptance Scenarios:**

1. **Given** a supported vision provider and a successful image read, **When** the next model request is sent, **Then** its provider-valid request contains the corresponding visual image, not merely base64 text, with the originating tool result preserved.
2. **Given** a model explicitly recorded as lacking image capability or a transport unable to carry images, **When** an image result is prepared, **Then** file access is preserved and explicit image-support guidance is returned without sending unsupported image input or automatically choosing another model.
3. **Given** unknown capability, a provider rejection, retry, or an existing fallback candidate, **When** the system attempts the request, **Then** it uses the actual candidate's capabilities and image limits, preserves valid correlation, and reports any unresolved failure without fabricating inspection.

### User Story 5 — Complete and retain honest document inspection (Priority: P1)

Mia and General Purpose render, inspect, correct, and re-inspect documents before delivery. Their durable conversation retains a text record of the inspection, while image bytes live only within the active turn and its provider requests/retries.

**Why this priority:** This proves the image plumbing enables the intended user workflow.
**Independent Test:** Give a live vision model a rendered document with independently recorded defects and observe its corrected output.

**Acceptance Scenarios:**

1. **Given** working document dependencies and a page with known layout defects, **When** Mia or General Purpose performs document validation, **Then** it identifies the defects, corrects them, inspects the new rendering, and delivers the requested artifact separately.
2. **Given** rendering, image access, or visual inference cannot complete, **When** the agent evaluates readiness to deliver, **Then** it reports the specific limitation and leaves visual validation incomplete without pretending text extraction proves layout.
3. **Given** a completed inspection or an ordinary delivery result, **When** history is saved, replayed or compacted, **Then** inspection bytes and snapshot references are absent from durable records, a text marker says “not retained; re-read to view”, and replay causes no new user delivery.

## Behavioral Contract

- When a permitted supported image is read without pagination, the current model receives visual input with dimensions.
- When an image needs resizing, the presented dimensions and resize notice accompany it.
- When access, format, size, capability or provider constraints prevent inspection, the reason is visible and inspection is not claimed.
- When the agent reads for inspection, no user file delivery occurs; a separate delivery action remains necessary.
- When old text/document inputs are read, existing pagination and extraction behavior remains.
- When a later turn needs an inspected image, it must read the source again through current filesystem authorization; the historical text marker grants no access.

## Edge Cases

- Explicit offset or length, including zero, is rejected for images; omitted arguments are distinguishable from supplied defaults.
- An empty text file remains empty text. A file named as an image but containing empty or truncated image bytes is an invalid image.
- Image identification uses bytes, not just filename; a valid PNG with an unexpected extension remains an image. A claimed image extension must not turn invalid image bytes into a successful text result.
- Input at the byte/pixel ceiling is allowed to attempt normalization; one unit above is rejected. Passing input limits does not guarantee fitting provider output limits.
- A file may grow or be replaced after authorization. Reading is bounded and uses the same authorized handle; a rejected oversized snapshot is never partly presented.
- An unknown model follows existing optimistic image-capability behavior; a known non-vision model receives guidance.
- Replay contains only the inspection text marker, never a retained inspection image. A fresh read of a moved, missing or newly denied source returns the existing access/read error.
- Cancellation or provider failure leaves validation incomplete and preserves existing cancellation behavior.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

The system MUST NOT add a separate viewer, launch a web server for reading, auto-deliver read images, broaden a reader's permissions, silently switch models, or treat extraction as visual inspection. It MUST NOT reuse a raw filename to bypass an already authorized handle. Images remain tool-derived evidence rather than instructions or trusted user messages, including when an adapter needs a supplemental image message. No image bytes or base64 are written to ordinary text logs.

### Machine-Verifiable Constraints

- Minimum supported inputs: valid PNG and JPEG. Other raster formats use the existing normalizer's supported formats; unsupported formats fail explicitly. Direct SVG reads retain text behavior as specified below.
- Image acquisition supports finite regular files only. An image candidate that is a FIFO, socket, device or directory receives **“image source must be a regular file”** without opening a potentially blocking stream. Unsupported special-file image inputs do not gain a new streaming protocol.
- Let **M** be the existing configured maximum media byte size, default **20 × 1024 × 1024 bytes**. Read no more than **M + 1 bytes** from the authorized source, including sniffed bytes, and reject input larger than M.
- Maximum decoded source area **P = 16 × 1024 × 1024 pixels**, checked from dimensions before full decode with overflow-safe arithmetic. Width/height must be positive. Existing format-specific guards remain.
- Provider output byte, long-edge and encoding limits come from the existing capability/resize catalog for the actual provider/model. Do not replace them with M; input and provider output budgets differ.
- Image pagination refusal for explicitly supplied numeric values includes the phrase **“image pagination is not supported”**; malformed types retain existing argument-type errors. Image-specific failures identify **“invalid image”**, **“unsupported image format”**, **“image exceeds byte limit”**, **“image exceeds pixel limit”**, or **“image cannot fit model limits”**, as applicable. Keep existing missing/path-access errors and audit classifications.
- Non-vision, source-access and provider errors must state the cause and must not include a successful visual-inspection claim. Replay states “not retained; re-read to view”. Provider errors retain existing bounded retry/cancellation behavior.
- Successful preparation reports original width/height and presented width/height as positive pixel integers. If they differ, the explanation includes **“resized”**.
- A read produces zero outbound media-delivery events, including on replay. Existing explicit deliveries retain their existing semantics.

### SVG and raster dimension semantics

Direct `read_file`/`library_read` of SVG retains the existing text-reading behavior. Markup is not visual inspection; document skills render PNG/JPEG pages when they need to inspect appearance. This preserves text-file compatibility and does not add an SVG viewer requirement.

Other existing tools can return SVG media. For that existing branch, retain the bounded pure-Go rasterizer: finite positive fractional viewport dimensions are allowed, a missing/nonpositive viewport uses the existing 512×512 default, and the raster canvas is capped before allocation at 4096 pixels per edge and P pixels overall. Reject non-finite values and arithmetic overflow as invalid input. For this vector branch, reported original dimensions mean the resulting bounded raster canvas before model resizing, not the potentially fractional or enormous vector viewport. Identify it as an SVG rasterization. Feed that PNG through the actual candidate's existing resize/encoding pipeline so the early SVG branch cannot bypass output limits.

Every such SVG image result carries a notice that the existing limited renderer may omit unsupported SVG features; it is not proof of full-fidelity rendering. No new SVG engine or unsupported-element parser is required. A document skill must validate its actual PNG/JPEG render, not substitute this limited SVG preview. Raw SVG is never sent as an image MIME to a provider.

### Conservative Type Design

Any internal inspection-purpose field must encode the delivery-versus-inspection distinction. Do not introduce a new viewer service, policy layer, redundant wrapper types, or public API schema merely for an internal tool result field.

## Integration Boundaries

| Boundary | Input/output contract | Failure behavior | Development evidence |
|---|---|---|---|
| Filesystem and library | Authorized source bytes and source identity enter; bounded snapshot plus provenance leave | Existing access/missing errors; no unrestricted reopen | Real temp fixtures beneath the project test environment, mount/evidence scope fixtures |
| Media normalization | Snapshot and actual model budgets enter; supported image content plus dimensions leave | Explicit format/limit/normalization limitation | Real PNG/JPEG decoding and independent output checks |
| Model provider | Tool-derived text/image content enters; provider-valid correlated request leaves | Existing transport/timeout/retry behavior, accurate capability guidance | Captured serialized HTTP requests plus live visual validation |
| Channel delivery | Existing delivery-purpose results still deliver; inspection-purpose results do not | Existing delivery errors unchanged | Captured outbound bus/channel events |
| History and media storage | Text marker only: source path, hash, dimensions and originating tool-call identity; no inspection bytes or snapshot references | Replay says “not retained; re-read to view”; a fresh read uses current access checks | Inspect every durable admission/archive/session output, then roundtrip/replay/compact |
| Document skills | Rendered pages enter reader workflow; corrections and separately delivered artifact leave | Validation remains incomplete on any required missing step | Real skill/runtime workflow supplied by companion spec |

## BDD Scenarios

Each BDD identifier names one planned behavioral test group. Dataset tables expand its variations.

### Scenario: BDD-01 — Agent sees a local or library image
**Traces to:** User Story 1, Acceptance Scenario 1
**Category:** Happy Path

- **Given** a readable valid PNG or JPEG with known dimensions and a vision-capable model
- **When** the agent reads it through either reader without pagination
- **Then** the result includes actual image content and the known dimensions.

### Scenario: BDD-02 — Image pagination is refused
**Traces to:** User Story 1, Acceptance Scenario 2
**Category:** Error Path

- **Given** a readable valid image
- **When** the caller supplies any explicit offset or length
- **Then** numeric values receive the image-pagination refusal, malformed types retain their existing argument-type error, and neither returns a partial image.

### Scenario: BDD-03 — Existing reading contracts remain
**Traces to:** User Story 1, Acceptance Scenario 3
**Category:** Alternate Path

- **Given** a text, supported document, or opaque non-image binary fixture with independently established baseline output
- **When** the caller reads it with the original supported parameters
- **Then** the text/extraction/refusal matches that baseline.

### Scenario: BDD-04 — Authorized inspection remains private and audited
**Traces to:** User Story 2, Acceptance Scenario 1
**Category:** Happy Path

- **Given** an allowed workspace, library, mount or Judge image in the reviewed workspace
- **When** the agent reads it
- **Then** the image is available for inspection, the correlated read audit is recorded, and no outbound delivery occurs.

### Scenario: BDD-05 — Forbidden or missing image is not disclosed
**Traces to:** User Story 2, Acceptance Scenario 2
**Category:** Error Path

- **Given** a missing, forbidden, metadata-protected or outside-reviewed-workspace image request
- **When** the agent reads it
- **Then** the appropriate existing error is returned with no image disclosure and the existing denial audit where applicable.

### Scenario: BDD-06 — Concurrent replacement does not bypass the authorized read
**Traces to:** User Story 2, Acceptance Scenario 3
**Category:** Edge Case

- **Given** an authorized regular-file image read whose source path is replaced after opening, or whose existing grant changes before a later candidate request
- **When** its result is presented within the live turn
- **Then** an allowed request uses only the original bounded authorized snapshot without reopening the replacement, a now-denied request gets the existing access refusal, and any later turn must make a fresh authorized read.

### Scenario: BDD-07 — Agent sees normalized dimensions and resize notice
**Traces to:** User Story 3, Acceptance Scenario 1
**Category:** Happy Path

- **Given** a supported image within input bounds and known model resize limits
- **When** it is prepared for the model
- **Then** the image satisfies those limits and reports original/presented dimensions with a resize notice only when changed.

### Scenario: BDD-08 — Invalid or unsupported images produce useful errors
**Traces to:** User Story 3, Acceptance Scenario 2
**Category:** Error Path

- **Given** an invalid or unsupported image fixture
- **When** the agent reads it
- **Then** the correct distinct format explanation is returned without image presentation or successful inspection.

### Scenario: BDD-09 — Image input limits remain bounded
**Traces to:** User Story 3, Acceptance Scenario 3
**Category:** Edge Case

- **Given** a fixture at M−1, M, M+1 bytes or P−1, P, P+1 pixels, including a growing regular file, non-regular image candidate or cancellation between bounded regular-file reads
- **When** the agent reads it
- **Then** allowable inputs can proceed, excessive inputs receive the corresponding byte/pixel error, non-regular image candidates receive the regular-file error without blocking acquisition, cancellation returns the interrupted outcome, no more than M+1 source bytes are consumed, and excessive dimensions are rejected before full decode. Cancellation or deadline expiry checked between bounded regular-file reads returns the interrupted outcome without image content; no asynchronous blocked-source cancellation worker is introduced.

### Scenario: BDD-10 — Every supported vision adapter preserves tool images
**Traces to:** User Story 4, Acceptance Scenario 1
**Category:** Happy Path

- **Given** a real successful image read and each supported vision-adapter fixture
- **When** the provider request is emitted
- **Then** its valid image block decodes to the expected normalized visual content and is correlated with the originating tool result.

### Scenario: BDD-11 — Non-vision model receives guidance
**Traces to:** User Story 4, Acceptance Scenario 2
**Category:** Error Path

- **Given** the exact configured provider/model is recorded as non-vision or its transport cannot carry image payloads
- **When** image input is prepared
- **Then** no unsupported image or base64 text workaround is sent, the file remains accessible under existing permissions, and guidance identifies the model or transport limitation without an automatic switch.

### Scenario: BDD-12 — Provider failure or candidate change stays honest
**Traces to:** User Story 4, Acceptance Scenario 3
**Category:** Edge Case

- **Given** unknown capability, a provider rejection, timeout/cancellation, or an existing configured fallback with different image capabilities
- **When** the existing request/retry process runs
- **Then** each actual candidate uses its own capability and size rules, valid image correlation is preserved where supported, retries remain bounded, and unresolved failure is reported without an inspection claim.

### Scenario: BDD-13 — Document defects are found and corrected
**Traces to:** User Story 5, Acceptance Scenario 1
**Category:** Happy Path

- **Given** the assigned document skill/runtime, a live vision model, and a rendered page with independently labelled clipping and overlap defects
- **When** Mia or General Purpose performs the validation workflow
- **Then** the agent identifies the defects, corrects the document, inspects a fresh rendering, and separately delivers the resulting file after validation.

### Scenario: BDD-14 — Failed validation remains incomplete
**Traces to:** User Story 5, Acceptance Scenario 2
**Category:** Error Path

- **Given** rendering, image reading, or visual inference is unavailable
- **When** the agent evaluates the document's completion
- **Then** it reports the specific limitation and keeps required visual validation incomplete.

### Scenario: BDD-15 — Replay retains inspection purpose without duplicate delivery
**Traces to:** User Story 5, Acceptance Scenario 3
**Category:** Edge Case

- **Given** a live inspection result and an ordinary delivery result
- **When** their canonical history is saved, roundtripped, replayed or compacted
- **Then** every durable record contains only the inspection text marker and no inspection bytes/base64/snapshot references, replay says “not retained; re-read to view”, and no delivery is repeated.

## Implementation Design and Existing Codebase Context

GitNexus cannot resolve this unindexed release worktree. The following is source-inspected context at baseline `3463b2d36`, not graph-derived impact evidence. The suggested Go reference-pattern index is absent; existing repository facilities below are the reuse reference.

| Source / symbol | Existing behavior and required change |
|---|---|
| [filesystem.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/tools/filesystem.go) — `ReadFileTool.Execute` | Resolves policy, protects metadata, opens an authorized handle, sniffs bytes, then rejects opaque binaries or extracts supported documents. Branch supported image detection before opaque-binary refusal; preserve argument presence before defaulting and preserve text/document behavior. |
| [library_tool.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/tools/library_tool.go) — reader wrapper | Delegate to the same reader and preserve omitted pagination arguments; do not create a second implementation with different limits or permissions. |
| [result.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/tools/result.go) — `ToolResult` / `MediaResult` | Existing Media means user delivery plus model attachment. Introduce an explicit internal inspection purpose/field and route it independently. `Silent` only affects text behavior and is not sufficient. |
| [loop_run_turn_tools.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/loop_run_turn_tools.go) — media delivery and result admission | Process inspection as model input without outbound publication or user-facing artifact emission. Preserve existing send_file/browser/MCP delivery behavior. Carry provenance through admission/history. |
| [loop_media.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/loop_media.go) — `attachToolResultMedia`, `encodeImageToDataURLCached`; `pkg/agent/media_present.go::modelSupportsImage` | Reuse normalization/capability rules, but current raw-path opening is unsuitable for protected read results. Accept a bounded snapshot captured from the already authorized handle. Existing tool-produced media must also receive capability handling. |
| [media resize](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/media/resize/resize.go) — `ResizeToFit` | Reuse provider output limits and encoding strategy. Perform byte and dimension guards before full decode. |
| [common.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/providers/common/common.go) — `SerializeMessages` | Currently builds multipart tool messages, but endpoint validity is unproven. Verify actual emitted request shape; use provider-valid supplemental image presentation if tool-role images are unsupported. |
| [responses_common.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/providers/openai_responses_common/responses_common.go) — `TranslateMessages` | Tool outputs currently drop Media. Preserve images and tool correlation for all consumers, including Codex/Azure routes using this translator. |
| [Anthropic Messages adapter](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/providers/anthropic_messages/provider.go) — `buildRequestBody` | Tool-result branch currently drops image content. Emit supported image blocks without breaking tool-result grouping. |
| [Anthropic SDK adapter](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/providers/anthropic/provider.go) — `buildParams` | Tool-result branch currently supplies text only. Preserve image payload and correlation, including streaming's shared request construction. |
| [Bedrock adapter](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/providers/bedrock/provider_bedrock.go) — `convertMessages` | Optional `bedrock` build currently builds text-only tool results although user image blocks exist. Include the tagged native adapter in preservation tests and retain its additional request/image limits. |

### Complete adapter inventory

This inventory follows the retained transports rather than counting provider brands as new adapters. A model's advertised vision capability cannot override a transport that cannot carry images.

| Retained adapter or route | Classification and required behavior |
|---|---|
| `HTTPProvider` / `openai_compat` / `common.SerializeMessages`, including factory OpenAI-compatible, Google-compatible, Ollama-compatible and custom HTTP rows | In scope: provider-valid real tool images and request capture; sharing the serializer does not prove every endpoint accepts tool-role image blocks. |
| `CodexProvider` / `openai_responses_common.TranslateMessages`, including OpenAI ChatGPT device-auth route | In scope: Responses tool-result image preservation. This is the native HTTP provider, not Codex CLI. |
| `azure.Provider` using the shared Responses translator | In scope: validate Azure request construction in addition to the shared translation test. |
| `anthropic_messages.buildRequestBody` (current catalog Anthropic protocol) | In scope: native Messages request preservation. |
| `ClaudeProvider` / `anthropic.buildParams` (retained native SDK wrapper) | In scope: native Anthropic image preservation; it is not a Claude CLI transport. |
| `bedrock.convertMessages` under the `bedrock` build tag | In scope for builds including Bedrock: tagged request tests and native image preservation. Default builds must not claim the tagged tests ran. |
| `CodexCliProvider`, `CopilotCliProvider` | Existing prompt-only transports do not carry Omnipus image payloads. Treat as unsupported-image transports with explicit guidance before model catalog optimism; no base64-in-prompt workaround. A real image-capable CLI protocol would be separate work. |
| External Claude Code CLI worker or other external coding runtime | Outside native reader/provider integration. Do not promise Omnipus tool-media support; it may use its own runtime tools. No new CLI bridge or vision protocol is added here. |
| Unknown/rejected provider and retired provider IDs | Existing construction error, not a silent text-only fallback or newly supported adapter. |

The native inventory has five serialization families: common chat-completions, Responses (including Azure), Anthropic Messages, Anthropic SDK, and Bedrock. Tests must cover the listed wrappers/routes as well as each shared serializer, streaming and non-streaming where implemented. Transport-level unsupported status takes precedence over a model's catalog image flag; unknown-model optimism applies only to a transport that supports image input.

### Implementation and impact boundaries

1. The tools package must not import the agent package. Extract the required media operation to an existing lower-level media package, or normalize the bounded authorized snapshot during loop presentation. Reuse existing decoding and budgets rather than copy them. For recognized images, test pagination presence before numeric range/default handling; otherwise an explicit zero or negative value could incorrectly take the old text-only path. Malformed parameter types retain existing input-validation errors.
2. Image acquisition accepts finite regular files only, includes sniffed bytes exactly once, and stops at M+1. Use the authorized PathHandle metadata operation before opening an image candidate, then verify the opened file is regular. The authorized regular-file open must not block on a concurrent replacement with a FIFO/device: use the existing anchored path guard with a narrow platform-appropriate nonblocking open/type verification, not a raw-path reopen or an abandoned reader goroutine. Non-regular targets named as images are refused before a potentially blocking open. Non-image special inputs retain the legacy text route and are not image-inspection candidates; existing regular text/document reading is unchanged. Turn cancellation is checked between bounded regular-file reads; no cancellable non-seekable image source protocol is added.
3. Keep image bytes, the bounded snapshot and candidate-normalized image only in live-turn/request memory, including existing retries and fallback candidates; release them when the turn ends. Before a later candidate request in that turn, re-evaluate the existing filesystem scope without reopening image content; an observable grant change uses the normal access refusal. The authority is the existing workspace/mount policy, not a new revocation registry. Preserve tool-call identity and dimensions in the live presentation. Canonical durable history stores only an inspection marker with source path, SHA-256 hash, original/presented dimensions and “not retained; re-read to view”. A later turn must issue a fresh read; never restore image data from a marker. The provider request builder performs the existing access-policy check immediately before each later candidate/retry request in the live turn, using the source identity and owning agent/workspace without reopening the path. On denial it removes the inspection image part from that request and substitutes the existing access-refusal text in the correlated tool result; it never transmits the cached bytes for that request. BDD-06/B13 must capture the outgoing request to assert the absence of those bytes.
4. Use native provider tool-result image blocks where supported. Otherwise emit a provider-valid supplemental image message clearly labelled with the originating tool name and call ID as tool-derived content. Do not invent a user instruction or omit the actual function/tool result. Request ordering must satisfy the adapter's protocol, including multiple tool calls.
5. Evaluate capability and output limits at actual request construction, including existing retries/fallbacks. Unknown model capability retains the catalog's current optimistic behavior; this feature adds no fallback selection policy.
6. Existing tool-produced images also use consistent capability handling. Their existing delivery purpose remains unchanged; private reading does not silently convert browser screenshots or MCP media into private outputs.
7. Internal-only fields need no public schema. If an implementation changes persisted data consumed by the SPA, REST or WebSocket bytes, define the corresponding contract first and regenerate the repository's types before changing consumers. No migrations are introduced.
8. Strip inspection image payloads before EVERY durable writing seam: tool-result admission/truncation archive, session append/save, checkpoints, replay/compaction persistence, audit and ordinary logs. Do not send an inspection data URL through a generic archive before stripping it. Keep live presentation separate from the canonical text-only record, including error/interrupted turns. Do not store a resolvable snapshot/media reference in place of the forbidden bytes. Reusing normalization must not put inspection snapshots or encodings in an on-disk media store/cache; any inspection cache is bounded to the live turn. This rule applies only to new read-tool inspection payloads: existing user uploads and explicit tool deliveries retain their current lifecycle; this feature does not erase those files or claim forensic deletion.

**Source-derived impact:** reader changes directly affect library reading and Judge evidence reads; result routing affects ordinary, scheduled and delegated turns; provider translation affects streaming/non-streaming requests and all adapters sharing the serializer. History/compaction and existing delivery are regression-sensitive. Graph risk levels are unavailable; no invented graph distances are claimed.

## Test-Driven Development Plan

Tests below are planned, not implemented or executed. Before writing tests, apply the repository's test-writing skill and demonstrate that a targeted mutation makes the relevant test fail. Expected image content, dimensions, limits and defect locations come from independently constructed fixtures, never the code under test.

| Order | Planned test name | Level | BDD | Required proof |
|---|---|---|---|---|
| 1 | TestReadImage_PaginationPresence | Unit | BDD-02 | Omitted values differ from explicit zero/default; no partial image. |
| 2 | TestReadImage_NormalizedDimensions | Unit | BDD-07 | Decode returned real image; independently compare dimensions/output limits. |
| 3 | TestReadImage_FormatErrors | Unit | BDD-08 | Distinct corrupt/unsupported outcomes, no visual payload. |
| 4 | TestReadImage_InputBudgets | Unit | BDD-09 | Independent counters prove M+1 maximum reads and dimension rejection before decode. |
| 5 | TestReadImage_ModelCapabilities | Unit | BDD-11 | Exact provider/model false versus unknown behavior; no automatic switch. |
| 6 | TestReadImage_ReadersReturnVisualContent | Integration | BDD-01 | Both registered readers produce a real decodable image. |
| 7 | TestReadImage_ExistingReadingContracts | Integration | BDD-03 | Real text/document/binary fixtures preserve baseline outputs. |
| 8 | TestReadImage_PrivateAudit | Integration | BDD-04 | Real scope + result routing; zero channel/bus sends and correct audit. |
| 9 | TestReadImage_AccessDenial | Integration | BDD-05 | Real filesystem/Judge/library enforcement blocks disclosure. |
| 10 | TestReadImage_AuthorizedSnapshot | Integration | BDD-06 | Controlled replacement after open and fresh-read access checks; no unrestricted reopen. |
| 11 | TestReadImage_ProviderRequestMatrix | Integration | BDD-10 | Capture adapter HTTP requests; decode image blocks and check tool correlation/order for each family. |
| 12 | TestReadImage_ProviderFailureAndCandidate | Integration | BDD-12 | Actual candidate budgets, non-vision fallback guidance, unknown/rejection/timeout/cancel outcomes. |
| 13 | TestReadImage_HistoryAndDeliveryRegression | Integration | BDD-15 | Inspect admission/archive/session persistence for zero inspection bytes or refs; replay marker, no new delivery; ordinary deliveries unchanged. |
| 14 | TestDocumentVisual_ValidationIncomplete | Integration | BDD-14 | Tool/runtime failure maps to incomplete validation, not success. |
| 15 | DocumentVisual_LiveDefectCorrection | E2E | BDD-13 | Live model reads actual pages, finds known defects, corrects and rereads; independent renderer confirms fixes. |

Provider request tests must reject fake image placeholders and mere presence of a tool-call ID. Capture the final serialized request from the real adapter; independently decode the image and validate provider-valid structure. Live evidence records provider/model, tool calls, before/after renders and observed defects. An image-generation success alone is not a pass.

### Test datasets

**Dataset A — Reader inputs and format behavior**

| # | Input | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| A1 | Known 32×24 PNG via both readers | Happy | Real image, matching dimensions | BDD-01 |
| A2 | Known 40×30 JPEG via both readers | Happy | Decodable image, matching dimensions | BDD-01 |
| A3 | Image with offset omitted, length omitted | Missing optional | Valid read | BDD-01 |
| A4 | Image with offset 0, 1 or −1 | Explicit numeric | Image pagination refusal, no image | BDD-02 |
| A5 | Image with length 0, 1 or text default | Explicit numeric | Image pagination refusal, no image | BDD-02 |
| A6 | Image with offset/length null or wrong type | Malformed | Existing argument-type validation error, no image | BDD-02 |
| A7 | PNG bytes under unexpected extension | Edge | Identified as image by content | BDD-01 |
| A8 | Unicode/spaces in permitted image name | Edge | Same successful image | BDD-01 |
| A9 | Empty/truncated bytes in claimed image file | Empty/corrupt | Invalid image | BDD-08 |
| A10 | Valid unsupported HEIC fixture | Unsupported | Unsupported image format | BDD-08 |
| A11 | Valid image needing resize | Output boundary | Within catalog limits, dimensions and resized notice | BDD-07 |
| A12 | Valid input impossible under tiny test model budget | Output limit | Image cannot fit model limits | BDD-07 |
| A13 | Direct SVG read, with text pagination | Regression | Existing SVG text result; no visual-inspection claim | BDD-03 |
| A14 | Tool SVG with missing viewport, fractional viewport, huge viewport, non-finite values | Vector boundary | Default/bounded positive raster canvas or invalid-value error; limited-render notice | BDD-07, BDD-08 |
| A15 | Existing SVG tool result with tiny actual-candidate budget | Vector output boundary | Post-raster resize within budget or explicit model-limit failure | BDD-07, BDD-12 |

**Dataset B — Size, access, and lifecycle**

Use valid metadata/chunk padding to create byte-boundary fixtures; do not pretend appending arbitrary garbage creates a valid image. Pixel fixtures with excessive advertised dimensions prove rejection without allocating those images.

The valid P boundary requires roughly 64 MiB for a 4096×4096 four-byte decoded image, before normalization copies. Run these full-decode boundaries sequentially in CI; use header-only over-limit fixtures for refusal tests. Do not lower the production pixel limit to make a local test cheap.

| # | Input | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| B1 | Valid 1×1 image | Minimum pixels | Allowed attempt | BDD-09 |
| B2 | Claimed width or height 0 | Invalid minimum | Invalid image | BDD-08 |
| B3 | Valid M−1 and M byte snapshots | Maximum bytes | Allowed normalization attempt | BDD-09 |
| B4 | M+1 bytes and growing stream | Over maximum | Byte-limit error; consume at most M+1 | BDD-09 |
| B5 | Valid P−1 and P pixel snapshots | Maximum area | Allowed normalization attempt | BDD-09 |
| B6 | Header declaring P+1 or overflowing dimensions | Over maximum | Pixel-limit or invalid-dimension refusal before full decode | BDD-09 |
| B7 | Allowed mount/library/task evidence | Allowed | Image plus correlated audit, no send | BDD-04 |
| B8 | Missing path, missing required path | Missing | Existing error, no image | BDD-05 |
| B9 | Forbidden mount, traversal, symlink escape | Denied | Existing access refusal, no image | BDD-05 |
| B10 | Judge requests image outside reviewed workspace, then another task image inside that workspace | Existing workspace boundary | Outside path refused; inside path allowed if otherwise authorized, with no invented per-task allowlist | BDD-05, BDD-04 |
| B11 | Metadata-protected path | Protected | Existing metadata guard refusal | BDD-05 |
| B12 | Swap source path after authorized open | Concurrent replacement | Original authorized bounded snapshot only | BDD-06 |
| B13 | Grant removed before later candidate or fresh read in a later turn | Current access | Existing access refusal; durable marker itself grants nothing | BDD-06, BDD-05 |
| B14 | Completed/interrupted turn saved through admission, archive, session and compaction | Durable boundary | Text marker only, zero image bytes/base64/snapshot refs; replay requires re-read | BDD-15 |
| B15 | Image-named FIFO/device and regular-file path replaced by FIFO before open | Non-regular/race | Refuse without blocking image acquisition, no bytes and no leaked reader worker | BDD-09 |
| B16 | Cancel between bounded reads of a regular image | Cancellation | Existing interrupted outcome, no partial image or durable bytes | BDD-09 |

**Dataset C — Provider and workflow**

| # | Input | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| C1 | All five native adapter families in the inventory, including Bedrock-tag CI; one tool image | Adapter matrix | Valid decodable image and correct correlation | BDD-10 |
| C2 | Two concurrent tool results, one image; mixed text+image | Ordering | Each result remains associated; image is tool-derived | BDD-10 |
| C3 | Exact known non-vision provider/model | Capability false | No unsupported image; guidance | BDD-11 |
| C4 | Unknown catalog provider/model | Capability unknown | Existing optimistic attempt, honest outcome | BDD-12 |
| C5 | Vision candidate replaced by configured non-vision fallback | Candidate change | Re-evaluate; guidance, no invalid image request | BDD-12 |
| C6 | Vision candidate replaced by lower-budget vision fallback | Candidate budget | Re-normalize to actual limits or report limit | BDD-12 |
| C7 | Image rejection, 503, timeout, cancellation | External failure | Existing bounded retry/cancel, no false inspection claim | BDD-12 |
| C8 | Clipped footer + overlapping chart label | Visual defects | Both identified, corrected, fresh rendering inspected | BDD-13 |
| C9 | Missing renderer, denied page read, unavailable inference | Dependency/error | Visual validation incomplete and specific reason | BDD-14 |
| C10 | Inspection plus explicit send_file/browser/MCP delivery | Purpose separation | Read sends zero; existing explicit delivery remains | BDD-15 |
| C11 | Codex/Copilot CLI transport with a vision-labelled or unknown model | Transport boundary | Explicit unsupported-image guidance; no image/base64 text passed as a workaround | BDD-11 |

### Regression test requirements

| Existing behavior | Existing test to retain | New protection |
|---|---|---|
| Text reading, missing path and required argument | TestFilesystemTool_ReadFile_Success; TestFilesystemTool_ReadFile_NotFound; TestFilesystemTool_ReadFile_MissingPath | TestReadImage_ExistingReadingContracts |
| Non-image binary refusal | TestReadFile_OpaqueBinaryStillRejected | Same test plus real image contrast |
| Existing user image translation / tool text output | TestTranslateMessages_UserWithMedia; TestTranslateMessages_ToolMessage | TestReadImage_ProviderRequestMatrix |
| Existing tool-image attachment | TestToolResultMedia_PlainPNG_StillAttached | Full request and private/delivery routing tests; helper alone is insufficient |
| Scheduled output routing workspace identity | TestProcessScheduled_MediaToolDelivery_StampsWorkspaceID | TestReadImage_HistoryAndDeliveryRegression |

**Regression dataset**

| # | Input | Previous behavior retained | Traces to |
|---|---|---|---|
| R1 | Empty UTF-8 text; Unicode text with offset/length | Existing text output/pagination | BDD-03 |
| R2 | Text length above 64 KiB reader cap | Existing capped read/header | BDD-03 |
| R3 | Valid DOCX, XLSX, PPTX and PDF | Extracted text, unchanged pagination | BDD-03 |
| R4 | Opaque binary containing NUL bytes | Existing binary refusal | BDD-03 |
| R5 | User image plus ordinary text-only tool result | Existing input behavior and tool correlation | BDD-10 |
| R6 | Ordinary scheduled send_file, browser screenshot and MCP image | Existing workspace routing and delivery, no replay resend | BDD-15 |

## Functional Requirements

- **FR-001:** Readers MUST return supported images as actual visual content using the same behavior for file and library reads.
- **FR-002:** Readers MUST distinguish omitted pagination from explicit parameters and reject image pagination without partial bytes.
- **FR-003:** Existing text, document extraction and opaque-binary behaviors MUST remain unchanged.
- **FR-004:** Image reads MUST retain filesystem/library authorization, metadata guards and correlated audit behavior. Judge files use the reviewed workspace boundary; this feature MUST NOT introduce per-task file scoping.
- **FR-005:** Inspection MUST use bounded authorized regular-file snapshots only within the live turn and its existing provider attempts. Later turns MUST make fresh authorized reads; durable markers MUST NOT restore image content.
- **FR-006:** Image processing MUST accept only regular-file image candidates, enforce M byte and P pixel limits before unbounded read/full decode, honor cancellation between bounded reads, and distinguish invalid/unsupported images. SVG tool media follows the explicit bounded-canvas rules above.
- **FR-007:** Normalization MUST use actual model output budgets and report original/presented dimensions and resizing.
- **FR-008:** Inspection-purpose results MUST NOT publish user files; existing delivery-purpose results MUST retain their behavior.
- **FR-009:** Every supported vision adapter MUST preserve provider-valid images, tool result identity and tool-derived provenance.
- **FR-010:** Capability handling MUST use the actual request candidate and transport, reject image-unsupported transports before catalog optimism, preserve known non-vision guidance and supported-transport unknown-model behavior, and add no automatic switching.
- **FR-011:** Provider failure/retry/cancellation MUST remain bounded and accurately reported without successful-inspection claims.
- **FR-012:** Every durable inspection-writing seam MUST store only the text marker and no image bytes/base64/snapshot references. Replay/compaction MUST retain the marker and re-read instruction without resending. Existing uploads and delivery payloads are unaffected.
- **FR-013:** Document validation MUST demonstrate detection, correction and renewed visual inspection; blocked validation MUST remain incomplete.

## Success Criteria

- **SC-001:** Both readers pass valid PNG/JPEG and all negative/boundary fixtures with zero unauthorized image disclosures.
- **SC-002:** Every inventoried supported adapter family passes final request capture with independently decoded real images and correct tool correlation.
- **SC-003:** Inspection produces zero outbound media-delivery events and zero durable image payloads or snapshot references across admission/archive/session/cache/checkpoint/compaction paths, including interrupted turns. Existing uploads and explicit deliveries retain baseline behavior.
- **SC-004:** Byte/pixel boundary tests prove at most M+1 source bytes consumed and no full decode above P.
- **SC-005:** Existing reader regression fixtures retain expected text/document/binary outcomes.
- **SC-006:** Live Mia and General Purpose workflows identify the two fixture defects, produce independently checked corrected renders and inspect the corrected pages. Any failed required step leaves validation incomplete.

## Traceability Matrix

| Requirement | User story | BDD scenarios | Planned tests |
|---|---|---|---|
| FR-001 | US-1 | BDD-01 | TestReadImage_ReadersReturnVisualContent |
| FR-002 | US-1 | BDD-02 | TestReadImage_PaginationPresence |
| FR-003 | US-1 | BDD-03 | TestReadImage_ExistingReadingContracts |
| FR-004 | US-2 | BDD-04, BDD-05 | TestReadImage_PrivateAudit; TestReadImage_AccessDenial |
| FR-005 | US-2 | BDD-06 | TestReadImage_AuthorizedSnapshot |
| FR-006 | US-3 | BDD-08, BDD-09 | TestReadImage_FormatErrors; TestReadImage_InputBudgets |
| FR-007 | US-3 | BDD-07 | TestReadImage_NormalizedDimensions |
| FR-008 | US-2, US-5 | BDD-04, BDD-15 | TestReadImage_PrivateAudit; TestReadImage_HistoryAndDeliveryRegression |
| FR-009 | US-4 | BDD-10 | TestReadImage_ProviderRequestMatrix |
| FR-010 | US-4 | BDD-11, BDD-12 | TestReadImage_ModelCapabilities; TestReadImage_ProviderFailureAndCandidate |
| FR-011 | US-4 | BDD-12 | TestReadImage_ProviderFailureAndCandidate |
| FR-012 | US-5 | BDD-15 | TestReadImage_HistoryAndDeliveryRegression |
| FR-013 | US-5 | BDD-13, BDD-14 | DocumentVisual_LiveDefectCorrection; TestDocumentVisual_ValidationIncomplete |

SC-001 maps to BDD-01/02/04/05/06/08/09; SC-002 to BDD-10/11/12; SC-003 to BDD-04/15; SC-004 to BDD-09; SC-005 to BDD-03; SC-006 to BDD-13/14.

## Prerequisites, development setup, and runtime

This is an extension of the existing pure-Go binary on Linux, macOS and Windows. Use repository-pinned Go dependencies and `CGO_ENABLED=0` with build tags `goolm,stdjson`; no new external image service or database. Existing media/history storage remains authoritative. Real document rendering requires the companion spec's installed skill dependencies. Live verification requires a configured supported vision model and its existing credentials/network access; it must not silently use the non-vision onboarding default.

Use the existing repository bootstrap and build instructions. No feature-specific bootstrap command or service is introduced. Fresh worktrees require the documented SPA build/stub before gateway compilation. CI owns broad Go verification; never run the full Go suite locally. At most one necessary narrow Go test is permitted locally, with the required tags and `-p 1`. Record executed checks separately from planned tests.

If wire contracts change, use `make gen-contracts` and `make verify-contracts` and preserve generated artifacts. Otherwise avoid unrelated contract edits. Startup/shutdown and cancellation use the existing application lifecycle. Audit outcome, model/provider and non-sensitive source identity; do not log image base64 or connector secrets. Runtime failure must be observable in the tool/workflow outcome, not only a debug log.

## Ambiguity Warnings and Assumptions

No unresolved product questions. Engineering choices remain bounded by the ADR: internal purpose field location and the reusable normalization helper may follow existing code organization; neither changes the observable contract. Provider-specific request representations must be validated against actual supported adapters during implementation. A failure to provide provider-valid images is an implementation defect, not an accepted silent limitation.

The founder-approved model behavior is guidance for known non-vision models, with no new automatic switching. Existing fallback machinery is preserved and re-evaluates each actual candidate. The global upfront set stays unchanged because both readers are already listed.

## Evaluation Scenarios (Holdout)

These are **post-implementation external evaluation briefs**, excluded from the TDD plan and traceability matrix. A separate evaluator creates fresh hidden fixtures and expected defect locations after development; do not hand those concrete fixtures/oracles to the implementer. Public examples here are not themselves secret holdouts.

| ID | Category | External setup and action | Expected outcome |
|---|---|---|---|
| H1 | Happy Path | Ask Mia to repair a freshly generated presentation with two evaluator-chosen layout defects | Finds/corrects both, checks new pages, returns presentation without publishing intermediate screenshots |
| H2 | Happy Path | Ask GP to inspect a newly supplied library JPEG containing a chart | Accurately describes evaluator-known visual relationships |
| H3 | Happy Path | Repeat a fresh rendered-page inspection using a second supported provider family | Actual visual evidence reaches model; equivalent observed result |
| H4 | Error | Remove access to a page before the requested inspection | Clear access limitation; no disclosure or completion claim |
| H5 | Error | Select a known non-vision model for fresh document validation | Guidance, retained file access, no automatic model switch or visual-success claim |
| H6 | Edge Case | Inspect a fresh large page where resizing obscures a small evaluator-chosen defect | Resize acknowledged; a permitted crop is read if needed; no unsupported fine-detail claim |
| H7 | Edge Case | Restart/replay after inspecting a fresh image | Text marker says re-read; no retained inspection image or duplicate delivery; any re-read respects current access |

## Clarifications

2026-09-17: visual inspection belongs in the existing readers; generic image-return plumbing alone is insufficient. Inspection is private, provider adapters must carry real images, and unsupported models use existing guidance. The Opus review corrected the Judge boundary to its existing reviewed workspace, simplified inspection retention to live-turn memory plus durable text markers, added the optional Bedrock adapter and explicit CLI transport limitations, and limited image acquisition to regular files. These corrections add no permissions, per-task file scope, revocation registry, persistent inspection archive or separate viewing tool.
