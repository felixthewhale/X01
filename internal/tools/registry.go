package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"X01/internal/core"
	"X01/internal/db"
	"X01/internal/logger"
	"X01/internal/server"
)

// stdinLines is the single delivery point for terminal input. One goroutine owns
// os.Stdin for the lifetime of the process (see startStdinPump); AskUser only
// reads from this channel. Previously every AskUser call spawned its own scanner
// goroutine, which leaked on the web/timeout paths and let stale readers steal or
// split lines typed by the human.
var (
	stdinMu       sync.Mutex
	stdinLines    chan string
	stdinPumpOnce sync.Once
)

// startStdinPump starts the one and only stdin reader for the process. The
// channel is buffered(1) so a line typed just before AskUser is called is still
// delivered; the pump goroutine blocks harmlessly while nobody is reading.
func startStdinPump() {
	stdinPumpOnce.Do(func() {
		lines := make(chan string, 1)

		stdinMu.Lock()
		stdinLines = lines
		stdinMu.Unlock()

		go func(ch chan string) {
			scanner := bufio.NewScanner(os.Stdin)
			// Allow long pasted lines instead of silently failing at 64 KiB.
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				ch <- scanner.Text()
			}
			close(ch)
		}(lines)
	})
}

// stdinChannel returns the channel owned by the stdin pump, if it has started.
func stdinChannel() chan string {
	stdinMu.Lock()
	defer stdinMu.Unlock()
	return stdinLines
}

func AskUser(ctx context.Context, args map[string]interface{}) string {
	question, _ := args["question"].(string)
	fmt.Printf("\n========================================\n")
	fmt.Printf("QUESTION FROM X01: %s\n", question)
	fmt.Printf("========================================\n")
	fmt.Print("You: ")

	startStdinPump()

	// Request web reply
	server.SetActivity("Awaiting Authorization (Human Proxy Needed)")
	webReplyChan := server.RequestReply(question)
	defer server.ClearRequest()

	stdinCh := stdinChannel()
	for {
		select {
		case result, ok := <-stdinCh:
			if !ok {
				// stdin is exhausted or unavailable (e.g. no TTY): stop waiting on
				// the terminal and keep waiting for a web reply or the deadline.
				stdinCh = nil
				continue
			}
			server.SetActivity("Engagement Received (Terminal)")
			return result
		case result, ok := <-webReplyChan:
			if !ok {
				return "Timed out waiting for user response"
			}
			server.SetActivity("Engagement Received (Web)")
			logger.LogSuccess("Received web reply: %s", result)
			return result
		case <-ctx.Done():
			fmt.Printf("\n[Timeout/Cancelled - aborting wait for response]\n")
			return "Timed out waiting for user response"
		}
	}
}

const ContainerName = "x01-sandbox"

func ensureContainer(image string) error {
	// 1. Create local sandbox directory if it doesn't exist
	sandboxPath := "sandbox"
	if _, err := os.Stat(sandboxPath); os.IsNotExist(err) {
		logger.LogInfo("Creating local sandbox directory: %s", sandboxPath)
		if err := os.MkdirAll(sandboxPath, 0755); err != nil {
			return fmt.Errorf("failed to create sandbox directory: %v", err)
		}
	}

	// 2. Check if container exists and is running
	checkCmd := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", ContainerName)
	out, err := checkCmd.Output()
	isRunning := (err == nil && string(out) == "true\n")

	if !isRunning {
		// Try to start it if it exists but is stopped
		logger.LogInfo("Starting sandbox container: %s", ContainerName)
		startExistingCmd := exec.Command("docker", "start", ContainerName)
		if err := startExistingCmd.Run(); err != nil {
			// Doesn't exist or failed to start, so run fresh
			logger.LogInfo("Creating new persistent sandbox container: %s", image)
			cwd, _ := os.Getwd()
			// Use absolute path for Windows volume mounting
			absSandbox := cwd + "\\" + sandboxPath
			
			runCmd := exec.Command("docker", "run", "-d", 
				"--name", ContainerName, 
				"-v", absSandbox+":/workspace",
				"-w", "/workspace",
				image, "tail", "-f", "/dev/null")
			if err := runCmd.Run(); err != nil {
				return fmt.Errorf("failed to run docker container: %v", err)
			}
		}
	}

	// 3. Warmup check (ensure curl/wget are present)
	warmupCheck := exec.Command("docker", "exec", ContainerName, "which", "curl")
	if err := warmupCheck.Run(); err != nil {
		logger.LogInfo("Warming up sandbox: installing curl, wget, python3, and build-essential...")
		installCmd := exec.Command("docker", "exec", ContainerName, "sh", "-c", 
			"apt-get update && apt-get install -y curl wget python3 python3-pip build-essential")
		if err := installCmd.Run(); err != nil {
			logger.LogWarning("Sandbox warmup failed: %v", err)
		} else {
			logger.LogSuccess("Sandbox warmup complete.")
		}
	}

	return nil
}

