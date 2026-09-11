#!/usr/bin/env node
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { renderFace } from "./render.js";
import scene from "./scene.json" with { type: "json" };

const root = dirname(fileURLToPath(import.meta.url));
const outDir = join(root, "faces");
mkdirSync(outDir, { recursive: true });

if (!Array.isArray(scene.faces) || scene.faces.length === 0) {
  throw new Error("scene.json has no faces");
}

for (const face of scene.faces) {
  const svg = `${renderFace(face, scene)}\n`;
  writeFileSync(join(outDir, `${face.id}.svg`), svg);
}

writeFileSync(
  join(root, "manifest.json"),
  `${JSON.stringify(
    {
      version: scene.version,
      product: scene.product,
      viewBox: scene.viewBox,
      faces: scene.faces.map((face) => ({
        id: face.id,
        label: face.label,
        file: `faces/${face.id}.svg`,
        publicPath: `/avatars/${face.id}.svg`,
        storedValue: face.id,
        legacy: face.legacy ?? [],
      })),
    },
    null,
    2,
  )}\n`,
);
