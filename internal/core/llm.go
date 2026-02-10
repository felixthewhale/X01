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

	payload := map[string]interface{}{
		"model":       "google/gemini-3-flash-preview",
		"messages":    messages,
		"tools":       tools,
		"tool_choice": "auto",
	}

	logger.LogInfo("Calling LLM (%s)...", payload["model"])
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logger.LogError("API request failed: %v", err)
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		logger.LogError("API Error (%d): %s", resp.StatusCode, string(body))
		return nil, nil, fmt.Errorf("API Error (%d): %s", resp.StatusCode, string(body))
	}

	var result LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
	}

	if len(result.Choices) == 0 {
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

	return &fullMsg, toolCalls, nil
}
