package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"X01/internal/core"
	"X01/internal/db"
	"X01/internal/logger"
	"X01/internal/prompt"
	"X01/internal/server"
	"X01/internal/tools"
)

func main() {
	logger.LogHeader("🔋 Agent X01 (Go) Starting")

	// 0. Load .env
	if err := core.LoadEnv(".env"); err != nil {
		logger.LogError("Failed to load .env: %v", err)
	}

	// 0.1 Initialize DB
	if err := db.InitDB(); err != nil {
		logger.LogError("Fatal error initializing database: %v", err)
		os.Exit(1)
	}
	defer db.CloseDB()

	// 1. Initialize State from DB
	stateText, err := db.GetState("prime_context")
	if err != nil {
		logger.LogError("Fatal error loading state from DB: %v", err)
		os.Exit(1)
	}

	configStr, err := db.GetState("config")
	if err != nil {
		logger.LogError("Fatal error loading config from DB: %v", err)
		os.Exit(1)
	}

	// Start Background Web Dashboard
	server.Start(8080)

	// Default Config
	config := map[string]interface{}{
		"poll_interval": 5.0,
		"timeout":       30.0,
	}

	if configStr != "" {
		json.Unmarshal([]byte(configStr), &config)
	}

	// Migration logic (one-time)
	if stateText == "" {
		logger.LogInfo("State empty, checking for legacy JSON state...")
		if _, err := os.Stat("state/current.json"); err == nil {
			logger.LogInfo("Migrating legacy state/current.json to DB...")
			data, _ := os.ReadFile("state/current.json")
			var legacy struct {
				Text   string                 `json:"text"`
				Config map[string]interface{} `json:"config"`
			}
			if err := json.Unmarshal(data, &legacy); err == nil {
				stateText = legacy.Text
				config = legacy.Config
				db.SetState("prime_context", stateText)
				cData, _ := json.Marshal(config)
				db.SetState("config", string(cData))
				logger.LogSuccess("Migration successful.")
			}
		} else {
			stateText = "You are an autonomous agent. This is your Prime Context.\n- Try to contact the user first"
			db.SetState("prime_context", stateText)
			cData, _ := json.Marshal(config)
			db.SetState("config", string(cData))
		}
	}
	logger.LogSuccess("System state initialized.")

	// Load dynamic addons
	tools.LoadAddons()

	// 2. Loop
	for {
		err := runHeartbeat()
		if err != nil {
			if err == context.DeadlineExceeded {
				logger.LogError("Cycle Timeout: LLM took too long to respond. Retrying...")
			} else {
				logger.LogError("LSPR Cycle Error: %v", err)
			}
			time.Sleep(5 * time.Second)
		}

		// Re-fetch config for potential updates
		configStr, _ = db.GetState("config")
		if configStr != "" {
			json.Unmarshal([]byte(configStr), &config)
		}

		pollInterval := 5
		if interval, ok := config["poll_interval"].(int); ok {
			pollInterval = interval
		} else if interval, ok := config["poll_interval"].(float64); ok {
			pollInterval = int(interval)
		}

		// Clear any pending wakeups before sleeping
		select {
		case <-server.WakeupChan:
		default:
		}

		logger.LogInfo("Sleeping for %ds (listening for wakeup)...", pollInterval)
		server.SetActivity(fmt.Sprintf("Resting (%ds pulse interval)", pollInterval))
		
		select {
		case <-time.After(time.Duration(pollInterval) * time.Second):
			// Woke up normally
		case <-server.WakeupChan:
			logger.LogSuccess("Received external wakeup signal, starting cycle now!")
		}
	}
}

