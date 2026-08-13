import * as vscode from "vscode";
import { RepoLensClient, RepoLensError } from "./repolens";
import { RepoLensCodeLensProvider } from "./codeLensProvider";

let client: RepoLensClient;
let outputChannel: vscode.OutputChannel;
let codeLensProvider: RepoLensCodeLensProvider;

export function activate(context: vscode.ExtensionContext) {
  client = new RepoLensClient(context);
  outputChannel = vscode.window.createOutputChannel("RepoLens");
  context.subscriptions.push(outputChannel);

  codeLensProvider = new RepoLensCodeLensProvider(client);
  context.subscriptions.push(
    vscode.languages.registerCodeLensProvider({ scheme: "file" }, codeLensProvider)
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("repolens.analyze", () => runAnalyze()),
    vscode.commands.registerCommand("repolens.coupling", () => runCoupling()),
    vscode.commands.registerCommand("repolens.impact", (symbol?: string) => runImpact(symbol)),
    vscode.commands.registerCommand("repolens.flow", (symbol?: string) => runFlow(symbol)),
    vscode.commands.registerCommand("repolens.ask", () => runAsk())
  );

  // Pick up a repo analyzed in a previous session so CodeLenses appear
  // without requiring the user to re-run Analyze every time they open the
  // workspace. Silent on failure (e.g. never analyzed yet).
  const repoPath = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  if (repoPath) {
    void codeLensProvider.refresh(repoPath);
  }
}

export function deactivate() {}

function currentWorkspacePath(): string | undefined {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders || folders.length === 0) {
    vscode.window.showErrorMessage("RepoLens: open a folder or workspace first.");
    return undefined;
  }
  // Multi-root workspaces: default to the first folder. Good enough for
  // v1 — most repos analyzed with RepoLens are single-root.
  return folders[0].uri.fsPath;
}

async function withErrorHandling<T>(action: () => Promise<T>): Promise<T | undefined> {
  try {
    return await action();
  } catch (err) {
    const message = err instanceof RepoLensError ? err.message : String(err);
    vscode.window.showErrorMessage(`RepoLens: ${message}`);
    outputChannel.appendLine(message);
    return undefined;
  }
}

async function runAnalyze() {
  const repoPath = currentWorkspacePath();
  if (!repoPath) return;

  await withErrorHandling(async () => {
    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: "RepoLens: analyzing workspace..." },
      () => client.analyze(repoPath)
    );
    vscode.window.showInformationMessage("RepoLens: analysis complete.");
    await codeLensProvider.refresh(repoPath);
  });
}

async function runCoupling() {
  const repoPath = currentWorkspacePath();
  if (!repoPath) return;

  const scores = await withErrorHandling(() => client.coupling(repoPath, 20));
  if (!scores) return;

  if (scores.length === 0) {
    vscode.window.showInformationMessage("RepoLens: no coupling data. Run 'RepoLens: Analyze Workspace' first.");
    return;
  }

  const items = scores.map((s) => ({
    label: `${s.node.name}`,
    description: `${s.node.kind} · fan-in ${s.fan_in} · fan-out ${s.fan_out} · total ${s.total}`,
    detail: s.node.file,
    node: s.node,
  }));

  const picked = await vscode.window.showQuickPick(items, {
    title: "Most coupled symbols (fan-in + fan-out)",
    matchOnDescription: true,
  });
  if (picked) {
    await openNode(repoPath, picked.node);
  }
}

async function symbolAtCursor(): Promise<string | undefined> {
  const editor = vscode.window.activeTextEditor;
  if (editor) {
    const wordRange = editor.document.getWordRangeAtPosition(editor.selection.active);
    if (wordRange) {
      return editor.document.getText(wordRange);
    }
  }
  return vscode.window.showInputBox({ prompt: "Symbol name to analyze" });
}

// CodeLens commands pass the full internal node ID ("symbol::path::Qualified.Name")
// so the CLI resolves unambiguously; for display, show just the trailing name.
function displayName(symbolOrId: string): string {
  const parts = symbolOrId.split("::");
  return parts[parts.length - 1];
}

