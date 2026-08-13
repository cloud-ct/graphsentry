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

const defaultOpenAIModel = "gpt-4o-mini"

type openAIProvider struct {
	apiKey string
	model  string
	client *http.Client
}

func newOpenAIProvider(apiKey, model string) Provider {
	if model == "" {
		model = defaultOpenAIModel
	}
	return &openAIProvider{apiKey: apiKey, model: model, client: &http.Client{Timeout: 90 * time.Second}}
}

func (p *openAIProvider) Name() string { return "openai" }

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *openAIProvider) Ask(ctx context.Context, req AskRequest) (*AskResponse, error) {
	body := openAIRequest{
		Model: p.model,
		Messages: []openAIMessage{
			{Role: "system", Content: prompts.SystemPrompt},
			{Role: "user", Content: prompts.BuildUserPrompt(req.Question, req.GraphContext)},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var or openAIResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("openai authentication failed: invalid OPENAI_API_KEY")
	}
	if or.Error != nil {
		return nil, fmt.Errorf("openai error: %s", or.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai request failed (%d): %s", resp.StatusCode, string(raw))
	}
	if len(or.Choices) == 0 {
		return nil, fmt.Errorf("openai returned an empty response")
	}

	return parseStructuredAnswer(or.Choices[0].Message.Content), nil
}