func DockerShell(ctx context.Context, args map[string]interface{}) string {
	command, _ := args["command"].(string)
	image, ok := args["image"].(string)
	if !ok || image == "" {
		image = "debian:stable-slim"
	}

	if err := ensureContainer(image); err != nil {
		return fmt.Sprintf("Error starting sandbox: %v", err)
	}

	logger.LogTool("docker_shell", "Exec: %s", command)
	cmd := exec.CommandContext(ctx, "docker", "exec", ContainerName, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "Error: Command timed out"
		}
		logger.LogError("Tool failed: %v", err)
		return fmt.Sprintf("Error: %v\nOutput: %s", err, string(out))
	}
	return string(out)
}

func Memorize(ctx context.Context, args map[string]interface{}) string {
	content, _ := args["content"].(string)
	id, err := db.AddMemory(content)
	if err != nil {
		logger.LogError("Failed to add memory: %v", err)
		return fmt.Sprintf("Error: %v", err)
	}
	logger.LogSuccess("Memory added: %d", id)
	return fmt.Sprintf("Memory stored with ID: %d. You can use this ID to delete it later.", id)
}

func ForgetMemory(ctx context.Context, args map[string]interface{}) string {
	idVal, ok := args["id"]
	if !ok {
		return "Error: memory ID missing"
	}

	var id int64
	switch v := idVal.(type) {
	case float64:
		id = int64(v)
	case int:
		id = int64(v)
	case int64:
		id = v
	default:
		return fmt.Sprintf("Error: invalid ID type %T", v)
	}

	if err := db.DeleteMemory(id); err != nil {
		logger.LogError("Failed to delete memory: %v", err)
		return fmt.Sprintf("Error: %v", err)
	}
	logger.LogSuccess("Memory deleted (ID: %d)", id)
	return "Memory forgotten."
}

func Think(ctx context.Context, args map[string]interface{}) string {
	text, _ := args["text"].(string)
	logger.LogThink("%s", text)
	return text
}

func Sleep(ctx context.Context, args map[string]interface{}) string {
	secondsVal, _ := args["seconds"]
	allowWakeup, ok := args["allow_wakeup"].(bool)
	if !ok {
		allowWakeup = true // Default to true
	}

	seconds := 0
	switch v := secondsVal.(type) {
	case float64:
		seconds = int(v)
	case int:
		seconds = v
	}

	if seconds <= 0 {
		return "Invalid sleep duration"
	}

	logger.LogInfo("Agent entering hibernation for %d seconds (allow_wakeup: %v)", seconds, allowWakeup)
	server.SetActivity(fmt.Sprintf("Hibernating (%ds)", seconds))

	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()

	if allowWakeup {
		select {
		case <-timer.C:
			server.SetActivity("Waking up from scheduled sleep")
			return fmt.Sprintf("Slept for %d seconds as requested.", seconds)
		case <-server.WakeupChan:
			server.SetActivity("Wakeup event detected!")
			logger.LogSuccess("Sleep interrupted by external wakeup signal.")
			return "Sleep interrupted by user message."
		case <-ctx.Done():
			return "Sleep cancelled."
		}
	} else {
		select {
		case <-timer.C:
			return fmt.Sprintf("Slept for %d seconds as requested.", seconds)
		case <-ctx.Done():
			return "Sleep cancelled."
		}
	}
}

