// Package sandboxkeys writes spec-named sandbox keys (for example
// sandbox.skill_policies, sandbox.tool_visibility, sandbox.skill_visibility,
// sandbox.skill_trust) as literal JSON into a candidate copy of a running loop's
// live configuration and makes that copy live through the harness's own
// production reload path. It never decides a product result and keeps no state
// of its own: a key the configuration decoder does not carry is a loud
// missing-implementation obligation, never a value parked in a test variable
// that a stub could fill.
//
// Why a reload and not only a config pointer swap: the agent loop keeps
// per-agent snapshots of the global sandbox section (the tool policy is one),
// built when an agent instance is built. A written key reaches a later turn only
// through the reload that rebuilds them — the path a person's settings change
// takes in production (pkg/agent/instance.go::StoreToolPolicy documents that
// production never hot-swaps a global policy). So the harness supplies publish:
// persist if its production path persists, call the loop's
// ReloadProviderAndConfig with its own provider, and re-bind anything the
// harness itself bound to instances, exactly as the F22 fixture does.
package sandboxkeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

var (
	// ErrNotCarried reports that the configuration decoder dropped, or changed,
	// something that was written: the product does not store that key yet.
	ErrNotCarried = errors.New("config does not carry the key")
	// ErrNotPublished reports that the harness's publish returned without error
	// but the loop's live configuration does not hold the write afterwards (a
	// no-op publisher).
	ErrNotPublished = errors.New("the loop did not publish the written key")
)

// Apply decodes the patch, a map of sandbox key to JSON value, as literal JSON
// into the sandbox section of a private copy of the live configuration, one key
// at a time in sorted order, so the first key the product cannot carry is the
// one reported. Only when every key and entry is carried is the copy handed to
// publish; the live configuration is then re-read to prove the loop holds it.
func Apply(live func() *config.Config, publish func(*config.Config) error, patch map[string]any) error {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	type write struct {
		key     string
		literal []byte
		want    map[string]any
	}
	writes := make([]write, 0, len(keys))
	for _, key := range keys {
		literal, err := json.Marshal(map[string]any{key: patch[key]})
		if err != nil {
			return fmt.Errorf("sandboxkeys: encode sandbox.%s: %w", key, err)
		}
		var want map[string]any
		if err := json.Unmarshal(literal, &want); err != nil {
			return fmt.Errorf("sandboxkeys: normalise sandbox.%s: %w", key, err)
		}
		writes = append(writes, write{key: key, literal: literal, want: want})
	}

	current := live()
	if current == nil {
		return errors.New("sandboxkeys: the loop has no live configuration to copy")
	}
	next, err := current.Clone()
	if err != nil {
		return fmt.Errorf("sandboxkeys: copy the live configuration: %w", err)
	}
	for _, w := range writes {
		// The literal is valid JSON built from Go values, so a decode error is the
		// product decoder refusing the written value (for example a stored value
		// outside the vocabulary it knows): it cannot carry it.
		if err := json.Unmarshal(w.literal, &next.Sandbox); err != nil {
			return fmt.Errorf("%w: sandbox.%s: the config decoder rejects it: %w", ErrNotCarried, w.key, err)
		}
		if err := carried(next.Sandbox, w.want, ErrNotCarried); err != nil {
			return err
		}
	}
	if err := publish(next); err != nil {
		return fmt.Errorf("sandboxkeys: the loop refused the reload: %w", err)
	}
	after := live()
	if after == nil {
		return fmt.Errorf("%w: the loop has no live configuration", ErrNotPublished)
	}
	for _, w := range writes {
		if err := carried(after.Sandbox, w.want, ErrNotPublished); err != nil {
			return err
		}
	}
	return nil
}

// Store is Apply for a test. A key the configuration does not carry fails with
// the BLOCKED text the RED rules ask for; any other error is an instrument
// failure and says so.
func Store(t testing.TB, live func() *config.Config, publish func(*config.Config) error, patch map[string]any, required string) {
	t.Helper()
	err := Apply(live, publish, patch)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotCarried):
		t.Fatalf("BLOCKED: %v — required by %s", err, required)
	default:
		t.Fatalf("sandboxkeys instrument failure: %v", err)
	}
}

// carried checks that sandbox, re-encoded, holds every key and entry of want.
// sandbox is the product's sandbox section (a config.OmnipusSandboxConfig value
// when called from Apply); it is any so the instrument's own tests can hand it a
// stand-in section that does carry a key the product does not carry yet.
func carried(sandbox any, want map[string]any, kind error) error {
	stored, err := json.Marshal(sandbox)
	if err != nil {
		return fmt.Errorf("sandboxkeys: encode sandbox: %w", err)
	}
	var have map[string]any
	if err := json.Unmarshal(stored, &have); err != nil {
		return fmt.Errorf("sandboxkeys: decode sandbox: %w", err)
	}
	keys := make([]string, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		got, present := have[key]
		if !present {
			return fmt.Errorf("%w: sandbox.%s", kind, key)
		}
		if !contains(got, want[key]) {
			return fmt.Errorf("%w: sandbox.%s holds %v, written %v", kind, key, got, want[key])
		}
	}
	return nil
}

// contains is true when got holds want: equal for a scalar, and for an object
// every written entry present and equal (other stored entries are allowed).
func contains(got, want any) bool {
	wantObject, isObject := want.(map[string]any)
	if !isObject {
		return reflect.DeepEqual(got, want)
	}
	gotObject, ok := got.(map[string]any)
	if !ok {
		return false
	}
	for name, value := range wantObject {
		held, present := gotObject[name]
		if !present || !contains(held, value) {
			return false
		}
	}
	return true
}
