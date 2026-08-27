package rules

import "github.com/cloud-ct/graphsentry/internal/security"

// Default returns every built-in Rule GraphSentry ships. Callers (the CLI,
// the VS Code extension) should go through this instead of listing
// individual rules themselves — adding a new Rule to this package means
// adding it here once, and every caller picks it up automatically.
func Default() []security.Rule {
	return []security.Rule{
		AspNetAuthorize{},
		AspNetCustomFilter{},
	}
}