func UpdateState(ctx context.Context, args map[string]interface{}) string {
	newText, ok := args["new_text"].(string)
	if !ok {
		return "Error: new_text is required"
	}
	err := db.SetState("prime_context", newText)
	if err != nil {
		logger.LogError("UpdateState failed: %v", err)
		return fmt.Sprintf("Error saving state: %v", err)
	}
	logger.LogSuccess("State updated via update_state")
	return "State updated successfully."
}

// CustomTool dispatches calls to dynamic addons
func CustomTool(ctx context.Context, args map[string]interface{}) string {
	name, _ := args["name"].(string)
	params, _ := args["parameters"].(map[string]interface{})

	logger.LogTool("custom_tool", "Dispatching to: %s", name)

	// Look up the addon
	var target *Addon
	for _, addon := range loadedAddons {
		if addon.Name == name {
			target = &addon
			break
		}
	}

	if target == nil {
		return fmt.Sprintf("Error: Custom tool '%s' not found.", name)
	}

	// Reuse existing runner logic
	runner := createAddonRunner(*target)
	return runner(ctx, params)
}

func ReplaceState(ctx context.Context, args map[string]interface{}) string {
	oldText, ok1 := args["old_text"].(string)
	newText, ok2 := args["new_text"].(string)
	if !ok1 || !ok2 {
		return "Error: old_text and new_text are required"
	}

	current, err := db.GetState("prime_context")
	if err != nil {
		return fmt.Sprintf("Error fetching current state: %v", err)
	}

	if !strings.Contains(current, oldText) {
		return "Error: old_text not found in current Prime Context. Match must be exact."
	}

	updated := strings.Replace(current, oldText, newText, 1)
	err = db.SetState("prime_context", updated)
	if err != nil {
		logger.LogError("ReplaceState failed: %v", err)
		return fmt.Sprintf("Error saving updated state: %v", err)
	}

	logger.LogSuccess("State updated via replace_state")
	return "State replaced successfully."
}

func SearchHistory(ctx context.Context, args map[string]interface{}) string {
	query, _ := args["query"].(string)
	limitVal, ok := args["limit"]
	limit := 10
	if ok {
		if v, ok := limitVal.(float64); ok {
			limit = int(v)
		} else if v, ok := limitVal.(int); ok {
			limit = v
		}
	}

	results, err := db.SearchMessages(query, limit)
	if err != nil {
		logger.LogError("SearchHistory failed: %v", err)
		return fmt.Sprintf("Error searching history: %v", err)
	}

	if len(results) == 0 {
		return "No matching messages found in history."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matches in history:\n\n", len(results)))
	for _, m := range results {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", m.Timestamp, m.Role, m.Content))
		if m.Reasoning != "" {
			sb.WriteString(fmt.Sprintf("(Reasoning: %s)\n", m.Reasoning))
		}
		sb.WriteString("---\n")
	}

	return sb.String()
}

func CheckMessages(ctx context.Context, args map[string]interface{}) string {
	messages, err := db.FetchAndClearPending()
	if err != nil {
		logger.LogError("CheckMessages failed: %v", err)
		return fmt.Sprintf("Error checking messages: %v", err)
	}

	if len(messages) == 0 {
		return "No pending messages found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You have %d new messages from the human:\n\n", len(messages)))
	for i, msg := range messages {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
		// Log them to the main history too so the agent remembers reading them
		db.SaveMessages([]db.Message{
			{Role: "user", Content: msg},
		})
	}
	
	logger.LogSuccess("Agent checked %d pending messages", len(messages))
	return sb.String()
}

// Addon represents a dynamically loaded custom tool
type Addon struct {
	Name       string
	Schema     interface{}
	ScriptPath string
}

var loadedAddons []Addon

