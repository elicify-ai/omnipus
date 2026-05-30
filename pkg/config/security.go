// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file provides:
//   - SensitiveDataCache: runtime lookup for filtering credential values out of
//     LLM responses and logs (used throughout the agent loop)
//   - SecureString / SecureStrings: typed wrappers for credential values that
//     are redacted on JSON serialization
//
// The old PicoClaw ".security.yml" credential-separation mechanism has been
// removed. Credential separation is now handled exclusively by the Omnipus
// encrypted credentials.json store (pkg/credentials) referenced via
// ModelConfig.APIKeyRef — see pkg/gateway/rest.go and pkg/sysagent/tools/deps.go
// for the resolution path. The credentials.json store uses AES-256-GCM +
// Argon2id per CLAUDE.md and BRD SEC-23.

// SensitiveDataCache holds the compiled strings.Replacer for filtering
// sensitive data out of LLM responses and logs.
//
// Instances are built fully populated and then stored atomically under
// Config.sensitiveMu; readers that obtain a non-nil cache pointer may call
// replacer without holding any lock.
type SensitiveDataCache struct {
	replacer *strings.Replacer
}

// SensitiveDataReplacer returns the strings.Replacer for filtering sensitive data.
// The replacer is built lazily on first call and rebuilt whenever
// RegisterSensitiveValues invalidates the cache.
//
// Thread-safe: safe to call concurrently with RegisterSensitiveValues.
func (sec *Config) SensitiveDataReplacer() *strings.Replacer {
	sec.sensitiveMu.RLock()
	cache := sec.sensitiveCache
	sec.sensitiveMu.RUnlock()
	if cache != nil {
		return cache.replacer
	}
	// Cache is nil — build it under the write lock.
	sec.sensitiveMu.Lock()
	defer sec.sensitiveMu.Unlock()
	if sec.sensitiveCache != nil {
		// Another goroutine already built it while we waited for the write lock.
		return sec.sensitiveCache.replacer
	}
	sec.buildAndPopulateSensitiveCache()
	return sec.sensitiveCache.replacer
}

// RegisterSensitiveValues replaces the runtime sensitive-data list with the
// supplied values and resets the compiled replacer cache so the next call to
// SensitiveDataReplacer rebuilds with the new set. Semantics are "replace not
// append" so that rotated or removed secrets are evicted on every reload; callers
// must pass the COMPLETE current set of plaintexts each time.
//
// Thread-safe: safe to call concurrently with SensitiveDataReplacer.
func (sec *Config) RegisterSensitiveValues(values []string) {
	sec.sensitiveMu.Lock()
	defer sec.sensitiveMu.Unlock()
	// Replace (not append) so stale secrets from a prior config are evicted.
	sec.registeredSensitive = append(sec.registeredSensitive[:0:0], values...)
	sec.registeredSensitive = unique(sec.registeredSensitive)
	// Invalidate the cache so the next SensitiveDataReplacer() call rebuilds.
	sec.sensitiveCache = nil
}

// buildAndPopulateSensitiveCache constructs the replacer and stores it on
// sec.sensitiveCache. Must be called with sensitiveMu write-locked.
func (sec *Config) buildAndPopulateSensitiveCache() {
	cache := &SensitiveDataCache{}

	// (a) Reflection-walked SecureString fields (kept for backward compat).
	values := sec.collectSensitiveValues()
	// (b) Runtime-registered plaintexts — read under the already-held write lock.
	values = unique(append(values, sec.registeredSensitive...))

	if len(values) == 0 {
		cache.replacer = strings.NewReplacer()
		sec.sensitiveCache = cache
		return
	}

	// Build old/new pairs for strings.Replacer.
	var pairs []string
	for _, v := range values {
		if len(v) > 3 {
			pairs = append(pairs, v, "[FILTERED]")
		}
	}
	if len(pairs) == 0 {
		cache.replacer = strings.NewReplacer()
	} else {
		cache.replacer = strings.NewReplacer(pairs...)
	}
	sec.sensitiveCache = cache
}

