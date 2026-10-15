package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Oracle: chief fix-round-1 F03 / Q1 B: BOTH native runtimes require a
// nonblank soul. A Main-only omission case cannot pin the Subagent rule.
func TestL11274NativeSoulRequiredForMainAndSubagent(t *testing.T) {
	for _, agentType := range []string{"Main", "Subagent"} {
		for _, supplied := range []string{"omitted", "empty", "whitespace"} {
			t.Run(agentType+"/"+supplied, func(t *testing.T) {
				f := newL11274Fixture(t, "", false)
				args := l11274Fix1NativeArgs(agentType)
				switch supplied {
				case "empty":
					args["soul"] = ""
				case "whitespace":
					args["soul"] = " \t\n "
				}
				before := l11274Fix1ReadResources(t, f)
				message := l11274FailureDetails(t, f.execute(t, "create_agent", args), "INVALID_INPUT")
				if !strings.Contains(message, "soul") || !strings.Contains(message, "required") {
					t.Errorf("native %s %s-soul refusal must name the required field, got %q", agentType, supplied, message)
				}
				l11274Fix1AssertNoWrites(t, f, "native-soul-boundary", before)
			})
		}
	}
}

func TestL11274NativeMainAndSubagentAcceptAndPersistSoul(t *testing.T) {
	for _, agentType := range []string{"Main", "Subagent"} {
		t.Run(agentType, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			args := l11274Fix1NativeArgs(agentType)
			args["soul"] = l11274Soul
			result := f.execute(t, "create_agent", args)
			if result.IsError {
				t.Fatalf("native %s valid soul must be accepted: %s", agentType, result.ForLLM)
			}
			body := l11274ResultObject(t, result)
			// Founder decision 2026-10-14 (WU-3): the id is a generated UUID.
			id, _ := body["id"].(string)
			if _, err := uuid.Parse(id); err != nil {
				t.Fatalf("native control id %q must be a generated UUID: %v", id, err)
			}
			for key, want := range map[string]any{"type": agentType,
				"model": l11274Model, "status": "joined_workspace", "persistence_status": "complete", "activation_status": "active"} {
				if body[key] != want {
					t.Errorf("native control result[%s] = %v, want %v", key, body[key], want)
				}
			}
			durable, err := agentstore.New(f.home).ReadState(id)
			if err != nil {
				t.Fatal(err)
			}
			if durable.Soul != l11274Soul || durable.Revision != body["revision"] {
				t.Errorf("native soul/revision readback differs: soul=%q, revision=%q, reported=%v", durable.Soul, durable.Revision, body["revision"])
			}
			if durable.Agent.Model == nil || durable.Agent.Model.Primary != l11274Model || durable.Agent.IsExternalCLIWorker() {
				t.Errorf("native model/runtime readback differs: %+v", durable.Agent)
			}
			if got := durable.Agent.Type == config.AgentTypeWorker; got != (agentType == "Subagent") {
				t.Errorf("native worker classification = %v for %s", got, agentType)
			}
			state, err := workspace.ReadState(f.home, l11274WorkspaceID)
			if err != nil || !reflect.DeepEqual(state.Workspace.CoreTeam, []string{"omnipus", id}) {
				t.Errorf("native create membership readback = %v, err=%v", state.Workspace.CoreTeam, err)
			}
			if _, ok := f.al.GetRegistry().GetAgent(id); !ok {
				t.Error("native control is durable but not live")
			}
		})
	}
}

// Chief: any supplied external soul is unsupported, including blank values.
// ValidateFields checks the supplied FIELD, so this does not contradict it.
func TestL11274BlankExternalSoulIsRefusedAsSupplied(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		for _, supplied := range []string{"empty", "whitespace"} {
			t.Run(cli+"/"+supplied, func(t *testing.T) {
				f := newL11274Fixture(t, "", false)
				path := l11274Executable(t, f.home, cli, true)
				args := l11274CreateArgs("Blank external soul", cli, path)
				args["soul"] = ""
				if supplied == "whitespace" {
					args["soul"] = " \t\n "
				}
				before := l11274Fix1ReadResources(t, f)
				message := l11274FailureDetails(t, f.execute(t, "create_agent", args), "INVALID_INPUT")
				if !strings.Contains(message, "soul") || !strings.Contains(message, "unsupported") {
					t.Errorf("external %s soul refusal must identify the unsupported field, got %q", supplied, message)
				}
				l11274Fix1AssertNoWrites(t, f, "blank-external-soul", before)
			})
		}
	}
}
