package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"X01/internal/db"
	"X01/internal/logger"
)

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type LLMMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type LLMResponse struct {
	Choices []struct {
		Message struct {
			db.Message
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
}

func LLMCall(messages []db.Message, tools []interface{}) (*db.Message, []ToolCall, error) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return &db.Message{Role: "assistant", Content: "No API Key"}, nil, nil
	}

	// The provided code edit seems to be attempting to insert a prompt string here.
	payload := map[string]interface{}{
		"model":       "google/gemini-3-flash-preview", // Switched to more stable version
		"messages":    messages,
		"tools":       tools,
		"tool_choice": "auto",
	}

	startTime := time.Now()
	logger.LogInfo("Calling OpenRouter API (%s)...", payload["model"])

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/felixthewhale/X01")
	req.Header.Set("X-Title", "X01 Orbital Core")

	client := &http.Client{
		Timeout: 160 * time.Second, // Hard client timeout
	}

	logger.LogInfo("-> Request Sent. Waiting for headers...")
	resp, err := client.Do(req)
	if err != nil {
		logger.LogError("!!! Network/API request failed after %v: %v", time.Since(startTime), err)
		return nil, nil, err
	}
	defer resp.Body.Close()

	logger.LogInfo("<- Response Received (Status: %d) after %v.", resp.StatusCode, time.Since(startTime))

	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		logger.LogError("API HTTP %d Error: %s", resp.StatusCode, string(body))
		return nil, nil, fmt.Errorf("API Error (%d): %s", resp.StatusCode, string(body))
	}

	var result LLMResponse
	// Read the response body into a buffer first, so it can be logged if choices are empty
	responseBodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	logger.LogInfo("<- Body Downloaded (%d bytes). Parsing JSON...", len(responseBodyBytes))

	if err := json.Unmarshal(responseBodyBytes, &result); err != nil {
		return nil, nil, fmt.Errorf("failed to decode LLM response: %w", err)
	}

	if len(result.Choices) == 0 {
		logger.LogError("LLM returned no choices. Raw response: %s", string(responseBodyBytes))
		return nil, nil, fmt.Errorf("no choices returned from LLM")
	}

	fullMsg := result.Choices[0].Message.Message
	if result.Choices[0].Message.ReasoningContent != "" {
		fullMsg.Reasoning = result.Choices[0].Message.ReasoningContent
	}

	if fullMsg.Reasoning != "" {
		logger.LogThink("Reasoning: %s", fullMsg.Reasoning)
	}

	// Parse tool calls from RawMessage if present
	var toolCalls []ToolCall
	if fullMsg.ToolCalls != nil {
		if err := json.Unmarshal(fullMsg.ToolCalls, &toolCalls); err != nil {
			return nil, nil, err
		}
	}

	logger.LogSuccess("LSPR Turn Processed in %v.", time.Since(startTime))
	return &fullMsg, toolCalls, nil
}
