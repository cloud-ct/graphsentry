// Command graphsentry is the CLI entry point for GraphSentry: analyze a git
// repository into a local code graph, then explore it deterministically
// (impact, coupling, flow) or, with a BYOK LLM key configured, in natural
// language via `ask`.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
