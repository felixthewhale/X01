package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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

	// Defaults persisted on first run (edit the "config" row in the DB to change):
	//   poll_interval: seconds between heartbeats
	//   timeout:       default tool timeout in seconds (tools.ResolveToolTimeout)
	//   max_turns:     max LLM turns per heartbeat cycle
	//   interactive:   true  -> a text-only reply becomes a blocking ask_user
	//                  false -> the reply ends the cycle (autonomous mode)
	config := map[string]interface{}{
		"poll_interval": 5.0,
		"timeout":       float64(tools.DefaultToolTimeoutSeconds),
		"max_turns":     30,
		"interactive":   true,
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

	// Autonomy switch: by default (interactive=true) a text-only reply is turned
	// into a blocking ask_user call, so the human always gets a chance to answer.
	// With "interactive": false in the persisted config, such a reply simply ends
	// the cycle - no synthesized tool call, no 10-minute wait.
	interactive := configBool(config, "interactive", true)
	maxTurns := configInt(config, "max_turns", 30)
	if maxTurns < 1 {
		maxTurns = 1
	}
	
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

				if !interactive {
					// Autonomy mode: the reply is the cycle's final answer.
					if len(thisTurnMsgs) > 0 {
						db.SaveMessages(thisTurnMsgs)
					}
					break
				}

				// Interactive mode: synthesize a virtual ask_user call so the
				// reply becomes a blocking question for the human.
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
				
				// Default tool timeout comes from config["timeout"], per-call
				// args["timeout"] overrides it, and ask_user/sleep get their
				// own rules (see tools.ResolveToolTimeout).
				timeout := tools.ResolveToolTimeout(call.Function.Name, args, config)

				server.SetActivity(fmt.Sprintf("Tool Engagement: %s", call.Function.Name))
				result = core.ExecuteTool(context.Background(), fn, args, timeout)
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

// configBool reads a boolean setting from the persisted config, tolerating the
// JSON-decoded types (bool, "true"/"false" strings, 0/1 numbers).
func configBool(config map[string]interface{}, key string, def bool) bool {
	if config == nil {
		return def
	}
	switch v := config[key].(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return def
}

// configInt reads an integer setting from the persisted config.
func configInt(config map[string]interface{}, key string, def int) int {
	if config == nil {
		return def
	}
	switch v := config[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return def
}
