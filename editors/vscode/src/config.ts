// Manages the BYOK LLM provider configuration for "Ask": which provider,
// and its API key. Keys are stored via VS Code's SecretStorage API (OS
// keychain-backed), never in settings.json — settings.json is plaintext,
// gets synced, and sometimes ends up committed to a repo by accident.
// Non-secret configuration (which provider, the Ollama host) lives in
// globalState instead, since it isn't sensitive but also isn't the kind of
// per-project setting workspace settings.json is meant for.
import * as vscode from "vscode";

const PROVIDER_KEY = "repolens.provider";
const OLLAMA_HOST_KEY = "repolens.ollamaHost";
const ANTHROPIC_SECRET = "repolens.anthropicApiKey";
const OPENAI_SECRET = "repolens.openaiApiKey";

interface ProviderChoice {
  label: string;
  description: string;
  value: "anthropic" | "openai" | "ollama";
}

const PROVIDERS: ProviderChoice[] = [
  { label: "Anthropic", description: "Claude — console.anthropic.com/settings/keys", value: "anthropic" },
  { label: "OpenAI", description: "platform.openai.com/api-keys", value: "openai" },
  { label: "Ollama (100% local)", description: "no key needed — just the host, default http://localhost:11434", value: "ollama" },
];

/** Prompts the user for a provider and its key/host, storing the key in
 * SecretStorage (or the host in globalState for Ollama). Invoked by the
 * "RepoLens: Configure LLM Provider" command. */
export async function configureProvider(context: vscode.ExtensionContext): Promise<void> {
  const picked = await vscode.window.showQuickPick(
    PROVIDERS.map((p) => ({ label: p.label, description: p.description, provider: p })),
    { title: "RepoLens: choose an LLM provider for 'Ask'", ignoreFocusOut: true }
  );
  if (!picked) return;
  const provider = picked.provider;

  if (provider.value === "ollama") {
    const host = await vscode.window.showInputBox({
      title: "Ollama host",
      value: context.globalState.get<string>(OLLAMA_HOST_KEY) ?? "http://localhost:11434",
      ignoreFocusOut: true,
    });
    if (host === undefined) return;
    await context.globalState.update(OLLAMA_HOST_KEY, host);
    await context.globalState.update(PROVIDER_KEY, "ollama");
    vscode.window.showInformationMessage("RepoLens: configured to use Ollama.");
    return;
  }

  const secretKey = provider.value === "anthropic" ? ANTHROPIC_SECRET : OPENAI_SECRET;
  const key = await vscode.window.showInputBox({
    title: `${provider.label} API key`,
    password: true,
    placeHolder: "sk-...",
    ignoreFocusOut: true,
  });
  if (!key) return;

  await context.secrets.store(secretKey, key);
  await context.globalState.update(PROVIDER_KEY, provider.value);
  vscode.window.showInformationMessage(`RepoLens: ${provider.label} configured. The key is stored in VS Code's Secret Storage, not in settings.json.`);
}

/** Removes the stored provider/key configuration. */
export async function clearProvider(context: vscode.ExtensionContext): Promise<void> {
  await context.secrets.delete(ANTHROPIC_SECRET);
  await context.secrets.delete(OPENAI_SECRET);
  await context.globalState.update(PROVIDER_KEY, undefined);
  await context.globalState.update(OLLAMA_HOST_KEY, undefined);
  vscode.window.showInformationMessage("RepoLens: LLM provider configuration cleared.");
}

/** Resolves the environment variables to pass to the repolens CLI
 * subprocess for the current provider configuration — REPOLENS_PROVIDER
 * plus whichever key/host variable that provider needs. Returns an empty
 * object if nothing is configured (deterministic commands don't need
 * this; "ask" will surface the CLI's own "no provider configured"
 * error, which already explains how to fix it). */
export async function resolveProviderEnv(context: vscode.ExtensionContext): Promise<NodeJS.ProcessEnv> {
  const provider = context.globalState.get<string>(PROVIDER_KEY);
  if (!provider) return {};

  const env: NodeJS.ProcessEnv = { REPOLENS_PROVIDER: provider };
  switch (provider) {
    case "anthropic": {
      const key = await context.secrets.get(ANTHROPIC_SECRET);
      if (key) env.ANTHROPIC_API_KEY = key;
      break;
    }
    case "openai": {
      const key = await context.secrets.get(OPENAI_SECRET);
      if (key) env.OPENAI_API_KEY = key;
      break;
    }
    case "ollama":
      env.OLLAMA_HOST = context.globalState.get<string>(OLLAMA_HOST_KEY) ?? "http://localhost:11434";
      break;
  }
  return env;
}

/** Whether a provider has been configured — used to show the right hint
 * ("Configure API Key" vs. just asking the question) before running Ask. */
export function hasProviderConfigured(context: vscode.ExtensionContext): boolean {
  return !!context.globalState.get<string>(PROVIDER_KEY);
}
