package tools

import (
	"encoding/json"
	"fmt"
	"os"
"regexp"
	"strings"
)

// ToolMetadata represents the parsed metadata from a Python tool file
type ToolMetadata struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// ParsePythonToolMetadata extracts tool metadata from Python docstring
func ParsePythonToolMetadata(content string) (*ToolMetadata, error) {
	// Extract docstring (first triple-quoted string)
	docstringPattern := regexp.MustCompile(`(?s)"""(.*?)"""`)
	matches := docstringPattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return nil, fmt.Errorf("no docstring found in tool file")
	}

	docstring := matches[1]
	metadata := &ToolMetadata{}

	// Extract TOOL_NAME
	namePattern := regexp.MustCompile(`TOOL_NAME:\s*(\S+)`)
	if m := namePattern.FindStringSubmatch(docstring); len(m) > 1 {
		metadata.Name = m[1]
	} else {
		return nil, fmt.Errorf("TOOL_NAME not found in docstring")
	}

	// Extract DESCRIPTION
	descPattern := regexp.MustCompile(`DESCRIPTION:\s*(.+?)(?:\n|PARAMETERS:)`)
	if m := descPattern.FindStringSubmatch(docstring); len(m) > 1 {
		metadata.Description = strings.TrimSpace(m[1])
	}

	// Extract PARAMETERS (JSON) - Greedily match from the first { to the last }
	paramPattern := regexp.MustCompile(`(?s)PARAMETERS:\s*(\{.*\})`)
	if m := paramPattern.FindStringSubmatch(docstring); len(m) > 1 {
		paramJSON := strings.TrimSpace(m[1])
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(paramJSON), &params); err != nil {
			return nil, fmt.Errorf("failed to parse PARAMETERS JSON: %v", err)
		}
		metadata.Parameters = params
	} else {
		// Default empty parameters
		metadata.Parameters = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	return metadata, nil
}

// LoadPythonTool reads a Python tool file and returns its metadata
func LoadPythonTool(path string) (*ToolMetadata, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return ParsePythonToolMetadata(string(content))
}
