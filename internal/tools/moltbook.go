package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const MoltbookBaseURL = "https://www.moltbook.com/api/v1"

func getMoltbookKey() string {
	return os.Getenv("MOLTBOOK_API_KEY")
}

func moltbookRequest(method, endpoint string, body interface{}) ([]byte, error) {
	apiKey := getMoltbookKey()
	if apiKey == "" {
		return nil, fmt.Errorf("MOLTBOOK_API_KEY not set in environment")
	}

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	url := MoltbookBaseURL + endpoint
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status: %d, body: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func MoltbookRegister(ctx context.Context, args map[string]interface{}) string {
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)

	if name == "" {
		return "Error: agent name is required for registration"
	}

	body := map[string]string{
		"name":        name,
		"description": description,
	}

	url := MoltbookBaseURL + "/agents/register"
	jsonBody, _ := json.Marshal(body)
	
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Sprintf("Error registering: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return string(respBody)
}

func MoltbookPost(ctx context.Context, args map[string]interface{}) string {
	submolt, _ := args["submolt"].(string)
	title, _ := args["title"].(string)
	content, _ := args["content"].(string)
	url, _ := args["url"].(string)

	if submolt == "" || title == "" {
		return "Error: submolt and title are required"
	}

	body := map[string]interface{}{
		"submolt": submolt,
		"title":   title,
	}
	if content != "" {
		body["content"] = content
	}
	if url != "" {
		body["url"] = url
	}

	resp, err := moltbookRequest("POST", "/posts", body)
	if err != nil {
		return fmt.Sprintf("Error posting to Moltbook: %v", err)
	}

	return string(resp)
}

func MoltbookGetFeed(ctx context.Context, args map[string]interface{}) string {
	sort, _ := args["sort"].(string)
	if sort == "" {
		sort = "hot"
	}

	endpoint := fmt.Sprintf("/feed?sort=%s", sort)
	resp, err := moltbookRequest("GET", endpoint, nil)
	if err != nil {
		return fmt.Sprintf("Error fetching Moltbook feed: %v", err)
	}

	return string(resp)
}

func MoltbookSearch(ctx context.Context, args map[string]interface{}) string {
	q, _ := args["q"].(string)
	searchType, _ := args["type"].(string)
	if q == "" {
		return "Error: query 'q' is required"
	}

	endpoint := fmt.Sprintf("/search?q=%s", q)
	if searchType != "" {
		endpoint += "&type=" + searchType
	}

	resp, err := moltbookRequest("GET", endpoint, nil)
	if err != nil {
		return fmt.Sprintf("Error searching Moltbook: %v", err)
	}

	return string(resp)
}

func MoltbookComment(ctx context.Context, args map[string]interface{}) string {
	postID, _ := args["post_id"].(string)
	content, _ := args["content"].(string)
	parentID, _ := args["parent_id"].(string)

	if postID == "" || content == "" {
		return "Error: post_id and content are required"
	}

	body := map[string]interface{}{
		"content": content,
	}
	if parentID != "" {
		body["parent_id"] = parentID
	}

	endpoint := fmt.Sprintf("/posts/%s/comments", postID)
	resp, err := moltbookRequest("POST", endpoint, body)
	if err != nil {
		return fmt.Sprintf("Error commenting on Moltbook: %v", err)
	}

	return string(resp)
}

func MoltbookVote(ctx context.Context, args map[string]interface{}) string {
	postID, _ := args["post_id"].(string)
	vote, _ := args["vote"].(string) // "upvote" or "downvote"

	if postID == "" || vote == "" {
		return "Error: post_id and vote are required"
	}

	endpoint := fmt.Sprintf("/posts/%s/%s", postID, vote)
	resp, err := moltbookRequest("POST", endpoint, nil)
	if err != nil {
		return fmt.Sprintf("Error voting on Moltbook: %v", err)
	}

	return string(resp)
}

func MoltbookGetIdentityToken(ctx context.Context, args map[string]interface{}) string {
	resp, err := moltbookRequest("POST", "/agents/me/identity-token", nil)
	if err != nil {
		return fmt.Sprintf("Error fetching identity token: %v", err)
	}

	return string(resp)
}

func MoltbookVerify(ctx context.Context, args map[string]interface{}) string {
	answer, _ := args["answer"].(string)
	verificationCode, _ := args["verification_code"].(string)

	if answer == "" || verificationCode == "" {
		return "Error: answer and verification_code are required"
	}

	body := map[string]string{
		"answer":            answer,
		"verification_code": verificationCode,
	}

	resp, err := moltbookRequest("POST", "/verify", body)
	if err != nil {
		return fmt.Sprintf("Error verifying on Moltbook: %v", err)
	}

	return string(resp)
}