func runHeartbeat() error {
	// 0. Fetch latest tools
	registry := tools.GetToolRegistry()
	schemas := tools.GetToolSchemas()

	// 1. Fetch State and Memories
	stateText, _ := db.GetState("prime_context")
	memories, err := db.GetMemories(20)
	if err != nil {
		logger.LogError("Failed to fetch memories: %v", err)
	}

	// 2. Prepare System Prompt
	server.SetActivity("Synthesizing context & history...")
	sysMsg := prompt.RenderPrompt(stateText, memories, 3.0)
	
	// 3. Get History from DB (Smart Context Window)
	history, err := db.GetContextWindow(50)
	if err != nil {
		return fmt.Errorf("failed to get history: %v", err)
	}

	// 3.1 Fetch Pending Web Messages
	pendingMsgs, err := db.FetchAndClearPending()
	if err != nil {
		logger.LogError("Failed to fetch pending messages: %v", err)
	}
	if len(pendingMsgs) > 0 {
		logger.LogInfo("Found %d pending messages from web dashboard", len(pendingMsgs))
		for _, content := range pendingMsgs {
			// Create and save the user message to history
			msg := db.Message{
				Role:    "user",
				Content: content,
			}
			if err := db.SaveMessage(msg); err != nil {
				logger.LogError("Failed to save pending message: %v", err)
			}
			// Add to current history context so the agent reacts immediately
			history = append(history, msg)
		}
	}

	// 4. Prepare Context
	messages := []db.Message{
		{Role: "system", Content: sysMsg},
	}
	messages = append(messages, history...)
	
	// 5. Execution Loop (LSPR)
	currentTurns := []db.Message{}
	maxTurns := 30
	logger.LogInfo("Starting LSPR cycle (max %d turns)...", maxTurns)
	
	for turn := 0; turn < maxTurns; turn++ {
		logger.LogInfo("--- Turn %d/%d ---", turn+1, maxTurns)
		server.SetActivity(fmt.Sprintf("Core Processing: Turn %d", turn+1))
		
		fullContext := append(messages, currentTurns...)
		msg, toolCalls, err := core.LLMCall(fullContext, schemas)
		if err != nil {
			return err
		}

		// Buffer for messages in THIS specific turn
		thisTurnMsgs := []db.Message{}
		
		
		thisTurnMsgs = append(thisTurnMsgs, *msg)
		currentTurns = append(currentTurns, *msg)

		if len(toolCalls) == 0 {
			if msg.Content != "" {
				logger.LogAgent("%s", msg.Content)

				// --- Implicit Engagement Upgrade ---
				// If there's content but no tool call, synthesize a virtual 'ask_user' call
				// to maintain turn order and trigger the next turn naturally.
				virtualCallID := fmt.Sprintf("v-call-%d", time.Now().UnixNano())
				tc := core.ToolCall{
					ID:   virtualCallID,
					Type: "function",
				}
				tc.Function.Name = "ask_user"
				tc.Function.Arguments = fmt.Sprintf(`{"question":%q}`, msg.Content)

				toolCalls = []core.ToolCall{tc}

				// Update the assistant message in history to include this virtual call
				tcBytes, _ := json.Marshal(toolCalls)
				msg.ToolCalls = tcBytes

				// Replace the last message in currentTurns and thisTurnMsgs with the updated one
				currentTurns[len(currentTurns)-1] = *msg
				thisTurnMsgs[len(thisTurnMsgs)-1] = *msg
			} else {
				// Truly empty response? Break and let it sleep.
				if len(thisTurnMsgs) > 0 {
					if err := db.SaveMessages(thisTurnMsgs); err != nil {
						logger.LogError("Failed to save final turn message: %v", err)
					}
				}
				break
			}
		}

		didReload := false
		for _, call := range toolCalls {
			logger.LogInfo("Tool Call: %s", call.Function.Name)

			if call.Function.Name == "reload_addons" || call.Function.Name == "define_tool" {
				didReload = true
			}

			var result string
			if fn, ok := registry[call.Function.Name]; ok {
				var args map[string]interface{}
				json.Unmarshal([]byte(call.Function.Arguments), &args)
				
				timeout := 30
				if t, ok := args["timeout"].(int); ok {
					timeout = t
				} else if t, ok := args["timeout"].(float64); ok {
					timeout = int(t)
				}

				// Special handling for long-running tools
				if call.Function.Name == "sleep" {
					if s, ok := args["seconds"].(float64); ok {
						if int(s)+5 > timeout {
							timeout = int(s) + 5
						}
					} else if s, ok := args["seconds"].(int); ok {
						if s+5 > timeout {
							timeout = s + 5
						}
					}
				} else if call.Function.Name == "ask_user" {
					if timeout < 600 {
						timeout = 600
					}
				}
				
				server.SetActivity(fmt.Sprintf("Tool Engagement: %s", call.Function.Name))
				result = core.ExecuteTool(context.Background(), fn, args, time.Duration(timeout)*time.Second)
			} else {
				result = fmt.Sprintf("Error: Tool %s not found", call.Function.Name)
				logger.LogError("Tool not found: %s", call.Function.Name)
			}

			toolResultMsg := db.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    result,
			}
			thisTurnMsgs = append(thisTurnMsgs, toolResultMsg)
			currentTurns = append(currentTurns, toolResultMsg)
		}

		// Refresh registry if tools were changed in this turn
		if didReload {
			logger.LogInfo("Refreshing tool registry for the next turn...")
			registry = tools.GetToolRegistry()
			schemas = tools.GetToolSchemas()
		}

		// ATOMIC COMMIT for this specific turn segment (Assistant + Tools)
		if len(thisTurnMsgs) > 0 {
			logger.LogInfo("Attempting to persist turn %d results...", turn+1)
			if err := db.SaveMessages(thisTurnMsgs); err != nil {
				logger.LogError("Failed to save turn messages incrementally: %v", err)
			}
		}
	}

	return nil
}
