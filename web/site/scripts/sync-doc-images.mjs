import { chmodSync, cpSync, existsSync, mkdirSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const siteRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const src = path.resolve(siteRoot, "../../docs/images");
const dest = path.resolve(siteRoot, "public/docs");

if (!existsSync(src)) {
  console.warn(`sync-doc-images: missing ${src}`);
  process.exit(0);
}

mkdirSync(dest, { recursive: true });
for (const name of readdirSync(src)) {
  if (!/\.(png|jpe?g|webp|gif|svg)$/i.test(name)) continue;
  const from = path.join(src, name);
  const to = path.join(dest, name);
  cpSync(from, to);
  // Ensure nginx (non-root) can read assets packaged into the image.
  try {
    chmodSync(to, 0o644);
  } catch {
    // best-effort on exotic FS
  }
}
console.log(`sync-doc-images: copied images → ${dest}`);
