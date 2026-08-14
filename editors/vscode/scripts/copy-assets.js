// Vendors third-party browser bundles into media/ so the flow-diagram
// webview can load them as local resources instead of from a CDN. Runs as
// part of the build (see package.json scripts) rather than being checked
// into git — these are regular devDependencies, so `npm install`/`npm ci`
// always has the source files available to copy.
const fs = require("fs");
const path = require("path");

const destDir = path.join(__dirname, "..", "media");
fs.mkdirSync(destDir, { recursive: true });

const assets = [
  ["mermaid", "dist/mermaid.min.js", "mermaid.min.js"],
  ["svg-pan-zoom", "dist/svg-pan-zoom.min.js", "svg-pan-zoom.min.js"],
  ["marked", "lib/marked.umd.js", "marked.js"],
];

for (const [pkg, relPath, destName] of assets) {
  const src = path.join(__dirname, "..", "node_modules", pkg, relPath);
  const dest = path.join(destDir, destName);
  fs.copyFileSync(src, dest);
  console.log(`Copied ${src} -> ${dest}`);
}
