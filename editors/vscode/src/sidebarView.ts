// A simple command-shortcut list in GraphSentry's own Activity Bar view
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

// "Show Call Flow" and "Ask a Question" are deliberately absent here: both
// need a specific symbol/context to be useful, which this list — a
// context-free set of shortcuts — can't supply. The CodeLens above each
// analyzed symbol opens flow (and, from there, a context-scoped Ask box)
// directly; a generic entry point here would just re-prompt for what the
// CodeLens click already knows.
const ENTRIES: CommandEntry[] = [
  { label: "Analyze Workspace", description: "Build/refresh the code graph", command: "graphsentry.analyze", icon: "sync" },
  { label: "Most Coupled Symbols", description: "Fan-in + fan-out ranking", command: "graphsentry.coupling", icon: "graph" },
  { label: "Impact Analysis", description: "Symbol at cursor", command: "graphsentry.impact", icon: "references" },
  { label: "Configure LLM Provider", description: "API key for Ask", command: "graphsentry.configureProvider", icon: "key" },
];

class CommandTreeItem extends vscode.TreeItem {
  constructor(entry: CommandEntry) {
    super(entry.label, vscode.TreeItemCollapsibleState.None);
    this.description = entry.description;
    this.iconPath = new vscode.ThemeIcon(entry.icon);
    this.command = { command: entry.command, title: entry.label };
  }
}

export class GraphSentrySidebarProvider implements vscode.TreeDataProvider<CommandTreeItem> {
  getTreeItem(element: CommandTreeItem): vscode.TreeItem {
    return element;
  }

  getChildren(): CommandTreeItem[] {
    return ENTRIES.map((e) => new CommandTreeItem(e));
  }
}
