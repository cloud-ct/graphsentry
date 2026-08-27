package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cloud-ct/graphsentry/internal/graph"
	"github.com/cloud-ct/graphsentry/internal/security"
	"github.com/cloud-ct/graphsentry/internal/security/rules"
)

// newSecurityCmd groups deterministic, no-LLM security checks over the code
// graph — the same "graph traversal, not a guess" philosophy as impact/
// coupling/flow, applied to a security question instead of an architecture
// one. See internal/security's package doc for how a new check gets added.
func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Deterministic security checks over the code graph (no LLM)",
	}
	cmd.AddCommand(newSecurityEndpointsCmd())
	return cmd
}

func newSecurityEndpointsCmd() *cobra.Command {
	var repoFlag string
	var asJSON bool
	var failOnUnprotected bool
	var statusFlag string
	cmd := &cobra.Command{
		Use:   "endpoints",
		Short: "List HTTP endpoints and whether an auth guard was found for each (deterministic, no LLM)",
		Long: "Runs every registered internal/security.Rule (currently: ASP.NET's\n" +
			"[Authorize]/[AllowAnonymous], and custom TypeFilterAttribute-backed\n" +
			"filters) against the analyzed graph and reports each endpoint's status:\n" +
			"protected, public (an explicit [AllowAnonymous]-style override), unprotected\n" +
			"(no guard found — the finding to act on), or unknown (no rule understands\n" +
			"this endpoint's language yet).\n\n" +
			"This is a structural signal, not a vulnerability scan: it reports what\n" +
			"guard attributes/decorators/filters are present in source, not whether\n" +
			"they're correctly configured or enforced at runtime.",
		Args: cobra.NoArgs,
		// --fail-on-unprotected is meant for CI: the exit code and the
		// message above are the actionable output, so don't also dump the
		// full flags/usage block on a legitimate "found issues" result.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var wantStatus security.Status
			if statusFlag != "" {
				wantStatus = security.Status(statusFlag)
				switch wantStatus {
				case security.StatusProtected, security.StatusPublic, security.StatusUnprotected, security.StatusUnknown:
				default:
					return fmt.Errorf("invalid --status %q (want one of: protected, public, unprotected, unknown)", statusFlag)
				}
			}

			target, err := resolveTarget(repoFlag)
			if err != nil {
				return err
			}
			dbPath, err := requireDB(target)
			if err != nil {
				return err
			}
			store, err := graph.OpenStore(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			g, err := store.Load()
			if err != nil {
				return err
			}

			findings := security.Analyze(g, rules.Default()...)
			if wantStatus != "" {
				findings = filterByStatus(findings, wantStatus)
			}
			sort.Slice(findings, func(i, j int) bool {
				a, b := findings[i].Endpoint, findings[j].Endpoint
				if a.File != b.File {
					return a.File < b.File
				}
				return a.StartLine < b.StartLine
			})

			if asJSON {
				if err := printJSON(findings); err != nil {
					return err
				}
			} else {
				printEndpointFindings(findings)
			}

			if unprotected := countStatus(findings, security.StatusUnprotected); failOnUnprotected && unprotected > 0 {
				return fmt.Errorf("%d endpoint(s) found with no auth guard", unprotected)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "repository to query (default: last analyzed)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output structured JSON instead of the human-readable rendering")
	cmd.Flags().BoolVar(&failOnUnprotected, "fail-on-unprotected", false, "exit with a non-zero status if any endpoint has no auth guard — for CI gates")
	cmd.Flags().StringVar(&statusFlag, "status", "", "only show endpoints with this status: protected, public, unprotected, unknown")
	return cmd
}

func filterByStatus(findings []security.EndpointFinding, status security.Status) []security.EndpointFinding {
	var out []security.EndpointFinding
	for _, f := range findings {
		if f.Status == status {
			out = append(out, f)
		}
	}
	return out
}

func countStatus(findings []security.EndpointFinding, status security.Status) int {
	n := 0
	for _, f := range findings {
		if f.Status == status {
			n++
		}
	}
	return n
}

func printEndpointFindings(findings []security.EndpointFinding) {
	if len(findings) == 0 {
		fmt.Println("(no endpoints found)")
		return
	}
	fmt.Printf("%-8s %-40s %-12s %-30s %s\n", "METHOD", "ROUTE", "STATUS", "GUARD", "FILE")
	for _, f := range findings {
		method, route := splitEndpointName(f.Endpoint.Name)
		guard := "—"
		if len(f.Guards) > 0 {
			names := make([]string, len(f.Guards))
			for i, gm := range f.Guards {
				names[i] = gm.GuardName
			}
			guard = strings.Join(names, ", ")
		}
		loc := fmt.Sprintf("%s:%d", f.Endpoint.File, f.Endpoint.StartLine)
		fmt.Printf("%-8s %-40s %-12s %-30s %s\n", method, truncate(route, 40), f.Status, truncate(guard, 30), loc)
	}

	unprotected := countStatus(findings, security.StatusUnprotected)
	if unprotected > 0 {
		fmt.Fprintf(os.Stderr, "\n%d endpoint(s) with no auth guard detected — this is a structural signal, not a vulnerability scan; verify each before treating it as a finding.\n", unprotected)
	}
}

// splitEndpointName splits an endpoint node's "VERB route" name (the
// convention every LanguageAnalyzer's endpoint detection uses) into its two
// display columns.
func splitEndpointName(name string) (method, route string) {
	method, route, ok := strings.Cut(name, " ")
	if !ok {
		return "", name
	}
	return method, route
}
