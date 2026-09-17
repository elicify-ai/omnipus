// loop_run_turn_types.go: Shared stage-state types for runTurn's extracted conductors
//
// These types used to live in loop.go next to runTurn. Their methods already
// live in loop_run_turn.go / loop_run_turn_iterations.go /
// loop_run_turn_response.go / loop_run_turn_tools.go; this file is the type
// declarations those methods attach to.

package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// agentLoopRunTurn carries the shared state of runTurn across its stages.
type agentLoopRunTurn struct {
	al                 *AgentLoop
	ts                 *turnState
	turnCtx            context.Context
	activeCandidates   []providers.FallbackCandidate
	activeProvider     providers.LLMProvider
	iteration          int
	llmOpts            map[string]any
	llmModel           string
	onToolCallProgress protocoltypes.OnToolCallProgress
	inspectionImages   map[string][]tools.InspectionImage
}

// agentLoopRunTurnFallbacks carries the shared state of runTurn across its stages.
type agentLoopRunTurnFallbacks struct {
	rt               *agentLoopRunTurn
	providerToolDefs []providers.ToolDefinition
	callMessages     []providers.Message
	callLLM          func(messagesForCall []providers.Message, toolDefsForCall []providers.ToolDefinition) (*providers.LLMResponse, error)
	response         *providers.LLMResponse
	err              error
}

// agentLoopRunTurnIteration carries the shared state of runTurn across its stages.
type agentLoopRunTurnIteration struct {
	rf                           *agentLoopRunTurnFallbacks
	turnMediaStore               media.MediaStore
	turnCatalog                  *catalog.Catalog
	turnRefcounter               *sessionRefcounter
	turnStatus                   TurnEndStatus
	wsDir                        string
	messages                     []providers.Message
	cfg                          *config.Config
	maxMediaSize                 int
	activeModel                  string
	pendingMessages              []providers.Message
	toolCallTruncationRepairUsed bool
	gracefulTerminal             bool
	ret0                         turnResult
	ret1                         error
}

// agentLoopRunTurnRequest carries the shared state of runTurn across its stages.
type agentLoopRunTurnRequest struct {
	ri                  *agentLoopRunTurnIteration
	policyFilteredTools []tools.Tool
	filterTimePolicyMap map[string]string
	goalForce           goalForcingDecision
	useNativeSearch     bool
	offeredTools        offeredToolSet
	ret0                turnResult
	ret1                error
}

// agentLoopRunTurnResponse carries the shared state of runTurn across its stages.
type agentLoopRunTurnResponse struct {
	rq                  *agentLoopRunTurnRequest
	citationTracker     *citationTracker
	continuationChain   []providers.Message
	orphanMarkup        providers.OrphanToolMarkup
	hasOrphanMarkup     bool
	normalizedToolCalls []providers.ToolCall
	ret0                turnResult
	ret1                error
}

// agentLoopRunTurnTools carries the shared state of runTurn across its stages.
type agentLoopRunTurnTools struct {
	ctx                context.Context
	rr                 *agentLoopRunTurnResponse
	turnChannelManager *channels.Manager
	finalContent       string
	midTurnGuardErr    error
	ret0               turnResult
	ret1               error
}

// agentLoopRunTurnConductor carries the shared state of runTurn across its stages.
type agentLoopRunTurnConductor struct {
	rx         *agentLoopRunTurnTools
	turnCancel context.CancelFunc
	ret0       turnResult
	ret1       error
}

// agentLoopRunTurnConductorFlow reports how a block stage of agentLoopRunTurnConductor wants the conductor to proceed.
type agentLoopRunTurnConductorFlow int

const (
	agentLoopRunTurnConductorNext agentLoopRunTurnConductorFlow = iota
	agentLoopRunTurnConductorReturn
	agentLoopRunTurnConductorContinue
	agentLoopRunTurnConductorBreak
)

// agentLoopRunTurnFinalize carries the shared state of runTurn across its stages.
type agentLoopRunTurnFinalize struct {
	rc *agentLoopRunTurnConductor
}
