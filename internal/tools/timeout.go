package tools

import (
	"encoding/json"
	"time"
)

// Timeout resolution for tool calls.
//
// Three sources are combined, in increasing priority:
//  1. DefaultToolTimeoutSeconds - hard fallback.
//  2. config["timeout"]         - the persisted agent config (DB state "config").
//  3. args["timeout"]           - per-call override supplied by the model.
//
// ask_user is special: it waits for a human, so when the model does not ask for a
// specific duration it gets a generous DefaultAskUserTimeoutSeconds instead of the
// normal tool default, and any explicit value is clamped to a sane range so a bad
// number cannot hang a heartbeat for hours (or abort the wait instantly).
//
// sleep is special too: the tool timeout must outlast the requested sleep,
// otherwise the wrapper cancels the tool before it finishes.
const (
	// DefaultToolTimeoutSeconds is used when nothing else is configured.
	DefaultToolTimeoutSeconds = 30
	// DefaultAskUserTimeoutSeconds is how long ask_user waits for a human when
	// the model does not pass an explicit timeout.
	DefaultAskUserTimeoutSeconds = 600
	// MinAskUserTimeoutSeconds / MaxAskUserTimeoutSeconds clamp an explicit
	// ask_user timeout. The schema parameter used to be silently ignored (every
	// call was forced to >= 600s).
	MinAskUserTimeoutSeconds = 30
	MaxAskUserTimeoutSeconds = 3600
)

// configTimeoutSeconds reads the default tool timeout (in seconds) from config.
// Invalid, missing or non-positive values fall back to DefaultToolTimeoutSeconds.
func configTimeoutSeconds(config map[string]interface{}) int {
	if config == nil {
		return DefaultToolTimeoutSeconds
	}
	switch v := config["timeout"].(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	}
	return DefaultToolTimeoutSeconds
}

// argInt reads an integer argument that may arrive as int or float64 (JSON).
func argInt(args map[string]interface{}, key string) (int, bool) {
	if args == nil {
		return 0, false
	}
	switch v := args[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n), true
		}
	}
	return 0, false
}

// ResolveToolTimeout returns the effective timeout for a tool call.
func ResolveToolTimeout(name string, args map[string]interface{}, config map[string]interface{}) time.Duration {
	timeout := configTimeoutSeconds(config)

	explicit, hasExplicit := argInt(args, "timeout")
	if hasExplicit && explicit > 0 {
		timeout = explicit
	} else {
		hasExplicit = false
	}

	switch name {
	case "ask_user":
		if !hasExplicit {
			timeout = DefaultAskUserTimeoutSeconds
		}
		if timeout < MinAskUserTimeoutSeconds {
			timeout = MinAskUserTimeoutSeconds
		}
		if timeout > MaxAskUserTimeoutSeconds {
			timeout = MaxAskUserTimeoutSeconds
		}
	case "sleep":
		if secs, ok := argInt(args, "seconds"); ok && secs > 0 {
			if secs+5 > timeout {
				timeout = secs + 5
			}
		}
	}

	if timeout < 1 {
		timeout = DefaultToolTimeoutSeconds
	}
	return time.Duration(timeout) * time.Second
}
