// Thin wrapper around the repolens CLI: runs it as a child process and
// parses its --json output. The extension never reimplements graph logic
// — every deterministic answer (impact, coupling, flow) comes straight
// from the Go binary, keeping a single source of truth between the CLI
// and the editor integration.
import * as vscode from "vscode";
import { execFile } from "child_process";
import { getBinaryPath } from "./binaryManager";
import { resolveProviderEnv } from "./config";

export interface GraphNode {
  id: string;
  kind: string;
  name: string;
  file: string;
  language: string;
  start_line: number;
  end_line: number;
  signature?: string;
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

export class RepoLensError extends Error {}

export class RepoLensClient {
  constructor(private context: vscode.ExtensionContext) {}

  /** Runs `repolens analyze <path>` for the given workspace folder. */
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

  async flow(repoPath: string, symbol: string, depth = 5): Promise<FlowResult> {
    const out = await this.run(["flow", symbol, "--repo", repoPath, "--depth", String(depth), "--json"]);
    return JSON.parse(out) as FlowResult;
  }

  async ask(repoPath: string, question: string): Promise<string> {
    return this.run(["ask", question, "--repo", repoPath]);
  }

  private async run(args: string[]): Promise<string> {
    const bin = await getBinaryPath(this.context);
    // Provider env vars (REPOLENS_PROVIDER + the matching key/host) come
    // from the extension's own config (SecretStorage-backed — see
    // config.ts), not from the user's shell environment, so "Ask" works
    // the same whether or not repolens was ever configured outside VS
    // Code. Harmless to pass on every command, not just ask.
    const providerEnv = await resolveProviderEnv(this.context);
    return new Promise((resolve, reject) => {
      execFile(bin, args, { maxBuffer: 1024 * 1024 * 64, env: { ...process.env, ...providerEnv } }, (err, stdout, stderr) => {
        if (err) {
          const message = stderr.trim() || err.message;
          reject(new RepoLensError(message));
          return;
        }
        resolve(stdout);
      });
    });
  }
}
