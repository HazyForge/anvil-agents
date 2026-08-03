import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    sourcemap: true,
    assetsInlineLimit: 4096,
  },
  server: {
    port: 5174,
    host: "127.0.0.1",
  },
});
