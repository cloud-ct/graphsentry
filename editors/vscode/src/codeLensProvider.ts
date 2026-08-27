// Shows a CodeLens above every analyzed symbol ("3 dependents" / "5
// dependencies" / "Show flow"), so coupling and impact are visible without
// opening the command palette. Backed entirely by `graphsentry coupling
// --json` — no separate computation, just a cache keyed by file so
// provideCodeLenses stays synchronous-fast (VS Code calls it on every
// keystroke-adjacent re-render).
import * as vscode from "vscode";
import * as path from "path";
import { GraphSentryClient, CouplingScore, EndpointFinding } from "./client";

/** One line's worth of CodeLens data — the result of merging every graph
 * symbol that happens to share a source line (see mergeLineGroup) into a
 * single, consistent display. */
interface LineEntry {
  line: number; // 1-based, matching GraphNode.start_line
  fanIn: number;
  fanOut: number;
  impactTargetId: string;
  flowTargetId: string;
}

/** One endpoint reported "unprotected" by `graphsentry security endpoints`
 * — the only status this CodeLens surfaces (see refreshSecurity). */
interface UnprotectedEntry {
  line: number; // 1-based, matching GraphNode.start_line
  route: string; // e.g. "GET /users", for the click-through message
  file: string;
}

export class GraphSentryCodeLensProvider implements vscode.CodeLensProvider {
  private readonly _onDidChangeCodeLenses = new vscode.EventEmitter<void>();
  readonly onDidChangeCodeLenses = this._onDidChangeCodeLenses.event;

  // repoPath -> (workspace-relative file path -> merged per-line entries)
  private cache = new Map<string, Map<string, LineEntry[]>>();
  // repoPath -> (workspace-relative file path -> unprotected endpoints)
  private securityCache = new Map<string, Map<string, UnprotectedEntry[]>>();

  constructor(private client: GraphSentryClient) {}

  /** Re-fetches coupling data for repoPath and fires a refresh. Call after
   * `graphsentry analyze` completes, or silently on activation to pick up a
   * repo analyzed in a previous session. */
  async refresh(repoPath: string): Promise<void> {
    try {
      const scores = await this.client.coupling(repoPath, 0); // 0 = all symbols, not just top-N
      const byFile = new Map<string, LineEntry[]>();

      // Group raw graph symbols by (file, line) first: an HTTP-endpoint
      // symbol and the method that handles it are always emitted on the
      // exact same source line (that's how the analyzers construct an
      // endpoint — a routing alias over its handler), so without this
      // grouping step they'd render as two independent, confusingly
      // inconsistent-looking CodeLens pairs stacked on one line.
      const byFileLine = new Map<string, CouplingScore[]>();
      for (const s of scores) {
        if (!s.node.file || s.node.kind === "file" || !s.node.start_line) continue;
        const key = `${s.node.file} ${s.node.start_line}`;
        const list = byFileLine.get(key) ?? [];
        list.push(s);
        byFileLine.set(key, list);
      }

      for (const group of byFileLine.values()) {
        const entry = mergeLineGroup(group);
        const file = group[0].node.file;
        const list = byFile.get(file) ?? [];
        list.push(entry);
        byFile.set(file, list);
      }

      this.cache.set(repoPath, byFile);
      this._onDidChangeCodeLenses.fire();
    } catch {
      // No analyzed graph yet, or repo not found — leave the cache as-is
      // (likely empty) and stay quiet. The user gets clear errors from the
      // explicit "Analyze Workspace" command instead.
    }
  }

  /** Re-fetches `graphsentry security endpoints` for repoPath — separate
   * from refresh() (a different CLI command, a different cache) but the
   * same call sites: after `analyze` completes, and silently on
   * activation. Only StatusUnprotected findings are cached: "protected"
   * repeated over every guarded endpoint would be noise, and "unknown"
   * would fire on every endpoint in a language with no security.Rule yet
   * (which is most of them today) — see internal/security's package doc.
   */
  async refreshSecurity(repoPath: string): Promise<void> {
    try {
      const findings = await this.client.securityEndpoints(repoPath);
      const byFile = new Map<string, UnprotectedEntry[]>();
      for (const f of findings) {
        if (f.status !== "unprotected" || !f.endpoint.file || !f.endpoint.start_line) continue;
        const list = byFile.get(f.endpoint.file) ?? [];
        list.push({ line: f.endpoint.start_line, route: f.endpoint.name, file: f.endpoint.file });
        byFile.set(f.endpoint.file, list);
      }
      this.securityCache.set(repoPath, byFile);
      this._onDidChangeCodeLenses.fire();
    } catch {
      // Same reasoning as refresh(): no analyzed graph yet, or a CLI
      // version that predates this command — stay quiet, leave the cache
      // as-is.
    }
  }

  /** Forces VS Code to re-request CodeLenses without refetching data —
   * used when the "graphsentry.codeLens.enabled" setting changes, since that
   * only affects rendering, not the underlying coupling data. */
  notifyChanged(): void {
    this._onDidChangeCodeLenses.fire();
  }

