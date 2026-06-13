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
	rateCategoryBackup  rateLimitCategory = "backup"
)

// toolCategory maps each system tool to its rate-limit category.
var toolCategory = map[string]rateLimitCategory{
	"system.agent.create":     rateCategoryCreate,
	"system.workspace.create": rateCategoryCreate,
	"system.task.create":      rateCategoryCreate,
	"system.skill.install":    rateCategoryCreate,
	"system.mcp.add":          rateCategoryCreate,
	"system.pin.create":       rateCategoryCreate,

	"system.agent.delete":     rateCategoryDelete,
	"system.workspace.delete": rateCategoryDelete,
	"system.task.delete":      rateCategoryDelete,
	"system.skill.remove":     rateCategoryDelete,
	"system.mcp.remove":       rateCategoryDelete,
	"system.pin.delete":       rateCategoryDelete,

	"system.config.set":         rateCategoryConfig,
	"system.provider.configure": rateCategoryConfig,
	"system.agent.update":       rateCategoryConfig,
	"system.workspace.update":   rateCategoryConfig,
	"system.task.update":        rateCategoryConfig,

	"system.agent.list":       rateCategoryList,
	"system.agent.activate":   rateCategoryList,
	"system.agent.deactivate": rateCategoryList,
	"system.workspace.list":   rateCategoryList,
	"system.task.list":        rateCategoryList,
	"system.skill.list":       rateCategoryList,
	"system.skill.search":     rateCategoryList,
	"system.mcp.list":         rateCategoryList,
	"system.provider.list":    rateCategoryList,
	"system.provider.test":    rateCategoryList,
	"system.pin.list":         rateCategoryList,
	"system.config.get":       rateCategoryList,
	"system.doctor.run":       rateCategoryList,
	"system.cost.query":       rateCategoryList,
	"system.navigate":         rateCategoryList,

	"system.channel.enable":    rateCategoryChannel,
	"system.channel.configure": rateCategoryChannel,
	"system.channel.disable":   rateCategoryChannel,
	"system.channel.list":      rateCategoryChannel,
	"system.channel.test":      rateCategoryChannel,

	"system.backup.create": rateCategoryBackup,
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
	rateCategoryBackup:  {maxCalls: 1, window: 5 * time.Minute},
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
