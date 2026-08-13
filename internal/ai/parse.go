package ai

import (
	"regexp"
	"strings"
)

var mermaidBlockRe = regexp.MustCompile("(?s)```mermaid\\s*(.*?)```")

// parseStructuredAnswer splits the model's raw text response into the
// EXPLANATION / MERMAID / ASCII sections described in prompts.SystemPrompt.
// It degrades gracefully: if the model didn't follow the format exactly,
// the whole response is returned as Explanation.
func parseStructuredAnswer(raw string) *AskResponse {
	resp := &AskResponse{}

	if m := mermaidBlockRe.FindStringSubmatch(raw); len(m) == 2 {
		resp.Mermaid = strings.TrimSpace(m[1])
	}

	explanation := raw
	if idx := strings.Index(raw, "EXPLANATION"); idx >= 0 {
		explanation = raw[idx+len("EXPLANATION"):]
		explanation = strings.TrimLeft(explanation, ":\n ")
	}
	if idx := strings.Index(explanation, "MERMAID"); idx >= 0 {
		explanation = explanation[:idx]
	} else if idx := strings.Index(explanation, "```mermaid"); idx >= 0 {
		explanation = explanation[:idx]
	}
	resp.Explanation = strings.TrimSpace(explanation)

	if idx := strings.Index(raw, "ASCII"); idx >= 0 {
		ascii := raw[idx+len("ASCII"):]
		ascii = strings.TrimLeft(ascii, ":\n ")
		ascii = strings.TrimPrefix(strings.TrimSpace(ascii), "```")
		ascii = strings.TrimSuffix(strings.TrimSpace(ascii), "```")
		resp.ASCII = strings.TrimSpace(ascii)
	}

	if resp.Explanation == "" {
		resp.Explanation = strings.TrimSpace(raw)
	}
	return resp
}
