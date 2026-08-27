// Thin wrapper around the graphsentry CLI: runs it as a child process and
// parses its --json output. The extension never reimplements graph logic
// — every deterministic answer (impact, coupling, flow) comes straight
// from the Go binary, keeping a single source of truth between the CLI
// and the editor integration.
import * as vscode from "vscode";
import { execFile, spawn } from "child_process";
import { getBinaryPath } from "./binaryManager";
import { resolveProviderEnv } from "./config";

export interface GraphNode {
  id: string;
  kind: string;
  name: string;
  qualified: string; // class/type-scoped name, e.g. "AppService.GetAllAsync" — prefer this over name for display
  file: string;
  language: string;
  start_line: number;
  end_line: number;
  signature?: string;
}

/** Prefers a node's class/type-scoped name over its bare name — two
 * different symbols in different classes/layers can share a bare method
 * name (a service delegating to a repository method of the same name is
 * common), which would otherwise look like a node calling itself. */
export function displayName(node: GraphNode): string {
  return node.qualified || node.name;
}

export interface ImpactedNode {
  node: GraphNode;
  distance: number;
  via: string;
}

export interface ImpactResult {
  root: string;
  impacted: ImpactedNode[];
}

export interface CouplingScore {
  node: GraphNode;
  fan_in: number;
  fan_out: number;
  total: number;
}

export interface PathStep {
  node: GraphNode;
  via: string;
}

export interface FlowResult {
  root: string;
  paths: PathStep[][];
  ascii: string;
  mermaid: string;
}

/** Mirrors internal/security.Status — see that type's doc comment for what
 * each value means. */
export type AuthStatus = "protected" | "public" | "unprotected" | "unknown";

export interface GuardMatch {
  endpoint_id: string;
  guard_name: string;
  file: string;
  line: number;
  public: boolean;
}

/** Mirrors internal/security.EndpointFinding. */
export interface EndpointFinding {
  endpoint: GraphNode;
  status: AuthStatus;
  guards?: GuardMatch[];
}

export class GraphSentryError extends Error {}

export class GraphSentryClient {
  constructor(private context: vscode.ExtensionContext) {}

  /** Runs `graphsentry analyze <path>` for the given workspace folder. */
  async analyze(repoPath: string): Promise<void> {
    await this.run(["analyze", repoPath]);
  }

  async coupling(repoPath: string, top = 15): Promise<CouplingScore[]> {
    const out = await this.run(["coupling", "--repo", repoPath, "--top", String(top), "--json"]);
    return JSON.parse(out) as CouplingScore[];
  }

  async impact(repoPath: string, symbol: string): Promise<ImpactResult> {
    const out = await this.run(["impact", symbol, "--repo", repoPath, "--json"]);
    return JSON.parse(out) as ImpactResult;
  }

  /** Mirror image of impact(): what the symbol itself depends on, not what
   * depends on it. Separate CLI command (`dependencies`) and CodeLens
   * entry from impact's — see codeLensProvider.ts's two-lens split. */
  async dependencies(repoPath: string, symbol: string): Promise<ImpactResult> {
    const out = await this.run(["dependencies", symbol, "--repo", repoPath, "--json"]);
    return JSON.parse(out) as ImpactResult;
  }

  async flow(repoPath: string, symbol: string, depth = 5): Promise<FlowResult> {
    const out = await this.run(["flow", symbol, "--repo", repoPath, "--depth", String(depth), "--json"]);
    return JSON.parse(out) as FlowResult;
  }

  /** Runs `graphsentry security endpoints --json` — see internal/security's
   * package doc for what this reports (and doesn't: a structural signal,
   * not a vulnerability scan). */
  async securityEndpoints(repoPath: string): Promise<EndpointFinding[]> {
    const out = await this.run(["security", "endpoints", "--repo", repoPath, "--json"]);
    return (JSON.parse(out) as EndpointFinding[] | null) ?? [];
  }

  /** Streams `graphsentry ask --stream`, invoking onDelta with each raw text
   * chunk as the model generates it, so the caller can render live instead
   * of waiting for the full answer. `root`, when given, scopes the
   * question to that symbol's subgraph directly (see the CLI's `--root`)
   * rather than letting the CLI's own repo-wide keyword search pick a
   * (possibly unrelated) one — this is what lets a question asked from
   * the flow panel stay about the flow that's actually on screen. */
  async askStream(repoPath: string, question: string, root: string | undefined, onDelta: (chunk: string) => void): Promise<void> {
    const bin = await getBinaryPath(this.context);
    const providerEnv = await resolveProviderEnv(this.context);
    const args = ["ask", question, "--repo", repoPath, "--stream"];
    if (root) args.push("--root", root);

    return new Promise((resolve, reject) => {
      const child = spawn(bin, args, { env: { ...process.env, ...providerEnv } });
      let stderr = "";
      child.stdout.on("data", (buf: Buffer) => onDelta(buf.toString("utf8")));
      child.stderr.on("data", (buf: Buffer) => {
        stderr += buf.toString("utf8");
      });
      child.on("error", (err) => reject(new GraphSentryError(err.message)));
      child.on("close", (code) => {
        if (code !== 0) {
          reject(new GraphSentryError(stderr.trim() || `graphsentry ask exited with code ${code}`));
          return;
        }
        resolve();
      });
    });
  }

  private async run(args: string[]): Promise<string> {
    const bin = await getBinaryPath(this.context);
    // Provider env vars (GRAPHSENTRY_PROVIDER + the matching key/host) come
    // from the extension's own config (SecretStorage-backed — see
    // config.ts), not from the user's shell environment, so "Ask" works
    // the same whether or not graphsentry was ever configured outside VS
    // Code. Harmless to pass on every command, not just ask.
    const providerEnv = await resolveProviderEnv(this.context);
    return new Promise((resolve, reject) => {
      execFile(bin, args, { maxBuffer: 1024 * 1024 * 64, env: { ...process.env, ...providerEnv } }, (err, stdout, stderr) => {
        if (err) {
          const message = stderr.trim() || err.message;
          reject(new GraphSentryError(message));
          return;
        }
        resolve(stdout);
      });
    });
  }
}
