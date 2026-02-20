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

type ReasoningConfig struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

type LLMResponse struct {
	Choices []struct {
		Message struct {
			db.Message
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
}

// SynthesizeVirtualCall creates a fake 'ask_user' tool call for messages that have content but no tools.
// This ensures the LSPR loop handles the response as an interactive engagement rather than a silent exit.
func SynthesizeVirtualCall(msg *db.Message) ([]ToolCall, error) {
	if msg.Content == "" {
		return nil, nil
	}

	virtualCallID := fmt.Sprintf("v-call-%d", time.Now().UnixNano())
	tc := ToolCall{
		ID:   virtualCallID,
		Type: "function",
	}
	tc.Function.Name = "ask_user"
	tc.Function.Arguments = fmt.Sprintf(`{"question":%q}`, msg.Content)

	toolCalls := []ToolCall{tc}

	tcBytes, err := json.Marshal(toolCalls)
	if err != nil {
		return nil, err
	}
	msg.ToolCalls = tcBytes

	return toolCalls, nil
}

func SanitizeMessages(messages []db.Message) []db.Message {
	if len(messages) == 0 {
		return messages
	}

	// Pass 1: Handle Tool Call Pairing
	var pass1 []db.Message
	
	i := 0
	for i < len(messages) {
		m := messages[i]

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var toolCalls []ToolCall
			if err := json.Unmarshal(m.ToolCalls, &toolCalls); err != nil {
				m.ToolCalls = nil
				if m.Content != "" {
					pass1 = append(pass1, m)
				}
				i++
				continue
			}

			responseMap := make(map[string]db.Message)
			j := i + 1
			for j < len(messages) {
				if messages[j].Role == "tool" {
					responseMap[messages[j].ToolCallID] = messages[j]
					j++
				} else {
					break
				}
			}

			allFound := true
			for _, tc := range toolCalls {
				if _, ok := responseMap[tc.ID]; !ok {
					allFound = false
					break
				}
			}

			if allFound {
				pass1 = append(pass1, m)
				for k := i + 1; k < j; k++ {
					pass1 = append(pass1, messages[k])
				}
				i = j 
			} else {
				logger.LogWarning("Turn Fix: Stripping incomplete tool calls from Assistant msg %d. (Missing responses)", m.ID)
				m.ToolCalls = nil
				if m.Content != "" {
					pass1 = append(pass1, m)
				}
				i = j 
			}
		} else if m.Role == "tool" {
			logger.LogWarning("Turn Fix: Dropping orphaned tool message %d (%s)", m.ID, m.ToolCallID)
			i++
		} else {
			pass1 = append(pass1, m)
			i++
		}
	}

	// Pass 2: Merge Consecutive Roles
	var pass2 []db.Message
	for _, m := range pass1 {
		if len(pass2) == 0 {
			pass2 = append(pass2, m)
			continue
		}

		last := &pass2[len(pass2)-1]

		if last.Role == "user" && m.Role == "user" {
			if m.Content != "" {
				if last.Content != "" {
					last.Content += "\n\n" + m.Content
				} else {
					last.Content = m.Content
				}
			}
			continue
		}

		if last.Role == "assistant" && m.Role == "assistant" {
			if len(last.ToolCalls) == 0 && len(m.ToolCalls) == 0 {
				if m.Content != "" {
					if last.Content != "" {
						last.Content += "\n\n" + m.Content
					} else {
						last.Content = m.Content
					}
				}
				continue
			}
		}

		pass2 = append(pass2, m)
	}

	if len(pass2) > 0 {
		last := pass2[len(pass2)-1]
		if last.Role == "assistant" && len(last.ToolCalls) > 0 {
			logger.LogWarning("Turn Fix: History ends with unanswered tool call. Stripping to allow new turn.")
			last.ToolCalls = nil
			if last.Content == "" {
				pass2 = pass2[:len(pass2)-1]
			} else {
				pass2[len(pass2)-1] = last
			}
		}
	}

	return pass2
}

func LLMCall(messages []db.Message, tools []interface{}, config map[string]interface{}) (*db.Message, []ToolCall, error) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return &db.Message{Role: "assistant", Content: "No API Key"}, nil, nil
	}

	messages = SanitizeMessages(messages)

	model := "google/gemini-3-flash-preview"
	if m, ok := config["model"].(string); ok && m != "" {
		model = m
	}

	payload := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"tools":       tools,
		"tool_choice": "auto",
	}

	reasoningObj := map[string]interface{}{}
	if effort := os.Getenv("OPENROUTER_REASONING_EFFORT"); effort != "" {
		reasoningObj["effort"] = effort
	}

	if r, ok := config["reasoning"].(map[string]interface{}); ok {
		for k, v := range r {
			reasoningObj[k] = v
		}
	} else if r, ok := config["reasoning"].(ReasoningConfig); ok {
		if r.Effort != "" { reasoningObj["effort"] = r.Effort }
		if r.MaxTokens != 0 { reasoningObj["max_tokens"] = r.MaxTokens }
		reasoningObj["exclude"] = r.Exclude
		reasoningObj["enabled"] = r.Enabled
	}

	if len(reasoningObj) > 0 {
		if reasoningObj["effort"] != nil && reasoningObj["effort"] != "" {
			delete(reasoningObj, "max_tokens")
		}
		if _, ok := reasoningObj["enabled"]; !ok {
			reasoningObj["enabled"] = true
		}
		payload["reasoning"] = reasoningObj
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
		Timeout: 160 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.LogError("!!! Network/API request failed after %v: %v", time.Since(startTime), err)
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("API Error (%d): %s", resp.StatusCode, string(body))
	}

	var result LLMResponse
	responseBodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if err := json.Unmarshal(responseBodyBytes, &result); err != nil {
		return nil, nil, fmt.Errorf("failed to decode LLM response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, nil, fmt.Errorf("no choices returned from LLM")
	}

	fullMsg := result.Choices[0].Message.Message
	if result.Choices[0].Message.Reasoning != "" {
		fullMsg.Reasoning = result.Choices[0].Message.Reasoning
	}
	if result.Choices[0].Message.ReasoningContent != "" {
		fullMsg.Reasoning = result.Choices[0].Message.ReasoningContent
	}

	var toolCalls []ToolCall
	if fullMsg.ToolCalls != nil {
		if err := json.Unmarshal(fullMsg.ToolCalls, &toolCalls); err != nil {
			return nil, nil, err
		}
	}

	logger.LogSuccess("LSPR Turn Processed in %v.", time.Since(startTime))
	return &fullMsg, toolCalls, nil
}
