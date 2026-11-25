// Package agentrecord reads an agent's persisted entity record the way the real
// store lays it down, for tests that must observe durable storage rather than a
// response, or a decoder's view of it. It decides no product result and keeps no
// state: it reads <home>/entities/agents/<id>.json and walks fixed keys.
//
// The carrier. pkg/agentstore writes the entity file by marshalling
// config.AgentConfig directly (state.go::Store.CreateState and ::MutateState;
// the legacy Store.Create does the same), so the record is a FLAT AgentConfig:
// the agent's tool settings are the top-level member `tools`
// (config.AgentConfig.Tools, json:"tools,omitempty") and there is no outer
// `agent` wrapper. The per-agent tool overrides are tools.builtin.policies today;
// spec C-4 adds the per-agent skill overrides to the same object as
// tools.skills.policies. It adds a member to the existing AgentToolsCfg, no wrapper
// and no renamed tag. `tools_cfg` is the REST request's name for that object
// (AgentCreateRequestMain.tools_cfg, PUT /agents/{id}); it is never a stored key,
// and a decoy file that carries a value under it must read as "no override".
//
// agentrecord_test.go shows the walk against records the real store wrote and
// against deliberately wrong wrappers and fields.
package agentrecord

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var (
	// ErrNoRecord reports that the entity file does not exist.
	ErrNoRecord = errors.New("no stored agent record")
	// ErrShape reports a record member of the wrong JSON type (a string where an
	// object belongs, a null, an array). A wrong shape is never read as "empty".
	ErrShape = errors.New("stored agent record has an unexpected shape")
	// ErrCarrier reports a carrier name this package does not know.
	ErrCarrier = errors.New("unknown policy carrier")
)

// Carrier names the member of the stored `tools` object whose `policies` map is read.
type Carrier string

const (
	// Builtin is tools.builtin.policies: the per-agent tool overrides
	// (config.AgentBuiltinToolsCfg.Policies).
	Builtin Carrier = "builtin"
	// Skills is tools.skills.policies: the per-agent skill overrides (spec C-4).
	Skills Carrier = "skills"
)

// Path is the entity file of agent id under an Omnipus home.
func Path(home, id string) string {
	return filepath.Join(home, "entities", "agents", id+".json")
}

// Read returns the record decoded as a JSON object, exactly as stored.
func Read(home, id string) (map[string]any, error) {
	raw, err := os.ReadFile(Path(home, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoRecord, Path(home, id))
	}
	if err != nil {
		return nil, err
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("%w: %s is not a JSON object: %w", ErrShape, Path(home, id), err)
	}
	return record, nil
}

// ReadPolicies returns tools.<carrier>.policies of the stored record. A record that
// lacks the member at any step (no tools, no such carrier, no policies) reads as an
// empty map: the agent has no overrides there. A member that is present but not a JSON
// object is an error, never silently empty.
func ReadPolicies(home, id string, carrier Carrier) (map[string]any, error) {
	if carrier != Builtin && carrier != Skills {
		return nil, fmt.Errorf("%w: %q", ErrCarrier, carrier)
	}
	record, err := Read(home, id)
	if err != nil {
		return nil, err
	}
	node := record
	for _, key := range []string{"tools", string(carrier), "policies"} {
		child, present := node[key]
		if !present {
			return map[string]any{}, nil
		}
		next, isObject := child.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("%w: %q is %T, want an object", ErrShape, key, child)
		}
		node = next
	}
	return node, nil
}

// Policies is ReadPolicies that fails the calling test on any error.
func Policies(t testing.TB, home, id string, carrier Carrier) map[string]any {
	t.Helper()
	policies, err := ReadPolicies(home, id, carrier)
	if err != nil {
		t.Fatalf("read the stored %s overrides of agent %q: %v", carrier, id, err)
	}
	return policies
}

// Raw returns the record's text, for assertion messages. It fails the calling test
// when the record cannot be read.
func Raw(t testing.TB, home, id string) string {
	t.Helper()
	raw, err := os.ReadFile(Path(home, id))
	if err != nil {
		t.Fatalf("read the stored record of agent %q: %v", id, err)
	}
	return string(raw)
}
