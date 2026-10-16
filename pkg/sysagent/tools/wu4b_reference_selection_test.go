package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWU4B_MatchingReferencesSelectWithoutCredentialWrite(t *testing.T) {
	for _, name := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		t.Run(name, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, ref := wu4bMatchingSelection(t, f, name)
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			after := f.snapshot(t)
			assert.Equal(t, before.credentials, after.credentials, "D2 selection never writes/overwrites credentials or receipts")
			wu3Success(t, result)
			assert.Equal(t, before.saves+1, after.saves, "one approved config publication")
			switch name {
			case "configure_provider":
				assert.Equal(t, ref, f.cfg.Providers[0].APIKeyRef)
				assert.Equal(t, 1, f.listings)
			case "add_mcp_server":
				assert.Equal(t, map[string]string{"TOKEN": ref}, f.cfg.Tools.MCP.Servers["fixture"].EnvRefs)
				assert.Equal(t, 1, f.reconciles)
			case "configure_channel":
				assert.Equal(t, ref, f.cfg.Channels["telegram"].TokenRef)
				assert.False(t, f.cfg.Channels["telegram"].Enabled, "selection is not enablement")
				wu3Outcome(t, result, "saved but not working")
			}
			assert.NotContains(t, string(after.config), "WU4B-SELECTED-VALUE")
			assert.NotContains(t, result.ForLLM, "WU4B-SELECTED-VALUE")
		})
	}
}

// FR-064/065: a plausible name and a readable value are not stored consent.
// Every invalid class is exercised for each of the three actual tools.
func TestWU4B_UnknownUnboundUnavailableAndWrongKindReferencesRefuseAtomically(t *testing.T) {
	for _, name := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		for _, condition := range []string{"unknown", "unbound", "unavailable", "wrong_kind", "empty", "null", "wrong_type", "no_store", "store_locked"} {
			t.Run(name+"/"+condition, func(t *testing.T) {
				f := newWU4BFixture(t)
				tool, args, validRef := wu4bMatchingSelection(t, f, name)
				var supplied any = validRef
				switch condition {
				case "unknown":
					supplied = "openai_api_key.v999999"
				case "unbound":
					supplied = "mcp_fixture_TOKEN.v1"
					require.NoError(t, f.d.CredStore.Set("mcp_fixture_TOKEN.v1", "WU4B-UNBOUND-VALUE"))
				case "unavailable":
					require.NoError(t, f.d.CredStore.Delete(validRef))
				case "wrong_kind":
					supplied = f.bound(t, credentials.CardRecipient{Kind: "private", AgentID: "omnipus", Field: "value"}, "WU4B-PRIVATE-VALUE")
				case "empty":
					supplied = ""
				case "null":
					supplied = nil
				case "wrong_type":
					supplied = 42
				case "no_store":
					f.d.CredStore = nil
				case "store_locked":
					f.d.CredStore.Close()
				}
				switch name {
				case "configure_provider":
					args["api_key_ref"] = supplied
				case "add_mcp_server":
					args["env_refs"] = map[string]any{"TOKEN": supplied}
				case "configure_channel":
					args["token_ref"] = supplied
				}
				before := f.snapshot(t)
				result := tool.Execute(context.Background(), args)
				f.unchanged(t, before)
				wu4bRefusal(t, result, "ref")
			})
		}
	}
}

func TestWU4B_MatchingReferencesStillRequireCurrentExpectedRevision(t *testing.T) {
	for _, name := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		for _, condition := range []string{"stale", "missing"} {
			t.Run(name+"/"+condition, func(t *testing.T) {
				f := newWU4BFixture(t)
				tool, args, _ := wu4bMatchingSelection(t, f, name)
				if condition == "missing" {
					delete(args, "expected_revision")
				} else {
					args["expected_revision"] = "confirmed-but-stale"
				}
				before := f.snapshot(t)
				result := tool.Execute(context.Background(), args)
				f.unchanged(t, before)
				code := "CONFLICT"
				if condition == "missing" {
					code = "INVALID_INPUT"
				}
				wu3Error(t, result, code)
				wu3Outcome(t, result, "failed: nothing saved")
			})
		}
	}
}

