package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/media"
)

// MCPManager defines the interface for MCP manager operations
// This allows for easier testing with mock implementations
type MCPManager interface {
	CallTool(
		ctx context.Context,
		serverName, toolName string,
		arguments map[string]any,
	) (*mcp.CallToolResult, error)
}

// MCPTool wraps an MCP tool to implement the Tool interface
type MCPTool struct {
	BaseTool
	manager    MCPManager
	serverName string
	tool       *mcp.Tool
	mediaStore media.MediaStore
}

var _ Tool = (*MCPTool)(nil)

// --- ADR-092 D9 auto-approve stand-in types ---------------------------
//
// docs/internal/specs/adr-092-auto-for-other-tools-design.md §5.1 puts
// AutoVerdict, PinnedPath and AutoApproveClassifier in a new
// pkg/tools/auto_approve.go, owned by lane L1 (origin/auto/l1-classifier).
// That lane had not pushed when MCPTool and browser.ScreenshotTool (lane
// L2) needed these exact shapes to implement AutoApproveClassifier, so they
// are defined here for now, matching §5.1 field-for-field. Once
// origin/auto/l1-classifier merges and defines the real types in
// auto_approve.go, this block becomes a duplicate declaration and must be
// deleted — the merge conflict is the signal.
type AutoVerdict struct {
	Run    bool
	Class  string // "runs" | "runs_if_args" | "mcp_not_destructive" (for audit)
	Reason string
	Paths  []PinnedPath // RUNS-IF file tools only
}

// PinnedPath is the resolved, real (symlink-free) filesystem path an
// AutoRunsIfArgs or MCP classifier verified against the §2 J2 workspace
// rule, plus the access it was verified for.
type PinnedPath struct {
	Real   string
	Access uint64
}

// AutoAccessWrite is the Access bit a RUNS-IF file classifier sets on a
// PinnedPath it resolved for a write. Stand-in pending L1's real encoding.
const AutoAccessWrite uint64 = 1 << 0

// AutoApproveClassifier is implemented by every AutoRunsIfArgs tool and by
// MCPTool (§5.1). AutoApproveVerdict must not itself refuse a call — an
// error or an unresolvable argument is reported as Run: false so the caller
// falls back to the ordinary ask flow, never as a hard failure.
type AutoApproveClassifier interface {
	AutoApproveVerdict(ctx context.Context, args map[string]any) AutoVerdict
}

// NewMCPTool creates a new MCP tool wrapper
func NewMCPTool(manager MCPManager, serverName string, tool *mcp.Tool) *MCPTool {
	return &MCPTool{
		manager:    manager,
		serverName: serverName,
		tool:       tool,
	}
}

func (t *MCPTool) SetMediaStore(store media.MediaStore) {
	t.mediaStore = store
}

// sanitizeIdentifierComponent normalizes a string so it can be safely used
// as part of a tool/function identifier for downstream providers.
// It:
//   - lowercases the string
//   - replaces any character not in [a-z0-9_-] with '_'
//   - collapses multiple consecutive '_' into a single '_'
//   - trims leading/trailing '_'
//   - falls back to "unnamed" if the result is empty
//   - truncates overly long components to a reasonable length
func sanitizeIdentifierComponent(s string) string {
	const maxLen = 64

	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))

	prevUnderscore := false
	for _, r := range s {
		isAllowed := (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-'

		if !isAllowed {
			// Normalize any disallowed character to '_'
			if !prevUnderscore {
				b.WriteRune('_')
				prevUnderscore = true
			}
			continue
		}

		if r == '_' {
			if prevUnderscore {
				continue
			}
			prevUnderscore = true
		} else {
			prevUnderscore = false
		}

		b.WriteRune(r)
	}

	result := strings.Trim(b.String(), "_")
	if result == "" {
		result = "unnamed"
	}

	if len(result) > maxLen {
		result = result[:maxLen]
	}

	return result
}

