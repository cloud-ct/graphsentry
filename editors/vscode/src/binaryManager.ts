// Resolves and lazily downloads the graphsentry CLI binary matching the
// user's platform, so installing the extension is enough — no separate
// `go install` step required. Binaries are pulled from the GraphSentry
// GitHub Releases (see .github/workflows/release.yml in the main repo),
// cached in the extension's global storage directory, and reused across
// sessions until the extension itself is upgraded.
import * as vscode from "vscode";
import * as fs from "fs";
import * as path from "path";
import * as https from "https";
import { IncomingMessage } from "http";

const REPO = "cloud-ct/graphsentry";
// Pinned to the CLI release this extension version was built against.
// Bump alongside editors/vscode/package.json's "version" when a new
// graphsentry CLI release should be picked up.
const CLI_RELEASE_TAG = "v0.2.0";

interface PlatformTarget {
  os: "linux" | "darwin" | "windows";
  arch: "amd64" | "arm64";
  ext: string;
}

function currentTarget(): PlatformTarget {
  const platform = process.platform;
  const arch = process.arch;

  const os = platform === "win32" ? "windows" : platform === "darwin" ? "darwin" : "linux";
  if (os !== "linux" && os !== "darwin" && os !== "windows") {
    throw new Error(`GraphSentry does not ship a binary for platform "${platform}". You can build one yourself from https://github.com/${REPO} and set the "graphsentry.binaryPath" setting.`);
  }

  const mappedArch = arch === "arm64" ? "arm64" : "amd64";
  if (arch !== "arm64" && arch !== "x64") {
    throw new Error(`GraphSentry does not ship a binary for architecture "${arch}". You can build one yourself from https://github.com/${REPO} and set the "graphsentry.binaryPath" setting.`);
  }

  return { os, arch: mappedArch, ext: os === "windows" ? ".exe" : "" };
}

function assetName(target: PlatformTarget): string {
  return `graphsentry-${target.os}-${target.arch}${target.ext}`;
}

/**
 * Returns the path to a usable graphsentry binary, downloading it into the
 * extension's global storage on first use. Honors the
 * "graphsentry.binaryPath" setting as an escape hatch for users who'd rather
 * manage their own install (e.g. via `go install`).
 */
export async function getBinaryPath(context: vscode.ExtensionContext): Promise<string> {
  const override = vscode.workspace.getConfiguration("graphsentry").get<string>("binaryPath");
  if (override && override.trim().length > 0) {
    return override.trim();
  }

  const target = currentTarget();
  const storageDir = path.join(context.globalStorageUri.fsPath, "bin");
  const destPath = path.join(storageDir, `graphsentry-${CLI_RELEASE_TAG}${target.ext}`);

  if (fs.existsSync(destPath)) {
    return destPath;
  }

  await fs.promises.mkdir(storageDir, { recursive: true });

  const url = `https://github.com/${REPO}/releases/download/${CLI_RELEASE_TAG}/${assetName(target)}`;
  await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: "GraphSentry: downloading the graphsentry CLI binary...",
      cancellable: false,
    },
    () => downloadFile(url, destPath)
  );

  if (target.os !== "windows") {
    await fs.promises.chmod(destPath, 0o755);
  }

  return destPath;
}

function downloadFile(url: string, destPath: string, redirectsLeft = 5): Promise<void> {
  return new Promise((resolve, reject) => {
    const tmpPath = destPath + ".download";
    const file = fs.createWriteStream(tmpPath);

    const request = https.get(url, { headers: { "User-Agent": "graphsentry-vscode-extension" } }, (res: IncomingMessage) => {
      // GitHub Releases assets are served via a redirect to S3.
      if (res.statusCode && res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        file.close();
        fs.unlink(tmpPath, () => {
          if (redirectsLeft <= 0) {
            reject(new Error("Too many redirects downloading the graphsentry binary"));
            return;
          }
          downloadFile(res.headers.location as string, destPath, redirectsLeft - 1).then(resolve, reject);
        });
        return;
      }
      if (res.statusCode !== 200) {
        file.close();
        fs.unlink(tmpPath, () => {
          reject(new Error(`Failed to download graphsentry binary: HTTP ${res.statusCode} from ${url}`));
        });
        return;
      }
      res.pipe(file);
      file.on("finish", () => {
        file.close(() => {
          fs.rename(tmpPath, destPath, (err) => (err ? reject(err) : resolve()));
        });
      });
    });

    request.on("error", (err) => {
      file.close();
      fs.unlink(tmpPath, () => reject(err));
    });
  });
}