// A change after the plan but BEFORE lock acquisition must be checked live,
// never validated against an earlier GetCfg snapshot or silently retried.
func TestWU4B_RevisionIsRecheckedInsideTheConfigLock(t *testing.T) {
	for _, name := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		t.Run(name, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, name)
			lock := f.d.LockConfigWrites
			var winner wu4bSnapshot
			f.d.LockConfigWrites = func() func() {
				switch name {
				case "configure_provider":
					f.cfg.Providers[0].APIBase = "https://winner.wu4b.invalid/v1"
				case "add_mcp_server":
					s := f.cfg.Tools.MCP.Servers["fixture"]
					s.Command = "winner-command"
					f.cfg.Tools.MCP.Servers["fixture"] = s
				case "configure_channel":
					c := f.cfg.Channels["telegram"]
					c.BotID = "winner-bot"
					f.cfg.Channels["telegram"] = c
				}
				f.persist(t)
				winner = f.snapshot(t)
				return lock()
			}
			result := tool.Execute(context.Background(), args)
			wu3Error(t, result, "CONFLICT")
			wu3Outcome(t, result, "failed: nothing saved")
			f.unchanged(t, winner)
		})
	}
}

func TestWU4B_ProviderRejectsEachChangedRecipientDimension(t *testing.T) {
	for _, dimension := range []string{"provider", "endpoint", "protocol", "field"} {
		t.Run(dimension, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, "configure_provider")
			r := credentials.CardRecipient{Kind: "provider_key", Provider: "openai", Endpoint: "https://provider.wu4b.invalid/v1", Protocol: "openai-compatible", Field: "api_key"}
			switch dimension {
			case "provider":
				r.Provider = "anthropic"
			case "endpoint":
				r.Endpoint = "https://other.wu4b.invalid/v1"
			case "protocol":
				r.Protocol = "anthropic"
			case "field":
				r.Field = "token"
			}
			args["api_key_ref"] = f.bound(t, r, "WU4B-WRONG-RECIPIENT")
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			f.unchanged(t, before)
			wu4bRefusal(t, result, "recipient")
		})
	}
}

func TestWU4B_ProviderTrailingSlashNormalizationKeepsExactRecipient(t *testing.T) {
	f := newWU4BFixture(t)
	tool, args, ref := wu4bMatchingSelection(t, f, "configure_provider")
	args["api_base"] = "https://provider.wu4b.invalid/v1/"
	before := f.snapshot(t)
	result := tool.Execute(context.Background(), args)
	wu3Success(t, result)
	assert.Equal(t, ref, f.cfg.Providers[0].APIKeyRef)
	assert.Equal(t, before.credentials, f.snapshot(t).credentials)
}

func TestWU4B_ChannelDestinationEditsCannotCarryRetainedReferences(t *testing.T) {
	for _, field := range []string{"bot_id", "app_id", "mode"} {
		for _, newReference := range []bool{false, true} {
			t.Run(field+map[bool]string{false: "/retained_only", true: "/retained_and_supplied"}[newReference], func(t *testing.T) {
				f := newWU4BFixture(t)
				tool, args, ref := wu4bMatchingSelection(t, f, "configure_channel")
				c := f.cfg.Channels["telegram"]
				c.TokenRef = ref
				f.cfg.Channels["telegram"] = c
				if !newReference {
					delete(args, "token_ref")
				}
				args[field] = "different-destination"
				args = systools.WithCurrentRevisionForTest(tool, args)
				f.persist(t)
				before := f.snapshot(t)
				result := tool.Execute(context.Background(), args)
				f.unchanged(t, before)
				wu4bRefusal(t, result, "recipient")
			})
		}
	}
}
