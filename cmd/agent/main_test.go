package main

import (
	"encoding/json"
	"testing"
)

func TestConfigBool(t *testing.T) {
	cases := []struct {
		name string
		val  interface{}
		def  bool
		want bool
	}{
		{"missing uses default", nil, true, true},
		{"bool true", true, false, true},
		{"bool false", false, true, false},
		{"string true", "true", false, true},
		{"string false", "false", true, false},
		{"string garbage uses default", "maybe", true, true},
		{"number 0", float64(0), true, false},
		{"number 1", float64(1), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]interface{}{}
			if tc.val != nil {
				cfg["interactive"] = tc.val
			}
			if got := configBool(cfg, "interactive", tc.def); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfigBoolNilMap(t *testing.T) {
	if !configBool(nil, "interactive", true) {
		t.Fatal("nil config should return the default")
	}
}

func TestConfigInt(t *testing.T) {
	cfg := map[string]interface{}{
		"int":       7,
		"float":     9.0,
		"jsonnum":   json.Number("12"),
		"garbage":   "lots",
		"negative":  -3,
		"nil_entry": nil,
	}
	if got := configInt(cfg, "int", 30); got != 7 {
		t.Fatalf("int: got %d", got)
	}
	if got := configInt(cfg, "float", 30); got != 9 {
		t.Fatalf("float: got %d", got)
	}
	if got := configInt(cfg, "jsonnum", 30); got != 12 {
		t.Fatalf("jsonnum: got %d", got)
	}
	if got := configInt(cfg, "garbage", 30); got != 30 {
		t.Fatalf("garbage should use default, got %d", got)
	}
	if got := configInt(cfg, "missing", 30); got != 30 {
		t.Fatalf("missing should use default, got %d", got)
	}
	if got := configInt(nil, "max_turns", 30); got != 30 {
		t.Fatalf("nil map should use default, got %d", got)
	}
}

// A persisted config that round-trips through JSON must still be understood:
// json.Unmarshal turns numbers into float64.
func TestConfigRoundTripThroughJSON(t *testing.T) {
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(`{"interactive":false,"max_turns":5,"timeout":60,"poll_interval":2}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if configBool(cfg, "interactive", true) {
		t.Fatal("interactive=false was lost")
	}
	if got := configInt(cfg, "max_turns", 30); got != 5 {
		t.Fatalf("max_turns: got %d", got)
	}
}
