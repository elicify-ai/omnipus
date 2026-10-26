// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestWithConfig_UnwiredMutateConfigRefusesNotPanics: a Deps whose config
// mutator is not wired must refuse the write with a reason, never panic
// (a tool must not crash the turn; SaveConfigLocked already refuses the
// same way). Found driving configure_channel through the loop.
func TestWithConfig_UnwiredMutateConfigRefusesNotPanics(t *testing.T) {
	d := &Deps{SaveConfigLocked: func(*config.Config) error { return nil }}
	called := false
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("WithConfig panicked with an unwired MutateConfig: %v", r)
			}
		}()
		err = d.WithConfig(func(*config.Config) error { called = true; return nil })
	}()
	if err == nil {
		t.Fatal("WithConfig must refuse when MutateConfig is not wired")
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("refusal must name the cause; got %q", err)
	}
	if called {
		t.Fatal("the mutation must not run when the config mutator is not wired")
	}
}

// TestConfigureChannel_UnwiredConfigMutatorIsAnErrorResult drives the tool
// that crashed: configure_channel must return an error result, not panic.
func TestConfigureChannel_UnwiredConfigMutatorIsAnErrorResult(t *testing.T) {
	cfg := &config.Config{}
	d := &Deps{GetCfg: func() *config.Config { return cfg }}
	tool := NewChannelConfigureTool(d)
	res := tool.Execute(context.Background(), map[string]any{"id": "telegram", "expected_revision": "absent"})
	if res == nil || !res.IsError {
		t.Fatalf("configure_channel must refuse with an error result; got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "not wired") {
		t.Fatalf("the refusal must name the unwired dependency; got %q", res.ForLLM)
	}
}