// Name returns the tool name, prefixed with the server name.
// The total length is capped at 64 characters (OpenAI-compatible API limit).
// A short hash of the original (unsanitized) server and tool names is appended
// whenever sanitization is lossy or the name is truncated, ensuring that two
// names which differ only in disallowed characters remain distinct after sanitization.
func (t *MCPTool) Name() string {
	// Prefix with server name to avoid conflicts, and sanitize components
	sanitizedServer := sanitizeIdentifierComponent(t.serverName)
	sanitizedTool := sanitizeIdentifierComponent(t.tool.Name)
	full := fmt.Sprintf("mcp_%s_%s", sanitizedServer, sanitizedTool)

	// Check if sanitization was lossless (only lowercasing, no char replacement/truncation)
	lossless := strings.ToLower(t.serverName) == sanitizedServer &&
		strings.ToLower(t.tool.Name) == sanitizedTool

	const maxTotal = 64
	if lossless && len(full) <= maxTotal {
		return full
	}

	// Sanitization was lossy or name too long: append hash of the ORIGINAL names
	// (not the sanitized names) so different originals always yield different hashes.
	h := fnv.New32a()
	_, _ = h.Write([]byte(t.serverName + "\x00" + t.tool.Name))
	suffix := fmt.Sprintf("%08x", h.Sum32()) // 8 chars

	base := full
	if len(base) > maxTotal-9 {
		base = strings.TrimRight(full[:maxTotal-9], "_")
	}
	return base + "_" + suffix
}

// Description returns the tool description
func (t *MCPTool) Description() string {
	desc := t.tool.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool from %s server", t.serverName)
	}
	// Add server info to description
	return fmt.Sprintf("[MCP:%s] %s", t.serverName, desc)
}

// Scope returns ScopeGeneral — MCP tools are treated as general-purpose tools
// and are available to all agent types (subject to per-agent MCP server binding config).
func (t *MCPTool) Scope() ToolScope { return ScopeGeneral }

func (t *MCPTool) Category() ToolCategory { return CategoryMCP }

func (t *MCPTool) MCPSource() (string, string) { return t.serverName, t.tool.Name }

var _ AutoApproveClassifier = (*MCPTool)(nil)

// AutoApproveVerdict implements ADR-092 D9 §4: under Auto, an MCP tool on
// Ask runs only when its server marked it read-only (ReadOnlyHint: true) or
// explicitly not destructive (DestructiveHint: false). Every other shape —
// no annotations at all, annotations present with DestructiveHint nil and
// ReadOnlyHint false, or DestructiveHint true — asks. This follows the MCP
// specification's own default: DestructiveHint defaults to true when it is
// absent, and the hint is meaningful only when ReadOnlyHint is false, so an
// unlabelled tool is treated as destructive and must ask (founder decision
// 2026-09-23, "following the MCP specification").
//
// args is intentionally unused: the verdict depends only on the server's
// own tool annotations (t.tool.Annotations), never on this call's
// arguments — an MCP tool has no J2 workspace-path condition of its own.
func (t *MCPTool) AutoApproveVerdict(_ context.Context, _ map[string]any) AutoVerdict {
	ann := t.tool.Annotations
	if ann == nil {
		return AutoVerdict{
			Run:    false,
			Class:  "asks",
			Reason: fmt.Sprintf("mcp tool %s has no annotations; the MCP specification treats an unlabelled tool as destructive", t.Name()),
		}
	}
	if ann.ReadOnlyHint {
		return AutoVerdict{
			Run:    true,
			Class:  "mcp_not_destructive",
			Reason: fmt.Sprintf("server %s marked %s read-only (readOnlyHint=true)", t.serverName, t.tool.Name),
		}
	}
	if ann.DestructiveHint != nil && !*ann.DestructiveHint {
		return AutoVerdict{
			Run:    true,
			Class:  "mcp_not_destructive",
			Reason: fmt.Sprintf("server %s marked %s explicitly not destructive (destructiveHint=false)", t.serverName, t.tool.Name),
		}
	}
	return AutoVerdict{
		Run:    false,
		Class:  "asks",
		Reason: fmt.Sprintf("mcp tool %s is not marked read-only and is not explicitly non-destructive", t.Name()),
	}
}

// Parameters returns the tool parameters schema
func (t *MCPTool) Parameters() map[string]any {
	// The InputSchema is already a JSON Schema object
	schema := t.tool.InputSchema

	// Handle nil schema
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		}
	}

	// Try direct conversion first (fast path)
	if schemaMap, ok := schema.(map[string]any); ok {
		return schemaMap
	}

	// Handle json.RawMessage and []byte - unmarshal directly
	var jsonData []byte
	if rawMsg, ok := schema.(json.RawMessage); ok {
		jsonData = rawMsg
	} else if bytes, ok := schema.([]byte); ok {
		jsonData = bytes
	}

	if jsonData != nil {
		var result map[string]any
		if err := json.Unmarshal(jsonData, &result); err == nil {
			return result
		}
		// Fallback on error
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		}
	}

	// For other types (structs, etc.), convert via JSON marshal/unmarshal
	var err error
	jsonData, err = json.Marshal(schema)
	if err != nil {
		// Fallback to empty schema if marshaling fails
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		}
	}

	var result map[string]any
	if err := json.Unmarshal(jsonData, &result); err != nil {
		// Fallback to empty schema if unmarshaling fails
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		}
	}

	return result
}

