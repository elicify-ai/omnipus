//go:build goolm && stdjson

package agent

// SF-3: FR-10/13/15/17 apply after the actual retryTimeout history rebuild,
// not just the first resolution. Use the existing window/recall harness and
// real media:// storage; assert the trim happened before judging the retry.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestChatPDF_TimeoutRebuildRetainsPresentation(t *testing.T) {
	for _, spent := range []bool{false, true} {
		name := "native-budget-unused"
		if spent {
			name = "text-budget-spent"
		}
		t.Run(name, func(t *testing.T) {
			h := cwR1New(t, 80_000)
			// cwR1New deliberately omits boot-only registry wiring; install its
			// real agent before SetMediaStore propagates to registered tools.
			h.al.registry = &AgentRegistry{agents: map[string]*AgentInstance{h.agent.ID: h.agent}}
			store := mediatest.NewFileMediaStore(t)
			h.al.SetMediaStore(store)
			data := pdf1968TextPDF(t, 1, true)
			ref := pdf1968StorePDF(t, store, "retained.pdf", data)
			h.agent.Model = "fx/pdf"
			cat := pdf1968Catalog(t, []pdf1968ProviderRow{pdf1968CapableRow("fx/timeout", "fx/pdf", nil)})
			h.al.SetCapabilityCatalog(cat)
			h.append(t, providers.Message{Role: "user", Content: "retained current user", Media: []string{ref}})
			ts := h.turn("")
			ts.mediaRetryDone.Store(spent)
			peer := newPdf1968Peer(t,
				pdf1968Step{status: 408, errBody: `{"error":{"code":"request_timeout","message":"request timed out"}}`},
				pdf1968Step{content: "after timeout"})
			p, err := providers.NewHTTPProviderWithTimeouts("test-placeholder", peer.server.URL, "", "max_tokens", 10, 10, nil)
			require.NoError(t, err)
			h.agent.StoreProviderPool(map[string]providers.LLMProvider{"fx/timeout": p})
			messages := h.al.assembleMessages(context.Background(), ts, h.store.GetHistory(h.key), "", nil, nil)
			rr := cwR1Flow(h, ts, messages, p)
			rt := rr.rq.ri.rf.rt
			rt.activeCandidates = []providers.FallbackCandidate{{Provider: "fx/timeout", Model: "fx/pdf"}}
			rt.chatPDFs = &chatPDFRegistry{}
			messages = resolveMediaRefsForTurn(messages, store, 25<<20, "fx/timeout", "fx/pdf", nil, cat, nil, "", rt.chatPDFs)
			rr.rq.ri.messages, rr.rq.ri.rf.callMessages = messages, messages
			_, err = rt.callProviderOnce(messages, nil)
			require.Error(t, err, "premise: the actual provider attempt failed with the peer's timeout")
			rr.rq.ri.rf.err = err
			before := peer.Bodies()
			require.Len(t, before, 1)
			if spent {
				require.Empty(t, pdf1968BodyPDFs(t, before[0]))
				require.Contains(t, before[0], "PAGE 1 text")
				require.Contains(t, before[0], pdf1968P1Sentence)
			} else {
				require.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, before[0]))
			}
			// A span activated while the request was in flight makes retry
			// over-budget. Dropping it is a successful real window trim even
			// when the current user attachment is the protected history floor.
			span := newRecallSpan(1, 1, []providers.Message{{Role: "user", Content: strings.Repeat("transient recalled context ", 80_000)}}, []int{1})
			h.al.setRecallSpan(h.key, span)
			require.True(t, isOverContextBudgetTokens(agentContextBudget(h.agent), append(append([]providers.Message(nil), messages...), span.Messages()...), 0), "premise: timeout retry actually requires trimming")
			rr.rq.ri.turnMediaStore, rr.rq.ri.turnCatalog, rr.rq.ri.maxMediaSize = store, cat, 25<<20
			cr := &agentLoopRunTurnResponseCallLLMWithRetries{rr: rr, maxRetries: 2}
			require.Equal(t, agentLoopRunTurnResponseCallLLMWithRetriesNext, cr.classifyFailure())
			require.True(t, cr.isTimeoutError, "real HTTP 408 must select the actual timeout-retry path")
			flow := cr.retryTimeout(0)
			require.Equal(t, agentLoopRunTurnResponseCallLLMWithRetriesContinue, flow)
			require.True(t, cr.compactionAttemptedOnTimeout, "premise: actual timeout trim/rebuild branch ran")
			require.Nil(t, h.al.activeRecallSpan(h.key), "real trim drops recall span first")
			history := h.store.GetHistory(h.key)
			require.Len(t, history, 1)
			require.Equal(t, []string{ref}, history[0].Media, "persisted source remains the original media reference")
			_, err = rt.callProviderOnce(rr.rq.ri.rf.callMessages, nil)
			require.NoError(t, err)
			bodies := peer.Bodies()
			require.Len(t, bodies, 2)
			if spent {
				assert.Empty(t, pdf1968BodyPDFs(t, bodies[1]))
				assert.Contains(t, bodies[1], "PAGE 1 text", "SF-3: rebuilt retry must retain extracted attachment text")
				assert.Contains(t, bodies[1], pdf1968P1Sentence, "spent recovery budget must retain its notice")
			} else {
				assert.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, bodies[1]), "SF-3: timeout rebuild must not silently drop the retained chat PDF")
			}
			assert.Contains(t, bodies[1], "retained current user")
			assert.Equal(t, spent, ts.mediaRetryDone.Load())
		})
	}
}