// collectSensitiveValues collects all sensitive strings from Config using reflection.
func (sec *Config) collectSensitiveValues() []string {
	var values []string
	collectSensitive(reflect.ValueOf(sec), &values)
	return values
}

// collectSensitive recursively traverses the value and collects SecureString/SecureStrings values.
func collectSensitive(v reflect.Value, values *[]string) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}

	t := v.Type()

	// SecureString: collect via String() method (defined on *SecureString)
	if t == reflect.TypeOf(SecureString{}) {
		var addr reflect.Value
		if v.CanAddr() {
			addr = v.Addr()
		} else {
			tmp := reflect.New(t)
			tmp.Elem().Set(v)
			addr = tmp
		}
		ss, ok := addr.Interface().(*SecureString)
		if ok && ss != nil {
			s := ss.String()
			if s != "" {
				*values = append(*values, s)
			}
		}
		return
	}

	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			collectSensitive(v.Field(i), values)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectSensitive(v.Index(i), values)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			collectSensitive(iter.Value(), values)
		}
	}
}

const (
	notHere = `"[NOT_HERE]"`
)

// SecureStrings is a slice of SecureString.
type SecureStrings []*SecureString

// Values returns the resolved values.
func (s *SecureStrings) Values() []string {
	if s == nil {
		return nil
	}
	keys := make([]string, len(*s))
	for i, k := range *s {
		keys[i] = k.String()
	}
	return unique(keys)
}

// SimpleSecureStrings builds a SecureStrings from plain string values.
// Used by tests and by internal config construction.
func SimpleSecureStrings(val ...string) SecureStrings {
	val = unique(val)
	vv := make(SecureStrings, len(val))
	for i, s := range val {
		vv[i] = NewSecureString(s)
	}
	return vv
}

// unique returns a new slice with duplicate elements removed.
func unique[T comparable](input []T) []T {
	m := make(map[T]struct{})
	var result []T
	for _, v := range input {
		if _, ok := m[v]; !ok {
			m[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}

func (s SecureStrings) MarshalJSON() ([]byte, error) {
	return []byte(notHere), nil
}

func (s *SecureStrings) UnmarshalJSON(value []byte) error {
	if string(value) == notHere {
		return nil
	}
	var v []*SecureString
	err := json.Unmarshal(value, &v)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// SecureString wraps a credential value. It is redacted on JSON output
// (MarshalJSON returns "[NOT_HERE]") so secrets never leak into API responses
// or logged config. On UnmarshalJSON it reads the plaintext value verbatim.
//
// Callers that need to store credentials separately should use the encrypted
// credentials.json store via ModelConfig.APIKeyRef instead of putting secrets
// directly in config.json.
//
//nolint:recvcheck
type SecureString struct {
	resolved string
}

// IsZero reports whether this SecureString has no value. Used by JSON
// `omitzero` to skip empty fields in serialized output.
func (s SecureString) IsZero() bool {
	return s.resolved == ""
}

// NewSecureString constructs a SecureString from a plaintext value.
func NewSecureString(value string) *SecureString {
	return &SecureString{resolved: value}
}

// String returns the plaintext value.
func (s *SecureString) String() string {
	if s == nil {
		return ""
	}
	return s.resolved
}

// Set replaces the stored value with a new plaintext.
func (s *SecureString) Set(value string) *SecureString {
	s.resolved = value
	return s
}

// MarshalJSON redacts the value in JSON output. The credential should NEVER
// be serialized to JSON — use credentials.json + APIKeyRef for persistence.
func (s SecureString) MarshalJSON() ([]byte, error) {
	return []byte(notHere), nil
}

func (s *SecureString) UnmarshalJSON(value []byte) error {
	if string(value) == notHere {
		return nil
	}
	var v string
	if err := json.Unmarshal(value, &v); err != nil {
		return err
	}
	s.resolved = v
	return nil
}

func (s SecureString) MarshalYAML() (any, error) {
	return s.resolved, nil
}

func (s *SecureString) UnmarshalYAML(value *yaml.Node) error {
	s.resolved = value.Value
	return nil
}

func (s *SecureString) UnmarshalText(text []byte) error {
	s.resolved = string(text)
	return nil
}
