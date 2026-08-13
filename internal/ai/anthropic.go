package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/huandert/repolens/internal/ai/prompts"
)

const defaultAnthropicModel = "claude-sonnet-4-5"

type anthropicProvider struct {
	apiKey string
	model  string
	client *http.Client
}

func newAnthropicProvider(apiKey, model string) Provider {
	if model == "" {
		model = defaultAnthropicModel
	}
	return &anthropicProvider{apiKey: apiKey, model: model, client: &http.Client{Timeout: 90 * time.Second}}
}

func (p *anthropicProvider) Name() string { return "anthropic" }

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *anthropicProvider) Ask(ctx context.Context, req AskRequest) (*AskResponse, error) {
	body := anthropicRequest{
		Model:     p.model,
		MaxTokens: 2048,
		System:    prompts.SystemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompts.BuildUserPrompt(req.Question, req.GraphContext)},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("anthropic authentication failed: invalid ANTHROPIC_API_KEY")
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("anthropic error: %s", ar.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("anthropic request failed (%d): %s", resp.StatusCode, string(raw))
	}
	if len(ar.Content) == 0 {
		return nil, fmt.Errorf("anthropic returned an empty response")
	}

	return parseStructuredAnswer(ar.Content[0].Text), nil
}
