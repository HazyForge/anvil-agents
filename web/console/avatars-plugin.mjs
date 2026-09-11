import { copyFileSync, mkdirSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const consoleDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(consoleDir, "../..");
const avatarsSrc = path.join(repoRoot, "assets/agent-avatars");

function copyAgentAvatars(destRoot) {
  const dest = path.join(destRoot, "avatars");
  mkdirSync(path.join(dest, "faces"), { recursive: true });
  for (const name of [
    "scene.json",
    "animation.json",
    "manifest.json",
    "render.js",
    "player.js",
    "preview.html",
    "README.md",
  ]) {
    copyFileSync(path.join(avatarsSrc, name), path.join(dest, name));
  }
  for (const name of readdirSync(path.join(avatarsSrc, "faces"))) {
    if (!name.endsWith(".svg")) {
      continue;
    }
    copyFileSync(path.join(avatarsSrc, "faces", name), path.join(dest, name));
    copyFileSync(path.join(avatarsSrc, "faces", name), path.join(dest, "faces", name));
  }
}

/** Vite plugin: copy the shared Anvil Agents face pack into /avatars/. */
export function anvilAgentAvatars() {
  return {
    name: "anvil-agent-avatars",
    buildStart() {
      copyAgentAvatars(path.join(consoleDir, "public"));
    },
    closeBundle() {
      copyAgentAvatars(path.join(consoleDir, "dist"));
    },
  };
}

export { avatarsSrc, repoRoot };
