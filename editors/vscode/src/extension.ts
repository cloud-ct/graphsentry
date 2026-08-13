import * as vscode from "vscode";
import { RepoLensClient, RepoLensError, displayName } from "./repolens";
import { RepoLensCodeLensProvider } from "./codeLensProvider";
import { configureProvider, clearProvider, hasProviderConfigured } from "./config";
import { RepoLensSidebarProvider } from "./sidebarView";

let client: RepoLensClient;
let outputChannel: vscode.OutputChannel;
let codeLensProvider: RepoLensCodeLensProvider;
let extensionUri: vscode.Uri;
let extContext: vscode.ExtensionContext;

export function activate(context: vscode.ExtensionContext) {
  extContext = context;
  extensionUri = context.extensionUri;
  client = new RepoLensClient(context);
  outputChannel = vscode.window.createOutputChannel("RepoLens");
  context.subscriptions.push(outputChannel);

  codeLensProvider = new RepoLensCodeLensProvider(client);
  context.subscriptions.push(
    vscode.languages.registerCodeLensProvider({ scheme: "file" }, codeLensProvider)
  );
  context.subscriptions.push(
    vscode.window.registerTreeDataProvider("repolens.commands", new RepoLensSidebarProvider())
  );
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("repolens.codeLens.enabled")) {
        codeLensProvider.notifyChanged();
      }
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("repolens.analyze", () => runAnalyze()),
    vscode.commands.registerCommand("repolens.coupling", () => runCoupling()),
    vscode.commands.registerCommand("repolens.impact", (symbol?: string) => runImpact(symbol)),
    vscode.commands.registerCommand("repolens.flow", (symbol?: string) => runFlow(symbol)),
    vscode.commands.registerCommand("repolens.ask", () => runAsk()),
    vscode.commands.registerCommand("repolens.configureProvider", () => configureProvider(context)),
    vscode.commands.registerCommand("repolens.clearProvider", () => clearProvider(context))
  );

  // Pick up a repo analyzed in a previous session so CodeLenses appear
  // without requiring the user to re-run Analyze every time they open the
  // workspace. Silent on failure (e.g. never analyzed yet). In a
  // multi-root workspace, prime the cache for every folder — the
  // CodeLensProvider looks up by the folder that actually owns the open
  // document, not just the first one.
  for (const folder of vscode.workspace.workspaceFolders ?? []) {
    void codeLensProvider.refresh(folder.uri.fsPath);
  }
}

export function deactivate() {}

/**
 * Resolves which workspace folder a command should act on: the folder
 * containing the active editor's file if there is one (correct behavior
 * in a multi-root workspace), the sole folder if there's only one, or a
 * picker if there are several and no active editor to disambiguate from.
 */
