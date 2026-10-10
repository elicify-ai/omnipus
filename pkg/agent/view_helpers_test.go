package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"

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

// scanJSONLRangeFixture is the test-local replacement for the deleted
// memory.ScanJSONLRange (session-core DEL-10): it streams newline-framed JSONL
// records from r over [from,to], decoding each into memory.ArchivedMessage, and
// honours ctx cancellation between records.
func scanJSONLRangeFixture(ctx context.Context, r io.Reader, from, to int, fn func(idx int, raw []byte, msg memory.ArchivedMessage) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), memory.EncodedLineBound)
	idx := 0
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw := sc.Bytes()
		if idx >= from && idx <= to {
			var msg memory.ArchivedMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				return err
			}
			if err := fn(idx, append([]byte(nil), raw...), msg); err != nil {
				return err
			}
		}
		idx++
	}
	return sc.Err()
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
