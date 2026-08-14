package ai

import (
	"bufio"
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

// AskStream mirrors Ask but reads Ollama's streamed response: unlike
// Anthropic/OpenAI's SSE, Ollama's /api/chat with stream:true sends plain
// newline-delimited JSON objects, each a partial ollamaResponse — a
// message.content chunk, until one arrives with done:true.
func (p *ollamaProvider) AskStream(ctx context.Context, req AskRequest, onDelta func(string)) (*AskResponse, error) {
	body := ollamaRequest{
		Model: p.model,
		Messages: []openAIMessage{
			{Role: "system", Content: prompts.SystemPrompt},
			{Role: "user", Content: prompts.BuildUserPrompt(req.Question, req.GraphContext)},
		},
		Stream: true,
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

	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama request failed (%d): %s", resp.StatusCode, string(raw))
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk ollamaResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue // ignore a malformed line rather than aborting an otherwise-good stream
		}
		if chunk.Error != "" {
			return nil, fmt.Errorf("ollama error: %s\nDo you have the model pulled? Try: ollama pull %s", chunk.Error, p.model)
		}
		if chunk.Message.Content != "" {
			full.WriteString(chunk.Message.Content)
			onDelta(chunk.Message.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read ollama stream: %w", err)
	}
	if full.Len() == 0 {
		return nil, fmt.Errorf("ollama returned an empty streamed response")
	}

	return parseStructuredAnswer(full.String()), nil
}
