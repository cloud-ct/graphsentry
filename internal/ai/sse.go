package ai

import (
	"bufio"
	"strings"
)

// forEachSSEDataLine scans a Server-Sent-Events stream, invoking fn with
// the JSON payload of each "data: ..." line until fn returns true (stop)
// or the stream ends. Shared by the Anthropic and OpenAI streaming
// implementations, which both speak SSE despite their per-chunk JSON
// shapes differing.
func forEachSSEDataLine(scanner *bufio.Scanner, fn func(data string) (stop bool)) error {
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if fn(data) {
			return nil
		}
	}
	return scanner.Err()
}
