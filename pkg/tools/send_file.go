package tools

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/h2non/filetype"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// SendFileTool allows the LLM to send a local file (image, document, etc.)
// to the user on the current chat channel via the MediaStore pipeline.
type SendFileTool struct {
	BaseTool
	// agentHome is the agent's fixed home directory (the pre-ADR-046-rename
	// "workspace" constructor parameter) — fspolicy.EffectiveFSPolicy's
	// agentHome argument. When the turn carries a TurnWorkspaceDir,
	// EffectiveFSPolicy prefers that re-rooted directory instead.
	agentHome   string
	restrict    bool
	maxFileSize int
	mediaStore  media.MediaStore
	allowPaths  []*regexp.Regexp

	defaultChannel string
	defaultChatID  string
}

func NewSendFileTool(
	agentHome string,
	restrict bool,
	maxFileSize int,
	store media.MediaStore,
	allowPaths ...[]*regexp.Regexp,
) *SendFileTool {
	if maxFileSize <= 0 {
		maxFileSize = config.DefaultMaxMediaSize
	}
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &SendFileTool{
		agentHome:   agentHome,
		restrict:    restrict,
		maxFileSize: maxFileSize,
		mediaStore:  store,
		allowPaths:  patterns,
	}
}

func (t *SendFileTool) Name() string { return "send_file" }
func (t *SendFileTool) Description() string {
	return "Send a local file (image, document, etc.) to the user on the current chat channel."
}
func (t *SendFileTool) Scope() ToolScope       { return ScopeCore }
func (t *SendFileTool) Category() ToolCategory { return CategoryCommunication }

func (t *SendFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the local file. Relative paths are resolved from workspace.",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Optional display filename. Defaults to the basename of path.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *SendFileTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
}

func (t *SendFileTool) SetMediaStore(store media.MediaStore) {
	t.mediaStore = store
}

func (t *SendFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return ErrorResult("path is required")
	}

	// Prefer context-injected channel/chatID (set by ExecuteWithContext), fall back to SetContext values.
	channel := ToolChannel(ctx)
	if channel == "" {
		channel = t.defaultChannel
	}
	chatID := ToolChatID(ctx)
	if chatID == "" {
		chatID = t.defaultChatID
	}
	if channel == "" || chatID == "" {
		return ErrorResult("no target channel/chat available")
	}

	if t.mediaStore == nil {
		return ErrorResult("media store not configured")
	}

	// Resolve the single, authoritative filesystem policy for this turn
	// (FR-036), mirroring the generic file tools (ReadFileTool et al.).
	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to resolve filesystem policy: %v", err))
	}

	// FR-2.3a: send_file uses FSOpSend, not FSOpRead — a disclosure to a chat
	// channel is distinguishable from an ordinary read in audit, though it
	// carries no additional path restriction (FR-2.3: the operator rejected
	// a path-based "publish" gate here as bypassable and misleading; tool
	// policy is the real gate).
	handle, err := ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpSend, path, t.allowPaths)
	if err != nil {
		return PermissionDeniedResult(t.Name(), err, err.Error())
	}
	defer handle.Close()

	info, err := handle.Stat()
	if err != nil {
		return ErrorResult(fmt.Sprintf("file not found: %v", err))
	}
	if info.IsDir() {
		return ErrorResult("path is a directory, expected a file")
	}
	if info.Size() > int64(t.maxFileSize) {
		return ErrorResult(fmt.Sprintf(
			"file too large: %d bytes (max %d bytes)",
			info.Size(), t.maxFileSize,
		))
	}

	// RealPath is the ONE documented exception to "never hand back a bare
	// string" (PathHandle.RealPath's doc comment) — used here solely because
	// media.MediaStore.Store/RefByPath are OS-boundary call sites with no
	// PathHandle parameter of their own, exactly like exec.Cmd.Dir.
	resolved, err := handle.RealPath()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to resolve file path: %v", err))
	}

	filename, _ := args["filename"].(string)
	if filename == "" {
		filename = filepath.Base(resolved)
	}

	mediaType := detectMediaType(resolved)
	scope := fmt.Sprintf("tool:send_file:%s:%s", channel, chatID)

	// If this exact path is already registered (e.g. browser.screenshot
	// stored an inline copy and the LLM is now telling us to deliver
	// that same file), reuse the existing ref instead of minting a
	// second one. Without this, the SPA receives two media frames for
	// one image and the user sees the screenshot twice.
	if existingRef, ok := t.mediaStore.RefByPath(resolved); ok {
		return MediaResult(fmt.Sprintf("File %q sent to user", filename), []string{existingRef})
	}

	ref, err := t.mediaStore.Store(resolved, media.MediaMeta{
		Filename:      filename,
		ContentType:   mediaType,
		Source:        "tool:send_file",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, scope)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to register media: %v", err))
	}

	return MediaResult(fmt.Sprintf("File %q sent to user", filename), []string{ref})
}

// detectMediaType determines the MIME type of a file.
// Uses magic-bytes detection (h2non/filetype) first, then falls back to
// extension-based lookup via mime.TypeByExtension.
func detectMediaType(path string) string {
	kind, err := filetype.MatchFile(path)
	if err == nil && kind != filetype.Unknown {
		return kind.MIME.Value
	}

	if ext := filepath.Ext(path); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}

	return "application/octet-stream"
}
