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

	if err := core.LoadEnv(".env"); err != nil {
		logger.LogError("Failed to load .env: %v", err)
	}

	if err := db.InitDB(); err != nil {
		logger.LogError("Fatal error initializing database: %v", err)
		os.Exit(1)
	}
	defer db.CloseDB()

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

	server.Start(8080)

	config := map[string]interface{}{
		"poll_interval": 5.0,
		"timeout":       30.0,
	}

	if configStr != "" {
		json.Unmarshal([]byte(configStr), &config)
	}

	if stateText == "" {
		stateText = "You are an autonomous agent. This is your Prime Context.\n- Try to contact the user first"
		db.SetState("prime_context", stateText)
		cData, _ := json.Marshal(config)
		db.SetState("config", string(cData))
	}
	logger.LogSuccess("System state initialized.")

	tools.LoadAddons()

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

		select {
		case <-server.WakeupChan:
		default:
		}

		logger.LogInfo("Sleeping for %ds (listening for wakeup)...", pollInterval)
		server.SetActivity(fmt.Sprintf("Resting (%ds pulse interval)", pollInterval))
		
		select {
		case <-time.After(time.Duration(pollInterval) * time.Second):
		case <-server.WakeupChan:
			logger.LogSuccess("Received external wakeup signal, starting cycle now!")
		}
	}
}

func runHeartbeat() error {
	registry := tools.GetToolRegistry()
	schemas := tools.GetToolSchemas()

	stateText, _ := db.GetState("prime_context")
	configStr, _ := db.GetState("config")
	config := map[string]interface{}{}
	if configStr != "" {
		json.Unmarshal([]byte(configStr), &config)
	}

	memories, _ := db.GetMemories(20)

	server.SetActivity("Synthesizing context & history...")
	sysMsg := prompt.RenderPrompt(stateText, memories, 3.0)
	
	history, err := db.GetContextWindow(50)
	if err != nil {
		return fmt.Errorf("failed to get history: %v", err)
	}

	pendingMsgs, _ := db.FetchAndClearPending()
	if len(pendingMsgs) > 0 {
		for _, content := range pendingMsgs {
			msg := db.Message{Role: "user", Content: content}
			db.SaveMessage(msg)
			history = append(history, msg)
		}
	}

	messages := []db.Message{
		{Role: "system", Content: sysMsg},
	}
	messages = append(messages, history...)
	
	currentTurns := []db.Message{}
	maxTurns := 30
	
	for turn := 0; turn < maxTurns; turn++ {
		server.SetActivity(fmt.Sprintf("Core Processing: Turn %d", turn+1))
		
		fullContext := append(messages, currentTurns...)
		msg, toolCalls, err := core.LLMCall(fullContext, schemas, config)
		if err != nil {
			return err
		}

		thisTurnMsgs := []db.Message{}
		thisTurnMsgs = append(thisTurnMsgs, *msg)
		currentTurns = append(currentTurns, *msg)

		if len(toolCalls) == 0 {
			if msg.Content != "" {
				logger.LogAgent("%s", msg.Content)

				// Refactored: Use helper function to synthesize virtual call
				var err error
				toolCalls, err = core.SynthesizeVirtualCall(msg)
				if err != nil {
					logger.LogError("Failed to synthesize virtual call: %v", err)
					break
				}

				// Synchronize the message back into history
				currentTurns[len(currentTurns)-1] = *msg
				thisTurnMsgs[len(thisTurnMsgs)-1] = *msg
			} else {
				if len(thisTurnMsgs) > 0 {
					db.SaveMessages(thisTurnMsgs)
				}
				break
			}
		}

		didReload := false
		for _, call := range toolCalls {
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
				
				if call.Function.Name == "sleep" {
					if s, ok := args["seconds"].(float64); ok {
						if int(s)+5 > timeout { timeout = int(s) + 5 }
					} else if s, ok := args["seconds"].(int); ok {
						if s+5 > timeout { timeout = s + 5 }
					}
				} else if call.Function.Name == "ask_user" {
					if timeout < 600 { timeout = 600 }
				}
				
				server.SetActivity(fmt.Sprintf("Tool Engagement: %s", call.Function.Name))
				result = core.ExecuteTool(context.Background(), fn, args, time.Duration(timeout)*time.Second)
			} else {
				result = fmt.Sprintf("Error: Tool %s not found", call.Function.Name)
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

		if didReload {
			registry = tools.GetToolRegistry()
			schemas = tools.GetToolSchemas()
		}

		if len(thisTurnMsgs) > 0 {
			db.SaveMessages(thisTurnMsgs)
		}
	}

	return nil
}
