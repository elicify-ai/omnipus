package logger

import (
	"context"
	"fmt"
	"log/slog"
)

// SlogHandler is a slog.Handler that forwards every record into pkg/logger's
// zerolog sink (console +, when logger.EnableFileLogging has been called,
// $OMNIPUS_HOME/logs/gateway.log) instead of slog's own zero-value
// stderr-only default.
//
// Why this exists: nothing in this repo ever calls slog.SetDefault in
// production code, so any bare `slog.Warn(...)`/`slog.Info(...)`/etc. call
// site — and there are >1200 of them across the codebase — silently writes
// to log/slog.Default()'s built-in stderr handler. On the documented launch
// form (`./omnipus gateway --allow-empty &`, a backgrounded process with no
// attached terminal) that output is lost: nothing redirects stderr into
// gateway.log. Installing this handler as the process-wide slog default (see
// gateway.go's RunContextWithOptions, right after logger.EnableFileLogging)
// makes every existing and future bare slog call land in the same file
// pkg/logger itself writes to, with no per-call-site changes required.
//
// Design notes:
//   - Level mapping caps at ERROR. A slog record is never promoted to
//     pkg/logger's FATAL level (which calls os.Exit(1)) — a caller reaching
//     for slog.Error (or any custom level at or above slog.LevelError) does
//     not expect an os.Exit side effect, and FATAL is reserved for pkg/logger's
//     own explicit Fatal/Fatalf callers.
//   - Structured attributes survive as fields (map[string]any), not a
//     flattened message string: slog.Attr key/value pairs, including ones
//     bound earlier via Logger.With (Handler.WithAttrs), are passed straight
//     through to logger.*CF's fields map, which zerolog serializes as
//     structured JSON/console fields exactly like every existing
//     logger.WarnCF/InfoCF/etc. call site.
//   - slog.Group (including nested groups and record-level groups from
//     Logger.WithGroup) is flattened to dot-joined keys ("group.key"), the
//     same convention slog's own built-in JSONHandler/TextHandler use for
//     nested groups. An anonymous group (empty key, e.g. slog.Group("", ...))
//     is inlined without a prefix, matching slog's own documented behavior
//     for Handler implementations.
//   - This handler does not alter pkg/logger's own output path in any way:
//     it is a new front-end that ultimately calls the same logMessage
//     function (via *CF helpers) every existing pkg/logger caller uses.
type SlogHandler struct {
	// groupPrefix is the dot-joined chain of open group names (from
	// WithGroup), applied to every attribute added after it — both ones
	// bound via a later WithAttrs call and ones present on a Record handed
	// to Handle. Empty when no group is open.
	groupPrefix string

	// attrs holds fields already bound via WithAttrs (and prefixed with
	// groupPrefix as it stood at the time of that WithAttrs call). Handle
	// copies this map before adding the record's own attributes so sibling
	// handlers created via With/WithGroup never share mutable state.
	attrs map[string]any
}

// NewSlogHandler constructs a fresh SlogHandler with no bound attributes and
// no open groups — the value to pass to slog.New before slog.SetDefault.
func NewSlogHandler() *SlogHandler {
	return &SlogHandler{}
}

// Enabled reports whether a record at the given level would actually be
// logged, mirroring pkg/logger's own currentLevelAtomic threshold (set via
// logger.SetLevel/SetLevelFromString) so slog callers are filtered exactly
// like native pkg/logger callers rather than always logging at slog's own
// default (Info) threshold.
func (h *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return slogLevelToLogLevel(level) >= GetLevel()
}

