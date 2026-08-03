import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const siteRoot = path.dirname(fileURLToPath(import.meta.url));
const repoDocs = path.resolve(siteRoot, "../../docs");

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@repo-docs": repoDocs,
    },
  },
  server: {
    port: 5174,
    host: "127.0.0.1",
    fs: {
      // Allow importing markdown from the monorepo docs/ tree.
      allow: [siteRoot, repoDocs],
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    assetsInlineLimit: 4096,
  },
});