// Execute executes the MCP tool
func (t *MCPTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	result, err := t.manager.CallTool(ctx, t.serverName, t.tool.Name, args)
	if err != nil {
		return ErrorResult(fmt.Sprintf("MCP tool execution failed: %v", err)).WithError(err)
	}

	if result == nil {
		nilErr := fmt.Errorf("MCP tool returned nil result without error")
		return ErrorResult("MCP tool execution failed: nil result").WithError(nilErr)
	}

	// Handle error result from server
	if result.IsError {
		errMsg := extractContentText(result.Content)
		return ErrorResult(fmt.Sprintf("MCP tool returned error: %s", errMsg)).
			WithError(fmt.Errorf("MCP tool error: %s", errMsg))
	}

	return t.normalizeResultContent(ctx, result.Content)
}

// extractContentText extracts text from MCP content array
func extractContentText(content []mcp.Content) string {
	var parts []string
	for _, c := range content {
		switch v := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, sanitizeToolLLMContent(v.Text))
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[Image: %s]", normalizedMIMEType(v.MIMEType)))
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[Audio: %s]", normalizedMIMEType(v.MIMEType)))
		case *mcp.ResourceLink:
			parts = append(parts, summarizeResourceLink(v))
		case *mcp.EmbeddedResource:
			parts = append(parts, summarizeEmbeddedResource(v))
		default:
			// For other content types, use string representation
			parts = append(parts, fmt.Sprintf("[Content: %T]", v))
		}
	}
	return sanitizeToolLLMContent(strings.Join(parts, "\n"))
}

func (t *MCPTool) normalizeResultContent(ctx context.Context, content []mcp.Content) *ToolResult {
	llmParts := make([]string, 0, len(content))
	mediaRefs := make([]string, 0, len(content))

	for _, c := range content {
		switch v := c.(type) {
		case *mcp.TextContent:
			text := strings.TrimSpace(sanitizeToolLLMContent(v.Text))
			if text != "" {
				llmParts = append(llmParts, text)
			}
		case *mcp.ImageContent:
			ref, note := t.storeBinaryContent(
				ctx,
				"image",
				normalizedMIMEType(v.MIMEType),
				v.Data,
				v.Annotations,
			)
			if ref != "" {
				mediaRefs = append(mediaRefs, ref)
			}
			if note != "" {
				llmParts = append(llmParts, note)
			}
		case *mcp.AudioContent:
			ref, note := t.storeBinaryContent(
				ctx,
				"audio",
				normalizedMIMEType(v.MIMEType),
				v.Data,
				v.Annotations,
			)
			if ref != "" {
				mediaRefs = append(mediaRefs, ref)
			}
			if note != "" {
				llmParts = append(llmParts, note)
			}
		case *mcp.ResourceLink:
			llmParts = append(llmParts, summarizeResourceLink(v))
		case *mcp.EmbeddedResource:
			ref, note := t.storeEmbeddedResource(ctx, v)
			if ref != "" {
				mediaRefs = append(mediaRefs, ref)
			}
			if note != "" {
				llmParts = append(llmParts, note)
			}
		default:
			llmParts = append(llmParts, fmt.Sprintf("[MCP returned unsupported content type %T]", v))
		}
	}

	result := &ToolResult{
		ForLLM: strings.Join(compactStrings(llmParts), "\n"),
		Media:  mediaRefs,
	}
	return result
}

func (t *MCPTool) storeEmbeddedResource(ctx context.Context, content *mcp.EmbeddedResource) (string, string) {
	if content == nil || content.Resource == nil {
		return "", "[MCP returned an embedded resource without data.]"
	}

	resource := content.Resource
	if len(resource.Blob) > 0 {
		return t.storeBinaryContent(
			ctx,
			"resource",
			normalizedMIMEType(resource.MIMEType),
			resource.Blob,
			content.Annotations,
		)
	}

	if strings.TrimSpace(resource.Text) != "" {
		return "", sanitizeToolLLMContent(resource.Text)
	}

	return "", summarizeEmbeddedResource(content)
}

