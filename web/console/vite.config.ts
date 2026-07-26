import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

// Local dev: Vite on :5173, proxy API to anvil-agents-api (default :8082).
// Production: same-origin relative /api/v1 under anvil-agents-api.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const proxyTarget = env.VITE_DEV_API_PROXY || "http://127.0.0.1:8082";
  return {
    plugins: [react()],
    base: "/",
    server: {
      port: 5173,
      proxy: {
        "/api": {
          target: proxyTarget,
          changeOrigin: true,
        },
        "/healthz": {
          target: proxyTarget,
          changeOrigin: true,
        },
        "/readyz": {
          target: proxyTarget,
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
      // Do not ship .map files on the API host (token-bearing operator console).
      sourcemap: false,
    },
  };
});
