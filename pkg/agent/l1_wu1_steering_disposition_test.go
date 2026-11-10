//go:build goolm && stdjson

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1SteeringEnqueue_MissingCanonicalOwnerRefusesVisiblyAndQueuesNothing(t *testing.T) {
	// FR-123 / §6.4: empty AgentID means consult the canonical owner,
	// never manufacture a default sender/owner. The enqueue boundary must
	// return a visible error, not accept something the drain will later lose.
	for _, owner := range []string{"mia", "deleted"} {
		t.Run(owner, func(t *testing.T) {
			al, _ := l1WU1Runtime(t)
			meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", owner)
			require.NoError(t, err)
			scope := "agent:" + owner + ":session:" + meta.ID
			item := steeringQueueItem{message: providers.Message{Role: "user", Content: "canonical-owner steer"}}
			_, err = al.enqueueSteeringItemWithStatus(scope, "", item, nil)
			if owner == "mia" {
				require.NoError(t, err, "live canonical-owner positive control remains enqueueable")
				assert.Equal(t, 1, al.steering.lenScope(scope), "control must really enqueue the item")
			} else {
				assert.Error(t, err, "FR-123: stale canonical owner must have a visible refused disposition")
				if err != nil {
					assert.Contains(t, err.Error(), owner, "refusal must identify the missing owner")
				}
				assert.Zero(t, al.steering.lenScope(scope), "an item with no runnable owner must not join any queue")
			}
		})
	}
}
