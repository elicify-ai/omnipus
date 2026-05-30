//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — Prometheus-compatible metrics for the tool registry (FR-039).
//
// We do not depend on the full prometheus/client_golang library to keep the binary
// footprint minimal.  Instead we expose a /metrics endpoint with hand-formatted
// Prometheus text exposition that exactly mirrors what the spec requires.
//
// Metrics:
//   omnipus_tool_filter_total         counter   agent_type, effective_policy
//   omnipus_tool_approval_pending     gauge     (none)
//   omnipus_tool_approval_latency_seconds histogram  outcome
//   omnipus_tool_mcp_collision_total  counter   conflict_with

package gateway

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// toolMetrics holds all Prometheus-style metrics for the tool registry.
// A singleton is created at gateway init and shared across all handlers.
type toolMetrics struct {
	// omnipus_tool_filter_total: labels agent_type × effective_policy.
	filterTotalMu sync.Mutex
	filterTotal   map[string]int64 // "agent_type=X,effective_policy=Y" → count

	// omnipus_tool_mcp_collision_total: label conflict_with.
	collisionTotalMu sync.Mutex
	collisionTotal   map[string]int64 // "conflict_with=X" → count

	// omnipus_tool_approval_latency_seconds: label outcome.
	// Stored as sum + count per outcome for a simple histogram approximation.
	// Buckets: .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, +Inf.
	latencyMu      sync.Mutex
	latencyBuckets map[string]*latencyHistogram // outcome → histogram
}

var latencyBounds = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

type latencyHistogram struct {
	counts [12]atomic.Int64 // one per bucket (11 bounds + +Inf)
	sum    atomic.Int64     // stored as int64 microseconds; divide by 1e6 when reporting
	total  atomic.Int64
}

// observe records a latency sample (seconds) into the histogram.
func (h *latencyHistogram) observe(seconds float64) {
	us := int64(seconds * 1e6)
	h.sum.Add(us)
	h.total.Add(1)
	for i, bound := range latencyBounds {
		if seconds <= bound {
			h.counts[i].Add(1)
			return
		}
	}
	// +Inf bucket
	h.counts[11].Add(1)
}

// globalToolMetrics is the package-level singleton, created once at init.
var globalToolMetrics = newToolMetrics()

func newToolMetrics() *toolMetrics {
	return &toolMetrics{
		filterTotal:    make(map[string]int64),
		collisionTotal: make(map[string]int64),
		latencyBuckets: make(map[string]*latencyHistogram),
	}
}

// IncFilterTotal increments omnipus_tool_filter_total{agent_type, effective_policy}.
func (m *toolMetrics) IncFilterTotal(agentType, effectivePolicy string) {
	key := "agent_type=" + agentType + ",effective_policy=" + effectivePolicy
	m.filterTotalMu.Lock()
	m.filterTotal[key]++
	m.filterTotalMu.Unlock()
}

// IncCollisionTotal increments omnipus_tool_mcp_collision_total{conflict_with}.
func (m *toolMetrics) IncCollisionTotal(conflictWith string) {
	key := "conflict_with=" + conflictWith
	m.collisionTotalMu.Lock()
	m.collisionTotal[key]++
	m.collisionTotalMu.Unlock()
}

// ObserveApprovalLatency records an approval latency sample for omnipus_tool_approval_latency_seconds.
func (m *toolMetrics) ObserveApprovalLatency(outcome string, seconds float64) {
	m.latencyMu.Lock()
	h, ok := m.latencyBuckets[outcome]
	if !ok {
		h = &latencyHistogram{}
		m.latencyBuckets[outcome] = h
	}
	m.latencyMu.Unlock()
	h.observe(seconds)
}

