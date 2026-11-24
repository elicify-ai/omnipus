package gateway

import (
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// contracts/components/schemas/ConfigurationRevision.yaml: a revision is 64
// lowercase hex characters. The SPA validates GET /programs and the channel
// programs setting with the generated schema, so a shorter token makes the
// Programs tab say "Programs could not be loaded." (#1960 S1 integration,
// found by the programs.spec.ts e2e rows after the frontend merge).
func TestProgramsRevision_MatchesContractPattern(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	cfg := &config.Config{}
	cfg.Channels = map[string]config.ChannelInstanceConfig{"tg": {}}

	if got := programsRevision(cfg); !pattern.MatchString(got) {
		t.Errorf("programsRevision = %q, want 64 lowercase hex characters", got)
	}
	if got := channelProgramsRevision(cfg, "tg"); !pattern.MatchString(got) {
		t.Errorf("channelProgramsRevision = %q, want 64 lowercase hex characters", got)
	}

	// The token still follows the state it guards.
	before := programsRevision(cfg)
	cfg.Programs.WebChatAllowed = !cfg.Programs.WebChatAllowed
	if programsRevision(cfg) == before {
		t.Error("programsRevision did not change when the programs settings changed")
	}
	chBefore := channelProgramsRevision(cfg, "tg")
	ch := cfg.Channels["tg"]
	ch.ProgramsAllowed = !ch.ProgramsAllowed
	cfg.Channels["tg"] = ch
	if channelProgramsRevision(cfg, "tg") == chBefore {
		t.Error("channelProgramsRevision did not change when the channel changed")
	}
}
