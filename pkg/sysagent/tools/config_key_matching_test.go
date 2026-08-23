// Omnipus — System Agent Tool Tests: config key matching semantics
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file is deliberately an INTERNAL test (package systools): like
// config_blocklist_test.go it walks blockedConfigKeys itself, so the policy is
// verified as data and a leaf entry added later cannot silently reintroduce a
// hole at a different granularity.
//
// It pins the two properties the deny loop got wrong, both of which produced a
// working privilege escalation:
//
//  1. CASE. The table compared raw bytes while the writer under it (dotSet →
//     json.Unmarshal) matches struct fields case-insensitively, so
//     "gateway.Dev_Mode_Bypass" missed the deny entry and set
//     Gateway.DevModeBypass — unauthenticated admin access to the whole API.
//
//  2. GRANULARITY. The loop matched a key at or under a blocked key but never
//     ABOVE it, while the allow list explicitly accepts a bare section name. One
//     write with key="gateway" and an object value therefore reached every
//     blocked leaf in that section at once, dev_mode_bypass included — gateway
//     authentication off for the whole API, live, with no restart.
package systools

import (
	"strings"
	"testing"
)

// configKeyCaseVariants returns spellings of key that name the SAME config
// fields as far as encoding/json is concerned, and therefore must be refused
// identically. If the comparison ever reverts to a byte-exact one, every
// assertion driven by this helper fails.
func configKeyCaseVariants(key string) []string {
	segments := strings.Split(key, ".")

	titleLast := append([]string(nil), segments...)
	titleLast[len(titleLast)-1] = titleWords(titleLast[len(titleLast)-1])

	upperLast := append([]string(nil), segments...)
	upperLast[len(upperLast)-1] = strings.ToUpper(upperLast[len(upperLast)-1])

	titleFirst := append([]string(nil), segments...)
	titleFirst[0] = titleWords(titleFirst[0])

	return []string{
		strings.ToUpper(key),
		strings.Join(titleLast, "."),
		strings.Join(upperLast, "."),
		strings.Join(titleFirst, "."),
	}
}

// titleWords upper-cases the first letter of each underscore-separated word,
// producing the "Dev_Mode_Bypass" spelling that was the demonstrated payload.
func titleWords(segment string) string {
	words := strings.Split(segment, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, "_")
}

// TestBlockedConfigKeys_ReadPolicyIsDecidedForEveryEntry forces the read
// decision to be made in writing for every entry. get_config previously had no
// deny list at all; the fix routes reads through the same table, and this test
// is what stops a new entry from defaulting to "readable" simply because
// whoever added it thought about writes only.
func TestBlockedConfigKeys_ReadPolicyIsDecidedForEveryEntry(t *testing.T) {
	for i, b := range blockedConfigKeys {
		hasDeny := strings.TrimSpace(b.ReadReason) != ""
		hasAllow := strings.TrimSpace(b.ReadOKReason) != ""
		switch {
		case !hasDeny && !hasAllow:
			t.Errorf("blockedConfigKeys[%d] (%q): neither ReadReason nor ReadOKReason set — "+
				"decide whether get_config may disclose this key and say why", i, b.Key)
		case hasDeny && hasAllow:
			t.Errorf("blockedConfigKeys[%d] (%q): both ReadReason and ReadOKReason set — "+
				"a key is either readable or it is not", i, b.Key)
		}
	}
}

// TestValidateConfigKey_CaseVariantsOfEveryBlockedKeyRefused is the regression
// guard for finding 1. It walks the whole table rather than a hand-written list
// of keys, so the property holds for entries added later too.
func TestValidateConfigKey_CaseVariantsOfEveryBlockedKeyRefused(t *testing.T) {
	for _, b := range blockedConfigKeys {
		for _, base := range []string{b.Key, b.Key + ".some_child", b.Key + ".some_child.grandchild"} {
			for _, variant := range configKeyCaseVariants(base) {
				if err := validateConfigKey(nil, variant); err == nil {
					t.Errorf("validateConfigKey(%q) = nil, want refusal — a case variant of the "+
						"blocked key %q reaches the same field, because dotSet writes through "+
						"json.Unmarshal, which matches field names case-insensitively",
						variant, b.Key)
				}
			}
		}
	}
}