async function runImpact(symbolArg?: string) {
  const repoPath = currentWorkspacePath();
  if (!repoPath) return;

  const symbol = symbolArg ?? (await symbolAtCursor());
  if (!symbol) return;

  const result = await withErrorHandling(() => client.impact(repoPath, symbol));
  if (!result) return;

  if (result.impacted.length === 0) {
    vscode.window.showInformationMessage(`RepoLens: nothing depends on "${displayName(symbol)}" — safe to change in isolation.`);
    return;
  }

  const items = result.impacted.map((i) => ({
    label: `${"  ".repeat(i.distance - 1)}↳ ${i.node.name}`,
    description: `depth ${i.distance} · via ${i.via} · ${i.node.kind}`,
    detail: i.node.file,
    node: i.node,
  }));

  const picked = await vscode.window.showQuickPick(items, {
    title: `What depends on "${displayName(symbol)}" (${result.impacted.length} impacted)`,
  });
  if (picked) {
    await openNode(repoPath, picked.node);
  }
}

async function runFlow(symbolArg?: string) {
  const repoPath = currentWorkspacePath();
  if (!repoPath) return;

  const symbol = symbolArg ?? (await symbolAtCursor());
  if (!symbol) return;

  const result = await withErrorHandling(() => client.flow(repoPath, symbol));
  if (!result) return;

  showFlowPanel(displayName(symbol), result.ascii, result.mermaid);
}

function showFlowPanel(symbol: string, ascii: string, mermaid: string) {
  const panel = vscode.window.createWebviewPanel(
    "repolensFlow",
    `RepoLens: flow of ${symbol}`,
    vscode.ViewColumn.Beside,
    { enableScripts: true }
  );
  panel.webview.html = flowHtml(symbol, ascii, mermaid);
}

// NOTE: loads mermaid.js from a CDN, so the diagram render needs network
// access — the graph computation itself (repolens flow --json) is fully
// local/offline. Bundling mermaid.js into the extension would remove this
// last network dependency; left as a follow-up since it doesn't affect
// RepoLens's core privacy guarantee (only the diagram *renderer* is
// remote, no code or graph data is sent anywhere).
function flowHtml(symbol: string, ascii: string, mermaid: string): string {
  const escapedAscii = ascii.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const escapedMermaid = mermaid.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src https://cdn.jsdelivr.net 'unsafe-inline'; style-src 'unsafe-inline'; connect-src https://cdn.jsdelivr.net;">
<style>
  body { font-family: var(--vscode-font-family); padding: 1rem; color: var(--vscode-foreground); }
  pre { background: var(--vscode-textCodeBlock-background); padding: 1rem; border-radius: 6px; overflow-x: auto; }
  h2 { font-weight: 600; }
</style>
<script type="module">
  import mermaid from "https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.esm.min.mjs";
  mermaid.initialize({ startOnLoad: true, theme: "dark" });
</script>
</head>
<body>
  <h2>Call flow: ${symbol}</h2>
  <pre class="mermaid">${escapedMermaid}</pre>
  <h2>ASCII</h2>
  <pre>${escapedAscii}</pre>
</body>
</html>`;
}

async function runAsk() {
  const repoPath = currentWorkspacePath();
  if (!repoPath) return;

  const question = await vscode.window.showInputBox({
    prompt: "Ask a question about this codebase's architecture",
    placeHolder: "How does user creation work?",
  });
  if (!question) return;

  const answer = await withErrorHandling(async () =>
    vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: "RepoLens: thinking..." },
      () => client.ask(repoPath, question)
    )
  );
  if (!answer) return;

  outputChannel.clear();
  outputChannel.appendLine(`Q: ${question}\n`);
  outputChannel.appendLine(answer);
  outputChannel.show();
}

async function openNode(repoPath: string, node: { file: string; start_line: number }) {
  if (!node.file) return;
  const uri = vscode.Uri.joinPath(vscode.Uri.file(repoPath), node.file);
  const doc = await vscode.workspace.openTextDocument(uri);
  const editor = await vscode.window.showTextDocument(doc);
  const line = Math.max(0, (node.start_line || 1) - 1);
  const pos = new vscode.Position(line, 0);
  editor.selection = new vscode.Selection(pos, pos);
  editor.revealRange(new vscode.Range(pos, pos), vscode.TextEditorRevealType.InCenter);
}
