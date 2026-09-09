package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePythonToolMetadata(t *testing.T) {
	content := `#!/usr/bin/env python3
"""
TOOL_NAME: my_tool
DESCRIPTION: Does something useful.
PARAMETERS: {"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}
"""

def execute(args):
    return "ok"
`
	meta, err := ParsePythonToolMetadata(content)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "my_tool" {
		t.Fatalf("name: %q", meta.Name)
	}
	if meta.Description != "Does something useful." {
		t.Fatalf("description: %q", meta.Description)
	}
	if meta.Parameters["type"] != "object" {
		t.Fatalf("parameters: %#v", meta.Parameters)
	}
}

func TestParsePythonToolMetadataNoParameters(t *testing.T) {
	meta, err := ParsePythonToolMetadata("\"\"\"\nTOOL_NAME: bare\nDESCRIPTION: no params\n\"\"\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Parameters == nil || meta.Parameters["type"] != "object" {
		t.Fatalf("expected default object schema, got %#v", meta.Parameters)
	}
}

func TestParsePythonToolMetadataErrors(t *testing.T) {
	if _, err := ParsePythonToolMetadata("print('no docstring')"); err == nil {
		t.Fatal("expected error for missing docstring")
	}
	if _, err := ParsePythonToolMetadata("\"\"\"DESCRIPTION: no name\"\"\""); err == nil {
		t.Fatal("expected error for missing TOOL_NAME")
	}
	if _, err := ParsePythonToolMetadata("\"\"\"TOOL_NAME: bad\nPARAMETERS: {not json}\"\"\""); err == nil {
		t.Fatal("expected error for invalid PARAMETERS JSON")
	}
}

// The example addon shipped in the repo must actually be loadable - it silently
// failed before PR #2 because it lived in a subdirectory.
func TestShippedHelloWorldAddonParses(t *testing.T) {
	candidates := []string{
		filepath.Join("..", "..", "addons", "hello_world.py"),
		filepath.Join("addons", "hello_world.py"),
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		t.Skip("addons/hello_world.py not present in this checkout")
	}

	meta, err := LoadPythonTool(path)
	if err != nil {
		t.Fatalf("shipped addon failed to load: %v", err)
	}
	if meta.Name != "hello_world" {
		t.Fatalf("unexpected addon name: %q", meta.Name)
	}
	props, ok := meta.Parameters["properties"].(map[string]interface{})
	if !ok || props["name"] == nil {
		t.Fatalf("expected a 'name' parameter, got %#v", meta.Parameters)
	}
}