// Handle converts a slog.Record into a pkg/logger *CF call, preserving the
// message, mapped level, and every structured attribute (bound + record-own,
// groups flattened to dot-joined keys).
func (h *SlogHandler) Handle(_ context.Context, record slog.Record) error {
	level := slogLevelToLogLevel(record.Level)

	fields := make(map[string]any, len(h.attrs)+record.NumAttrs())
	for k, v := range h.attrs {
		fields[k] = v
	}
	record.Attrs(func(a slog.Attr) bool {
		flattenSlogAttr(h.groupPrefix, a, fields)
		return true
	})

	// component is intentionally "" — matching the existing InfoF/WarnF/
	// ErrorF convention (fields-only, no component tag) so bridged slog
	// output looks like any other untagged pkg/logger call. DebugCF has no
	// component-less sibling, so "" is passed explicitly for every level to
	// keep the four branches uniform.
	switch {
	case level <= DEBUG:
		DebugCF("", record.Message, fields)
	case level == INFO:
		InfoCF("", record.Message, fields)
	case level == WARN:
		WarnCF("", record.Message, fields)
	default: // ERROR and above (slog has no FATAL level; see type doc)
		ErrorCF("", record.Message, fields)
	}
	return nil
}

// WithAttrs returns a new handler with attrs bound under the receiver's
// current group prefix. The receiver is never mutated — attrs is copied so
// two handlers derived from the same parent (e.g. two different
// sub-loggers from the same slog.Logger.With call site) never alias the
// same map.
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	next := &SlogHandler{
		groupPrefix: h.groupPrefix,
		attrs:       make(map[string]any, len(h.attrs)+len(attrs)),
	}
	for k, v := range h.attrs {
		next.attrs[k] = v
	}
	for _, a := range attrs {
		flattenSlogAttr(h.groupPrefix, a, next.attrs)
	}
	return next
}

// WithGroup returns a new handler that prefixes every subsequent attribute
// (from a later WithAttrs call, or present on a Record handed to Handle)
// with name + ".". Per slog's documented Handler contract, an empty name is
// a no-op that returns the receiver unchanged.
func (h *SlogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := &SlogHandler{
		groupPrefix: h.groupPrefix + name + ".",
		attrs:       h.attrs,
	}
	return next
}

// slogLevelToLogLevel maps a slog.Level onto pkg/logger's LogLevel, capping
// at ERROR (see SlogHandler's doc comment for why FATAL is never reachable
// from a slog call). Follows slog's own convention that any level at or
// above a threshold constant is treated as that severity — the same rule
// slog.Level.String uses to render custom/offset levels (e.g. LevelWarn+2).
func slogLevelToLogLevel(level slog.Level) LogLevel {
	switch {
	case level < slog.LevelInfo:
		return DEBUG
	case level < slog.LevelWarn:
		return INFO
	case level < slog.LevelError:
		return WARN
	default:
		return ERROR
	}
}

// flattenSlogAttr adds a onto out under prefix, recursing into slog.Group
// values (dot-joining the group's own key onto prefix, or leaving prefix
// unchanged for an anonymous group per slog's inlining convention) and
// resolving slog.LogValuer values before conversion.
func flattenSlogAttr(prefix string, a slog.Attr, out map[string]any) {
	a.Value = a.Value.Resolve()

	if a.Value.Kind() == slog.KindGroup {
		groupAttrs := a.Value.Group()
		if len(groupAttrs) == 0 {
			// slog: a group with no attributes is omitted entirely, even if
			// it has a name.
			return
		}
		nestedPrefix := prefix
		if a.Key != "" {
			nestedPrefix = prefix + a.Key + "."
		}
		for _, ga := range groupAttrs {
			flattenSlogAttr(nestedPrefix, ga, out)
		}
		return
	}

	if a.Key == "" {
		// slog: a non-group Attr with an empty key is ignored (lets callers
		// conditionally omit an attribute, e.g. slog.Any("", nil)).
		return
	}

	out[prefix+a.Key] = slogValueToAny(a.Value)
}

// slogValueToAny converts a resolved, non-group slog.Value to the plain Go
// value logger.appendFields' type switch (error/string/int/int64/float64/
// bool, with an Interface fallback for everything else) already knows how
// to serialize.
func slogValueToAny(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration()
	case slog.KindTime:
		return v.Time()
	case slog.KindAny:
		return v.Any()
	default:
		return fmt.Sprintf("%v", v.Any())
	}
}