  provideCodeLenses(document: vscode.TextDocument): vscode.CodeLens[] {
    const enabled = vscode.workspace.getConfiguration("graphsentry").get<boolean>("codeLens.enabled", true);
    if (!enabled) return [];

    const folder = vscode.workspace.getWorkspaceFolder(document.uri);
    if (!folder) return [];
    const repoPath = folder.uri.fsPath;

    const relFile = path.relative(repoPath, document.uri.fsPath).split(path.sep).join("/");
    const entries = this.cache.get(repoPath)?.get(relFile);

    const lenses: vscode.CodeLens[] = [];

    const unprotected = this.securityCache.get(repoPath)?.get(relFile);
    if (unprotected) {
      for (const u of unprotected) {
        const line = Math.max(0, u.line - 1);
        lenses.push(
          new vscode.CodeLens(new vscode.Range(line, 0, line, 0), {
            title: "⚠ No auth guard detected",
            command: "graphsentry.securityEndpointInfo",
            arguments: [u],
          })
        );
      }
    }

    if (!entries || entries.length === 0) return lenses;

    for (const entry of entries) {
      const line = Math.max(0, entry.line - 1);
      const range = new vscode.Range(line, 0, line, 0);

      // Plain text, no $(codicon) prefix: CodeLens titles render codicons
      // flush against the following text with no automatic gap, which
      // reads as glued-together at typical font sizes. Not worth fighting
      // with manual padding characters that'd look inconsistent across
      // themes/fonts — plain text is the more reliable choice here.
      //
      // Two separate CodeLens entries, not one combined "N dependents · M
      // dependencies" label: they used to be a single lens whose title
      // showed both numbers but whose command only ever opened the
      // dependents (fan-in) list — the dependencies (fan-out) number had
      // no way to be clicked through to its own list at all.
      lenses.push(
        new vscode.CodeLens(range, {
          title: `${entry.fanIn} dependent${entry.fanIn === 1 ? "" : "s"}`,
          command: "graphsentry.impact",
          arguments: [entry.impactTargetId],
        })
      );
      lenses.push(
        new vscode.CodeLens(range, {
          title: `${entry.fanOut} dependenc${entry.fanOut === 1 ? "y" : "ies"}`,
          command: "graphsentry.dependencies",
          arguments: [entry.impactTargetId],
        })
      );
      lenses.push(
        new vscode.CodeLens(range, {
          title: "Show flow",
          command: "graphsentry.flow",
          arguments: [entry.flowTargetId],
        })
      );
    }
    return lenses;
  }
}

/**
 * Merges every graph symbol sharing one source line into a single
 * LineEntry. In the common case (one symbol, one line) this is a
 * pass-through. The case that matters is an "endpoint" node paired with
 * its handler (method/function): an endpoint is a thin routing alias whose
 * own fan-in is always 0 (nothing in-repo "calls" a route by URL) and
 * fan-out is always exactly 1 (the handler) — showing those numbers inline
 * would be structurally uninformative and, worse, inconsistent-looking
 * next to the handler's own real stats. So:
 *   - the "N dependents" / "M dependencies" stats use the handler's real
 *     fan-in/fan-out (both target the handler's id, so the numbers shown
 *     and the numbers `graphsentry impact`/`graphsentry dependencies`
 *     report agree), and
 *   - "Show flow" targets the endpoint when one exists, since starting
 *     the flow diagram at the named route ("GET /users") is the more
 *     useful, more recognizable entry point for exploration — it leads
 *     to the exact same downstream graph as starting at the handler,
 *     just with that one extra, meaningful root node.
 */
function mergeLineGroup(group: CouplingScore[]): LineEntry {
  const first = group[0];
  if (group.length === 1) {
    return {
      line: first.node.start_line,
      fanIn: first.fan_in,
      fanOut: first.fan_out,
      impactTargetId: first.node.id,
      flowTargetId: first.node.id,
    };
  }

  const endpoint = group.find((g) => g.node.kind === "endpoint");
  const handler = group.find((g) => g.node.kind !== "endpoint");

  if (endpoint && handler) {
    return {
      line: handler.node.start_line,
      fanIn: handler.fan_in,
      fanOut: handler.fan_out,
      impactTargetId: handler.node.id,
      flowTargetId: endpoint.node.id,
    };
  }

  // Unexpected shape (multiple non-endpoint symbols sharing an exact
  // line — none of the current analyzers produce this) — rather than
  // silently dropping data, surface whichever is most coupled.
  const best = group.reduce((a, b) => (b.fan_in + b.fan_out > a.fan_in + a.fan_out ? b : a));
  return {
    line: best.node.start_line,
    fanIn: best.fan_in,
    fanOut: best.fan_out,
    impactTargetId: best.node.id,
    flowTargetId: best.node.id,
  };
}