// LoadAddons scans addon directories for Python tools.
// - sandbox/addons: runtime / user-defined tools (mounted into the Docker sandbox)
// - addons:         repository-shipped example tools (staged into sandbox/addons)
func LoadAddons() {
	loadedAddons = []Addon{}
	roots := []string{"sandbox/addons", "addons"}

	for _, addonsRoot := range roots {
		// Ensure addons directory exists
		if _, err := os.Stat(addonsRoot); os.IsNotExist(err) {
			logger.LogInfo("Creating addons directory: %s", addonsRoot)
			os.MkdirAll(addonsRoot, 0755)
			continue
		}

		entries, err := os.ReadDir(addonsRoot)
		if err != nil {
			logger.LogError("Failed to read addons directory: %v", err)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".py") {
				continue
			}

			srcPath := filepath.Join(addonsRoot, entry.Name())

			// Parse tool metadata from Python file
			metadata, err := LoadPythonTool(srcPath)
			if err != nil {
				logger.LogWarning("Failed to load tool %s: %v", entry.Name(), err)
				continue
			}

			// Skip tools already loaded (sandbox/addons wins over repo addons)
			alreadyLoaded := false
			for _, addon := range loadedAddons {
				if addon.Name == metadata.Name {
					alreadyLoaded = true
					break
				}
			}
			if alreadyLoaded {
				logger.LogInfo("Skipping duplicate addon %s (%s)", metadata.Name, entry.Name())
				continue
			}

			toolPath := srcPath
			if addonsRoot == "addons" {
				// Stage repo-shipped addons into the sandbox bind-mount
				// (host ./sandbox -> container /workspace) so Docker can run them.
				dstPath := filepath.Join("sandbox/addons", entry.Name())
				content, rerr := os.ReadFile(srcPath)
				if rerr != nil {
					logger.LogWarning("Failed to read addon %s: %v", entry.Name(), rerr)
					continue
				}
				if err := os.WriteFile(dstPath, content, 0644); err != nil {
					logger.LogWarning("Failed to stage addon %s: %v", entry.Name(), err)
					continue
				}
				toolPath = dstPath
			}

			// Build OpenAI function schema
			schema := map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        metadata.Name,
					"description": metadata.Description,
					"parameters":  metadata.Parameters,
				},
			}

			loadedAddons = append(loadedAddons, Addon{
				Name:       metadata.Name,
				Schema:     schema,
				ScriptPath: toolPath,
			})
			logger.LogSuccess("Loaded dynamic addon: %s (%s)", metadata.Name, entry.Name())
		}
	}
}

// createAddonRunner creates a function that executes a Python addon in Docker
func createAddonRunner(addon Addon) core.ToolFunc {
	return func(ctx context.Context, args map[string]interface{}) string {
		logger.LogTool(addon.Name, "Executing addon in Docker: %s", addon.ScriptPath)

		// Ensure container is running
		if err := ensureContainer("debian:stable-slim"); err != nil {
			return fmt.Sprintf("Error: Failed to start sandbox: %v", err)
		}

		argData, _ := json.Marshal(args)

		// Execute in Docker - path is relative to /workspace
		// sandbox/addons/tool.py -> /workspace/addons/tool.py
		dockerPath := strings.Replace(addon.ScriptPath, "sandbox/", "/workspace/", 1)
		dockerPath = strings.Replace(dockerPath, "\\", "/", -1)

		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", ContainerName, "python3", dockerPath)
		cmd.Stdin = strings.NewReader(string(argData))
		out, err := cmd.CombinedOutput()

		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return "Error: Addon timed out"
			}
			return fmt.Sprintf("Error: %v\nOutput: %s", err, string(out))
		}

		return string(out)
	}
}

// sanitizeSchema recursively ensures all "type" fields are lowercase as required by Gemini
func sanitizeSchema(schema interface{}) interface{} {
	switch v := schema.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{})
		for key, val := range v {
			if key == "type" {
				if str, ok := val.(string); ok {
					newMap[key] = strings.ToLower(str)
					continue
				}
			}
			newMap[key] = sanitizeSchema(val)
		}
		return newMap
	case []interface{}:
		newSlice := make([]interface{}, len(v))
		for i, val := range v {
			newSlice[i] = sanitizeSchema(val)
		}
		return newSlice
	default:
		return v
	}
}

