// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestSessionMessaging_HasNoCancelGraceSetting pins the config half of the
// founder's one-stop decision (2026-10-05): the agent tool's own 5 s
// cooperative-stop grace is removed, so the `session_messaging.cancel_grace`
// setting (SessionMessagingConfig.CancelGrace, its default, its resolver) no
// longer exists. There is one stop timeline -- polite immediately, forced at
// 3 s -- and nothing to tune it by.
//
// Oracle: the decision file (coordination/LANE-A-ONE-STOP-DECISION-20261005.md,
// "the agent tool's 5-second grace (defaultCancelGrace, SetCancelGrace,
// session_messaging.cancel_grace) is removed") -- not the current struct.
func TestSessionMessaging_HasNoCancelGraceSetting(t *testing.T) {
	typ := reflect.TypeOf(SessionMessagingConfig{})

	if _, found := typ.FieldByName("CancelGrace"); found {
		t.Errorf("SessionMessagingConfig still has a CancelGrace field; the cancel grace setting is removed")
	}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if strings.Split(tag, ",")[0] == "cancel_grace" {
			t.Errorf("field %s still carries the json key %q", typ.Field(i).Name, "cancel_grace")
		}
	}
	if _, found := typ.MethodByName("EffectiveCancelGrace"); found {
		t.Errorf("SessionMessagingConfig still has EffectiveCancelGrace; the resolver for the removed setting must go too")
	}

	// A fresh install's serialized defaults must not mention it either.
	raw, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatalf("marshal the default config: %v", err)
	}
	if strings.Contains(string(raw), "cancel_grace") {
		t.Errorf("the default config still serializes a cancel_grace key")
	}
}
