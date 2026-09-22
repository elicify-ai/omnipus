# Visual inspection verification

Date: 2026-09-17. Code baseline: `a0b36050b`. Scope: source verification of the existing image path and current locally revised agents-and-skills requirements. No runtime implementation changes, tests, or live model demonstration were performed.

## Conclusion

The gap is real, but it is not a complete absence of image-return infrastructure. Existing tools can produce image media, and the agent loop attaches that media to tool messages. Permissions, provider conversion, and a convenient private inspection workflow remain gaps. Python execution can generate document pages; it does not by itself establish that the model can see them.

## Evidence

| Area | Verified finding | Source |
| --- | --- | --- |
| File readers | Supported Office/PDF files become text; the readers do not return image blocks for local page images. | `pkg/tools/filesystem.go`, `ReadFileTool`; `pkg/tools/library_tool.go` |
| Existing direct image return | `send_file` stores a local file and returns media references. It requires a channel/chat and also delivers the file to the user. | `pkg/tools/send_file.go`; `pkg/agent/loop_run_turn_tools.go` |
| Existing browser route | A rendered page image can be hosted using `serve_web`, opened with `browser_navigate`, and captured with `browser_screenshot`. Screenshot output contains an inline JPEG data URL for normalization. Direct `file://` navigation is blocked; its error explicitly recommends the preview route. | `pkg/tools/web_serve.go:332`; `pkg/tools/browser/tools.go:736`; `pkg/tools/browser/manager.go:969` |
| Deployment conditions | Browser route requires enabled preview serving, reachable gateway origin, managed browser availability/control, and accessible files. It is not just a permission toggle. | `pkg/tools/web_serve.go`; `pkg/gateway/rest_preview.go`; `pkg/agent/loop_wire.go` |
| Current Mia | Deny-all seed with no browser or `serve_web` override prevents the browser route. | `pkg/coreagent/seed.go`, `miaSeedPolicies` |
| Draft permission mismatch | Revised requirements give Mia browser access but deny `serve_web`; General Purpose gets `serve_web` but no browser. Neither can independently complete this route under those defaults. | `built-in-agents-and-skills-2026-09.md`, role permissions table |
| Internal image attachment | Normalization extracts inline images into media references; the loop resolves supported image references and passes image data into the admitted tool message. Tool media is also routed to user delivery. | `pkg/tools/normalization.go`; `pkg/agent/loop_media.go:609`; `pkg/agent/loop_run_turn_tools.go:1528` |
| Responses adapter | `TranslateMessages` serializes tool results using only `msg.Content`, omitting `msg.Media`. User images have a separate multipart branch. Codex and Azure call this translator. | `pkg/providers/openai_responses_common/responses_common.go:80`; `pkg/providers/codex_provider.go:224`; `pkg/providers/azure/provider.go:99` |
| Anthropic adapters | Both inspected adapters construct text-only tool-result blocks and omit tool-message media. | `pkg/providers/anthropic_messages/provider.go:282`; `pkg/providers/anthropic/provider.go:473` |
| OpenAI-compatible adapter | Shared serialization emits image parts for media-bearing messages, including tool messages. This establishes serialization, not acceptance or successful visual inference by every compatible endpoint. | `pkg/providers/common/common.go`, `SerializeMessages`; `pkg/providers/openai_compat/provider.go` |

## Recommended requirement

**Decision update:** the founder has selected image support in `read_file` and `library_read`; the main requirements §6.6 are authoritative. The earlier alternatives below explain the investigation and are no longer open implementation choices. See the companion decisions record.

### Follow-up: existing generic image handling and non-vision models

Further source inspection confirms that image support is not limited to named screenshot/file tools. `pkg/tools/mcp_tool.go`, `normalizeResultContent`, accepts MCP image content and stores its bytes as media. `pkg/tools/normalization.go`, `normalizeToolResult`, also extracts inline image data URLs from generic tool results when media-store and channel context are available. These feed the existing image attachment path. A dedicated viewer is therefore an optional convenience, not a prerequisite for adding image-return capability.

The existing attachment presentation path checks the exact provider/model's image capability through `modelSupportsImage` (`pkg/agent/media_present.go`). In `resolveMediaRefsWithOffload` (`pkg/agent/loop_media.go`), supported images are encoded/resized for model input; unsupported images are preserved in the workspace and accompanied by explicit guidance to switch to a vision-capable model. SVG markup can additionally be supplied as text. This preserves access to the file but does not automatically perform visual inspection using another model.

Runtime provider rejection is also handled: `synthesizeImageRejection` (`pkg/agent/loop_run_turn.go`) returns a clear cannot-view-images response for image-only rejections. Mixed-media failures can follow the bounded downgrade/retry path in `pkg/agent/media_downgrade.go`. `FallbackChain.ExecuteImage` exists, but a search of non-test Go source found no caller; its existence alone is not evidence of automatic vision delegation.

One integration distinction remains: `attachToolResultMedia` encodes tool images directly into data URLs without the model capability gate; `resolveMediaRefsWithOffload` passes pre-encoded URLs through unchanged. Consequently the attachment capability/offload handling is not uniformly applied to tool-generated images. This is separate from the provider-adapter omissions identified above. The requirement should reuse and connect these existing paths, rather than assume the fallback or image infrastructure is absent.

Mia and General Purpose must be able to inspect rendered local document pages as actual model image input before claiming visual validation. Reuse the existing media infrastructure. A local-image inspection tool would provide a simpler private workflow than publishing intermediate pages or starting a browser preview; alternatively, explicitly support the complete browser permission and deployment combination. This choice remains a recommendation, not a confirmed implementation decision.

Provider conversion must preserve tool-produced images in a format accepted by each supported vision-capable provider. Verification should include a real rendered page with known visual defects, evidence that its image reaches the provider request, and a live check that the model identifies the defects. A browser screenshot alone, text extraction, or successful file generation is insufficient proof. If the selected model cannot inspect images, the agent must report that visual validation was not completed.

Tool visibility remains governed by the agreed global list and tool search; this finding does not introduce per-agent visibility settings or worktree isolation work.
