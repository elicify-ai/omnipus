// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sysagent

import (
	"fmt"
	"sync"
	"time"
)

// rateLimitCategory groups system tools for per-category rate limiting.
// Limits per BRD Wave 5b spec §US-11.
type rateLimitCategory string

const (
	rateCategoryCreate  rateLimitCategory = "create"
	rateCategoryDelete  rateLimitCategory = "delete"
	rateCategoryConfig  rateLimitCategory = "config"
	rateCategoryList    rateLimitCategory = "list"
	rateCategoryChannel rateLimitCategory = "channel"
)

// toolCategory maps each system tool to its rate-limit category.
var toolCategory = map[string]rateLimitCategory{
	"create_agent":             rateCategoryCreate,
	"create_workspace":         rateCategoryCreate,
	"create_task_in_workspace": rateCategoryCreate,
	"create_skill":             rateCategoryCreate,
	"add_mcp_server":           rateCategoryCreate,

	"delete_agent":             rateCategoryDelete,
	"delete_workspace":         rateCategoryDelete,
	"delete_task_in_workspace": rateCategoryDelete,
	"remove_skill":             rateCategoryDelete,
	"remove_mcp_server":        rateCategoryDelete,

	"set_config":               rateCategoryConfig,
	"configure_provider":       rateCategoryConfig,
	"update_agent":             rateCategoryConfig,
	"update_workspace":         rateCategoryConfig,
	"update_task_in_workspace": rateCategoryConfig,
	"edit_skill":               rateCategoryConfig,

	"read_agent_metadata":  rateCategoryList,
	"write_agent_metadata": rateCategoryConfig,

	"list_workspaces":         rateCategoryList,
	"get_workspace":           rateCategoryList,
	"list_tasks_in_workspace": rateCategoryList,
	"list_skills":             rateCategoryList,
	"list_mcp_servers":        rateCategoryList,
	"list_providers":          rateCategoryList,
	"test_provider":           rateCategoryList,
	"list_models":             rateCategoryList,
	"get_config":              rateCategoryList,
	"run_doctor":              rateCategoryList,
	"query_cost":              rateCategoryList,
	"navigate":                rateCategoryList,

	"enable_channel":    rateCategoryChannel,
	"configure_channel": rateCategoryChannel,
	"disable_channel":   rateCategoryChannel,
	"list_channels":     rateCategoryChannel,
	"test_channel":      rateCategoryChannel,
}

// categoryLimit defines the max calls and window per category.
type categoryLimit struct {
	maxCalls int
	window   time.Duration
}

// limits per BRD Wave 5b spec §US-11.
var categoryLimits = map[rateLimitCategory]categoryLimit{
	rateCategoryCreate:  {maxCalls: 30, window: time.Minute},
	rateCategoryDelete:  {maxCalls: 10, window: time.Minute},
	rateCategoryConfig:  {maxCalls: 10, window: time.Minute},
	rateCategoryList:    {maxCalls: 60, window: time.Minute},
	rateCategoryChannel: {maxCalls: 5, window: time.Minute},
}

// RateLimitedError is returned when a tool call is rate-limited.
type RateLimitedError struct {
	Category          rateLimitCategory
	RetryAfterSeconds float64
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("RATE_LIMITED: too many %s operations, retry in %.0f seconds",
		e.Category, e.RetryAfterSeconds)
}

// slidingWindow is a simple sliding-window rate limiter for a single category.
type slidingWindow struct {
	mu     sync.Mutex
	events []time.Time
	limit  categoryLimit
}

func newSlidingWindow(limit categoryLimit) *slidingWindow {
	return &slidingWindow{
		events: make([]time.Time, 0, limit.maxCalls+1),
		limit:  limit,
	}
}

// allow returns nil if the call is permitted, or RateLimitedError.
func (w *slidingWindow) allow() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-w.limit.window)

	// Drop expired events.
	// Reuses the backing array of w.events (avoids allocation). Safe because all access is serialized under w.mu.
	kept := w.events[:0]
	for _, t := range w.events {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	w.events = kept

	if len(w.events) >= w.limit.maxCalls {
		oldest := w.events[0]
		retryAt := oldest.Add(w.limit.window)
		return &RateLimitedError{
			RetryAfterSeconds: retryAt.Sub(now).Seconds(),
		}
	}

	w.events = append(w.events, now)
	return nil
}

// SystemRateLimiter holds per-category sliding windows for system tools.
type SystemRateLimiter struct {
	windows map[rateLimitCategory]*slidingWindow
}

// NewSystemRateLimiter creates a rate limiter with default category windows.
func NewSystemRateLimiter() *SystemRateLimiter {
	rl := &SystemRateLimiter{
		windows: make(map[rateLimitCategory]*slidingWindow, len(categoryLimits)),
	}
	for cat, lim := range categoryLimits {
		rl.windows[cat] = newSlidingWindow(lim)
	}
	return rl
}

// Check returns nil if toolName is within its rate limit, RateLimitedError otherwise.
func (rl *SystemRateLimiter) Check(toolName string) error {
	cat, ok := toolCategory[toolName]
	if !ok {
		return nil // unknown tools not rate-limited
	}
	w, ok := rl.windows[cat]
	if !ok {
		return nil
	}
	if err := w.allow(); err != nil {
		if rlErr, ok := err.(*RateLimitedError); ok {
			rlErr.Category = cat
		}
		return err
	}
	return nil
}
