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

	// 2. Loop
	registry := tools.GetToolRegistry()
	schemas := tools.GetToolSchemas()

	for {
		err := runHeartbeat(registry, schemas)
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

func runHeartbeat(registry map[string]core.ToolFunc, schemas []interface{}) error {
	// Fetch State and Memories
	stateText, _ := db.GetState("prime_context")
	memories, err := db.GetMemories(20)
	if err != nil {
		logger.LogError("Failed to fetch memories: %v", err)
	}

	// 3. Prepare Prompt
	server.SetActivity("Synthesizing context & history...")
	sysMsg := prompt.RenderPrompt(stateText, memories, 3.0)
	
	// Get History from DB
	history, err := db.GetHistory(20)
	if err != nil {
		return fmt.Errorf("failed to get history: %v", err)
	}

	// We no longer need the manual pruning here because transactions ensure the DB is always clean.
	// But as a safety measure for existing corrupted DBs, we'll keep a simpler check once.
	if len(history) > 0 {
		lastMsg := history[len(history)-1]
		if lastMsg.Role == "assistant" && len(lastMsg.ToolCalls) > 0 {
			logger.LogInfo("Cleaning up legacy dangling tool call for transaction safety.")
			history = history[:len(history)-1]
		}
	}

	messages := []db.Message{
		{Role: "system", Content: sysMsg},
	}
	messages = append(messages, history...)
	
	pulse := fmt.Sprintf("HEARTBEAT PULSE:\n{\"time\": %d}\n\nThis is an automated heartbeat pulse. Continue your cycle.", time.Now().Unix())
	pulseMsg := db.Message{Role: "user", Content: pulse}
	messages = append(messages, pulseMsg)

	// Save the Pulse immediately as a baseline
	if err := db.SaveMessages([]db.Message{pulseMsg}); err != nil {
		logger.LogError("Failed to save initial heartbeat pulse: %v", err)
	}

	// currentTurns will now only track messages added DURING this loop for context,
	// but we will save them in groups to ensure sequence validity.
	currentTurns := []db.Message{}

	maxTurns := 10
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
		thisTurnMsgs := []db.Message{*msg}
		currentTurns = append(currentTurns, *msg)

		if len(toolCalls) == 0 {
			if msg.Content != "" {
				logger.LogAgent("%s", msg.Content)
			}
			// Save the final assistant response of the turn
			if err := db.SaveMessages(thisTurnMsgs); err != nil {
				logger.LogError("Failed to save final turn message: %v", err)
			}
			break
		}

		for _, call := range toolCalls {
			logger.LogInfo("Tool Call: %s", call.Function.Name)

			var result string
			// ... (tool execution logic) ...
			if fn, ok := registry[call.Function.Name]; ok {
				var args map[string]interface{}
				json.Unmarshal([]byte(call.Function.Arguments), &args)
				
				timeout := 30
				if t, ok := args["timeout"].(int); ok {
					timeout = t
				} else if t, ok := args["timeout"].(float64); ok {
					timeout = int(t)
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

		// ATOMIC COMMIT for this specific turn segment (Assistant + Tools)
		logger.LogInfo("Attempting to persist turn %d results...", turn+1)
		if err := db.SaveMessages(thisTurnMsgs); err != nil {
			logger.LogError("Failed to save turn messages incrementally: %v", err)
		}
	}

	return nil
}
