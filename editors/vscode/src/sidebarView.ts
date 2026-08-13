// A simple command-shortcut list in RepoLens's own Activity Bar view
// container, so the common actions are one click away instead of requiring
// the command palette (Ctrl+Shift+P). Deliberately just a flat TreeView of
// clickable items — no state, no expansion — since every action here is a
// one-shot command, not a hierarchy to browse.
import * as vscode from "vscode";

interface CommandEntry {
  label: string;
  description: string;
  command: string;
  icon: string;
}

const ENTRIES: CommandEntry[] = [
  { label: "Analyze Workspace", description: "Build/refresh the code graph", command: "repolens.analyze", icon: "sync" },
  { label: "Most Coupled Symbols", description: "Fan-in + fan-out ranking", command: "repolens.coupling", icon: "graph" },
  { label: "Impact Analysis", description: "Symbol at cursor", command: "repolens.impact", icon: "references" },
  { label: "Show Call Flow", description: "Symbol at cursor", command: "repolens.flow", icon: "arrow-swap" },
  { label: "Ask a Question", description: "Natural-language Q&A (BYOK)", command: "repolens.ask", icon: "comment-discussion" },
  { label: "Configure LLM Provider", description: "API key for Ask", command: "repolens.configureProvider", icon: "key" },
];

class CommandTreeItem extends vscode.TreeItem {
  constructor(entry: CommandEntry) {
    super(entry.label, vscode.TreeItemCollapsibleState.None);
    this.description = entry.description;
    this.iconPath = new vscode.ThemeIcon(entry.icon);
    this.command = { command: entry.command, title: entry.label };
  }
}

export class RepoLensSidebarProvider implements vscode.TreeDataProvider<CommandTreeItem> {
  getTreeItem(element: CommandTreeItem): vscode.TreeItem {
    return element;
  }

  getChildren(): CommandTreeItem[] {
    return ENTRIES.map((e) => new CommandTreeItem(e));
  }
}
