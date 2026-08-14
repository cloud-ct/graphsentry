package main

import (
	"encoding/json"
	"fmt"
)

// printJSON marshals v as indented JSON to stdout. Used by --json on the
// deterministic commands (impact, coupling, flow) so external tools —
// notably the VSCode extension — can consume structured output instead of
// parsing the human-readable terminal rendering.
func printJSON(v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	fmt.Println(string(b))
	return nil
}