// DefineTool creates a new custom tool at runtime
func DefineTool(ctx context.Context, args map[string]interface{}) string {
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	code, _ := args["code"].(string)

	if name == "" || code == "" {
		return "Error: 'name' and 'code' are required"
	}

	// Parse and sanitize parameters
	parameters := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
	if params, ok := args["parameters"].(map[string]interface{}); ok {
		// Automatically fix schema types (OBJECT -> object)
		parameters = sanitizeSchema(params).(map[string]interface{})
	}

	// Build Python tool file
	paramsJSON, _ := json.MarshalIndent(parameters, "", "    ")

	var toolBody string
	if strings.Contains(code, "def execute(args):") {
		// Code already contains the entry point
		toolBody = code
	} else {
		// Wrap in function
		toolBody = fmt.Sprintf("def execute(args):\n    \"\"\"Main entry point for the tool.\"\"\"\n%s", indentCode(code, "    "))
	}

	toolContent := fmt.Sprintf(`#!/usr/bin/env python3
"""
TOOL_NAME: %s
DESCRIPTION: %s
PARAMETERS: %s
"""

import sys
import json

%s

if __name__ == "__main__":
    # Read args from stdin
    input_data = sys.stdin.read()
    args = json.loads(input_data) if input_data else {}
    
    # Execute and print result
    try:
        result = execute(args)
        if result is not None:
            print(result)
    except Exception as e:
        print(f"Execution Error: {str(e)}")
`, name, description, string(paramsJSON), toolBody)

	// Ensure addons directory exists
	os.MkdirAll("sandbox/addons", 0755)

	// Write to sandbox/addons/name.py
	toolPath := filepath.Join("sandbox/addons", name+".py")
	if err := os.WriteFile(toolPath, []byte(toolContent), 0644); err != nil {
		return fmt.Sprintf("Error writing tool file: %v", err)
	}

	// Auto-reload
	LoadAddons()

	logger.LogSuccess("Tool '%s' created and registered at %s", name, toolPath)
	return fmt.Sprintf("Tool '%s' created successfully! It's now available for use.", name)
}

// Helper to indent code blocks
func indentCode(code string, indent string) string {
	lines := strings.Split(code, "\n")
	var indented []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			indented = append(indented, indent+line)
		} else {
			indented = append(indented, "")
		}
	}
	return strings.Join(indented, "\n")
}

//ReloadAddons refreshes loaded addons from disk
func ReloadAddons(ctx context.Context, args map[string]interface{}) string {
	LoadAddons()
	return "Addons reloaded successfully. Your tool schema has been updated."
}

func GetToolRegistry() map[string]core.ToolFunc {
	registry := map[string]core.ToolFunc{
		"ask_user":       AskUser,
		"docker_shell":   DockerShell,
		"think":          Think,
		"memorize":       Memorize,
		"forget_memory":  ForgetMemory,
		"update_state":   UpdateState,
		"replace_state":  ReplaceState,
		"search_history": SearchHistory,
		"check_messages": CheckMessages,
		"sleep":          Sleep,
		"reload_addons":  ReloadAddons,
		"define_tool":    DefineTool,
		"custom_tool":    CustomTool,
		"read_file":      ReadFile,
		"write_file":     WriteFile,
		"edit_file":      EditFile,
	}

	return registry
}

