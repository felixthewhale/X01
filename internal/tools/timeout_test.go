package tools

import (
	"encoding/json"
	"testing"
	"time"
)

func TestResolveToolTimeoutDefaults(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]interface{}
		want   time.Duration
	}{
		{"nil config", nil, 30 * time.Second},
		{"empty config", map[string]interface{}{}, 30 * time.Second},
		{"int config", map[string]interface{}{"timeout": 45}, 45 * time.Second},
		{"float config", map[string]interface{}{"timeout": 45.0}, 45 * time.Second},
		{"json.Number config", map[string]interface{}{"timeout": json.Number("90")}, 90 * time.Second},
		{"zero config", map[string]interface{}{"timeout": 0}, 30 * time.Second},
		{"negative config", map[string]interface{}{"timeout": -10}, 30 * time.Second},
		{"garbage config", map[string]interface{}{"timeout": "soon"}, 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveToolTimeout("docker_shell", nil, tc.config); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveToolTimeoutPerCallOverride(t *testing.T) {
	config := map[string]interface{}{"timeout": 45}

	if got := ResolveToolTimeout("docker_shell", map[string]interface{}{"timeout": 120}, config); got != 120*time.Second {
		t.Fatalf("int override: got %v", got)
	}
	if got := ResolveToolTimeout("docker_shell", map[string]interface{}{"timeout": 12.0}, config); got != 12*time.Second {
		t.Fatalf("float override: got %v", got)
	}
	// Invalid overrides fall back to the configured default.
	for _, bad := range []interface{}{0, -5, "abc", nil} {
		if got := ResolveToolTimeout("docker_shell", map[string]interface{}{"timeout": bad}, config); got != 45*time.Second {
			t.Fatalf("invalid override %v: got %v, want 45s", bad, got)
		}
	}
}

func TestResolveToolTimeoutAskUser(t *testing.T) {
	cases := []struct {
		name   string
		args   map[string]interface{}
		config map[string]interface{}
		want   time.Duration
	}{
		{"no arg uses generous default", nil, map[string]interface{}{"timeout": 45}, 600 * time.Second},
		{"explicit respected", map[string]interface{}{"timeout": 900}, nil, 900 * time.Second},
		{"short explicit clamped up", map[string]interface{}{"timeout": 5}, nil, 30 * time.Second},
		{"float explicit respected", map[string]interface{}{"timeout": 120.0}, nil, 120 * time.Second},
		{"huge explicit clamped down", map[string]interface{}{"timeout": 99999}, nil, 3600 * time.Second},
		{"zero falls back to ask_user default", map[string]interface{}{"timeout": 0}, nil, 600 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveToolTimeout("ask_user", tc.args, tc.config); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveToolTimeoutSleepOutlastsRequest(t *testing.T) {
	config := map[string]interface{}{"timeout": 30}

	if got := ResolveToolTimeout("sleep", map[string]interface{}{"seconds": 700}, config); got != 705*time.Second {
		t.Fatalf("sleep: got %v, want 705s", got)
	}
	// Even an explicit small timeout must not cancel the sleep early.
	args := map[string]interface{}{"seconds": 700, "timeout": 10}
	if got := ResolveToolTimeout("sleep", args, config); got != 705*time.Second {
		t.Fatalf("sleep with small override: got %v, want 705s", got)
	}
	// Short sleeps keep the normal default.
	if got := ResolveToolTimeout("sleep", map[string]interface{}{"seconds": 3}, config); got != 30*time.Second {
		t.Fatalf("short sleep: got %v, want 30s", got)
	}
}
