package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	opencode "github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"
)

// OpencodeClient wraps the SDK client and provides direct HTTP helpers
// for endpoints not covered by the SDK.
type OpencodeClient struct {
	sdk  *opencode.Client
	cfg  *Config
	http *http.Client
}

// NewOpencodeClient creates a new Opencode API client.
func NewOpencodeClient(cfg *Config) *OpencodeClient {
	opts := []option.RequestOption{
		option.WithBaseURL(cfg.OpencodeURL),
	}
	if cfg.OpencodePassword != "" {
		// Basic auth with empty username
		opts = append(opts, option.WithHTTPClient(&http.Client{}))
		// We'll handle auth manually for direct HTTP calls
	}
	return &OpencodeClient{
		sdk:  opencode.NewClient(opts...),
		cfg:  cfg,
		http: &http.Client{},
	}
}

// ListSessions returns all sessions.
func (c *OpencodeClient) ListSessions(ctx context.Context) ([]opencode.Session, error) {
	sessions, err := c.sdk.Session.List(ctx, opencode.SessionListParams{})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	if sessions == nil {
		return []opencode.Session{}, nil
	}
	return *sessions, nil
}

// NewSession creates a new session with an optional title.
func (c *OpencodeClient) NewSession(ctx context.Context, title string) (*opencode.Session, error) {
	params := opencode.SessionNewParams{}
	if title != "" {
		params.Title = opencode.String(title)
	}
	session, err := c.sdk.Session.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	return session, nil
}

// GetMessages returns the last N messages for a session.
func (c *OpencodeClient) GetMessages(ctx context.Context, sessionID string, limit int) ([]opencode.SessionMessagesResponse, error) {
	msgs, err := c.sdk.Session.Messages(ctx, sessionID, opencode.SessionMessagesParams{})
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	if msgs == nil {
		return []opencode.SessionMessagesResponse{}, nil
	}
	all := *msgs
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

// AbortSession aborts a running session.
func (c *OpencodeClient) AbortSession(ctx context.Context, sessionID string) error {
	_, err := c.sdk.Session.Abort(ctx, sessionID, opencode.SessionAbortParams{})
	if err != nil {
		return fmt.Errorf("abort session: %w", err)
	}
	return nil
}

// PromptAsync sends a message to a session using the SDK.
func (c *OpencodeClient) PromptAsync(ctx context.Context, sessionID string, text string) error {
	_, err := c.sdk.Session.Prompt(ctx, sessionID, opencode.SessionPromptParams{
		Parts: opencode.F([]opencode.SessionPromptParamsPartUnion{
			opencode.TextPartInputParam{
				Type: opencode.F(opencode.TextPartInputTypeText),
				Text: opencode.F(text),
			},
		}),
	})
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	return nil
}

// SessionStatus holds the status of a session.
type SessionStatus struct {
	State string `json:"type"` // "idle" | "busy" | "running" | "retry" | "error"
}

// GetSessionStatus returns the status of all sessions.
// Maps session ID -> SessionStatus.
func (c *OpencodeClient) GetSessionStatus(ctx context.Context) (map[string]SessionStatus, error) {
	url := fmt.Sprintf("%s/session/status", c.cfg.OpencodeURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get session status: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("session status returned %d: %s", resp.StatusCode, string(data))
	}
	var result map[string]SessionStatus
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse session status: %w", err)
	}
	return result, nil
}

// TodoItem represents a single TODO entry.
type TodoItem struct {
	Content   string `json:"content"`
	Completed bool   `json:"completed"`
}

// GetTodo returns the TODO list for a session.
func (c *OpencodeClient) GetTodo(ctx context.Context, sessionID string) ([]TodoItem, error) {
	url := fmt.Sprintf("%s/session/%s/todo", c.cfg.OpencodeURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get todo: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("get todo returned %d: %s", resp.StatusCode, string(data))
	}
	var items []TodoItem
	if err := json.Unmarshal(data, &items); err != nil {
		// try alternative structure
		var wrapper struct {
			Items []TodoItem `json:"items"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 == nil {
			return wrapper.Items, nil
		}
		return nil, fmt.Errorf("parse todo: %w", err)
	}
	return items, nil
}

// GetLastMessageID returns the ID of the most recent message in a session,
// or empty string if there are no messages.
// Uses ?limit=1 to avoid downloading the full message history.
func (c *OpencodeClient) GetLastMessageID(ctx context.Context, sessionID string) (string, error) {
	url := fmt.Sprintf("%s/session/%s/message?limit=1", c.cfg.OpencodeURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("get last message: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("get last message returned %d", resp.StatusCode)
	}
	var msgs []struct {
		Info struct {
			ID string `json:"id"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) == 0 {
		return "", nil
	}
	return msgs[0].Info.ID, nil
}

// RespondToPermission responds to a permission request.
// response can be "once", "always", or "reject".
func (c *OpencodeClient) RespondToPermission(ctx context.Context, permissionID string, response string) error {
	url := fmt.Sprintf("%s/permission/%s/reply", c.cfg.OpencodeURL, permissionID)
	bodyMap := map[string]string{"reply": response}
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("marshal permission reply: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, io.NopCloser(bytes.NewReader(bodyBytes)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("respond to permission: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("permission reply returned %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// RespondToQuestion responds to a question request.
// answers is a slice of slices of strings (one slice per question, can have multiple answers)
func (c *OpencodeClient) RespondToQuestion(ctx context.Context, questionID string, answers [][]string) error {
	url := fmt.Sprintf("%s/question/%s/reply", c.cfg.OpencodeURL, questionID)
	bodyMap := map[string]interface{}{"answers": answers}
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("marshal question reply: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, io.NopCloser(bytes.NewReader(bodyBytes)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("respond to question: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("question reply returned %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// RejectQuestion rejects a question request.
func (c *OpencodeClient) RejectQuestion(ctx context.Context, questionID string) error {
	url := fmt.Sprintf("%s/question/%s/reject", c.cfg.OpencodeURL, questionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, io.NopCloser(bytes.NewReader([]byte("{}"))))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reject question: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("question reject returned %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// setAuth adds Basic Auth header if password is configured.
func (c *OpencodeClient) setAuth(req *http.Request) {
	if c.cfg.OpencodePassword != "" {
		req.SetBasicAuth("", c.cfg.OpencodePassword)
	}
}
