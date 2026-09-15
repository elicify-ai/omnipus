package config

import (
	"testing"
)

func TestChannelIdentity_Validate(t *testing.T) {
	tests := []struct {
		name    string
		id      ChannelIdentity
		wantErr bool
	}{
		{"empty kind (back-compat user default)", ChannelIdentity{Kind: ""}, false},
		{"user kind", ChannelIdentity{Kind: "user"}, false},
		{"agent kind with id", ChannelIdentity{Kind: "agent", ID: "mia"}, false},
		{"agent kind without id", ChannelIdentity{Kind: "agent"}, true},
		{"typo'd kind", ChannelIdentity{Kind: "agnet", ID: "mia"}, true},
		{"unknown kind", ChannelIdentity{Kind: "robot"}, true},
		// CRITICAL-1 regression: the API write path (validateChannelIdentity) and
		// route.go both lowercase+trim the kind, so Validate MUST accept exactly
		// those values or an API-constructible config bricks the next load.
		{"mixed-case Agent kind with id", ChannelIdentity{Kind: "Agent", ID: "mia"}, false},
		{"upper AGENT kind with id", ChannelIdentity{Kind: "AGENT", ID: "mia"}, false},
		{"padded user kind", ChannelIdentity{Kind: " user ", ID: ""}, false},
		{"mixed-case User kind", ChannelIdentity{Kind: "User"}, false},
		{"padded mixed-case Agent without id still rejected", ChannelIdentity{Kind: " Agent "}, true},
		{"whitespace-only kind (back-compat empty)", ChannelIdentity{Kind: "   "}, false},
		{"mixed-case unknown still rejected", ChannelIdentity{Kind: "Robot"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.id.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentRef_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     AgentRef
		wantErr bool
	}{
		{"empty kind (back-compat local default)", AgentRef{Kind: "", ID: "x"}, false},
		{"local kind", AgentRef{Kind: "local", ID: "x"}, false},
		{"remote-a2a kind", AgentRef{Kind: "remote-a2a", ID: "x"}, false},
		{"typo'd kind", AgentRef{Kind: "locol", ID: "x"}, true},
		{"unknown kind", AgentRef{Kind: "remote", ID: "x"}, true},
		// CRITICAL-1 regression: mixed-case / whitespace kinds the case-tolerant
		// paths accept must also pass Validate.
		{"mixed-case Local kind", AgentRef{Kind: "Local", ID: "x"}, false},
		{"upper LOCAL kind", AgentRef{Kind: "LOCAL", ID: "x"}, false},
		{"padded local kind", AgentRef{Kind: " local ", ID: "x"}, false},
		{"mixed-case remote-a2a kind", AgentRef{Kind: "Remote-A2A", ID: "x"}, false},
		{"whitespace-only kind (back-compat empty)", AgentRef{Kind: "   ", ID: "x"}, false},
		{"mixed-case unknown still rejected", AgentRef{Kind: "Robot", ID: "x"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
