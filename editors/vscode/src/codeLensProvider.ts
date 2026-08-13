// Shows a CodeLens above every analyzed symbol ("3 dependents · 5
// dependencies" / "Show flow"), so coupling and impact are visible without
// opening the command palette. Backed entirely by `repolens coupling
// --json` — no separate computation, just a cache keyed by file so
// provideCodeLenses stays synchronous-fast (VS Code calls it on every
// keystroke-adjacent re-render).
import * as vscode from "vscode";
import * as path from "path";
import { RepoLensClient, CouplingScore } from "./repolens";

export class RepoLensCodeLensProvider implements vscode.CodeLensProvider {
  private readonly _onDidChangeCodeLenses = new vscode.EventEmitter<void>();
  readonly onDidChangeCodeLenses = this._onDidChangeCodeLenses.event;

  // repoPath -> (workspace-relative file path -> symbols in that file)
  private cache = new Map<string, Map<string, CouplingScore[]>>();

  constructor(private client: RepoLensClient) {}

  /** Re-fetches coupling data for repoPath and fires a refresh. Call after
   * `repolens analyze` completes, or silently on activation to pick up a
   * repo analyzed in a previous session. */
  async refresh(repoPath: string): Promise<void> {
    try {
      const scores = await this.client.coupling(repoPath, 0); // 0 = all symbols, not just top-N
      const byFile = new Map<string, CouplingScore[]>();
      for (const s of scores) {
        if (!s.node.file || s.node.kind === "file") continue; // file-level rows have no meaningful line
        const list = byFile.get(s.node.file) ?? [];
        list.push(s);
        byFile.set(s.node.file, list);
      }
      this.cache.set(repoPath, byFile);
      this._onDidChangeCodeLenses.fire();
    } catch {
      // No analyzed graph yet, or repo not found — leave the cache as-is
      // (likely empty) and stay quiet. The user gets clear errors from the
      // explicit "Analyze Workspace" command instead.
    }
  }

  provideCodeLenses(document: vscode.TextDocument): vscode.CodeLens[] {
    const folder = vscode.workspace.getWorkspaceFolder(document.uri);
    if (!folder) return [];
    const repoPath = folder.uri.fsPath;

    const byFile = this.cache.get(repoPath);
    if (!byFile) return [];

    const relFile = path.relative(repoPath, document.uri.fsPath).split(path.sep).join("/");
    const symbols = byFile.get(relFile);
    if (!symbols || symbols.length === 0) return [];

    const lenses: vscode.CodeLens[] = [];
    for (const s of symbols) {
      const line = Math.max(0, (s.node.start_line || 1) - 1);
      const range = new vscode.Range(line, 0, line, 0);

      lenses.push(
        new vscode.CodeLens(range, {
          title: `$(references) ${s.fan_in} dependent${s.fan_in === 1 ? "" : "s"} · ${s.fan_out} dependenc${s.fan_out === 1 ? "y" : "ies"}`,
          command: "repolens.impact",
          arguments: [s.node.id],
        })
      );
      lenses.push(
        new vscode.CodeLens(range, {
          title: "$(graph) Show flow",
          command: "repolens.flow",
          arguments: [s.node.id],
        })
      );
    }
    return lenses;
  }
}
