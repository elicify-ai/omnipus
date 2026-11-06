package agent

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func t25PrintedTargets(t *testing.T, block string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	instance := ""
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- ") {
			instance = strings.SplitN(line[2:], " (", 2)[0]
		}
		if !strings.HasPrefix(line, "  - ") {
			continue
		}
		require.NotEmpty(t, instance, "instrument: a target belongs to a connection heading")
		parts := strings.SplitN(line[4:], " (", 2)
		require.Len(t, parts, 2, "T-5 target line includes its kind label")
		key := instance + "|" + parts[0]
		require.False(t, out[key], "a target must not be printed twice: %s", key)
		out[key] = true
	}
	return out
}

func TestT25_Block_PropertyEveryPrintedTargetPassesStartAllowed_S05(t *testing.T) {
	// S-05 requests generated configs. A fixed seed makes every counterexample
	// reproducible. Expected eligibility below comes from T-1/T-3, not a
	// second call to StartAllowed or any renderer helper.
	rng := rand.New(rand.NewSource(25))
	positiveSamples, refusedSamples := 0, 0
	for sample := 0; sample < 128; sample++ {
		instances := map[string]config.ChannelInstanceConfig{}
		availability := map[string]channels.Availability{}
		expected := map[string]bool{}
		candidates := map[string][]string{}
		eligible := map[string]bool{}
		for i, platform := range []string{"discord", "telegram", "slack"} {
			id := platform + ".generated"
			policy := []string{"", "reply_only", "agent_may_start"}[rng.Intn(3)]
			inst := t25Instance(platform, policy)
			inst.Enabled = rng.Intn(2) == 1
			binding := rng.Intn(4)
			switch binding {
			case 1:
				inst.WorkspaceID = "ws-other"
			case 2:
				inst.Identity.ID = "agent-other"
			case 3:
				inst.WorkspaceID = ""
			}
			state := []channels.AvailabilityState{"running", "not_running", "not_loaded"}[rng.Intn(3)]
			availability[id] = channels.Availability{State: state, Reason: "QA unavailable"}
			if state == "running" {
				availability[id] = channels.Availability{State: state}
			}
			count := rng.Intn(4)
			bareIDs := []string{}
			for j := 0; j < count; j++ {
				bareIDs = append(bareIDs, fmt.Sprint(100+sample*12+i*4+j))
			}
			if inst.Outbound != nil {
				for _, bare := range bareIDs {
					inst.Outbound.Targets = append(inst.Outbound.Targets, platform+":"+bare)
				}
			}
			instances[id] = inst
			candidates[id] = bareIDs
			eligible[id] = platform != "slack" && inst.Enabled && binding == 0 && policy == "agent_may_start" && state == "running"
		}
		// Finish generation before either consumer sees the one snapshot.
		cfg := &config.Config{Channels: instances}
		for id, bareIDs := range candidates {
			for _, bare := range bareIDs {
				want := eligible[id]
				decision := channels.StartAllowed(cfg, availability[id], id, bare, "ws-a", "agent-a")
				require.Equal(t, want, decision.Allowed, "S-05 independent table oracle: sample%d %s/%s", sample, id, bare)
				if want {
					expected[id+"|"+bare] = true
					positiveSamples++
				} else {
					refusedSamples++
				}
			}
			for j := 0; j < 5; j++ {
				// Disjoint from generated stored ids; no sentinel or wildcard.
				bare := fmt.Sprint(1_000_000_000 + sample*100 + j + rng.Intn(2)*10_000_000)
				require.False(t, channels.StartAllowed(cfg, availability[id], id, bare, "ws-a", "agent-a").Allowed,
					"S-05: fresh unlisted id must be refused, sample%d %s/%s", sample, id, bare)
			}
		}
		in := t25Input(instances)
		in.Config = cfg
		calls := map[string]int{}
		in.Availability = func(id string) channels.Availability { calls[id]++; return availability[id] }
		got := t25PrintedTargets(t, channels.RenderChannelsBlock(in))
		require.Equal(t, expected, got, "S-05 complete printed set equals the independently allowed set, sample%d", sample)
		for id, inst := range instances {
			wantCalls := 0
			if inst.Enabled && inst.WorkspaceID == "ws-a" && inst.Identity.ID == "agent-a" {
				wantCalls = 1
			}
			require.Equal(t, wantCalls, calls[id], "FR-005: sample%d availability for%s", sample, id)
		}
	}
	require.Positive(t, positiveSamples, "instrument: generated corpus must include allowed targets")
	require.Positive(t, refusedSamples, "instrument: generated corpus must include refused targets")
}