// TestValidateConfigKey_AncestorOfEveryBlockedKeyRefused is the regression
// guard for finding 4 (the section write). Every strict ancestor of every
// blocked key must be refused, in every case spelling: writing an object at an
// ancestor path merges it into the live struct and reaches the blocked leaf.
func TestValidateConfigKey_AncestorOfEveryBlockedKeyRefused(t *testing.T) {
	for _, b := range blockedConfigKeys {
		segments := strings.Split(b.Key, ".")
		for i := 1; i < len(segments); i++ {
			ancestor := strings.Join(segments[:i], ".")
			candidates := append([]string{ancestor}, configKeyCaseVariants(ancestor)...)
			for _, candidate := range candidates {
				err := validateConfigKey(nil, candidate)
				if err == nil {
					t.Errorf("validateConfigKey(%q) = nil, want refusal — writing that section "+
						"replaces the blocked key %q wholesale", candidate, b.Key)
					continue
				}
				// The message names the FIRST blocked descendant it matched,
				// not all of them — but it must name one, so the caller is
				// told what is under the section rather than just "no".
				if !strings.Contains(err.Error(), "replaces") {
					t.Errorf("validateConfigKey(%q) error %q does not name the blocked descendant "+
						"it hit (at least %q is under it)", candidate, err.Error(), b.Key)
				}
			}
		}
	}
}

// TestValidateConfigKey_MalformedKeysStillRefused pins the near-miss spellings
// that were already refused before this change. Case-folding the comparison
// widens what is DENIED, and these must not become accepted as a side effect —
// in particular the Cyrillic homoglyph, which folds to nothing ASCII and must
// keep failing the allow list.
func TestValidateConfigKey_MalformedKeysStillRefused(t *testing.T) {
	for _, key := range []string{
		"ѕandbox.mode", // Cyrillic dze homoglyph for "s"
		" sandbox.mode",
		"sandbox .mode",
		"sandbox..mode",
		"sandbox[0]",
		"Sandbox.mode",
		"SANDBOX.mode",
		"nonexistent.section",
		"",
	} {
		if err := validateConfigKey(nil, key); err == nil {
			t.Errorf("validateConfigKey(%q) = nil, want refusal", key)
		}
	}
}

// TestValidateConfigKey_LegitimateKeysStillWritable is the positive control for
// both new rules. Without it the assertions above would pass against a build
// that refuses everything — a broken tool, not a secure one. Each key here is
// canonical, unblocked, and NOT an ancestor of any blocked key.
func TestValidateConfigKey_LegitimateKeysStillWritable(t *testing.T) {
	for _, key := range []string{
		"gateway.port",
		"gateway.host",
		"agents.defaults",
		"agents.defaults.default_model.model",
		"tools.read_file.max_read_file_size",
		"tools.skills.max_concurrent_searches",
		"channels.telegram.enabled",
	} {
		if err := validateConfigKey(nil, key); err != nil {
			t.Errorf("validateConfigKey(%q) = %v, want nil — this key is legitimately writable",
				key, err)
		}
	}
}

// TestValidateConfigReadKey_EveryUndisclosableKeyRefused walks the read side of
// the same table: at the key, under it, and in every case spelling.
func TestValidateConfigReadKey_EveryUndisclosableKeyRefused(t *testing.T) {
	for _, b := range blockedConfigKeys {
		if b.ReadReason == "" {
			continue
		}
		for _, base := range []string{b.Key, b.Key + ".some_child", b.Key + "[0]"} {
			candidates := append([]string{base}, configKeyCaseVariants(base)...)
			for _, candidate := range candidates {
				err := validateConfigReadKey(nil, candidate)
				if err == nil {
					t.Errorf("validateConfigReadKey(%q) = nil, want refusal (undisclosable via %q)",
						candidate, b.Key)
					continue
				}
				if !strings.Contains(err.Error(), b.ReadReason) {
					t.Errorf("validateConfigReadKey(%q) error %q does not carry the read reason for %q",
						candidate, err.Error(), b.Key)
				}
			}
		}
	}
}