// HandleMetrics exposes all tool-registry metrics in Prometheus text format.
// Registered at GET /metrics by the gateway.
func (a *restAPI) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var sb strings.Builder

	// omnipus_tool_filter_total
	sb.WriteString("# HELP omnipus_tool_filter_total Total tool filter evaluations by agent type and policy.\n")
	sb.WriteString("# TYPE omnipus_tool_filter_total counter\n")
	globalToolMetrics.filterTotalMu.Lock()
	keys := make([]string, 0, len(globalToolMetrics.filterTotal))
	for k := range globalToolMetrics.filterTotal {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		labels := prometheusLabels(k)
		fmt.Fprintf(&sb, "omnipus_tool_filter_total{%s} %d\n", labels, globalToolMetrics.filterTotal[k])
	}
	globalToolMetrics.filterTotalMu.Unlock()

	// omnipus_tool_approval_pending (gauge, driven from approvalRegV2 if present)
	sb.WriteString("# HELP omnipus_tool_approval_pending Current number of pending tool approvals.\n")
	sb.WriteString("# TYPE omnipus_tool_approval_pending gauge\n")
	var pendingVal int64
	if a.approvalReg != nil {
		pendingVal = a.approvalReg.pendingGaugeValue()
	}
	fmt.Fprintf(&sb, "omnipus_tool_approval_pending %d\n", pendingVal)

	// omnipus_tool_approval_latency_seconds
	sb.WriteString("# HELP omnipus_tool_approval_latency_seconds Tool approval latency in seconds.\n")
	sb.WriteString("# TYPE omnipus_tool_approval_latency_seconds histogram\n")
	globalToolMetrics.latencyMu.Lock()
	outcomes := make([]string, 0, len(globalToolMetrics.latencyBuckets))
	for o := range globalToolMetrics.latencyBuckets {
		outcomes = append(outcomes, o)
	}
	sort.Strings(outcomes)
	for _, outcome := range outcomes {
		h := globalToolMetrics.latencyBuckets[outcome]
		label := fmt.Sprintf(`outcome=%q`, outcome)
		cumulative := int64(0)
		for i, bound := range latencyBounds {
			cumulative += h.counts[i].Load()
			fmt.Fprintf(&sb, "omnipus_tool_approval_latency_seconds_bucket{%s,le=\"%v\"} %d\n",
				label, bound, cumulative)
		}
		cumulative += h.counts[11].Load()
		fmt.Fprintf(&sb, "omnipus_tool_approval_latency_seconds_bucket{%s,le=\"+Inf\"} %d\n", label, cumulative)
		sumSec := float64(h.sum.Load()) / 1e6
		fmt.Fprintf(&sb, "omnipus_tool_approval_latency_seconds_sum{%s} %v\n", label, formatFloat(sumSec))
		fmt.Fprintf(&sb, "omnipus_tool_approval_latency_seconds_count{%s} %d\n", label, h.total.Load())
	}
	globalToolMetrics.latencyMu.Unlock()

	// omnipus_tool_mcp_collision_total
	sb.WriteString("# HELP omnipus_tool_mcp_collision_total Total MCP tool name collisions.\n")
	sb.WriteString("# TYPE omnipus_tool_mcp_collision_total counter\n")
	globalToolMetrics.collisionTotalMu.Lock()
	ckeys := make([]string, 0, len(globalToolMetrics.collisionTotal))
	for k := range globalToolMetrics.collisionTotal {
		ckeys = append(ckeys, k)
	}
	sort.Strings(ckeys)
	for _, k := range ckeys {
		labels := prometheusLabels(k)
		fmt.Fprintf(&sb, "omnipus_tool_mcp_collision_total{%s} %d\n", labels, globalToolMetrics.collisionTotal[k])
	}
	globalToolMetrics.collisionTotalMu.Unlock()

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, sb.String())
}

// prometheusLabels converts our internal "k=v,k=v" key format to Prometheus label syntax.
// Input: "agent_type=custom,effective_policy=deny"
// Output: `agent_type="custom",effective_policy="deny"`
func prometheusLabels(s string) string {
	var out strings.Builder
	for i, part := range strings.Split(s, ",") {
		if i > 0 {
			out.WriteString(",")
		}
		if idx := strings.Index(part, "="); idx >= 0 {
			k := part[:idx]
			v := part[idx+1:]
			fmt.Fprintf(&out, `%s="%s"`, k, v)
		} else {
			out.WriteString(part)
		}
	}
	return out.String()
}

// formatFloat formats a float64 for Prometheus text without scientific notation.
func formatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	if math.IsInf(f, -1) {
		return "-Inf"
	}
	if math.IsNaN(f) {
		return "NaN"
	}
	return fmt.Sprintf("%.6f", f)
}
