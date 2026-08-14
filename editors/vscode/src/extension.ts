import * as vscode from "vscode";
import { RepoLensClient, RepoLensError, displayName, FlowResult } from "./repolens";
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

  showFlowPanel(repoPath, symbolLabel(symbol), result);
}

function showFlowPanel(repoPath: string, symbol: string, result: FlowResult) {
  const mediaDir = vscode.Uri.joinPath(extensionUri, "media");
  const panel = vscode.window.createWebviewPanel(
    "repolensFlow",
    `RepoLens: flow of ${symbol}`,
    vscode.ViewColumn.Beside,
    { enableScripts: true, localResourceRoots: [mediaDir] }
  );
  const mermaidUri = panel.webview.asWebviewUri(vscode.Uri.joinPath(mediaDir, "mermaid.min.js"));
  const panZoomUri = panel.webview.asWebviewUri(vscode.Uri.joinPath(mediaDir, "svg-pan-zoom.min.js"));
  panel.webview.html = flowHtml(panel.webview, symbol, result, mermaidUri, panZoomUri);

  panel.webview.onDidReceiveMessage(async (msg) => {
    switch (msg.command) {
      case "open":
        await openNode(repoPath, { file: msg.file, start_line: msg.line });
        return;
      case "ask": {
        if (!hasProviderConfigured(extContext)) {
          panel.webview.postMessage({
            command: "askError",
            message: "No LLM provider configured yet. Run 'RepoLens: Configure LLM Provider' from the command palette, then ask again.",
          });
          return;
        }
        // Scope the question to this flow's root so the CLI's relevance
        // matching (which works from the question text) is biased toward
        // the subgraph already on screen, instead of the whole repo.
        const contextualized = `Regarding the call flow starting at ${symbol}: ${msg.question}`;
        try {
          const answer = await client.ask(repoPath, contextualized);
          panel.webview.postMessage({ command: "askResult", answer });
        } catch (err) {
          const message = err instanceof RepoLensError ? err.message : String(err);
          panel.webview.postMessage({ command: "askError", message });
        }
        return;
      }
    }
  });
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
//
// The tree below the diagram is rendered client-side from result.paths
// (not the CLI's plain-text ASCII string) specifically so each node can
// carry its file/line and be Ctrl/Cmd+clickable — jumping to source the
// same way the QuickPicks elsewhere in the extension do.
function flowHtml(webview: vscode.Webview, symbol: string, result: FlowResult, mermaidUri: vscode.Uri, panZoomUri: vscode.Uri): string {
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
  pre, #tree { background: var(--vscode-textCodeBlock-background); padding: 1rem; border-radius: 6px; overflow: auto; }
  h2 { font-weight: 600; margin: 0.5rem 0; }
  #graphContainer {
    background: var(--vscode-textCodeBlock-background);
    border-radius: 6px;
    flex: 1 1 auto;
    min-height: 45vh;
    width: 100%;
    box-sizing: border-box;
    /* svg-pan-zoom takes over wheel/drag on the SVG itself; the
       container just needs to clip it and give it room to breathe. */
    overflow: hidden;
    position: relative;
  }
  #graphContainer svg { display: block; width: 100%; height: 100%; }
  #treeSection { flex: 0 0 auto; max-height: 25vh; overflow-y: auto; font-family: var(--vscode-editor-font-family, monospace); font-size: 0.9em; }
  .hint { opacity: 0.65; font-size: 0.85em; margin: 0 0 0.5rem; }
  .node-line { white-space: pre; }
  .node-link { cursor: pointer; color: var(--vscode-textLink-foreground); }
  .node-link:hover { text-decoration: underline; }
  #askSection { flex: 0 0 auto; margin-top: 0.75rem; }
  #askInput { width: 100%; box-sizing: border-box; background: var(--vscode-input-background); color: var(--vscode-input-foreground); border: 1px solid var(--vscode-input-border, transparent); border-radius: 4px; padding: 0.5rem; font-family: inherit; resize: vertical; }
  #askButton { margin-top: 0.4rem; background: var(--vscode-button-background); color: var(--vscode-button-foreground); border: none; border-radius: 4px; padding: 0.4rem 0.9rem; cursor: pointer; }
  #askButton:hover { background: var(--vscode-button-hoverBackground); }
  #askButton:disabled { opacity: 0.6; cursor: default; }
  #askAnswer { margin-top: 0.6rem; white-space: pre-wrap; }
  #askAnswer:empty { display: none; }
</style>
<script nonce="${cspNonce}" src="${mermaidUri}"></script>
<script nonce="${cspNonce}" src="${panZoomUri}"></script>
</head>
<body>
  <h2>Call flow: ${symbol}</h2>
  <p class="hint">Scroll to zoom, drag to pan.</p>
  <div id="graphContainer"></div>

  <div id="treeSection">
    <h2>Call tree</h2>
    <p class="hint">Ctrl/Cmd+click a name to open it.</p>
    <div id="tree"></div>
  </div>

  <div id="askSection">
    <h2>Ask about this flow</h2>
    <textarea id="askInput" rows="2" placeholder="e.g. why does this call GetUserId three times?"></textarea>
    <br>
    <button id="askButton">Ask</button>
    <div id="askAnswer"></div>
  </div>

  <script nonce="${cspNonce}">
    const vscodeApi = acquireVsCodeApi();
    const paths = ${JSON.stringify(result.paths)};
    const graphDefinition = ${JSON.stringify(result.mermaid)};

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

    // Builds the same merged tree as the Go CLI's ASCII rendering
    // (internal/diagram.treeFromPaths): paths sharing a prefix collapse
    // into one branch, keyed by (edge kind, node id) so two different
    // symbols that happen to share a bare name never merge into one.
    function buildTree(paths) {
      const root = { label: paths[0][0].node.qualified || paths[0][0].node.name, node: paths[0][0].node, children: [], childIndex: new Map() };
      for (const path of paths) {
        let cur = root;
        for (let i = 1; i < path.length; i++) {
          const step = path[i];
          const key = step.via + "::" + step.node.id;
          let next = cur.childIndex.get(key);
          if (!next) {
            const name = step.node.qualified || step.node.name;
            const label = step.via && step.via !== "calls" ? "[" + step.via + "] " + name : name;
            next = { label, node: step.node, children: [], childIndex: new Map() };
            cur.childIndex.set(key, next);
            cur.children.push(next);
          }
          cur = next;
        }
      }
      return root;
    }

    function nodeSpan(n) {
      const span = document.createElement("span");
      span.className = "node-link";
      span.textContent = n.label;
      span.title = n.node.file ? n.node.file + ":" + n.node.start_line : "";
      span.addEventListener("click", (e) => {
        if (!e.ctrlKey && !e.metaKey) return;
        if (!n.node.file) return;
        vscodeApi.postMessage({ command: "open", file: n.node.file, line: n.node.start_line });
      });
      return span;
    }

    function renderTree(root) {
      const container = document.getElementById("tree");
      const rootLine = document.createElement("div");
      rootLine.className = "node-line";
      rootLine.appendChild(nodeSpan(root));
      container.appendChild(rootLine);
      renderChildren(root.children, "", container);
    }

    function renderChildren(children, prefix, container) {
      children.forEach((c, i) => {
        const last = i === children.length - 1;
        const connector = last ? "└─► " : "├─► ";
        const nextPrefix = prefix + (last ? "     " : "│    ");

        const line = document.createElement("div");
        line.className = "node-line";
        line.appendChild(document.createTextNode(prefix + connector));
        line.appendChild(nodeSpan(c));
        container.appendChild(line);

        renderChildren(c.children, nextPrefix, container);
      });
    }

    if (paths.length > 0) {
      renderTree(buildTree(paths));
    } else {
      document.getElementById("tree").textContent = "(no outgoing calls found)";
    }

    const askButton = document.getElementById("askButton");
    const askInput = document.getElementById("askInput");
    const askAnswer = document.getElementById("askAnswer");

    askButton.addEventListener("click", () => {
      const question = askInput.value.trim();
      if (!question) return;
      askButton.disabled = true;
      askAnswer.textContent = "Thinking...";
      vscodeApi.postMessage({ command: "ask", question });
    });

    window.addEventListener("message", (event) => {
      const msg = event.data;
      if (msg.command === "askResult") {
        askAnswer.textContent = msg.answer;
        askButton.disabled = false;
      } else if (msg.command === "askError") {
        askAnswer.textContent = "Error: " + msg.message;
        askButton.disabled = false;
      }
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
