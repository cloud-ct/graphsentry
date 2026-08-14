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

	"github.com/cloud-ct/repolens/internal/ai/prompts"
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
	Stream   bool            `json:"stream,omitempty"`
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
	defer func() { _ = resp.Body.Close() }()

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

// AskStream mirrors Ask but reads OpenAI's SSE stream (each "data: {...}"
// line carries a delta.content chunk; the stream ends with "data: [DONE]"),
// invoking onDelta with each chunk as it arrives.
func (p *openAIProvider) AskStream(ctx context.Context, req AskRequest, onDelta func(string)) (*AskResponse, error) {
	body := openAIRequest{
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("openai authentication failed: invalid OPENAI_API_KEY")
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai request failed (%d): %s", resp.StatusCode, string(raw))
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	streamErr := forEachSSEDataLine(scanner, func(data string) bool {
		if data == "[DONE]" {
			return true
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return false
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			full.WriteString(chunk.Choices[0].Delta.Content)
			onDelta(chunk.Choices[0].Delta.Content)
		}
		return false
	})
	if streamErr != nil {
		return nil, fmt.Errorf("read openai stream: %w", streamErr)
	}
	if full.Len() == 0 {
		return nil, fmt.Errorf("openai returned an empty streamed response")
	}

	return parseStructuredAnswer(full.String()), nil
}