func (t *MCPTool) storeBinaryContent(
	ctx context.Context,
	kind string,
	mimeType string,
	data []byte,
	annotations *mcp.Annotations,
) (string, string) {
	if len(data) == 0 {
		return "", fmt.Sprintf("[MCP returned %s content (%s) but it was empty.]", kind, mimeType)
	}
	if !annotationsAllowUser(annotations) {
		return "", fmt.Sprintf(
			"[MCP returned %s content (%s) for non-user audience; omitted from model context.]",
			kind,
			mimeType,
		)
	}
	if t.mediaStore == nil {
		return "", fmt.Sprintf(
			"[MCP returned %s content (%s); omitted from model context because media delivery is unavailable.]",
			kind,
			mimeType,
		)
	}

	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	if channel == "" || chatID == "" {
		return "", fmt.Sprintf(
			"[MCP returned %s content (%s); omitted from model context because no target chat was available.]",
			kind,
			mimeType,
		)
	}

	dir := media.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Sprintf("[MCP returned %s content (%s) but it could not be stored.]", kind, mimeType)
	}

	ext := extensionForMIMEType(mimeType)
	tmpFile, err := os.CreateTemp(dir, "mcp-*"+ext)
	if err != nil {
		return "", fmt.Sprintf("[MCP returned %s content (%s) but it could not be stored.]", kind, mimeType)
	}
	tmpPath := tmpFile.Name()
	if _, err = tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[MCP returned %s content (%s) but it could not be stored.]", kind, mimeType)
	}
	if err = tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[MCP returned %s content (%s) but it could not be stored.]", kind, mimeType)
	}

	scope := fmt.Sprintf(
		"tool:mcp:%s:%s:%s:%d",
		sanitizeIdentifierComponent(t.serverName),
		channel,
		chatID,
		time.Now().UnixNano(),
	)
	filename := fmt.Sprintf(
		"%s_%s%s",
		sanitizeIdentifierComponent(t.serverName),
		sanitizeIdentifierComponent(t.tool.Name),
		ext,
	)

	ref, err := t.mediaStore.Store(tmpPath, media.MediaMeta{
		Filename:    filename,
		ContentType: mimeType,
		Source: fmt.Sprintf(
			"tool:mcp:%s:%s",
			sanitizeIdentifierComponent(t.serverName),
			sanitizeIdentifierComponent(t.tool.Name),
		),
	}, scope)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf(
			"[MCP returned %s content (%s) but it could not be registered as media.]",
			kind,
			mimeType,
		)
	}

	return ref, fmt.Sprintf(
		"[MCP returned %s content (%s); omitted from model context and stored as a local media artifact.]",
		kind,
		mimeType,
	)
}

func summarizeResourceLink(content *mcp.ResourceLink) string {
	if content == nil {
		return "[MCP returned an empty resource link.]"
	}

	parts := []string{"[MCP returned resource link"}
	if content.Name != "" {
		parts = append(parts, fmt.Sprintf("name=%q", content.Name))
	}
	if content.URI != "" {
		parts = append(parts, fmt.Sprintf("uri=%q", content.URI))
	}
	if content.MIMEType != "" {
		parts = append(parts, fmt.Sprintf("mime=%q", content.MIMEType))
	}
	if content.Description != "" {
		desc := strings.TrimSpace(content.Description)
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}
		parts = append(parts, fmt.Sprintf("description=%q", desc))
	}
	return strings.Join(parts, ", ") + "]"
}

func summarizeEmbeddedResource(content *mcp.EmbeddedResource) string {
	if content == nil || content.Resource == nil {
		return "[MCP returned an embedded resource.]"
	}

	resource := content.Resource
	if resource.URI != "" {
		return fmt.Sprintf(
			"[MCP returned embedded resource %q (%s).]",
			resource.URI,
			normalizedMIMEType(resource.MIMEType),
		)
	}
	return fmt.Sprintf("[MCP returned embedded resource (%s).]", normalizedMIMEType(resource.MIMEType))
}

func annotationsAllowUser(annotations *mcp.Annotations) bool {
	if annotations == nil || len(annotations.Audience) == 0 {
		return true
	}
	for _, audience := range annotations.Audience {
		if strings.EqualFold(string(audience), "user") {
			return true
		}
	}
	return false
}

func normalizedMIMEType(mimeType string) string {
	if strings.TrimSpace(mimeType) == "" {
		return "application/octet-stream"
	}
	return mimeType
}

func compactStrings(parts []string) []string {
	compact := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		compact = append(compact, part)
	}
	return compact
}