func GetToolSchemas() []interface{} {
	schemas := []interface{}{
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "ask_user",
				"description": "Pauses execution and waits for a human reply in the terminal.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"question": map[string]interface{}{
							"type":        "string",
							"description": "The question to the human.",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Optional seconds to wait for a reply (30-3600). Defaults to 600.",
						},
					},
					"required": []string{"question"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "docker_shell",
				"description": "Executes a shell command inside a Docker container.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "The command to run.",
						},
						"image": map[string]interface{}{
							"type":        "string",
							"description": "The docker image.",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Optional timeout in seconds.",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "think",
				"description": "Logs a thought to the console.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"text": map[string]interface{}{
							"type":        "string",
							"description": "The thought content.",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Optional timeout in seconds.",
						},
					},
					"required": []string{"text"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "memorize",
				"description": "Stores a discrete fact or finding in long-term memory. Returns the ID of the memory.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"content": map[string]interface{}{
							"type":        "string",
							"description": "The fact to remember.",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Optional timeout in seconds.",
						},
					},
					"required": []string{"content"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "forget_memory",
				"description": "Removes a fact from long-term memory by its ID. IDs are shown in your DISCRETE MEMORIES section.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "integer",
							"description": "The ID of the memory to forget.",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Optional timeout in seconds.",
						},
					},
					"required": []string{"id"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "update_state",
				"description": "Updates the agent's prime context / state / memory.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"new_text": map[string]interface{}{
							"type":        "string",
							"description": "The new full text of the state.",
						},
						"timeout": map[string]interface{}{
							"type":        "integer",
							"description": "Optional timeout in seconds.",
						},
					},
					"required": []string{"new_text"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "replace_state",
				"description": "Selectively replace a block of text in your Prime Context. Safer than update_state.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"old_text": map[string]interface{}{"type": "string", "description": "The exact text to find and remove"},
						"new_text": map[string]interface{}{"type": "string", "description": "The new text to insert instead"},
						"timeout":  map[string]interface{}{"type": "integer", "description": "Optional timeout in seconds."},
					},
					"required": []string{"old_text", "new_text"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "search_history",
				"description": "Search your past conversation history for keywords.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query":   map[string]interface{}{"type": "string", "description": "The keyword or phrase to search for"},
						"limit":   map[string]interface{}{"type": "integer", "description": "Maximum number of results to return (default 10)"},
						"timeout": map[string]interface{}{"type": "integer", "description": "Optional timeout in seconds."},
					},
					"required": []string{"query"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "check_messages",
				"description": "Reads and clears any pending messages from the human that were received while you were busy.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"timeout": map[string]interface{}{"type": "integer", "description": "Optional timeout in seconds."},
					},
					"required": []string{},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "sleep",
				"description": "Hibernates the agent for a specific amount of time. Useful for waiting for tasks to complete or checking back later.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"seconds": map[string]interface{}{
							"type":        "integer",
							"description": "Number of seconds to sleep.",
						},
						"allow_wakeup": map[string]interface{}{
							"type":        "boolean",
							"description": "If true, the agent will wake up immediately if a user message is received. Default is true.",
						},
					},
					"required": []string{"seconds"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "reload_addons",
				"description": "Scans the addons folder and updates the agent's available tools without restarting.",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "define_tool",
				"description": "Create a new custom tool dynamically at runtime that will be immediately available.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": map[string]interface{}{
							"type":        "string",
							"description": "Tool name in snake_case",
						},
						"description": map[string]interface{}{
							"type":        "string",
							"description": "What the tool does",
						},
						"parameters": map[string]interface{}{
							"type":        "object",
							"description": "OpenAI function parameter schema (optional)",
						},
						"code": map[string]interface{}{
							"type":        "string",
							"description": "Python code for the execute(args) function body",
						},
					},
					"required": []string{"name", "description", "code"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "custom_tool",
				"description": "Execute a custom tool by name with parameters. Use this for any tools not listed in the main schema.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": map[string]interface{}{
							"type":        "string",
							"description": "The name of the custom tool to execute",
						},
						"parameters": map[string]interface{}{
							"type":        "object",
							"description": "The parameters for the custom tool (key-value pairs)",
						},
					},
					"required": []string{"name", "parameters"},
				},
			},
		},

		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "read_file",
				"description": "Read a UTF-8 text file, returning numbered lines. Use offset/limit to window large files instead of loading them whole. Paths are relative to the agent's working directory and cannot escape it.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path relative to the working directory",
						},
						"offset": map[string]interface{}{
							"type":        "integer",
							"description": "First line to return (1-based). Default 1.",
						},
						"limit": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum number of lines to return. Default 200, max 2000.",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "write_file",
				"description": "Create a new file, or replace an existing one when overwrite=true. Refuses to clobber an existing file by default. For targeted changes to an existing file use edit_file instead.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path relative to the working directory",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "Full new file content",
						},
						"overwrite": map[string]interface{}{
							"type":        "boolean",
							"description": "Allow replacing an existing file (default false)",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        "edit_file",
				"description": "Replace an exact substring in a file. old_string must match exactly once unless replace_all=true; a 0-match or ambiguous match is a hard error. Returns a diff of what changed. Anchor on exact current text, including indentation.",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path relative to the working directory",
						},
						"old_string": map[string]interface{}{
							"type":        "string",
							"description": "Exact existing text to replace (must be unique unless replace_all=true)",
						},
						"new_string": map[string]interface{}{
							"type":        "string",
							"description": "Replacement text",
						},
						"replace_all": map[string]interface{}{
							"type":        "boolean",
							"description": "Replace every occurrence instead of requiring a unique match (default false)",
						},
						"dry_run": map[string]interface{}{
							"type":        "boolean",
							"description": "Preview the change without writing (default false)",
						},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		},
	}

	return schemas
}
