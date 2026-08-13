// Vendors mermaid's browser bundle into media/ so the flow-diagram webview
// can load it as a local resource instead of from a CDN. Runs as part of
// the build (see package.json scripts) rather than being checked into
// git — mermaid is a regular dependency, so `npm install`/`npm ci` always
// has the source file available to copy.
const fs = require("fs");
const path = require("path");

const src = path.join(__dirname, "..", "node_modules", "mermaid", "dist", "mermaid.min.js");
const destDir = path.join(__dirname, "..", "media");
const dest = path.join(destDir, "mermaid.min.js");

fs.mkdirSync(destDir, { recursive: true });
fs.copyFileSync(src, dest);
console.log(`Copied ${src} -> ${dest}`);
