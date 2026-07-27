package media

import (
	"errors"
	"fmt"
	"strings"
)

// WorkspaceRefPrefix is the discriminator for workspace-library media refs
// (FR-028). Refs of the form "media://workspace/<ws>/<id>" resolve through
// the owning workspace's media library; every other "media://<uuid>" ref
// resolves through the legacy global registry.
const WorkspaceRefPrefix = "media://workspace/"

// ResolveOpts carries the nilable caller-workspace context for the
// workspace-aware resolver methods (ResolveWithOpts / ResolveWithMetaOpts,
// FR-028/FR-028a).
//
// A nil (or empty) CallerWorkspace is the LEGACY call-site posture: the
// cross-workspace membership guard is bypassed and the ref resolves via the
// global registry exactly as before (FR-029 sunset v0.1.2). Only refs that
// carry the workspace prefix ("media://workspace/...") trigger the guard;
// for those, a non-nil CallerWorkspace whose value equals the ref's
// workspace is REQUIRED (FR-028a STRIDE Spoofing guard).
type ResolveOpts struct {
	// CallerWorkspace is the workspace the resolving caller belongs to. It
	// is nil for legacy media://<uuid> call-sites and for callers that have
	// no workspace context (legacy session replay, channel inbound delivery
	// of session-scoped media). It MUST be non-nil and match the ref's
	// workspace for media://workspace/<ws>/<id> refs.
	CallerWorkspace *string
}

// WithCallerWorkspace returns ResolveOpts whose CallerWorkspace is set to ws.
// It is the ergonomic constructor for the workspace-aware call-sites.
func WithCallerWorkspace(ws string) ResolveOpts {
	return ResolveOpts{CallerWorkspace: &ws}
}

// ErrWorkspaceContextRequired is returned when a workspace-prefixed ref is
// resolved without a caller-workspace context (FR-028a). Legacy
// media://<uuid> refs never produce this error — they bypass the guard.
var ErrWorkspaceContextRequired = errors.New("media store: caller workspace context required for workspace media ref")

// ErrCrossWorkspaceRef is returned when a caller in one workspace resolves a
// media ref owned by a different workspace (FR-028a STRIDE Spoofing guard).
// Cross-workspace media sharing is explicitly out of scope (FR-032).
var ErrCrossWorkspaceRef = errors.New("media store: caller workspace does not own media ref")

// ErrInvalidWorkspaceRef is returned when a ref carries the workspace prefix
// but is structurally malformed (not "media://workspace/<ws>/<id>").
var ErrInvalidWorkspaceRef = errors.New("media store: malformed workspace media ref")

// ErrNotFound is returned by the resolver methods (Resolve/ResolveWithMeta/
// ResolveWithOpts/ResolveWithMetaOpts) when a ref is well-formed — and, for
// workspace refs, has already passed the FR-028a caller-workspace guard —
// but genuinely does not resolve to any known entry: a legacy ref absent
// from the global registry, or a workspace ref for which no library
// provider is wired at all, or a wired provider that reports no resolver
// for the given workspace. This is the ROUTINE absent-ref case and callers
// map it to HTTP 404.
//
// It is deliberately distinct from a resolution-path FAILURE — e.g. a wired
// provider returning a non-nil error because the owning workspace's
// library could not be opened (disk error, corrupt manifest, permission
// denied). That failure means "resolution could not be completed", not
// "this media never existed", and must NOT be wrapped in ErrNotFound: doing
// so would let a real backend fault masquerade as a routine 404, exactly
// the confusion this sentinel exists to prevent. See
// FileMediaStore.resolveWorkspaceRef for where the two are told apart.
var ErrNotFound = errors.New("media store: unknown ref")