async function currentWorkspacePath(): Promise<string | undefined> {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders || folders.length === 0) {
    vscode.window.showErrorMessage("RepoLens: open a folder or workspace first.");
    return undefined;
  }
  if (folders.length === 1) {
    return folders[0].uri.fsPath;
  }

  const activeUri = vscode.window.activeTextEditor?.document.uri;
  if (activeUri) {
    const owning = vscode.workspace.getWorkspaceFolder(activeUri);
    if (owning) return owning.uri.fsPath;
  }

  const picked = await vscode.window.showWorkspaceFolderPick({ placeHolder: "Which workspace folder should RepoLens use?" });
  return picked?.uri.fsPath;
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
  const repoPath = await currentWorkspacePath();
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
  const repoPath = await currentWorkspacePath();
  if (!repoPath) return;

  const scores = await withErrorHandling(() => client.coupling(repoPath, 20));
  if (!scores) return;

  if (scores.length === 0) {
    vscode.window.showInformationMessage("RepoLens: no coupling data. Run 'RepoLens: Analyze Workspace' first.");
    return;
  }

  const items = scores.map((s) => ({
    label: displayName(s.node),
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
// so the CLI resolves unambiguously; for display before the CLI has
// resolved it to a full GraphNode (i.e. we still only have the ID/name
// string, not the node), show just the trailing qualified part.
function symbolLabel(symbolOrId: string): string {
  const parts = symbolOrId.split("::");
  return parts[parts.length - 1];
}

async function runImpact(symbolArg?: string) {
  const repoPath = await currentWorkspacePath();
  if (!repoPath) return;

  const symbol = symbolArg ?? (await symbolAtCursor());
  if (!symbol) return;

  const result = await withErrorHandling(() => client.impact(repoPath, symbol));
  if (!result) return;

  if (result.impacted.length === 0) {
    vscode.window.showInformationMessage(`RepoLens: nothing depends on "${symbolLabel(symbol)}" — safe to change in isolation.`);
    return;
  }

  const items = result.impacted.map((i) => ({
    label: `${"  ".repeat(i.distance - 1)}↳ ${displayName(i.node)}`,
    description: `depth ${i.distance} · via ${i.via} · ${i.node.kind}`,
    detail: i.node.file,
    node: i.node,
  }));

  const picked = await vscode.window.showQuickPick(items, {
    title: `What depends on "${symbolLabel(symbol)}" (${result.impacted.length} impacted)`,
  });
  if (picked) {
    await openNode(repoPath, picked.node);
  }
}

async function runFlow(symbolArg?: string) {
  const repoPath = await currentWorkspacePath();
  if (!repoPath) return;

  const symbol = symbolArg ?? (await symbolAtCursor());
  if (!symbol) return;

  const result = await withErrorHandling(() => client.flow(repoPath, symbol));
  if (!result) return;

  showFlowPanel(symbolLabel(symbol), result.ascii, result.mermaid);
}

function showFlowPanel(symbol: string, ascii: string, mermaid: string) {
  const mediaDir = vscode.Uri.joinPath(extensionUri, "media");
  const panel = vscode.window.createWebviewPanel(
    "repolensFlow",
    `RepoLens: flow of ${symbol}`,
    vscode.ViewColumn.Beside,
    { enableScripts: true, localResourceRoots: [mediaDir] }
  );
  const mermaidUri = panel.webview.asWebviewUri(vscode.Uri.joinPath(mediaDir, "mermaid.min.js"));
  const panZoomUri = panel.webview.asWebviewUri(vscode.Uri.joinPath(mediaDir, "svg-pan-zoom.min.js"));
  panel.webview.html = flowHtml(panel.webview, symbol, ascii, mermaid, mermaidUri, panZoomUri);
}

/** A fresh per-render nonce, required to let the inline <script> block
 * (which sets up mermaid + svg-pan-zoom) run under a strict CSP that
 * otherwise blocks inline scripts — the standard VS Code webview pattern,
 * safer than 'unsafe-inline' since it only whitelists this exact script. */
function nonce(): string {
  let text = "";
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  for (let i = 0; i < 32; i++) {
    text += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return text;
}

// Renders the flow diagram using mermaid.js + svg-pan-zoom, both vendored
// into media/ at build time (see scripts/copy-assets.js) rather than
// loaded from a CDN, so the panel works fully offline and needs no CSP
// exception for a remote host — matching RepoLens's local-first stance end
// to end, not just in the graph computation.
//
// mermaid.render() (not the startOnLoad auto-scan) is used deliberately:
// it returns a promise that resolves once the SVG actually exists, which
// is the point at which svg-pan-zoom can safely attach to it — wiring
// svg-pan-zoom to a still-rendering or not-yet-existing SVG is a race
// startOnLoad doesn't give a clean hook to avoid.
function flowHtml(webview: vscode.Webview, symbol: string, ascii: string, mermaid: string, mermaidUri: vscode.Uri, panZoomUri: vscode.Uri): string {
  const escapedAscii = ascii.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const cspNonce = nonce();
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src ${webview.cspSource} 'nonce-${cspNonce}'; style-src 'unsafe-inline';">
<style>
  /* Full-bleed layout: the graph gets the whole viewport width/height
     available to the panel, not just however wide its initial (often
     small) content happens to be — see the JS below for why the SVG's
     own width/height also have to be stripped for this to actually take
     effect once svg-pan-zoom is attached. */
  html, body { height: 100%; margin: 0; }
  body {
    font-family: var(--vscode-font-family);
    color: var(--vscode-foreground);
    padding: 0.75rem 1rem;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  pre { background: var(--vscode-textCodeBlock-background); padding: 1rem; border-radius: 6px; overflow-x: auto; }
  h2 { font-weight: 600; margin: 0.5rem 0; }
  #graphContainer {
    background: var(--vscode-textCodeBlock-background);
    border-radius: 6px;
    flex: 1 1 auto;
    min-height: 55vh;
    width: 100%;
    box-sizing: border-box;
    /* svg-pan-zoom takes over wheel/drag on the SVG itself; the
       container just needs to clip it and give it room to breathe. */
    overflow: hidden;
    position: relative;
  }
  #graphContainer svg { display: block; width: 100%; height: 100%; }
  #asciiSection { flex: 0 0 auto; max-height: 25vh; overflow-y: auto; }
  .hint { opacity: 0.65; font-size: 0.85em; margin: 0 0 0.5rem; }
</style>
<script nonce="${cspNonce}" src="${mermaidUri}"></script>
<script nonce="${cspNonce}" src="${panZoomUri}"></script>
</head>
<body>
  <h2>Call flow: ${symbol}</h2>
  <p class="hint">Scroll to zoom, drag to pan.</p>
  <div id="graphContainer"></div>
  <div id="asciiSection">
    <h2>ASCII</h2>
    <pre>${escapedAscii}</pre>
  </div>
  <script nonce="${cspNonce}">
    const graphDefinition = ${JSON.stringify(mermaid)};
    mermaid.initialize({ startOnLoad: false, theme: "dark" });
    mermaid.render("repolensFlowSvg", graphDefinition).then(({ svg }) => {
      const container = document.getElementById("graphContainer");
      container.innerHTML = svg;
      const svgEl = container.querySelector("svg");
      if (!svgEl) return;

      // Mermaid sets explicit pixel width/height (+ a matching viewBox)
      // sized to the diagram's own content — small for a small diagram.
      // Our CSS "width:100%; height:100%" only overrides how the SVG is
      // *drawn*, not the intrinsic size svg-pan-zoom measures at init, so
      // without stripping these attributes svg-pan-zoom locks its pan
      // boundaries to that original small size: zooming in then runs out
      // of diagram before it runs out of screen, which reads as the
      // diagram getting "cut off" at the edges instead of just being
      // zoomed. The viewBox (which defines the diagram's own coordinate
      // system) is left alone — only the fixed pixel sizing goes.
      svgEl.removeAttribute("width");
      svgEl.removeAttribute("height");

      const instance = svgPanZoom(svgEl, {
        zoomEnabled: true,
        controlIconsEnabled: true,
        fit: true,
        center: true,
        minZoom: 0.1,
        maxZoom: 20,
      });

      // Re-fit once layout has actually settled (fonts/flex sizing can
      // still be resolving on the very first frame) and again whenever
      // the panel is resized, so the full-bleed container size from the
      // CSS above is what svg-pan-zoom actually measures against.
      const refit = () => {
        instance.resize();
        instance.fit();
        instance.center();
      };
      requestAnimationFrame(refit);
      window.addEventListener("resize", refit);
    });
  </script>
</body>
</html>`;
}

async function runAsk() {
  const repoPath = await currentWorkspacePath();
  if (!repoPath) return;

  if (!hasProviderConfigured(extContext)) {
    const choice = await vscode.window.showInformationMessage(
      "RepoLens: 'Ask' needs an LLM provider configured (BYOK — bring your own key). This is only needed once.",
      "Configure now"
    );
    if (choice !== "Configure now") return;
    await configureProvider(extContext);
    if (!hasProviderConfigured(extContext)) return; // user cancelled the picker
  }

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
