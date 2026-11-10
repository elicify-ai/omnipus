package gateway

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// The existing guest-reply transport emits a token+done pair without a turn id.
// That is a mirror receipt, NOT proof the producing model turn ended. Wait for
// the actual turn-scoped done when testing completion, cost or failed status.
func wu15ReadTurnDone(t *testing.T, conn *websocket.Conn) gen.DoneFrame {
	t.Helper()
	for {
		var done gen.DoneFrame
		require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "done"), &done))
		if done.TurnId != nil && done.Stats != nil {
			return done
		}
	}
}
