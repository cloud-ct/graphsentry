package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/huandert/repolens/internal/ai/prompts"
)

const defaultOllamaModel = "llama3.1"

// ollamaProvider talks to a local Ollama daemon — the fully local, no
// external API option for users who don't want any code sent off-machine
// at all.
type ollamaProvider struct {
	host   string
	model  string
	client *http.Client
}

func newOllamaProvider(host, model string) Provider {
	if model == "" {
		model = defaultOllamaModel
	}
	return &ollamaProvider{host: strings.TrimRight(host, "/"), model: model, client: &http.Client{Timeout: 180 * time.Second}}
}

func (p *ollamaProvider) Name() string { return "ollama" }

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"` // Ollama's chat API mirrors OpenAI's message shape
	Stream   bool            `json:"stream"`
}

type ollamaResponse struct {
	Message openAIMessage `json:"message"`
	Error   string        `json:"error"`
}

func (p *ollamaProvider) Ask(ctx context.Context, req AskRequest) (*AskResponse, error) {
	body := ollamaRequest{
		Model: p.model,
		Messages: []openAIMessage{
			{Role: "system", Content: prompts.SystemPrompt},
			{Role: "user", Content: prompts.BuildUserPrompt(req.Question, req.GraphContext)},
		},
		Stream: false,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("could not reach Ollama at %s: %w\nIs it running? Start it with `ollama serve` and pull a model with `ollama pull %s`", p.host, err, p.model)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var or ollamaResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	if or.Error != "" {
		return nil, fmt.Errorf("ollama error: %s\nDo you have the model pulled? Try: ollama pull %s", or.Error, p.model)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama request failed (%d): %s", resp.StatusCode, string(raw))
	}

	return parseStructuredAnswer(or.Message.Content), nil
}