// TestValidateConfigReadKey_CarveOutsStayReadable is the positive control for
// the read policy: the keys the table deliberately keeps readable must stay
// readable, otherwise "block reads too" would quietly become "block reads",
// breaking install_skill (which needs the registry list) and preview links
// (which need the canonical origin).
func TestValidateConfigReadKey_CarveOutsStayReadable(t *testing.T) {
	carveOuts := 0
	for _, b := range blockedConfigKeys {
		if b.ReadOKReason == "" {
			continue
		}
		carveOuts++
		for _, candidate := range []string{b.Key, b.Key + ".child"} {
			if err := validateConfigReadKey(nil, candidate); err != nil {
				t.Errorf("validateConfigReadKey(%q) = %v, want nil — the table marks it readable: %s",
					candidate, err, b.ReadOKReason)
			}
		}
	}
	if carveOuts == 0 {
		t.Error("no ReadOKReason entries remain — the read policy has collapsed into " +
			"\"refuse everything the write policy refuses\", which is not the decision recorded " +
			"in the table")
	}
}

// TestValidateConfigReadKey_OrdinaryKeysStillReadable is the second positive
// control: keys that appear nowhere in the table must read freely.
func TestValidateConfigReadKey_OrdinaryKeysStillReadable(t *testing.T) {
	for _, key := range []string{
		"gateway", // an ancestor read: served, with blocked descendants redacted
		"gateway.port",
		"agents.defaults.default_model.model",
		"tools.read_file.max_read_file_size",
	} {
		if err := validateConfigReadKey(nil, key); err != nil {
			t.Errorf("validateConfigReadKey(%q) = %v, want nil", key, err)
		}
	}
}

// TestRedactConfigValue_BlockedDescendantsAndSecretsRemoved proves the
// redaction that makes an ancestor read safe: get_config "gateway" must not
// return what get_config "gateway.users" refuses.
func TestRedactConfigValue_BlockedDescendantsAndSecretsRemoved(t *testing.T) {
	section := map[string]any{
		"port":            float64(5000),
		"host":            "127.0.0.1",
		"dev_mode_bypass": true,
		"trust_xff":       true,
		"token":           "raw-bearer-token",
		"users":           []any{map[string]any{"username": "admin", "password_hash": "$2a$10$deadbeef"}},
		"nested": map[string]any{
			"api_key_ref": "ANTHROPIC_API_KEY",
			"harmless":    "value",
		},
	}

	got, ok := redactConfigValue("gateway", section).(map[string]any)
	if !ok {
		t.Fatalf("redactConfigValue returned %T, want map[string]any", got)
	}
	// Credentials only. dev_mode_bypass and trust_xff used to be redacted here;
	// the operator decided enforcement configuration is readable by an agent
	// granted the config tool, so they now come back with their real values
	// while the credential fields do not.
	for _, blocked := range []string{"token", "users"} {
		if got[blocked] != redactedConfigValue {
			t.Errorf("gateway.%s = %v, want %q", blocked, got[blocked], redactedConfigValue)
		}
	}
	for _, readable := range []string{"dev_mode_bypass", "trust_xff"} {
		if got[readable] == redactedConfigValue {
			t.Errorf("gateway.%s was redacted — it is enforcement configuration, not a "+
				"credential, and an agent that can see it can explain a refusal instead "+
				"of failing opaquely", readable)
		}
	}
	if got["port"] != float64(5000) || got["host"] != "127.0.0.1" {
		t.Errorf("redaction ate the ordinary fields: port=%v host=%v", got["port"], got["host"])
	}
	nested, _ := got["nested"].(map[string]any)
	if nested["api_key_ref"] != redactedConfigValue {
		t.Errorf("nested credential-bearing name survived: %v", nested["api_key_ref"])
	}
	if nested["harmless"] != "value" {
		t.Errorf("nested harmless field was redacted: %v", nested["harmless"])
	}
}
