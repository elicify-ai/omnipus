package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// viewArchived flattens a WindowView's live slots into the ArchivedMessage
// slice the dense WindowSnapshot.Archive used to expose, so a fixture that
// inspects archived role/content keeps its assertion unchanged after DEL-12 /
// DEL-10 deleted the dense snapshot API.
func viewArchived(v session.WindowView) []memory.ArchivedMessage {
	out := make([]memory.ArchivedMessage, 0, len(v.Live))
	for _, s := range v.Live {
		out = append(out, memory.ArchivedMessage{Message: s.Message, TS: s.TS})
	}
	return out
}

// appendWindowMsg is the test replacement for the deleted AppendWindowMessage
// (session-core DEL-12/DEL-10): it appends one MODEL message through the
// checked model seam and returns the bounded view after the append.
func appendWindowMsg(store session.ContextWindowStore, ctx context.Context, key string, msg providers.Message) (session.WindowView, error) {
	// A real UnifiedStore's own append resolves the issuing assistant for a
	// role:"tool" message from the window (the legacy SessionWriter seam), which
	// is what the deleted AppendWindowMessage offered.
	if us, ok := store.(*session.UnifiedStore); ok {
		us.AddFullMessage(key, msg)
		return store.WindowView(ctx, key)
	}
	_, view, err := store.AppendModelMessage(ctx, key, session.ModelAppend{
		Message: msg, ViewMembership: session.ViewMembershipModel, Source: session.EntrySource{Kind: entrySourceKind(msg.Role)},
	})
	return view, err
}

// entrySourceKind mirrors the trusted-source kind the store mints from a message
// role (pkg/session's own sourceKindForRole is unexported).
func entrySourceKind(role string) string {
	switch role {
	case "user", "assistant", "system", "tool":
		if role == "assistant" {
			return "agent"
		}
		return role
	default:
		return "anonymous"
	}
}