// WorkspaceLibraryResolver is the path-level read contract the media store
// needs from a workspace media library (pkg/media/library.Library implements
// it). It resolves a workspace-prefixed ref to its on-disk path plus
// transport metadata AFTER the cross-workspace membership guard has passed.
//
// The contract intentionally returns a path (not decoded bytes): the
// consumers that reach the media store's resolver (channels delivering
// outbound attachments, session replay, the agent loop's tool-result
// tagging) are transport layers, not decoders. The sha256-on-read integrity
// invariant (FR-002) is upheld by the library's byte-returning Read path,
// which the presentation orchestrator uses for decode-bound consumption.
type WorkspaceLibraryResolver interface {
	// ResolvePathWithCaller resolves ref under the caller-workspace
	// membership guard (callerWorkspaceID must be non-nil and equal to the
	// ref's workspace) and returns the on-disk path + transport metadata.
	ResolvePathWithCaller(ref string, callerWorkspaceID *string) (localPath string, meta MediaMeta, err error)
}

// WorkspaceLibraryProvider returns the path-level resolver for a given
// workspace. It is the seam through which the gateway injects the
// workspace-library cache; the media package never names the concrete
// library type (avoiding an import cycle). A nil/empty workspaceID MUST NOT
// be requested — the guard authorizes the ref's workspace before the
// provider is consulted.
type WorkspaceLibraryProvider func(workspaceID string) (WorkspaceLibraryResolver, error)

// IsWorkspaceRef reports whether ref carries the workspace-library prefix
// (the FR-028 discriminator). It is exported so call-sites and tests can
// branch on ref shape without re-deriving the prefix.
func IsWorkspaceRef(ref string) bool {
	return strings.HasPrefix(ref, WorkspaceRefPrefix)
}

// ParseWorkspaceRef splits a workspace-prefixed ref into its workspace and
// media IDs. ok is false if ref is not a workspace ref or is structurally
// malformed. The workspace ID is validated to be a single path segment (no
// separators or traversal) so it cannot be used to escape the
// workspaces/<id>/ root when handed to a library provider.
func ParseWorkspaceRef(ref string) (workspaceID, mediaID string, ok bool) {
	rest := strings.TrimPrefix(ref, WorkspaceRefPrefix)
	if rest == ref {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	if strings.ContainsAny(parts[0], `/\`) || strings.Contains(parts[0], "..") {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ValidateCallerWorkspace enforces the FR-028a membership guard for a
// workspace-prefixed ref. It returns ErrWorkspaceContextRequired when the
// caller supplied no workspace context, and ErrCrossWorkspaceRef (wrapping
// the caller/ref values) when the caller's workspace does not own the ref.
// A matching caller workspace authorizes the resolution.
func ValidateCallerWorkspace(refWorkspaceID string, opts ResolveOpts) error {
	if opts.CallerWorkspace == nil || *opts.CallerWorkspace == "" {
		return ErrWorkspaceContextRequired
	}
	if *opts.CallerWorkspace != refWorkspaceID {
		return fmt.Errorf("%w: caller=%q ref_workspace=%q",
			ErrCrossWorkspaceRef, *opts.CallerWorkspace, refWorkspaceID)
	}
	return nil
}

// IsCallerWorkspaceDenied reports whether err represents the FR-028a
// caller-workspace membership guard's denial (see ValidateCallerWorkspace)
// — either a missing caller-workspace context (ErrWorkspaceContextRequired)
// or an explicit cross-workspace spoofing attempt (ErrCrossWorkspaceRef) —
// as opposed to a routine stale/missing ref (ErrNotFound) or any other
// resolution failure.
//
// Review FIX 4: FileMediaStore.ResolveWithCallerWorkspace (the entry point
// every channel's SendMedia implementation calls to resolve an outbound
// media part) can return either of those two sentinels unwrapped, but
// nothing previously made that fact easy to act on — a caller wanting to
// treat a security-relevant Spoofing denial differently from a routine
// stale/deleted ref had to know about and separately check two different
// package-level error variables, which is easy to get wrong or skip
// entirely (and every current channel call site does exactly that: an
// identical logger.ErrorCF(...); continue folds the denial into the same
// undifferentiated "N of M media parts failed to send" count as any other
// resolve failure, losing the security-relevant signal exactly where audit
// visibility matters). This is the single, hard-to-misuse check callers
// should use instead: err should be logged/audited as a caller-workspace
// denial (not a routine delivery failure) whenever this returns true.
func IsCallerWorkspaceDenied(err error) bool {
	return errors.Is(err, ErrWorkspaceContextRequired) || errors.Is(err, ErrCrossWorkspaceRef)
}
