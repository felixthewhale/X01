package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"X01/internal/core"
	"X01/internal/db"
	"X01/internal/logger"
	"X01/internal/server"
)

func AskUser(ctx context.Context, args map[string]interface{}) string {
	question, _ := args["question"].(string)
	fmt.Printf("\n========================================\n")
	fmt.Printf("QUESTION FROM X01: %s\n", question)
	fmt.Printf("========================================\n")
	fmt.Print("You: ")

	// Use a channel to receive input so we can select on context cancellation
	inputChan := make(chan string)
	errorChan := make(chan error)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			inputChan <- scanner.Text()
		} else if err := scanner.Err(); err != nil {
			errorChan <- err
		} else {
			inputChan <- "No response"
		}
	}()

	// Request web reply
	server.SetActivity("Awaiting Authorization (Human Proxy Needed)")
	webReplyChan := server.RequestReply(question)
	defer server.ClearRequest()

	select {
	case result := <-inputChan:
		server.SetActivity("Engagement Received (Terminal)")
		return result
	case result := <-webReplyChan:
		server.SetActivity("Engagement Received (Web)")
		logger.LogSuccess("Received web reply: %s", result)
		return result
	case err := <-errorChan:
		return fmt.Sprintf("Error reading input: %v", err)
	case <-ctx.Done():
		fmt.Printf("\n[Timeout/Cancelled - aborting wait for response]\n")
		return "Timed out waiting for user response"
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
		logger.LogInfo("Warming up sandbox: installing curl, wget, and build-essential...")
		installCmd := exec.Command("docker", "exec", ContainerName, "sh", "-c", 
			"apt-get update && apt-get install -y curl wget build-essential")
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
		image = "python:3.10-slim"
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

func GetToolRegistry() map[string]core.ToolFunc {
	return map[string]core.ToolFunc{
		"ask_user":      AskUser,
		"docker_shell":  DockerShell,
		"think":         Think,
		"memorize":      Memorize,
		"forget_memory": ForgetMemory,
		"update_state":  UpdateState,
		"replace_state": ReplaceState,
		"search_history": SearchHistory,
		"check_messages": CheckMessages,
		"sleep":          Sleep,
	}
}

func GetToolSchemas() []interface{} {
	return []interface{}{
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
							"description": "Optional timeout in seconds.",
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
	}
}
